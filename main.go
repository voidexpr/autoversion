package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultMaxFileSizeMB = 10
	defaultBufferTime    = 3
)

var (
	// Global flags
	sourceFlag string
	destFlag   string
	debug      bool
	noGit      bool
	useRestic  bool
	errLogFlag string

	// Command-specific flags
	maxFileSize     int64
	bufferTime      int
	dryRun          bool
	reportOnly      bool
	noLogin         bool
	logDirFlag      string
	forceNewSource  bool
	forceObliterate bool
	includeExtsFlag string
	excludeDirsFlag []string
)

// requirePaths returns an error unless --source, and --destination when
// needDest is set, were given
func requirePaths(needDest bool) error {
	if sourceFlag == "" {
		return fmt.Errorf("--source is required")
	}
	if needDest && destFlag == "" {
		return fmt.Errorf("--destination is required")
	}
	return nil
}

// isDirEmpty returns true if the directory exists and contains no entries
func isDirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

func hasPathPrefix(path, prefix string) bool {
	return strings.HasPrefix(
		path+string(os.PathSeparator),
		prefix+string(os.PathSeparator),
	)
}

// verifyPaths verifies that source and destination paths are safe to use
func verifyPaths(absSourcePath, absDestPath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	// Check if paths are HOME or /
	dangerousPaths := []struct {
		path string
		name string
	}{
		{absSourcePath, "source"},
		{absDestPath, "destination"},
	}

	for _, p := range dangerousPaths {
		if p.path == homeDir {
			return fmt.Errorf("%s path cannot be HOME directory (%s)", p.name, homeDir)
		}
		if p.path == "/" {
			return fmt.Errorf("%s path cannot be root directory (/)", p.name)
		}
	}

	// Check exact match
	if absSourcePath == absDestPath {
		return fmt.Errorf("source and destination paths cannot be the same (%s)", absSourcePath)
	}

	// Check if one path contains the other
	if hasPathPrefix(absSourcePath, absDestPath) {
		return fmt.Errorf("source path (%s) cannot be inside destination path (%s)", absSourcePath, absDestPath)
	}
	if hasPathPrefix(absDestPath, absSourcePath) {
		return fmt.Errorf("destination path (%s) cannot be inside source path (%s)", absDestPath, absSourcePath)
	}

	return nil
}

// validateSourcePath checks that source path exists and is a directory
func validateSourcePath(sourcePath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("source path error: %v", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source path must be a directory: %s", sourcePath)
	}
	return nil
}

// resolveSourcePath validates the source directory and returns its absolute path with symlinks resolved
func resolveSourcePath(sourcePath string) (string, error) {
	if err := validateSourcePath(sourcePath); err != nil {
		return "", err
	}
	absSourcePath, err := resolvePath(sourcePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve source path: %v", err)
	}
	return absSourcePath, nil
}

// resolvePath returns the absolute path with symlinks resolved. For a path
// that doesn't exist yet, its nearest existing parent is resolved, so the
// result is the same before and after the path is created.
func resolvePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	existing, rest := absPath, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to stat %s: %v", existing, err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return absPath, nil
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}

	realPath, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks: %v", err)
	}

	return filepath.Join(realPath, rest), nil
}

// findInParents returns the first parent directory of path that contains name
func findInParents(path, name string) (string, bool) {
	current := filepath.Dir(path) // start from parent, not the path itself
	for current != "/" && current != "." {
		if _, err := os.Lstat(filepath.Join(current, name)); err == nil {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", false
}

var rootCmd = &cobra.Command{
	Use:          "autoversion",
	Short:        "Track file versions in real-time using git",
	SilenceUsage: true,
	Long: `autoversion - track file versions in real-time using git

Automatically tracks changes to files and commits them to git repositories,
making it easy to see what changed and restore files to any point in time.`,
}

var filterCmd = &cobra.Command{
	Use:   "filter",
	Short: "List files that would be tracked",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requirePaths(false); err != nil {
			return err
		}

		absSourcePath, err := resolveSourcePath(sourceFlag)
		if err != nil {
			return err
		}

		SetSourceRoot(absSourcePath)

		// Walk and print matching files
		filepath.Walk(absSourcePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
				return nil
			}

			if info.IsDir() {
				if IsExcludedDir(path) {
					return filepath.SkipDir
				}
				return nil
			}

			// Skip all symlinks (both file and directory symlinks)
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}

			if shouldTrack, _ := ShouldTrack(path, maxFileSize, false); shouldTrack {
				if relPath, err := filepath.Rel(absSourcePath, path); err == nil {
					fmt.Println(relPath)
				}
			}

			return nil
		})

		return nil
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a destination for a source",
	Long: `Creates the destination if needed, initializes git (unless --no-git) and
restic (with --restic), and writes the .autoversion marker naming the source.
Commands that copy files (sync, watch, install) refuse a destination without
a marker naming their source.

The destination must be missing, empty, or already initialized for the source:
  --force-new-source                use a non-empty destination, or one whose marker
                                    names another source, keeping its contents and
                                    git history. The next sync makes it match the
                                    source.
  --force-obliterate-yes-i-am-sure  remove everything in the destination, .git
                                    included, then initialize it`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requirePaths(true); err != nil {
			return err
		}

		result, err := ValidateSourceAndDestination(sourceFlag, destFlag)
		if err != nil {
			return err
		}
		if len(result.Errors) > 0 {
			return fmt.Errorf("path validation failed: %s", result.Errors[0])
		}

		return initDestination(result.SourceResolved, result.DestinationResolved, forceNewSource, forceObliterate)
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync source directory to destination",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requirePaths(true); err != nil {
			return err
		}

		absSourcePath, destPath, err := validatePaths(sourceFlag, destFlag)
		if err != nil {
			return err
		}

		SetSourceRoot(absSourcePath)

		// Sync files
		return scanPaths(absSourcePath, destPath, maxFileSize, dryRun)
	},
}

// logEvent prints a watcher event. Filtered-out events are only printed with
// --debug or --report-only, they are most of the events on a busy source.
func logEvent(eventType EventType, path string, tracked bool) {
	if !tracked && !debug && !reportOnly {
		return
	}
	var opName string
	switch eventType {
	case EventCreated:
		opName = "created "
	case EventModified:
		opName = "modified"
	case EventDeleted:
		opName = "deleted "
	case EventDirChanged:
		opName = "dir     "
	}
	switch {
	case !tracked:
		logSkip("[%s] event skip   %s %s \n", nowTS(), opName, path)
	case reportOnly:
		fmt.Printf("[%s] event track  %s %s \n", nowTS(), opName, path)
	default:
		fmt.Printf("[%s] event BUFFER %s %s \n", nowTS(), opName, path)
	}
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch for changes in real-time",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if logDirFlag != "" {
			redirectLogs(logDirFlag)
		}

		if err := requirePaths(!reportOnly); err != nil {
			return err
		}

		var absSourcePath, destPath string
		var err error
		if reportOnly {
			absSourcePath, err = resolveSourcePath(sourceFlag)
		} else {
			// Validate all paths and get absolute paths
			absSourcePath, destPath, err = validatePaths(sourceFlag, destFlag)
		}
		if err != nil {
			return err
		}

		SetSourceRoot(absSourcePath)

		// First, run scan to capture any missed changes
		if !reportOnly {
			fmt.Println("Initial scan...")
			if err := scanPaths(absSourcePath, destPath, maxFileSize, dryRun); err != nil {
				return fmt.Errorf("initial scan failed: %w", err)
			}
		}

		// Create watcher
		watcher, err := NewWatcher(absSourcePath, maxFileSize, debug)
		if err != nil {
			return err
		}
		defer watcher.Close()

		// Create event buffer
		buffer := NewEventBuffer()

		// flushAll processes all remaining buffered events before exiting
		flushAll := func() error {
			events := buffer.Flush(0)
			if len(events) == 0 {
				return nil
			}
			fmt.Println("Processing remaining events...")
			if err := processEvents(events, absSourcePath, destPath, dryRun); err != nil {
				logError("Error processing final events: %v", err)
				return err
			}
			return nil
		}

		// Setup signal handling for graceful shutdown
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		// Start flush timer
		flushDuration := time.Duration(bufferTime) * time.Second
		ticker := time.NewTicker(flushDuration)
		defer ticker.Stop()

		// Restic forget tracking (12 hour interval)
		var lastResticForget time.Time
		var hasEventsForRestic bool
		resticForgetTicker := time.NewTicker(12 * time.Hour)
		defer resticForgetTicker.Stop()

		if reportOnly {
			fmt.Printf("Watching %s for changes (report only, nothing is copied)...\n", absSourcePath)
		} else {
			fmt.Printf("Watching %s for changes (buffer time: %ds)...\n", absSourcePath, bufferTime)
		}
		fmt.Println("Press Ctrl+C to stop")

		for {
			select {
			case event := <-watcher.Events():
				// Handle MustResyncAll specially
				if event.Type == EventMustResyncAll {
					if reportOnly {
						fmt.Printf("[%s] event resync required\n", nowTS())
						continue
					}
					fmt.Printf("[%s] File system resync required - performing full scan\n", time.Now().Format("2006-01-02 15:04:05"))
					if err := scanPaths(absSourcePath, destPath, maxFileSize, dryRun); err != nil {
						logError("Error during resync: %v", err)
						return err
					}
					continue
				}

				var doPassFilter bool
				if event.Type == EventDirChanged {
					doPassFilter = !IsExcludedDir(event.Path)
				} else {
					var err error
					doPassFilter, err = ShouldTrack(event.Path, maxFileSize, event.Type == EventDeleted)
					if err != nil {
						logError("failed to check tracking for %s: %v", event.Path, err)
						continue
					}
				}

				logEvent(event.Type, event.Path, doPassFilter)

				if doPassFilter && !reportOnly {
					buffer.Add(event.Path, event.Type)
				}

			case err := <-watcher.Errors():
				logError("Watch error: %v", err)
				if errors.Is(err, errWatchLimit) {
					// New directories would go unwatched: stop instead of running partially
					if flushErr := flushAll(); flushErr != nil {
						return flushErr
					}
					return err
				}

			case <-ticker.C:
				// Flush events older than buffer time
				events := buffer.Flush(flushDuration)
				if len(events) > 0 {
					if err := processEvents(events, absSourcePath, destPath, dryRun); err != nil {
						logError("Error processing events: %v", err)
						return err
					}
					hasEventsForRestic = true
				}

			case <-resticForgetTicker.C:
				// Apply restic retention policy if conditions are met
				if useRestic && !dryRun && hasEventsForRestic && time.Since(lastResticForget) >= 12*time.Hour {
					if err := ForgetRestic(destPath, debug); err != nil {
						logError("Error applying restic retention policy: %v", err)
					} else {
						lastResticForget = time.Now()
						hasEventsForRestic = false
					}
				}

			case <-sigChan:
				fmt.Println("\nShutting down...")
				if err := flushAll(); err != nil {
					return err
				}
				fmt.Println("Done")
				return nil
			}
		}
	},
}

var checkPathsCmd = &cobra.Command{
	Use:   "check-paths",
	Short: "Validate source and destination paths",
	Long: `Validates that source and destination paths are compatible for autoversion.

Checks performed:
- Source and destination paths can't be the same, or HOME, or /
- Neither path can be inside the other
- The source can't contain a .autoversion marker (swapped paths)
- No parent of the destination can contain a .autoversion marker, or a .git
  repository unless --no-git
- The destination must have a .autoversion marker naming the source (see init)

Also shows how symlinks are resolved.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requirePaths(true); err != nil {
			return err
		}

		if _, err := checkPaths(sourceFlag, destFlag); err != nil {
			return err
		}

		fmt.Println("Validation: ✓ All checks passed")
		return nil
	},
}

func init() {
	// Root-level persistent flags
	rootCmd.PersistentFlags().StringVar(&sourceFlag, "source", "", "source directory to track")
	rootCmd.PersistentFlags().StringVar(&destFlag, "destination", "", "destination directory, initialized by init")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().BoolVar(&noGit, "no-git", false, "disable git versioning")
	rootCmd.PersistentFlags().BoolVar(&useRestic, "restic", false, "enable restic backup")
	rootCmd.PersistentFlags().StringVar(&errLogFlag, "err-log", "", "error log file path")
	rootCmd.PersistentFlags().StringVar(&includeExtsFlag, "only-include-extensions", "", "only include files with these extensions (comma-separated, e.g. md,csv,txt)")
	rootCmd.PersistentFlags().StringArrayVar(&excludeDirsFlag, "exclude-dir", nil, "exclude a directory relative to source (repeatable, e.g. --exclude-dir Backup --exclude-dir tmp)")

	// Filter command flags
	filterCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")

	// Init command flags
	initCmd.Flags().BoolVar(&forceNewSource, "force-new-source", false, "use a non-empty destination or one initialized for another source, keeping its contents and git history")
	initCmd.Flags().BoolVar(&forceObliterate, "force-obliterate-yes-i-am-sure", false, "remove everything in the destination, .git included, then initialize it")
	initCmd.MarkFlagsMutuallyExclusive("force-new-source", "force-obliterate-yes-i-am-sure")

	// Sync command flags
	syncCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without executing")

	// Watch command flags
	watchCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	watchCmd.Flags().IntVar(&bufferTime, "buffer-time", defaultBufferTime, "buffer time in seconds")
	watchCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without executing")
	watchCmd.Flags().BoolVar(&reportOnly, "report-only", false, "only print file events: no --destination needed, no initial scan, nothing copied")
	watchCmd.Flags().StringVar(&logDirFlag, "log-dir", "", "write stdout.log and stderr.log in this directory, rotated by size (set by install)")

	// Add commands to root
	rootCmd.AddCommand(filterCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(checkPathsCmd)
}

func main() {
	// Use PersistentPreRunE to init error log before any command runs
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		SetIncludeExtensions(includeExtsFlag)
		if err := SetExcludeDirs(excludeDirsFlag); err != nil {
			return err
		}
		return initErrLog(errLogFlag)
	}

	defer closeErrLog()

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// formatSize formats a byte size in human-readable format (MB or GB)
func formatSize(bytes int64) string {
	const (
		MB = 1024 * 1024
		GB = 1024 * MB
	)

	if bytes >= GB {
		gb := float64(bytes) / float64(GB)
		return fmt.Sprintf("%.2f GB", gb)
	}
	mb := float64(bytes) / float64(MB)
	return fmt.Sprintf("%.2f MB", mb)
}

// scanPaths scans directories, prints actions, and optionally executes them
func scanPaths(source, dest string, maxFileSize int64, dryRun bool) error {
	// Create filter function
	filterFunc := func(path string, info os.FileInfo) bool {
		// Skip all symlinks (both file and directory symlinks)
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}

		// Apply ShouldTrack filter
		shouldTrack, _ := ShouldTrack(path, maxFileSize, false)
		return shouldTrack
	}

	var filesToCopy []string
	var filesToUpdate []string
	filesToCopyMap := make(map[string]bool)
	var totalScannedFiles int
	var totalScannedSize int64
	var totalChangedSize int64

	// Walk source directory
	err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			if IsExcludedDir(path) {
				return filepath.SkipDir
			}
			return nil
		}

		if filterFunc != nil && !filterFunc(path, info) {
			return nil
		}

		filesToCopyMap[path] = true
		totalScannedFiles++

		sourceInfo := GetFileInfo(path)
		totalScannedSize += sourceInfo.Size

		relPath, _ := filepath.Rel(source, path)
		destPath := filepath.Join(dest, relPath)

		destInfo := GetFileInfo(destPath)

		if !FilesEqual(sourceInfo, destInfo) {
			fmt.Printf("copy '%s' to '%s'\n", path, destPath)
			totalChangedSize += sourceInfo.Size

			if destInfo.Exists {
				filesToUpdate = append(filesToUpdate, path)
			} else {
				filesToCopy = append(filesToCopy, path)
			}

			// Execute if not dry run
			if !dryRun {
				if _, err := CopyFile(path, source, dest, debug); err != nil {
					if errors.Is(err, errDestMissing) {
						return err
					}
					logError("Error copying %s: %v", relPath, err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Find and process deleted files
	var filesToDelete []string
	filepath.Walk(dest, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(dest, path)
		if relPath == markerFileName {
			return nil
		}
		sourcePath := filepath.Join(source, relPath)

		if _, exists := filesToCopyMap[sourcePath]; !exists {
			fmt.Printf("remove '%s'\n", path)
			filesToDelete = append(filesToDelete, relPath)
		}

		return nil
	})

	changeCount := len(filesToCopy) + len(filesToUpdate) + len(filesToDelete)

	// Execute deletions and commit if not dry run
	if !dryRun && changeCount > 0 {
		if len(filesToDelete) > 0 {
			if err := Remove(dest, filesToDelete, debug); err != nil {
				return fmt.Errorf("failed to remove files: %w", err)
			}
		}

		// Commit changes to git
		if !noGit {
			if err := Add(dest, []string{"."}, debug); err != nil {
				return fmt.Errorf("failed to add files to git: %w", err)
			}
			if err := Commit(dest, fmt.Sprintf("scanned %d changes", changeCount)); err != nil {
				return fmt.Errorf("failed to commit changes: %w", err)
			}
		}

		// Create restic snapshot
		if useRestic {
			if err := BackupRestic(dest, debug); err != nil {
				return fmt.Errorf("failed to backup with restic: %w", err)
			}
			if err := ForgetRestic(dest, debug); err != nil {
				return fmt.Errorf("failed to apply restic retention policy: %w", err)
			}
		}
	}

	// Print summary
	fmt.Printf("Scanned %d files (%s)\n", totalScannedFiles, formatSize(totalScannedSize))
	if changeCount > 0 {
		var parts []string
		if len(filesToCopy) > 0 {
			parts = append(parts, fmt.Sprintf("    added: %d", len(filesToCopy)))
		}
		if len(filesToUpdate) > 0 {
			parts = append(parts, fmt.Sprintf("  updated: %d", len(filesToUpdate)))
		}
		if len(filesToDelete) > 0 {
			parts = append(parts, fmt.Sprintf("  deleted: %d", len(filesToDelete)))
		}

		changeStr := strings.Join(parts, ", ")
		if totalChangedSize > 0 {
			fmt.Printf("%s (%s)\n", changeStr, formatSize(totalChangedSize))
		} else {
			fmt.Printf("%s\n", changeStr)
		}
	} else {
		fmt.Println("No changes detected")
	}

	return nil
}

// expandDirChanges replaces each EventDirChanged by events for the files under
// that directory: created for tracked files in the source, deleted for
// destination files no longer tracked in the source. Other events are kept,
// and take precedence over the expanded ones for the same path.
func expandDirChanges(events []PendingFileChanges, absSourcePath, absDestPath string) []PendingFileChanges {
	var result []PendingFileChanges
	var dirs []string
	seen := make(map[string]bool)
	for _, event := range events {
		if event.Action == EventDirChanged {
			dirs = append(dirs, event.Path)
			continue
		}
		result = append(result, event)
		seen[event.Path] = true
	}
	add := func(path string, action EventType) {
		if !seen[path] {
			seen[path] = true
			result = append(result, PendingFileChanges{Path: path, Action: action})
		}
	}

	for _, dir := range dirs {
		// Missing (moved away or removed): nothing to walk
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if IsExcludedDir(path) {
					return filepath.SkipDir
				}
				return nil
			}
			if info.Mode()&os.ModeSymlink == 0 {
				if track, _ := ShouldTrack(path, maxFileSize, false); track {
					add(path, EventCreated)
				}
			}
			return nil
		})

		relDir, err := filepath.Rel(absSourcePath, dir)
		if err != nil || !filepath.IsLocal(relDir) {
			continue
		}
		filepath.Walk(filepath.Join(absDestPath, relDir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if info.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			relPath, _ := filepath.Rel(absDestPath, path)
			if relPath == markerFileName {
				return nil
			}
			sourcePath := filepath.Join(absSourcePath, relPath)
			if track, _ := ShouldTrack(sourcePath, maxFileSize, false); !track {
				add(sourcePath, EventDeleted)
			}
			return nil
		})
	}
	return result
}

// processEvents processes a batch of events
func processEvents(events []PendingFileChanges, absSourcePath, absDestPath string, dryRun bool) error {
	changeCount := 0
	events = expandDirChanges(events, absSourcePath, absDestPath)

	for _, event := range events {
		relPath, err := filepath.Rel(absSourcePath, event.Path)
		if err != nil {
			continue
		}
		if !filepath.IsLocal(relPath) {
			logError("skipping event outside source tree: %s (rel=%s)", event.Path, relPath)
			continue
		}

		destPath := filepath.Join(absDestPath, relPath)
		timestamp := time.Now().Format("2006-01-02 15:04:05")

		// Process the event based on type
		if event.Action == EventDeleted {
			fmt.Printf("[%s] remove '%s'\n", timestamp, destPath)
			if !dryRun {
				err := Remove(absDestPath, []string{relPath}, debug)
				if errors.Is(err, errDestMissing) {
					return err
				}
				if err == nil {
					changeCount++
				}
			} else {
				changeCount++
			}
		} else {
			// Same check as scanPaths. It also stops a loop on macOS, where
			// autoversion's own copy (a clone) triggers an event for the source.
			if FilesEqual(GetFileInfo(event.Path), GetFileInfo(destPath)) {
				if debug {
					logSkip("[%s] unchanged '%s'\n", timestamp, event.Path)
				}
				continue
			}

			// Get source and destination file info for logging
			sourceInfo, err := os.Stat(event.Path)
			destInfo, destErr := os.Stat(destPath)

			if err == nil {
				sourceDetails := fmt.Sprintf(" (mtime=%s, size=%d)",
					sourceInfo.ModTime().Format("2006-01-02 15:04:05"),
					sourceInfo.Size())

				destDetails := ""
				if destErr == nil {
					destDetails = fmt.Sprintf(" (mtime=%s, size=%d)",
						destInfo.ModTime().Format("2006-01-02 15:04:05"),
						destInfo.Size())
				}

				fmt.Printf("[%s] copy '%s'%s to '%s'%s\n",
					timestamp, event.Path, sourceDetails, destPath, destDetails)
			} else {
				fmt.Printf("[%s] copy '%s' to '%s'\n", timestamp, event.Path, destPath)
			}

			if !dryRun {
				metaChanged, err := CopyFile(event.Path, absSourcePath, absDestPath, debug)
				if errors.Is(err, errDestMissing) {
					return err
				}
				if err == nil {
					if !metaChanged {
						fmt.Printf("    no metadata change\n")
					}
					changeCount++
				}
			} else {
				changeCount++
			}
		}
	}

	if changeCount > 0 {
		if !dryRun {
			// Commit changes to git
			if !noGit {
				if err := Add(absDestPath, []string{"."}, debug); err != nil {
					return fmt.Errorf("failed to add files to git: %w", err)
				}
				if err := Commit(absDestPath, fmt.Sprintf("detected %d changes", changeCount)); err != nil {
					return fmt.Errorf("failed to commit changes: %w", err)
				}
			}

			// Create restic snapshot
			if useRestic {
				if err := BackupRestic(absDestPath, debug); err != nil {
					return fmt.Errorf("failed to backup with restic: %w", err)
				}
			}
		}
	}

	return nil
}

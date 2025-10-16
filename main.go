package main

import (
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
	basePath string
	debug    bool

	// Command-specific flags
	maxFileSize int64
	bufferTime  int
	dryRun      bool
)

// computeTargetPath computes the target path from base and source path
// For source under $HOME: target = base + (source - $HOME)
// For source outside $HOME: target = base + source
func computeTargetPath(base, sourcePath string) (string, error) {
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute source path: %v", err)
	}

	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute base path: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %v", err)
	}

	// Check if source is under home directory
	relPath := absSource
	if filepath.HasPrefix(absSource, homeDir+string(filepath.Separator)) || absSource == homeDir {
		// Remove home directory prefix
		relPath, err = filepath.Rel(homeDir, absSource)
		if err != nil {
			return "", fmt.Errorf("failed to compute relative path: %v", err)
		}
	} else {
		// Use absolute path without leading /
		relPath = strings.TrimPrefix(absSource, string(filepath.Separator))
	}

	target := filepath.Join(absBase, relPath)

	fmt.Printf("source: '%s' into target: '%s'\n", sourcePath, target)
	return target, nil
}

// validateTargetPath validates that the target is initialized as a git repository
// If target doesn't exist, creates it and initializes as git repo
// If target exists but is not a git repo, returns error
// In dry-run mode, only prints warnings instead of creating/erroring
func validateTargetPath(targetPath, basePath, sourcePath string, dryRun bool) error {
	// Check if target directory exists
	info, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Target doesn't exist - create and initialize
			if dryRun {
				fmt.Fprintf(os.Stderr, "dry-run: would create and initialize %s\n", targetPath)
				return nil
			}

			fmt.Printf("Creating target directory: %s\n", targetPath)
			if err := EnsureDir(targetPath); err != nil {
				return fmt.Errorf("failed to create target directory: %v", err)
			}

			fmt.Printf("Initializing git repository at %s\n", targetPath)
			if err := Init(targetPath, debug); err != nil {
				return fmt.Errorf("failed to initialize git repository: %v", err)
			}

			return nil
		}
		return fmt.Errorf("failed to stat target path: %v", err)
	}

	// Target exists - check if it's a directory
	if !info.IsDir() {
		return fmt.Errorf("target path exists but is not a directory: %s", targetPath)
	}

	// Target directory exists - check if it's a git repo
	if !IsGitRepo(targetPath) {
		return fmt.Errorf("%s exists but is not a git repository", targetPath)
	}

	return nil
}

// verifyPaths verifies that source and target paths are safe to use
func verifyPaths(absSourcePath, absTargetPath string) error {
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
		{absTargetPath, "target"},
	}

	for _, p := range dangerousPaths {
		if p.path == homeDir {
			return fmt.Errorf("%s path cannot be HOME directory (%s)", p.name, homeDir)
		}
		if p.path == "/" {
			return fmt.Errorf("%s path cannot be root directory (/)", p.name)
		}
	}

	// Check if one path contains the other
	if filepath.HasPrefix(absSourcePath, absTargetPath+string(filepath.Separator)) {
		return fmt.Errorf("source path (%s) cannot be inside target path (%s)", absSourcePath, absTargetPath)
	}
	if filepath.HasPrefix(absTargetPath, absSourcePath+string(filepath.Separator)) {
		return fmt.Errorf("target path (%s) cannot be inside source path (%s)", absTargetPath, absSourcePath)
	}

	// Check exact match
	if absSourcePath == absTargetPath {
		return fmt.Errorf("source and target paths cannot be the same")
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

// resolvePath resolves symlinks and returns the absolute real path
// If the path doesn't exist, returns the absolute path without resolving symlinks
func resolvePath(path string) (string, error) {
	// First get absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	// Check if path exists
	if _, err := os.Lstat(absPath); os.IsNotExist(err) {
		// Path doesn't exist - return absolute path without resolving
		return absPath, nil
	}

	// Path exists - resolve any symlinks
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks: %v", err)
	}

	return realPath, nil
}

// validatePaths validates source and base paths, computes target, and verifies everything
// Returns absolute source path (with symlinks resolved) and target path, or an error
func validatePaths(sourcePath, basePath string, dryRun bool) (absSourcePath, targetPath string, err error) {
	// Validate source path exists and is a directory
	if err := validateSourcePath(sourcePath); err != nil {
		return "", "", err
	}

	// Resolve source path symlinks
	absSourcePath, err = resolvePath(sourcePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve source path: %v", err)
	}

	// Resolve base path symlinks
	resolvedBasePath, err := resolvePath(basePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve base path: %v", err)
	}

	// Compute target path using resolved source path
	targetPath, err = computeTargetPath(resolvedBasePath, absSourcePath)
	if err != nil {
		return "", "", err
	}

	// Validate target is initialized
	if err := validateTargetPath(targetPath, resolvedBasePath, absSourcePath, dryRun); err != nil {
		return "", "", err
	}

	// Verify paths are safe
	if err := verifyPaths(absSourcePath, targetPath); err != nil {
		return "", "", err
	}

	return absSourcePath, targetPath, nil
}

var rootCmd = &cobra.Command{
	Use:   "autoversion",
	Short: "Track file versions in real-time using git",
	Long: `autoversion - track file versions in real-time using git

Automatically tracks changes to files and commits them to git repositories,
making it easy to see what changed and restore files to any point in time.`,
}

var filterCmd = &cobra.Command{
	Use:   "filter <source>",
	Short: "List files that would be tracked",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sourcePath := args[0]

		if err := validateSourcePath(sourcePath); err != nil {
			return err
		}

		// Resolve symlinks in source path
		absSourcePath, err := resolvePath(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to resolve source path: %v", err)
		}

		// Walk and print matching files
		filepath.Walk(absSourcePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
				return nil
			}

			if info.IsDir() {
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
	Use:   "init <source>",
	Short: "Initialize a git repository for version tracking",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if basePath == "" {
			return fmt.Errorf("--base is required")
		}

		sourcePath := args[0]

		// Resolve source path symlinks
		absSourcePath, err := resolvePath(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to resolve source path: %v", err)
		}

		// Resolve base path symlinks
		resolvedBasePath, err := resolvePath(basePath)
		if err != nil {
			return fmt.Errorf("failed to resolve base path: %v", err)
		}

		// Compute target path
		targetPath, err := computeTargetPath(resolvedBasePath, absSourcePath)
		if err != nil {
			return err
		}

		// Create directory if needed
		if err := EnsureDir(targetPath); err != nil {
			return err
		}

		// Check if already initialized
		gitDir := filepath.Join(targetPath, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			fmt.Fprintf(os.Stderr, "Repository already initialized at %s\n", targetPath)
			return nil
		}

		// Initialize git repository
		if err := Init(targetPath, debug); err != nil {
			return err
		}

		fmt.Printf("Initialized git repository at %s\n", targetPath)
		return nil
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync <source>",
	Short: "Sync source directory to target",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if basePath == "" {
			return fmt.Errorf("--base is required")
		}

		sourcePath := args[0]

		// Validate all paths and get absolute paths
		absSourcePath, targetPath, err := validatePaths(sourcePath, basePath, dryRun)
		if err != nil {
			return err
		}

		// Sync files
		return scanPaths(absSourcePath, targetPath, maxFileSize, dryRun)
	},
}

var watchCmd = &cobra.Command{
	Use:   "watch <source>",
	Short: "Watch for changes in real-time",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if basePath == "" {
			return fmt.Errorf("--base is required")
		}

		sourcePath := args[0]

		// Validate all paths and get absolute paths
		absSourcePath, targetPath, err := validatePaths(sourcePath, basePath, dryRun)
		if err != nil {
			return err
		}

		// First, run scan to capture any missed changes
		fmt.Println("Initial scan...")
		if err := scanPaths(absSourcePath, targetPath, maxFileSize, dryRun); err != nil {
			return fmt.Errorf("initial scan failed: %w", err)
		}

		// Create watcher
		watcher, err := NewWatcher(absSourcePath, maxFileSize, debug)
		if err != nil {
			return err
		}
		defer watcher.Close()

		// Create event buffer
		buffer := NewEventBuffer()

		// Setup signal handling for graceful shutdown
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		// Start flush timer
		flushDuration := time.Duration(bufferTime) * time.Second
		ticker := time.NewTicker(flushDuration)
		defer ticker.Stop()

		fmt.Printf("Watching %s for changes (buffer time: %ds)...\n", absSourcePath, bufferTime)
		fmt.Println("Press Ctrl+C to stop")

		for {
			select {
			case event := <-watcher.Events():
				// Handle MustResyncAll specially
				if event.Type == EventMustResyncAll {
					fmt.Printf("[%s] File system resync required - performing full scan\n", time.Now().Format("2006-01-02 15:04:05"))
					if err := scanPaths(absSourcePath, targetPath, maxFileSize, dryRun); err != nil {
						fmt.Fprintf(os.Stderr, "Error during resync: %v\n", err)
					}
					continue
				}

				doPassFilter, _ := ShouldTrack(event.Path, maxFileSize, event.Type == EventDeleted)
				// Map event type to operation name
				var opName string
				switch event.Type {
				case EventCreated:
					opName = "created "
				case EventModified:
					opName = "modified"
				case EventDeleted:
					opName = "deleted "
				}

				evStatus :=  "BUFFER"
				if !doPassFilter {
					evStatus = "skip  "
				}
				fmt.Printf("[%s] event %s %s %s \n", time.Now().Format("2006-01-02 15:04:05"), evStatus, opName, event.Path)

				if doPassFilter {
					buffer.Add(event.Path, event.Type)
				}

			case err := <-watcher.Errors():
				fmt.Fprintf(os.Stderr, "Watch error: %v\n", err)

			case <-ticker.C:
				// Flush events older than buffer time
				events := buffer.Flush(flushDuration)
				if len(events) > 0 {
					if err := processEvents(events, absSourcePath, targetPath, dryRun); err != nil {
						fmt.Fprintf(os.Stderr, "Error processing events: %v\n", err)
						// for now let's keep going on errors, maybe they'll go away
						// return err
					}
				}

			case <-sigChan:
				fmt.Println("\nShutting down...")
				// Flush all remaining events
				events := buffer.Flush(0)
				if len(events) > 0 {
					fmt.Println("Processing remaining events...")
					if err := processEvents(events, absSourcePath, targetPath, dryRun); err != nil {
						fmt.Fprintf(os.Stderr, "Error processing final events: %v\n", err)
						return err
					}
				}
				fmt.Println("Done")
				return nil
			}
		}
	},
}

func init() {
	// Root-level persistent flags
	rootCmd.PersistentFlags().StringVar(&basePath, "base", "", "base directory path")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable debug logging")

	// Filter command flags
	filterCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")

	// Sync command flags
	syncCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without executing")

	// Watch command flags
	watchCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	watchCmd.Flags().IntVar(&bufferTime, "buffer-time", defaultBufferTime, "buffer time in seconds")
	watchCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without executing")

	// Add commands to root
	rootCmd.AddCommand(filterCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(watchCmd)
}

func main() {
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
func scanPaths(source, target string, maxFileSize int64, dryRun bool) error {
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
	filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
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
		targetPath := filepath.Join(target, relPath)

		targetInfo := GetFileInfo(targetPath)

		if !FilesEqual(sourceInfo, targetInfo) {
			fmt.Printf("copy '%s' to '%s'\n", path, targetPath)
			totalChangedSize += sourceInfo.Size

			if targetInfo.Exists {
				filesToUpdate = append(filesToUpdate, path)
			} else {
				filesToCopy = append(filesToCopy, path)
			}

			// Execute if not dry run
			if !dryRun {
				if _, err := CopyFile(path, source, target, debug); err != nil {
					fmt.Fprintf(os.Stderr, "Error copying %s: %v\n", relPath, err)
				}
			}
		}

		return nil
	})

	// Find and process deleted files
	var filesToDelete []string
	filepath.Walk(target, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(target, path)
		sourcePath := filepath.Join(source, relPath)

		if _, exists := filesToCopyMap[sourcePath]; !exists {
			if sourceInfo := GetFileInfo(sourcePath); !sourceInfo.Exists {
				fmt.Printf("remove '%s'\n", path)
				filesToDelete = append(filesToDelete, relPath)
			}
		}

		return nil
	})

	changeCount := len(filesToCopy) + len(filesToUpdate) + len(filesToDelete)

	// Execute deletions and commit if not dry run
	if !dryRun && changeCount > 0 {
		if len(filesToDelete) > 0 {
			if err := Remove(target, filesToDelete, debug); err != nil {
				return fmt.Errorf("failed to remove files: %w", err)
			}
		}

		// Commit changes
		if err := Add(target, []string{"."}, debug); err != nil {
			return fmt.Errorf("failed to add files to git: %w", err)
		}
		if err := Commit(target, fmt.Sprintf("scanned %d changes", changeCount)); err != nil {
			return fmt.Errorf("failed to commit changes: %w", err)
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

// processEvents processes a batch of events
func processEvents(events []PendingFileChanges, absSourcePath, absTargetPath string, dryRun bool) error {
	changeCount := 0

	for _, event := range events {
		relPath, err := filepath.Rel(absSourcePath, event.Path)
		if err != nil {
			continue
		}

		targetPath := filepath.Join(absTargetPath, relPath)
		timestamp := time.Now().Format("2006-01-02 15:04:05")

		// Process the event based on type
		if event.Action == EventDeleted {
			fmt.Printf("[%s] remove '%s'\n", timestamp, targetPath)
			if !dryRun {
				if err := Remove(absTargetPath, []string{relPath}, debug); err == nil {
					changeCount++
				}
			} else {
				changeCount++
			}
		} else {
			fmt.Printf("[%s] copy '%s' to '%s'\n", timestamp, event.Path, targetPath)
			if !dryRun {
				if metaChanged, err := CopyFile(event.Path, absSourcePath, absTargetPath, debug); err == nil {
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
			if err := Add(absTargetPath, []string{"."}, debug); err != nil {
				return fmt.Errorf("failed to add files to git: %w", err)
			}
			if err := Commit(absTargetPath, fmt.Sprintf("detected %d changes", changeCount)); err != nil {
				return fmt.Errorf("failed to commit changes: %w", err)
			}
		}
	}

	return nil
}

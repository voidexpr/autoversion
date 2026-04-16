package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultMaxFileSizeMB = 10
	defaultBufferTime    = 3

	rapidRestartWindow = 30 * time.Second
	rapidRestartLimit  = 2

	restartStateFile = "restart_state"
	keyLastStart     = "last_start"
	keyRapidCount    = "rapid_count"
)

var (
	// Global flags
	basePath   string
	debug      bool
	noGit      bool
	useRestic  bool
	errLogFlag string

	// Command-specific flags
	maxFileSize    int64
	bufferTime     int
	dryRun         bool
	noLogin        bool
	includeExtsFlag string
	excludeDirsFlag string
)

// longestCommonDir returns the longest common directory path between two absolute paths
func longestCommonDir(a, b string) string {
	aParts := strings.Split(filepath.Clean(a), string(filepath.Separator))
	bParts := strings.Split(filepath.Clean(b), string(filepath.Separator))

	var common []string
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if aParts[i] == bParts[i] {
			common = append(common, aParts[i])
		} else {
			break
		}
	}

	if len(common) <= 1 {
		return string(filepath.Separator)
	}
	return strings.Join(common, string(filepath.Separator))
}

// computeTargetPath computes the target path from base and source path
// For source under $HOME: target = base + (source - $HOME)
// For source outside $HOME with common ancestor: target = base + (source - commonAncestor)
// For source outside $HOME with no common ancestor: target = base + source
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

	var relPath string

	// Case 1: Source is under home directory
	if strings.HasPrefix(absSource, homeDir+string(filepath.Separator)) || absSource == homeDir {
		relPath, err = filepath.Rel(homeDir, absSource)
		if err != nil {
			return "", fmt.Errorf("failed to compute relative path: %v", err)
		}
	} else {
		// Case 2: Source is outside HOME
		// Check if base and source share a common ancestor
		commonDir := longestCommonDir(absBase, absSource)

		if commonDir != string(filepath.Separator) {
			// They share a common ancestor - use relative path from there
			relPath, err = filepath.Rel(commonDir, absSource)
			if err != nil {
				// Fallback to full path
				relPath = strings.TrimPrefix(absSource, string(filepath.Separator))
			}
		} else {
			// No common ancestor - use full path structure
			relPath = strings.TrimPrefix(absSource, string(filepath.Separator))
		}
	}

	target := filepath.Join(absBase, relPath)

	if debug {
		fmt.Printf("computeTargetPath:\n")
		fmt.Printf("  base:      '%s'\n", base)
		fmt.Printf("  absBase:   '%s'\n", absBase)
		fmt.Printf("  source:    '%s'\n", sourcePath)
		fmt.Printf("  absSource: '%s'\n", absSource)
		fmt.Printf("  homeDir:   '%s'\n", homeDir)
		fmt.Printf("  relPath:   '%s'\n", relPath)
		fmt.Printf("  target:    '%s'\n", target)
	}
	return target, nil
}

// isDirEmpty returns true if the directory exists and contains no entries
func isDirEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) == 0
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

			// Initialize git if enabled (not disabled with --no-git)
			if !noGit {
				fmt.Printf("Initializing git repository at %s\n", targetPath)
				if err := Init(targetPath, debug); err != nil {
					return fmt.Errorf("failed to initialize git repository: %v", err)
				}
			}

			// Initialize restic if enabled
			if useRestic {
				if err := InitRestic(targetPath, debug); err != nil {
					return fmt.Errorf("failed to initialize restic: %v", err)
				}
			}

			return nil
		}
		return fmt.Errorf("failed to stat target path: %v", err)
	}

	// Target exists - check if it's a directory
	if !info.IsDir() {
		return fmt.Errorf("target path exists but is not a directory: %s", targetPath)
	}

	// Target directory exists - if non-empty, must be a git repo (if git is not disabled)
	if !noGit && !IsGitRepo(targetPath) && !isDirEmpty(targetPath) {
		return fmt.Errorf("%s exists with files but is not a git repository", targetPath)
	}

	// Initialize restic if enabled and not already initialized
	if useRestic && !IsResticRepo(targetPath) {
		if dryRun {
			fmt.Fprintf(os.Stderr, "dry-run: would initialize restic for %s\n", targetPath)
		} else {
			if err := InitRestic(targetPath, debug); err != nil {
				return fmt.Errorf("failed to initialize restic: %v", err)
			}
		}
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

// hasGitInParents checks if any parent directory of path contains a .git directory
func hasGitInParents(path string) (string, bool) {
	current := filepath.Dir(path) // start from parent, not the path itself
	for current != "/" && current != "." {
		gitPath := filepath.Join(current, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() {
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

// PathValidationResult contains the results of path validation
type PathValidationResult struct {
	SourceInput    string
	SourceResolved string
	BaseInput      string
	BaseResolved   string
	TargetComputed string
	Errors         []string
}

// ValidateSourceAndBase validates that source and base paths are compatible
// Returns resolved paths and any validation errors
func ValidateSourceAndBase(sourcePath, basePath string) (*PathValidationResult, error) {
	result := &PathValidationResult{
		SourceInput: sourcePath,
		BaseInput:   basePath,
	}

	// Resolve source path
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return result, fmt.Errorf("failed to get absolute source path: %v", err)
	}

	// Check source exists and is a directory
	sourceInfo, err := os.Stat(absSource)
	if err != nil {
		if os.IsNotExist(err) {
			result.Errors = append(result.Errors, fmt.Sprintf("source path does not exist: %s", absSource))
		} else {
			return result, fmt.Errorf("failed to stat source path: %v", err)
		}
	} else if !sourceInfo.IsDir() {
		result.Errors = append(result.Errors, fmt.Sprintf("source path is not a directory: %s", absSource))
	}

	resolvedSource, err := resolvePath(absSource)
	if err != nil {
		return result, fmt.Errorf("failed to resolve source path: %v", err)
	}
	result.SourceResolved = resolvedSource

	// Resolve base path
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return result, fmt.Errorf("failed to get absolute base path: %v", err)
	}

	// Check base exists and is a directory
	baseInfo, err := os.Stat(absBase)
	if err != nil {
		if os.IsNotExist(err) {
			result.Errors = append(result.Errors, fmt.Sprintf("base path does not exist: %s", absBase))
		} else {
			return result, fmt.Errorf("failed to stat base path: %v", err)
		}
	} else if !baseInfo.IsDir() {
		result.Errors = append(result.Errors, fmt.Sprintf("base path is not a directory: %s", absBase))
	}

	resolvedBase, err := resolvePath(absBase)
	if err != nil {
		return result, fmt.Errorf("failed to resolve base path: %v", err)
	}
	result.BaseResolved = resolvedBase

	// If either path doesn't exist, skip further validation that depends on resolved paths
	if len(result.Errors) > 0 {
		return result, nil
	}

	// Compute target path
	target, err := computeTargetPath(resolvedBase, resolvedSource)
	if err != nil {
		return result, fmt.Errorf("failed to compute target path: %v", err)
	}
	result.TargetComputed = target

	// Validation checks
	sep := string(filepath.Separator)

	// Check 1: base and source can't be the same
	if resolvedSource == resolvedBase {
		result.Errors = append(result.Errors, fmt.Sprintf("source and base paths cannot be the same (%s)", resolvedSource))
	}

	// Check 2: source can't be within base
	if filepath.HasPrefix(resolvedSource, resolvedBase+sep) {
		result.Errors = append(result.Errors, fmt.Sprintf("source path (%s) cannot be inside base path (%s)", resolvedSource, resolvedBase))
	}

	// Check 3: base can't be within source
	if filepath.HasPrefix(resolvedBase, resolvedSource+sep) {
		result.Errors = append(result.Errors, fmt.Sprintf("base path (%s) cannot be inside source path (%s)", resolvedBase, resolvedSource))
	}

	// Checks 4-6 only apply when git is enabled
	if !noGit {
		// Check 4: base directory itself can't be a git repository
		gitDir := filepath.Join(resolvedBase, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			result.Errors = append(result.Errors, fmt.Sprintf("base path (%s) is a git repository - autoversion creates subdirectories with their own git repos", resolvedBase))
		}

		// Check 5: no parent of base can have a .git repository
		if gitParent, found := hasGitInParents(resolvedBase); found {
			result.Errors = append(result.Errors, fmt.Sprintf("base path has a parent with .git repository (%s) - this would create nested git repos", gitParent))
		}

		// Check 6: if target exists and is non-empty, it must be a git repo
		if targetInfo, err := os.Stat(target); err == nil && targetInfo.IsDir() {
			if !IsGitRepo(target) && !isDirEmpty(target) {
				result.Errors = append(result.Errors, fmt.Sprintf("target path (%s) exists with files but is not a git repository", target))
			}
		}
	}

	return result, nil
}

// validatePaths validates source and base paths, computes target, and verifies everything
// Returns absolute source path (with symlinks resolved) and target path, or an error
func validatePaths(sourcePath, basePath string, dryRun bool) (absSourcePath, targetPath string, err error) {
	// Run comprehensive source/base validation
	result, err := ValidateSourceAndBase(sourcePath, basePath)
	if err != nil {
		return "", "", err
	}

	if len(result.Errors) > 0 {
		return "", "", fmt.Errorf("path validation failed: %s", result.Errors[0])
	}

	absSourcePath = result.SourceResolved
	targetPath = result.TargetComputed

	// Validate target is initialized
	if err := validateTargetPath(targetPath, result.BaseResolved, absSourcePath, dryRun); err != nil {
		return "", "", err
	}

	// Verify additional path safety (HOME, root, etc.)
	if err := verifyPaths(absSourcePath, targetPath); err != nil {
		return "", "", err
	}

	return absSourcePath, targetPath, nil
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

		SetSourceRoot(absSourcePath)

		// Walk and print matching files
		filepath.Walk(absSourcePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
				return nil
			}

			if info.IsDir() {
				if IsExcludedPath(path) {
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

		SetSourceRoot(absSourcePath)

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

		SetSourceRoot(absSourcePath)

		// Must run before expensive work (may exit 0).
		checkRapidRestartLoop(absSourcePath, targetPath)

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

		// Restic forget tracking (12 hour interval)
		var lastResticForget time.Time
		var hasEventsForRestic bool
		resticForgetTicker := time.NewTicker(12 * time.Hour)
		defer resticForgetTicker.Stop()

		fmt.Printf("Watching %s for changes (buffer time: %ds)...\n", absSourcePath, bufferTime)
		fmt.Println("Press Ctrl+C to stop")

		for {
			select {
			case event := <-watcher.Events():
				// Handle MustResyncAll specially
				if event.Type == EventMustResyncAll {
					fmt.Printf("[%s] File system resync required - performing full scan\n", time.Now().Format("2006-01-02 15:04:05"))
					if err := scanPaths(absSourcePath, targetPath, maxFileSize, dryRun); err != nil {
						logError("Error during resync: %v", err)
					return err
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

				if doPassFilter {
					fmt.Printf("[%s] event BUFFER %s %s \n", time.Now().Format("2006-01-02 15:04:05"), opName, event.Path)
				} else {
					logSkip("[%s] event skip   %s %s \n", time.Now().Format("2006-01-02 15:04:05"), opName, event.Path)
				}

				if doPassFilter {
					buffer.Add(event.Path, event.Type)
				}

			case err := <-watcher.Errors():
				logError("Watch error: %v", err)

			case <-ticker.C:
				// Flush events older than buffer time
				events := buffer.Flush(flushDuration)
				if len(events) > 0 {
					if err := processEvents(events, absSourcePath, targetPath, dryRun); err != nil {
						logError("Error processing events: %v", err)
						return err
					}
					hasEventsForRestic = true
				}

			case <-resticForgetTicker.C:
				// Apply restic retention policy if conditions are met
				if useRestic && !dryRun && hasEventsForRestic && time.Since(lastResticForget) >= 12*time.Hour {
					if err := ForgetRestic(targetPath, debug); err != nil {
						logError("Error applying restic retention policy: %v", err)
					} else {
						lastResticForget = time.Now()
						hasEventsForRestic = false
					}
				}


			case <-sigChan:
				fmt.Println("\nShutting down...")
				// Flush all remaining events
				events := buffer.Flush(0)
				if len(events) > 0 {
					fmt.Println("Processing remaining events...")
					if err := processEvents(events, absSourcePath, targetPath, dryRun); err != nil {
						logError("Error processing final events: %v", err)
						return err
					}
				}
				fmt.Println("Done")
				return nil
			}
		}
	},
}

var checkPathsCmd = &cobra.Command{
	Use:   "check-paths <source-path>",
	Short: "Validate source and base paths",
	Long: `Validates that source and base paths are compatible for autoversion.

Checks performed:
- Source and base paths can't be the same
- Source path can't be inside base path
- Base path can't be inside source path
- No parent directory of base can have a .git repository

Also shows how symlinks are resolved and what target path would be computed.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sourcePath := args[0]

		if basePath == "" {
			return fmt.Errorf("--base flag is required")
		}

		result, err := ValidateSourceAndBase(sourcePath, basePath)
		if err != nil {
			return err
		}

		fmt.Println("Path Resolution:")
		fmt.Printf("  source (input):    %s\n", result.SourceInput)
		if result.SourceInput != result.SourceResolved {
			fmt.Printf("  source (resolved): %s\n", result.SourceResolved)
		} else {
			fmt.Printf("  source (resolved): (no symlinks)\n")
		}
		fmt.Printf("  base (input):      %s\n", result.BaseInput)
		if result.BaseInput != result.BaseResolved {
			fmt.Printf("  base (resolved):   %s\n", result.BaseResolved)
		} else {
			fmt.Printf("  base (resolved):   (no symlinks)\n")
		}
		fmt.Printf("  target (computed): %s\n", result.TargetComputed)
		fmt.Println()

		if len(result.Errors) > 0 {
			fmt.Println("Validation Errors:")
			for _, e := range result.Errors {
				fmt.Printf("  ✗ %s\n", e)
			}
			return fmt.Errorf("validation failed with %d error(s)", len(result.Errors))
		}

		fmt.Println("Validation: ✓ All checks passed")
		return nil
	},
}

func init() {
	// Root-level persistent flags
	rootCmd.PersistentFlags().StringVar(&basePath, "base", "", "base directory path")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().BoolVar(&noGit, "no-git", false, "disable git versioning")
	rootCmd.PersistentFlags().BoolVar(&useRestic, "restic", false, "enable restic backup")
	rootCmd.PersistentFlags().StringVar(&errLogFlag, "err-log", "", "error log file path")
	rootCmd.PersistentFlags().StringVar(&includeExtsFlag, "only-include-extensions", "", "only include files with these extensions (comma-separated, e.g. md,csv,txt)")
	rootCmd.PersistentFlags().StringVar(&excludeDirsFlag, "exclude-dirs", "", "exclude directories relative to source (comma-separated, e.g. Backup,tmp)")

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

// No-op when the log dir doesn't exist (watch invoked outside launchd).
func checkRapidRestartLoop(sourcePath, targetPath string) {
	jobID := jobIdentifier(sourcePath, targetPath)
	dir, err := logDir(jobID)
	if err != nil {
		return
	}
	if _, err := os.Stat(dir); err != nil {
		return
	}
	statePath := filepath.Join(dir, restartStateFile)

	var lastStart int64
	var rapidCount int
	if data, err := os.ReadFile(statePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			switch k {
			case keyLastStart:
				lastStart, _ = strconv.ParseInt(v, 10, 64)
			case keyRapidCount:
				rapidCount, _ = strconv.Atoi(v)
			}
		}
	}

	now := time.Now()
	tripped := false
	if lastStart > 0 && now.Sub(time.Unix(lastStart, 0)) < rapidRestartWindow {
		rapidCount++
		tripped = rapidCount >= rapidRestartLimit
	} else {
		rapidCount = 0
	}

	newStart := now.Unix()
	if tripped {
		logError("rapid restart loop detected (%d fast exits in a row within %s); exiting 0 so launchd stops relaunching. Inspect %s/stderr.log and run 'launchctl unload && launchctl load' once fixed.",
			rapidCount, rapidRestartWindow, dir)
		newStart, rapidCount = 0, 0
	}
	content := fmt.Sprintf("%s=%d\n%s=%d\n", keyLastStart, newStart, keyRapidCount, rapidCount)
	_ = os.WriteFile(statePath, []byte(content), 0644)

	if tripped {
		closeErrLog()
		os.Exit(0)
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
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			if IsExcludedPath(path) {
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
					logError("Error copying %s: %v", relPath, err)
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
			fmt.Printf("remove '%s'\n", path)
			filesToDelete = append(filesToDelete, relPath)
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

		// Commit changes to git
		if !noGit {
			if err := Add(target, []string{"."}, debug); err != nil {
				return fmt.Errorf("failed to add files to git: %w", err)
			}
			if err := Commit(target, fmt.Sprintf("scanned %d changes", changeCount)); err != nil {
				return fmt.Errorf("failed to commit changes: %w", err)
			}
		}

		// Create restic snapshot
		if useRestic {
			if err := BackupRestic(target, debug); err != nil {
				return fmt.Errorf("failed to backup with restic: %w", err)
			}
			if err := ForgetRestic(target, debug); err != nil {
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
			// Get source and target file info for logging
			sourceInfo, err := os.Stat(event.Path)
			targetInfo, targetErr := os.Stat(targetPath)

			if err == nil {
				sourceDetails := fmt.Sprintf(" (mtime=%s, size=%d)",
					sourceInfo.ModTime().Format("2006-01-02 15:04:05"),
					sourceInfo.Size())

				targetDetails := ""
				if targetErr == nil {
					targetDetails = fmt.Sprintf(" (mtime=%s, size=%d)",
						targetInfo.ModTime().Format("2006-01-02 15:04:05"),
						targetInfo.Size())
				}

				fmt.Printf("[%s] copy '%s'%s to '%s'%s\n",
					timestamp, event.Path, sourceDetails, targetPath, targetDetails)
			} else {
				fmt.Printf("[%s] copy '%s' to '%s'\n", timestamp, event.Path, targetPath)
			}

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
			// Commit changes to git
			if !noGit {
				if err := Add(absTargetPath, []string{"."}, debug); err != nil {
					return fmt.Errorf("failed to add files to git: %w", err)
				}
				if err := Commit(absTargetPath, fmt.Sprintf("detected %d changes", changeCount)); err != nil {
					return fmt.Errorf("failed to commit changes: %w", err)
				}
			}

			// Create restic snapshot 
			if useRestic {
				if err := BackupRestic(absTargetPath, debug); err != nil {
					return fmt.Errorf("failed to backup with restic: %w", err)
				}
			}
		}
	}

	return nil
}

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	defaultMaxFileSizeMB = 10
	defaultBufferTime    = 5
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "filter":
		filterCommand(os.Args[2:])
	case "init":
		initCommand(os.Args[2:])
	case "sync":
		syncCommand(os.Args[2:])
	case "watch":
		watchCommand(os.Args[2:])
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `autoversion - track file versions in real-time using git

Usage: autoversion <command> [options]

Commands:
  filter <path>                   List files that would be tracked
  init --base <base> <source>     Initialize a version repository
  sync --base <base> <source>     Sync files to target
  watch --base <base> <source>    Watch for changes in real-time

Examples:
  autoversion filter ~/projects/myapp
  autoversion init --base ~/repos ~/projects/myapp
  autoversion sync --base ~/repos ~/projects/myapp

Use "autoversion <command> -h" for more information about a command.
`)
}

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

	fmt.Println("source: '%s' into target: '%s'", sourcePath, target)
	return target, nil
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

func filterCommand(args []string) {
	fs := flag.NewFlagSet("filter", flag.ExitOnError)
	maxFileSize := fs.Int64("max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: autoversion filter <path> [options]\n\n")
		fmt.Fprintf(os.Stderr, "Scan directory and list files that would be tracked.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	sourcePath := fs.Arg(0)

	// Verify path exists and is a directory
	info, err := os.Stat(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %s is not a directory\n", sourcePath)
		os.Exit(1)
	}

	absSourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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

		if shouldTrack, _ := ShouldTrack(path, *maxFileSize); shouldTrack {
			if relPath, err := filepath.Rel(absSourcePath, path); err == nil {
				fmt.Println(relPath)
			}
		}

		return nil
	})
}

func initCommand(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	base := fs.String("base", "", "base path (required)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: autoversion init --base <base> <source>\n\n")
		fmt.Fprintf(os.Stderr, "Initialize a git repository for version tracking.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nNote: Options must come before positional arguments.\n")
	}

	fs.Parse(args)

	if *base == "" {
		fmt.Fprintf(os.Stderr, "Error: --base is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Error: source path is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	sourcePath := fs.Arg(0)

	// Compute target path
	targetPath, err := computeTargetPath(*base, sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Create directory if needed
	if err := EnsureDir(targetPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Check if already initialized
	gitDir := filepath.Join(targetPath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		fmt.Fprintf(os.Stderr, "Repository already initialized at %s\n", targetPath)
		return
	}

	// Initialize git repository
	if err := Init(targetPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Initialized git repository at %s\n", targetPath)
}

func syncCommand(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	base := fs.String("base", "", "base path (required)")
	maxFileSize := fs.Int64("max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	dryRun := fs.Bool("dry-run", false, "show what would be done without executing")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: autoversion sync [options] <source>\n\n")
		fmt.Fprintf(os.Stderr, "Sync source directory to target.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nNote: Options must come before positional arguments.\n")
	}

	fs.Parse(args)

	if *base == "" {
		fmt.Fprintf(os.Stderr, "Error: --base is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Error: source path is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	sourcePath := fs.Arg(0)

	// Verify source path exists and is a directory
	if info, err := os.Stat(sourcePath); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: source path must be a directory\n")
		os.Exit(1)
	}

	// Compute target path
	targetPath, err := computeTargetPath(*base, sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Get absolute paths for verification
	absSourcePath, _ := filepath.Abs(sourcePath)

	// Verify target is initialized
	if !IsGitRepo(targetPath) {
		if *dryRun {
			fmt.Fprintf(os.Stderr, "Warning: %s is not a git repository. Run 'autoversion init --base %s %s' first\n", targetPath, *base, sourcePath)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s is not a git repository. Run 'autoversion init --base %s %s' first\n", targetPath, *base, sourcePath)
			os.Exit(1)
		}
	}

	// Verify paths are safe
	if err := verifyPaths(absSourcePath, targetPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Sync files
	scanPaths(absSourcePath, targetPath, *maxFileSize, *dryRun)
}

func watchCommand(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	base := fs.String("base", "", "base path (required)")
	maxFileSize := fs.Int64("max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	bufferTime := fs.Int("buffer-time", defaultBufferTime, "buffer time in seconds")
	dryRun := fs.Bool("dry-run", false, "show what would be done without executing")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: autoversion watch [options] <source>\n\n")
		fmt.Fprintf(os.Stderr, "Watch source directory for changes and sync to target in real-time.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nNote: Options must come before positional arguments.\n")
	}

	fs.Parse(args)

	if *base == "" {
		fmt.Fprintf(os.Stderr, "Error: --base is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Error: source path is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	sourcePath := fs.Arg(0)

	// Verify source path exists and is a directory
	if info, err := os.Stat(sourcePath); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: source path must be a directory\n")
		os.Exit(1)
	}

	// Compute target path
	targetPath, err := computeTargetPath(*base, sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Get absolute paths
	absSourcePath, _ := filepath.Abs(sourcePath)

	// Verify target is initialized
	if !IsGitRepo(targetPath) {
		if *dryRun {
			fmt.Fprintf(os.Stderr, "Warning: %s is not a git repository. Run 'autoversion init --base %s %s' first\n", targetPath, *base, sourcePath)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s is not a git repository. Run 'autoversion init --base %s %s' first\n", targetPath, *base, sourcePath)
			os.Exit(1)
		}
	}

	// Verify paths are safe
	if err := verifyPaths(absSourcePath, targetPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// First, run scan to capture any missed changes
	fmt.Println("Initial scan...")
	scanPaths(absSourcePath, targetPath, *maxFileSize, *dryRun)

	// Create watcher
	watcher, err := NewWatcher(absSourcePath, *maxFileSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer watcher.Close()

	// Create event buffer
	buffer := NewEventBuffer()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start flush timer
	flushDuration := time.Duration(*bufferTime) * time.Second
	ticker := time.NewTicker(flushDuration)
	defer ticker.Stop()

	fmt.Printf("Watching %s for changes (buffer time: %ds)...\n", absSourcePath, *bufferTime)
	fmt.Println("Press Ctrl+C to stop")

	for {
		select {
		case event := <-watcher.Events():
			if watcher.ShouldProcess(event) {
				eventType := MapEventType(event.Op)
				buffer.Add(event.Name, eventType)
			}

		case err := <-watcher.Errors():
			fmt.Fprintf(os.Stderr, "Watch error: %v\n", err)

		case <-ticker.C:
			// Flush events older than buffer time
			events := buffer.Flush(flushDuration)
			if len(events) > 0 {
				processEvents(events, absSourcePath, targetPath, *dryRun)
			}

		case <-sigChan:
			fmt.Println("\nShutting down...")
			// Flush all remaining events
			events := buffer.Flush(0)
			if len(events) > 0 {
				fmt.Println("Processing remaining events...")
				processEvents(events, absSourcePath, targetPath, *dryRun)
			}
			fmt.Println("Done")
			return
		}
	}
}

// scanPaths scans directories, prints actions, and optionally executes them
func scanPaths(source, target string, maxFileSize int64, dryRun bool) {
	// Create filter function
	filterFunc := func(path string, info os.FileInfo) bool {
		// Skip all symlinks (both file and directory symlinks)
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}

		// Apply ShouldTrack filter
		shouldTrack, _ := ShouldTrack(path, maxFileSize)
		return shouldTrack
	}

	var filesToCopy []string
	filesToCopyMap := make(map[string]bool)

	// Walk source directory
	filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}

		if filterFunc != nil && !filterFunc(path, info) {
			return nil
		}

		filesToCopyMap[path] = true

		relPath, _ := filepath.Rel(source, path)
		targetPath := filepath.Join(target, relPath)

		sourceInfo := GetFileInfo(path)
		targetInfo := GetFileInfo(targetPath)

		if !FilesEqual(sourceInfo, targetInfo) {
			// Always print the action
			fmt.Printf("copy: %s\n", relPath)
			filesToCopy = append(filesToCopy, path)

			// Execute if not dry run
			if !dryRun {
				if err := CopyFile(path, source, target); err != nil {
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
				// Always print the action
				fmt.Printf("delete: %s\n", relPath)
				filesToDelete = append(filesToDelete, relPath)
			}
		}

		return nil
	})

	changeCount := len(filesToCopy) + len(filesToDelete)

	// Execute deletions and commit if not dry run
	if !dryRun && changeCount > 0 {
		if len(filesToDelete) > 0 {
			if err := Remove(target, filesToDelete); err != nil {
				fmt.Fprintf(os.Stderr, "Error removing files: %v\n", err)
			}
		}

		// Commit changes
		Add(target, []string{"."})
		Commit(target, fmt.Sprintf("scanned %d changes", changeCount))
	}

	// Print summary
	if changeCount > 0 {
		fmt.Printf("Scanned %d changes\n", changeCount)
	} else {
		fmt.Println("No changes detected")
	}
}

// processEvents processes a batch of events
func processEvents(events []Event, absSourcePath, absRepoPath string, dryRun bool) {
	changeCount := 0

	for _, event := range events {
		relPath, err := filepath.Rel(absSourcePath, event.Path)
		if err != nil {
			continue
		}

		// Map event type to operation name
		var opName string
		switch event.Action {
		case EventCreated:
			opName = "created"
		case EventModified:
			opName = "modified"
		case EventDeleted:
			opName = "deleted"
		}

		// Always print the detected event
		fmt.Printf("%s: %s\n", opName, relPath)

		// Always print the action that would be taken
		if event.Action == EventDeleted {
			fmt.Printf("delete: %s\n", relPath)
		} else {
			fmt.Printf("copy: %s\n", relPath)
		}

		// Actually perform the action if not dry run
		if !dryRun {
			if event.Action == EventDeleted {
				// Delete from repo
				if err := Remove(absRepoPath, []string{relPath}); err == nil {
					changeCount++
				}
			} else {
				// Copy to repo
				if err := CopyFile(event.Path, absSourcePath, absRepoPath); err == nil {
					changeCount++
				}
			}
		} else {
			changeCount++
		}
	}

	if changeCount > 0 && !dryRun {
		Add(absRepoPath, []string{"."})
		Commit(absRepoPath, fmt.Sprintf("detected %d changes", changeCount))
	}
}

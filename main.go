package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
	case "scan":
		scanCommand(os.Args[2:])
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
  filter <path>              List files that would be tracked
  init --repo <path>         Initialize a version repository
  scan --repo <repo> <src>   Scan and copy files to repository
  watch --repo <repo> <src>  Watch for changes in real-time

Examples:
  autoversion filter ~/projects/myapp
  autoversion init --repo ~/repos/myapp
  autoversion scan --repo ~/repos/myapp ~/projects/myapp

Use "autoversion <command> -h" for more information about a command.
`)
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

		// Skip symlinks to directories
		if info.Mode()&os.ModeSymlink != 0 {
			if targetInfo, err := os.Stat(path); err == nil && targetInfo.IsDir() {
				return nil
			}
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
	repoPath := fs.String("repo", "", "repository path (required)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: autoversion init --repo <path>\n\n")
		fmt.Fprintf(os.Stderr, "Initialize a git repository for version tracking.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	if *repoPath == "" {
		fmt.Fprintf(os.Stderr, "Error: --repo is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	// Create directory if needed
	if err := EnsureDir(*repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Check if already initialized
	gitDir := filepath.Join(*repoPath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		fmt.Fprintf(os.Stderr, "Repository already initialized at %s\n", *repoPath)
		return
	}

	// Initialize git repository
	if err := Init(*repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Initialized git repository at %s\n", *repoPath)
}

func scanCommand(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	repoPath := fs.String("repo", "", "repository path (required)")
	maxFileSize := fs.Int64("max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: autoversion scan --repo <repo> <source>\n\n")
		fmt.Fprintf(os.Stderr, "Scan source directory and copy filtered files to repository.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	if *repoPath == "" {
		fmt.Fprintf(os.Stderr, "Error: --repo is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Error: source path is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	sourcePath := fs.Arg(0)

	// Verify paths
	if info, err := os.Stat(sourcePath); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: source path must be a directory\n")
		os.Exit(1)
	}

	if _, err := os.Stat(*repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: repo path does not exist: %v\n", err)
		os.Exit(1)
	}

	// Get absolute paths
	absSourcePath, _ := filepath.Abs(sourcePath)
	absRepoPath, _ := filepath.Abs(*repoPath)

	// Scan and copy files
	filesToCopy := make(map[string]bool)
	changeCount := 0

	// Walk source directory
	filepath.Walk(absSourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Skip symlinks to directories
		if info.Mode()&os.ModeSymlink != 0 {
			if targetInfo, err := os.Stat(path); err == nil && targetInfo.IsDir() {
				return nil
			}
		}

		shouldTrack, _ := ShouldTrack(path, *maxFileSize)
		if !shouldTrack {
			return nil
		}

		filesToCopy[path] = true

		relPath, _ := filepath.Rel(absSourcePath, path)
		targetPath := filepath.Join(absRepoPath, relPath)

		// Compare and copy if different
		sourceInfo := GetFileInfo(path)
		targetInfo := GetFileInfo(targetPath)

		if !FilesEqual(sourceInfo, targetInfo) {
			if err := CopyFile(path, absSourcePath, absRepoPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			} else {
				changeCount++
			}
		}

		return nil
	})

	// Find deleted files
	var filesToDelete []string

	filepath.Walk(absRepoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(absRepoPath, path)
		sourcePath := filepath.Join(absSourcePath, relPath)

		if _, exists := filesToCopy[sourcePath]; !exists {
			if sourceInfo := GetFileInfo(sourcePath); !sourceInfo.Exists {
				filesToDelete = append(filesToDelete, relPath)
			}
		}

		return nil
	})

	// Delete files
	if len(filesToDelete) > 0 {
		if err := Remove(absRepoPath, filesToDelete); err == nil {
			changeCount += len(filesToDelete)
		}
	}

	// Commit changes
	if changeCount > 0 {
		Add(absRepoPath, []string{"."})
		Commit(absRepoPath, fmt.Sprintf("detected %d changes", changeCount))
		fmt.Printf("Scanned and committed %d changes\n", changeCount)
	} else {
		fmt.Println("No changes detected")
	}
}

func watchCommand(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	repoPath := fs.String("repo", "", "repository path (required)")
	maxFileSize := fs.Int64("max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	bufferTime := fs.Int("buffer-time", defaultBufferTime, "buffer time in seconds")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: autoversion watch --repo <repo> <source> [options]\n\n")
		fmt.Fprintf(os.Stderr, "Watch source directory for changes and copy to repository in real-time.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)

	if *repoPath == "" {
		fmt.Fprintf(os.Stderr, "Error: --repo is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Error: source path is required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	sourcePath := fs.Arg(0)

	// Verify paths
	if info, err := os.Stat(sourcePath); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: source path must be a directory\n")
		os.Exit(1)
	}

	if _, err := os.Stat(*repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: repo path does not exist: %v\n", err)
		os.Exit(1)
	}

	absSourcePath, _ := filepath.Abs(sourcePath)
	absRepoPath, _ := filepath.Abs(*repoPath)

	// First, run scan to capture any missed changes
	fmt.Println("Initial scan...")
	scanPaths(absSourcePath, absRepoPath, *maxFileSize)

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
				processEvents(events, absSourcePath, absRepoPath)
			}

		case <-sigChan:
			fmt.Println("\nShutting down...")
			// Flush all remaining events
			events := buffer.Flush(0)
			if len(events) > 0 {
				fmt.Println("Processing remaining events...")
				processEvents(events, absSourcePath, absRepoPath)
			}
			fmt.Println("Done")
			return
		}
	}
}

// scanPaths performs the initial scan (same as scan command)
func scanPaths(absSourcePath, absRepoPath string, maxFileSize int64) {
	filesToCopy := make(map[string]bool)
	changeCount := 0

	filepath.Walk(absSourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		if info.Mode()&os.ModeSymlink != 0 {
			if targetInfo, err := os.Stat(path); err == nil && targetInfo.IsDir() {
				return nil
			}
		}

		shouldTrack, _ := ShouldTrack(path, maxFileSize)
		if !shouldTrack {
			return nil
		}

		filesToCopy[path] = true

		relPath, _ := filepath.Rel(absSourcePath, path)
		targetPath := filepath.Join(absRepoPath, relPath)

		sourceInfo := GetFileInfo(path)
		targetInfo := GetFileInfo(targetPath)

		if !FilesEqual(sourceInfo, targetInfo) {
			if err := CopyFile(path, absSourcePath, absRepoPath); err == nil {
				changeCount++
			}
		}

		return nil
	})

	var filesToDelete []string
	filepath.Walk(absRepoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(absRepoPath, path)
		sourcePath := filepath.Join(absSourcePath, relPath)

		if _, exists := filesToCopy[sourcePath]; !exists {
			if sourceInfo := GetFileInfo(sourcePath); !sourceInfo.Exists {
				filesToDelete = append(filesToDelete, relPath)
			}
		}

		return nil
	})

	if len(filesToDelete) > 0 {
		if err := Remove(absRepoPath, filesToDelete); err == nil {
			changeCount += len(filesToDelete)
		}
	}

	if changeCount > 0 {
		Add(absRepoPath, []string{"."})
		Commit(absRepoPath, fmt.Sprintf("detected %d changes", changeCount))
		fmt.Printf("Initial scan: committed %d changes\n", changeCount)
	} else {
		fmt.Println("Initial scan: no changes")
	}
}

// processEvents processes a batch of events
func processEvents(events []Event, absSourcePath, absRepoPath string) {
	changeCount := 0

	for _, event := range events {
		relPath, err := filepath.Rel(absSourcePath, event.Path)
		if err != nil {
			continue
		}

		if event.Action == EventDeleted {
			// Delete from repo
			if err := Remove(absRepoPath, []string{relPath}); err == nil {
				fmt.Printf("Deleted: %s\n", relPath)
				changeCount++
			}
		} else {
			// Copy to repo
			if err := CopyFile(event.Path, absSourcePath, absRepoPath); err == nil {
				fmt.Printf("Updated: %s\n", relPath)
				changeCount++
			}
		}
	}

	if changeCount > 0 {
		Add(absRepoPath, []string{"."})
		Commit(absRepoPath, fmt.Sprintf("detected %d changes", changeCount))
	}
}

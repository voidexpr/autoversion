package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultMaxFileSizeMB = 10
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "filter":
		filterCommand()
	case "init":
		initCommand()
	case "scan":
		scanCommand()
	case "watch":
		fmt.Fprintf(os.Stderr, "watch command not yet implemented\n")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: autoversion COMMAND [OPTIONS]

Commands:
  filter PATH [--max-file-size MB]
      Scan recursively all files under PATH and print files to be versioned

  init --repo PATH
      Create repo directory and initialize git repository

  scan --repo PATH1 PATH2 [--max-file-size MB]
      Process all files from PATH2, filter and copy to PATH1, then commit

  watch --repo PATH2 PATH1 [--buffer-time SECONDS] [--max-file-size MB]
      Monitor PATH1 for changes and copy filtered files to PATH2

Options:
  --repo PATH           Repository path for versioned files
  --max-file-size MB    Maximum file size in megabytes (default: %d)
  --buffer-time SEC     Buffer time in seconds for watch (default: 5)
`, defaultMaxFileSizeMB)
}

func filterCommand() {
	fs := flag.NewFlagSet("filter", flag.ExitOnError)
	maxFileSize := fs.Int64("max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")

	fs.Parse(os.Args[2:])

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Error: filter command requires PATH argument\n")
		fmt.Fprintf(os.Stderr, "Usage: autoversion filter PATH [--max-file-size MB]\n")
		os.Exit(1)
	}

	sourcePath := fs.Arg(0)

	// Verify path exists
	info, err := os.Stat(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %s is not a directory\n", sourcePath)
		os.Exit(1)
	}

	// Get absolute path for relative path calculation
	absSourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting absolute path: %v\n", err)
		os.Exit(1)
	}

	// Walk the directory and print matching files
	err = filepath.Walk(absSourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error accessing %s: %v\n", path, err)
			return nil // Continue walking
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Check if file should be tracked
		shouldTrack, err := ShouldTrack(path, *maxFileSize)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error checking %s: %v\n", path, err)
			return nil
		}

		if shouldTrack {
			// Print relative path
			relPath, err := filepath.Rel(absSourcePath, path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting relative path for %s: %v\n", path, err)
				return nil
			}
			fmt.Println(relPath)
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking directory: %v\n", err)
		os.Exit(1)
	}
}

func initCommand() {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	repoPath := fs.String("repo", "", "repository path (required)")

	fs.Parse(os.Args[2:])

	if *repoPath == "" {
		fmt.Fprintf(os.Stderr, "Error: --repo PATH is required\n")
		fmt.Fprintf(os.Stderr, "Usage: autoversion init --repo PATH\n")
		os.Exit(1)
	}

	// Create directory if it doesn't exist
	if err := EnsureDir(*repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
		os.Exit(1)
	}

	// Check if already a git repo
	gitDir := filepath.Join(*repoPath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		fmt.Fprintf(os.Stderr, "Repository already initialized at %s\n", *repoPath)
		return
	}

	// Initialize git repository
	if err := Init(*repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing git repository: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Initialized git repository at %s\n", *repoPath)
}

func scanCommand() {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	repoPath := fs.String("repo", "", "repository path (required)")
	maxFileSize := fs.Int64("max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")

	fs.Parse(os.Args[2:])

	if *repoPath == "" {
		fmt.Fprintf(os.Stderr, "Error: --repo PATH is required\n")
		fmt.Fprintf(os.Stderr, "Usage: autoversion scan --repo REPO_PATH SOURCE_PATH\n")
		os.Exit(1)
	}

	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Error: SOURCE_PATH is required\n")
		fmt.Fprintf(os.Stderr, "Usage: autoversion scan --repo REPO_PATH SOURCE_PATH\n")
		os.Exit(1)
	}

	sourcePath := fs.Arg(0)

	// Verify paths
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: source path %s: %v\n", sourcePath, err)
		os.Exit(1)
	}

	if !sourceInfo.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: source path %s is not a directory\n", sourcePath)
		os.Exit(1)
	}

	if _, err := os.Stat(*repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: repo path %s: %v\n", *repoPath, err)
		os.Exit(1)
	}

	// Get absolute paths
	absSourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting absolute source path: %v\n", err)
		os.Exit(1)
	}

	absRepoPath, err := filepath.Abs(*repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting absolute repo path: %v\n", err)
		os.Exit(1)
	}

	// Track files to copy
	filesToCopy := make(map[string]bool)
	changeCount := 0

	// Walk source directory
	err = filepath.Walk(absSourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error accessing %s: %v\n", path, err)
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Check if file should be tracked
		shouldTrack, err := ShouldTrack(path, *maxFileSize)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error checking %s: %v\n", path, err)
			return nil
		}

		if !shouldTrack {
			return nil
		}

		// Track this file
		filesToCopy[path] = true

		// Get relative path
		relPath, err := filepath.Rel(absSourcePath, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting relative path for %s: %v\n", path, err)
			return nil
		}

		targetPath := filepath.Join(absRepoPath, relPath)

		// Compare with target
		sourceFileInfo := GetFileInfo(path)
		targetFileInfo := GetFileInfo(targetPath)

		if !FilesEqual(sourceFileInfo, targetFileInfo) {
			// Copy file
			if err := CopyFile(path, absSourcePath, absRepoPath); err != nil {
				fmt.Fprintf(os.Stderr, "Error copying %s: %v\n", path, err)
				return nil
			}
			changeCount++
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking source directory: %v\n", err)
		os.Exit(1)
	}

	// Now walk the repo to find files that don't exist in source (deleted files)
	filesToDelete := []string{}

	err = filepath.Walk(absRepoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip .git directory
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}

		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(absRepoPath, path)
		if err != nil {
			return nil
		}

		sourcePath := filepath.Join(absSourcePath, relPath)

		// If file doesn't exist in source, mark for deletion
		if _, exists := filesToCopy[sourcePath]; !exists {
			sourceInfo := GetFileInfo(sourcePath)
			if !sourceInfo.Exists {
				filesToDelete = append(filesToDelete, relPath)
			}
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking repo directory: %v\n", err)
	}

	// Delete files that don't exist in source
	if len(filesToDelete) > 0 {
		if err := Remove(absRepoPath, filesToDelete); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing deleted files: %v\n", err)
		} else {
			changeCount += len(filesToDelete)
		}
	}

	// Git add and commit
	if changeCount > 0 {
		if err := Add(absRepoPath, []string{"."}); err != nil {
			fmt.Fprintf(os.Stderr, "Error adding files to git: %v\n", err)
			os.Exit(1)
		}

		commitMsg := fmt.Sprintf("detected %d changes", changeCount)
		if err := Commit(absRepoPath, commitMsg); err != nil {
			fmt.Fprintf(os.Stderr, "Error committing changes: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Scanned and committed %d changes\n", changeCount)
	} else {
		fmt.Println("No changes detected")
	}
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// FileInfo holds file metadata for comparison
type FileInfo struct {
	ModTime int64
	Size    int64
	Exists  bool
}

// GetFileInfo returns metadata for a file
func GetFileInfo(path string) FileInfo {
	info, err := os.Stat(path)
	if err != nil {
		return FileInfo{Exists: false}
	}

	return FileInfo{
		ModTime: info.ModTime().Unix(),
		Size:    info.Size(),
		Exists:  true,
	}
}

// FilesEqual compares two files by mtime and size
func FilesEqual(info1, info2 FileInfo) bool {
	if !info1.Exists || !info2.Exists {
		return false
	}
	return info1.ModTime == info2.ModTime && info1.Size == info2.Size
}

// CopyFile copies a file from src to dst, preserving relative path structure
// sourcePath: the original file path
// sourceRoot: the root directory being watched
// targetRoot: the target repository root
func CopyFile(sourcePath, sourceRoot, targetRoot string) error {
	// Check if source is a directory - never copy directories
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to stat source: %v", err)
	}
	if info.IsDir() {
		return fmt.Errorf("cannot copy directory: %s", sourcePath)
	}

	// Calculate relative path
	relPath, err := filepath.Rel(sourceRoot, sourcePath)
	if err != nil {
		return fmt.Errorf("failed to calculate relative path: %v", err)
	}

	targetPath := filepath.Join(targetRoot, relPath)

	// Ensure parent directory exists
	if err := EnsureDir(filepath.Dir(targetPath)); err != nil {
		return err
	}

	// Choose cp flags based on OS
	// macOS: use -pc for COW optimization and preserve timestamps
	// Others: use -p to preserve timestamps/permissions
	cpFlags := "-p"
	if runtime.GOOS == "darwin" {
		cpFlags = "-pc"
	}

	cmd := exec.Command("cp", cpFlags, sourcePath, targetPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp failed: %v, output: %s", err, output)
	}

	return nil
}

// DeleteFile removes a file
func DeleteFile(path string) error {
	// Check if file exists
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Skip delete: file doesn't exist: %s\n", path)
			return nil
		}
		return fmt.Errorf("failed to stat file: %v", err)
	}

	// Check if it's a regular file (not a directory or symlink)
	if info.IsDir() {
		fmt.Fprintf(os.Stderr, "Skip delete: path is a directory: %s\n", path)
		return nil
	}

	if info.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(os.Stderr, "Skip delete: path is a symlink: %s\n", path)
		return nil
	}

	// It's a valid file, proceed with deletion
	return os.Remove(path)
}

// EnsureDir creates a directory and all parent directories if they don't exist
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// FilterFunc is a callback to determine if a file should be copied
type FilterFunc func(path string, info os.FileInfo) bool

// CopyTree copies all files from source to target, applying optional filter
// Returns the number of files copied, list of files to delete, and error
func CopyTree(source, target string, filterFunc FilterFunc) (copied int, filesToDelete []string, err error) {
	filesToCopy := make(map[string]bool)

	// Walk source directory and copy matching files
	err = filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}

		// Apply filter if provided
		if filterFunc != nil && !filterFunc(path, info) {
			return nil
		}

		filesToCopy[path] = true

		relPath, _ := filepath.Rel(source, path)
		targetPath := filepath.Join(target, relPath)

		// Compare and copy if different
		sourceInfo := GetFileInfo(path)
		targetInfo := GetFileInfo(targetPath)

		if !FilesEqual(sourceInfo, targetInfo) {
			if err := CopyFile(path, source, target); err == nil {
				copied++
			}
		}

		return nil
	})

	if err != nil {
		return 0, nil, err
	}

	// Find deleted files in target
	filepath.Walk(target, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(target, path)
		sourcePath := filepath.Join(source, relPath)

		if _, exists := filesToCopy[sourcePath]; !exists {
			if sourceInfo := GetFileInfo(sourcePath); !sourceInfo.Exists {
				filesToDelete = append(filesToDelete, relPath)
			}
		}

		return nil
	})

	return copied, filesToDelete, nil
}

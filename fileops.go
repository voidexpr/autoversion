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
// Returns (metadataChanged, error) where metadataChanged indicates if any metadata actually changed
func CopyFile(sourcePath, sourceRoot, targetRoot string, debug bool) (bool, error) {
	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG CopyFile: sourcePath=%s, sourceRoot=%s, targetRoot=%s\n", sourcePath, sourceRoot, targetRoot)
	}

	// Check if source is a directory - never copy directories
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: failed to stat source: %v\n", err)
		}
		return false, fmt.Errorf("failed to stat source: %v", err)
	}
	if sourceInfo.IsDir() {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: source is a directory, cannot copy\n")
		}
		return false, fmt.Errorf("cannot copy directory: %s", sourcePath)
	}

	// Calculate relative path
	relPath, err := filepath.Rel(sourceRoot, sourcePath)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: failed to calculate relative path: %v\n", err)
		}
		return false, fmt.Errorf("failed to calculate relative path: %v", err)
	}

	targetPath := filepath.Join(targetRoot, relPath)

	// Get target file info before copying
	targetInfoBefore, err := os.Stat(targetPath)
	targetExistsBefore := (err == nil)

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG CopyFile: targetPath=%s, relPath=%s\n", targetPath, relPath)

		// Compare source and target file attributes
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "DEBUG CopyFile: target file does not exist (new file)\n")
			} else {
				fmt.Fprintf(os.Stderr, "DEBUG CopyFile: error stating target: %v\n", err)
			}
		} else {
			// Both files exist, compare attributes
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: comparing source vs target:\n")

			// Size
			if sourceInfo.Size() != targetInfoBefore.Size() {
				fmt.Fprintf(os.Stderr, "  - Size differs: source=%d, target=%d (diff=%d bytes)\n",
					sourceInfo.Size(), targetInfoBefore.Size(), sourceInfo.Size()-targetInfoBefore.Size())
			} else {
				fmt.Fprintf(os.Stderr, "  - Size: %d bytes (same)\n", sourceInfo.Size())
			}

			// ModTime
			if sourceInfo.ModTime() != targetInfoBefore.ModTime() {
				fmt.Fprintf(os.Stderr, "  - ModTime differs: source=%s, target=%s\n",
					sourceInfo.ModTime().Format("2006-01-02 15:04:05.000"),
					targetInfoBefore.ModTime().Format("2006-01-02 15:04:05.000"))
			} else {
				fmt.Fprintf(os.Stderr, "  - ModTime: %s (same)\n", sourceInfo.ModTime().Format("2006-01-02 15:04:05.000"))
			}

			// Permissions
			if sourceInfo.Mode().Perm() != targetInfoBefore.Mode().Perm() {
				fmt.Fprintf(os.Stderr, "  - Permissions differ: source=%s, target=%s\n",
					sourceInfo.Mode().Perm(), targetInfoBefore.Mode().Perm())
			} else {
				fmt.Fprintf(os.Stderr, "  - Permissions: %s (same)\n", sourceInfo.Mode().Perm())
			}
		}
	}

	// Ensure parent directory exists
	if err := EnsureDir(filepath.Dir(targetPath)); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: failed to ensure parent directory: %v\n", err)
		}
		return false, err
	}

	// Choose cp flags based on OS
	// macOS: use -c for COW optimization and -p to preserve timestamps
	// Others: use -p to preserve timestamps/permissions
	// Note: exec.Command properly handles special characters in paths
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: executing cp -p -c %q %q\n", sourcePath, targetPath)
		}
		cmd = exec.Command("cp", "-p", "-c", "--", sourcePath, targetPath)
	} else {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: executing cp -p %q %q\n", sourcePath, targetPath)
		}
		cmd = exec.Command("cp", "-p", "--", sourcePath, targetPath)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: cp failed: %v, output: %s\n", err, output)
		}
		return false, fmt.Errorf("cp failed: %v, output: %s", err, output)
	}

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG CopyFile: successfully copied file\n")
	}

	// Get target file info after copying
	targetInfoAfter, err := os.Stat(targetPath)
	if err != nil {
		// Copy succeeded but can't stat - assume changed
		return true, nil
	}

	// Determine if metadata actually changed
	metadataChanged := false
	if !targetExistsBefore {
		// New file created
		metadataChanged = true
	} else {
		// Check if any metadata changed
		if targetInfoBefore.Size() != targetInfoAfter.Size() ||
			targetInfoBefore.ModTime() != targetInfoAfter.ModTime() ||
			targetInfoBefore.Mode().Perm() != targetInfoAfter.Mode().Perm() {
			metadataChanged = true
		}
	}

	if !metadataChanged {
		fmt.Printf("Info: no metadata change detected after copy for %s\n", relPath)
	}

	return metadataChanged, nil
}

// DeleteFile removes a file
func DeleteFile(path string, debug bool) error {
	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG DeleteFile: path=%s\n", path)
	}

	// Check if file exists
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if debug {
				fmt.Fprintf(os.Stderr, "DEBUG DeleteFile: file doesn't exist\n")
			}
			fmt.Fprintf(os.Stderr, "Skip delete: file doesn't exist: %s\n", path)
			return nil
		}
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG DeleteFile: failed to stat file: %v\n", err)
		}
		return fmt.Errorf("failed to stat file: %v", err)
	}

	// Check if it's a regular file (not a directory or symlink)
	if info.IsDir() {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG DeleteFile: path is a directory\n")
		}
		fmt.Fprintf(os.Stderr, "Skip delete: path is a directory: %s\n", path)
		return nil
	}

	if info.Mode()&os.ModeSymlink != 0 {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG DeleteFile: path is a symlink\n")
		}
		fmt.Fprintf(os.Stderr, "Skip delete: path is a symlink: %s\n", path)
		return nil
	}

	// It's a valid file, proceed with deletion
	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG DeleteFile: executing os.Remove\n")
	}
	err = os.Remove(path)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG DeleteFile: os.Remove failed: %v\n", err)
		}
		return err
	}

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG DeleteFile: successfully deleted file\n")
	}

	return nil
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
			if changed, err := CopyFile(path, source, target, false); err == nil && changed {
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

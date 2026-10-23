package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// errDestMissing is returned when the destination root disappeared (renamed,
// unmounted) after startup: it is never recreated
var errDestMissing = errors.New("destination disappeared")

// checkDestExists returns an errDestMissing error if destRoot no longer exists
func checkDestExists(destRoot string) error {
	if _, err := os.Stat(destRoot); os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", errDestMissing, destRoot)
	}
	return nil
}

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
// destRoot: the destination root
// Returns (metadataChanged, error) where metadataChanged indicates if any metadata actually changed
func CopyFile(sourcePath, sourceRoot, destRoot string, debug bool) (bool, error) {
	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG CopyFile: sourcePath=%s, sourceRoot=%s, destRoot=%s\n", sourcePath, sourceRoot, destRoot)
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

	destPath := filepath.Join(destRoot, relPath)

	// Get destination file info before copying
	destInfoBefore, err := os.Stat(destPath)
	destExistsBefore := (err == nil)

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG CopyFile: destPath=%s, relPath=%s\n", destPath, relPath)

		// Compare source and destination file attributes
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "DEBUG CopyFile: destination file does not exist (new file)\n")
			} else {
				fmt.Fprintf(os.Stderr, "DEBUG CopyFile: error stating destination: %v\n", err)
			}
		} else {
			// Both files exist, compare attributes
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: comparing source vs destination:\n")

			// Size
			if sourceInfo.Size() != destInfoBefore.Size() {
				fmt.Fprintf(os.Stderr, "  - Size differs: source=%d, destination=%d (diff=%d bytes)\n",
					sourceInfo.Size(), destInfoBefore.Size(), sourceInfo.Size()-destInfoBefore.Size())
			} else {
				fmt.Fprintf(os.Stderr, "  - Size: %d bytes (same)\n", sourceInfo.Size())
			}

			// ModTime
			if sourceInfo.ModTime() != destInfoBefore.ModTime() {
				fmt.Fprintf(os.Stderr, "  - ModTime differs: source=%s, destination=%s\n",
					sourceInfo.ModTime().Format("2006-01-02 15:04:05.000"),
					destInfoBefore.ModTime().Format("2006-01-02 15:04:05.000"))
			} else {
				fmt.Fprintf(os.Stderr, "  - ModTime: %s (same)\n", sourceInfo.ModTime().Format("2006-01-02 15:04:05.000"))
			}

			// Permissions
			if sourceInfo.Mode().Perm() != destInfoBefore.Mode().Perm() {
				fmt.Fprintf(os.Stderr, "  - Permissions differ: source=%s, destination=%s\n",
					sourceInfo.Mode().Perm(), destInfoBefore.Mode().Perm())
			} else {
				fmt.Fprintf(os.Stderr, "  - Permissions: %s (same)\n", sourceInfo.Mode().Perm())
			}
		}
	}

	// Create missing parent directories, but never destRoot itself
	parentDir := filepath.Dir(destPath)
	if _, err := os.Stat(parentDir); os.IsNotExist(err) {
		if err := checkDestExists(destRoot); err != nil {
			return false, err
		}
		if err := EnsureDir(parentDir); err != nil {
			if debug {
				fmt.Fprintf(os.Stderr, "DEBUG CopyFile: failed to ensure parent directory: %v\n", err)
			}
			return false, err
		}
	}

	// Choose cp flags based on OS
	// macOS: use -c for COW optimization and -p to preserve timestamps
	// Others: use -p to preserve timestamps/permissions
	// Note: exec.Command properly handles special characters in paths
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: executing cp -p -c %q %q\n", sourcePath, destPath)
		}
		cmd = exec.Command("cp", "-p", "-c", "--", sourcePath, destPath)
	} else {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG CopyFile: executing cp -p %q %q\n", sourcePath, destPath)
		}
		cmd = exec.Command("cp", "-p", "--", sourcePath, destPath)
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

	// Get destination file info after copying
	destInfoAfter, err := os.Stat(destPath)
	if err != nil {
		// Copy succeeded but can't stat - assume changed
		return true, nil
	}

	// Determine if metadata actually changed
	metadataChanged := false
	if !destExistsBefore {
		// New file created
		metadataChanged = true
	} else {
		// Check if any metadata changed
		if destInfoBefore.Size() != destInfoAfter.Size() ||
			destInfoBefore.ModTime() != destInfoAfter.ModTime() ||
			destInfoBefore.Mode().Perm() != destInfoAfter.Mode().Perm() {
			metadataChanged = true
		}
	}

	if !metadataChanged {
		fmt.Printf("Info: no metadata change detected after copy for %s\n", relPath)
	}

	return metadataChanged, nil
}

// EnsureDir creates a directory and all parent directories if they don't exist
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

package main

import (
	"fmt"
	"io"
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

	// Use optimized copy on macOS
	if runtime.GOOS == "darwin" {
		return copyFileOptimized(sourcePath, targetPath)
	}

	// Fallback to standard copy
	return copyFileStandard(sourcePath, targetPath)
}

// copyFileOptimized uses cp -pc on macOS for better performance
func copyFileOptimized(src, dst string) error {
	cmd := exec.Command("cp", "-pc", src, dst)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp failed: %v, output: %s", err, output)
	}
	return nil
}

// copyFileStandard is a standard Go file copy
func copyFileStandard(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	// Copy permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, sourceInfo.Mode())
}

// DeleteFile removes a file
func DeleteFile(path string) error {
	return os.Remove(path)
}

// EnsureDir creates a directory and all parent directories if they don't exist
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

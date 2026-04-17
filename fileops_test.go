package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile_NewFile(t *testing.T) {
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	srcPath := filepath.Join(srcRoot, "hello.txt")
	if err := os.WriteFile(srcPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := CopyFile(srcPath, srcRoot, dstRoot, false)
	if err != nil {
		t.Fatalf("CopyFile error: %v", err)
	}
	if !changed {
		t.Error("expected metadataChanged=true for new file")
	}

	dstPath := filepath.Join(dstRoot, "hello.txt")
	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("destination not created: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("destination content = %q, want %q", data, "hello")
	}
}

func TestCopyFile_SkipWhenIdentical(t *testing.T) {
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	srcPath := filepath.Join(srcRoot, "same.txt")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	// First copy: file is new at destination.
	if _, err := CopyFile(srcPath, srcRoot, dstRoot, false); err != nil {
		t.Fatalf("first CopyFile error: %v", err)
	}

	dstPath := filepath.Join(dstRoot, "same.txt")
	srcInfo := GetFileInfo(srcPath)
	dstInfo := GetFileInfo(dstPath)

	if !FilesEqual(srcInfo, dstInfo) {
		t.Skip("cp -p did not preserve mtime; cannot test FilesEqual skipping on this platform")
	}

	// Second copy: metadata unchanged, metadataChanged should be false.
	changed, err := CopyFile(srcPath, srcRoot, dstRoot, false)
	if err != nil {
		t.Fatalf("second CopyFile error: %v", err)
	}
	if changed {
		t.Error("expected metadataChanged=false when file is identical after copy")
	}
}

func TestCopyFile_WhenContentDiffers(t *testing.T) {
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	srcPath := filepath.Join(srcRoot, "file.txt")
	dstPath := filepath.Join(dstRoot, "file.txt")

	if err := os.WriteFile(srcPath, []byte("version1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dstPath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := CopyFile(srcPath, srcRoot, dstRoot, false)
	if err != nil {
		t.Fatalf("CopyFile error: %v", err)
	}
	if !changed {
		t.Error("expected metadataChanged=true when content differs")
	}

	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "version1" {
		t.Errorf("destination content = %q, want %q", data, "version1")
	}
}

func TestFilesEqual_TrueWhenSame(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	info := GetFileInfo(path)
	if !FilesEqual(info, info) {
		t.Error("expected FilesEqual to return true for same FileInfo")
	}
}

func TestFilesEqual_FalseWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(path, []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	existing := GetFileInfo(path)
	missing := GetFileInfo(filepath.Join(dir, "missing.txt"))

	if FilesEqual(existing, missing) {
		t.Error("expected FilesEqual to return false when one file is missing")
	}
	if FilesEqual(missing, existing) {
		t.Error("expected FilesEqual to return false when one file is missing")
	}
}

func TestEnsureDir_ExistingDir(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureDir(dir); err != nil {
		t.Errorf("EnsureDir on existing dir returned error: %v", err)
	}
}

func TestEnsureDir_MissingDir(t *testing.T) {
	base := t.TempDir()
	newDir := filepath.Join(base, "a", "b", "c")
	if err := EnsureDir(newDir); err != nil {
		t.Fatalf("EnsureDir failed: %v", err)
	}
	if info, err := os.Stat(newDir); err != nil || !info.IsDir() {
		t.Errorf("expected directory to be created at %s", newDir)
	}
}

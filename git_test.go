package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupGitRepo initialises a temp directory as a git repo with user config.
func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "."},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestInit(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, false); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !IsGitRepo(dir) {
		t.Error("Init() did not create a git repository")
	}
}

func TestAdd(t *testing.T) {
	dir := setupGitRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Add(dir, []string{"test.txt"}, false); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git diff: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "test.txt") {
		t.Errorf("Add() did not stage test.txt; diff output: %s", out)
	}
}

func TestCommit(t *testing.T) {
	dir := setupGitRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Add(dir, []string{"hello.txt"}, false); err != nil {
		t.Fatal(err)
	}

	if err := Commit(dir, "initial commit"); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "initial commit") {
		t.Errorf("Commit() message not found in log: %s", out)
	}
}

func TestCommit_NothingToCommit(t *testing.T) {
	dir := setupGitRepo(t)

	// Make an initial commit so the repo has a HEAD.
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Add(dir, []string{"seed.txt"}, false); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "seed"); err != nil {
		t.Fatal(err)
	}

	// Nothing new staged — Commit should return nil, not an error.
	if err := Commit(dir, "nothing new"); err != nil {
		t.Fatalf("Commit() with nothing to commit returned error: %v", err)
	}
}

func TestRemove(t *testing.T) {
	dir := setupGitRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "toremove.txt"), []byte("bye"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Add(dir, []string{"toremove.txt"}, false); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "add file"); err != nil {
		t.Fatal(err)
	}

	oldNoGit := noGit
	noGit = false
	defer func() { noGit = oldNoGit }()

	if err := Remove(dir, []string{"toremove.txt"}, false); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "toremove.txt")); !os.IsNotExist(err) {
		t.Error("Remove() did not delete file from disk")
	}

	// Commit the removal and verify the file is no longer tracked.
	if err := Commit(dir, "remove file"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "ls-files", "toremove.txt")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-files: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("Remove() left file in git index: %s", out)
	}
}

// TestRunGitBatched_MultiBatch verifies that Add handles a file list large
// enough to exceed the 256 KB per-batch byte budget (forcing multiple git
// invocations) and that every file ends up committed.
//
// Budget: 262144 bytes. Each path is "d/<200-char name>" = 202 chars,
// need = 203 bytes. Prefix ("add", "--") = 7 bytes.
// Files per batch ≈ (262144-7)/203 ≈ 1291. 1300 files → 2 batches.
func TestRunGitBatched_MultiBatch(t *testing.T) {
	dir := setupGitRepo(t)

	const numFiles = 1300
	const nameLen = 200
	subdir := filepath.Join(dir, "d")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	paths := make([]string, numFiles)
	for i := 0; i < numFiles; i++ {
		// 10-digit zero-padded index + "_" + (nameLen-11) a's = nameLen chars
		name := fmt.Sprintf("%010d_%s", i, strings.Repeat("a", nameLen-11))
		paths[i] = filepath.Join("d", name)
		if err := os.WriteFile(filepath.Join(dir, paths[i]), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Add(dir, paths, false); err != nil {
		t.Fatalf("Add() large batch error = %v", err)
	}
	if err := Commit(dir, "large batch commit"); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	// All files must appear in the tracked-files list after the commit.
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-files: %v\n%s", err, out)
	}
	lsOutput := string(out)

	missing := 0
	for _, p := range paths {
		// git ls-files uses forward slashes on all platforms
		if !strings.Contains(lsOutput, filepath.ToSlash(p)) {
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("git ls-files missing %d/%d files after large-batch commit", missing, numFiles)
	}
}

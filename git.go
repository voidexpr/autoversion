package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func log_and_err(format string, args ...interface{}) error {
    err := fmt.Errorf(format, args...)
    logError("Error: %v", err)
    return err
}

// runGit runs a git command and prints its output
func runGit(repoPath string, args ...string) (string, error) {
	logGit("git command in %s args: %v\n", repoPath, args)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()

	outputStr := string(output)
	if outputStr != "" {
		logGit("%s", outputStr)
	}

	return outputStr, err
}

// Init initializes a git repository at the given path
func Init(repoPath string, debug bool) error {
	_, err := runGit(repoPath, "init", ".")
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG Init: git init failed: %v\n", err)
		}
		return log_and_err("git init failed: %v", err)
	}
	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG Init: successfully initialized git repository\n")
	}
	return nil
}

// Add adds files to the git index, handling .gitignore errors gracefully
func Add(repoPath string, paths []string, debug bool) error {
	if len(paths) == 0 {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG Add: no paths to add\n")
		}
		return nil
	}

	// Add -- to separate paths from options, protecting special file names
	args := append([]string{"add", "--"}, paths...)
	_, err := runGit(repoPath, args...)

	// If some files are in .gitignore, git add will fail for those
	// but we want to continue with the others
	if err != nil {
		return log_and_err("git add failed: %v", err)
	}
	return nil
}

// Commit creates a commit with the given message, with retry logic
func Commit(repoPath string, message string) error {
	output, err := runGit(repoPath, "commit", ".", "-m", message)
	if err != nil {
		// Check if it's because there's nothing to commit
		if strings.Contains(output, "nothing to commit") {
			return nil
		}

		return log_and_err("git commit failed: %v", err)
	}

	return nil
}

// Remove removes files from the target directory (and from git if enabled)
func Remove(repoPath string, paths []string, debug bool) error {
	if len(paths) == 0 {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG Remove: no paths to remove\n")
		}
		return nil
	}

	if !noGit {
		args := append([]string{"rm", "--ignore-unmatch", "-f", "--"}, paths...)
		_, err := runGit(repoPath, args...)
		if err != nil {
			return log_and_err("git rm failed: %v", err)
		}
	}

	// Delete any remaining files (handles files git rm couldn't touch,
	// e.g. .git worktree files, or all files when --no-git is set)
	for _, relPath := range paths {
		fullPath := filepath.Join(repoPath, relPath)
		if rmErr := os.Remove(fullPath); rmErr != nil && !os.IsNotExist(rmErr) {
			logError("Remove: failed to delete %s: %v", relPath, rmErr)
		} else if rmErr == nil && debug {
			fmt.Fprintf(os.Stderr, "DEBUG Remove: deleted %s\n", relPath)
		}
	}

	return nil
}

// IsGitRepo checks if a directory is a valid git repository
func IsGitRepo(repoPath string) bool {
	gitDir := filepath.Join(repoPath, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// hasPendingCommit checks if there's a pending git commit by looking for git processes
func hasPendingCommit(repoPath string) bool {
	// Check for .git/index.lock which indicates a pending git operation
	lockFile := repoPath + "/.git/index.lock"
	if _, err := os.Stat(lockFile); err == nil {
		return true
	}
	return false
}

// hasChanges checks if there are uncommitted changes
func hasChanges(repoPath string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	return len(strings.TrimSpace(string(output))) > 0
}

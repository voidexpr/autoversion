package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const retryDelay = 5 * time.Second

// Init initializes a git repository at the given path
func Init(repoPath string) error {
	cmd := exec.Command("git", "init", ".")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init failed: %v, output: %s", err, output)
	}
	return nil
}

// Add adds files to the git index, handling .gitignore errors gracefully
func Add(repoPath string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	// Try to add all paths
	args := append([]string{"add"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()

	// If some files are in .gitignore, git add will fail for those
	// but we want to continue with the others
	if err != nil {
		outputStr := string(output)
		// Check if it's a .gitignore error
		if strings.Contains(outputStr, "ignored") || strings.Contains(outputStr, ".gitignore") {
			fmt.Fprintf(os.Stderr, "Warning: some files ignored by .gitignore: %s\n", outputStr)
			// Continue - other files may have been added
			return nil
		}
		return fmt.Errorf("git add failed: %v, output: %s", err, output)
	}

	return nil
}

// Commit creates a commit with the given message, with retry logic
func Commit(repoPath string, message string) error {
	for {
		// Check if there's a pending commit
		if hasPendingCommit(repoPath) {
			fmt.Fprintf(os.Stderr, "Pending git commit detected, waiting %v...\n", retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		// Check if there are changes to commit
		if !hasChanges(repoPath) {
			return nil
		}

		cmd := exec.Command("git", "commit", "-m", message)
		cmd.Dir = repoPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			outputStr := string(output)
			// Check if it's because there's nothing to commit
			if strings.Contains(outputStr, "nothing to commit") {
				return nil
			}

			fmt.Fprintf(os.Stderr, "git commit failed: %v, output: %s, retrying in %v...\n",
				err, outputStr, retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		return nil
	}
}

// Remove removes files from git
func Remove(repoPath string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	for {
		args := append([]string{"rm"}, paths...)
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			outputStr := string(output)
			// File might not exist in git - that's ok
			if strings.Contains(outputStr, "did not match any files") {
				return nil
			}

			fmt.Fprintf(os.Stderr, "git rm failed: %v, output: %s, retrying in %v...\n",
				err, outputStr, retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		return nil
	}
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

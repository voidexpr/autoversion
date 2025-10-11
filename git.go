package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const retryDelay = 5 * time.Second

// runGit runs a git command and prints its output
func runGit(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()

	outputStr := string(output)
	if outputStr != "" {
		fmt.Print(outputStr)
	}

	return outputStr, err
}

// Init initializes a git repository at the given path
func Init(repoPath string) error {
	_, err := runGit(repoPath, "init", ".")
	if err != nil {
		return fmt.Errorf("git init failed: %v", err)
	}
	return nil
}

// Add adds files to the git index, handling .gitignore errors gracefully
func Add(repoPath string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"add"}, paths...)
	output, err := runGit(repoPath, args...)

	// If some files are in .gitignore, git add will fail for those
	// but we want to continue with the others
	if err != nil {
		// Check if it's a .gitignore error
		if strings.Contains(output, "ignored") || strings.Contains(output, ".gitignore") {
			// Continue - other files may have been added
			return nil
		}
		return fmt.Errorf("git add failed: %v", err)
	}

	return nil
}

// Commit creates a commit with the given message, with retry logic
func Commit(repoPath string, message string) error {
	output, err := runGit(repoPath, "commit", "-m", message)

	if err != nil {
		// Check if it's because there's nothing to commit
		if strings.Contains(output, "nothing to commit") {
			return nil
		}

		return fmt.Errorf("git commit failed: %v", err)
	}

	return nil

	// for {
	// 	// // Check if there's a pending commit
	// 	// if hasPendingCommit(repoPath) {
	// 	// 	fmt.Fprintf(os.Stderr, "Pending git commit detected, waiting %v...\n", retryDelay)
	// 	// 	time.Sleep(retryDelay)
	// 	// 	continue
	// 	// }

	// 	// Check if there are changes to commit
	// 	if !hasChanges(repoPath) {
	// 		return nil
	// 	}

	// 	output, err := runGit(repoPath, "commit", "-m", message)

	// 	if err != nil {
	// 		// Check if it's because there's nothing to commit
	// 		if strings.Contains(output, "nothing to commit") {
	// 			return nil
	// 		}

	// 		fmt.Fprintf(os.Stderr, "git commit failed: %v, retrying in %v...\n", err, retryDelay)
	// 		time.Sleep(retryDelay)
	// 		continue
	// 	}

	// 	return nil
	// }
}

// Remove removes files from git
func Remove(repoPath string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"rm"}, paths...)
	output, err := runGit(repoPath, args...)

	if err != nil {
		// File might not exist in git - that's ok
		if strings.Contains(output, "did not match any files") {
			return nil
		}

		return fmt.Errorf("git rm failed: %v", err)
	}

	return nil

	// for {
	// 	args := append([]string{"rm"}, paths...)
	// 	output, err := runGit(repoPath, args...)

	// 	if err != nil {
	// 		// File might not exist in git - that's ok
	// 		if strings.Contains(output, "did not match any files") {
	// 			return nil
	// 		}

	// 		fmt.Fprintf(os.Stderr, "git rm failed: %v, retrying in %v...\n", err, retryDelay)
	// 		time.Sleep(retryDelay)
	// 		continue
	// 	}

	// 	return nil
	// }
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

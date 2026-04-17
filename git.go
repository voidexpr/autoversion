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

// Target argv size per git invocation. Well under Darwin ARG_MAX (~1 MB)
// so env and prefix flags fit with plenty of headroom.
const gitArgBytesBudget = 256 * 1024

func runGitBatched(repoPath string, prefixArgs []string, paths []string) error {
	prefixBytes := 0
	for _, a := range prefixArgs {
		prefixBytes += len(a) + 1
	}
	args := make([]string, 0, len(prefixArgs)+64)
	i := 0
	for i < len(paths) {
		args = append(args[:0], prefixArgs...)
		bytes := prefixBytes
		start := i
		for i < len(paths) {
			need := len(paths[i]) + 1
			// Always include at least one path per batch so a single
			// oversized path still makes progress.
			if len(args) > len(prefixArgs) && bytes+need > gitArgBytesBudget {
				break
			}
			args = append(args, paths[i])
			bytes += need
			i++
		}
		if _, err := runGit(repoPath, args...); err != nil {
			return fmt.Errorf("batch %d-%d of %d: %w", start, i, len(paths), err)
		}
	}
	return nil
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

	if err := runGitBatched(repoPath, []string{"add", "--"}, paths); err != nil {
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
		if err := runGitBatched(repoPath,
			[]string{"rm", "--ignore-unmatch", "-f", "--"}, paths); err != nil {
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


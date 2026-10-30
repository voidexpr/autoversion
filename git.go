package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func logAndErr(format string, args ...interface{}) error {
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

	if err != nil {
		return outputStr, fmt.Errorf("%w (repo=%s args=%v output=%q)",
			err, repoPath, args, strings.TrimSpace(outputStr))
	}
	return outputStr, nil
}

// gitOutput runs a git command that changes nothing and returns its output,
// without printing it
func gitOutput(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%w (repo=%s args=%v output=%q)", err, repoPath, args, strings.TrimSpace(string(output)))
	}
	return string(output), nil
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
		return logAndErr("git init failed: %v", err)
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
		return logAndErr("git add failed: %v", err)
	}
	return nil
}

// Commit creates a commit with the given message, with retry logic
func Commit(repoPath string, message string) error {
	return CommitPaths(repoPath, message, ".")
}

// CommitPaths commits the changes to the given paths only
func CommitPaths(repoPath string, message string, paths ...string) error {
	args := append([]string{"commit", "-m", message, "--"}, paths...)
	output, err := runGit(repoPath, args...)
	if err != nil {
		// Check if it's because there's nothing to commit
		if strings.Contains(output, "nothing to commit") {
			return nil
		}

		return logAndErr("git commit failed: %v", err)
	}

	return nil
}

// Remove removes files from the destination directory (and from git if enabled).
// Returns an errDestMissing error if the destination itself is gone.
func Remove(repoPath string, paths []string, debug bool) error {
	if len(paths) == 0 {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG Remove: no paths to remove\n")
		}
		return nil
	}

	if !noGit {
		if err := runGitBatched(repoPath,
			[]string{"rm", "-r", "--ignore-unmatch", "-f", "--"}, paths); err != nil {
			if missingErr := checkDestExists(repoPath); missingErr != nil {
				return missingErr
			}
			return logAndErr("git rm failed: %v", err)
		}
	}

	// Delete any remaining files (handles files git rm couldn't touch,
	// e.g. .git worktree files, or all files when --no-git is set)
	for _, relPath := range paths {
		fullPath := filepath.Join(repoPath, relPath)
		rmErr := os.Remove(fullPath)
		switch {
		case rmErr == nil:
			if debug {
				fmt.Fprintf(os.Stderr, "DEBUG Remove: deleted %s\n", relPath)
			}
		case os.IsNotExist(rmErr):
			// already gone, unless the whole destination is
			if missingErr := checkDestExists(repoPath); missingErr != nil {
				return missingErr
			}
		case errors.Is(rmErr, syscall.ENOTEMPTY):
			// FSEvents can deliver a directory's delete before its
			// children's; those later events drain the directory.
			if debug {
				fmt.Fprintf(os.Stderr, "DEBUG Remove: %s not empty, leaving\n", relPath)
			}
		default:
			logError("Remove: failed to delete %s: %v", relPath, rmErr)
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

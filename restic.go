package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// computeResticPaths returns (repoPath, passwordPath) from destPath
// destPath: path/to/dirname/ (the destination directory)
// Returns: (path/to/.restic_dirname/repo, path/to/.restic_password_dirname)
func computeResticPaths(destPath string) (repoPath string, passwordPath string) {
	// Clean the path to handle trailing slashes
	cleanPath := filepath.Clean(destPath)

	// Get the directory name (last component)
	dirName := filepath.Base(cleanPath)

	// Get the parent directory
	parentDir := filepath.Dir(cleanPath)

	// Build paths
	repoPath = filepath.Join(parentDir, ".restic_"+dirName, "repo")
	passwordPath = filepath.Join(parentDir, ".restic_password_"+dirName)

	return repoPath, passwordPath
}

// generatePassword creates a random 32-character alphanumeric password
func generatePassword() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 32

	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to a simpler approach if crypto/rand fails
		panic(fmt.Sprintf("failed to generate random password: %v", err))
	}

	for i, b := range bytes {
		bytes[i] = chars[b%byte(len(chars))]
	}

	return string(bytes)
}

// runRestic executes restic commands with repo and password paths
func runRestic(repoPath, passwordPath string, args ...string) (string, error) {
	// Check if restic is installed
	if _, err := exec.LookPath("restic"); err != nil {
		return "", fmt.Errorf("restic not found in PATH, install from https://restic.net/")
	}

	// Build command with repo and password-file flags
	cmdArgs := []string{"--repo", repoPath, "--password-file", passwordPath}
	cmdArgs = append(cmdArgs, args...)

	fmt.Fprintf(os.Stdout, "restic command: %v\n", cmdArgs)

	cmd := exec.Command("restic", cmdArgs...)
	output, err := cmd.CombinedOutput()

	outputStr := string(output)
	if outputStr != "" {
		fmt.Print(outputStr)
	}

	return outputStr, err
}

// InitRestic initializes restic repository and generates password
// Creates .restic_dirname/repo and .restic_password_dirname as siblings to destPath
func InitRestic(destPath string, debug bool) error {
	repoPath, passwordPath := computeResticPaths(destPath)

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG InitRestic: destPath=%s, repoPath=%s, passwordPath=%s\n",
			destPath, repoPath, passwordPath)
	}

	// Check if repo already exists
	if _, err := os.Stat(repoPath); err == nil {
		if debug {
			fmt.Fprintf(os.Stderr, "DEBUG InitRestic: repository already exists\n")
		}
		fmt.Printf("Restic repository already initialized at %s\n", repoPath)
		return nil
	}

	// Generate password
	password := generatePassword()

	// Ensure parent directory exists for password file
	passwordDir := filepath.Dir(passwordPath)
	if err := EnsureDir(passwordDir); err != nil {
		return fmt.Errorf("failed to create password directory: %v", err)
	}

	// Write password file with mode 0600
	if err := os.WriteFile(passwordPath, []byte(password), 0600); err != nil {
		return fmt.Errorf("failed to write password file: %v", err)
	}

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG InitRestic: password file created at %s\n", passwordPath)
	}

	// Ensure parent directory exists for repo
	repoDir := filepath.Dir(repoPath)
	if err := EnsureDir(repoDir); err != nil {
		return fmt.Errorf("failed to create repo directory: %v", err)
	}

	// Initialize restic repository
	_, err := runRestic(repoPath, passwordPath, "init")
	if err != nil {
		// Clean up password file if init fails
		if rmErr := os.Remove(passwordPath); rmErr != nil {
			logError("failed to clean up password file: %v", rmErr)
		}
		return fmt.Errorf("restic init failed: %v", err)
	}

	fmt.Printf("Initialized restic repository at %s\n", repoPath)
	return nil
}

// IsResticRepo checks if .restic_dirname/repo exists
func IsResticRepo(destPath string) bool {
	repoPath, _ := computeResticPaths(destPath)
	info, err := os.Stat(repoPath)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// BackupRestic creates a snapshot of destPath (excluding .git/)
func BackupRestic(destPath string, debug bool) error {
	repoPath, passwordPath := computeResticPaths(destPath)

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG BackupRestic: backing up %s\n", destPath)
	}

	// Check if password file exists
	if _, err := os.Stat(passwordPath); os.IsNotExist(err) {
		return fmt.Errorf("password file not found at %s", passwordPath)
	}

	// Backup with .git exclusion
	_, err := runRestic(repoPath, passwordPath, "backup", destPath, "--exclude", ".git")
	if err != nil {
		return fmt.Errorf("restic backup failed: %v", err)
	}

	return nil
}

// ForgetRestic applies retention policy and prunes
func ForgetRestic(destPath string, debug bool) error {
	repoPath, passwordPath := computeResticPaths(destPath)

	if debug {
		fmt.Fprintf(os.Stderr, "DEBUG ForgetRestic: applying retention policy\n")
	}

	// Check if password file exists
	if _, err := os.Stat(passwordPath); os.IsNotExist(err) {
		return fmt.Errorf("password file not found at %s", passwordPath)
	}

	// Apply retention policy with prune
	_, err := runRestic(repoPath, passwordPath,
		"forget",
		"--keep-within", "30d",
		"--keep-weekly", "52",
		"--keep-monthly", "999999",
		"--prune")
	if err != nil {
		return fmt.Errorf("restic forget failed: %v", err)
	}

	return nil
}

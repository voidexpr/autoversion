package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const appBundleName = "Autoversion.app"

// sanitizePath converts a path to a safe identifier component
// Replaces non-alphanumeric chars with underscores, collapses multiple underscores
func sanitizePath(path string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	s := re.ReplaceAllString(path, "_")
	s = strings.Trim(s, "_")
	return s
}

// jobIdentifier returns the launchd job label for a source/target pair
func jobIdentifier(sourcePath, targetPath string) string {
	srcSan := sanitizePath(sourcePath)
	tgtSan := sanitizePath(targetPath)
	return fmt.Sprintf("projects.autoversion.%s_to_%s", srcSan, tgtSan)
}

// launchAgentDir returns the user's LaunchAgents directory
func launchAgentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// plistPath returns the path to the plist file for a job
func plistPath(jobID string) (string, error) {
	agentDir, err := launchAgentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(agentDir, jobID+".plist"), nil
}

// logDir returns the log directory for a job
func logDir(jobID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "autoversion", jobID), nil
}

// promptPath prompts the user for a path with validation
func promptPath(prompt string, mustExist bool) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(prompt + ": ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		path := strings.TrimSpace(input)
		if path == "" {
			fmt.Println("Path cannot be empty")
			continue
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			fmt.Printf("Invalid path: %v\n", err)
			continue
		}

		if mustExist {
			info, err := os.Stat(absPath)
			if err != nil {
				fmt.Printf("Path does not exist: %s\n", absPath)
				continue
			}
			if !info.IsDir() {
				fmt.Printf("Path is not a directory: %s\n", absPath)
				continue
			}
		}

		return absPath, nil
	}
}

// generatePlist creates the launchd plist content
// basePath is the original --base value (not the computed target), so watch can re-compute the target correctly
func generatePlist(jobID, execPath, sourcePath, basePath, logDir string, maxFileSize int64, bufferTime int, noGit, useRestic, debug, noLogin bool, includeExts string, excludeDirs []string) string {
	stdoutLog := filepath.Join(logDir, "stdout.log")
	stderrLog := filepath.Join(logDir, "stderr.log")

	args := []string{
		execPath,
		"watch",
		"--base", basePath,
		"--max-file-size", fmt.Sprintf("%d", maxFileSize),
		"--buffer-time", fmt.Sprintf("%d", bufferTime),
	}
	if noGit {
		args = append(args, "--no-git")
	}
	if useRestic {
		args = append(args, "--restic")
	}
	if debug {
		args = append(args, "--debug")
	}
	if includeExts != "" {
		args = append(args, "--only-include-extensions", includeExts)
	}
	for _, d := range excludeDirs {
		args = append(args, "--exclude-dir", d)
	}
	args = append(args, sourcePath)

	// Build ProgramArguments XML
	var argsXML strings.Builder
	for _, arg := range args {
		argsXML.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", escapeXML(arg)))
	}

	runAtLoad := "true"
	if noLogin {
		runAtLoad = "false"
	}

	// ThrottleInterval=21600 caps auto-restart attempts at one per 6 h, so a
	// persistent crash doesn't hot-loop. Healthy long-lived runs still relaunch
	// instantly after a crash (throttle measured spawn-to-spawn).
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>RunAtLoad</key>
	<%s/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>21600</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>WorkingDirectory</key>
	<string>%s</string>
</dict>
</plist>
`, jobID, argsXML.String(), runAtLoad, stdoutLog, stderrLog, sourcePath)

	return plist
}

// unescapeXML reverses escapeXML. Process &amp; last to avoid double-unescaping.
func unescapeXML(s string) string {
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&apos;", "'")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}

// parsePlistExecPath reads an existing plist file and extracts the executable
// path (first <string> in ProgramArguments).
func parsePlistExecPath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	arrayStart := strings.Index(content, "<array>")
	arrayEnd := strings.Index(content, "</array>")
	if arrayStart == -1 || arrayEnd == -1 {
		return "", fmt.Errorf("no ProgramArguments array found in %s", path)
	}
	re := regexp.MustCompile(`<string>(.*?)</string>`)
	match := re.FindStringSubmatch(content[arrayStart:arrayEnd])
	if match == nil {
		return "", fmt.Errorf("no string values in ProgramArguments in %s", path)
	}
	return unescapeXML(match[1]), nil
}

// parsePlistPaths reads an existing plist file and extracts the source path
// (last <string> in ProgramArguments) and base path (value after --base).
func parsePlistPaths(path string) (source, base string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	content := string(data)

	// Extract all <string>...</string> values inside <array>...</array>
	arrayStart := strings.Index(content, "<array>")
	arrayEnd := strings.Index(content, "</array>")
	if arrayStart == -1 || arrayEnd == -1 {
		return "", "", fmt.Errorf("no ProgramArguments array found in %s", path)
	}
	arrayContent := content[arrayStart:arrayEnd]

	re := regexp.MustCompile(`<string>(.*?)</string>`)
	matches := re.FindAllStringSubmatch(arrayContent, -1)
	if len(matches) == 0 {
		return "", "", fmt.Errorf("no string values in ProgramArguments in %s", path)
	}

	// Source path is the last argument
	source = unescapeXML(matches[len(matches)-1][1])

	// Base path is the value after --base
	for i, m := range matches {
		if unescapeXML(m[1]) == "--base" && i+1 < len(matches) {
			base = unescapeXML(matches[i+1][1])
			break
		}
	}

	return source, base, nil
}

// escapeXML escapes special XML characters
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// isJobLoaded checks if a launchd job is loaded
func isJobLoaded(jobID string) bool {
	cmd := exec.Command("launchctl", "list", jobID)
	err := cmd.Run()
	return err == nil
}

// getExecutablePath returns the path to the current executable
func getExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// localAppBundlePath returns the path to the app bundle in the source directory (next to the binary)
func localAppBundlePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeDir := filepath.Dir(exe)
	return filepath.Join(exeDir, appBundleName), nil
}

// installedAppBundlePath returns the path to the app bundle in ~/Applications
func installedAppBundlePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Applications", appBundleName), nil
}

// appBundleExecutable returns the path to the executable inside the installed app bundle
func appBundleExecutable() (string, error) {
	appPath, err := installedAppBundlePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(appPath, "Contents", "MacOS", "autoversion-wrapper"), nil
}

// isLocalAppBundleCreated checks if the local app bundle exists (in source dir)
func isLocalAppBundleCreated() bool {
	appPath, err := localAppBundlePath()
	if err != nil {
		return false
	}
	_, err = os.Stat(appPath)
	return err == nil
}

// localWrapperPath returns the path to the local bundle's wrapper script
func localWrapperPath() (string, error) {
	appPath, err := localAppBundlePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(appPath, "Contents", "MacOS", "autoversion-wrapper"), nil
}

// installedWrapperPath returns the path to the installed bundle's wrapper script
func installedWrapperPath() (string, error) {
	appPath, err := installedAppBundlePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(appPath, "Contents", "MacOS", "autoversion-wrapper"), nil
}

// getBinaryMtime returns the modification time of the autoversion binary
func getBinaryMtime() (time.Time, error) {
	exe, err := getExecutablePath()
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(exe)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// getLocalWrapperMtime returns the mtime of the local bundle's wrapper script
func getLocalWrapperMtime() (time.Time, error) {
	wrapperPath, err := localWrapperPath()
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(wrapperPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// getInstalledWrapperMtime returns the mtime of the installed bundle's wrapper
func getInstalledWrapperMtime() (time.Time, error) {
	wrapperPath, err := installedWrapperPath()
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(wrapperPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// getPlistMtime returns the mtime of a plist file
func getPlistMtime(plistPath string) (time.Time, error) {
	info, err := os.Stat(plistPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// needsLocalBundle returns true if local bundle should be created/updated
func needsLocalBundle() bool {
	if !isLocalAppBundleCreated() {
		return true
	}
	binaryMtime, err := getBinaryMtime()
	if err != nil {
		return true
	}
	wrapperMtime, err := getLocalWrapperMtime()
	if err != nil {
		return true
	}
	return binaryMtime.After(wrapperMtime)
}

// needsInstalledBundleUpdate returns true if installed bundle needs updating
func needsInstalledBundleUpdate() bool {
	if !isAppBundleInstalled() {
		return true
	}
	localMtime, err := getLocalWrapperMtime()
	if err != nil {
		return true
	}
	installedMtime, err := getInstalledWrapperMtime()
	if err != nil {
		return true
	}
	return localMtime.After(installedMtime)
}

// needsServiceReload returns true if the running service should be reloaded
func needsServiceReload(plistPath string) bool {
	binaryMtime, err := getBinaryMtime()
	if err != nil {
		return false
	}
	plistMtime, err := getPlistMtime(plistPath)
	if err != nil {
		return false
	}
	return binaryMtime.After(plistMtime)
}

// createLocalBundle creates/updates the local Autoversion.app bundle
// Returns true if bundle was created/updated, false if already up-to-date
func createLocalBundle() (bool, error) {
	execPath, err := getExecutablePath()
	if err != nil {
		return false, fmt.Errorf("failed to get executable path: %v", err)
	}

	appPath, err := localAppBundlePath()
	if err != nil {
		return false, err
	}

	contentsDir := filepath.Join(appPath, "Contents")
	macosDir := filepath.Join(contentsDir, "MacOS")
	resourcesDir := filepath.Join(contentsDir, "Resources")

	for _, dir := range []string{macosDir, resourcesDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return false, fmt.Errorf("failed to create directory %s: %v", dir, err)
		}
	}

	infoPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>autoversion-wrapper</string>
	<key>CFBundleIdentifier</key>
	<string>projects.autoversion</string>
	<key>CFBundleName</key>
	<string>Autoversion</string>
	<key>CFBundleDisplayName</key>
	<string>Autoversion</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleVersion</key>
	<string>1.0</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0</string>
	<key>LSBackgroundOnly</key>
	<true/>
	<key>LSUIElement</key>
	<true/>
</dict>
</plist>
`
	if err := os.WriteFile(filepath.Join(contentsDir, "Info.plist"), []byte(infoPlist), 0644); err != nil {
		return false, fmt.Errorf("failed to write Info.plist: %v", err)
	}

	wrapperScript := fmt.Sprintf(`#!/bin/bash
# Autoversion wrapper for Full Disk Access
# This script executes the autoversion binary with all arguments passed through.
# The app bundle is granted FDA, which is inherited by the executed binary.

AUTOVERSION_BIN="%s"

if [ ! -x "$AUTOVERSION_BIN" ]; then
    echo "Error: autoversion binary not found at $AUTOVERSION_BIN" >&2
    echo "Please rebuild autoversion and run 'autoversion bundle' again." >&2
    exit 1
fi

exec "$AUTOVERSION_BIN" "$@"
`, execPath)

	wrapperPath := filepath.Join(macosDir, "autoversion-wrapper")
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0755); err != nil {
		return false, fmt.Errorf("failed to write wrapper script: %v", err)
	}

	return true, nil
}

// isAppBundleInstalled checks if the app bundle is installed in ~/Applications
func isAppBundleInstalled() bool {
	appPath, err := installedAppBundlePath()
	if err != nil {
		return false
	}
	_, err = os.Stat(appPath)
	return err == nil
}

// getEffectiveExecutable returns the app bundle wrapper if installed, otherwise the direct executable
func getEffectiveExecutable() (string, error) {
	if isAppBundleInstalled() {
		return appBundleExecutable()
	}
	return getExecutablePath()
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
}

// installAppBundle copies the local app bundle to ~/Applications
func installAppBundle() error {
	localPath, err := localAppBundlePath()
	if err != nil {
		return err
	}

	installedPath, err := installedAppBundlePath()
	if err != nil {
		return err
	}

	// Remove existing if present
	if _, err := os.Stat(installedPath); err == nil {
		if err := os.RemoveAll(installedPath); err != nil {
			return fmt.Errorf("failed to remove existing app bundle: %v", err)
		}
	}

	// Ensure ~/Applications exists
	if err := os.MkdirAll(filepath.Dir(installedPath), 0755); err != nil {
		return fmt.Errorf("failed to create ~/Applications: %v", err)
	}

	// Copy the bundle
	if err := copyDir(localPath, installedPath); err != nil {
		return fmt.Errorf("failed to copy app bundle: %v", err)
	}

	return nil
}

var installCmd = &cobra.Command{
	Use:   "install <source-path>",
	Short: "Install autoversion as a launchd service (macOS only)",
	Long:  "Configures launchd to run autoversion in watch mode. Requires --base flag.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("install command is only supported on macOS (launchd). Linux systemd support coming later")
		}

		if basePath == "" {
			return fmt.Errorf("--base flag is required")
		}

		// Run comprehensive source/base validation
		result, err := ValidateSourceAndBase(args[0], basePath)
		if err != nil {
			return err
		}

		// Print path resolution (same as check-paths)
		fmt.Println("Path Resolution:")
		fmt.Printf("  source (input):    %s\n", result.SourceInput)
		if result.SourceInput != result.SourceResolved {
			fmt.Printf("  source (resolved): %s\n", result.SourceResolved)
		} else {
			fmt.Printf("  source (resolved): (no symlinks)\n")
		}
		fmt.Printf("  base (input):      %s\n", result.BaseInput)
		if result.BaseInput != result.BaseResolved {
			fmt.Printf("  base (resolved):   %s\n", result.BaseResolved)
		} else {
			fmt.Printf("  base (resolved):   (no symlinks)\n")
		}
		fmt.Printf("  target (computed): %s\n", result.TargetComputed)
		fmt.Println()

		if len(result.Errors) > 0 {
			fmt.Println("Validation Errors:")
			for _, e := range result.Errors {
				fmt.Printf("  ✗ %s\n", e)
			}
			return fmt.Errorf("path validation failed with %d error(s)", len(result.Errors))
		}

		sourcePath := result.SourceResolved
		targetPath := result.TargetComputed

		// Verify additional path safety (HOME, root, etc.)
		if err := verifyPaths(sourcePath, targetPath); err != nil {
			return err
		}

		// Auto-create/update local bundle if needed
		if needsLocalBundle() {
			bundleExisted := isLocalAppBundleCreated()
			_, err := createLocalBundle()
			if err != nil {
				return fmt.Errorf("failed to create app bundle: %v", err)
			}
			if bundleExisted {
				fmt.Println("Updated app bundle")
			} else {
				fmt.Println("Created app bundle")
			}
		}

		// Generate job ID
		jobID := jobIdentifier(sourcePath, targetPath)

		// Check if already installed
		plist, err := plistPath(jobID)
		if err != nil {
			return err
		}

		if _, err := os.Stat(plist); err == nil {
			// Check for job ID collision: different paths producing the same sanitized ID
			existingSource, existingBase, parseErr := parsePlistPaths(plist)
			if parseErr == nil && (existingSource != sourcePath || existingBase != result.BaseResolved) {
				return fmt.Errorf("job ID collision: %s\n  Existing install:\n    source: %s\n    base:   %s\n  New install:\n    source: %s\n    base:   %s\nThese paths produce the same sanitized job ID.\nRename one of the source directories to avoid the collision.", jobID, existingSource, existingBase, sourcePath, result.BaseResolved)
			}

			// Update installed bundle if needed
			if needsInstalledBundleUpdate() {
				if err := installAppBundle(); err != nil {
					return fmt.Errorf("failed to update installed app bundle: %v", err)
				}
				fmt.Println("Updated installed app bundle")
			}

			// Check if plist content needs updating (flag changes, binary path changes)
			execPath, err := parsePlistExecPath(plist)
			if err != nil {
				return fmt.Errorf("failed to parse existing plist: %v", err)
			}
			maxSize, _ := cmd.Flags().GetInt64("max-file-size")
			bufTime, _ := cmd.Flags().GetInt("buffer-time")
			logDirPath, _ := logDir(jobID)
			newPlist := generatePlist(jobID, execPath, sourcePath, result.BaseResolved, logDirPath, maxSize, bufTime, noGit, useRestic, debug, noLogin, includeExtsFlag, excludeDirsFlag)
			existingPlist, _ := os.ReadFile(plist)

			needsReload := needsServiceReload(plist) || string(existingPlist) != newPlist

			if needsReload {
				if err := os.WriteFile(plist, []byte(newPlist), 0644); err != nil {
					return fmt.Errorf("failed to write plist: %v", err)
				}

				isRunning := isJobLoaded(jobID)
				if isRunning {
					unloadCmd := exec.Command("launchctl", "unload", plist)
					if output, err := unloadCmd.CombinedOutput(); err != nil {
						return fmt.Errorf("failed to unload launchd job: %v\n%s", err, output)
					}
				}

				// Clear stderr log so stale errors don't persist across reloads
				stderrPath := filepath.Join(logDirPath, "stderr.log")
				if err := os.Remove(stderrPath); err != nil && !os.IsNotExist(err) {
					logError("failed to remove stderr log: %v", err)
				}

				loadCmd := exec.Command("launchctl", "load", plist)
				if output, err := loadCmd.CombinedOutput(); err != nil {
					return fmt.Errorf("failed to load launchd job: %v\n%s", err, output)
				}

				fmt.Println("Updated and reloaded service")
				fmt.Println()
				printStatusInfo(jobID, sourcePath, targetPath)
				return nil
			}

			// No reload needed, just print status
			isRunning := isJobLoaded(jobID)
			if isRunning {
				fmt.Printf("Already installed and running: %s\n", jobID)
			} else {
				fmt.Printf("Already installed but not running: %s\n", jobID)
			}
			printStatusInfo(jobID, sourcePath, targetPath)
			return nil
		}

		// New install: update installed bundle if needed
		var execPath string
		firstInstall := false
		if isLocalAppBundleCreated() {
			if needsInstalledBundleUpdate() {
				if err := installAppBundle(); err != nil {
					return fmt.Errorf("failed to install app bundle: %v", err)
				}
				if !isAppBundleInstalled() {
					firstInstall = true
				}
			}
			execPath, err = appBundleExecutable()
			if err != nil {
				return fmt.Errorf("failed to get app bundle executable: %v", err)
			}
			installedApp, _ := installedAppBundlePath()
			fmt.Printf("Installed app bundle: %s\n", installedApp)
			if firstInstall {
				fmt.Println("Grant Full Disk Access to this app in System Preferences > Privacy & Security")
			}
			fmt.Println()
		} else {
			execPath, err = getExecutablePath()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %v", err)
			}
		}

		// Create log directory
		logDirPath, err := logDir(jobID)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(logDirPath, 0755); err != nil {
			return fmt.Errorf("failed to create log directory: %v", err)
		}

		// Create target, initialize git, and run initial sync
		if err := validateTargetPath(targetPath, result.BaseResolved, sourcePath, dryRun); err != nil {
			return err
		}

		// Get flag values from command
		maxSize, _ := cmd.Flags().GetInt64("max-file-size")

		SetSourceRoot(sourcePath)

		if !dryRun {
			fmt.Println("Initial sync...")
			if err := scanPaths(sourcePath, targetPath, maxSize, dryRun); err != nil {
				return fmt.Errorf("initial sync failed: %v", err)
			}
		}

		// Create LaunchAgents directory if needed
		agentDir, err := launchAgentDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			return fmt.Errorf("failed to create LaunchAgents directory: %v", err)
		}
		bufTime, _ := cmd.Flags().GetInt("buffer-time")

		// Generate plist - pass original base path so watch can correctly compute target
		plistContent := generatePlist(jobID, execPath, sourcePath, result.BaseResolved, logDirPath, maxSize, bufTime, noGit, useRestic, debug, noLogin, includeExtsFlag, excludeDirsFlag)

		if dryRun {
			fmt.Println("=== Generated plist (dry-run) ===")
			fmt.Print(plistContent)
			fmt.Println("=== End plist ===")
			fmt.Printf("\nWould install to: %s\n", plist)
			fmt.Printf("Would create log dir: %s\n", logDirPath)
			return nil
		}

		if err := os.WriteFile(plist, []byte(plistContent), 0644); err != nil {
			return fmt.Errorf("failed to write plist: %v", err)
		}

		// Load the job
		loadCmd := exec.Command("launchctl", "load", plist)
		if output, err := loadCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to load launchd job: %v\n%s", err, output)
		}

		fmt.Printf("Installed and started: %s\n\n", jobID)
		fmt.Printf("Watching: %s\n", sourcePath)
		fmt.Printf("Target:   %s\n\n", targetPath)

		printStatusInfo(jobID, sourcePath, targetPath)
		return nil
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall <source-path>",
	Short: "Uninstall autoversion launchd service",
	Long:  "Stops and removes the launchd job for the specified source path.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("uninstall command is only supported on macOS")
		}

		if basePath == "" {
			return fmt.Errorf("--base flag is required to identify the job to uninstall")
		}

		// Use ValidateSourceAndBase to get properly resolved paths
		result, err := ValidateSourceAndBase(args[0], basePath)
		if err != nil {
			return err
		}

		sourcePath := result.SourceResolved
		targetPath := result.TargetComputed
		jobID := jobIdentifier(sourcePath, targetPath)
		plist, err := plistPath(jobID)
		if err != nil {
			return err
		}

		// Check if installed
		if _, err := os.Stat(plist); os.IsNotExist(err) {
			return fmt.Errorf("not installed: %s", jobID)
		}

		// Unload if loaded
		if isJobLoaded(jobID) {
			unloadCmd := exec.Command("launchctl", "unload", plist)
			if output, err := unloadCmd.CombinedOutput(); err != nil {
				logError("failed to unload job: %v\n%s", err, output)
			} else {
				fmt.Println("Stopped service")
			}
		}

		// Remove plist
		if err := os.Remove(plist); err != nil {
			return fmt.Errorf("failed to remove plist: %v", err)
		}

		fmt.Printf("Uninstalled: %s\n", jobID)
		return nil
	},
}

// listAllAutoversionJobs finds all autoversion plist files and prints a summary
func listAllAutoversionJobs() error {
	agentDir, err := launchAgentDir()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(agentDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No autoversion services installed")
			return nil
		}
		return err
	}

	var plists []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "projects.autoversion.") && strings.HasSuffix(e.Name(), ".plist") {
			plists = append(plists, e.Name())
		}
	}

	if len(plists) == 0 {
		fmt.Println("No autoversion services installed")
		return nil
	}

	for i, plistName := range plists {
		if i > 0 {
			fmt.Println()
		}
		jobID := strings.TrimSuffix(plistName, ".plist")
		plistFile := filepath.Join(agentDir, plistName)

		source, _, parseErr := parsePlistPaths(plistFile)

		pid, exitStatus, running := getJobInfo(jobID)

		if running {
			uptime := formatUptime(getProcessUptime(pid))
			fmt.Printf("%-10s %s (pid %s, uptime %s)\n", "RUNNING", jobID, pid, uptime)
		} else {
			fmt.Printf("%-10s %s (exit %s)\n", "STOPPED", jobID, exitStatus)
		}
		if parseErr == nil {
			fmt.Printf("           source: %s\n", source)
		}
	}

	return nil
}

var statusCmd = &cobra.Command{
	Use:   "status [source-path]",
	Short: "Show status of autoversion launchd service(s)",
	Long:  "With no arguments, lists all autoversion services. With a source path and --base, shows detailed status.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("status command is only supported on macOS")
		}

		if len(args) == 0 {
			return listAllAutoversionJobs()
		}

		if basePath == "" {
			return fmt.Errorf("--base flag is required to identify the job")
		}

		// Use ValidateSourceAndBase to get properly resolved paths
		result, err := ValidateSourceAndBase(args[0], basePath)
		if err != nil {
			return err
		}

		sourcePath := result.SourceResolved
		targetPath := result.TargetComputed
		jobID := jobIdentifier(sourcePath, targetPath)
		plist, err := plistPath(jobID)
		if err != nil {
			return err
		}

		// Check if installed
		if _, err := os.Stat(plist); os.IsNotExist(err) {
			fmt.Printf("Status: NOT INSTALLED\n")
			fmt.Printf("Source: %s\n", sourcePath)
			fmt.Printf("Target: %s\n", targetPath)
			fmt.Printf("Job ID: %s\n", jobID)
			return nil
		}

		// Check if running
		running := isJobLoaded(jobID)
		if running {
			fmt.Printf("Status: RUNNING\n")
		} else {
			fmt.Printf("Status: INSTALLED (not running)\n")
		}

		fmt.Printf("Source: %s\n", sourcePath)
		fmt.Printf("Target: %s\n\n", targetPath)

		printStatusInfo(jobID, sourcePath, targetPath)
		return nil
	},
}

// countLines returns the number of lines in a file, or -1 if the file doesn't exist
func countLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}
	return count
}

// lastLines returns the last n lines of a file
func lastLines(path string, n int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}

// printLogFile prints a log file path with line count, and optionally the last n lines
func printLogFile(label, path string, showTail bool) {
	count := countLines(path)
	if count < 0 {
		fmt.Printf("  %s: %s (not found)\n", label, path)
		return
	}
	fmt.Printf("  %s: %s (%d lines)\n", label, path, count)
	if showTail && count > 0 {
		lines := lastLines(path, 10)
		for _, line := range lines {
			fmt.Printf("      %s\n", line)
		}
	}
}

// getJobInfo runs launchctl list for a job and returns PID and status
func getJobInfo(jobID string) (pid string, status string, running bool) {
	cmd := exec.Command("launchctl", "list", jobID)
	output, err := cmd.Output()
	if err != nil {
		return "-", "-", false
	}

	// Parse dictionary output format:
	//   "PID" = 5229;
	//   "LastExitStatus" = 0;
	content := string(output)
	pid = "-"
	status = "-"

	// Extract PID
	if pidIdx := strings.Index(content, "\"PID\""); pidIdx != -1 {
		line := content[pidIdx:]
		if eqIdx := strings.Index(line, "="); eqIdx != -1 {
			rest := strings.TrimSpace(line[eqIdx+1:])
			if semiIdx := strings.Index(rest, ";"); semiIdx != -1 {
				pid = strings.TrimSpace(rest[:semiIdx])
				running = true
			}
		}
	}

	// Extract LastExitStatus
	if statusIdx := strings.Index(content, "\"LastExitStatus\""); statusIdx != -1 {
		line := content[statusIdx:]
		if eqIdx := strings.Index(line, "="); eqIdx != -1 {
			rest := strings.TrimSpace(line[eqIdx+1:])
			if semiIdx := strings.Index(rest, ";"); semiIdx != -1 {
				status = strings.TrimSpace(rest[:semiIdx])
			}
		}
	}

	return pid, status, running
}

// printStatusInfo prints log locations and control commands
func printStatusInfo(jobID, sourcePath, targetPath string) {
	logDirPath, _ := logDir(jobID)
	plist, _ := plistPath(jobID)

	// Get and show process info
	pid, status, running := getJobInfo(jobID)
	fmt.Printf("Process:\n")
	if running {
		uptime := formatUptime(getProcessUptime(pid))
		fmt.Printf("  PID:     %s\n", pid)
		fmt.Printf("  Status:  %s (running)\n", status)
		fmt.Printf("  Uptime:  %s\n", uptime)
	} else {
		fmt.Printf("  PID:     - (not running)\n")
		fmt.Printf("  Status:  %s\n", status)
	}
	fmt.Println()

	fmt.Printf("Logs:\n")
	printLogFile("stdout", filepath.Join(logDirPath, "stdout.log"), false)
	printLogFile("stderr", filepath.Join(logDirPath, "stderr.log"), true)
	fmt.Println()

	fmt.Printf("Commands:\n")
	fmt.Printf("  Status:  launchctl list %s\n", jobID)
	fmt.Printf("  Stop:    launchctl unload %s\n", plist)
	fmt.Printf("  Start:   launchctl load %s\n", plist)
	fmt.Printf("  Remove:  autoversion uninstall --base <BASE> %s\n", sourcePath)
}

var bundleCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Create macOS app bundle for Full Disk Access support",
	Long: `Creates Autoversion.app in the same directory as the autoversion binary.

This app bundle can be granted Full Disk Access in System Preferences,
which persists across CLI rebuilds. The install command will automatically
copy this bundle to ~/Applications and use it.

After running 'autoversion install':
1. Open System Preferences > Privacy & Security > Full Disk Access
2. Click + and add ~/Applications/Autoversion.app
3. The service will have Full Disk Access`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("bundle command is only supported on macOS")
		}

		_, err := createLocalBundle()
		if err != nil {
			return err
		}

		appPath, _ := localAppBundlePath()
		execPath, _ := getExecutablePath()

		fmt.Printf("Created app bundle: %s\n", appPath)
		fmt.Printf("Wrapper points to: %s\n\n", execPath)
		fmt.Println("The bundle will be copied to ~/Applications when you run 'autoversion install'.")
		fmt.Println("After install, grant Full Disk Access to ~/Applications/Autoversion.app")
		fmt.Println()
		fmt.Println("If you rebuild autoversion, run 'autoversion bundle' again to update the wrapper.")

		return nil
	},
}

var unbundleCmd = &cobra.Command{
	Use:   "unbundle",
	Short: "Remove the macOS app bundles",
	Long:  "Removes Autoversion.app from both the local directory and ~/Applications.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("unbundle command is only supported on macOS")
		}

		removed := false

		// Remove local bundle
		localPath, err := localAppBundlePath()
		if err == nil {
			if _, err := os.Stat(localPath); err == nil {
				if err := os.RemoveAll(localPath); err != nil {
					logError("failed to remove local app bundle: %v", err)
				} else {
					fmt.Printf("Removed: %s\n", localPath)
					removed = true
				}
			}
		}

		// Remove installed bundle
		installedPath, err := installedAppBundlePath()
		if err == nil {
			if _, err := os.Stat(installedPath); err == nil {
				if err := os.RemoveAll(installedPath); err != nil {
					logError("failed to remove installed app bundle: %v", err)
				} else {
					fmt.Printf("Removed: %s\n", installedPath)
					removed = true
				}
			}
		}

		if !removed {
			fmt.Println("No app bundles found")
			return nil
		}

		fmt.Println("\nNote: Any installed services will continue running but may lose FDA.")
		fmt.Println("To update services to use the direct binary, reinstall them:")
		fmt.Println("  autoversion uninstall --base <BASE> <SOURCE>")
		fmt.Println("  autoversion install --base <BASE> <SOURCE>")

		return nil
	},
}

// getProcessUptime returns how long a process has been running
func getProcessUptime(pid string) string {
	if pid == "-" || pid == "" {
		return "-"
	}
	// ps -o etime= gives elapsed time in [[DD-]HH:]MM:SS format
	cmd := exec.Command("ps", "-o", "etime=", "-p", pid)
	output, err := cmd.Output()
	if err != nil {
		return "-"
	}
	etime := strings.TrimSpace(string(output))
	if etime == "" {
		return "-"
	}
	return etime
}

// formatUptime converts ps etime format to human readable
func formatUptime(etime string) string {
	if etime == "-" {
		return "-"
	}
	// Format: [[DD-]HH:]MM:SS
	// Examples: "05:23", "01:05:23", "2-01:05:23"
	parts := strings.Split(etime, "-")
	var days, timepart string
	if len(parts) == 2 {
		days = parts[0]
		timepart = parts[1]
	} else {
		timepart = parts[0]
	}

	timeParts := strings.Split(timepart, ":")
	var hours, mins, secs string
	switch len(timeParts) {
	case 3:
		hours = timeParts[0]
		mins = timeParts[1]
		secs = timeParts[2]
	case 2:
		mins = timeParts[0]
		secs = timeParts[1]
	default:
		return etime
	}

	var result []string
	if days != "" && days != "0" {
		result = append(result, days+"d")
	}
	if hours != "" && hours != "00" {
		result = append(result, strings.TrimLeft(hours, "0")+"h")
	}
	if mins != "" && mins != "00" {
		result = append(result, strings.TrimLeft(mins, "0")+"m")
	}
	if len(result) == 0 {
		// Less than a minute
		s := strings.TrimLeft(secs, "0")
		if s == "" {
			s = "0"
		}
		return s + "s"
	}
	return strings.Join(result, " ")
}

var installCheckCmd = &cobra.Command{
	Use:   "install-check <source-path>",
	Short: "Check installed service status and show recent logs",
	Long:  "Shows detailed status of an installed service including PID, uptime, and recent log output.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("install-check command is only supported on macOS")
		}

		if basePath == "" {
			return fmt.Errorf("--base flag is required to identify the job")
		}

		// Use ValidateSourceAndBase to get properly resolved paths
		result, err := ValidateSourceAndBase(args[0], basePath)
		if err != nil {
			return err
		}

		sourcePath := result.SourceResolved
		targetPath := result.TargetComputed
		jobID := jobIdentifier(sourcePath, targetPath)
		plist, err := plistPath(jobID)
		if err != nil {
			return err
		}

		// Check if installed
		if _, err := os.Stat(plist); os.IsNotExist(err) {
			return fmt.Errorf("not installed: %s", jobID)
		}

		// Get process info
		pid, status, running := getJobInfo(jobID)

		fmt.Printf("Job:    %s\n", jobID)
		fmt.Printf("Source: %s\n", sourcePath)
		fmt.Printf("Target: %s\n\n", targetPath)

		if running {
			uptime := getProcessUptime(pid)
			fmt.Printf("PID:    %s\n", pid)
			fmt.Printf("Status: running (exit code %s)\n", status)
			fmt.Printf("Uptime: %s\n", formatUptime(uptime))
		} else {
			fmt.Printf("PID:    - (not running)\n")
			fmt.Printf("Status: stopped (exit code %s)\n", status)
		}
		fmt.Println()

		// Show log files with last 10 lines
		logDirPath, _ := logDir(jobID)

		stdoutPath := filepath.Join(logDirPath, "stdout.log")
		stderrPath := filepath.Join(logDirPath, "stderr.log")

		fmt.Printf("=== stdout: %s ===\n", stdoutPath)
		lines := lastLines(stdoutPath, 10)
		if len(lines) == 0 {
			fmt.Println("(empty)")
		} else {
			for _, line := range lines {
				fmt.Println(line)
			}
		}
		fmt.Println()

		fmt.Printf("=== stderr: %s ===\n", stderrPath)
		lines = lastLines(stderrPath, 10)
		if len(lines) == 0 {
			fmt.Println("(empty)")
		} else {
			for _, line := range lines {
				fmt.Println(line)
			}
		}

		return nil
	},
}

func init() {
	// Install command flags (same as watch)
	installCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	installCmd.Flags().IntVar(&bufferTime, "buffer-time", defaultBufferTime, "buffer time in seconds")
	installCmd.Flags().BoolVar(&noLogin, "no-login", false, "don't start service at login")
	installCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show generated plist without installing")

	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(bundleCmd)
	rootCmd.AddCommand(unbundleCmd)
	rootCmd.AddCommand(installCheckCmd)
}

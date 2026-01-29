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
func generatePlist(jobID, execPath, sourcePath, targetPath, logDir string, maxFileSize int64, bufferTime int, noGit, useRestic, debug bool) string {
	stdoutLog := filepath.Join(logDir, "stdout.log")
	stderrLog := filepath.Join(logDir, "stderr.log")
	errLog := filepath.Join(logDir, "errors.log")

	args := []string{
		execPath,
		"watch",
		"--base", targetPath,
		"--max-file-size", fmt.Sprintf("%d", maxFileSize),
		"--buffer-time", fmt.Sprintf("%d", bufferTime),
		"--err-log", errLog,
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
	args = append(args, sourcePath)

	// Build ProgramArguments XML
	var argsXML strings.Builder
	for _, arg := range args {
		argsXML.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", escapeXML(arg)))
	}

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
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>WorkingDirectory</key>
	<string>%s</string>
</dict>
</plist>
`, jobID, argsXML.String(), stdoutLog, stderrLog, sourcePath)

	return plist
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
	Use:   "install [source-path]",
	Short: "Install autoversion as a launchd service (macOS only)",
	Long:  "Configures launchd to run autoversion in watch mode. Prompts for paths if not provided.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("install command is only supported on macOS (launchd). Linux systemd support coming later")
		}

		// Get source path
		var sourcePath string
		var err error
		if len(args) > 0 {
			sourcePath, err = filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("invalid source path: %v", err)
			}
			if err := validateSourcePath(sourcePath); err != nil {
				return err
			}
		} else {
			sourcePath, err = promptPath("Which path to watch", true)
			if err != nil {
				return err
			}
		}

		// Resolve symlinks in source path
		sourcePath, err = resolvePath(sourcePath)
		if err != nil {
			return err
		}

		// Get base/target path
		var targetPath string
		if basePath != "" {
			targetPath, err = computeTargetPath(basePath, sourcePath)
			if err != nil {
				return err
			}
		} else {
			baseInput, err := promptPath("Where should the versioned files be stored", false)
			if err != nil {
				return err
			}
			targetPath, err = computeTargetPath(baseInput, sourcePath)
			if err != nil {
				return err
			}
		}

		// Validate paths don't overlap
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
			// Service is already installed
			isRunning := isJobLoaded(jobID)
			needsReload := isRunning && needsServiceReload(plist)

			if needsReload {
				// Update installed bundle if needed
				if needsInstalledBundleUpdate() {
					if err := installAppBundle(); err != nil {
						return fmt.Errorf("failed to update installed app bundle: %v", err)
					}
					fmt.Println("Updated installed app bundle")
				}

				// Reload service: unload then load
				unloadCmd := exec.Command("launchctl", "unload", plist)
				if output, err := unloadCmd.CombinedOutput(); err != nil {
					return fmt.Errorf("failed to unload launchd job: %v\n%s", err, output)
				}

				// Touch the plist to update its mtime
				now := time.Now()
				if err := os.Chtimes(plist, now, now); err != nil {
					logError("failed to update plist mtime: %v", err)
				}

				loadCmd := exec.Command("launchctl", "load", plist)
				if output, err := loadCmd.CombinedOutput(); err != nil {
					return fmt.Errorf("failed to load launchd job: %v\n%s", err, output)
				}

				fmt.Println("Reloaded service (binary updated)")
				fmt.Println()
				printStatusInfo(jobID, sourcePath, targetPath)
				return nil
			}

			// No reload needed, just print status
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

		// Create target directory if needed
		if err := os.MkdirAll(targetPath, 0755); err != nil {
			return fmt.Errorf("failed to create target directory: %v", err)
		}

		// Create LaunchAgents directory if needed
		agentDir, err := launchAgentDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			return fmt.Errorf("failed to create LaunchAgents directory: %v", err)
		}

		// Get flag values from command
		maxSize, _ := cmd.Flags().GetInt64("max-file-size")
		bufTime, _ := cmd.Flags().GetInt("buffer-time")

		// Generate and write plist
		plistContent := generatePlist(jobID, execPath, sourcePath, targetPath, logDirPath, maxSize, bufTime, noGit, useRestic, debug)
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

		sourcePath, err := filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("invalid source path: %v", err)
		}
		sourcePath, err = resolvePath(sourcePath)
		if err != nil {
			return err
		}

		// Need base path to find the job
		if basePath == "" {
			return fmt.Errorf("--base flag is required to identify the job to uninstall")
		}

		targetPath, err := computeTargetPath(basePath, sourcePath)
		if err != nil {
			return err
		}

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

var statusCmd = &cobra.Command{
	Use:   "status <source-path>",
	Short: "Show status of autoversion launchd service",
	Long:  "Shows whether the service is installed and running, log locations, and control commands.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("status command is only supported on macOS")
		}

		sourcePath, err := filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("invalid source path: %v", err)
		}
		sourcePath, err = resolvePath(sourcePath)
		if err != nil {
			return err
		}

		// Need base path to find the job
		if basePath == "" {
			return fmt.Errorf("--base flag is required to identify the job")
		}

		targetPath, err := computeTargetPath(basePath, sourcePath)
		if err != nil {
			return err
		}

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

// printStatusInfo prints log locations and control commands
func printStatusInfo(jobID, sourcePath, targetPath string) {
	logDirPath, _ := logDir(jobID)
	plist, _ := plistPath(jobID)

	fmt.Printf("Logs:\n")
	fmt.Printf("  stdout: %s/stdout.log\n", logDirPath)
	fmt.Printf("  stderr: %s/stderr.log\n", logDirPath)
	fmt.Printf("  errors: %s/errors.log\n\n", logDirPath)

	fmt.Printf("Control commands:\n")
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

func init() {
	// Install command flags (same as watch)
	installCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB")
	installCmd.Flags().IntVar(&bufferTime, "buffer-time", defaultBufferTime, "buffer time in seconds")

	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(bundleCmd)
	rootCmd.AddCommand(unbundleCmd)
}

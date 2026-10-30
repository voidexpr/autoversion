package main

import (
	"bufio"
	_ "embed"
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

// makeFDAAppScript builds the launcher app, see ensureAppBundle
//
//go:embed make-fda-app.sh
var makeFDAAppScript string

// sanitizePath converts a path to a safe identifier component
// Replaces non-alphanumeric chars with underscores, collapses multiple underscores
func sanitizePath(path string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	s := re.ReplaceAllString(path, "_")
	s = strings.Trim(s, "_")
	return s
}

// jobIdentifier returns the launchd job label for a source/destination pair
func jobIdentifier(sourcePath, destPath string) string {
	srcSan := sanitizePath(sourcePath)
	destSan := sanitizePath(destPath)
	return fmt.Sprintf("projects.autoversion.%s_to_%s", srcSan, destSan)
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

// generatePlist creates the launchd plist content. It has the runtime flags
// only: the settings and the rules are read from the destination.
func generatePlist(jobID, execPath, sourcePath, destPath, logDir string, bufferTime int, debug, noLogin bool) string {
	stdoutLog := filepath.Join(logDir, stdoutLogName)
	stderrLog := filepath.Join(logDir, stderrLogName)

	args := []string{
		execPath,
		"watch",
		"--source", sourcePath,
		"--destination", destPath,
		"--buffer-time", fmt.Sprintf("%d", bufferTime),
		"--log-dir", logDir,
	}
	if debug {
		args = append(args, "--debug")
	}

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

// parsePlistPaths reads an existing plist file and extracts the values of
// --source and --destination in ProgramArguments
func parsePlistPaths(path string) (source, dest string, err error) {
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

	for i := 0; i+1 < len(matches); i++ {
		switch unescapeXML(matches[i][1]) {
		case "--source":
			source = unescapeXML(matches[i+1][1])
		case "--destination":
			dest = unescapeXML(matches[i+1][1])
		}
	}
	if source == "" || dest == "" {
		return "", "", fmt.Errorf("no --source and --destination in ProgramArguments in %s", path)
	}

	return source, dest, nil
}

// jobPaths resolves --source and --destination, which identify a launchd job.
// They don't have to exist, so a job can be removed after its destination is gone.
func jobPaths() (sourcePath, destPath string, err error) {
	if err := requirePaths(true); err != nil {
		return "", "", err
	}
	if sourcePath, err = resolvePath(sourceFlag); err != nil {
		return "", "", err
	}
	if destPath, err = resolvePath(destFlag); err != nil {
		return "", "", err
	}
	return sourcePath, destPath, nil
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
	// make-fda-app.sh names the executable after the app
	return filepath.Join(appPath, "Contents", "MacOS", strings.TrimSuffix(appBundleName, ".app")), nil
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

// getPlistMtime returns the mtime of a plist file
func getPlistMtime(plistPath string) (time.Time, error) {
	info, err := os.Stat(plistPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
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

// ensureAppBundle creates or updates ~/Applications/Autoversion.app, a small
// signed launcher that runs this binary and stays its parent, so autoversion
// inherits the Full Disk Access granted to the app. Rebuilding the binary in
// place keeps the app (and the grant); it is only rebuilt if the binary moves.
func ensureAppBundle() error {
	appPath, err := installedAppBundlePath()
	if err != nil {
		return err
	}
	execPath, err := getExecutablePath()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	// make-fda-app.sh refuses to replace an app it didn't create (no
	// launcher.c), so remove bundles made by older versions of autoversion
	if _, err := os.Stat(appPath); err == nil {
		if _, err := os.Stat(filepath.Join(appPath, "Contents", "Resources", "launcher.c")); os.IsNotExist(err) {
			if err := os.RemoveAll(appPath); err != nil {
				return fmt.Errorf("failed to remove legacy app bundle: %v", err)
			}
			fmt.Printf("Removed legacy app bundle: %s\n", appPath)
		}
	}

	cmd := exec.Command("bash", "-c", makeFDAAppScript, "make-fda-app.sh", appPath, execPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create app bundle: %v", err)
	}
	return nil
}

// reloadJob unloads a launchd job if it is loaded, clears its stderr log so
// stale errors don't persist across reloads, and loads it
func reloadJob(jobID, plist, logDirPath string) error {
	if isJobLoaded(jobID) {
		unloadCmd := exec.Command("launchctl", "unload", plist)
		if output, err := unloadCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to unload launchd job: %v\n%s", err, output)
		}
	}

	if err := removeLogs(filepath.Join(logDirPath, stderrLogName)); err != nil {
		logError("failed to remove stderr log: %v", err)
	}

	loadCmd := exec.Command("launchctl", "load", plist)
	if output, err := loadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to load launchd job: %v\n%s", err, output)
	}
	return nil
}

// restartJob restarts the running launchd job of a source and destination,
// if there is one, so it reads the destination's new rules. A variable, so
// tests never run launchctl.
var restartJob = func(sourcePath, destPath string) error {
	if runtime.GOOS != "darwin" {
		fmt.Println("Restart the watch of this destination, if one runs, to apply the rules")
		return nil
	}
	jobID := jobIdentifier(sourcePath, destPath)
	plist, err := plistPath(jobID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(plist); err != nil {
		return nil
	}
	if !isJobLoaded(jobID) {
		fmt.Printf("Service %s is installed but not running: the rules apply when it starts\n", jobID)
		return nil
	}
	logDirPath, err := logDir(jobID)
	if err != nil {
		return err
	}
	if err := reloadJob(jobID, plist, logDirPath); err != nil {
		return err
	}
	fmt.Printf("Restarted service %s\n", jobID)
	return nil
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install autoversion as a launchd service (macOS only)",
	Long:  "Configures launchd to run autoversion in watch mode. The destination must be initialized for the source (see init); the service reads its settings and rules from it.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("install command is only supported on macOS (launchd). Linux systemd support coming later")
		}

		if err := requirePaths(true); err != nil {
			return err
		}

		// Same validation and output as check-paths
		result, err := checkPaths(sourceFlag, destFlag)
		if err != nil {
			return err
		}
		useDestination(result)

		sourcePath := result.SourceResolved
		destPath := result.DestinationResolved

		// The service runs through the launcher app that holds Full Disk Access
		if err := ensureAppBundle(); err != nil {
			return err
		}
		execPath, err := appBundleExecutable()
		if err != nil {
			return err
		}
		fmt.Println()

		// Generate job ID
		jobID := jobIdentifier(sourcePath, destPath)

		// Check if already installed
		plist, err := plistPath(jobID)
		if err != nil {
			return err
		}

		if _, err := os.Stat(plist); err == nil {
			// Check for job ID collision: different paths producing the same sanitized ID
			existingSource, existingDest, parseErr := parsePlistPaths(plist)
			if parseErr == nil && (existingSource != sourcePath || existingDest != destPath) {
				return fmt.Errorf("job ID collision: %s\n  Existing install:\n    source:      %s\n    destination: %s\n  New install:\n    source:      %s\n    destination: %s\nThese paths produce the same sanitized job ID.\nRename one of the directories to avoid the collision.", jobID, existingSource, existingDest, sourcePath, destPath)
			}

			// Check if plist content needs updating (flag changes, launcher path changes)
			bufTime, _ := cmd.Flags().GetInt("buffer-time")
			logDirPath, _ := logDir(jobID)
			newPlist := generatePlist(jobID, execPath, sourcePath, destPath, logDirPath, bufTime, debug, noLogin)
			existingPlist, _ := os.ReadFile(plist)

			needsReload := needsServiceReload(plist) || string(existingPlist) != newPlist

			if needsReload {
				if err := os.WriteFile(plist, []byte(newPlist), 0644); err != nil {
					return fmt.Errorf("failed to write plist: %v", err)
				}

				if err := reloadJob(jobID, plist, logDirPath); err != nil {
					return err
				}

				fmt.Println("Updated and reloaded service")
				fmt.Println()
				printStatusInfo(jobID, sourcePath, destPath)
				return nil
			}

			// No reload needed, just print status
			isRunning := isJobLoaded(jobID)
			if isRunning {
				fmt.Printf("Already installed and running: %s\n", jobID)
			} else {
				fmt.Printf("Already installed but not running: %s\n", jobID)
			}
			printStatusInfo(jobID, sourcePath, destPath)
			return nil
		}

		// Create log directory
		logDirPath, err := logDir(jobID)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(logDirPath, 0755); err != nil {
			return fmt.Errorf("failed to create log directory: %v", err)
		}

		if !dryRun {
			fmt.Println("Initial sync...")
			if err := scanPaths(sourcePath, destPath, maxFileSize, dryRun); err != nil {
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

		plistContent := generatePlist(jobID, execPath, sourcePath, destPath, logDirPath, bufTime, debug, noLogin)

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
		fmt.Printf("Watching:    %s\n", sourcePath)
		fmt.Printf("Destination: %s\n\n", destPath)

		printStatusInfo(jobID, sourcePath, destPath)
		return nil
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall autoversion launchd service",
	Long:  "Stops and removes the launchd job for the given source and destination.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("uninstall command is only supported on macOS")
		}

		sourcePath, destPath, err := jobPaths()
		if err != nil {
			return err
		}

		jobID := jobIdentifier(sourcePath, destPath)
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

		source, dest, parseErr := parsePlistPaths(plistFile)

		pid, exitStatus, running := getJobInfo(jobID)

		if running {
			uptime := formatUptime(getProcessUptime(pid))
			fmt.Printf("%-10s %s (pid %s, uptime %s)\n", "RUNNING", jobID, pid, uptime)
		} else {
			fmt.Printf("%-10s %s (exit %s)\n", "STOPPED", jobID, exitStatus)
		}
		if parseErr == nil {
			fmt.Printf("           source:      %s\n", source)
			fmt.Printf("           destination: %s\n", dest)
		}
	}

	return nil
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of autoversion launchd service(s)",
	Long:  "Without --source and --destination, lists all autoversion services. With them, shows detailed status.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("status command is only supported on macOS")
		}

		if sourceFlag == "" && destFlag == "" {
			return listAllAutoversionJobs()
		}

		sourcePath, destPath, err := jobPaths()
		if err != nil {
			return err
		}

		jobID := jobIdentifier(sourcePath, destPath)
		plist, err := plistPath(jobID)
		if err != nil {
			return err
		}

		// Check if installed
		if _, err := os.Stat(plist); os.IsNotExist(err) {
			fmt.Printf("Status:      NOT INSTALLED\n")
			fmt.Printf("Source:      %s\n", sourcePath)
			fmt.Printf("Destination: %s\n", destPath)
			fmt.Printf("Job ID:      %s\n", jobID)
			return nil
		}

		// Check if running
		running := isJobLoaded(jobID)
		if running {
			fmt.Printf("Status:      RUNNING\n")
		} else {
			fmt.Printf("Status:      INSTALLED (not running)\n")
		}

		fmt.Printf("Source:      %s\n", sourcePath)
		fmt.Printf("Destination: %s\n\n", destPath)

		printStatusInfo(jobID, sourcePath, destPath)
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
func printStatusInfo(jobID, sourcePath, destPath string) {
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
	printLogFile("stdout", filepath.Join(logDirPath, stdoutLogName), false)
	printLogFile("stderr", filepath.Join(logDirPath, stderrLogName), true)
	fmt.Println()

	fmt.Printf("Commands:\n")
	fmt.Printf("  Status:  launchctl list %s\n", jobID)
	fmt.Printf("  Stop:    launchctl unload %s\n", plist)
	fmt.Printf("  Start:   launchctl load %s\n", plist)
	fmt.Printf("  Remove:  autoversion uninstall --source %s --destination %s\n", sourcePath, destPath)
}

var bundleCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Create macOS app bundle for Full Disk Access support",
	Long: `Creates or updates ~/Applications/Autoversion.app, a small signed launcher
that runs this autoversion binary. Services set up by 'autoversion install' run
through it, so they inherit the Full Disk Access granted to the app.

Rebuilding autoversion in place keeps the app and its grant. The app is only
rebuilt, and needs a new grant, if the binary path changes.
Requires clang (Xcode Command Line Tools). 'install' runs this automatically.

To grant Full Disk Access:
1. Open System Settings > Privacy & Security > Full Disk Access
2. Remove any old Autoversion entry, click + and add ~/Applications/Autoversion.app`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("bundle command is only supported on macOS")
		}
		return ensureAppBundle()
	},
}

var unbundleCmd = &cobra.Command{
	Use:   "unbundle",
	Short: "Remove the macOS app bundle",
	Long:  "Removes ~/Applications/Autoversion.app.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("unbundle command is only supported on macOS")
		}

		installedPath, err := installedAppBundlePath()
		if err != nil {
			return err
		}
		if _, err := os.Stat(installedPath); os.IsNotExist(err) {
			fmt.Println("No app bundle found")
			return nil
		}
		if err := os.RemoveAll(installedPath); err != nil {
			return fmt.Errorf("failed to remove app bundle: %v", err)
		}
		fmt.Printf("Removed: %s\n", installedPath)

		fmt.Println("\nNote: installed services run through this app and won't start without it.")
		fmt.Println("Recreate it with: autoversion bundle")

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
	Use:   "install-check",
	Short: "Check installed service status and show recent logs",
	Long:  "Shows detailed status of an installed service including PID, uptime, and recent log output.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("install-check command is only supported on macOS")
		}

		sourcePath, destPath, err := jobPaths()
		if err != nil {
			return err
		}

		jobID := jobIdentifier(sourcePath, destPath)
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

		fmt.Printf("Job:         %s\n", jobID)
		fmt.Printf("Source:      %s\n", sourcePath)
		fmt.Printf("Destination: %s\n\n", destPath)

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

		stdoutPath := filepath.Join(logDirPath, stdoutLogName)
		stderrPath := filepath.Join(logDirPath, stderrLogName)

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
	// Install command flags (runtime ones: settings and rules are the destination's)
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

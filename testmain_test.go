package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps the tests away from the user's files and settings: HOME is
// a temp folder (LaunchAgents, logs and ~ paths land there), git reads no
// global or system config (hooks, templates, ignore files), and no launchd
// service is restarted.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "autoversion-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for key, value := range map[string]string{
		"HOME":                home,
		"XDG_CONFIG_HOME":     home,
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		os.Setenv(key, value)
	}
	restartJob = func(sourcePath, destPath string) error {
		restartedJobs = append(restartedJobs, [2]string{sourcePath, destPath})
		return nil
	}

	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// restartedJobs records the services the commands would restart
var restartedJobs [][2]string

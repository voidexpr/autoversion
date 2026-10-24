package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratePlist_ParsePlistPaths(t *testing.T) {
	source := "/Users/me/src & 'docs'"
	dest := "/Volumes/backup/<dest>"
	plist := generatePlist("projects.autoversion.job", "/bin/autoversion", source, dest, "/tmp/logs",
		10, 3, true, false, false, false, "md", []string{"Backup"}, []string{"settings.json"})
	if !strings.Contains(plist, "<string>--only-include-file</string>\n\t\t<string>settings.json</string>") {
		t.Errorf("plist misses --only-include-file settings.json:\n%s", plist)
	}

	path := filepath.Join(t.TempDir(), "job.plist")
	if err := os.WriteFile(path, []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	gotSource, gotDest, err := parsePlistPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotSource != source || gotDest != dest {
		t.Errorf("parsePlistPaths() = %q, %q, want %q, %q", gotSource, gotDest, source, dest)
	}

	// Plists written before --source/--destination are not parsed
	old := strings.Replace(plist, "<string>--source</string>", "<string>--base</string>", 1)
	if err := os.WriteFile(path, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parsePlistPaths(path); err == nil {
		t.Error("expected an error for a plist without --source")
	}
}

func TestEnsureAppBundle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("app bundles are macOS only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	t.Setenv("HOME", t.TempDir())

	appPath, err := installedAppBundlePath()
	if err != nil {
		t.Fatal(err)
	}
	legacyWrapper := filepath.Join(appPath, "Contents", "MacOS", "autoversion-wrapper")
	if err := os.MkdirAll(filepath.Dir(legacyWrapper), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyWrapper, []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := ensureAppBundle(); err != nil {
		t.Fatalf("ensureAppBundle: %v", err)
	}
	if _, err := os.Stat(legacyWrapper); !os.IsNotExist(err) {
		t.Errorf("legacy wrapper bundle was not replaced")
	}

	exe, err := appBundleExecutable()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("launcher executable missing: %v", err)
	}
	if out, err := exec.Command("codesign", "--verify", "--strict", appPath).CombinedOutput(); err != nil {
		t.Errorf("codesign --verify: %v\n%s", err, out)
	}

	src, err := os.ReadFile(filepath.Join(appPath, "Contents", "Resources", "launcher.c"))
	if err != nil {
		t.Fatal(err)
	}
	execPath, err := getExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"`+execPath+`"`) {
		t.Errorf("launcher does not run %s", execPath)
	}

	// Same binary path: the app (and its Full Disk Access grant) must be kept
	if err := ensureAppBundle(); err != nil {
		t.Fatalf("second ensureAppBundle: %v", err)
	}
	after, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("launcher was rebuilt although the binary path did not change")
	}
}

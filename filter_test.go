package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setRules sets the active rules for a test, use default plus lines, and
// restores the previous ones after
func setRules(t *testing.T, root string, lines ...string) {
	t.Helper()
	rules := defaultRulesWith(t, lines...)
	old := activeRules
	SetFilter(root, rules, false)
	t.Cleanup(func() { activeRules = old })
}

func TestShouldTrack(t *testing.T) {
	tmpDir := resolvedTempDir(t)
	setRules(t, tmpDir)

	tests := []struct {
		name     string
		filename string
		content  string
		maxSize  int64
		want     bool
	}{
		// Should track
		{"normal go file", "main.go", "package main", 0, true},
		{"normal txt file", "README.txt", "hello", 0, true},
		{"normal py file", "script.py", "print('hi')", 0, true},
		{"gitignore", ".gitignore", "*.log", 0, true},
		{"gitmodules", ".gitmodules", "[submodule]", 0, true},
		{"lock file (source)", "Cargo.lock", "lock", 0, true},
		{"vscode settings", ".vscode/settings.json", "json", 0, true},
		{"numeric name", "subdir/12345", "data", 0, true},
		{"file named build", "build", "#!/bin/sh", 0, true},

		// Should exclude - file extensions
		{"log file", "app.log", "log data", 0, false},
		{"dump file", "core.dump", "data", 0, false},
		{"swp file", ".file.swp", "vim swap", 0, false},
		{"swpx file", ".file.swpx", "vim swap", 0, false},
		{"swo file", ".file.swo", "vim swap", 0, false},
		{"pid file", "app.pid", "1234", 0, false},
		{"pyc file", "module.pyc", "bytecode", 0, false},
		{"wal file", "db.wal", "wal data", 0, false},
		{"old file", "backup.old", "old", 0, false},
		{"temp file", "scratch.temp", "temp", 0, false},
		{"atomic write temp", "PLAN.md.tmp.18812.1760578375713", "temp", 0, false},
		{"DS_Store", ".DS_Store", "mac", 0, false},
		{"object file", "code.o", "obj", 0, false},
		{"shared lib", "lib.so", "lib", 0, false},
		{"archive", "lib.a", "ar", 0, false},
		{"vim write test", "subdir/4913", "", 0, false},

		// Should exclude - tilde suffix
		{"tilde backup", "file~", "backup", 0, false},

		// Should exclude - paths
		// .git is a rule (use nested-git), or a git destination's constraint: see TestShouldTrack_NestedGit
		{"git dir file", ".git/config", "git", 0, true},
		{"git worktree file", "subdir/.git", "gitdir: ../.git/worktrees/x", 0, true},
		{"node_modules", "node_modules/pkg/index.js", "code", 0, false},
		{"pycache", "__pycache__/module.pyc", "bytecode", 0, false},
		{"venv", ".venv/lib/python", "py", 0, false},

		// C/C++ / Windows build artifacts
		{"Windows object file", "code.obj", "obj", 0, false},
		{"Windows lib", "math.lib", "lib", 0, false},
		{"Windows DLL", "runtime.dll", "dll", 0, false},
		{"Windows exe", "app.exe", "exe", 0, false},
		{"PDB debug", "app.pdb", "pdb", 0, false},
		{"precompiled header", "stdafx.gch", "gch", 0, false},
		{"dependency file", "main.d", "dep", 0, false},

		// Java
		{"class file", "Main.class", "bytecode", 0, false},
		{"jar file", "lib.jar", "jar", 0, false},

		// Directories
		{"gradle cache", ".gradle/caches/file.bin", "data", 0, false},
		{"build dir", "build-dir/build/output.js", "js", 0, false},
		{"dist dir", "dist/bundle.js", "js", 0, false},
		{"next dir", ".next/cache/file", "data", 0, false},
		{"idea dir", ".idea/workspace.xml", "xml", 0, false},
		{"DerivedData", "DerivedData/Build/obj", "data", 0, false},

		// OS files
		{"Thumbs.db", "Thumbs.db", "thumb", 0, false},
		{"desktop.ini", "desktop.ini", "ini", 0, false},

		// IntelliJ
		{"iml file", "project.iml", "iml", 0, false},

		// Destination files at the source root
		{"marker", markerFileName, "{}", 0, false},
		{"rules file", filterFileName, "version 1", 0, false},
		{"rules file below the root", "sub/" + filterFileName, "version 1", 0, true},

		// File size limits
		{"small file under limit", "small.txt", "x", 1 << 20, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testPath := filepath.Join(tmpDir, tt.filename)
			writeFile(t, testPath, tt.content)

			got, err := ShouldTrack(testPath, tt.maxSize, false)
			if err != nil {
				t.Fatalf("ShouldTrack() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ShouldTrack(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestShouldTrack_FileSize(t *testing.T) {
	tmpDir := resolvedTempDir(t)
	setRules(t, tmpDir)
	largePath := filepath.Join(tmpDir, "large.bin")
	writeFile(t, largePath, strings.Repeat("x", 2*1024*1024))

	captureStdout(t, func() {
		if got, err := ShouldTrack(largePath, 1<<20, false); got || err != nil {
			t.Errorf("ShouldTrack(2MB file, 1MB limit) = %v, %v, want false", got, err)
		}
	})
	if got, err := ShouldTrack(largePath, 3<<20, false); !got || err != nil {
		t.Errorf("ShouldTrack(2MB file, 3MB limit) = %v, %v, want true", got, err)
	}
}

func TestShouldTrack_LargeFileReportedOnce(t *testing.T) {
	tmpDir := resolvedTempDir(t)
	setRules(t, tmpDir)
	largePath := filepath.Join(tmpDir, "large.bin")
	writeFile(t, largePath, strings.Repeat("x", 2*1024*1024))

	out := captureStdout(t, func() {
		for range 3 {
			if _, err := ShouldTrack(largePath, 1<<20, false); err != nil {
				t.Fatalf("ShouldTrack() error = %v", err)
			}
		}
	})
	if n := strings.Count(out, "skipping large file"); n != 1 {
		t.Errorf("large file reported %d times, want 1; output: %q", n, out)
	}
}

func TestShouldTrack_VanishedFile(t *testing.T) {
	oldDebug := debug
	defer func() { debug = oldDebug }()
	dir := resolvedTempDir(t)
	setRules(t, dir)
	vanished := filepath.Join(dir, "gone.txt")
	excluded := filepath.Join(dir, "target", "debug", "incremental", "gone.o")

	for _, d := range []bool{false, true} {
		debug = d
		out := captureStdout(t, func() {
			for _, path := range []string{vanished, excluded} {
				got, err := ShouldTrack(path, 10<<20, false)
				if err != nil || got {
					t.Errorf("ShouldTrack(%q) = %v, %v, want false, nil", path, got, err)
				}
			}
		})
		// Only with --debug, and paths the rules leave out are not looked up
		want := 0
		if d {
			want = 1
		}
		if n := strings.Count(out, "skipping vanished file"); n != want {
			t.Errorf("debug=%v: %d vanished file messages, want %d; output: %q", d, n, want, out)
		}
		if strings.Contains(out, "gone.o") {
			t.Errorf("debug=%v: excluded path reported: %q", d, out)
		}
	}
}

func TestShouldTrack_NotRegularFiles(t *testing.T) {
	dir := resolvedTempDir(t)
	setRules(t, dir)
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"link", "sub"} {
		if got, err := ShouldTrack(filepath.Join(dir, name), 0, false); got || err != nil {
			t.Errorf("ShouldTrack(%s) = %v, %v, want false", name, got, err)
		}
	}
	// A deleted entry is decided by the rules only
	if got, _ := ShouldTrack(filepath.Join(dir, "link"), 0, true); !got {
		t.Error("ShouldTrack(deleted link) = false, want true")
	}
}

func TestShouldTrack_Rules(t *testing.T) {
	root := resolvedTempDir(t)
	setRules(t, root, "exclude /*", "/settings.json", "/debug.log", "/projects/*/memory")

	for _, name := range []string{"settings.json", "debug.log", "other.json", "Backup/data.txt",
		"projects/p/memory/MEMORY.md", "projects/p/x.jsonl", "projects/p/memory/x.log"} {
		writeFile(t, filepath.Join(root, name), "x")
	}
	for name, want := range map[string]bool{
		"settings.json":               true,
		"debug.log":                   true, // the literal include beats the logs group
		"other.json":                  false,
		"Backup/data.txt":             false,
		"projects/p/memory/MEMORY.md": true,
		"projects/p/x.jsonl":          false,
		"projects/p/memory/x.log":     false, // the logs group is closer than the include
	} {
		if got, err := ShouldTrack(filepath.Join(root, name), 10<<20, false); got != want || err != nil {
			t.Errorf("ShouldTrack(%s) = %v, %v, want %v", name, got, err, want)
		}
	}
	if trackedPath("/elsewhere/settings.json", false) {
		t.Error("a path outside the source is tracked")
	}
}

// TestShouldTrack_NestedGit: .git folders are left out by the nested-git
// group, and by a git destination whatever its rules say
func TestShouldTrack_NestedGit(t *testing.T) {
	root := resolvedTempDir(t)
	for _, name := range []string{".git/config", "sub/.git", "a/b/.git/objects/x", "sub/git/x", "sub/.gitignore"} {
		writeFile(t, filepath.Join(root, name), "x")
	}
	gitEntries := []string{".git/config", "sub/.git", "a/b/.git/objects/x"}
	others := []string{"sub/git/x", "sub/.gitignore"}
	check := func(t *testing.T, want bool) {
		t.Helper()
		for _, name := range gitEntries {
			if got, _ := ShouldTrack(filepath.Join(root, name), 0, false); got != want {
				t.Errorf("ShouldTrack(%s) = %v, want %v", name, got, want)
			}
		}
		for _, name := range others {
			if got, _ := ShouldTrack(filepath.Join(root, name), 0, false); !got {
				t.Errorf("ShouldTrack(%s) = false, want true", name)
			}
		}
	}
	old := activeRules
	t.Cleanup(func() { activeRules = old })

	SetFilter(root, defaultRulesWith(t), false)
	check(t, true)
	SetFilter(root, defaultRulesWith(t, "use nested-git"), false)
	check(t, false)
	// A git destination, even with rules that include .git
	SetFilter(root, defaultRulesWith(t, "/**/.git"), true)
	check(t, false)
	if !skipDir(filepath.Join(root, "a/b/.git")) || skipDir(filepath.Join(root, "sub/git")) {
		t.Error("skipDir: a .git is skipped, a folder named git is not")
	}

	// gitExcluded decides like the group
	group := defaultRulesWith(t, "use nested-git")
	for _, name := range append(append([]string{}, gitEntries...), others...) {
		if got, want := gitExcluded(name), !group.Decide(name, false).Kept; got != want {
			t.Errorf("gitExcluded(%s) = %v, the nested-git group says %v", name, got, want)
		}
	}
}

func TestSkipDir(t *testing.T) {
	root := resolvedTempDir(t)
	setRules(t, root, "use nested-git", "exclude /Backup")
	for dir, want := range map[string]bool{
		"":                         false,
		"project":                  false,
		"project/src":              false,
		"project/builder":          false,
		"project/build":            false, // its content is left out, not the folder
		"Backups":                  false,
		"x.old":                    false, // f: rules are for files
		"project/node_modules":     true,
		"project/node_modules/pkg": true,
		"project/.venv":            true,
		"project/__pycache__":      true,
		"project/target/debug":     true,
		"project/.git":             true,
		".git":                     true,
		"Backup":                   true,
		"Backup/sub":               true,
	} {
		path := filepath.Join(root, dir)
		if got := skipDir(path); got != want {
			t.Errorf("skipDir(%q) = %v, want %v", dir, got, want)
		}
		// A skipped folder must not hold files the rules keep
		if want && trackedPath(filepath.Join(path, "file.txt"), false) {
			t.Errorf("skipDir(%q) is true but a file in it is kept", dir)
		}
	}
	if !skipDir("/elsewhere") {
		t.Error("a folder outside the source is not skipped")
	}

	t.Run("allowlist", func(t *testing.T) {
		setRules(t, root, "exclude /*", "/a/b/f.txt")
		for dir, want := range map[string]bool{"": false, "a": false, "a/b": false, "a/b/c": true, "a/c": true, "x": true} {
			if got := skipDir(filepath.Join(root, dir)); got != want {
				t.Errorf("skipDir(%s) = %v, want %v", dir, got, want)
			}
		}
	})
}

func TestRulesSetRequired(t *testing.T) {
	old := activeRules
	activeRules = nil
	defer func() {
		activeRules = old
		if recover() == nil {
			t.Error("no panic without SetFilter")
		}
	}()
	trackedPath("/src/a.txt", false)
}

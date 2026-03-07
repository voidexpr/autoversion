package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShouldTrack(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "filter_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name      string
		filename  string
		content   string
		maxSizeMB int64
		want      bool
	}{
		// Should track
		{"normal go file", "main.go", "package main", 0, true},
		{"normal txt file", "README.txt", "hello", 0, true},
		{"normal py file", "script.py", "print('hi')", 0, true},
		{"gitignore", ".gitignore", "*.log", 0, true},
		{"gitmodules", ".gitmodules", "[submodule]", 0, true},

		// Should exclude - file extensions
		{"log file", "app.log", "log data", 0, false},
		{"dump file", "core.dump", "data", 0, false},
		{"swp file", ".file.swp", "vim swap", 0, false},
		{"swpx file", ".file.swpx", "vim swap", 0, false},
		{"swo file", ".file.swo", "vim swap", 0, false},
		{"pid file", "app.pid", "1234", 0, false},
		{"lock file", "app.lock", "lock", 0, false},
		{"pyc file", "module.pyc", "bytecode", 0, false},
		{"wal file", "db.wal", "wal data", 0, false},
		{"old file", "backup.old", "old", 0, false},
		{"temp file", "scratch.temp", "temp", 0, false},
		{"DS_Store", ".DS_Store", "mac", 0, false},
		{"object file", "code.o", "obj", 0, false},
		{"shared lib", "lib.so", "lib", 0, false},
		{"archive", "lib.a", "ar", 0, false},

		// Should exclude - tilde suffix
		{"tilde backup", "file~", "backup", 0, false},

		// Should exclude - paths
		{"git dir file", ".git/config", "git", 0, false},
		{"git worktree file", "subdir/.git", "gitdir: ../.git/worktrees/x", 0, false},
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
		{"build dir", "build/output.js", "js", 0, false},
		{"dist dir", "dist/bundle.js", "js", 0, false},
		{"next dir", ".next/cache/file", "data", 0, false},
		{"idea dir", ".idea/workspace.xml", "xml", 0, false},
		{"vscode dir", ".vscode/settings.json", "json", 0, false},
		{"DerivedData", "DerivedData/Build/obj", "data", 0, false},

		// OS files
		{"Thumbs.db", "Thumbs.db", "thumb", 0, false},
		{"desktop.ini", "desktop.ini", "ini", 0, false},

		// IntelliJ
		{"iml file", "project.iml", "iml", 0, false},

		// File size limits
		{"small file under limit", "small.txt", "x", 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			testPath := filepath.Join(tmpDir, tt.filename)

			// Create parent dirs if needed
			if dir := filepath.Dir(testPath); dir != tmpDir {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}

			if err := os.WriteFile(testPath, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			got, err := ShouldTrack(testPath, tt.maxSizeMB, false)
			if err != nil {
				t.Fatalf("ShouldTrack() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ShouldTrack(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestShouldTrack_IncludeExtensions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "filter_include_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	files := map[string]string{
		"doc.md":     "markdown",
		"script.py":  "print('hi')",
		"app.log":    "log data",
		"data.csv":   "a,b,c",
		"README.txt": "hello",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name       string
		includeExt string
		filename   string
		want       bool
	}{
		{"md included when in list", "md,csv", "doc.md", true},
		{"csv included when in list", "md,csv", "data.csv", true},
		{"py excluded when not in list", "md,csv", "script.py", false},
		{"txt excluded when not in list", "md,csv", "README.txt", false},
		{"log included when in list (overrides default exclude)", "log", "app.log", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			includeExtensions = nil
			SetIncludeExtensions(tt.includeExt)
			defer func() { includeExtensions = nil }()

			got, err := ShouldTrack(filepath.Join(tmpDir, tt.filename), 0, false)
			if err != nil {
				t.Fatalf("ShouldTrack() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ShouldTrack(%q) with include=%q = %v, want %v", tt.filename, tt.includeExt, got, tt.want)
			}
		})
	}
}

func TestShouldTrack_EmptyIncludeExtensions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "filter_empty_include_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	includeExtensions = nil
	SetIncludeExtensions("")

	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(tmpDir, "app.log")
	if err := os.WriteFile(logFile, []byte("log"), 0644); err != nil {
		t.Fatal(err)
	}

	got, _ := ShouldTrack(goFile, 0, false)
	if !got {
		t.Error("ShouldTrack(main.go) with empty include = false, want true")
	}

	got, _ = ShouldTrack(logFile, 0, false)
	if got {
		t.Error("ShouldTrack(app.log) with empty include = true, want false (default exclude)")
	}
}

func TestShouldTrack_FileSize(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "filter_size_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a 2MB file
	largePath := filepath.Join(tmpDir, "large.bin")
	largeData := make([]byte, 2*1024*1024) // 2MB
	if err := os.WriteFile(largePath, largeData, 0644); err != nil {
		t.Fatal(err)
	}

	// Should be excluded with 1MB limit
	got, err := ShouldTrack(largePath, 1, false)
	if err != nil {
		t.Fatalf("ShouldTrack() error = %v", err)
	}
	if got != false {
		t.Errorf("ShouldTrack(2MB file, 1MB limit) = %v, want false", got)
	}

	// Should be allowed with 3MB limit
	got, err = ShouldTrack(largePath, 3, false)
	if err != nil {
		t.Fatalf("ShouldTrack() error = %v", err)
	}
	if got != true {
		t.Errorf("ShouldTrack(2MB file, 3MB limit) = %v, want true", got)
	}
}

func TestShouldTrack_Directory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "filter_dir_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Directories should not be tracked
	got, err := ShouldTrack(tmpDir, 0, false)
	if err != nil {
		t.Fatalf("ShouldTrack() error = %v", err)
	}
	if got != false {
		t.Errorf("ShouldTrack(directory) = %v, want false", got)
	}
}

func TestShouldTrack_NumericVimTemp(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "filter_vim_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Vim creates temp files ending in numbers
	vimTempPath := filepath.Join(tmpDir, "subdir", "12345")
	if err := os.MkdirAll(filepath.Dir(vimTempPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vimTempPath, []byte("temp"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ShouldTrack(vimTempPath, 0, false)
	if err != nil {
		t.Fatalf("ShouldTrack() error = %v", err)
	}
	if got != false {
		t.Errorf("ShouldTrack(numeric vim temp) = %v, want false", got)
	}
}

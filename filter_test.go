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
		{"node_modules", "node_modules/pkg/index.js", "code", 0, false},
		{"pycache", "__pycache__/module.pyc", "bytecode", 0, false},
		{"venv", ".venv/lib/python", "py", 0, false},

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

			got, err := ShouldTrack(testPath, tt.maxSizeMB)
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
	got, err := ShouldTrack(largePath, 1)
	if err != nil {
		t.Fatalf("ShouldTrack() error = %v", err)
	}
	if got != false {
		t.Errorf("ShouldTrack(2MB file, 1MB limit) = %v, want false", got)
	}

	// Should be allowed with 3MB limit
	got, err = ShouldTrack(largePath, 3)
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
	got, err := ShouldTrack(tmpDir, 0)
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

	got, err := ShouldTrack(vimTempPath, 0)
	if err != nil {
		t.Fatalf("ShouldTrack() error = %v", err)
	}
	if got != false {
		t.Errorf("ShouldTrack(numeric vim temp) = %v, want false", got)
	}
}

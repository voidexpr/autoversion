package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLongestCommonDir(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{"shared parent dir", "/home/user/foo", "/home/user/bar", "/home/user"},
		{"deeper shared ancestor", "/home/user/a/b", "/home/user/a/c", "/home/user/a"},
		{"only root in common", "/foo/bar", "/baz/qux", "/"},
		{"identical paths", "/home/user/projects", "/home/user/projects", "/home/user/projects"},
		{"one is prefix of other", "/home/user", "/home/user/sub", "/home/user"},
		{"single level under root", "/foo", "/foo/bar", "/foo"},
		{"dotdot cleaned before comparison", "/home/user/foo/../bar", "/home/user/baz", "/home/user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := longestCommonDir(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("longestCommonDir(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestComputeTargetPath(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot get home dir: %v", err)
	}

	oldDebug := debug
	debug = false
	defer func() { debug = oldDebug }()

	tests := []struct {
		name   string
		base   string
		source string
		want   string
	}{
		{
			name:   "source under HOME",
			base:   "/archive/base",
			source: filepath.Join(homeDir, "projects", "myrepo"),
			want:   "/archive/base/projects/myrepo",
		},
		{
			name:   "source is HOME itself",
			base:   "/archive/base",
			source: homeDir,
			want:   "/archive/base",
		},
		{
			name:   "source with dotdot under HOME resolved",
			base:   "/archive/base",
			source: homeDir + "/foo/../bar",
			want:   "/archive/base/bar",
		},
		{
			name:   "source outside HOME shares ancestor with base",
			base:   "/data/projects",
			source: "/data/docs",
			want:   "/data/projects/docs",
		},
		{
			name:   "source outside HOME no shared ancestor",
			base:   "/mnt/backup",
			source: "/var/data",
			want:   "/mnt/backup/var/data",
		},
		{
			name:   "base with dotdot cleaned",
			base:   "/archive/../other/base",
			source: filepath.Join(homeDir, "work"),
			want:   "/other/base/work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := computeTargetPath(tt.base, tt.source)
			if err != nil {
				t.Fatalf("computeTargetPath(%q, %q) error: %v", tt.base, tt.source, err)
			}
			if got != tt.want {
				t.Errorf("computeTargetPath(%q, %q) = %q, want %q", tt.base, tt.source, got, tt.want)
			}
		})
	}
}

func TestValidateSourceAndBase(t *testing.T) {
	oldNoGit := noGit
	noGit = true
	defer func() { noGit = oldNoGit }()

	t.Run("valid source and base", func(t *testing.T) {
		source := t.TempDir()
		base := t.TempDir()

		result, err := ValidateSourceAndBase(source, base)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Errors) != 0 {
			t.Errorf("expected no errors, got: %v", result.Errors)
		}
		if result.SourceResolved == "" {
			t.Error("SourceResolved not populated")
		}
		if result.BaseResolved == "" {
			t.Error("BaseResolved not populated")
		}
		if result.TargetComputed == "" {
			t.Error("TargetComputed not populated")
		}
	})

	t.Run("source does not exist", func(t *testing.T) {
		base := t.TempDir()
		source := filepath.Join(base, "nonexistent")

		result, err := ValidateSourceAndBase(source, base)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Errors) == 0 {
			t.Error("expected error for nonexistent source")
		}
	})

	t.Run("base does not exist", func(t *testing.T) {
		source := t.TempDir()
		base := filepath.Join(t.TempDir(), "nonexistent")

		result, err := ValidateSourceAndBase(source, base)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Errors) == 0 {
			t.Error("expected error for nonexistent base")
		}
	})

	t.Run("source and base are the same", func(t *testing.T) {
		dir := t.TempDir()

		result, err := ValidateSourceAndBase(dir, dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Errors) == 0 {
			t.Error("expected error when source == base")
		}
	})

	t.Run("source inside base", func(t *testing.T) {
		base := t.TempDir()
		source := filepath.Join(base, "subdir")
		if err := os.MkdirAll(source, 0755); err != nil {
			t.Fatal(err)
		}

		result, err := ValidateSourceAndBase(source, base)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Errors) == 0 {
			t.Error("expected error when source is inside base")
		}
	})

	t.Run("base inside source", func(t *testing.T) {
		source := t.TempDir()
		base := filepath.Join(source, "subdir")
		if err := os.MkdirAll(base, 0755); err != nil {
			t.Fatal(err)
		}

		result, err := ValidateSourceAndBase(source, base)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Errors) == 0 {
			t.Error("expected error when base is inside source")
		}
	})
}

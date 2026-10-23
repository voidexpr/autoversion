package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// resolvedTempDir returns a new temp directory with symlinks resolved, the
// form autoversion stores paths in (on macOS /var is a symlink)
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// setGlobals sets noGit and useRestic for a test and restores them after
func setGlobals(t *testing.T, git bool) {
	t.Helper()
	oldNoGit, oldUseRestic, oldSourceRoot := noGit, useRestic, sourceRoot
	noGit, useRestic = !git, false
	t.Cleanup(func() { noGit, useRestic, sourceRoot = oldNoGit, oldUseRestic, oldSourceRoot })
	if git {
		t.Setenv("GIT_AUTHOR_NAME", "Test")
		t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
		t.Setenv("GIT_COMMITTER_NAME", "Test")
		t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func markerSourceOf(t *testing.T, destPath string) string {
	t.Helper()
	marker, err := readMarker(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil {
		return ""
	}
	return marker.Source
}

// gitLogCount returns the number of commits touching path (all commits if path is empty)
func gitLogCount(t *testing.T, repo, path string) int {
	t.Helper()
	args := []string{"log", "--oneline"}
	if path != "" {
		args = append(args, "--", path)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	return len(strings.Split(strings.TrimSpace(string(out)), "\n"))
}

func TestResolvePath_MissingPathResolvesExistingParent(t *testing.T) {
	dir := t.TempDir()
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, err := resolvePath(filepath.Join(dir, "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(resolvedDir, "a", "b"); got != want {
		t.Errorf("resolvePath() = %q, want %q", got, want)
	}
}

func TestValidateSourceAndDestination(t *testing.T) {
	setGlobals(t, false)

	validate := func(t *testing.T, source, dest string) *PathValidationResult {
		t.Helper()
		result, err := ValidateSourceAndDestination(source, dest)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return result
	}
	expectError := func(t *testing.T, result *PathValidationResult, substr string) {
		t.Helper()
		for _, e := range result.Errors {
			if strings.Contains(e, substr) {
				return
			}
		}
		t.Errorf("expected an error containing %q, got %v", substr, result.Errors)
	}

	t.Run("destination missing", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := filepath.Join(resolvedTempDir(t), "dest")
		result := validate(t, source, dest)
		if len(result.Errors) != 0 {
			t.Errorf("expected no errors, got: %v", result.Errors)
		}
		if result.SourceResolved != source || result.DestinationResolved != dest {
			t.Errorf("resolved paths = %q, %q", result.SourceResolved, result.DestinationResolved)
		}
		if result.DestinationExists || result.Marker != MarkerMissing {
			t.Errorf("DestinationExists = %v, Marker = %v", result.DestinationExists, result.Marker)
		}
		if problems := result.problems(); len(problems) != 1 || !strings.Contains(problems[0], "not initialized") {
			t.Errorf("problems() = %v, want only 'not initialized'", problems)
		}
	})

	t.Run("marker names the source", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := resolvedTempDir(t)
		if err := writeMarker(dest, source); err != nil {
			t.Fatal(err)
		}
		result := validate(t, source, dest)
		if result.Marker != MarkerMatches {
			t.Errorf("Marker = %v, want MarkerMatches", result.Marker)
		}
		if problems := result.problems(); len(problems) != 0 {
			t.Errorf("problems() = %v, want none", problems)
		}
	})

	t.Run("marker names another source", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := resolvedTempDir(t)
		if err := writeMarker(dest, "/other/source"); err != nil {
			t.Fatal(err)
		}
		result := validate(t, source, dest)
		if result.Marker != MarkerOtherSource || result.MarkerSource != "/other/source" {
			t.Errorf("Marker = %v, MarkerSource = %q", result.Marker, result.MarkerSource)
		}
		if err := result.markerError(); err == nil || !strings.Contains(err.Error(), "--force-new-source") {
			t.Errorf("markerError() = %v", err)
		}
	})

	t.Run("invalid marker", func(t *testing.T) {
		dest := resolvedTempDir(t)
		writeFile(t, filepath.Join(dest, markerFileName), "not json")
		expectError(t, validate(t, resolvedTempDir(t), dest), "invalid marker")
	})

	t.Run("source does not exist", func(t *testing.T) {
		source := filepath.Join(resolvedTempDir(t), "nonexistent")
		expectError(t, validate(t, source, resolvedTempDir(t)), "source path does not exist")
	})

	t.Run("destination is a file", func(t *testing.T) {
		dest := filepath.Join(resolvedTempDir(t), "file")
		writeFile(t, dest, "x")
		expectError(t, validate(t, resolvedTempDir(t), dest), "destination path is not a directory")
	})

	t.Run("source and destination are the same", func(t *testing.T) {
		dir := resolvedTempDir(t)
		expectError(t, validate(t, dir, dir), "cannot be the same")
	})

	t.Run("source inside destination", func(t *testing.T) {
		dest := resolvedTempDir(t)
		source := filepath.Join(dest, "sub")
		if err := os.Mkdir(source, 0755); err != nil {
			t.Fatal(err)
		}
		expectError(t, validate(t, source, dest), "cannot be inside destination path")
	})

	t.Run("destination inside source", func(t *testing.T) {
		source := resolvedTempDir(t)
		expectError(t, validate(t, source, filepath.Join(source, "dest")), "cannot be inside source path")
	})

	t.Run("source has a marker (swapped flags)", func(t *testing.T) {
		source := resolvedTempDir(t)
		if err := writeMarker(source, "/some/source"); err != nil {
			t.Fatal(err)
		}
		expectError(t, validate(t, source, filepath.Join(resolvedTempDir(t), "dest")), "swapped")
	})

	t.Run("parent of destination has a marker", func(t *testing.T) {
		outer := resolvedTempDir(t)
		if err := writeMarker(outer, "/some/source"); err != nil {
			t.Fatal(err)
		}
		expectError(t, validate(t, resolvedTempDir(t), filepath.Join(outer, "inner")), "inside another autoversion destination")
	})

	t.Run("parent of destination is a git repo", func(t *testing.T) {
		repo := resolvedTempDir(t)
		if err := os.Mkdir(filepath.Join(repo, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(repo, "dest")

		if result := validate(t, resolvedTempDir(t), dest); len(result.Errors) != 0 {
			t.Errorf("with --no-git expected no errors, got: %v", result.Errors)
		}
		noGit = false
		defer func() { noGit = true }()
		expectError(t, validate(t, resolvedTempDir(t), dest), "parent with a .git repository")
	})
}

func TestInitDestination(t *testing.T) {
	setGlobals(t, true)

	t.Run("missing destination", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := filepath.Join(resolvedTempDir(t), "dest")
		if err := initDestination(source, dest, false, false); err != nil {
			t.Fatal(err)
		}
		if !IsGitRepo(dest) {
			t.Error("destination is not a git repository")
		}
		if got := markerSourceOf(t, dest); got != source {
			t.Errorf("marker source = %q, want %q", got, source)
		}
		if n := gitLogCount(t, dest, markerFileName); n != 1 {
			t.Errorf("marker commits = %d, want 1", n)
		}
	})

	t.Run("empty destination, then already initialized", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := resolvedTempDir(t)
		for i := 0; i < 2; i++ {
			if err := initDestination(source, dest, false, false); err != nil {
				t.Fatalf("init #%d: %v", i+1, err)
			}
		}
		if n := gitLogCount(t, dest, ""); n != 1 {
			t.Errorf("commits = %d, want 1 (second init is a no-op)", n)
		}
	})

	t.Run("marker names another source", func(t *testing.T) {
		oldSource := resolvedTempDir(t)
		newSource := resolvedTempDir(t)
		dest := filepath.Join(resolvedTempDir(t), "dest")
		if err := initDestination(oldSource, dest, false, false); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dest, "kept.txt"), "kept")

		if err := initDestination(newSource, dest, false, false); err == nil {
			t.Fatal("expected an error without --force-new-source")
		}
		if err := initDestination(newSource, dest, true, false); err != nil {
			t.Fatalf("--force-new-source: %v", err)
		}
		if got := markerSourceOf(t, dest); got != newSource {
			t.Errorf("marker source = %q, want %q", got, newSource)
		}
		if _, err := os.Stat(filepath.Join(dest, "kept.txt")); err != nil {
			t.Errorf("contents not kept: %v", err)
		}
		if n := gitLogCount(t, dest, markerFileName); n != 2 {
			t.Errorf("marker commits = %d, want 2", n)
		}
	})

	t.Run("non-empty destination without marker (migration)", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := resolvedTempDir(t)
		if err := Init(dest, false); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dest, "old.txt"), "history")
		if err := Add(dest, []string{"."}, false); err != nil {
			t.Fatal(err)
		}
		if err := Commit(dest, "old commit"); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dest, "old.txt"), "uncommitted change")

		if err := initDestination(source, dest, false, false); err == nil {
			t.Fatal("expected an error without --force-new-source")
		}
		if err := initDestination(source, dest, true, false); err != nil {
			t.Fatalf("--force-new-source: %v", err)
		}
		if got := markerSourceOf(t, dest); got != source {
			t.Errorf("marker source = %q, want %q", got, source)
		}
		if n := gitLogCount(t, dest, ""); n != 2 {
			t.Errorf("commits = %d, want 2 (old commit + marker)", n)
		}
		// The marker commit must not include other pending changes
		if n := gitLogCount(t, dest, "old.txt"); n != 1 {
			t.Errorf("commits touching old.txt = %d, want 1", n)
		}
	})

	t.Run("obliterate", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := resolvedTempDir(t)
		if err := initDestination("/old/source", dest, false, false); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dest, "sub", "gone.txt"), "gone")

		if err := initDestination(source, dest, false, true); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dest, "sub")); !os.IsNotExist(err) {
			t.Error("contents not removed")
		}
		if got := markerSourceOf(t, dest); got != source {
			t.Errorf("marker source = %q, want %q", got, source)
		}
		if n := gitLogCount(t, dest, ""); n != 1 {
			t.Errorf("commits = %d, want 1 (fresh repository)", n)
		}
	})

	t.Run("no-git", func(t *testing.T) {
		noGit = true
		defer func() { noGit = false }()
		source := resolvedTempDir(t)
		dest := filepath.Join(resolvedTempDir(t), "dest")
		if err := initDestination(source, dest, false, false); err != nil {
			t.Fatal(err)
		}
		if IsGitRepo(dest) {
			t.Error("git repository created with --no-git")
		}
		if got := markerSourceOf(t, dest); got != source {
			t.Errorf("marker source = %q, want %q", got, source)
		}
	})
}

func TestInitCmd_OverrideFlagsAreExclusive(t *testing.T) {
	setGlobals(t, false)
	defer func() {
		sourceFlag, destFlag, forceNewSource, forceObliterate = "", "", false, false
		rootCmd.SetArgs(nil)
	}()

	dest := filepath.Join(resolvedTempDir(t), "dest")
	rootCmd.SetArgs([]string{"init", "--source", resolvedTempDir(t), "--destination", dest,
		"--force-new-source", "--force-obliterate-yes-i-am-sure"})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected an error for --force-new-source with --force-obliterate-yes-i-am-sure")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("destination created despite the error")
	}
}

func TestValidatePaths_RequiresMatchingMarker(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := resolvedTempDir(t)

	if _, _, err := validatePaths(source, dest); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("without marker: err = %v", err)
	}
	if err := writeMarker(dest, "/other/source"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validatePaths(source, dest); err == nil || !strings.Contains(err.Error(), "belongs to source") {
		t.Errorf("marker for another source: err = %v", err)
	}
	if err := writeMarker(dest, source); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validatePaths(source, dest); err != nil {
		t.Errorf("matching marker: err = %v", err)
	}
}

func TestScanPaths_KeepsDestinationMarker(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := resolvedTempDir(t)
	if err := initDestination(source, dest, false, false); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "a.txt"), "a")
	// A marker at the source root must not be copied over the destination's
	writeFile(t, filepath.Join(source, markerFileName), `{"source": "/elsewhere"}`)

	SetSourceRoot(source)
	if err := scanPaths(source, dest, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.txt")); err != nil {
		t.Errorf("a.txt not copied: %v", err)
	}
	if got := markerSourceOf(t, dest); got != source {
		t.Errorf("destination marker source = %q, want %q (deleted or overwritten)", got, source)
	}
}

func TestDestinationDisappeared(t *testing.T) {
	newSource := func(t *testing.T) string {
		source := resolvedTempDir(t)
		writeFile(t, filepath.Join(source, "root.txt"), "root")
		writeFile(t, filepath.Join(source, "sub", "nested.txt"), "nested")
		return source
	}
	expectDestMissing := func(t *testing.T, err error, dest string) {
		t.Helper()
		if !errors.Is(err, errDestMissing) {
			t.Errorf("err = %v, want errDestMissing", err)
		}
		if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
			t.Error("destination was recreated")
		}
	}

	t.Run("CopyFile", func(t *testing.T) {
		setGlobals(t, false)
		source := newSource(t)
		dest := filepath.Join(resolvedTempDir(t), "gone")
		for _, file := range []string{"root.txt", "sub/nested.txt"} {
			_, err := CopyFile(filepath.Join(source, file), source, dest, false)
			expectDestMissing(t, err, dest)
		}
	})

	for _, git := range []bool{false, true} {
		name := "Remove no-git"
		if git {
			name = "Remove git"
		}
		t.Run(name, func(t *testing.T) {
			setGlobals(t, git)
			dest := filepath.Join(resolvedTempDir(t), "gone")
			expectDestMissing(t, Remove(dest, []string{"root.txt"}, false), dest)
		})
	}

	t.Run("processEvents", func(t *testing.T) {
		setGlobals(t, false)
		source := newSource(t)
		dest := filepath.Join(resolvedTempDir(t), "gone")
		for _, action := range []EventType{EventModified, EventDeleted} {
			events := []PendingFileChanges{{Path: filepath.Join(source, "sub", "nested.txt"), Action: action}}
			expectDestMissing(t, processEvents(events, source, dest, false), dest)
		}
	})

	t.Run("scanPaths", func(t *testing.T) {
		setGlobals(t, false)
		source := newSource(t)
		dest := filepath.Join(resolvedTempDir(t), "gone")
		SetSourceRoot(source)
		expectDestMissing(t, scanPaths(source, dest, 0, false), dest)
	})
}

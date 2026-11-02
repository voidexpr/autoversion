package main

import (
	"bytes"
	"encoding/json"
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

// setGlobals sets noGit and useRestic for a test and restores them, the size
// limit and the active rules after
func setGlobals(t *testing.T, git bool) {
	t.Helper()
	oldNoGit, oldUseRestic, oldMaxFileSize, oldRules := noGit, useRestic, maxFileSize, activeRules
	noGit, useRestic = !git, false
	t.Cleanup(func() { noGit, useRestic, maxFileSize, activeRules = oldNoGit, oldUseRestic, oldMaxFileSize, oldRules })
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

func ptr[T any](v T) *T { return &v }

// writeOldMarker writes a marker as made before settings were stored in it
func writeOldMarker(t *testing.T, destPath, source string) {
	t.Helper()
	writeFile(t, filepath.Join(destPath, markerFileName), `{"source": "`+source+`"}`+"\n")
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
		writeOldMarker(t, dest, source)
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
		writeOldMarker(t, dest, "/other/source")
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
		writeOldMarker(t, source, "/some/source")
		expectError(t, validate(t, source, filepath.Join(resolvedTempDir(t), "dest")), "swapped")
	})

	t.Run("parent of destination has a marker", func(t *testing.T) {
		outer := resolvedTempDir(t)
		writeOldMarker(t, outer, "/some/source")
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
		if err := initDestination(source, dest, initOptions{}); err != nil {
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
			if err := initDestination(source, dest, initOptions{}); err != nil {
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
		if err := initDestination(oldSource, dest, initOptions{}); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dest, "kept.txt"), "kept")

		if err := initDestination(newSource, dest, initOptions{}); err == nil {
			t.Fatal("expected an error without --force-new-source")
		}
		err := initDestination(newSource, dest, initOptions{forceNewSource: true})
		if err == nil || !strings.Contains(err.Error(), "--force-keep-marker-settings or --force-replace-marker-settings") {
			t.Fatalf("--force-new-source alone: error %v, want one asking for the marker settings flags", err)
		}
		if err := initDestination(newSource, dest, initOptions{forceNewSource: true, keepMarkerSettings: true}); err != nil {
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

		if err := initDestination(source, dest, initOptions{}); err == nil {
			t.Fatal("expected an error without --force-new-source")
		}
		if err := initDestination(source, dest, initOptions{forceNewSource: true}); err != nil {
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
		if err := initDestination("/old/source", dest, initOptions{}); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dest, "sub", "gone.txt"), "gone")

		if err := initDestination(source, dest, initOptions{obliterate: true}); err != nil {
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
		source := resolvedTempDir(t)
		dest := filepath.Join(resolvedTempDir(t), "dest")
		if err := initDestination(source, dest, initOptions{settings: settingsFlags{git: ptr(false)}}); err != nil {
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

func TestMarker_SettingsAndUnknownFields(t *testing.T) {
	t.Run("old marker: settings describe the destination", func(t *testing.T) {
		mirror := resolvedTempDir(t)
		writeOldMarker(t, mirror, "/src")
		versioned := resolvedTempDir(t)
		writeOldMarker(t, versioned, "/src")
		if err := os.Mkdir(filepath.Join(versioned, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		for dest, git := range map[string]bool{mirror: false, versioned: true} {
			marker, err := readMarker(dest)
			if err != nil {
				t.Fatal(err)
			}
			want := destinationSettings{Git: git, MaxFileSizeMB: defaultMaxFileSizeMB}
			if marker.destinationSettings != want {
				t.Errorf("settings %+v, want %+v", marker.destinationSettings, want)
			}
		}
	})

	t.Run("unknown fields are kept", func(t *testing.T) {
		dest := resolvedTempDir(t)
		writeFile(t, filepath.Join(dest, markerFileName),
			`{"remotes": [{"host": "nas", "path": "/b"}], "source": "/src", "git": false, "max_file_size_mb": 50}`)
		marker, err := readMarker(dest)
		if err != nil {
			t.Fatal(err)
		}
		if marker.Git || marker.MaxFileSizeMB != 50 {
			t.Errorf("settings %+v", marker.destinationSettings)
		}
		marker.MaxFileSizeMB = 20
		if err := writeMarker(dest, marker); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(filepath.Join(dest, markerFileName))
		want := `{
  "source": "/src",
  "git": false,
  "restic": false,
  "max_file_size_mb": 20,
  "remotes": [
    {
      "host": "nas",
      "path": "/b"
    }
  ]
}
`
		if string(data) != want {
			t.Errorf("marker\n%s\nwant\n%s", data, want)
		}
	})

	t.Run("invalid fields", func(t *testing.T) {
		for _, text := range []string{`{"source": "/src", "git": "yes"}`, `{"source": "/src", "max_file_size_mb": -1}`,
			`{"source": "/src", "max_file_size_mb": 1.5}`, `{"git": true}`, `[]`} {
			dest := resolvedTempDir(t)
			writeFile(t, filepath.Join(dest, markerFileName), text)
			if _, err := readMarker(dest); err == nil || !strings.Contains(err.Error(), "invalid marker") {
				t.Errorf("%s: error %v, want invalid marker", text, err)
			}
		}
	})
}

func TestValidateSourceAndDestination_Rules(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := resolvedTempDir(t)
	writeOldMarker(t, dest, source)

	result, err := ValidateSourceAndDestination(source, dest)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rules == nil || result.RulesErr != nil || len(result.problems()) != 0 {
		t.Errorf("without a rules file: rules %v, error %v, problems %v", result.Rules, result.RulesErr, result.problems())
	}
	if result.Rules.Decide("node_modules", true).Kept {
		t.Error("without a rules file, the rules are not use default")
	}

	writeFile(t, filepath.Join(dest, filterFileName), "version 1\nexclude /a/\n")
	result, err = ValidateSourceAndDestination(source, dest)
	if err != nil {
		t.Fatal(err)
	}
	if problems := result.problems(); len(problems) != 1 || !strings.Contains(problems[0], "trailing-slash") {
		t.Errorf("problems %v, want the rules error", problems)
	}
}

func TestInitDestination_RulesAndSettings(t *testing.T) {
	setGlobals(t, true)
	readText := func(t *testing.T, path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	initFresh := func(t *testing.T, opts initOptions) (source, dest string) {
		t.Helper()
		source = resolvedTempDir(t)
		writeFile(t, filepath.Join(source, "notes", "a.md"), "a")
		dest = filepath.Join(resolvedTempDir(t), "dest")
		if err := initDestination(source, dest, opts); err != nil {
			t.Fatal(err)
		}
		return source, dest
	}

	t.Run("new destination: settings and default rules, one commit", func(t *testing.T) {
		_, dest := initFresh(t, initOptions{})
		marker, err := readMarker(dest)
		if err != nil {
			t.Fatal(err)
		}
		if marker.destinationSettings != defaultSettings {
			t.Errorf("settings %+v, want %+v", marker.destinationSettings, defaultSettings)
		}
		if got := readText(t, filepath.Join(dest, filterFileName)); got != defaultRules {
			t.Errorf("rules %q, want %q", got, defaultRules)
		}
		if n := gitLogCount(t, dest, filterFileName); n != 1 || gitLogCount(t, dest, "") != 1 {
			t.Errorf("commits: %d of the rules, %d in all, want 1 and 1", n, gitLogCount(t, dest, ""))
		}
	})

	t.Run("rules and settings given", func(t *testing.T) {
		rules := "version 1\nuse default\n\n# notes only\nexclude /*\n/notes\n"
		_, dest := initFresh(t, initOptions{rules: rules, settings: settingsFlags{git: ptr(false)}})
		if got := readText(t, filepath.Join(dest, filterFileName)); got != rules {
			t.Errorf("rules %q, want %q", got, rules)
		}
		marker, _ := readMarker(dest)
		if want := (destinationSettings{MaxFileSizeMB: defaultMaxFileSizeMB}); marker.destinationSettings != want || IsGitRepo(dest) {
			t.Errorf("settings %+v, want %+v without git", marker.destinationSettings, want)
		}
	})

	t.Run("invalid rules create nothing", func(t *testing.T) {
		for rules, reason := range map[string]string{
			"version 1\nexclude /a/\n":           "trailing-slash",
			"version 1\nexclude /*\n/CLAUD.md\n": "none of the includes exists",
		} {
			source := resolvedTempDir(t)
			dest := filepath.Join(resolvedTempDir(t), "dest")
			err := initDestination(source, dest, initOptions{rules: rules})
			if err == nil || !strings.Contains(err.Error(), reason) {
				t.Errorf("%q: error %v, want %s", rules, err, reason)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Errorf("%q: destination created", rules)
			}
		}
	})

	t.Run("initialized: other rules go through filter set, the same are fine", func(t *testing.T) {
		source, dest := initFresh(t, initOptions{rules: starterRules})
		err := initDestination(source, dest, initOptions{rules: "version 1\nuse default\n"})
		if err == nil || !strings.Contains(err.Error(), "filter set") {
			t.Errorf("error %v, want one pointing to filter set", err)
		}
		if err := initDestination(source, dest, initOptions{rules: starterRules}); err != nil {
			t.Errorf("the same rules: %v", err)
		}
		if n := gitLogCount(t, dest, ""); n != 1 {
			t.Errorf("commits %d, want 1 (nothing changed)", n)
		}
	})

	t.Run("initialized: settings change only with --update-settings", func(t *testing.T) {
		source, dest := initFresh(t, initOptions{settings: settingsFlags{git: ptr(false)}})
		restartedJobs = nil
		marker, _ := readMarker(dest)
		marker.other["remotes"] = []byte(`["nas"]`)
		if err := writeMarker(dest, marker); err != nil {
			t.Fatal(err)
		}

		if err := initDestination(source, dest, initOptions{settings: settingsFlags{git: ptr(false)}}); err != nil {
			t.Errorf("same settings: %v", err)
		}
		opts := initOptions{settings: settingsFlags{git: ptr(true), restic: ptr(false)}}
		err := initDestination(source, dest, opts)
		if err == nil || !strings.Contains(err.Error(), "git off → on") || !strings.Contains(err.Error(), "--update-settings") {
			t.Errorf("error %v, want the change and --update-settings", err)
		}
		if len(restartedJobs) != 0 {
			t.Fatalf("restarted %v without a change", restartedJobs)
		}
		opts.updateSettings = true
		if err := initDestination(source, dest, opts); err != nil {
			t.Fatal(err)
		}
		if len(restartedJobs) != 1 || restartedJobs[0] != [2]string{source, dest} {
			t.Errorf("restarted %v, want the service of %s", restartedJobs, dest)
		}
		marker, _ = readMarker(dest)
		var remotes bytes.Buffer
		json.Compact(&remotes, marker.other["remotes"])
		if !marker.Git || remotes.String() != `["nas"]` {
			t.Errorf("marker %+v, remotes %s", marker.destinationSettings, marker.other["remotes"])
		}
		if n := gitLogCount(t, dest, ""); n != 1 {
			t.Errorf("commits %d, want 1 (the marker)", n)
		}
	})

	t.Run("a setting given but only inferred is stored", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := resolvedTempDir(t)
		markerPath := filepath.Join(dest, markerFileName)
		writeFile(t, markerPath, `{"source": "`+source+`", "remotes": ["nas"]}`)
		noGit := initOptions{settings: settingsFlags{git: ptr(false)}}
		restartedJobs = nil

		// Without flags, an old marker stays as it is
		before := readText(t, markerPath)
		if err := initDestination(source, dest, initOptions{}); err != nil {
			t.Fatal(err)
		}
		if got := readText(t, markerPath); got != before {
			t.Errorf("marker rewritten without a flag: %s", got)
		}

		if err := initDestination(source, dest, noGit); err != nil {
			t.Fatal(err)
		}
		stored := readText(t, markerPath)
		if !strings.Contains(stored, `"git": false`) || !strings.Contains(stored, `"nas"`) {
			t.Errorf("marker %s, want git false and the unknown field", stored)
		}
		if IsGitRepo(dest) || len(restartedJobs) != 0 {
			t.Errorf("git repository %v, restarted %v: nothing changes for the destination", IsGitRepo(dest), restartedJobs)
		}
		if err := initDestination(source, dest, noGit); err != nil {
			t.Fatal(err)
		}
		if got := readText(t, markerPath); got != stored {
			t.Errorf("marker rewritten a second time: %s", got)
		}

		// A .git folder appearing later doesn't turn it into a git destination
		if err := os.Mkdir(filepath.Join(dest, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
		marker, err := readMarker(dest)
		if err != nil || marker.Git {
			t.Errorf("marker git %v (%v), want false", marker.Git, err)
		}
		if err := initDestination(source, dest, noGit); err != nil {
			t.Errorf("--no-git with a stored git false: %v", err)
		}
	})

	t.Run("an old marker on a git destination", func(t *testing.T) {
		source := resolvedTempDir(t)
		dest := resolvedTempDir(t)
		writeOldMarker(t, dest, source)
		if err := Init(dest, false); err != nil {
			t.Fatal(err)
		}
		if err := Add(dest, []string{"."}, false); err != nil {
			t.Fatal(err)
		}
		if err := Commit(dest, "old commit"); err != nil {
			t.Fatal(err)
		}
		before := readText(t, filepath.Join(dest, markerFileName))

		err := initDestination(source, dest, initOptions{settings: settingsFlags{git: ptr(false)}})
		if err == nil || !strings.Contains(err.Error(), "versioned with git") {
			t.Errorf("--no-git: error %v", err)
		}
		if got := readText(t, filepath.Join(dest, markerFileName)); got != before {
			t.Errorf("marker changed by a refused --no-git: %s", got)
		}

		if err := initDestination(source, dest, initOptions{settings: settingsFlags{git: ptr(true)}}); err != nil {
			t.Fatal(err)
		}
		if n := gitLogCount(t, dest, markerFileName); n != 2 {
			t.Errorf("%d commits of the marker, want 2", n)
		}
	})

	t.Run("git to plain mirror is refused", func(t *testing.T) {
		source, dest := initFresh(t, initOptions{})
		for _, update := range []bool{false, true} {
			err := initDestination(source, dest, initOptions{updateSettings: update, settings: settingsFlags{git: ptr(false)}})
			if err == nil || !strings.Contains(err.Error(), "remove "+filepath.Join(dest, ".git")+" by hand") {
				t.Errorf("--update-settings %v: error %v", update, err)
			}
		}
		other := resolvedTempDir(t)
		err := initDestination(other, dest, initOptions{forceNewSource: true, replaceMarkerSettings: true, settings: settingsFlags{git: ptr(false)}})
		if err == nil || !strings.Contains(err.Error(), "versioned with git") {
			t.Errorf("--force-new-source --no-git: error %v", err)
		}
	})

	t.Run("a new source keeps or replaces the marker's settings", func(t *testing.T) {
		mirror := settingsFlags{git: ptr(false)}
		_, dest := initFresh(t, initOptions{settings: mirror})

		// Each init switches to a new source: the same source would be a
		// settings update
		if err := initDestination(resolvedTempDir(t), dest, initOptions{forceNewSource: true, keepMarkerSettings: true}); err != nil {
			t.Fatal(err)
		}
		marker, _ := readMarker(dest)
		if marker.Git || IsGitRepo(dest) {
			t.Errorf("keep: settings %v, git repo %v", marker.destinationSettings, IsGitRepo(dest))
		}

		if err := initDestination(resolvedTempDir(t), dest, initOptions{forceNewSource: true, keepMarkerSettings: true, settings: settingsFlags{git: ptr(true)}}); err != nil {
			t.Fatal(err)
		}
		marker, _ = readMarker(dest)
		if !marker.Git || !IsGitRepo(dest) {
			t.Errorf("keep with a flag: settings %v, git repo %v", marker.destinationSettings, IsGitRepo(dest))
		}

		_, dest = initFresh(t, initOptions{settings: mirror})
		if err := initDestination(resolvedTempDir(t), dest, initOptions{forceNewSource: true, replaceMarkerSettings: true}); err != nil {
			t.Fatal(err)
		}
		marker, _ = readMarker(dest)
		if marker.destinationSettings != defaultSettings || !IsGitRepo(dest) {
			t.Errorf("replace: settings %v, git repo %v", marker.destinationSettings, IsGitRepo(dest))
		}
	})

	t.Run("plain mirror to git", func(t *testing.T) {
		source, dest := initFresh(t, initOptions{settings: settingsFlags{git: ptr(false)}})
		if err := initDestination(source, dest, initOptions{updateSettings: true, settings: settingsFlags{git: ptr(true)}}); err != nil {
			t.Fatal(err)
		}
		marker, _ := readMarker(dest)
		if !marker.Git || !IsGitRepo(dest) || gitLogCount(t, dest, markerFileName) != 1 {
			t.Errorf("git %v, repo %v", marker.Git, IsGitRepo(dest))
		}
	})

	t.Run("a new source keeps the rules", func(t *testing.T) {
		rules := "version 1\nexclude /*\n/notes\n"
		_, dest := initFresh(t, initOptions{rules: rules})
		newSource := resolvedTempDir(t)
		writeFile(t, filepath.Join(newSource, "notes", "b.md"), "b")
		if err := initDestination(newSource, dest, initOptions{forceNewSource: true, keepMarkerSettings: true}); err != nil {
			t.Fatal(err)
		}
		if got := readText(t, filepath.Join(dest, filterFileName)); got != rules {
			t.Errorf("rules %q, want %q", got, rules)
		}
	})
}

func TestInitCmd_OverrideFlagsAreExclusive(t *testing.T) {
	setGlobals(t, false)
	defer func() {
		resetFlags(rootCmd)
		sourceFlag, destFlag = "", ""
		rootCmd.SetArgs(nil)
	}()
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	for _, flags := range [][]string{
		{"--force-new-source", "--force-obliterate-yes-i-am-sure"},
		{"--force-new-source", "--force-keep-marker-settings", "--force-replace-marker-settings"},
		{"--force-keep-marker-settings"},
		{"--force-replace-marker-settings"},
		{"--mirror", "--git"},
		{"--mirror", "--restic"},
	} {
		resetFlags(rootCmd)
		dest := filepath.Join(resolvedTempDir(t), "dest")
		rootCmd.SetArgs(append([]string{"init", "--source", resolvedTempDir(t), "--destination", dest, "--git", "--use", "default"}, flags...))
		if err := rootCmd.Execute(); err == nil {
			t.Errorf("%v: expected an error", flags)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Errorf("%v: destination created despite the error", flags)
		}
	}
}

// init says how the destination versions, and takes rules
func TestInitCmd_RequiresVersioningAndRules(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := filepath.Join(resolvedTempDir(t), "dest")

	_, err := runCLI(t, "init", "--source", source, "--destination", dest, "--use", "default")
	if err == nil || !strings.Contains(err.Error(), "--git, --restic (or both), or --mirror") {
		t.Errorf("without a versioning flag: %v", err)
	}
	_, err = runCLI(t, "init", "--source", source, "--destination", dest, "--mirror")
	if err == nil || !strings.Contains(err.Error(), "init needs rules") || !strings.Contains(err.Error(), starterRules) {
		t.Errorf("without rules: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("destination created despite the errors")
	}

	if _, err := runCLI(t, "init", "--source", source, "--destination", dest, "--mirror", "--use", "default", "--use", "nested-git", "--max-file-size", "10M"); err != nil {
		t.Fatal(err)
	}
	marker, err := readMarker(dest)
	if err != nil || marker.Git || marker.Restic || IsGitRepo(dest) {
		t.Errorf("mirror: marker %+v (%v), git repo %v", marker, err, IsGitRepo(dest))
	}
	if got, _ := os.ReadFile(filepath.Join(dest, filterFileName)); string(got) != starterRules {
		t.Errorf("rules %q, want the starter file", got)
	}
}

func TestValidatePaths_RequiresMatchingMarker(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := resolvedTempDir(t)

	if _, err := validatePaths(source, dest); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("without marker: err = %v", err)
	}
	writeOldMarker(t, dest, "/other/source")
	if _, err := validatePaths(source, dest); err == nil || !strings.Contains(err.Error(), "belongs to source") {
		t.Errorf("marker for another source: err = %v", err)
	}
	writeOldMarker(t, dest, source)
	if _, err := validatePaths(source, dest); err != nil {
		t.Errorf("matching marker: err = %v", err)
	}
}

func TestScanPaths_KeepsDestinationMarker(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := resolvedTempDir(t)
	if err := initDestination(source, dest, initOptions{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "a.txt"), "a")
	// A marker or rules file at the source root must not be copied over the destination's
	writeFile(t, filepath.Join(source, markerFileName), `{"source": "/elsewhere"}`)
	writeFile(t, filepath.Join(source, filterFileName), "version 1\n")

	setRules(t, source)
	if err := scanPaths(source, dest, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.txt")); err != nil {
		t.Errorf("a.txt not copied: %v", err)
	}
	if got := markerSourceOf(t, dest); got != source {
		t.Errorf("destination marker source = %q, want %q (deleted or overwritten)", got, source)
	}
	if data, err := os.ReadFile(filepath.Join(dest, filterFileName)); err != nil || string(data) != defaultRules {
		t.Errorf("destination rules %q (%v), want %q (deleted or overwritten)", data, err, defaultRules)
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
		setRules(t, source)
		for _, action := range []EventType{EventModified, EventDeleted} {
			events := []PendingFileChanges{{Path: filepath.Join(source, "sub", "nested.txt"), Action: action}}
			expectDestMissing(t, processEvents(events, source, dest, false), dest)
		}
	})

	t.Run("scanPaths", func(t *testing.T) {
		setGlobals(t, false)
		source := newSource(t)
		dest := filepath.Join(resolvedTempDir(t), "gone")
		setRules(t, source)
		expectDestMissing(t, scanPaths(source, dest, 0, false), dest)
	})
}

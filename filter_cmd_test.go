package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nicolas/autoversion/filterrules"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// runCLI runs autoversion with args and returns what it prints. Flags are
// reset after: cobra keeps their values, and Changed, between runs.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() {
		rootCmd.SetArgs(args)
		rootCmd.SetErr(io.Discard)
		err = rootCmd.Execute()
	})
	resetFlags(rootCmd)
	rootCmd.SetArgs(nil)
	return out, err
}

func resetFlags(cmd *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if s, ok := f.Value.(pflag.SliceValue); ok {
			s.Replace(nil)
		} else {
			f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	cmd.Flags().VisitAll(reset)
	cmd.PersistentFlags().VisitAll(reset)
	for _, c := range cmd.Commands() {
		resetFlags(c)
	}
}

// newRulesSource returns a source tree for the rules tests
func newRulesSource(t *testing.T) string {
	t.Helper()
	source := resolvedTempDir(t)
	for _, file := range []string{"keep.txt", "notes/a.md", "notes/b.md", "node_modules/x.js",
		"sub/.git/config", "sub/c.md", "skipped/deep/d.txt", filterFileName} {
		writeFile(t, filepath.Join(source, file), "x")
	}
	writeFile(t, filepath.Join(source, "big.bin"), strings.Repeat("x", 1024*1024+1))
	if err := os.Symlink("keep.txt", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	return source
}

func defaultRulesWith(t *testing.T, lines ...string) *filterrules.Rules {
	t.Helper()
	_, rules, err := parseRules(defaultRules + strings.Join(lines, "\n") + "\n")
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestSourceRulesWalk(t *testing.T) {
	source := newRulesSource(t)
	s := &sourceRules{root: source, rules: defaultRulesWith(t, "exclude /skipped")}
	walk := func(dir string, stopAt string) ([]string, int, error) {
		var files []string
		dirs, err := s.walk(dir, func(path string, _ fs.FileInfo) error {
			rel, _ := s.rel(path)
			files = append(files, rel)
			if rel == stopAt {
				return errDestMissing
			}
			return nil
		})
		return files, dirs, err
	}

	// The size limit is the caller's to check
	files, dirs, err := walk(source, "")
	if want := []string{"big.bin", "keep.txt", "notes/a.md", "notes/b.md", "sub/c.md"}; !slices.Equal(files, want) || err != nil {
		t.Errorf("files %q (error %v), want %q", files, err, want)
	}
	// The root, notes and sub: node_modules, .git and skipped aren't entered
	if dirs != 3 {
		t.Errorf("folders visited %d, want 3", dirs)
	}

	if files, _, _ := walk(filepath.Join(source, "notes"), ""); !slices.Equal(files, []string{"notes/a.md", "notes/b.md"}) {
		t.Errorf("from notes: files %q", files)
	}
	if files, _, _ := walk(filepath.Join(source, "skipped"), ""); len(files) != 0 {
		t.Errorf("from a folder the rules leave out: files %q", files)
	}
	if files, _, err := walk(source, "keep.txt"); len(files) != 2 || err != errDestMissing {
		t.Errorf("an error stops the walk: files %q, error %v", files, err)
	}
}

func TestExplain(t *testing.T) {
	source := newRulesSource(t)
	s := &sourceRules{root: source, rules: defaultRulesWith(t, "exclude /skipped")}
	tests := map[string]string{
		"keep.txt":             "keep.txt (file): kept: no rule matches it or a folder above it",
		"node_modules/x.js":    "node_modules/x.js (file): left out by exclude /**/node_modules (group deps), matching node_modules",
		"skipped":              "skipped (folder): left out by exclude /skipped, matching skipped",
		"sub/.git/config":      "sub/.git/config (file): left out: always applied",
		filterFileName:         filterFileName + " (file): left out: always applied",
		"link":                 "link (file): left out: not a regular file",
		"big.bin":              "big.bin (file): left out: larger than 1 MB (kept: no rule",
		"gone.log":             "gone.log (missing, decided as a file): left out by exclude f:/**/*.log (group logs)",
		source + "/notes/a.md": "notes/a.md (file): kept",
	}
	for path, want := range tests {
		out := captureStdout(t, func() {
			if err := explain(s, path, 1); err != nil {
				t.Error(err)
			}
		})
		if !strings.HasPrefix(out, want) {
			t.Errorf("explain %s:\n got %q\nwant %q...", path, out, want)
		}
	}
	if err := explain(s, "../outside", 1); err == nil {
		t.Error("a path outside the source is explained")
	}
}

func setRuleFlags(t *testing.T, filter string, uses, includes, excludes []string) {
	t.Helper()
	filterFileFlag, useFlags, includeFlags, excludeFlags = filter, uses, includes, excludes
	t.Cleanup(func() { filterFileFlag, useFlags, includeFlags, excludeFlags = "", nil, nil, nil })
}

func TestRulesText(t *testing.T) {
	source := resolvedTempDir(t)
	file := filepath.Join(resolvedTempDir(t), "rules.filter")
	written := "version 1\nuse default   # the groups\n\nexclude /*\n/notes   # kept\n"

	t.Run("none", func(t *testing.T) {
		if text, err := rulesText(source); text != "" || err != nil {
			t.Errorf("rules %q, error %v", text, err)
		}
	})
	t.Run("flags, one per line", func(t *testing.T) {
		setRuleFlags(t, "", []string{"default"}, []string{"/notes"}, []string{"/*"})
		text, err := rulesText(source)
		if want := "version 1\nuse default\n\ninclude /notes\nexclude /*\n"; text != want || err != nil {
			t.Errorf("rules %q (error %v), want %q", text, err, want)
		}
	})
	t.Run("a file, as written", func(t *testing.T) {
		writeFile(t, file, written)
		setRuleFlags(t, file, nil, nil, nil)
		if text, err := rulesText(source); text != written || err != nil {
			t.Errorf("rules %q (error %v), want %q", text, err, written)
		}
	})
	t.Run("a file's root line names the source and is removed", func(t *testing.T) {
		writeFile(t, file, "version 1\nroot "+source+"\n"+strings.TrimPrefix(written, "version 1\n"))
		setRuleFlags(t, file, nil, nil, nil)
		if text, err := rulesText(source); text != written || err != nil {
			t.Errorf("rules %q (error %v), want %q", text, err, written)
		}
		if _, err := rulesText(resolvedTempDir(t)); err == nil || !strings.Contains(err.Error(), "is not the source") {
			t.Errorf("another source: error %v", err)
		}
	})
	t.Run("a file and flags", func(t *testing.T) {
		writeFile(t, file, written)
		setRuleFlags(t, file, []string{"os", "default"}, []string{"/x/y"}, []string{"/x"})
		want := "version 1\nuse os\nuse default   # the groups\n\nexclude /*\n/notes   # kept\ninclude /x/y\nexclude /x\n"
		if text, err := rulesText(source); text != want || err != nil {
			t.Errorf("rules %q (error %v), want %q", text, err, want)
		}
		rules, err := rulesFromFlags(source)
		if err != nil || rules.Decide("x", false).Kept || !rules.Decide("notes", true).Kept {
			t.Errorf("scan and dump combine them: error %v", err)
		}
	})
	t.Run("use from a flag, after the version line", func(t *testing.T) {
		writeFile(t, file, "# notes only\nversion 1\nexclude /*\n/notes")
		setRuleFlags(t, file, []string{"default"}, nil, nil)
		want := "# notes only\nversion 1\nuse default\nexclude /*\n/notes\n"
		if text, err := rulesText(source); text != want || err != nil {
			t.Errorf("rules %q (error %v), want %q", text, err, want)
		}
	})
	t.Run("stdin", func(t *testing.T) {
		stdin := filepath.Join(resolvedTempDir(t), "stdin")
		writeFile(t, stdin, "version 1\nexclude /*\n/notes\n")
		f, err := os.Open(stdin)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		oldStdin := os.Stdin
		os.Stdin = f
		defer func() { os.Stdin = oldStdin }()
		setRuleFlags(t, "-", []string{"default"}, nil, nil)
		rules, err := rulesFromFlags(source)
		if err != nil || rules.Decide("other", true).Kept || !rules.Decide("notes", true).Kept || rules.Decide("notes/x.log", false).Kept {
			t.Errorf("rules from stdin: error %v", err)
		}
	})
	t.Run("a file without rules", func(t *testing.T) {
		writeFile(t, file, "version 1\n# nothing yet\n")
		setRuleFlags(t, file, nil, nil, nil)
		if _, err := rulesText(source); err == nil || !strings.Contains(err.Error(), "no rules") {
			t.Errorf("error %v, want no rules", err)
		}
		writeFile(t, file, "exclude /x\n")
		if _, err := rulesText(source); err == nil || !strings.Contains(err.Error(), "version 1 comes first") {
			t.Errorf("no version line: error %v", err)
		}
	})
	t.Run("invalid rule", func(t *testing.T) {
		setRuleFlags(t, "", nil, nil, []string{"node_modules"})
		if _, err := rulesText(source); err == nil || !strings.Contains(err.Error(), "--exclude: no-root") {
			t.Errorf("error %v", err)
		}
	})
}

// newSyncedDestination returns a source with three files and its
// destination, initialized and synced with the default rules
func newSyncedDestination(t *testing.T, git bool) (*PathValidationResult, string) {
	t.Helper()
	setGlobals(t, git)
	source := resolvedTempDir(t)
	for _, file := range []string{"a.txt", "b.txt", "notes/n.md"} {
		writeFile(t, filepath.Join(source, file), file)
	}
	dest := filepath.Join(resolvedTempDir(t), "dest")
	if err := initDestination(source, dest, initOptions{settings: settingsFlags{git: &git}}); err != nil {
		t.Fatal(err)
	}
	setRules(t, source)
	if err := scanPaths(source, dest, 0, false); err != nil {
		t.Fatal(err)
	}
	result, err := ValidateSourceAndDestination(source, dest)
	if err != nil {
		t.Fatal(err)
	}
	return result, dest
}

func TestApplyRules(t *testing.T) {
	notesOnly := "version 1\nuse default\nexclude /*\n/notes\n"
	readRulesFile := func(t *testing.T, dest string) string {
		t.Helper()
		data, _ := os.ReadFile(filepath.Join(dest, filterFileName))
		return string(data)
	}
	apply := func(t *testing.T, result *PathValidationResult, text string, replace, dry bool) (string, error) {
		t.Helper()
		dryRun = dry
		t.Cleanup(func() { dryRun = false })
		restartedJobs = nil
		var err error
		out := captureStdout(t, func() { err = applyRules(result, text, replace) })
		return out, err
	}

	t.Run("dry run changes nothing", func(t *testing.T) {
		result, dest := newSyncedDestination(t, true)
		commits := gitLogCount(t, dest, "")
		out, err := apply(t, result, notesOnly, true, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Kept: 1 files", "removes 2 files", "kept in git history", "  a.txt\n", "New rules:", "Dry run"} {
			if !strings.Contains(out, want) {
				t.Errorf("output has no %q:\n%s", want, out)
			}
		}
		if readRulesFile(t, dest) != defaultRules || gitLogCount(t, dest, "") != commits || len(restartedJobs) != 0 {
			t.Error("dry run wrote, committed or restarted")
		}
	})

	t.Run("set writes, commits and restarts", func(t *testing.T) {
		result, dest := newSyncedDestination(t, true)
		out, err := apply(t, result, notesOnly, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if readRulesFile(t, dest) != notesOnly || gitLogCount(t, dest, filterFileName) != 2 {
			t.Errorf("rules %q, %d commits of them", readRulesFile(t, dest), gitLogCount(t, dest, filterFileName))
		}
		if !strings.Contains(out, "+/notes") || !strings.Contains(out, "Committed") {
			t.Errorf("no diff or commit in the output:\n%s", out)
		}
		if len(restartedJobs) != 1 || restartedJobs[0] != [2]string{result.SourceResolved, dest} {
			t.Errorf("restarted %v", restartedJobs)
		}
	})

	t.Run("check commits a hand edit", func(t *testing.T) {
		result, dest := newSyncedDestination(t, true)
		writeFile(t, filepath.Join(dest, filterFileName), notesOnly)
		if _, err := apply(t, result, notesOnly, false, false); err != nil {
			t.Fatal(err)
		}
		if n := gitLogCount(t, dest, filterFileName); n != 2 {
			t.Errorf("%d commits of the rules, want 2", n)
		}
		out, err := apply(t, result, notesOnly, false, false)
		if err != nil || !strings.Contains(out, "no change since the last commit") {
			t.Errorf("second check: error %v\n%s", err, out)
		}
	})

	t.Run("refused rules change nothing", func(t *testing.T) {
		result, dest := newSyncedDestination(t, true)
		for text, reason := range map[string]string{
			"version 1\nexclude /*\nf:/**/*.mdd\n": "keep no file",
			"version 1\nexclude /*\n/note\n":       "none of the includes exists",
			"version 1\nexclude /*\n":              "whole-root",
		} {
			if _, err := apply(t, result, text, true, false); err == nil || !strings.Contains(err.Error(), reason) {
				t.Errorf("%q: error %v, want %s", text, err, reason)
			}
		}
		if readRulesFile(t, dest) != defaultRules || len(restartedJobs) != 0 {
			t.Error("refused rules were written or restarted the service")
		}
	})

	t.Run("plain mirror", func(t *testing.T) {
		result, dest := newSyncedDestination(t, false)
		out, err := apply(t, result, notesOnly, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "not kept anywhere") || readRulesFile(t, dest) != notesOnly {
			t.Errorf("rules %q\n%s", readRulesFile(t, dest), out)
		}
	})
}

func TestCopyCommandsUseTheDestination(t *testing.T) {
	for _, git := range []bool{true, false} {
		result, dest := newSyncedDestination(t, git)
		source := result.SourceResolved
		if _, err := runCLI(t, "filter", "set", "--destination", dest, "--use", "default", "--exclude", "/a.txt"); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(source, "c.log"), "log")
		if out, err := runCLI(t, "sync", "--source", source, "--destination", dest); err != nil {
			t.Fatalf("sync: %v\n%s", err, out)
		}
		for file, want := range map[string]bool{"a.txt": false, "b.txt": true, "notes/n.md": true, "c.log": false} {
			if _, err := os.Stat(filepath.Join(dest, file)); (err == nil) != want {
				t.Errorf("git %v: %s in the destination: %v, want %v", git, file, err == nil, want)
			}
		}
		if IsGitRepo(dest) != git {
			t.Errorf("git %v: the destination's git repository changed", git)
		}
	}

	result, dest := newSyncedDestination(t, true)
	source := result.SourceResolved
	for _, args := range [][]string{
		{"sync", "--no-git"}, {"sync", "--restic"}, {"sync", "--max-file-size", "5"},
		{"sync", "--exclude-dir", "x"}, {"sync", "--only-include-extensions", "md"}, {"sync", "--exclude", "/x"},
		{"check-paths", "--no-git"},
	} {
		_, err := runCLI(t, append(args, "--source", source, "--destination", dest)...)
		if err == nil || !strings.Contains(err.Error(), "set them with init or filter set") {
			t.Errorf("%v: error %v", args, err)
		}
	}
	for _, args := range [][]string{{"--exclude", "/x"}, {"--use", "os"}, {"--max-file-size", "5"}} {
		_, err := runCLI(t, append([]string{"watch", "--source", source, "--destination", dest}, args...)...)
		if err != errDestinationSettings {
			t.Errorf("watch %v: error %v", args, err)
		}
	}
	// --report-only takes rules
	if _, err := runCLI(t, "watch", "--report-only", "--source", source, "--exclude", "node_modules"); err == nil || !strings.Contains(err.Error(), "no-root") {
		t.Errorf("watch --report-only with an invalid rule: error %v", err)
	}
}

func TestFilterCommands(t *testing.T) {
	result, dest := newSyncedDestination(t, true)
	source := result.SourceResolved
	restartedJobs = nil

	out, err := runCLI(t, "filter", "scan", "--source", source, "--summary")
	if err != nil || out != "3 files kept, 2 folders visited\n" {
		t.Errorf("scan --summary: %v\n%s", err, out)
	}
	out, err = runCLI(t, "filter", "scan", "--source", source, "--exclude", "/notes")
	if err != nil || out != "a.txt\nb.txt\n" {
		t.Errorf("scan --exclude: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "filter", "scan", "--source", source, "--destination", dest, "--use", "os"); err == nil {
		t.Error("scan with --destination and rules is accepted")
	}
	out, err = runCLI(t, "filter", "dump", "--use", "temp")
	if err != nil || !strings.Contains(out, "# group temp\nexclude f:/**/*.tmp\n") || !strings.HasPrefix(out, "# always applied") {
		t.Errorf("dump: %v\n%s", err, out)
	}
	out, err = runCLI(t, "filter", "groups")
	if err != nil || !strings.Contains(out, "# default: all the groups above (use default)") {
		t.Errorf("groups: %v\n%s", err, out)
	}

	out, err = runCLI(t, "filter", "set", "--destination", dest, "--use", "default", "--exclude", "/b.txt", "--dry-run")
	if err != nil || !strings.Contains(out, "removes 1 files") || len(restartedJobs) != 0 {
		t.Errorf("set --dry-run: %v, restarted %v\n%s", err, restartedJobs, out)
	}
	out, err = runCLI(t, "filter", "set", "--destination", dest, "--use", "default", "--exclude", "/b.txt")
	if err != nil || len(restartedJobs) != 1 {
		t.Errorf("set: %v, restarted %v\n%s", err, restartedJobs, out)
	}
	out, err = runCLI(t, "filter", "scan", "--source", source, "--destination", dest)
	if err != nil || out != "a.txt\nnotes/n.md\n" {
		t.Errorf("scan --destination: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "init", "--source", source, "--destination", dest, "--exclude", "/a.txt"); err == nil {
		t.Error("init with rules on an initialized destination is accepted")
	}
}

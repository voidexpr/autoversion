package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout returns what fn prints to os.Stdout
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// newTestLogFile returns a logFile whose out is a temp file standing in for stdout
func newTestLogFile(t *testing.T, maxSize int64) *logFile {
	t.Helper()
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "original"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	l := &logFile{out: out, path: filepath.Join(dir, stdoutLogName), maxSize: maxSize}
	if err := l.open(); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *logFile) write(t *testing.T, s string) {
	t.Helper()
	if _, err := l.out.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLogFile_WritesGoToPath(t *testing.T) {
	l := newTestLogFile(t, 100)
	l.write(t, "hello\n")
	if got := readFile(t, l.path); got != "hello\n" {
		t.Errorf("log content = %q, want %q", got, "hello\n")
	}
}

func TestLogFile_NoRotationBelowLimit(t *testing.T) {
	l := newTestLogFile(t, 100)
	l.write(t, "small\n")
	if err := l.check(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rotatedLogPath(l.path, 1)); !os.IsNotExist(err) {
		t.Errorf("expected no rotated file, stat err = %v", err)
	}
	l.write(t, "more\n")
	if got := readFile(t, l.path); got != "small\nmore\n" {
		t.Errorf("log content = %q", got)
	}
}

func TestLogFile_RotatesAndKeepsN(t *testing.T) {
	l := newTestLogFile(t, 10)
	gens := logKeep + 2
	for i := 1; i <= gens; i++ {
		l.write(t, fmt.Sprintf("gen%d over the limit\n", i))
		if err := l.check(); err != nil {
			t.Fatal(err)
		}
	}
	l.write(t, "current\n")

	if got := readFile(t, l.path); got != "current\n" {
		t.Errorf("%s = %q, want %q", filepath.Base(l.path), got, "current\n")
	}
	// path.1 holds the latest generation, path.<logKeep> the oldest kept
	for n := 1; n <= logKeep; n++ {
		want := fmt.Sprintf("gen%d over the limit\n", gens+1-n)
		if got := readFile(t, rotatedLogPath(l.path, n)); got != want {
			t.Errorf("%s.%d = %q, want %q", filepath.Base(l.path), n, got, want)
		}
	}
	if _, err := os.Stat(rotatedLogPath(l.path, logKeep+1)); !os.IsNotExist(err) {
		t.Errorf("expected %s.%d to not exist, stat err = %v", filepath.Base(l.path), logKeep+1, err)
	}
}

func TestLogFile_ReopensRemovedFile(t *testing.T) {
	l := newTestLogFile(t, 100)
	l.write(t, "before\n")
	if err := os.Remove(l.path); err != nil {
		t.Fatal(err)
	}
	if err := l.check(); err != nil {
		t.Fatal(err)
	}
	l.write(t, "after\n")
	if got := readFile(t, l.path); got != "after\n" {
		t.Errorf("log content = %q, want %q", got, "after\n")
	}
}

func TestRemoveLogs(t *testing.T) {
	dir := t.TempDir()
	stderrPath := filepath.Join(dir, "stderr.log")
	for _, name := range []string{"stderr.log", "stderr.log.1", "stderr.log.5", "stdout.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeLogs(stderrPath); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "stdout.log" {
		t.Errorf("remaining files = %v, want only stdout.log", entries)
	}
}

func TestLogEvent(t *testing.T) {
	origDebug, origReportOnly := debug, reportOnly
	defer func() { debug, reportOnly = origDebug, origReportOnly }()

	tests := []struct {
		name       string
		debug      bool
		reportOnly bool
		tracked    bool
		want       string
	}{
		{"tracked", false, false, true, "event BUFFER modified /src/a.txt"},
		{"skipped without debug", false, false, false, ""},
		{"skipped with debug", true, false, false, "event skip   modified /src/a.txt"},
		{"tracked in report-only", false, true, true, "event track  modified /src/a.txt"},
		{"skipped in report-only", false, true, false, "event skip   modified /src/a.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			debug, reportOnly = tt.debug, tt.reportOnly
			out := captureStdout(t, func() { logEvent(EventModified, "/src/a.txt", tt.tracked) })
			if tt.want == "" && out != "" {
				t.Errorf("expected no output, got %q", out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("output %q does not contain %q", out, tt.want)
			}
		})
	}
}

func TestColorize_NotTerminal(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := colorize(f, colorBold, "msg"); got != "msg" {
		t.Errorf("colorize to a file = %q, want plain %q", got, "msg")
	}
}

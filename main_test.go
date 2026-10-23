package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessEvents_SkipsUnchangedFiles(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := resolvedTempDir(t)
	file := filepath.Join(source, "a.txt")
	writeFile(t, file, "a")
	events := []PendingFileChanges{{Path: file, Action: EventModified}}

	process := func() string {
		return captureStdout(t, func() {
			if err := processEvents(events, source, dest, false); err != nil {
				t.Error(err)
			}
		})
	}

	if out := process(); !strings.Contains(out, "copy '") {
		t.Errorf("first event not copied: %q", out)
	}
	// A second event for the unchanged file (on macOS, the one triggered by
	// autoversion's own copy) must not copy again
	if out := process(); strings.Contains(out, "copy '") {
		t.Errorf("unchanged file copied again: %q", out)
	}

	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("more")
	f.Close()
	if out := process(); !strings.Contains(out, "copy '") {
		t.Errorf("changed file not copied: %q", out)
	}
}

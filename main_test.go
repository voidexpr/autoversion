package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandDirChanges(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := resolvedTempDir(t)
	SetSourceRoot(source)
	writeFile(t, filepath.Join(source, "dir", "a.txt"), "a")
	writeFile(t, filepath.Join(source, "dir", "sub", "b.txt"), "b")
	writeFile(t, filepath.Join(source, "dir", "node_modules", "x.js"), "x")
	writeFile(t, filepath.Join(source, "dir", "skip.log"), "log")
	writeFile(t, filepath.Join(source, "other.txt"), "other")
	// In the destination: a file gone from the source, one kept, one outside dir
	writeFile(t, filepath.Join(dest, "dir", "gone.txt"), "gone")
	writeFile(t, filepath.Join(dest, "dir", "a.txt"), "old a")
	writeFile(t, filepath.Join(dest, "kept.txt"), "kept")
	writeFile(t, filepath.Join(dest, markerFileName), "{}")

	events := expandDirChanges([]PendingFileChanges{
		{Path: filepath.Join(source, "dir"), Action: EventDirChanged},
		{Path: filepath.Join(source, "dir", "sub", "b.txt"), Action: EventModified},
	}, source, dest)

	got := map[string]EventType{}
	for _, e := range events {
		rel, _ := filepath.Rel(source, e.Path)
		got[rel] = e.Action
	}
	want := map[string]EventType{
		"dir/a.txt":     EventCreated,
		"dir/sub/b.txt": EventModified, // the explicit event wins
		"dir/gone.txt":  EventDeleted,
	}
	if len(got) != len(want) {
		t.Errorf("expandDirChanges() = %v, want %v", got, want)
	}
	for path, action := range want {
		if got[path] != action {
			t.Errorf("%s: got %v, want %v", path, got[path], action)
		}
	}
}

func TestProcessEvents_DirectoryMovedInAndOut(t *testing.T) {
	setGlobals(t, false)
	source := resolvedTempDir(t)
	dest := resolvedTempDir(t)
	SetSourceRoot(source)
	moved := filepath.Join(source, "moved")
	writeFile(t, filepath.Join(moved, "sub", "m.txt"), "m")

	dirChanged := []PendingFileChanges{{Path: moved, Action: EventDirChanged}}
	captureStdout(t, func() {
		if err := processEvents(dirChanged, source, dest, false); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.Stat(filepath.Join(dest, "moved", "sub", "m.txt")); err != nil {
		t.Errorf("file of a moved-in directory not copied: %v", err)
	}

	if err := os.Rename(moved, filepath.Join(resolvedTempDir(t), "moved")); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := processEvents(dirChanged, source, dest, false); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.Stat(filepath.Join(dest, "moved", "sub", "m.txt")); !os.IsNotExist(err) {
		t.Errorf("file of a moved-out directory not removed: %v", err)
	}
}

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

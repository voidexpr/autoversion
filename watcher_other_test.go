//go:build !darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitEvent waits for an event of eventType for path, skipping others
func waitEvent(t *testing.T, w *Watcher, path string, eventType EventType) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e := <-w.Events():
			if e.Path == path && e.Type == eventType {
				return
			}
		case err := <-w.Errors():
			t.Fatalf("watch error: %v", err)
		case <-timeout:
			t.Fatalf("no %s event for %s", eventType, path)
		}
	}
}

// drainEvents waits for d and fails on an event for a path below dir
func drainEvents(t *testing.T, w *Watcher, d time.Duration, dir string) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case e := <-w.Events():
			if dir != "" && e.Path != dir && hasPathPrefix(e.Path, dir) {
				t.Fatalf("unexpected %s event for %s", e.Type, e.Path)
			}
		case <-timeout:
			return
		}
	}
}

func TestWatcher_Directories(t *testing.T) {
	root := t.TempDir()
	w, err := NewWatcher(root, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Created with a file before the watcher can add a watch for it
	dir := filepath.Join(root, "dir")
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	waitEvent(t, w, dir, EventDirChanged)

	// Deleted, then recreated: watched again
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, w, dir, EventDirChanged)
	drainEvents(t, w, 200*time.Millisecond, "")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, w, dir, EventDirChanged)
	later := filepath.Join(dir, "later.txt")
	writeFile(t, later, "later")
	waitEvent(t, w, later, EventCreated)

	// Moved out of the source: no more events for its files
	outside := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(dir, outside); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, w, dir, EventDirChanged)
	writeFile(t, filepath.Join(outside, "after.txt"), "after")
	drainEvents(t, w, 500*time.Millisecond, dir)
}

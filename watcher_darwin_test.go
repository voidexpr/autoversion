//go:build darwin

package main

import (
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsevents"
)

func TestMapFSEventFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags fsevents.EventFlags
		want  EventType
	}{
		// Raw flags from plans/fix_deletion_ignored.md
		{"create", 0x19100, EventCreated},
		{"after autoversion's clone copy", 0x419100, EventCreated},
		{"rm of a cloned file", 0x419300, EventCheckExists},

		{"rm", fsevents.ItemRemoved | fsevents.ItemIsFile, EventCheckExists},
		{"rename", fsevents.ItemRenamed | fsevents.ItemIsFile, EventCheckExists},
		{"modify", fsevents.ItemModified | fsevents.ItemIsFile, EventModified},
		{"touch", fsevents.ItemInodeMetaMod | fsevents.ItemIsFile, EventModified},
		{"xattr only", fsevents.ItemXattrMod | fsevents.ItemIsFile, EventCanIgnore},
		{"clone only", itemCloned | fsevents.ItemIsFile, EventCanIgnore},
		{"events dropped", fsevents.KernelDropped | fsevents.ItemRemoved, EventMustResyncAll},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, details := mapFSEventFlagsWithDetails(tt.flags); got != tt.want {
				t.Errorf("mapFSEventFlagsWithDetails(0x%x) = %s, want %s (%s)", tt.flags, got, tt.want, details)
			}
		})
	}
}

func TestResolveFileEvent(t *testing.T) {
	w := &Watcher{}
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	writeFile(t, file, "a")

	tests := []struct {
		name   string
		path   string
		in     EventType
		want   EventType
		wantOK bool
	}{
		{"existing file", file, EventCheckExists, EventCreated, true},
		{"missing file", filepath.Join(dir, "gone.txt"), EventCheckExists, EventDeleted, true},
		{"modified file", file, EventModified, EventModified, true},
		{"directory", dir, EventCheckExists, EventCheckExists, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := w.resolveFileEvent(tt.path, tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("resolveFileEvent(%s) = %s, %v, want %s, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

package main

import (
	"fmt"
	"os"
)

// EventType represents the type of file system event
type EventType int

const (
	EventCreated EventType = iota
	EventModified
	EventDeleted
	EventRenamed // Special marker for rename events, resolved to Created/Deleted based on file existence
	EventMustResyncAll
	EventCanIgnore
)

// String returns a human-readable string representation of the EventType
func (e EventType) String() string {
	switch e {
	case EventCreated:
		return "EventCreated"
	case EventModified:
		return "EventModified"
	case EventDeleted:
		return "EventDeleted"
	case EventRenamed:
		return "EventRenamed"
	case EventMustResyncAll:
		return "EventMustResyncAll"
	case EventCanIgnore:
		return "EventCanIgnore"
	default:
		return fmt.Sprintf("EventType(%d)", e)
	}
}

// FileEvent represents a file system event
type FileEvent struct {
	Path    string
	Type    EventType
	Details string // Human-readable description of flags that led to this event type
}

// Watcher wraps file system watchers and provides filtered file watching
type Watcher struct {
	watcherPlatform // platform-specific fields (defined in watcher_darwin.go / watcher_other.go)

	events     chan FileEvent
	errors     chan error
	done       chan struct{}
	maxSize    int64
	sourcePath string
	debug      bool
}

// printDebug prints a debug message if debug mode is enabled
func (w *Watcher) printDebug(format string, args ...interface{}) {
	if w.debug {
		fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

// Events returns the events channel
func (w *Watcher) Events() <-chan FileEvent {
	return w.events
}

// Errors returns the errors channel
func (w *Watcher) Errors() <-chan error {
	return w.errors
}


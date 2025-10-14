package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fsnotify/fsevents"
	"github.com/fsnotify/fsnotify"
)

// EventType represents the type of file system event
type EventType int

const (
	EventCreated EventType = iota
	EventModified
	EventDeleted
)

// FileEvent represents a file system event
type FileEvent struct {
	Path string
	Type EventType
}

// Watcher wraps file system watchers and provides filtered file watching
type Watcher struct {
	// Backend selection
	useFSEvents bool

	// fsnotify backend (non-macOS)
	fsnotifyWatcher *fsnotify.Watcher
	watched         map[string]bool // Track directories already being watched

	// fsevents backend (macOS)
	fseventsStream *fsevents.EventStream

	// Common fields
	events     chan FileEvent
	errors     chan error
	done       chan struct{}
	maxSize    int64
	sourcePath string
}

// NewWatcher creates a new file system watcher
func NewWatcher(sourcePath string, maxSize int64) (*Watcher, error) {
	w := &Watcher{
		useFSEvents: runtime.GOOS == "darwin",
		events:      make(chan FileEvent, 100),
		errors:      make(chan error, 10),
		done:        make(chan struct{}),
		maxSize:     maxSize,
		sourcePath:  sourcePath,
	}

	if w.useFSEvents {
		// Use fsevents on macOS
		return w.initFSEvents()
	}

	// Use fsnotify on other platforms
	return w.initFSNotify()
}

// initFSEvents initializes the macOS fsevents backend
func (w *Watcher) initFSEvents() (*Watcher, error) {
	// Create event stream for the source path
	stream := &fsevents.EventStream{
		Paths:   []string{w.sourcePath},
		Flags:   fsevents.FileEvents,
		Latency: 100 * time.Millisecond,
	}

	w.fseventsStream = stream

	// Start the event stream
	if err := stream.Start(); err != nil {
		return nil, fmt.Errorf("failed to start fsevents stream: %v", err)
	}

	// Start goroutine to convert fsevents to FileEvent
	go w.processFSEvents()

	return w, nil
}

// initFSNotify initializes the fsnotify backend for non-macOS platforms
func (w *Watcher) initFSNotify() (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %v", err)
	}

	w.fsnotifyWatcher = fw
	w.watched = make(map[string]bool)

	// Add all directories recursively
	if err := w.addRecursive(w.sourcePath); err != nil {
		fw.Close()
		return nil, err
	}

	// Start goroutine to convert fsnotify events to FileEvent
	go w.processFSNotifyEvents()

	return w, nil
}

// processFSEvents processes events from fsevents and sends them as FileEvent
func (w *Watcher) processFSEvents() {
	for {
		select {
		case <-w.done:
			return
		case events := <-w.fseventsStream.Events:
			for _, event := range events {
				// Skip directories - only process files
				info, err := os.Lstat(event.Path)
				if err != nil && !os.IsNotExist(err) {
					continue
				}

				// Determine event type
				var eventType EventType
				if os.IsNotExist(err) || (event.Flags&fsevents.ItemRemoved != 0) || (event.Flags&fsevents.ItemRenamed != 0) {
					eventType = EventDeleted
				} else if info != nil && info.IsDir() {
					// Skip directories
					continue
				} else if event.Flags&fsevents.ItemCreated != 0 {
					eventType = EventCreated
				} else {
					eventType = EventModified
				}

				// Send the event
				select {
				case w.events <- FileEvent{Path: event.Path, Type: eventType}:
				case <-w.done:
					return
				}
			}
		}
	}
}

// processFSNotifyEvents processes events from fsnotify and sends them as FileEvent
func (w *Watcher) processFSNotifyEvents() {
	for {
		select {
		case <-w.done:
			return
		case event := <-w.fsnotifyWatcher.Events:
			// Check if it's a directory and handle accordingly
			info, err := os.Lstat(event.Name)
			isDelete := (event.Op&fsnotify.Remove != 0) || (event.Op&fsnotify.Rename != 0)

			if err == nil && info.IsDir() {
				// If a new directory was created, add it and all subdirectories to watch list
				if event.Op&fsnotify.Create == fsnotify.Create {
					w.addRecursive(event.Name)
				}
				// Don't send directory events
				continue
			}

			// Skip symlinks to directories
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				if targetInfo, err := os.Stat(event.Name); err == nil && targetInfo.IsDir() {
					continue
				}
			}

			// Map to EventType
			var eventType EventType
			if event.Op&fsnotify.Create == fsnotify.Create {
				eventType = EventCreated
			} else if isDelete {
				eventType = EventDeleted
			} else {
				eventType = EventModified
			}

			// Send the event
			select {
			case w.events <- FileEvent{Path: event.Name, Type: eventType}:
			case <-w.done:
				return
			}

		case err := <-w.fsnotifyWatcher.Errors:
			select {
			case w.errors <- err:
			case <-w.done:
				return
			}
		}
	}
}

// addRecursive adds a directory and all subdirectories to the watcher (fsnotify only)
func (w *Watcher) addRecursive(path string) error {
	return filepath.Walk(path, func(walkPath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		if info.IsDir() {
			// Skip .git directories
			if info.Name() == ".git" {
				return filepath.SkipDir
			}

			// Skip if already watching this directory
			if w.watched[walkPath] {
				return nil
			}

			// Add directory to watcher
			if err := w.fsnotifyWatcher.Add(walkPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to watch %s: %v\n", walkPath, err)
			} else {
				w.watched[walkPath] = true
			}
		}

		return nil
	})
}

// Events returns the events channel
func (w *Watcher) Events() <-chan FileEvent {
	return w.events
}

// Errors returns the errors channel
func (w *Watcher) Errors() <-chan error {
	return w.errors
}

// ShouldProcess is kept for compatibility but is now mostly a no-op
// The backend handles filtering internally
func (w *Watcher) ShouldProcess(event FileEvent) bool {
	// All filtering is done in the backend goroutines now
	return true
}

// Close closes the watcher
func (w *Watcher) Close() error {
	close(w.done)

	if w.useFSEvents {
		w.fseventsStream.Stop()
	} else if w.fsnotifyWatcher != nil {
		return w.fsnotifyWatcher.Close()
	}

	return nil
}

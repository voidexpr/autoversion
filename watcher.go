package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// Watcher wraps fsnotify and provides filtered file watching
type Watcher struct {
	watcher    *fsnotify.Watcher
	maxSize    int64
	sourcePath string
}

// NewWatcher creates a new file system watcher
func NewWatcher(sourcePath string, maxSize int64) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %v", err)
	}

	w := &Watcher{
		watcher:    fw,
		maxSize:    maxSize,
		sourcePath: sourcePath,
	}

	// Add all directories recursively
	if err := w.addRecursive(sourcePath); err != nil {
		fw.Close()
		return nil, err
	}

	return w, nil
}

// addRecursive adds a directory and all subdirectories to the watcher
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

			// Add directory to watcher
			if err := w.watcher.Add(walkPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to watch %s: %v\n", walkPath, err)
			}
		}

		return nil
	})
}

// Events returns the events channel
func (w *Watcher) Events() <-chan fsnotify.Event {
	return w.watcher.Events
}

// Errors returns the errors channel
func (w *Watcher) Errors() <-chan error {
	return w.watcher.Errors
}

// ShouldProcess determines if an event should be processed based on filters
func (w *Watcher) ShouldProcess(event fsnotify.Event) bool {
	// Check if it's a directory
	info, err := os.Lstat(event.Name)
	if err != nil {
		if MapEventType(event.Op) == EventDeleted {
			return true
		}
		return false
	}

	if info.IsDir() {
		// If a new directory was created, add it to watch list
		if event.Op&fsnotify.Create == fsnotify.Create {
			w.watcher.Add(event.Name)
		}
		return false
	}

	// Skip symlinks to directories
	if info.Mode()&os.ModeSymlink != 0 {
		if targetInfo, err := os.Stat(event.Name); err == nil && targetInfo.IsDir() {
			return false
		}
	}
	return true
}

// MapEventType converts fsnotify operations to our EventType
func MapEventType(op fsnotify.Op) EventType {
	if op&fsnotify.Create == fsnotify.Create {
		return EventCreated
	}
	if op&fsnotify.Remove == fsnotify.Remove || op&fsnotify.Rename == fsnotify.Rename {
		return EventDeleted
	}
	return EventModified // Write, Chmod, etc.
}

// Close closes the watcher
func (w *Watcher) Close() error {
	return w.watcher.Close()
}

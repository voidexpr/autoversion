//go:build !darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

type watcherPlatform struct {
	fsnotifyWatcher *fsnotify.Watcher
	watched         map[string]bool
}

// NewWatcher creates a new file system watcher (non-macOS: uses fsnotify)
func NewWatcher(sourcePath string, debug bool) (*Watcher, error) {
	w := &Watcher{
		events:     make(chan FileEvent, 100),
		errors:     make(chan error, 10),
		done:       make(chan struct{}),
		sourcePath: sourcePath,
		debug:      debug,
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %v", err)
	}

	w.fsnotifyWatcher = fw
	w.watched = make(map[string]bool)

	if err := w.addRecursive(sourcePath); err != nil {
		fw.Close()
		return nil, err
	}

	go w.processFSNotifyEvents()
	return w, nil
}

// Close closes the watcher
func (w *Watcher) Close() error {
	close(w.done)
	if w.fsnotifyWatcher != nil {
		return w.fsnotifyWatcher.Close()
	}
	return nil
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
					if err := w.addRecursive(event.Name); err != nil {
						select {
						case w.errors <- err:
						case <-w.done:
							return
						}
					}
					// Files created or moved in before the new watches were added
					// have no events: the main loop rescans the directory
					if !w.sendEvent(FileEvent{Path: event.Name, Type: EventDirChanged, Details: "fsnotify: directory created"}) {
						return
					}
				}
				// Don't send other directory events
				continue
			}

			// A watched directory removed or moved away: drop its watches, so it
			// is watched again if recreated, and let the main loop rescan it
			if isDelete && w.watched[event.Name] {
				if event.Op&fsnotify.Rename != 0 {
					// Its subdirectories moved too, without events of their own
					w.forgetDir(event.Name)
				} else {
					// fsnotify drops the watch of a deleted directory
					delete(w.watched, event.Name)
				}
				if !w.sendEvent(FileEvent{Path: event.Name, Type: EventDirChanged, Details: "fsnotify: directory removed or moved"}) {
					return
				}
				continue
			}

			// Skip symlinks to directories
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				if targetInfo, err := os.Stat(event.Name); err == nil && targetInfo.IsDir() {
					continue
				}
			}

			// Map to EventType and create details
			var eventType EventType
			var details string
			if event.Op&fsnotify.Create == fsnotify.Create {
				eventType = EventCreated
				details = "fsnotify: Create"
			} else if isDelete {
				eventType = EventDeleted
				details = "fsnotify: Remove/Rename"
			} else {
				eventType = EventModified
				details = fmt.Sprintf("fsnotify: %v", event.Op)
			}

			if !w.sendEvent(FileEvent{Path: event.Name, Type: eventType, Details: details}) {
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

// sendEvent passes an event to the main loop, returns false if the watcher is closed
func (w *Watcher) sendEvent(event FileEvent) bool {
	select {
	case w.events <- event:
		return true
	case <-w.done:
		return false
	}
}

// forgetDir drops the watches of dir and its subdirectories
func (w *Watcher) forgetDir(dir string) {
	for path := range w.watched {
		if hasPathPrefix(path, dir) {
			w.fsnotifyWatcher.Remove(path) // fails if fsnotify already dropped it
			delete(w.watched, path)
		}
	}
}

// addRecursive adds a directory and all subdirectories to the watcher (fsnotify only).
// Once the inotify watch limit is reached no more watches are added; the walk
// goes on only to count the directories left unwatched, and an errWatchLimit
// error reports both counts.
func (w *Watcher) addRecursive(path string) error {
	unwatched := 0
	err := filepath.Walk(path, func(walkPath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		if info.IsDir() {
			// Folders the rules leave out, .git included, with nothing kept below
			if skipDir(walkPath) {
				return filepath.SkipDir
			}

			// Skip if already watching this directory
			if w.watched[walkPath] {
				return nil
			}

			if unwatched > 0 {
				unwatched++
				return nil
			}

			// Add directory to watcher
			if err := w.fsnotifyWatcher.Add(walkPath); err != nil {
				if errors.Is(err, syscall.ENOSPC) {
					unwatched = 1
					return nil
				}
				fmt.Fprintf(os.Stderr, "Warning: failed to watch %s: %v\n", walkPath, err)
			} else {
				w.watched[walkPath] = true
			}
		}

		return nil
	})
	if err != nil {
		return err
	}
	if unwatched > 0 {
		return fmt.Errorf("%w (fs.inotify.max_user_watches=%s, shared by all processes of this user): %d directories watched, %d more under %s need a watch. Raise the limit or exclude folders with filter set (see README, Linux)",
			errWatchLimit, inotifyWatchLimit(), len(w.watched), unwatched, path)
	}
	return nil
}

// inotifyWatchLimit returns the fs.inotify.max_user_watches value, or "?" if it can't be read
func inotifyWatchLimit() string {
	data, err := os.ReadFile("/proc/sys/fs/inotify/max_user_watches")
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(data))
}

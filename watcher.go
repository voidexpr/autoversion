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
	debug      bool
}

// printDebug prints a debug message if debug mode is enabled
func (w *Watcher) printDebug(format string, args ...interface{}) {
	if w.debug {
		fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

// NewWatcher creates a new file system watcher
func NewWatcher(sourcePath string, maxSize int64, debug bool) (*Watcher, error) {
	w := &Watcher{
		useFSEvents: runtime.GOOS == "darwin",
		events:      make(chan FileEvent, 100),
		errors:      make(chan error, 10),
		done:        make(chan struct{}),
		maxSize:     maxSize,
		sourcePath:  sourcePath,
		debug:       debug,
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

// decodeFSEventFlags decodes fsevents flags into human-readable names
func decodeFSEventFlags(flags fsevents.EventFlags) []string {
	var flagNames []string
	if flags&fsevents.MustScanSubDirs != 0 {
		flagNames = append(flagNames, "MustScanSubDirs")
	}
	if flags&fsevents.UserDropped != 0 {
		flagNames = append(flagNames, "UserDropped")
	}
	if flags&fsevents.KernelDropped != 0 {
		flagNames = append(flagNames, "KernelDropped")
	}
	if flags&fsevents.EventIDsWrapped != 0 {
		flagNames = append(flagNames, "EventIDsWrapped")
	}
	if flags&fsevents.HistoryDone != 0 {
		flagNames = append(flagNames, "HistoryDone")
	}
	if flags&fsevents.RootChanged != 0 {
		flagNames = append(flagNames, "RootChanged")
	}
	if flags&fsevents.Mount != 0 {
		flagNames = append(flagNames, "Mount")
	}
	if flags&fsevents.Unmount != 0 {
		flagNames = append(flagNames, "Unmount")
	}
	if flags&fsevents.ItemCreated != 0 {
		flagNames = append(flagNames, "ItemCreated")
	}
	if flags&fsevents.ItemRemoved != 0 {
		flagNames = append(flagNames, "ItemRemoved")
	}
	if flags&fsevents.ItemInodeMetaMod != 0 {
		flagNames = append(flagNames, "ItemInodeMetaMod")
	}
	if flags&fsevents.ItemRenamed != 0 {
		flagNames = append(flagNames, "ItemRenamed")
	}
	if flags&fsevents.ItemModified != 0 {
		flagNames = append(flagNames, "ItemModified")
	}
	if flags&fsevents.ItemFinderInfoMod != 0 {
		flagNames = append(flagNames, "ItemFinderInfoMod")
	}
	if flags&fsevents.ItemChangeOwner != 0 {
		flagNames = append(flagNames, "ItemChangeOwner")
	}
	if flags&fsevents.ItemXattrMod != 0 {
		flagNames = append(flagNames, "ItemXattrMod")
	}
	if flags&fsevents.ItemIsFile != 0 {
		flagNames = append(flagNames, "ItemIsFile")
	}
	if flags&fsevents.ItemIsDir != 0 {
		flagNames = append(flagNames, "ItemIsDir")
	}
	if flags&fsevents.ItemIsSymlink != 0 {
		flagNames = append(flagNames, "ItemIsSymlink")
	}
	if flags&0x400000 != 0 { // ItemCloned
		flagNames = append(flagNames, "ItemCloned")
	}
	return flagNames
}

// mapFSEventFlagsWithDetails maps fsevents flags to EventType and returns details
// Returns EventCanIgnore if the event should be dropped, along with the decoded flags string
func mapFSEventFlagsWithDetails(flags fsevents.EventFlags) (EventType, string) {
	flagNames := decodeFSEventFlags(flags)
	details := fmt.Sprintf("%v", flagNames)

	// Check for special system flags that require full resync
	const specialSystemFlags = fsevents.MustScanSubDirs | fsevents.UserDropped |
		fsevents.KernelDropped | fsevents.RootChanged | fsevents.Mount | fsevents.Unmount

	if flags&specialSystemFlags != 0 {
		return EventMustResyncAll, details
	}

	// Check for ItemCloned flag - always ignore clone events
	// Clone operations are COW (copy-on-write) and don't represent actual file changes
	const itemCloned = 0x400000
	if flags&itemCloned != 0 {
		return EventCanIgnore, details
	}

	// Define action flags that indicate actual file changes we care about
	// ItemInodeMetaMod is included because it tracks timestamp changes (e.g., touch command)
	const actionFlags = fsevents.ItemCreated | fsevents.ItemModified | fsevents.ItemRemoved |
		fsevents.ItemRenamed | fsevents.ItemInodeMetaMod

	// Check if event has ONLY type/metadata flags and NO action flags
	// Type flags: ItemIsFile, ItemIsDir, ItemIsSymlink, ItemIsHardlink, etc.
	// Ignorable metadata flags: ItemFinderInfoMod, ItemChangeOwner, ItemXattrMod
	if flags&actionFlags == 0 {
		return EventCanIgnore, details
	}

	// Map to regular event types based on action flags
	if flags&fsevents.ItemRemoved != 0 {
		return EventDeleted, details
	}

	// ItemRenamed can be either source (deleted) or target (created) of rename
	// Return EventRenamed and let the caller check file existence to determine which
	if flags&fsevents.ItemRenamed != 0 {
		return EventRenamed, details
	}

	if flags&fsevents.ItemCreated != 0 {
		return EventCreated, details
	}

	if flags&fsevents.ItemModified != 0 {
		return EventModified, details
	}

	if flags&fsevents.ItemInodeMetaMod != 0 {
		// Inode metadata changed (e.g., timestamp via touch command)
		return EventModified, details
	}

	// Should not reach here if actionFlags check above is correct, but default to modified
	return EventModified, details
}

// processFSEvents processes events from fsevents and sends them as FileEvent
func (w *Watcher) processFSEvents() {
	for {
		select {
		case <-w.done:
			return
		case events := <-w.fseventsStream.Events:
			for _, event := range events {
				// Map flags to event type and get details
				eventType, details := mapFSEventFlagsWithDetails(event.Flags)

				w.printDebug("fsevents: raw event path=%s, flags=0x%x, ID=%d, mapped type: %s, flags decoded: %s", event.Path, event.Flags, event.ID, eventType, details)

				if eventType == EventCanIgnore {
					w.printDebug("fsevents: event can be ignored (only type flags)")
					continue
				}

				// For MustResyncAll, send immediately without file checks
				if eventType == EventMustResyncAll {
					w.printDebug("fsevents: EventMustResyncAll detected")
					select {
					case w.events <- FileEvent{Path: event.Path, Type: eventType, Details: details}:
						w.printDebug("fsevents: event sent to channel")
					case <-w.done:
						return
					}
					continue
				}

				// Skip directories - only process files
				info, err := os.Lstat(event.Path)
				if err != nil && !os.IsNotExist(err) {
					w.printDebug("fsevents: lstat error (skipping): %v", err)
					continue
				}

				if err == nil && info.IsDir() {
					w.printDebug("fsevents: skipping (is directory)")
					continue
				}

				// For EventRenamed, determine if it's a deletion or creation based on file existence
				if eventType == EventRenamed {
					if err == nil {
						// File exists, so this is the target of the rename (new name)
						eventType = EventCreated
						w.printDebug("fsevents: EventRenamed with file exists -> EventCreated")
					} else {
						// File doesn't exist, so this is the source of the rename (old name)
						eventType = EventDeleted
						w.printDebug("fsevents: EventRenamed with file missing -> EventDeleted")
					}
				}

				// Send the event
				select {
				case w.events <- FileEvent{Path: event.Path, Type: eventType, Details: details}:
					w.printDebug("fsevents: event sent to channel")
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

			// Send the event
			select {
			case w.events <- FileEvent{Path: event.Name, Type: eventType, Details: details}:
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

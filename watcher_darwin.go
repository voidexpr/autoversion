//go:build darwin

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/fsnotify/fsevents"
)

type watcherPlatform struct {
	fseventsStream *fsevents.EventStream
}

// NewWatcher creates a new file system watcher (macOS: uses fsevents)
func NewWatcher(sourcePath string, maxSize int64, debug bool) (*Watcher, error) {
	w := &Watcher{
		events:     make(chan FileEvent, 100),
		errors:     make(chan error, 10),
		done:       make(chan struct{}),
		maxSize:    maxSize,
		sourcePath: sourcePath,
		debug:      debug,
	}

	stream := &fsevents.EventStream{
		Paths:   []string{sourcePath},
		Flags:   fsevents.FileEvents,
		Latency: 100 * time.Millisecond,
	}
	w.fseventsStream = stream

	if err := stream.Start(); err != nil {
		return nil, fmt.Errorf("failed to start fsevents stream: %v", err)
	}

	go w.processFSEvents()
	return w, nil
}

// Close closes the watcher
func (w *Watcher) Close() error {
	close(w.done)
	w.fseventsStream.Stop()
	return nil
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

				if IsExcludedPath(event.Path) {
					w.printDebug("fsevents: skipping excluded path: %s", event.Path)
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

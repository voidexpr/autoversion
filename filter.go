package main

import (
	"os"

	"github.com/nicolas/autoversion/filterrules"
)

// activeRules decide what sync, watch and the watchers track, see SetFilter
var activeRules *sourceRules

// reportedLargeFiles holds the large files already logged, a file being
// written would otherwise be reported on every event
var reportedLargeFiles = map[string]bool{}

// SetFilter sets the source and the rules that decide what is tracked
func SetFilter(root string, rules *filterrules.Rules) {
	activeRules = &sourceRules{root: root, rules: rules}
}

// rulesSet returns the active rules. Without them nothing would be kept, and
// a sync would remove the destination's files: a missing SetFilter is a bug.
func rulesSet() *sourceRules {
	if activeRules == nil {
		panic("SetFilter not called")
	}
	return activeRules
}

// trackedPath reports whether the rules keep a path of the source, without
// looking at the file. A deleted entry has no type: it is decided as a file.
func trackedPath(path string, isDir bool) bool {
	return rulesSet().tracked(path, isDir)
}

// skipDir reports whether walks and watchers can skip a folder of the source
func skipDir(path string) bool {
	return rulesSet().skipDir(path)
}

// tooLarge reports whether a file is over the size limit, in bytes (0: no
// limit). A file of exactly the limit is kept.
func tooLarge(size, maxSize int64) bool {
	return maxSize > 0 && size > maxSize
}

// tooLargeToTrack is tooLarge, logging each large file once
func tooLargeToTrack(path string, size, maxSize int64) bool {
	if !tooLarge(size, maxSize) {
		return false
	}
	if !reportedLargeFiles[path] {
		reportedLargeFiles[path] = true
		logSkip("[%s] skipping large file: %s (%s > %s)\n",
			nowTS(), path, formatSize(size), formatSize(maxSize))
	}
	return true
}

// ShouldTrack returns true if a file of the source is tracked: the rules keep
// it, it is a regular file, and it is within maxSize bytes (0: no limit).
// isDelete should be true for a deleted file: only the rules apply.
func ShouldTrack(path string, maxSize int64, isDelete bool) (bool, error) {
	// The rules first: they need no file access, and the files they leave out
	// are often temporary ones already gone (e.g. build outputs)
	if !trackedPath(path, false) {
		return false, nil
	}

	if isDelete {
		return true, nil
	}

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Removed before its event was handled, its delete event follows
			if debug {
				logSkip("[%s] skipping vanished file: %s\n", nowTS(), path)
			}
			return false, nil
		}
		return false, err
	}

	// Folders, symlinks and other special files are never tracked
	if !info.Mode().IsRegular() {
		return false, nil
	}

	return !tooLargeToTrack(path, info.Size(), maxSize), nil
}

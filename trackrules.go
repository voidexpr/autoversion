package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nicad/autoversion/filterrules"
	"golang.org/x/sys/unix"
)

const alwaysAppliedText = "symlinks, /" + markerFileName + " and /" + filterFileName

// nestedGitText says why a git destination leaves out a .git
const nestedGitText = "use nested-git, applied by the git destination"

// alwaysExcluded reports whether an entry of the source (rel, see rel) is
// left out whatever the rules: a destination's own files at the source
// root, like the rules exclude /.autoversion and /.autoversion.filter
func alwaysExcluded(rel string) bool {
	return rel == markerFileName || rel == filterFileName
}

// gitExcluded reports whether an entry is a .git or anything under one:
// what the nested-git group leaves out (exclude /**/.git), decided without
// a rules walk. A git destination applies it whatever its rules say: git
// would record a copied .git as a nested repository, not its files.
func gitExcluded(rel string) bool {
	for rel != "" {
		var name string
		name, rel, _ = strings.Cut(rel, "/")
		if name == ".git" {
			return true
		}
	}
	return false
}

// sourceRules decides what autoversion tracks in a source: alwaysExcluded,
// gitExcluded for a git destination, then the destination's rules
type sourceRules struct {
	root      string
	rules     *filterrules.Rules
	nestedGit bool // a git destination: .git folders are left out whatever the rules say

	// markers caches which folders hold a marker entry during a walk
	// (relDir + "/" + name); nil outside a walk, where each lookup stats
	markerMu sync.Mutex
	markers  map[string]bool
}

// newSourceRules returns the rules of a source, with the lookups the rules'
// exclude marker:NAME and exclude xattr:NAME lines need. rules is unchanged.
func newSourceRules(root string, rules *filterrules.Rules, nestedGit bool) *sourceRules {
	s := &sourceRules{root: root, nestedGit: nestedGit}
	s.rules = rules.WithMarkers(s.holdsMarker).WithXattrs(s.hasXattr)
	return s
}

// holdsMarker reports whether the folder relDir of the source directly holds
// an entry named name
func (s *sourceRules) holdsMarker(relDir, name string) bool {
	return s.cached(relDir+"/"+name, func() bool {
		_, err := os.Lstat(filepath.Join(s.root, filepath.FromSlash(relDir), name))
		return err == nil
	})
}

// hasXattr reports whether the entry rel of the source carries the extended
// attribute name (never followed through a symlink)
func (s *sourceRules) hasXattr(rel, name string) bool {
	return s.cached("@"+rel+"/"+name, func() bool {
		_, err := unix.Lgetxattr(filepath.Join(s.root, filepath.FromSlash(rel)), name, nil)
		return err == nil
	})
}

// cached returns lookup's answer for key, from the walk cache when on
func (s *sourceRules) cached(key string, lookup func() bool) bool {
	s.markerMu.Lock()
	defer s.markerMu.Unlock()
	if held, ok := s.markers[key]; ok {
		return held
	}
	held := lookup()
	if s.markers != nil {
		s.markers[key] = held
	}
	return held
}

// cacheMarkers turns the marker and attribute cache on for a walk; the
// function returned turns it off. Outside a walk a marker entry or an
// attribute may appear or go at any time.
func (s *sourceRules) cacheMarkers() func() {
	s.markerMu.Lock()
	defer s.markerMu.Unlock()
	if s.markers != nil || len(s.rules.Markers())+len(s.rules.Xattrs()) == 0 {
		return func() {}
	}
	s.markers = map[string]bool{}
	return func() {
		s.markerMu.Lock()
		s.markers = nil
		s.markerMu.Unlock()
	}
}

// leftOut reports whether an entry is left out before the rules
func (s *sourceRules) leftOut(rel string) bool {
	return alwaysExcluded(rel) || s.nestedGit && gitExcluded(rel)
}

// rel returns path relative to the root with "/" separators ("" for the
// root), and false for a path outside the root
func (s *sourceRules) rel(path string) (string, bool) {
	rel, err := filepath.Rel(s.root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// tracked reports whether the rules keep an entry of the source
func (s *sourceRules) tracked(path string, isDir bool) bool {
	rel, ok := s.rel(path)
	return ok && !s.leftOut(rel) && s.rules.Decide(rel, isDir).Kept
}

// limit returns the size limit of a file of the source, in bytes (0: none),
// and the scoped max-file-size line that sets it (nil: the default)
func (s *sourceRules) limit(path string) (int64, *filterrules.Rule) {
	rel, ok := s.rel(path)
	if ok {
		if rule := s.rules.Limit(rel); rule != nil {
			_, bytes := rule.Size()
			return bytes, rule
		}
	}
	_, bytes := s.rules.MaxFileSize()
	return bytes, nil
}

// skipDir reports whether a walker can skip a folder of the source: it is
// left out, and nothing below it can be kept
func (s *sourceRules) skipDir(path string) bool {
	rel, ok := s.rel(path)
	return !ok || s.leftOut(rel) || s.rules.SkipDir(rel)
}

// walk calls fn for each file the rules keep under dir, a folder of the
// source, skipping the folders that hold none. Symlinks and other files that
// are not regular files are never tracked; the size limit is fn's to check.
// An error from fn stops the walk. walk returns the number of folders
// visited: the watches a Linux watcher needs.
func (s *sourceRules) walk(dir string, fn func(path string, info fs.FileInfo) error) (int, error) {
	defer s.cacheMarkers()()
	dirs := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if s.skipDir(path) {
				return filepath.SkipDir
			}
			dirs++
			return nil
		}
		if !d.Type().IsRegular() || !s.tracked(path, false) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		return fn(path, info)
	})
	return dirs, err
}

// walkDestination calls fn with the path, relative to the destination, of
// each file in its folder dir ("" for the whole destination), leaving out
// the destination's own .git, marker and rules file. A .git below the root
// is a copy from the source (a mirror's), walked like any folder.
func walkDestination(destPath, dir string, fn func(rel string)) {
	ownGit := filepath.Join(destPath, ".git")
	filepath.Walk(filepath.Join(destPath, dir), func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path == ownGit {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(destPath, path)
		if rel != markerFileName && rel != filterFileName {
			fn(rel)
		}
		return nil
	})
}

package main

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/nicolas/autoversion/filterrules"
)

const alwaysAppliedText = "/**/.git, symlinks, /" + markerFileName + " and /" + filterFileName

// alwaysExcluded reports whether an entry of the source (rel, see rel) is
// left out whatever the rules: a .git, or anything under one (a nested
// repository's .git would break a git destination), and a destination's
// own files at the source root. Like the rules exclude /**/.git,
// /.autoversion and /.autoversion.filter, applied first.
func alwaysExcluded(rel string) bool {
	if rel == markerFileName || rel == filterFileName {
		return true
	}
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
// then the destination's rules
type sourceRules struct {
	root  string
	rules *filterrules.Rules
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
	return ok && !alwaysExcluded(rel) && s.rules.Decide(rel, isDir).Kept
}

// skipDir reports whether a walker can skip a folder of the source: it is
// left out, and nothing below it can be kept
func (s *sourceRules) skipDir(path string) bool {
	rel, ok := s.rel(path)
	return !ok || alwaysExcluded(rel) || s.rules.SkipDir(rel)
}

// walk calls fn for each file the rules keep under dir, a folder of the
// source, skipping the folders that hold none. Symlinks and other files that
// are not regular files are never tracked; the size limit is fn's to check.
// An error from fn stops the walk. walk returns the number of folders
// visited: the watches a Linux watcher needs.
func (s *sourceRules) walk(dir string, fn func(path string, info fs.FileInfo) error) (int, error) {
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
// .git folders and the destination's marker and rules file
func walkDestination(destPath, dir string, fn func(rel string)) {
	filepath.Walk(filepath.Join(destPath, dir), func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".git" {
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

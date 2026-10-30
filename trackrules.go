package main

import (
	"io/fs"
	"path/filepath"

	"github.com/nicolas/autoversion/filterrules"
)

// alwaysApplied are the rules no filter changes: a nested repository's .git
// is never copied (it would break a git destination), and a destination's
// own files at the source root are never tracked
var alwaysApplied = func() *filterrules.Rules {
	var f filterrules.File
	for _, rule := range []string{"/**/.git", "/" + markerFileName, "/" + filterFileName} {
		if err := f.Add(filterrules.Exclude, rule); err != nil {
			panic(err)
		}
	}
	rules, err := filterrules.New(&f)
	if err != nil {
		panic(err)
	}
	return rules
}()

const alwaysAppliedText = "/**/.git, symlinks, /" + markerFileName + " and /" + filterFileName

// sourceRules decides what autoversion tracks in a source: the always
// applied rules, then the destination's rules
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
	return ok && alwaysApplied.Decide(rel, isDir).Kept && s.rules.Decide(rel, isDir).Kept
}

// skipDir reports whether a walker can skip a folder of the source: it is
// left out, and nothing below it can be kept
func (s *sourceRules) skipDir(path string) bool {
	rel, ok := s.rel(path)
	return !ok || alwaysApplied.SkipDir(rel) || s.rules.SkipDir(rel)
}

// walk calls fn for each tracked file of the source, skipping the folders
// that hold none. Symlinks and other files that are not regular files are
// never tracked. It returns the number of folders visited: the watches a
// Linux watcher needs.
func (s *sourceRules) walk(maxFileSizeMB int64, fn func(path string, info fs.FileInfo)) int {
	dirs := 0
	filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
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
		if err != nil || tooLarge(info.Size(), maxFileSizeMB) {
			return nil
		}
		fn(path, info)
		return nil
	})
	return dirs
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

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicad/autoversion/filterrules"
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

const gitDirName = ".git"

// gitExcluded reports whether an entry is a .git or anything under one:
// what the nested-git group leaves out (exclude /**/.git), decided without
// a rules walk. A git destination applies it whatever its rules say: git
// would record a copied .git as a nested repository, not its files.
func gitExcluded(rel string) bool {
	for rel != "" {
		var name string
		name, rel, _ = strings.Cut(rel, "/")
		if name == gitDirName {
			return true
		}
	}
	return false
}

// sourceRules decides what autoversion tracks in a source: alwaysExcluded,
// gitExcluded for a git destination, then the destination's rules, bound
// to the source for their marker and attribute lookups
type sourceRules struct {
	root      string
	rules     *filterrules.Rules
	tree      *filterrules.Tree
	nestedGit bool // a git destination: .git folders are left out whatever the rules say
}

// newSourceRules returns the rules of a source. rules is unchanged.
func newSourceRules(root string, rules *filterrules.Rules, nestedGit bool) *sourceRules {
	return &sourceRules{root: root, rules: rules, tree: rules.Bind(root), nestedGit: nestedGit}
}

// leftOut reports whether an entry is left out before the rules
func (s *sourceRules) leftOut(rel string) bool {
	return alwaysExcluded(rel) || s.nestedGit && gitExcluded(rel)
}

// leftOutName is leftOut for the entry name of a folder a walk entered: the
// folder itself isn't left out, so only the name counts (inRoot: the folder
// is the root)
func (s *sourceRules) leftOutName(inRoot bool, name string) bool {
	return inRoot && alwaysExcluded(name) || s.nestedGit && name == gitDirName
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

// tracked reports whether the rules keep an entry of the source. For a
// one-off question (an event): a walk decides from its folders.
func (s *sourceRules) tracked(path string, isDir bool) bool {
	rel, ok := s.rel(path)
	return ok && !s.leftOut(rel) && s.tree.Decide(rel, isDir).Kept
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
	return !ok || s.leftOut(rel) || s.tree.SkipDir(rel)
}

// walk calls fn for each file the rules keep under dir, a folder of the
// source, skipping the folders that hold none. Symlinks and other files that
// are not regular files are never tracked; the size limit is fn's to check.
// An error from fn stops the walk. walk returns the number of folders
// visited: the watches a Linux watcher needs.
//
// Each folder is read once; its listing gives the rules its marker entries,
// so a marker rule costs no lookup. An entry's type comes from the listing.
func (s *sourceRules) walk(dir string, fn func(path string, info fs.FileInfo) error) (int, error) {
	rel, ok := s.rel(dir)
	if !ok {
		return 0, nil
	}
	dirs := 0
	// visit handles the folder node, whose listing entries was read to
	// decide it
	var visit func(path string, node *filterrules.Dir, entries []fs.DirEntry) error
	visit = func(path string, node *filterrules.Dir, entries []fs.DirEntry) error {
		dirs++
		for _, e := range entries {
			name := e.Name()
			if s.leftOutName(node.Rel() == "", name) {
				continue
			}
			if e.IsDir() {
				// The listing is read once, when the rules need it to decide
				// the folder or when the walk enters it; a folder a rule
				// leaves out is never read
				childPath := filepath.Join(path, name)
				var children []fs.DirEntry
				read := func() []fs.DirEntry {
					if children == nil {
						children, _ = os.ReadDir(childPath)
					}
					return children
				}
				child := node.Child(name, func() []string { return names(read()) })
				if child.Skip() || read() == nil {
					continue
				}
				if err := visit(childPath, child, children); err != nil {
					return err
				}
				continue
			}
			if !e.Type().IsRegular() || !node.File(name).Kept {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if err := fn(filepath.Join(path, name), info); err != nil {
				return err
			}
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil || s.leftOut(rel) {
		return 0, nil
	}
	node := s.tree.Node(rel)
	if node.Skip() {
		return 0, nil
	}
	return dirs, visit(dir, node, entries)
}

// names returns the names of a listing
func names(entries []fs.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
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

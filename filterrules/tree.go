package filterrules

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// Lookups is what a Tree asks the file system for: whether a folder holds
// a marker entry (exclude marker:NAME) and whether an entry carries an
// extended attribute (exclude xattr:NAME). A Tree only asks when its rules
// have such lines, and for a level no path rule decides. DiskLookups reads
// the disk; NoLookups answers no, for rules without such lines, or to leave
// those rules out knowingly. Several goroutines may ask at once.
type Lookups interface {
	HoldsMarker(relDir, name string) bool
	HasXattr(rel, name string) bool
}

// DiskLookups answers from the tree under Root: lstat for a marker entry,
// lgetxattr for an attribute (never followed through a symlink).
type DiskLookups struct {
	Root     string
	NoXattrs bool // answer no for attributes without reading them: xattr rules never match
}

// LookupCount counts the lstat and lgetxattr calls of DiskLookups, for
// benchmarks
var LookupCount atomic.Int64

func (d DiskLookups) HoldsMarker(relDir, name string) bool {
	LookupCount.Add(1)
	_, err := os.Lstat(filepath.Join(d.Root, filepath.FromSlash(relDir), name))
	return err == nil
}

func (d DiskLookups) HasXattr(rel, name string) bool {
	if d.NoXattrs {
		return false
	}
	LookupCount.Add(1)
	_, err := unix.Lgetxattr(filepath.Join(d.Root, filepath.FromSlash(rel)), name, nil)
	return err == nil
}

type noLookups struct{}

func (noLookups) HoldsMarker(string, string) bool { return false }
func (noLookups) HasXattr(string, string) bool    { return false }

// NoLookups is the Lookups that answers no: marker and xattr rules never
// match. For rules without such lines, tests, or to leave them out knowingly.
var NoLookups Lookups = noLookups{}

// Tree decides the entries of one tree with a Rules. A walker builds a Dir
// per folder it enters, from the root down (Root, Dir.Child), and decides
// each entry from its folder (Dir.File, Dir.Child). Decide and SkipDir
// decide a path on its own, building the folders above it as they go, for
// one-off questions. Entry paths are relative to the root, "/" between
// names, "" for the root. A Tree and its Dirs are read only: goroutines
// share them freely.
type Tree struct {
	rules   *Rules
	lookups Lookups
}

// Bind returns the Tree deciding the tree under root on disk (DiskLookups)
func (r *Rules) Bind(root string) *Tree {
	return r.BindWith(DiskLookups{Root: root})
}

// BindWith returns a Tree deciding with the given lookups
func (r *Rules) BindWith(lookups Lookups) *Tree {
	return &Tree{rules: r, lookups: lookups}
}

// Rules returns the rules the Tree decides with
func (t *Tree) Rules() *Rules {
	return t.rules
}

// Dir is a folder of a Tree: its decision, computed once, and what it holds
// for marker rules. Its children are decided from it.
type Dir struct {
	tree     *Tree
	parent   *Dir // nil for the root
	rel      string
	names    []string
	decision Decision

	// For marker rules: list gives the folder's listing (names) when the
	// caller has one, nil otherwise (the lookups answer); held, resolved
	// once from the listing, says per marker rule whether the folder holds
	// its entry
	list func() []string
	once sync.Once
	held []bool
}

// Root returns the root folder, always kept
func (t *Tree) Root() *Dir {
	return &Dir{tree: t, decision: Decision{Kept: true}}
}

// Child returns the subfolder name of d, decided. list returns the
// folder's listing (names); it is called at most once, and only when a
// marker rule has to be checked: no path rule decides the folder, and the
// rules have marker lines. A walker passes the read it does anyway, so the
// listing is read once and a folder a rule leaves out is never listed.
// nil: the caller has no listing, and a marker rule then costs one lstat.
func (d *Dir) Child(name string, list func() []string) *Dir {
	c := &Dir{tree: d.tree, parent: d, rel: d.join(name), list: list}
	c.names = append(append(make([]string, 0, len(d.names)+1), d.names...), name)
	c.decision = c.decideSelf(-1)
	return c
}

// resolveHeld fills held from the listing, once
func (d *Dir) resolveHeld() {
	d.once.Do(func() {
		markers := d.tree.rules.markers
		d.held = make([]bool, len(markers))
		entries := d.list()
		for i, m := range markers {
			for _, name := range entries {
				if name == m.Marker {
					d.held[i] = true
					break
				}
			}
		}
	})
}

// decideSelf decides the folder: its own level, else its parent's
// decision. The marker rule at index except doesn't count (-1: all do).
func (c *Dir) decideSelf(except int) Decision {
	var marked func() *Rule
	if len(c.tree.rules.markers)+len(c.tree.rules.xattrs) > 0 {
		marked = func() *Rule {
			if m := c.markedBy(except); m != nil {
				return m
			}
			return c.tree.attrBy(c.rel)
		}
	}
	if rule := c.tree.rules.decideLevel(c.names, c.parent.rel, true, marked); rule != nil {
		return Decision{Kept: rule.Kind == Include, Level: len(c.names), Rule: rule}
	}
	return c.parent.decision
}

// File decides the non-folder entry name of d. The marker entry of a marked
// folder is decided as if its folder had no such marker: the folder is
// stored bare with it.
func (d *Dir) File(name string) Decision {
	// The path as names, on the stack for the usual depths
	var buf [32]string
	names := append(append(buf[:0], d.names...), name)
	var marked func() *Rule
	if len(d.tree.rules.xattrs) > 0 {
		marked = func() *Rule { return d.tree.attrBy(d.join(name)) }
	}
	if rule := d.tree.rules.decideLevel(names, d.rel, false, marked); rule != nil {
		return Decision{Kept: rule.Kind == Include, Level: len(names), Rule: rule}
	}
	if r := d.decision.Rule; r != nil && r.Marker == name && d.decision.Level == len(d.names) {
		return d.decideSelf(d.markerIndex(name))
	}
	return d.decision
}

// markerIndex returns the index of the marker rule on the entry name, -1
// for none
func (d *Dir) markerIndex(name string) int {
	for i, m := range d.tree.rules.markers {
		if m.Marker == name {
			return i
		}
	}
	return -1
}

// Decision returns the folder's own decision
func (d *Dir) Decision() Decision {
	return d.decision
}

// Rel returns the folder's path, "" for the root
func (d *Dir) Rel() string {
	return d.rel
}

// Skip reports whether a walker can skip the folder: it is left out, no
// include can match below it, and it holds no marker entry that is kept (a
// marked folder is stored bare with its marker)
func (d *Dir) Skip() bool {
	if d.decision.Kept || d.tree.rules.CanMatchBelow(d.rel) {
		return false
	}
	for i := range d.tree.rules.markers {
		if d.markerKept(i) && d.holds(i) {
			return false
		}
	}
	return true
}

// markerKept reports whether the entry of the marker rule i would be kept
// if the folder held it, without asking the lookups or the listing unless
// the folder was left out by that very marker: a path rule on the entry
// decides it, else it inherits the folder's decision. An xattr on the
// entry could only leave it out, so not asking errs towards not skipping.
func (d *Dir) markerKept(i int) bool {
	m := d.tree.rules.markers[i]
	var buf [32]string
	names := append(append(buf[:0], d.names...), m.Marker)
	if rule := d.tree.rules.decideLevel(names, d.rel, false, nil); rule != nil {
		return rule.Kind == Include
	}
	if r := d.decision.Rule; r != nil && r.Marker == m.Marker && d.decision.Level == len(d.names) {
		return d.decideSelf(i).Kept
	}
	return d.decision.Kept
}

func (d *Dir) join(name string) string {
	if d.rel == "" {
		return name
	}
	return d.rel + "/" + name
}

// holds reports whether the folder holds the entry of the marker rule i:
// from the listing, else the lookups answer (one lstat per question; a
// folder is asked once per marker at most, and stops at the first held)
func (d *Dir) holds(i int) bool {
	if d.list == nil {
		return d.tree.lookups.HoldsMarker(d.rel, d.tree.rules.markers[i].Marker)
	}
	d.resolveHeld()
	return d.held[i]
}

// markedBy returns the marker rule that leaves out the folder, the rule at
// index except aside
func (d *Dir) markedBy(except int) *Rule {
	for i, m := range d.tree.rules.markers {
		if i != except && d.holds(i) {
			return m
		}
	}
	return nil
}

// attrBy returns the xattr rule that leaves out the entry rel: it carries
// the attribute
func (t *Tree) attrBy(rel string) *Rule {
	for _, x := range t.rules.xattrs {
		if t.lookups.HasXattr(rel, x.Xattr) {
			return x
		}
	}
	return nil
}

// Node returns the folder relDir ("" for the root), building the folders
// above it without their listings
func (t *Tree) Node(relDir string) *Dir {
	d := t.Root()
	for _, name := range splitPath(relDir) {
		d = d.Child(name, nil)
	}
	return d
}

// Decide decides an entry on its own: the closest match wins, the entry
// itself first, then each folder above it (see Dir). For a one-off
// question; a walk decides from its Dirs.
func (t *Tree) Decide(rel string, isDir bool) Decision {
	if rel == "" {
		return Decision{Kept: true}
	}
	parent, name := splitLast(rel)
	d := t.Node(parent)
	if isDir {
		return d.Child(name, nil).decision
	}
	return d.File(name)
}

// SkipDir reports whether a walker can skip the folder relDir, see Dir.Skip
func (t *Tree) SkipDir(relDir string) bool {
	return t.Node(relDir).Skip()
}

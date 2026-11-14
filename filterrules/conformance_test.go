package filterrules

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

// testdata/filter_conformance.json is targy's tests/filter_conformance.json,
// copied unchanged from targy commit ca45764.
func TestConformance(t *testing.T) {
	data, err := os.ReadFile("testdata/filter_conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Version int `json:"version"`
		Cases   []struct {
			Name        string              `json:"name"`
			Tools       []string            `json:"tools"` // empty: every tool, native included
			Tree        []string            `json:"tree"`
			Exclude     []string            `json:"exclude"`
			Include     []string            `json:"include"`
			Kept        []string            `json:"kept"`
			MaxFileSize json.RawMessage     `json:"max_file_size"` // the directive's value, or a list of lines ("10M", "none /**/.git"); absent without
			Sizes       map[string]int64    `json:"sizes"`         // file entry to bytes, 0 when absent
			Xattrs      map[string][]string `json:"xattrs"`        // entry to the extended attributes it carries
		} `json:"cases"`
		Invalid []struct {
			Rule string `json:"rule"`
			Code string `json:"code"`
		} `json:"invalid"`
		Sugar []struct {
			Kind     string   `json:"kind"`
			Rule     string   `json:"rule"`
			Expanded []string `json:"expanded"`
		} `json:"sugar"`
		InvalidFiles []struct {
			Text string `json:"text"`
			Code string `json:"code"`
			Line int    `json:"line"`
		} `json:"invalid_files"`
		InvalidFilters []struct {
			Exclude []string `json:"exclude"`
			Include []string `json:"include"`
			Codes   []string `json:"codes"` // errors only, warnings not counted
		} `json:"invalid_filters"`
	}
	if err := json.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Version != 1 {
		t.Fatalf("version %d, want 1", suite.Version)
	}

	native := 0
	for _, c := range suite.Cases {
		if len(c.Tools) > 0 && !slices.Contains(c.Tools, "native") {
			continue
		}
		native++
		t.Run(c.Name, func(t *testing.T) {
			f := newFile(t, c.Exclude, c.Include)
			for _, line := range sizeLines(t, c.MaxFileSize) {
				size, rule, scoped := strings.Cut(line, " ")
				if !scoped {
					f.MaxFileSize = line
				} else if err := f.AddSizeLimit(size, rule); err != nil {
					t.Fatal(err)
				}
			}
			r, err := New(f)
			if err != nil {
				t.Fatal(err)
			}
			tree := r.BindWith(testLookups{treeHas(c.Tree), treeXattrs(c.Xattrs)})
			got, want := selectTree(tree, c.Tree, c.Sizes), slices.Clone(c.Kept)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("kept\n got %q\nwant %q", got, want)
			}
			checkSkipDir(t, tree, c.Tree)
			checkWalk(t, tree, c.Tree, got)
		})
	}
	if native == 0 {
		t.Fatal("no case for the native matcher")
	}

	if len(suite.Invalid) == 0 || len(suite.Sugar) == 0 || len(suite.InvalidFiles) == 0 || len(suite.InvalidFilters) == 0 {
		t.Fatal("a section of the suite is missing or empty")
	}
	for _, inv := range suite.Invalid {
		for _, kind := range []Kind{Exclude, Include} {
			_, err := ParseRule(kind, inv.Rule)
			if got := errorCodes(err); !slices.Equal(got, []string{inv.Code}) {
				t.Errorf("%s %q: codes %q, want %s", kind, inv.Rule, got, inv.Code)
			}
		}
	}
	for _, s := range suite.Sugar {
		kind := Exclude
		if s.Kind == "include" {
			kind = Include
		}
		rules, err := ParseRule(kind, s.Rule)
		if got := ruleStrings(rules); err != nil || !slices.Equal(got, s.Expanded) {
			t.Errorf("%s %q: expanded %q (error %v), want %q", s.Kind, s.Rule, got, err, s.Expanded)
		}
	}
	for _, inv := range suite.InvalidFiles {
		_, err := ParseFile(strings.NewReader(inv.Text))
		if e, ok := err.(*Error); !ok || e.Code != inv.Code || e.Line != inv.Line {
			t.Errorf("file %q: error %v, want %s on line %d", inv.Text, err, inv.Code, inv.Line)
		}
	}
	for _, inv := range suite.InvalidFilters {
		var f File
		for _, text := range inv.Exclude {
			if err := f.Add(Exclude, text); err != nil {
				t.Fatal(err)
			}
		}
		for _, text := range inv.Include {
			if err := f.Add(Include, text); err != nil {
				t.Fatal(err)
			}
		}
		_, err := New(&f)
		if got := errorCodes(err); !slices.Equal(got, inv.Codes) {
			t.Errorf("exclude %q include %q: codes %q, want %q", inv.Exclude, inv.Include, got, inv.Codes)
		}
	}
}

// sizeLines returns a case's max-file-size lines: a string is the default
// alone, a list holds the lines as written, absent is none
func sizeLines(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err != nil {
		t.Fatalf("max_file_size %s: %v", raw, err)
	}
	return lines
}

// newFile builds a File from rules as written and groups
func newFile(t *testing.T, exclude, include []string, uses ...string) *File {
	t.Helper()
	var f File
	for _, text := range exclude {
		if err := f.Add(Exclude, text); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range include {
		if err := f.Add(Include, text); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range uses {
		if err := f.Use(name); err != nil {
			t.Fatal(err)
		}
	}
	return &f
}

// newRules builds Rules from rules as written and groups
func newRules(t *testing.T, exclude, include []string, uses ...string) *Rules {
	t.Helper()
	r, err := New(newFile(t, exclude, include, uses...))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// testLookups answers from a case's tree and xattrs
type testLookups struct {
	holds func(dir, name string) bool
	attr  func(rel, name string) bool
}

func (l testLookups) HoldsMarker(relDir, name string) bool { return l.holds(relDir, name) }
func (l testLookups) HasXattr(rel, name string) bool       { return l.attr(rel, name) }

// bare binds rules without lookups: marker and xattr rules never match
func bare(r *Rules) *Tree {
	return r.BindWith(NoLookups)
}

// decide decides a tree entry: a folder ends with "/"
func decide(tr *Tree, entry string) Decision {
	return tr.Decide(strings.TrimSuffix(entry, "/"), strings.HasSuffix(entry, "/"))
}

// selectTree returns the kept entries of a tree (folders end with "/"), bare
// folders included, in tree order. sizes gives file sizes for the size
// limit, missing entries are empty.
func selectTree(tr *Tree, tree []string, sizes map[string]int64) []string {
	kept := map[string]bool{}
	for _, entry := range tree {
		if decide(tr, entry).Kept && (strings.HasSuffix(entry, "/") || !tr.Rules().TooLarge(entry, sizes[entry])) {
			kept[entry] = true
			names := strings.Split(strings.TrimSuffix(entry, "/"), "/")
			for i := 1; i < len(names); i++ {
				kept[strings.Join(names[:i], "/")+"/"] = true
			}
		}
	}
	var out []string
	for _, entry := range tree {
		if kept[entry] {
			out = append(out, entry)
		}
	}
	return out
}

// checkSkipDir checks that SkipDir never skips a folder holding a kept entry
func checkSkipDir(t *testing.T, tr *Tree, tree []string) {
	t.Helper()
	for _, dir := range tree {
		if !strings.HasSuffix(dir, "/") || !tr.SkipDir(strings.TrimSuffix(dir, "/")) {
			continue
		}
		for _, entry := range tree {
			if strings.HasPrefix(entry, dir) && entry != dir && decide(tr, entry).Kept {
				t.Errorf("SkipDir(%s) is true, but %s below it is kept", dir, entry)
			}
		}
	}
}

// checkWalk walks the case's tree the way a walker does, from Dirs built
// with each folder's listing, and checks it decides every entry like
// Decide does: the kept files, folders and bare marked folders are want.
// Sizes aside: the walk doesn't apply them.
func checkWalk(t *testing.T, tr *Tree, tree []string, want []string) {
	t.Helper()
	children := map[string][]string{}
	isDir := map[string]bool{}
	for _, entry := range tree {
		rel := strings.TrimSuffix(entry, "/")
		isDir[rel] = strings.HasSuffix(entry, "/")
		parent, name := splitLast(rel)
		children[parent] = append(children[parent], name)
	}
	var got []string
	var visit func(d *Dir)
	visit = func(d *Dir) {
		for _, name := range children[d.Rel()] {
			rel := name
			if d.Rel() != "" {
				rel = d.Rel() + "/" + name
			}
			if isDir[rel] {
				if c := d.Child(name, listing(children[rel])); !c.Skip() {
					if c.Decision().Kept {
						got = append(got, rel+"/")
					}
					visit(c)
				}
			} else if d.File(name).Kept {
				got = append(got, rel)
			}
		}
	}
	visit(tr.Root())
	// A walk lists a kept folder once entered; Decide's kept list also has
	// the bare folders a kept entry sits in: compare on files and folders
	// Decide keeps that the walk reached
	for _, entry := range want {
		if strings.HasSuffix(entry, "/") {
			continue
		}
		if !slices.Contains(got, entry) && decide(tr, entry).Kept {
			t.Errorf("walk: %s kept by Decide, not reached by the walk", entry)
		}
	}
	for _, entry := range got {
		if !decide(tr, entry).Kept {
			t.Errorf("walk: %s kept by the walk, not by Decide", entry)
		}
	}
}

// treeXattrs is the attribute lookup for a case: whether the entry rel
// carries the attribute name
func treeXattrs(xattrs map[string][]string) func(rel, name string) bool {
	byEntry := map[string][]string{}
	for entry, names := range xattrs {
		byEntry[strings.TrimSuffix(entry, "/")] = names
	}
	return func(rel, name string) bool {
		return slices.Contains(byEntry[rel], name)
	}
}

// treeHas is the marker lookup for a tree: whether dir directly holds name
func treeHas(tree []string) func(dir, name string) bool {
	entries := map[string]bool{}
	for _, entry := range tree {
		entries[strings.TrimSuffix(entry, "/")] = true
	}
	return func(dir, name string) bool {
		return entries[path.Join(dir, name)]
	}
}

// listTree lists the names in a folder of a tree (folders end with "/")
func listTree(tree []string) ListFunc {
	return func(rel string) ([]string, error) {
		var names []string
		for _, entry := range tree {
			dir, name := path.Split(strings.TrimSuffix(entry, "/"))
			if strings.TrimSuffix(dir, "/") == rel {
				names = append(names, name)
			}
		}
		return names, nil
	}
}

// errorCodes returns the codes of an error, joined errors included
func errorCodes(err error) []string {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var codes []string
		for _, e := range joined.Unwrap() {
			codes = append(codes, errorCodes(e)...)
		}
		return codes
	}
	var e *Error
	if errors.As(err, &e) {
		return []string{e.Code}
	}
	return []string{err.Error()}
}

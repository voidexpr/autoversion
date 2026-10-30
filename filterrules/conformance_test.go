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

// testdata/filter_conformance.json is ownit's tests/filter_conformance.json,
// copied unchanged. ownit hasn't committed it yet: this is its working copy
// of 2026-10-29 (ownit at 6464d7d). Copy it again, and name the commit here,
// once ownit commits it.
func TestConformance(t *testing.T) {
	data, err := os.ReadFile("testdata/filter_conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Version int `json:"version"`
		Cases   []struct {
			Name    string   `json:"name"`
			Tools   []string `json:"tools"` // empty: every tool, native included
			Tree    []string `json:"tree"`
			Exclude []string `json:"exclude"`
			Include []string `json:"include"`
			Kept    []string `json:"kept"`
		} `json:"cases"`
		Invalid []struct {
			Rule string `json:"rule"`
			Code string `json:"code"`
		} `json:"invalid"`
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
			r := newRules(t, c.Exclude, c.Include)
			got, want := selectTree(r, c.Tree), slices.Clone(c.Kept)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("kept\n got %q\nwant %q", got, want)
			}
			checkSkipDir(t, r, c.Tree)
		})
	}
	if native == 0 {
		t.Fatal("no case for the native matcher")
	}

	for _, inv := range suite.Invalid {
		for _, kind := range []Kind{Exclude, Include} {
			_, err := ParseRule(kind, inv.Rule)
			if got := errorCodes(err); !slices.Equal(got, []string{inv.Code}) {
				t.Errorf("%s %q: codes %q, want %s", kind, inv.Rule, got, inv.Code)
			}
		}
	}
}

// newRules builds Rules from rules as written and groups
func newRules(t *testing.T, exclude, include []string, uses ...string) *Rules {
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
	r, err := New(&f)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// decide decides a tree entry: a folder ends with "/"
func decide(r *Rules, entry string) Decision {
	return r.Decide(strings.TrimSuffix(entry, "/"), strings.HasSuffix(entry, "/"))
}

// selectTree returns the kept entries of a tree (folders end with "/"), bare
// folders included, in tree order
func selectTree(r *Rules, tree []string) []string {
	kept := map[string]bool{}
	for _, entry := range tree {
		if decide(r, entry).Kept {
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
func checkSkipDir(t *testing.T, r *Rules, tree []string) {
	t.Helper()
	for _, dir := range tree {
		if !strings.HasSuffix(dir, "/") || !r.SkipDir(strings.TrimSuffix(dir, "/")) {
			continue
		}
		for _, entry := range tree {
			if strings.HasPrefix(entry, dir) && entry != dir && decide(r, entry).Kept {
				t.Errorf("SkipDir(%s) is true, but %s below it is kept", dir, entry)
			}
		}
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

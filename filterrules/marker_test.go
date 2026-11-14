package filterrules

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestMarkers(t *testing.T) {
	tree := []string{
		"CACHEDIR.TAG", "keep.txt",
		"cache/", "cache/CACHEDIR.TAG", "cache/data.bin", "cache/sub/", "cache/sub/x",
		"proj/", "proj/.venv/", "proj/.venv/pyvenv.cfg", "proj/.venv/lib/", "proj/.venv/lib/x", "proj/main.py",
		"tools/", "tools/env/", "tools/env/pyvenv.cfg", "tools/env/bin/", "tools/env/bin/run",
	}
	r := newFileRules(t, "version 1\nexclude marker:{CACHEDIR.TAG,pyvenv.cfg}\n/tools/env\n")
	if got := r.Markers(); !slices.Equal(got, []string{"CACHEDIR.TAG", "pyvenv.cfg"}) {
		t.Errorf("Markers() = %q", got)
	}

	// Without lookups, markers never match
	if !decide(bare(r), "cache/data.bin").Kept {
		t.Error("with NoLookups, cache/data.bin is left out")
	}
	tr := r.BindWith(testLookups{treeHas(tree), noAttr})

	d := decide(tr, "cache/data.bin")
	if d.Kept || d.Rule == nil || d.Rule.String() != "exclude marker:CACHEDIR.TAG" || d.Level != 1 {
		t.Errorf("cache/data.bin: %+v", d)
	}
	checkTreeDecisions(t, tr,
		[]string{"cache/", "cache/data.bin", "cache/sub/", "cache/sub/x", "proj/.venv/", "proj/.venv/lib/x"},
		[]string{"keep.txt", "CACHEDIR.TAG", "cache/CACHEDIR.TAG", "proj/.venv/pyvenv.cfg", "proj/main.py",
			"tools/env/", "tools/env/pyvenv.cfg", "tools/env/bin/run"})

	// A walker enters a marked folder for its marker entry, skips its subfolders
	if tr.SkipDir("cache") || !tr.SkipDir("cache/sub") || tr.SkipDir("tools/env") {
		t.Errorf("SkipDir: cache %v (want false), cache/sub %v (want true), tools/env %v (want false)",
			tr.SkipDir("cache"), tr.SkipDir("cache/sub"), tr.SkipDir("tools/env"))
	}
	checkSkipDir(t, tr, tree)

	// The same from a walker's Dirs, with the listings: no lookup asked
	asked := 0
	counting := testLookups{func(dir, name string) bool { asked++; return treeHas(tree)(dir, name) }, noAttr}
	wt := r.BindWith(counting)
	root := wt.Root()
	cache := root.Child("cache", listing([]string{"CACHEDIR.TAG", "data.bin", "sub"}))
	if cache.Decision().Kept || cache.Skip() || !cache.File("CACHEDIR.TAG").Kept || cache.File("data.bin").Kept {
		t.Errorf("cache from its listing: %+v, skip %v", cache.Decision(), cache.Skip())
	}
	if sub := cache.Child("sub", listing([]string{"x"})); !sub.Skip() || sub.Decision().Level != 1 {
		t.Errorf("cache/sub: %+v, skip %v", sub.Decision(), sub.Skip())
	}
	env := root.Child("tools", listing([]string{"env"})).Child("env", listing([]string{"pyvenv.cfg", "bin"}))
	if !env.Decision().Kept || env.Skip() {
		t.Errorf("tools/env, included: %+v", env.Decision())
	}
	if asked != 0 {
		t.Errorf("%d lookups asked with the listings given", asked)
	}
	// Without a listing, the lookups answer, once per marker name
	if c := root.Child("cache", nil); c.Decision().Kept || asked != 1 {
		t.Errorf("cache without a listing: %+v, %d lookups (want 1: the first marker holds)", c.Decision(), asked)
	}

	// Check: the root holds a marker; /tools/env is marked and included
	var codes []string
	for _, p := range r.Check(listTree(tree)) {
		codes = append(codes, p.Code)
	}
	for _, want := range []string{"root-marked", "include-under-marker"} {
		if !slices.Contains(codes, want) {
			t.Errorf("Check codes %q, want %s", codes, want)
		}
	}

	if !strings.Contains(r.Dump(), "exclude marker:pyvenv.cfg\n") {
		t.Errorf("Dump:\n%s", r.Dump())
	}
}

func TestMarkerRules(t *testing.T) {
	rules, err := ParseRule(Exclude, "marker:{CACHEDIR.TAG,pyvenv.cfg}")
	if err != nil || len(rules) != 2 || rules[0].Marker != "CACHEDIR.TAG" || rules[1].Literal() {
		t.Errorf("rules %v, error %v", rules, err)
	}
	for text, code := range map[string]string{
		"marker:": "bad-marker", "marker:a/b": "bad-marker", "marker:*.tag": "bad-marker",
		"marker:a?b": "forbidden-char", "marker:.": "dot-segment", "marker: x": "space-edge",
	} {
		if _, err := ParseRule(Exclude, text); errorCodes(err)[0] != code {
			t.Errorf("%s: error %v, want %s", text, err, code)
		}
	}
	if _, err := ParseRule(Include, "marker:x"); errorCodes(err)[0] != "bad-marker" {
		t.Errorf("include marker: error %v, want bad-marker", err)
	}
	// Twice in a file: one rule
	r := newFileRules(t, "version 1\nexclude marker:x\nexclude marker:x\n")
	if len(r.Markers()) != 1 {
		t.Errorf("markers %q, want one", r.Markers())
	}
}

// noAttr is the attribute lookup of a tree without attributes
func noAttr(string, string) bool { return false }

// listing returns a folder listing as Dir.Child takes it
func listing(names []string) func() []string {
	return func() []string { return names }
}

// One Rules shared by walks of two trees, each deciding with its own Tree,
// two goroutines per tree sharing its Dirs (go test -race)
func TestBindConcurrent(t *testing.T) {
	r := newFileRules(t, "version 1\nexclude marker:CACHEDIR.TAG\n")
	trees := []struct {
		name string
		tr   *Tree
		kept bool
	}{
		{"marked tree", r.BindWith(testLookups{treeHas([]string{"cache/", "cache/CACHEDIR.TAG", "cache/x"}), noAttr}), false},
		{"unmarked tree", r.BindWith(testLookups{treeHas([]string{"cache/", "cache/x"}), noAttr}), true},
		{"no lookup", bare(r), true},
	}
	var wg sync.WaitGroup
	for _, c := range trees {
		root := c.tr.Root()
		for range 2 {
			wg.Go(func() {
				for range 1000 {
					if got := c.tr.Decide("cache/x", false).Kept; got != c.kept {
						t.Errorf("%s: cache/x kept %v, want %v", c.name, got, c.kept)
						return
					}
					if got := root.Child("cache", nil).File("x").Kept; got != c.kept {
						t.Errorf("%s: cache/x from a shared Dir kept %v, want %v", c.name, got, c.kept)
						return
					}
					if c.tr.SkipDir("cache") {
						t.Errorf("%s: SkipDir(cache)", c.name)
						return
					}
				}
			})
		}
	}
	wg.Wait()
}

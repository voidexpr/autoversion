package filterrules

import (
	"slices"
	"strings"
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

	// Without a lookup, markers never match
	if !decide(r, "cache/data.bin").Kept {
		t.Error("without UseMarkers, cache/data.bin is left out")
	}
	r.UseMarkers(treeHas(tree))

	d := decide(r, "cache/data.bin")
	if d.Kept || d.Rule == nil || d.Rule.String() != "exclude marker:CACHEDIR.TAG" || d.Level != 1 {
		t.Errorf("cache/data.bin: %+v", d)
	}
	checkDecisions(t, r,
		[]string{"cache/", "cache/data.bin", "cache/sub/", "cache/sub/x", "proj/.venv/", "proj/.venv/lib/x"},
		[]string{"keep.txt", "CACHEDIR.TAG", "cache/CACHEDIR.TAG", "proj/.venv/pyvenv.cfg", "proj/main.py",
			"tools/env/", "tools/env/pyvenv.cfg", "tools/env/bin/run"})

	// A walker enters a marked folder for its marker entry, skips its subfolders
	if r.SkipDir("cache") || !r.SkipDir("cache/sub") || r.SkipDir("tools/env") {
		t.Errorf("SkipDir: cache %v (want false), cache/sub %v (want true), tools/env %v (want false)",
			r.SkipDir("cache"), r.SkipDir("cache/sub"), r.SkipDir("tools/env"))
	}
	checkSkipDir(t, r, tree)

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

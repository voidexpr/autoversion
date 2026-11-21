package filterrules

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

// textFilters is a Resolver over filter texts, parsed on each Lookup with
// itself for their own use lines, as a caller would
type textFilters map[string]string

func (n textFilters) Lookup(name string) (*File, error) {
	text, ok := n[name]
	if !ok {
		return nil, nil
	}
	return ParseFileWith(strings.NewReader(text), n)
}

func (n textFilters) Names() []string { return slices.Sorted(maps.Keys(n)) }

var named = textFilters{
	"dev/go":        "version 1\nuse dev/build\nexclude /**/*.test\n",
	"dev/build":     "version 1\nexclude /**/bin\n",
	"agent/secrets": "version 1\nexclude /**/.env\n",
	"keep-docs":     "version 1\ninclude /docs\n",
	"go-writes":     "version 1\nexclude /*\ninclude /bin\ninclude d:/**/testdata\n",
	"vcs":           "version 1\nexclude /mine\n", // shadows the built-in group
	"with-default":  "version 1\nuse default\n",
	"loop-a":        "version 1\nuse loop-b\n",
	"loop-b":        "version 1\nuse loop-c\n",
	"loop-c":        "version 1\nuse loop-a\n",
	"self":          "version 1\nuse self\n",
	"rooted":        "version 1\nroot ~\nexclude /x\n",
	"typed":         "version 1\ntypes decay\nexclude /x\n",
	"sized":         "version 1\nmax-file-size 1M\n",
	"scoped":        "version 1\nmax-file-size 1M /x\n",
	"broken":        "version 1\nexclude x\n",
}

// namedRules parses text with named and builds its rules
func namedRules(t *testing.T, text string) (*Rules, error) {
	t.Helper()
	f, err := ParseFileWith(strings.NewReader(text), named)
	if err != nil {
		t.Fatal(err)
	}
	return NewWith(f, named)
}

func groupsOf(rules []*Rule) []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.Group)
	}
	return out
}

func TestNamed_PrefixedAndNested(t *testing.T) {
	r, err := namedRules(t, "version 1\nuse dev/go\nuse agent/secrets\nexclude /a\n")
	if err != nil {
		t.Fatal(err)
	}
	list := r.List()
	want := []string{"exclude /a", "exclude /**/*.test", "exclude /**/bin", "exclude /**/.env"}
	if got := ruleStrings(derefRules(list)); !slices.Equal(got, want) {
		t.Errorf("rules %q, want %q", got, want)
	}
	if got := groupsOf(list); !slices.Equal(got, []string{"", "dev/go", "dev/build", "agent/secrets"}) {
		t.Errorf("groups %q", got)
	}
	tree := bare(r)
	for rel, kept := range map[string]bool{"a": false, "x/y.test": false, "x/bin": false, "x/.env": false, "x/main.go": true} {
		if d := tree.Decide(rel, false); d.Kept != kept {
			t.Errorf("%s: kept %v, want %v", rel, d.Kept, kept)
		}
	}
}

func TestNamed_IncludesInUsedFilter(t *testing.T) {
	r, err := namedRules(t, "version 1\nuse go-writes\n")
	if err != nil {
		t.Fatal(err)
	}
	tree := bare(r)
	for rel, kept := range map[string]bool{"bin": true, "bin/x": true, "src": false, "a/testdata": true, "a/testdata/x": true} {
		if d := tree.Decide(rel, true); d.Kept != kept {
			t.Errorf("%s: kept %v, want %v", rel, d.Kept, kept)
		}
	}
	r, err = namedRules(t, "version 1\nuse keep-docs\nexclude /*\n")
	if err != nil {
		t.Fatal(err)
	}
	if d := bare(r).Decide("docs", true); !d.Kept || d.Rule.Group != "keep-docs" || d.Rule.Kind != Include {
		t.Errorf("docs: %+v", d)
	}
}

func TestNamed_BuiltInsAndShadowing(t *testing.T) {
	// the caller's vcs shadows the built-in group
	r, err := namedRules(t, "version 1\nuse vcs\n")
	if err != nil {
		t.Fatal(err)
	}
	tree := bare(r)
	if d := tree.Decide("mine", true); d.Kept || d.Rule.Group != "vcs" {
		t.Errorf("mine: %+v, want left out by the caller's vcs", d)
	}
	if d := tree.Decide(".hg", true); !d.Kept {
		t.Errorf(".hg: left out by %v, want the built-in vcs shadowed", d.Rule)
	}
	// the built-in groups, directly and through a used filter
	r, err = namedRules(t, "version 1\nuse os\nuse with-default\n")
	if err != nil {
		t.Fatal(err)
	}
	tree = bare(r)
	if d := tree.Decide(".DS_Store", false); d.Kept || d.Rule.Group != "os" {
		t.Errorf(".DS_Store: %+v, want left out by os", d)
	}
	if d := tree.Decide("x/node_modules", true); d.Kept || d.Rule.Group != "deps" {
		t.Errorf("node_modules: %+v, want left out by deps through with-default", d)
	}
	// without a resolver, use vcs is the built-in
	f := parseFile(t, "version 1\nuse vcs\n")
	rules, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	if d := bare(rules).Decide(".hg", true); d.Kept {
		t.Error(".hg kept by the built-in vcs")
	}
}

func TestNamed_Errors(t *testing.T) {
	tests := []struct {
		text string
		code string // of ParseFileWith when parse is set, else of NewWith
		want string // in the error message
		line int
	}{
		{"version 1\nuse nope\n", "unknown-rules", "known: agent/secrets, agents, ", 2},
		{"version 1\nuse dev/nope\n", "unknown-rules", "dev/build, dev/go, ", 2},
		{"version 1\nuse loop-a\n", "use-cycle", `"loop-a -> loop-b -> loop-c -> loop-a"`, 0},
		{"version 1\nuse self\n", "use-cycle", `"self -> self"`, 0},
		{"version 1\nuse rooted\n", "bad-directive", `use rooted: bad-directive: "root ~"`, 0},
		{"version 1\nuse typed\n", "bad-directive", `use typed: bad-directive: "types decay"`, 0},
		{"version 1\nuse sized\n", "bad-directive", `use sized: bad-directive: "max-file-size 1M"`, 0},
		{"version 1\nuse scoped\n", "bad-directive", `use scoped: bad-directive: "max-file-size 1M /x"`, 0},
		{"version 1\nuse broken\n", "no-root", `use broken: line 2: no-root`, 2},
	}
	for _, tt := range tests {
		f, err := ParseFileWith(strings.NewReader(tt.text), named)
		if err == nil {
			_, err = NewWith(f, named)
		}
		var e *Error
		if !errors.As(err, &e) || e.Code != tt.code || e.Line != tt.line || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: error %v, want %s on line %d containing %q", tt.text, err, tt.code, tt.line, tt.want)
		}
	}
	// the cycle is found from any filter on it, and from the file's own use line
	if _, err := namedRules(t, "version 1\nuse loop-b\n"); !strings.Contains(err.Error(), "loop-b -> loop-c -> loop-a -> loop-b") {
		t.Errorf("from loop-b: %v", err)
	}
}

func TestNamed_UseWith(t *testing.T) {
	var f File
	if err := f.UseWith("dev/go", named); err != nil {
		t.Fatal(err)
	}
	if err := f.UseWith("dev/go", named); err != nil || !slices.Equal(f.Uses, []string{"dev/go"}) {
		t.Errorf("twice: %v, uses %q", err, f.Uses)
	}
	if err := f.UseWith("os", named); err != nil {
		t.Errorf("built-in with a resolver: %v", err)
	}
	if err := f.Use("dev/go"); errorCodes(err)[0] != "unknown-rules" || strings.Contains(err.Error(), "dev/go,") {
		t.Errorf("Use without a resolver: %v", err)
	}
	if got := f.Format(); got != "version 1\nuse dev/go\nuse os\n" {
		t.Errorf("Format:\n%s", got)
	}
}

func derefRules(list []*Rule) []Rule {
	out := make([]Rule, len(list))
	for i, r := range list {
		out[i] = *r
	}
	return out
}

package filterrules

import (
	"slices"
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	valid := map[string]int64{
		"none": 0, "1k": 1024, "1K": 1024, "10M": 10 << 20, "10m": 10 << 20, "2G": 2 << 30, "4k": 4096, "1T": 1 << 40,
		"1kb": 1024, "10MB": 10 << 20, "10Mb": 10 << 20, "2gb": 2 << 30, "1tb": 1 << 40,
		"10": 10, "4096": 4096, "10b": 10, "10B": 10,
	}
	for text, want := range valid {
		if got, err := ParseSize(text); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v, want %d", text, got, err, want)
		}
	}
	for _, text := range []string{"", "b", "10kbb", "10 M", "1.5G", "1.5", "0", "0k", "-1", "-1k", "+1", "+1k", "k", "kb", "10P", "None", "99999999999999999999", "99999999999999999999g", "9999999999t"} {
		if got, err := ParseSize(text); err == nil {
			t.Errorf("ParseSize(%q) = %d, want an error", text, got)
		}
	}
}

func TestRulesMaxFileSize(t *testing.T) {
	r := newFileRules(t, "version 1\nmax-file-size 4k\nuse default\n")
	if text, bytes := r.MaxFileSize(); text != "4k" || bytes != 4096 {
		t.Errorf("MaxFileSize() = %q, %d", text, bytes)
	}
	if r.TooLarge("x", 4096) || !r.TooLarge("x", 4097) {
		t.Error("exactly 4k is kept, one byte more is not")
	}
	if !strings.Contains(r.Dump(), "max-file-size 4k\n") {
		t.Errorf("Dump():\n%s", r.Dump())
	}

	none := newFileRules(t, "version 1\nmax-file-size none\nuse default\n")
	if text, bytes := none.MaxFileSize(); text != "none" || bytes != 0 || none.TooLarge("x", 1<<40) {
		t.Errorf("none: %q, %d", text, bytes)
	}
	absent := newFileRules(t, "version 1\nuse default\n")
	if text, bytes := absent.MaxFileSize(); text != "" || bytes != 0 || absent.TooLarge("x", 1<<40) {
		t.Errorf("absent: %q, %d", text, bytes)
	}
}

func TestScopedLimits(t *testing.T) {
	r := newFileRules(t, `version 1
max-file-size 10M
max-file-size none /**/.git
max-file-size 1G /data
max-file-size 100M /data/raw
max-file-size 1G f:/**/*.pack
max-file-size 1M /**/*.bin
max-file-size 2M f:/**/*.bin
max-file-size none /**/*.iso
max-file-size 2M /**/*.iso
max-file-size 1G /**/models
max-file-size none /**/models
use default
`)
	tests := map[string]string{
		"a/big.db":              "", // the default
		"a/.git/objects/p.pack": "max-file-size 1G f:/**/*.pack",
		"a/.git/y.bin":          "max-file-size 1M /**/*.bin", // its own match, a tier below the folder's named one but closer
		"a/.git/config":         "max-file-size none /**/.git",
		"data/x":                "max-file-size 1G /data",
		"data/raw/y":            "max-file-size 100M /data/raw",
		"x.bin":                 "max-file-size 1M /**/*.bin",  // two unnamed: the strictest
		"x.iso":                 "max-file-size 2M /**/*.iso",  // none is the loosest
		"ml/models/w":           "max-file-size 1G /**/models", // two named: the strictest
	}
	for rel, want := range tests {
		got := ""
		if rule := r.Limit(rel); rule != nil {
			got = rule.String()
		}
		if got != want {
			t.Errorf("Limit(%s) = %q, want %q", rel, got, want)
		}
	}
	if r.TooLarge("a/big.db", 20<<20) != true || r.TooLarge("a/.git/config", 20<<20) != false ||
		r.TooLarge("data/raw/z", 50<<20) != false || r.TooLarge("data/raw/y", 500<<20) != true {
		t.Error("TooLarge: the scoped limit of the entry applies")
	}
	if limits := r.SizeLimits(); len(limits) != 10 || limits[0].String() != "max-file-size none /**/.git" {
		t.Errorf("SizeLimits: %v", limits)
	}
	dump := r.Dump()
	if !strings.Contains(dump, "max-file-size 10M\nmax-file-size none /**/.git\nmax-file-size 1G /data\n") {
		t.Errorf("Dump():\n%s", dump)
	}
	again := newFileRules(t, dump)
	if !slices.Equal(ruleStrings(again.all), ruleStrings(r.all)) || !slices.Equal(ruleStrings(again.sizeLimits), ruleStrings(r.sizeLimits)) {
		t.Errorf("a dump doesn't read back as the same rules and limits:\n%s", again.Dump())
	}
}

func TestScopedLimits_Errors(t *testing.T) {
	var f File
	for _, tt := range []struct{ size, rule, want string }{
		{"10M", "marker:CACHEDIR.TAG", "not a marker"},
		{"10M", "/a/!b", "no !"},
		{"10X", "/a", "the unit"},
		{"10M", "a", "max-file-size:"},
	} {
		err := f.AddSizeLimit(tt.size, tt.rule)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("AddSizeLimit(%s, %s) = %v, want %s", tt.size, tt.rule, err, tt.want)
		}
	}
	if len(f.SizeLimits) != 0 {
		t.Errorf("a refused line was added: %v", f.SizeLimits)
	}

	// Two literal lines on the same path with different sizes
	_, err := ParseFile(strings.NewReader("version 1\nmax-file-size 1M /x\nmax-file-size 2M /x\n"))
	if err != nil {
		t.Fatal(err)
	}
	f2, _ := ParseFile(strings.NewReader("version 1\nmax-file-size 1M /x\nmax-file-size 2M /x\n"))
	if _, err := New(f2); err == nil || !strings.Contains(err.Error(), "contradiction") {
		t.Errorf("New: %v, want a contradiction", err)
	}
	f3, _ := ParseFile(strings.NewReader("version 1\nmax-file-size 1M /x\nmax-file-size 1M /x\nmax-file-size 2M f:/x\n"))
	if _, err := New(f3); err != nil {
		t.Errorf("the same size twice, or another type, is no contradiction: %v", err)
	}
}

package filterrules

import (
	"slices"
	"strings"
	"testing"
)

func parseFile(t *testing.T, text string) *File {
	t.Helper()
	f, err := ParseFile(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func lineStrings(lines []Line) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l.String())
	}
	return out
}

func TestParseFile(t *testing.T) {
	f := parseFile(t, `# home
version 1   # the version
root ~/x
types decay
use os
use os

exclude /a   # a comment
/Issue #12
include /b
f:/**/*.md
/Library/Application Support/x
	exclude /skills/!synced
`)
	if f.Root != "~/x" || f.Types != "decay" || !slices.Equal(f.Uses, []string{"os"}) {
		t.Errorf("root %q, types %q, uses %q", f.Root, f.Types, f.Uses)
	}
	wantLines := []string{"exclude /a", "include /Issue", "include /b", "include f:/**/*.md",
		"include /Library/Application Support/x", "exclude /skills/!synced"}
	if got := lineStrings(f.Lines); !slices.Equal(got, wantLines) {
		t.Errorf("lines\n got %q\nwant %q", got, wantLines)
	}
	wantRules := []string{"exclude /a", "include /Issue", "include /b", "include f:/**/*.md",
		"include /Library/Application Support/x", "exclude /skills/*", "include /skills/synced"}
	if got := ruleStrings(f.Rules); !slices.Equal(got, wantRules) {
		t.Errorf("rules\n got %q\nwant %q", got, wantRules)
	}
}

func TestParseFileRoot(t *testing.T) {
	for _, root := range []string{"~", "~/Dropbox", "/Users/x"} {
		if f := parseFile(t, "version 1\nroot "+root+"\n"); f.Root != root {
			t.Errorf("root %q, want %q", f.Root, root)
		}
	}
	if f := parseFile(t, "version 1\r\nexclude /a\r\n"); !slices.Equal(lineStrings(f.Lines), []string{"exclude /a"}) {
		t.Errorf("CRLF lines: %q", lineStrings(f.Lines))
	}
}

func TestParseFileErrors(t *testing.T) {
	tests := []struct {
		text string
		code string
		line int
	}{
		{"", "bad-directive", 0},
		{"# only a comment\n", "bad-directive", 0},
		{"use default\nversion 1\n", "bad-directive", 1},
		{"version 2\n", "bad-version", 1},
		{"version\n", "bad-version", 1},
		{"version 1\nversion 1\n", "bad-directive", 2},
		{"version 1\nroot ~\nroot ~\n", "bad-directive", 3},
		{"version 1\nroot x\n", "bad-directive", 2},
		{"version 1\ntypes maybe\n", "bad-directive", 2},
		{"version 1\ntypes decay\ntypes strict\n", "bad-directive", 3},
		{"version 1\nmax-file-size b\n", "bad-directive", 2},
		{"version 1\nmax-file-size 10P\n", "bad-directive", 2},
		{"version 1\nmax-file-size 1.5G\n", "bad-directive", 2},
		{"version 1\nmax-file-size 0k\n", "bad-directive", 2},
		{"version 1\nmax-file-size -1k\n", "bad-directive", 2},
		{"version 1\nmax-file-size\n", "bad-directive", 2},
		{"version 1\nmax-file-size 10M\nmax-file-size none\n", "bad-directive", 3},
		{"version 1\n/a\nmax-file-size 10M\n", "bad-directive", 3},
		{"version 1\n/a\nuse default\n", "bad-directive", 3},
		{"version 1\nuse common\n", "unknown-rules", 2},
		{"version 1\nexclude-if-present pyvenv.cfg\n", "unknown-directive", 2},
		{"version 1\nexclude-files /a\n", "unknown-directive", 2},
		{"version 1\nnode_modules\n", "no-root", 2},
		{"version 1\nexclude node_modules\n", "no-root", 2},
		{"version 1\n\nexclude /a/\n", "trailing-slash", 3},
		{"version 1\nexclude  /a\n", "no-root", 2},
	}
	for _, tt := range tests {
		_, err := ParseFile(strings.NewReader(tt.text))
		e, ok := err.(*Error)
		if !ok || e.Code != tt.code || e.Line != tt.line {
			t.Errorf("%q: error %v, want %s on line %d", tt.text, err, tt.code, tt.line)
		}
	}
}

func TestFileFormat(t *testing.T) {
	f := File{MaxFileSize: "10M"}
	f.Use("default")
	f.Add(Exclude, "/*")
	f.Add(Include, "/skills/!synced")
	want := "version 1\nmax-file-size 10M\nuse default\n\nexclude /*\ninclude /skills/!synced\n"
	if got := f.Format(); got != want {
		t.Errorf("Format()\n%s\nwant\n%s", got, want)
	}
	again := parseFile(t, f.Format())
	if !slices.Equal(ruleStrings(again.Rules), ruleStrings(f.Rules)) || !slices.Equal(again.Uses, f.Uses) || again.MaxFileSize != "10M" {
		t.Errorf("read back: uses %q rules %q max-file-size %q", again.Uses, ruleStrings(again.Rules), again.MaxFileSize)
	}
	if got := (&File{}).Format(); got != "version 1\n" {
		t.Errorf("empty file: %q", got)
	}
}

func TestFileAddAndUse(t *testing.T) {
	var f File
	if err := f.Add(Exclude, "/a/"); errorCodes(err)[0] != "trailing-slash" {
		t.Errorf("Add: error %v, want trailing-slash", err)
	}
	if err := f.Use("nope"); errorCodes(err)[0] != "unknown-rules" {
		t.Errorf("Use: error %v, want unknown-rules", err)
	}
	if len(f.Lines) != 0 || len(f.Rules) != 0 || len(f.Uses) != 0 {
		t.Errorf("a refused rule or group was added: %+v", f)
	}
}

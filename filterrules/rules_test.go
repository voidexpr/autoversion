package filterrules

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// The ~/.claude example of plans/feature_include_exclude_rules.md
const claudeFilter = `version 1
use default

exclude /*
/CLAUDE.md
/settings.json
/settings.local.json
/skills/!synced
/projects/*/memory
`

func newFileRules(t *testing.T, text string) *Rules {
	t.Helper()
	r, err := New(parseFile(t, text))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestClaudeExample(t *testing.T) {
	r := newFileRules(t, claudeFilter)
	tests := []struct {
		entry string
		kept  bool
		level int
		rule  string
	}{
		{"CLAUDE.md", true, 1, "include /CLAUDE.md"},
		{"settings.json", true, 1, "include /settings.json"},
		{"settings.local.json", true, 1, "include /settings.local.json"},
		{"plugins/cache/x/CLAUDE.md", false, 1, "exclude /*"},
		{"history.jsonl", false, 1, "exclude /*"},
		{"skills/pdf/SKILL.md", true, 1, "include /skills"},
		{"skills/synced/foo/SKILL.md", false, 2, "exclude /skills/synced"},
		{"skills/pdf/node_modules/x.js", false, 3, "exclude /**/node_modules"},
		{"skills/pdf/x.pyc", false, 3, "exclude f:/**/*.pyc"},
		{"projects/-Users-x/memory/MEMORY.md", true, 3, "include /projects/*/memory"},
		{"projects/-Users-x/abc123.jsonl", false, 1, "exclude /*"},
		{"file-history/abc/1.json", false, 1, "exclude /*"},
	}
	for _, tt := range tests {
		d := r.Decide(tt.entry, false)
		if d.Kept != tt.kept || d.Level != tt.level || d.Rule == nil || d.Rule.String() != tt.rule {
			t.Errorf("%s: kept %v level %d by %v, want %v level %d by %q",
				tt.entry, d.Kept, d.Level, d.Rule, tt.kept, tt.level, tt.rule)
		}
	}
	if d := r.Decide("skills/pdf/node_modules", true); d.Rule.Group != "deps" {
		t.Errorf("node_modules decided by group %q, want deps", d.Rule.Group)
	}

	skip := map[string]bool{
		"file-history":                true,
		"plugins":                     true,
		"skills/synced":               true,
		"skills/pdf/node_modules":     true,
		"projects/-Users-x/subagents": true,
		"skills":                      false,
		"skills/pdf":                  false,
		"projects":                    false,
		"projects/-Users-x":           false,
		"projects/-Users-x/memory":    false,
	}
	for dir, want := range skip {
		if got := r.SkipDir(dir); got != want {
			t.Errorf("SkipDir(%s) = %v, want %v", dir, got, want)
		}
	}

	tree := []string{"CLAUDE.md", "settings.json", "settings.local.json", "history.jsonl",
		"skills/", "skills/pdf/", "skills/pdf/SKILL.md", "skills/synced/", "skills/synced/a.md",
		"projects/", "projects/-Users-x/", "projects/-Users-x/memory/", "projects/-Users-x/memory/MEMORY.md",
		"projects/-Users-x/abc.jsonl", "plugins/", "plugins/CLAUDE.md"}
	checkSkipDir(t, r, tree)
	if problems := r.Check(listTree(tree)); len(problems) > 0 {
		t.Errorf("problems: %v", problems)
	}
}

// One case per row of the plan's translation table, with the default groups
func TestDefaultGroups(t *testing.T) {
	r := newRules(t, nil, nil, "default")
	out := []string{
		"a.log", "a.dump", "a.swp", "a.swpx", "a.swo", "a.pid", "x.beam",
		"4913", "a/4913", "notes.txt~",
		"a.out", "run.stdout", "x.err", "x.stderr", "x.depend", ".dropbox/x", ".dropbox.cache/x",
		"a.old", "a.temp", "a.bak", "a.tmp",
		".DS_Store", "a/.DS_Store", ".nfs000123",
		"lib.o", "x.obj", "x.so", "x.a", "x.dylib", "x.rlib", "x.rmeta", "x.dll", "x.exe", "x.pdb", "x.d", "x.gch",
		"A.class", "x.jar", "x.war", "x.ear",
		"repo.git/HEAD", ".hg/x", ".svn/x", ".cvs/x",
		"x.tmp.1", "x.tmp.1.2", "PLAN.md.tmp.18812.1760578375713",
		".venv/x", ".pytest_cache/x", ".playwright-mcp/x",
		"node_modules/x", "a/node_modules/x", "__pycache__/x.pyc", "pgdata/x",
		"target/debug/x", "a/target/release/x",
		".gradle/x", "DerivedData/x", ".build/x",
		"dist/x", "build/x", "out/x", "a/build/sub/x",
		".cache/x", ".next/x", ".nuxt/x", ".output/x", "playwright-report/x", "test-results/x",
		".npm/x", "bower_components/x", ".pnpm-store/x", "venv/x",
		".idea/x", "x.iml", "Thumbs.db", "desktop.ini", ".Spotlight-V100/x", ".Trashes/x",
		"x.pyc", "x.wal", "db.pgdb/x",
	}
	kept := []string{
		"Cargo.lock", "uv.lock", "flake.lock", "x.bea#", "2024", "notes.output.md", "x.errors/a.txt", "x.nfs",
		"conf.d/", "conf.d/x.conf", "foo.old/", "foo.old/x.txt", "x.log/y.txt", "a.tmp.d/",
		"my_node_modules/x", "target/x", ".xcworkspace/x",
		"build", "build/", "dist", "out/", ".yarn/releases/x.cjs", ".vscode/settings.json",
		"src/main.go", "README.md",
		"a.jpg", "a.mp4", "@eaDir/x", "a.jpg@SynoResource",
	}
	checkDecisions(t, r, out, kept)
}

// checkDecisions checks the entries r leaves out and keeps
func checkDecisions(t *testing.T, r *Rules, out, kept []string) {
	t.Helper()
	for _, entry := range out {
		if decide(r, entry).Kept {
			t.Errorf("%s kept, want left out", entry)
		}
	}
	for _, entry := range kept {
		if d := decide(r, entry); !d.Kept {
			t.Errorf("%s left out by %v, want kept", entry, d.Rule)
		}
	}
}

// The opt-in groups, see plans/feature_reusable_filter_files.md
func TestMediaAndNASGroups(t *testing.T) {
	media := newRules(t, nil, nil, "media")
	checkDecisions(t, media,
		[]string{"a.mp3", "a.MP3", "IMG.HEIC", "clip.mov", "clip.MOV", "CLIP.MTS", "x/raw/a.CR3", "a.flac"},
		[]string{"x.ts", "x.mts", "go.mod", "logo.svg", "a.Jpg", "a.move", "x.mp3/", "x.mp3/file", "x.raw", "a.psd"})
	nas := newRules(t, nil, nil, "nas")
	checkDecisions(t, nas,
		[]string{"@eaDir/x", "a/@eaDir/x", "a.jpg@SynoResource", "a.jpg@SynoEAStream"},
		[]string{"x@SynoResource/", "x@SynoResource/y", "a.jpg", "@eaDirs/x"})
}

func TestGroups(t *testing.T) {
	want := []string{"build", "caches", "data", "default", "deps", "editors", "logs", "media", "music", "nas", "os", "photos", "temp", "vcs", "videos"}
	if got := GroupNames(); !slices.Equal(got, want) {
		t.Errorf("groups %q, want %q", got, want)
	}
	byName := map[string]GroupInfo{}
	for _, g := range Groups() {
		byName[g.Name] = g
	}
	for _, combined := range []string{"default", "media"} {
		all, _ := Group(combined)
		count := 0
		for _, name := range byName[combined].Use {
			rules, err := Group(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rules {
				if r.Kind != Exclude || r.Group != name {
					t.Errorf("%s: %v from group %q", name, r, r.Group)
				}
			}
			count += len(rules)
		}
		if len(all) != count || count == 0 {
			t.Errorf("%s has %d rules, its groups %d", combined, len(all), count)
		}
	}
	if want := []string{"os", "editors", "temp", "logs", "build", "deps", "caches", "data", "vcs"}; !slices.Equal(byName["default"].Use, want) {
		t.Errorf("default uses %q, want %q", byName["default"].Use, want)
	}
	if _, err := Group("common"); errorCodes(err)[0] != "unknown-rules" {
		t.Errorf("Group(common): %v, want unknown-rules", err)
	}
}

func TestNewErrors(t *testing.T) {
	tests := []struct {
		exclude, include []string
		uses             []string
		codes            []string
	}{
		{[]string{"/*"}, nil, nil, []string{"whole-root"}},
		{[]string{"/*"}, nil, []string{"default"}, []string{"whole-root"}},
		{[]string{"/**/*"}, nil, nil, []string{"whole-root"}},
		{[]string{"/**/*"}, []string{"/a"}, nil, []string{"whole-root"}},
		{[]string{"/a"}, []string{"/a"}, nil, []string{"contradiction"}},
		{[]string{"f:/a"}, []string{"d:/a"}, nil, []string{"contradiction"}},
		{[]string{"/**/*", "/a"}, []string{"/a"}, nil, []string{"whole-root", "contradiction"}},
		{[]string{"/*"}, []string{"/a"}, nil, nil},
		{[]string{"f:/*", "d:/**/*"}, nil, nil, nil},
		{[]string{"/a/*"}, []string{"/a/*"}, nil, nil},
	}
	for _, tt := range tests {
		var f File
		for _, text := range tt.exclude {
			f.Add(Exclude, text)
		}
		for _, text := range tt.include {
			f.Add(Include, text)
		}
		for _, name := range tt.uses {
			f.Use(name)
		}
		_, err := New(&f)
		if got := errorCodes(err); !slices.Equal(got, tt.codes) {
			t.Errorf("exclude %q include %q use %q: codes %q, want %q", tt.exclude, tt.include, tt.uses, got, tt.codes)
		}
	}
}

func TestCheck(t *testing.T) {
	tree := []string{"CLAUDE.md", "debug.log", "skills/", "skills/a/", "Library/", "Library/Caches/", "x/", "x/y/"}
	tests := []struct {
		filter string
		want   []string // Problem.String() prefixes: level and code
	}{
		{"exclude /*\n/CLAUDE.md\n/missing", []string{"warning: dead-rule: include /missing"}},
		{"exclude /*\n/CLAUD.md", []string{"warning: dead-rule: include /CLAUD.md", "error: whole-root"}},
		{"exclude /*\n/CLAUD.md\nf:/**/*.md", []string{"warning: dead-rule: include /CLAUD.md"}},
		{"exclude f:/*\n/CLAUD.md", []string{"warning: dead-rule: include /CLAUD.md"}},
		{"exclude /Library/caches", []string{"warning: dead-rule: exclude /Library/caches"}},
		{"/skills", []string{"warning: changes-nothing: no include changes anything"}},
		{"exclude /x\n/skills\n/x/y", []string{"warning: changes-nothing: include /skills changes nothing"}},
		{"use logs\ninclude f:/debug.log", nil},
		{"use logs\ninclude /debug.log", nil},
		{"exclude /skills\ninclude /skills/*", nil},
		{"exclude /café", []string{"warning: non-ascii: exclude /café", "warning: dead-rule: exclude /café"}},
	}
	for _, tt := range tests {
		r := newFileRules(t, "version 1\n"+tt.filter+"\n")
		problems := r.Check(listTree(tree))
		if len(problems) != len(tt.want) {
			t.Errorf("%q: problems %v, want %q", tt.filter, problems, tt.want)
			continue
		}
		for i, p := range problems {
			if !strings.HasPrefix(p.String(), tt.want[i]) {
				t.Errorf("%q: problem %q, want %q...", tt.filter, p, tt.want[i])
			}
		}
	}
}

// A folder that can't be listed (permissions) says nothing about what it
// holds: the rules under it are not dead, and an allowlist isn't refused
func TestCheck_UnreadableFolderIsNotDead(t *testing.T) {
	list := func(rel string) ([]string, error) {
		switch rel {
		case "":
			return []string{"Library"}, nil
		case "Library":
			return nil, errors.New("operation not permitted")
		}
		return nil, nil
	}
	r := newFileRules(t, "version 1\nexclude /*\n/Library/Mail/x\n/CLAUD.md\n")
	problems := r.Check(list)
	if len(problems) != 1 || !strings.HasPrefix(problems[0].String(), "warning: dead-rule: include /CLAUD.md") {
		t.Errorf("problems %v, want only the dead /CLAUD.md", problems)
	}
}

func TestDump(t *testing.T) {
	r := newFileRules(t, "version 1\ntypes decay\nuse temp\nexclude /*\n/skills/!synced\n")
	want := `version 1
types decay

# own rules
exclude /*
include /skills
exclude /skills/synced

# group temp
exclude f:/**/*.tmp
exclude f:/**/*.tmp.*
exclude f:/**/*.temp
exclude f:/**/*.bak
exclude f:/**/*.old
`
	if got := r.Dump(); got != want {
		t.Errorf("dump\n%s\nwant\n%s", got, want)
	}

	// A dump reads back as the same rules
	r = newFileRules(t, claudeFilter)
	again := newFileRules(t, r.Dump())
	if got, want := ruleStrings(again.all), ruleStrings(r.all); !slices.Equal(got, want) {
		t.Errorf("dump read back\n got %q\nwant %q", got, want)
	}
}

func BenchmarkDecideDefault(b *testing.B) {
	f := &File{}
	f.Use("default")
	r, _ := New(f)
	for b.Loop() {
		r.Decide("projects/autoversion/filterrules/testdata/filter_conformance.json", false)
	}
}

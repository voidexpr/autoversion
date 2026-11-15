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
		d := bare(r).Decide(tt.entry, false)
		if d.Kept != tt.kept || d.Level != tt.level || d.Rule == nil || d.Rule.String() != tt.rule {
			t.Errorf("%s: kept %v level %d by %v, want %v level %d by %q",
				tt.entry, d.Kept, d.Level, d.Rule, tt.kept, tt.level, tt.rule)
		}
	}
	if d := bare(r).Decide("skills/pdf/node_modules", true); d.Rule.Group != "deps" {
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
		if got := bare(r).SkipDir(dir); got != want {
			t.Errorf("SkipDir(%s) = %v, want %v", dir, got, want)
		}
	}

	tree := []string{"CLAUDE.md", "settings.json", "settings.local.json", "history.jsonl",
		"skills/", "skills/pdf/", "skills/pdf/SKILL.md", "skills/synced/", "skills/synced/a.md",
		"projects/", "projects/-Users-x/", "projects/-Users-x/memory/", "projects/-Users-x/memory/MEMORY.md",
		"projects/-Users-x/abc.jsonl", "plugins/", "plugins/CLAUDE.md"}
	checkSkipDir(t, bare(r), tree)
	if problems := r.Check(listTree(tree)); len(problems) > 0 {
		t.Errorf("problems: %v", problems)
	}
}

// One case per row of the plan's translation table, with the default groups
func TestDefaultGroups(t *testing.T) {
	r := newRules(t, nil, nil, "default")
	out := []string{
		"a.log", "a.swp", "a.swpx", "a.swo", "a.pid", "x.beam",
		"4913", "a/4913", "notes.txt~",
		"a.out", "run.stdout", "x.err", "x.stderr", "x.depend", ".dropbox/x", ".dropbox.cache/x",
		"a.temp", "a.bak", "a.tmp",
		".DS_Store", "a/.DS_Store", ".nfs000123", ".localized", "Desktop/.localized", ".fseventsd/x",
		"lib.o", "x.obj", "x.so", "x.a", "x.dylib", "x.rlib", "x.rmeta", "x.dll", "x.exe", "x.pdb", "x.gch",
		"A.class", "x.jar", "x.war", "x.ear",
		".hg/x", ".svn/x", ".cvs/x",
		"x.tmp.1", "x.tmp.1.2", "PLAN.md.tmp.18812.1760578375713",
		".venv/x", ".pytest_cache/x", ".playwright-mcp/x",
		"node_modules/x", "a/node_modules/x", "__pycache__/x.pyc", "pgdata/x",
		"target/debug/x", "a/target/release/x",
		".gradle/x", "DerivedData/x", ".build/x",
		"dist/x", "build/x", "out/x", "a/build/sub/x",
		".cache/x", ".next/x", ".nuxt/x", ".output/x", "playwright-report/x", "test-results/x",
		".npm/x", "bower_components/x", ".pnpm-store/x", "venv/x", ".local/share/pnpm/x", "a/.local/share/pnpm/store/x",
		".idea/x", "x.iml", "Thumbs.db", "desktop.ini", ".Spotlight-V100/x", ".Trashes/x",
		"x.pyc", "x.wal", "db.pgdb/x",
		".mypy_cache/x", ".ruff_cache/x", ".tox/x", ".nox/x", ".parcel-cache/x", ".turbo/x", ".svelte-kit/x",
		".angular/cache/x", ".zig-cache/x", "zig-out/bin/x", "cdk.out/x", "__pypackages__/x", ".direnv/x",
		"go/pkg/mod/x", "a/go/pkg/mod/github.com/x@v1.0.0/y.go",
	}
	kept := []string{
		"db.dump", "exp.dmp", "db.sql.gz", "schema.sql",
		"Cargo.lock", "uv.lock", "flake.lock", "x.bea#", "2024", "notes.output.md", "x.errors/a.txt", "x.nfs",
		".localized/", ".localized/x", ".local/share/x", "pnpm/x", "share/pnpm/x",
		"conf.d/", "conf.d/x.conf", "foo.old/", "foo.old/x.txt", "x.log/y.txt", "a.tmp.d/",
		"my_node_modules/x", "target/x", ".xcworkspace/x",
		"build", "build/", "dist", "out/", ".yarn/releases/x.cjs", ".vscode/settings.json",
		"src/main.go", "README.md",
		"a.jpg", "a.mp4", "@eaDir/x", "a.jpg@SynoResource",
		".git/config", "a/.git/x", "repo.git/HEAD", // nested-git, not in default
		"go/pkg/x", "pkg/mod/x", "go/src/mod/x",
		".m2/repository/x", ".terraform/x", "_build/x", "Pods/x", ".yarn/cache/x", ".Trash/x", // opt-in
	}
	checkDecisions(t, r, out, kept)

	// nested-git: .git folders and files, bare repositories, at any level
	checkDecisions(t, newRules(t, nil, nil, "nested-git"),
		[]string{".git/", ".git/config", "a/.git/x", "a/b/.git", "repo.git/", "repo.git/HEAD"},
		[]string{".gitignore", ".gitmodules", ".github/x", "sub/git/x", "git.txt"})
}

// checkDecisions checks the entries r leaves out and keeps
func checkDecisions(t *testing.T, r *Rules, out, kept []string) {
	t.Helper()
	checkTreeDecisions(t, bare(r), out, kept)
}

func checkTreeDecisions(t *testing.T, tr *Tree, out, kept []string) {
	t.Helper()
	for _, entry := range out {
		if decide(tr, entry).Kept {
			t.Errorf("%s kept, want left out", entry)
		}
	}
	for _, entry := range kept {
		if d := decide(tr, entry); !d.Kept {
			t.Errorf("%s left out by %v, want kept", entry, d.Rule)
		}
	}
}

// The opt-in groups, see plans/feature_reusable_filter_files.md
func TestOptInGroups(t *testing.T) {
	media := newRules(t, nil, nil, "media")
	checkDecisions(t, media,
		[]string{"a.mp3", "a.MP3", "IMG.HEIC", "clip.mov", "clip.MOV", "CLIP.MTS", "x/raw/a.CR3", "a.flac"},
		[]string{"x.ts", "x.mts", "go.mod", "logo.svg", "a.Jpg", "a.move", "x.mp3/", "x.mp3/file", "x.raw", "a.psd"})
	nas := newRules(t, nil, nil, "nas")
	checkDecisions(t, nas,
		[]string{"@eaDir/x", "a/@eaDir/x", "a.jpg@SynoResource", "a.jpg@SynoEAStream"},
		[]string{"x@SynoResource/", "x@SynoResource/y", "a.jpg", "@eaDirs/x"})
	checkDecisions(t, newRules(t, nil, nil, "home-caches"),
		[]string{".m2/repository/x", ".nuget/packages/x", ".kube/cache/x", "a/.kube/http-cache/x", ".vagrant.d/boxes/x"},
		[]string{".m2/settings.xml", ".nuget/NuGet/NuGet.Config", ".kube/config", ".vagrant.d/Vagrantfile", "repository/x"})
	checkDecisions(t, newRules(t, nil, nil, "extra-tools"),
		[]string{".dart_tool/x", ".stack-work/x", "elm-stuff/x", ".cpcache/x", ".shadow-cljs/x", ".godot/x",
			"_build/x", "docs/_build/html/x", "Pods/x", ".terraform/x", ".terragrunt-cache/x",
			"Carthage/Build/x", "Carthage/Checkouts/x", "renv/library/x", ".yarn/cache/x", ".yarn/unplugged/x"},
		[]string{"Podfile", "Podfile.lock", "Cartfile.resolved", ".terraform.lock.hcl", "renv.lock", "renv/activate.R",
			".yarn/releases/yarn.cjs", ".yarn/plugins/x", ".yarnrc.yml", "Build/x"})
	checkDecisions(t, newRules(t, nil, nil, "db-dumps"),
		[]string{"db.dump", "exp.dmp", "db.sql.gz", "x/db.sql.zst"},
		[]string{"schema.sql", "db.sql", "dump/", "dump/x"})
	checkDecisions(t, newRules(t, nil, nil, "installers"),
		[]string{"x.dmg", "Downloads/App.pkg", "Old.pkg/", "Old.pkg/Contents/Info.plist", "Suite.mpkg/", "flat.mpkg"},
		[]string{"x.dmg/", "x.pkgs", "a.dmg.txt", "pkg/x"})
}

func TestGroups(t *testing.T) {
	want := []string{"agents", "browser-profiles", "build", "cache-markers", "caches", "data", "db-dumps", "default", "deps", "editors", "extra-tools", "home-caches", "installers", "junk", "local-state", "logs", "media", "music", "nas", "nested-git", "os", "photos", "temp", "time-machine", "toolchains", "trash", "vcs", "videos", "vms"}
	if got := GroupNames(); !slices.Equal(got, want) {
		t.Errorf("groups %q, want %q", got, want)
	}
	byName := map[string]GroupInfo{}
	for _, g := range Groups() {
		byName[g.Name] = g
	}
	for _, combined := range []string{"default", "media", "junk"} {
		all, _ := Group(combined)
		count := 0
		for _, r := range all {
			if r.Group == combined {
				count++
			}
		}
		for _, name := range byName[combined].Use {
			rules, err := Group(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rules {
				if r.Kind != Exclude || len(byName[name].Use) == 0 && r.Group != name {
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
	r := newFileRules(t, "version 1\ntypes decay\nuse vcs\nexclude /*\n/skills/!synced\n")
	want := `version 1
types decay

# own rules
exclude /*
include /skills
exclude /skills/synced

# group vcs
exclude /**/.hg
exclude /**/.svn
exclude /**/.cvs
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
	tr := bare(r)
	for b.Loop() {
		tr.Decide("projects/autoversion/filterrules/testdata/filter_conformance.json", false)
	}
}

func TestList(t *testing.T) {
	r := newFileRules(t, "version 1\nuse default\nexclude /a\nexclude /**/.DS_Store\n")
	list := r.List()
	if len(list) != len(r.all) || list[0].String() != "exclude /a" || list[0].Group != "" || list[len(list)-1].Group == "" {
		t.Errorf("List(): %d rules, first %v, last group %q", len(list), list[0], list[len(list)-1].Group)
	}
	for _, entry := range []string{"a", "x/.DS_Store", "node_modules/y"} {
		d := bare(r).Decide(entry, false)
		if d.Rule == nil || !slices.Contains(list, d.Rule) {
			t.Errorf("%s: decided by %v, not a pointer into List()", entry, d.Rule)
		}
	}
}

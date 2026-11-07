package filterrules

import (
	"path"
	"slices"
	"testing"
)

// The spec says path.Match reads a name pattern as the spec does, once "?",
// "[" and "\" are refused
func TestPatternMatchesLikePathMatch(t *testing.T) {
	patterns := []string{"a", "*", "*.log", ".nfs*", "*.tmp.*", "a*b", "a*b*c", "*a*", "ab*ba", "*~", "**x"}
	names := []string{"", "a", "b", "ab", "aba", "abba", "abcba", "abc", "aXbYc", "acb", "x.log", ".log",
		"x.log.txt", ".nfs0001", "x.tmp.1", "x.tmp", "notes~", "~", "x", "**x", "été.log"}
	for _, p := range patterns {
		for _, name := range names {
			want, _ := path.Match(p, name)
			if got := newPattern(p).match(name); got != want {
				t.Errorf("%q matches %q: %v, path.Match says %v", p, name, got, want)
			}
		}
	}
}

func ruleStrings(rules []Rule) []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.String())
	}
	return out
}

func TestParseRuleSugar(t *testing.T) {
	tests := []struct {
		kind Kind
		text string
		want []string
	}{
		{Exclude, "/**/node_modules", []string{"exclude /**/node_modules"}},
		{Include, "/projects/*/memory", []string{"include /projects/*/memory"}},
		{Exclude, "/Library/Mobile Documents/com~apple~CloudDocs/Old stuff",
			[]string{"exclude /Library/Mobile Documents/com~apple~CloudDocs/Old stuff"}},
		{Exclude, "/a:b", []string{"exclude /a:b"}},
		{Include, "/skills/!synced", []string{"include /skills", "exclude /skills/synced"}},
		{Exclude, "/Library/Containers/!net.whatsapp.WhatsApp",
			[]string{"exclude /Library/Containers/*", "include /Library/Containers/net.whatsapp.WhatsApp"}},
		{Exclude, "/!{Sync,notes}", []string{"exclude /*", "include /Sync", "include /notes"}},
		{Include, "/!{Pictures,Movies}", []string{"exclude /Pictures", "exclude /Movies"}},
		{Include, "/Dropbox/!{Borg,.dropbox,dotfiles/borg/.cache}", []string{
			"include /Dropbox", "exclude /Dropbox/Borg", "exclude /Dropbox/.dropbox", "exclude /Dropbox/dotfiles/borg/.cache"}},
		{Exclude, "/Library/Application Support/*/{Cache,Code Cache,GPUCache}", []string{
			"exclude /Library/Application Support/*/Cache",
			"exclude /Library/Application Support/*/Code Cache",
			"exclude /Library/Application Support/*/GPUCache"}},
		{Exclude, "/{a}", []string{"exclude /a"}},
		{Exclude, "/{a,b}/{c,d}", []string{"exclude /a/c", "exclude /a/d", "exclude /b/c", "exclude /b/d"}},
		{Exclude, "/{a,a}", []string{"exclude /a"}},
		{Exclude, "f:/**/{*.tmp,*.bak}", []string{"exclude f:/**/*.tmp", "exclude f:/**/*.bak"}},
		{Exclude, "d:/**/build", []string{"exclude d:/**/build"}},
		{Include, "f:/**/*.md", []string{"include f:/**/*.md"}},
	}
	for _, tt := range tests {
		rules, err := ParseRule(tt.kind, tt.text)
		if err != nil {
			t.Errorf("%s %q: %v", tt.kind, tt.text, err)
			continue
		}
		if got := ruleStrings(rules); !slices.Equal(got, tt.want) {
			t.Errorf("%s %q\n got %q\nwant %q", tt.kind, tt.text, got, tt.want)
		}
	}
}

// Codes beyond the conformance suite's
func TestParseRuleErrors(t *testing.T) {
	tests := []struct {
		kind Kind
		text string
		code string
	}{
		{Exclude, "", "no-root"},
		{Exclude, "f:node_modules", "no-root"},
		{Exclude, "/", "trailing-slash"},
		{Exclude, "C:/a", "bad-type"},
		{Exclude, "/a/{b", "bad-brace"},
		{Exclude, "/a}", "bad-brace"},
		{Exclude, "/a/{!b,c}", "bad-except"},
		{Exclude, "/a/!", "bad-except"},
		{Exclude, "!/a", "bad-except"},
		{Exclude, "d:/a/!b", "bad-except"},
		{Include, "/**/!x", "double-star-last"},
		{Exclude, "/**", "double-star-last"},
		{Exclude, "/a/***", "star-run"},
		{Exclude, "/a\tb", "forbidden-char"},
		{Exclude, "/a\x7f", "forbidden-char"},
		{Exclude, "/a ", "space-edge"},
		{Exclude, "/a/./b", "dot-segment"},
	}
	for _, tt := range tests {
		_, err := ParseRule(tt.kind, tt.text)
		if got := errorCodes(err); !slices.Equal(got, []string{tt.code}) {
			t.Errorf("%s %q: codes %q, want %s", tt.kind, tt.text, got, tt.code)
		}
	}

	_, err := ParseRule(Exclude, "f:/a/{b,!c}")
	if e, ok := err.(*Error); !ok || e.Rule != "f:/a/{b,!c}" {
		t.Errorf("error %v: want the rule as written", err)
	}
}

// The shared spec's Excludes table
func TestDecideSpecExcludes(t *testing.T) {
	tests := []struct {
		rule      string
		out, kept []string
	}{
		{"/Backup/autoversion", []string{"Backup/autoversion", "Backup/autoversion/2026/a.txt"},
			[]string{"Backup/autoversion2", "Old/Backup/autoversion", "Backup/Autoversion"}},
		{"/Backup/auto*", []string{"Backup/auto", "Backup/autoversion/a.txt", "Backup/auto.log"},
			[]string{"Backup/old/autoversion", "auto"}},
		{"/*.iso", []string{"disk.iso"}, []string{"old/disk.iso"}},
		{"/**/.DS_Store", []string{".DS_Store", "a/b/.DS_Store"}, []string{"a/.DS_Store.bak"}},
		{"/**/node_modules", []string{"node_modules", "web/node_modules/x/y.js"}, []string{"web/node_modules_old"}},
		{"/**/*.tmp", []string{"x.tmp", "a/.tmp", "a/b.tmp/c.txt"}, []string{"a/b.tmp.txt"}},
		{"/**/.cache/*", []string{".cache/x", "a/.cache/x", "a/.cache/sub/y"}, []string{"a/.cache/", "b/.cache"}},
		{"/**/.evidence/template", []string{".evidence/template", "x/.evidence/template/f"},
			[]string{"x/.evidence/templates", "x/template"}},
		{"/Photos/*/thumbnails", []string{"Photos/2024/thumbnails", "Photos/2024/thumbnails/a.jpg"},
			[]string{"Photos/thumbnails", "Photos/2024/x/thumbnails"}},
		{"/Projects/**/build", []string{"Projects/build", "Projects/a/b/build/x.o"},
			[]string{"build", "Projects/a/builder"}},
		{"/Library/Containers/*/Data/Library/Caches", []string{"Library/Containers/com.x.app/Data/Library/Caches/f"},
			[]string{"Library/Containers/com.x.app/Data/Library/Preferences/p.plist"}},
		{"/Library/Mobile Documents/com~apple~CloudDocs/Old stuff",
			[]string{"Library/Mobile Documents/com~apple~CloudDocs/Old stuff/x"},
			[]string{"Library/Mobile Documents/com~apple~CloudDocs/Old stuffs"}},
		{"f:/**/*.old", []string{"a.old", "c/d.old"}, []string{"b.old/", "b.old/x", "c/e.old/y"}},
		{"d:/**/build", []string{"w/build/", "w/build/o"}, []string{"build"}},
	}
	for _, tt := range tests {
		r := newRules(t, []string{tt.rule}, nil)
		for _, entry := range tt.out {
			if decide(r, entry).Kept {
				t.Errorf("exclude %s: %s kept, want left out", tt.rule, entry)
			}
		}
		for _, entry := range tt.kept {
			if !decide(r, entry).Kept {
				t.Errorf("exclude %s: %s left out, want kept", tt.rule, entry)
			}
		}
	}
}

// The shared spec's Includes table and "All the cases"
func TestDecideSpecIncludes(t *testing.T) {
	whatsapp := [][]string{{"/Library/Containers", "/**/.cache"}, {"/Library/Containers/net.whatsapp.WhatsApp"}}
	data := [][]string{{"/Data", "/Data/Backup/Dropbox"}, {"/Data/Backup"}}
	tools := [][]string{{"/**/node_modules"}, {"/tools/node_modules"}}
	memory := [][]string{{"/projects/old/memory"}, {"/projects/*/memory"}}
	logs := [][]string{{"/**/*.log"}, {"/Logs"}}
	allowlist := [][]string{{"/*"}, {"/notes"}}
	tie := [][]string{{"/**/x"}, {"/a/*"}} // a named wildcard beats one ending in a wildcard
	dotfiles := [][]string{{"/*", "/**/.cache"}, {"/.*"}}
	named := [][]string{{"/**/*.log"}, {"/**/app.log"}} // and an include too
	plain := [][]string{{"/**/*.log"}, {"/logs/*"}}     // two unnamed wildcards: the include wins
	tests := []struct {
		rules [][]string // excludes, includes
		entry string
		kept  bool
		level int
		rule  string // "" when no rule matched
	}{
		{whatsapp, "Library/Containers/com.other.app/x", false, 2, "exclude /Library/Containers"},
		{whatsapp, "Library/Containers/net.whatsapp.WhatsApp/Data/msg.db", true, 3,
			"include /Library/Containers/net.whatsapp.WhatsApp"},
		{whatsapp, "Library/Containers/net.whatsapp.WhatsApp/Data/.cache/x", false, 5, "exclude /**/.cache"},
		{whatsapp, "Library/Caches/y", true, 0, ""},
		{data, "Data/Media/a.mp4", false, 1, "exclude /Data"},
		{data, "Data/Backup/Contacts/a.vcf", true, 2, "include /Data/Backup"},
		{data, "Data/Backup/Dropbox/x", false, 3, "exclude /Data/Backup/Dropbox"},
		{tools, "tools/node_modules/x.js", true, 2, "include /tools/node_modules"},
		{tools, "tools/node_modules/a/node_modules/y.js", false, 4, "exclude /**/node_modules"},
		{memory, "projects/old/memory/x.md", false, 3, "exclude /projects/old/memory"},
		{memory, "projects/abc/memory/x.md", true, 3, "include /projects/*/memory"},
		{logs, "Logs/app.log", false, 2, "exclude /**/*.log"},
		{allowlist, "notes/a.md", true, 1, "include /notes"},
		{allowlist, "Media/x.mp4", false, 1, "exclude /*"},
		{tie, "a/x", false, 2, "exclude /**/x"},
		{dotfiles, ".cache/uv/x", false, 1, "exclude /**/.cache"},
		{dotfiles, ".vimrc", true, 1, "include /.*"},
		{named, "logs/app.log", true, 2, "include /**/app.log"},
		{plain, "logs/app.log", true, 2, "include /logs/*"},
	}
	for _, tt := range tests {
		r := newRules(t, tt.rules[0], tt.rules[1])
		d := decide(r, tt.entry)
		rule := ""
		if d.Rule != nil {
			rule = d.Rule.String()
		}
		if d.Kept != tt.kept || d.Level != tt.level || rule != tt.rule {
			t.Errorf("%v %s: kept %v level %d by %q, want %v level %d by %q",
				tt.rules, tt.entry, d.Kept, d.Level, rule, tt.kept, tt.level, tt.rule)
		}
	}
}

func TestDecideTypedRules(t *testing.T) {
	tests := []struct {
		exclude, include []string
		entry            string
		kept             bool
	}{
		{[]string{"f:/**/*.old"}, nil, "a.old", false},
		{[]string{"f:/**/*.old"}, nil, "b.old/", true},
		{[]string{"f:/**/*.old"}, nil, "b.old/x", true},
		{[]string{"f:/a"}, nil, "a", false},
		{[]string{"f:/a"}, nil, "a/", true},
		{[]string{"f:/a"}, nil, "a/x", true},
		{[]string{"d:/**/build"}, nil, "build", true},
		{[]string{"d:/**/build"}, nil, "w/build/", false},
		{[]string{"d:/**/build"}, nil, "w/build/o", false},
		{[]string{"/*"}, []string{"f:/**/*.md"}, "a.md", true},
		{[]string{"/*"}, []string{"f:/**/*.md"}, "docs/x.md", true},
		{[]string{"/*"}, []string{"f:/**/*.md"}, "docs.md/", false},
		{[]string{"/*"}, []string{"f:/**/*.md"}, "docs/x.txt", false},
	}
	for _, tt := range tests {
		r := newRules(t, tt.exclude, tt.include)
		if got := decide(r, tt.entry).Kept; got != tt.kept {
			t.Errorf("exclude %q include %q: %s kept %v, want %v", tt.exclude, tt.include, tt.entry, got, tt.kept)
		}
	}
}

func TestCanMatchBelow(t *testing.T) {
	tests := []struct {
		include string
		dir     string
		want    bool
	}{
		{"/a/b/c", "", true},
		{"/a/b/c", "a", true},
		{"/a/b/c", "a/b", true},
		{"/a/b/c", "a/b/c", false},
		{"/a/b/c", "x", false},
		{"/a/*/c", "a/x", true},
		{"/a/*/c", "b", false},
		{"/**/x", "any/where", true},
		{"/a/**/x", "a/b/c", true},
		{"/a/**/x", "b", false},
		{"/a/b/**/x", "a", true},
		{"/a/b/**/x", "c", false},
		{"f:/**/*.md", "docs", true},
	}
	for _, tt := range tests {
		r := newRules(t, []string{"/*"}, []string{tt.include})
		if got := r.CanMatchBelow(tt.dir); got != tt.want {
			t.Errorf("include %s: CanMatchBelow(%q) = %v, want %v", tt.include, tt.dir, got, tt.want)
		}
	}
}

package filterrules

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// TestGroupFiles checks the group files as they load: a "# NAME: about"
// first line, version 1, only excludes, no root or types
func TestGroupFiles(t *testing.T) {
	for _, g := range Groups() {
		if g.About == "" {
			t.Errorf("%s: no about", g.Name)
		}
		if len(g.Exclude) == 0 && len(g.Use) == 0 {
			t.Errorf("%s: no rules", g.Name)
		}
		if len(g.Exclude) > 0 && len(g.Use) > 0 {
			t.Errorf("%s: both rules and use lines", g.Name)
		}
	}
}

func TestLoadGroupFiles_Errors(t *testing.T) {
	// The names exist in filters/, so use accepts them: only the loader's
	// own checks fail
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no header", map[string]string{"os.filter": "version 1\nexclude /a\n"}, "first line"},
		{"another name", map[string]string{"os.filter": "# vcs: x\nversion 1\nexclude /a\n"}, "first line"},
		{"empty about", map[string]string{"os.filter": "# os: \nversion 1\nexclude /a\n"}, "first line"},
		{"no version", map[string]string{"os.filter": "# os: x\nexclude /a\n"}, "version 1"},
		{"include", map[string]string{"os.filter": "# os: x\nversion 1\n/a\n"}, "only excludes"},
		{"root", map[string]string{"os.filter": "# os: x\nversion 1\nroot ~\nexclude /a\n"}, "filter's choice"},
		{"types", map[string]string{"os.filter": "# os: x\nversion 1\ntypes decay\nexclude /a\n"}, "filter's choice"},
		{"unknown use", map[string]string{"os.filter": "# os: x\nversion 1\nuse nope\n"}, "unknown-rules"},
		{"use itself", map[string]string{"os.filter": "# os: x\nversion 1\nuse os\n"}, "use cycle: os -> os"},
		{"cycle", map[string]string{
			"os.filter":      "# os: x\nversion 1\nuse vcs\n",
			"vcs.filter":     "# vcs: x\nversion 1\nuse default\n",
			"default.filter": "# default: x\nversion 1\nuse vcs\n",
		}, "use cycle: default -> vcs -> default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for name, text := range tt.files {
				fsys[name] = &fstest.MapFile{Data: []byte(text)}
			}
			_, err := loadGroupFiles(fsys)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadGroupFiles_UsesAreResolved(t *testing.T) {
	s, err := loadGroupFiles(fstest.MapFS{
		"os.filter":      &fstest.MapFile{Data: []byte("# os: x\nversion 1\nexclude /a\n")},
		"vcs.filter":     &fstest.MapFile{Data: []byte("# vcs: x\nversion 1\nexclude /b\nexclude /a\n")},
		"default.filter": &fstest.MapFile{Data: []byte("# default: x\nversion 1\nuse vcs\nuse os\n")},
		"notes.txt":      &fstest.MapFile{Data: []byte("not a group")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range s.list {
		names = append(names, g.Name)
	}
	if want := []string{"default", "os", "vcs"}; !slices.Equal(names, want) {
		t.Errorf("groups %q, want %q", names, want)
	}
	// vcs's rules first, then os's; /a deduped, kept as vcs's
	if got := ruleStrings(s.rules["default"]); !slices.Equal(got, []string{"exclude /b", "exclude /a"}) {
		t.Errorf("default rules %q", got)
	}
	if g := s.rules["default"][1].Group; g != "vcs" {
		t.Errorf("/a from group %q, want vcs", g)
	}
}

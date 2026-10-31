package filterrules

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/nicolas/autoversion/filters"
)

// GroupInfo is a built-in group: filters/NAME.filter, a rules file whose
// first line is "# NAME: about".
type GroupInfo struct {
	Name    string
	About   string
	Exclude []string // its rules as written, sugar included
	Use     []string // the groups it combines
}

const groupExt = ".filter"

// groupSet is the built-in groups, loaded from filters.FS
type groupSet struct {
	list  []GroupInfo
	rules map[string][]Rule // a group's rules and its used groups', deduped
}

// loadGroups loads the groups on first use. A package variable would be an
// initialization cycle: ParseFile checks use names against the groups.
var loadGroups = sync.OnceValue(func() *groupSet {
	s, err := loadGroupFiles(filters.FS)
	if err != nil {
		panic(err)
	}
	return s
})

// groupFileNames returns the names of the groups in fsys, alphabetically,
// without parsing them
func groupFileNames(fsys fs.FS) []string {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		panic(fmt.Sprintf("filters: %v", err))
	}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), groupExt); ok && !e.IsDir() {
			names = append(names, name)
		}
	}
	return names
}

// groupExists reports whether a built-in group exists. It doesn't load the
// groups: File.Use calls it while they load.
func groupExists(name string) bool {
	info, err := fs.Stat(filters.FS, name+groupExt)
	return err == nil && !info.IsDir()
}

// loadGroupFiles parses every NAME.filter in fsys. A rule's Group is the
// file it is written in; a group that uses others gets their rules after
// its own (built-in Rules use nothing, shared spec).
func loadGroupFiles(fsys fs.FS) (*groupSet, error) {
	s := &groupSet{rules: map[string][]Rule{}}
	own := map[string][]Rule{}
	uses := map[string][]string{}
	for _, name := range groupFileNames(fsys) {
		data, err := fs.ReadFile(fsys, name+groupExt)
		if err != nil {
			return nil, err
		}
		info, rules, err := parseGroup(name, string(data))
		if err != nil {
			return nil, fmt.Errorf("filters/%s%s: %v", name, groupExt, err)
		}
		s.list = append(s.list, *info)
		own[name], uses[name] = rules, info.Use
	}

	var resolve func(name string, stack []string) ([]Rule, error)
	resolve = func(name string, stack []string) ([]Rule, error) {
		if rules, ok := s.rules[name]; ok {
			return rules, nil
		}
		if i := slices.Index(stack, name); i >= 0 {
			return nil, &Error{Code: "use-cycle", Rule: strings.Join(append(stack[i:], name), " -> "), Reason: "a group uses itself"}
		}
		stack = append(stack, name)
		all := slices.Clone(own[name])
		for _, used := range uses[name] {
			rules, err := resolve(used, stack)
			if err != nil {
				return nil, err
			}
			all = append(all, rules...)
		}
		s.rules[name] = dedupe(all)
		return s.rules[name], nil
	}
	for _, g := range s.list {
		if _, err := resolve(g.Name, nil); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// parseGroup parses a group file: "# NAME: about" first, then a rules file
// with only exclude rules, no root and no types (a filter's choice; the
// shared spec lets an implementation refuse them with bad-directive)
func parseGroup(name, text string) (*GroupInfo, []Rule, error) {
	first, _, _ := strings.Cut(text, "\n")
	about, ok := strings.CutPrefix(first, "# "+name+": ")
	if !ok || strings.TrimSpace(about) == "" {
		return nil, nil, fmt.Errorf("first line is %q, want \"# %s: what it is\"", first, name)
	}
	f, err := ParseFile(strings.NewReader(text))
	if err != nil {
		return nil, nil, err
	}
	for directive, value := range map[string]string{"root": f.Root, "types": f.Types} {
		if value != "" {
			return nil, nil, &Error{Code: "bad-directive", Rule: directive + " " + value, Reason: "a filter's choice, not a group's"}
		}
	}
	info := &GroupInfo{Name: name, About: strings.TrimSpace(about), Use: f.Uses}
	for _, l := range f.Lines {
		if l.Kind != Exclude {
			return nil, nil, fmt.Errorf("%s: a group only excludes", l)
		}
		info.Exclude = append(info.Exclude, l.Text)
	}
	rules := slices.Clone(f.Rules)
	for i := range rules {
		rules[i].Group = name
	}
	return info, rules, nil
}

// Groups returns the built-in groups, alphabetically
func Groups() []GroupInfo {
	return slices.Clone(loadGroups().list)
}

// GroupNames returns the names of the built-in groups, alphabetically,
// without loading them
func GroupNames() []string {
	return groupFileNames(filters.FS)
}

// Group returns the rules of a built-in group
func Group(name string) ([]Rule, error) {
	rules, ok := loadGroups().rules[name]
	if !ok {
		return nil, unknownGroup(name)
	}
	return slices.Clone(rules), nil
}

func unknownGroup(name string) error {
	return &Error{Code: "unknown-rules", Rule: name, Reason: "known groups: " + strings.Join(GroupNames(), ", ")}
}

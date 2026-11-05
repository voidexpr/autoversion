package filterrules

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/nicad/autoversion/filters"
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
	list  []GroupInfo       // the groups that load
	rules map[string][]Rule // a group's rules and its used groups', deduped
	errs  map[string]error  // a group that doesn't load: its file's error, or a used group's
}

// loadGroups loads the groups on first use. A package variable would be an
// initialization cycle: ParseFile checks use names against the groups. A
// group file that doesn't load fails only the filters using it, see Group.
var loadGroups = sync.OnceValue(func() *groupSet {
	s, _ := loadGroupFiles(filters.FS)
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
// its own (built-in Rules use nothing, shared spec). A group that doesn't
// load, or uses one that doesn't, is left out of the set with its error;
// the others load. The error returned joins those errors, nil when every
// group loads.
func loadGroupFiles(fsys fs.FS) (*groupSet, error) {
	s := &groupSet{rules: map[string][]Rule{}, errs: map[string]error{}}
	var infos []GroupInfo
	own := map[string][]Rule{}
	uses := map[string][]string{}
	names := groupFileNames(fsys)
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name+groupExt)
		if err != nil {
			s.errs[name] = err
			continue
		}
		info, rules, err := parseGroup(name, string(data))
		if err != nil {
			s.errs[name] = fmt.Errorf("filters/%s%s: %w", name, groupExt, err)
			continue
		}
		infos = append(infos, *info)
		own[name], uses[name] = rules, info.Use
	}

	var resolve func(name string, stack []string) ([]Rule, error)
	resolve = func(name string, stack []string) ([]Rule, error) {
		if err := s.errs[name]; err != nil {
			return nil, err
		}
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
				s.errs[name] = fmt.Errorf("filters/%s%s: use %s: %w", name, groupExt, used, err)
				return nil, s.errs[name]
			}
			all = append(all, rules...)
		}
		s.rules[name] = dedupe(all)
		return s.rules[name], nil
	}
	for _, g := range infos {
		if _, err := resolve(g.Name, nil); err == nil {
			s.list = append(s.list, g)
		}
	}
	var errs []error
	for _, name := range names {
		if err := s.errs[name]; err != nil {
			errs = append(errs, err)
		}
	}
	return s, errors.Join(errs...)
}

// group returns the rules of a group of the set
func (s *groupSet) group(name string) ([]Rule, error) {
	if err := s.errs[name]; err != nil {
		return nil, err
	}
	rules, ok := s.rules[name]
	if !ok {
		return nil, unknownGroup(name)
	}
	return slices.Clone(rules), nil
}

// parseGroup parses a group file: "# NAME: about" first, then a rules file
// with only exclude rules, no root, types or max-file-size (a filter's
// choice; the shared spec lets an implementation refuse them with
// bad-directive)
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
	for directive, value := range map[string]string{"root": f.Root, "types": f.Types, "max-file-size": f.MaxFileSize} {
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

// Groups returns the built-in groups that load, alphabetically. Group gives
// the error of one that doesn't.
func Groups() []GroupInfo {
	return slices.Clone(loadGroups().list)
}

// GroupNames returns the names of the built-in groups, alphabetically,
// without loading them
func GroupNames() []string {
	return groupFileNames(filters.FS)
}

// Group returns the rules of a built-in group. A group whose file doesn't
// load, or that uses one that doesn't, returns that file's error: New fails
// for the filters using it, and only for them.
func Group(name string) ([]Rule, error) {
	return loadGroups().group(name)
}

func unknownGroup(name string) error {
	return &Error{Code: "unknown-rules", Rule: name, Reason: "known groups: " + strings.Join(GroupNames(), ", ")}
}

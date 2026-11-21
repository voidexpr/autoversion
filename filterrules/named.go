package filterrules

import (
	"fmt"
	"slices"
	"strings"
)

// Resolver finds the filters a file may use by name, beside the built-in
// groups: a caller's filters, "dev/go" or "agent/secrets", a built-in group
// of the same name being shadowed. ParseFileWith and File.UseWith accept
// the names of Names; NewWith calls Lookup, which may parse, and resolves
// the use lines of the filter it returns the same way, so Lookup needs no
// care for nesting or cycles.
type Resolver interface {
	// Lookup returns the filter named name, nil when the name is not one of
	// Names (a built-in group then, or unknown-rules). The filter carries no
	// root, types or max-file-size: it is rules, used under the root of the
	// file using it; one that does is a bad-directive error.
	Lookup(name string) (*File, error)
	// Names lists the names Lookup knows, for the unknown-rules error
	Names() []string
}

// knownName reports whether a use line may name name: a filter of named,
// or a built-in group
func knownName(name string, named Resolver) bool {
	return named != nil && slices.Contains(named.Names(), name) || groupExists(name)
}

// unknownName is the error of a use line naming nothing: the known names,
// the caller's and the built-in groups
func unknownName(name string, named Resolver) error {
	known := GroupNames()
	if named != nil {
		known = append(known, named.Names()...)
		slices.Sort(known)
		known = slices.Compact(known)
	}
	return &Error{Code: "unknown-rules", Rule: name, Reason: "known: " + strings.Join(known, ", ")}
}

// notUsable is the error of a used filter that carries a filter's choice:
// root, types or max-file-size (the shared spec lets a used file be refused
// with bad-directive; applying rules written for one root under another is
// not what the user meant)
func notUsable(f *File) error {
	for _, d := range []struct{ word, value string }{{"root", f.Root}, {"types", f.Types}, {"max-file-size", f.MaxFileSize}} {
		if d.value != "" {
			return &Error{Code: "bad-directive", Rule: d.word + " " + d.value, Reason: "a filter's choice, not a used one's"}
		}
	}
	for _, l := range f.SizeLimits {
		return &Error{Code: "bad-directive", Rule: l.String(), Reason: "a filter's choice, not a used one's"}
	}
	return nil
}

// usedRules returns the rules of the filters names use, in order, each
// rule's Group being the filter it is written in: the filters of named,
// their own use lines resolved the same way, and the built-in groups for
// the other names. stack is the filters being resolved, for use-cycle.
// Every error names the filter it comes from: a cycle names its chain, a
// bad used filter is wrapped in "use NAME".
func usedRules(names []string, named Resolver, stack []string) ([]Rule, error) {
	var all []Rule
	for _, name := range names {
		rules, err := resolveName(name, named, stack)
		if err != nil {
			return nil, err
		}
		all = append(all, rules...)
	}
	return all, nil
}

func resolveName(name string, named Resolver, stack []string) ([]Rule, error) {
	if i := slices.Index(stack, name); i >= 0 {
		return nil, &Error{Code: "use-cycle", Rule: strings.Join(append(stack[i:], name), " -> "), Reason: "a filter uses itself"}
	}
	var f *File
	if named != nil {
		var err error
		if f, err = named.Lookup(name); err != nil {
			return nil, fmt.Errorf("use %s: %w", name, err)
		}
	}
	if f == nil {
		if !groupExists(name) {
			return nil, unknownName(name, named)
		}
		return Group(name)
	}
	if err := notUsable(f); err != nil {
		return nil, fmt.Errorf("use %s: %w", name, err)
	}
	rules := slices.Clone(f.Rules)
	for i := range rules {
		rules[i].Group = name
	}
	used, err := usedRules(f.Uses, named, append(stack, name))
	if err != nil {
		return nil, err
	}
	return append(rules, used...), nil
}

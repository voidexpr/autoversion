package filterrules

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Rules is a filter's combined rules, its own and its groups', ready to
// decide entries. Entry paths are relative to the root, with "/" between
// names and no leading or trailing "/". The root itself is "" and is always
// kept.
type Rules struct {
	types    string
	all      []Rule             // own rules first, then the groups'
	literal  map[string][]*Rule // literal rules by path
	wildcard []*Rule
	includes []*Rule
}

// New combines a file's rules with the groups it uses, and validates them
// (whole-root, contradiction).
func New(f *File) (*Rules, error) {
	all := slices.Clone(f.Rules)
	for _, name := range f.Uses {
		rules, err := Group(name)
		if err != nil {
			return nil, err
		}
		all = append(all, rules...)
	}
	r := compile(dedupe(all))
	r.types = f.Types
	if err := r.validate(); err != nil {
		return nil, err
	}
	return r, nil
}

func compile(all []Rule) *Rules {
	r := &Rules{all: all, literal: map[string][]*Rule{}}
	for i := range r.all {
		rule := &r.all[i]
		if rule.Literal() {
			r.literal[rule.Text[1:]] = append(r.literal[rule.Text[1:]], rule)
		} else {
			r.wildcard = append(r.wildcard, rule)
		}
		if rule.Kind == Include {
			r.includes = append(r.includes, rule)
		}
	}
	return r
}

func (r *Rules) validate() error {
	var errs []error
	for _, rule := range r.all {
		// A typed /* or /**/* keeps every folder (f:) or every file (d:)
		if rule.Kind != Exclude || rule.Type != TypeAny {
			continue
		}
		switch {
		case rule.Text == "/**/*":
			errs = append(errs, &Error{Code: "whole-root", Rule: rule.String(),
				Reason: "leaves out everything, even inside includes (exclude /* for an allowlist)"})
		case rule.Text == "/*" && len(r.includes) == 0:
			errs = append(errs, &Error{Code: "whole-root", Rule: rule.String(),
				Reason: "without any include, leaves out everything"})
		}
	}
	for _, inc := range r.includes {
		if !inc.Literal() {
			continue
		}
		for _, other := range r.literal[inc.Text[1:]] {
			if other.Kind == Exclude {
				errs = append(errs, &Error{Code: "contradiction", Rule: inc.Text,
					Reason: "both excluded and included: delete one"})
				break
			}
		}
	}
	return errors.Join(errs...)
}

// Decision is the result for one entry.
type Decision struct {
	Kept  bool
	Level int   // names in the path of the level that decided, 0 when no rule matched
	Rule  *Rule // the rule that decided, nil when no rule matched
}

// Decide decides an entry. The closest match wins: the entry itself is
// checked first, then each folder above it.
func (r *Rules) Decide(rel string, isDir bool) Decision {
	if rel == "" {
		return Decision{Kept: true}
	}
	names := strings.Split(rel, "/")
	end := len(rel)
	for d := len(names); d >= 1; d-- {
		if d < len(names) {
			end -= len(names[d]) + 1
		}
		if rule := r.decideLevel(names[:d], rel[:end], isDir || d < len(names)); rule != nil {
			return Decision{Kept: rule.Kind == Include, Level: d, Rule: rule}
		}
	}
	return Decision{Kept: true}
}

// decideLevel returns the rule that decides the level q (path qPath), or nil
// when no rule matches it. A literal rule beats a wildcard rule, then an
// include beats an exclude.
func (r *Rules) decideLevel(q []string, qPath string, isDir bool) *Rule {
	var exclude *Rule
	for _, rule := range r.literal[qPath] {
		if rule.fits(isDir) {
			if rule.Kind == Include {
				return rule
			}
			exclude = rule
		}
	}
	if exclude != nil {
		return exclude
	}
	for _, rule := range r.wildcard {
		if rule.fits(isDir) && rule.match(q) {
			if rule.Kind == Include {
				return rule
			}
			if exclude == nil {
				exclude = rule
			}
		}
	}
	return exclude
}

// CanMatchBelow reports whether an include can match an entry below the
// folder relDir
func (r *Rules) CanMatchBelow(relDir string) bool {
	dir := splitPath(relDir)
	for _, rule := range r.includes {
		if rule.canMatchBelow(dir) {
			return true
		}
	}
	return false
}

// SkipDir reports whether a walker can skip the folder relDir: it is left
// out, and no include can match below it
func (r *Rules) SkipDir(relDir string) bool {
	return !r.Decide(relDir, true).Kept && !r.CanMatchBelow(relDir)
}

func splitPath(rel string) []string {
	if rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}

// Problem is a warning found by Check, or an error when Error is set.
type Problem struct {
	Code    string
	Message string
	Error   bool
}

func (p Problem) String() string {
	level := "warning"
	if p.Error {
		level = "error"
	}
	return fmt.Sprintf("%s: %s: %s", level, p.Code, p.Message)
}

// ListFunc returns the names in a folder of the tree, "" being the root.
type ListFunc func(rel string) ([]string, error)

// Check returns what the rules can't tell on their own: non-ASCII rules,
// includes that change nothing, literal rules that name no entry of the tree
// (dead rules), and an allowlist with nothing alive (an error). Entries are
// looked up in listings, byte for byte: a case-insensitive file system would
// say that Library/caches exists. An entry under a folder that can't be
// listed counts as alive: unknown is not dead.
func (r *Rules) Check(list ListFunc) []Problem {
	var problems []Problem
	for _, rule := range r.all {
		if strings.ContainsFunc(rule.Text, func(c rune) bool { return c > 127 }) {
			problems = append(problems, Problem{Code: "non-ascii",
				Message: rule.String() + ": a name stored decomposed (NFD) on disk doesn't match it"})
		}
	}
	problems = append(problems, r.changesNothing()...)

	listings := map[string]map[string]bool{}
	exists := func(rel string) bool {
		path := strings.Split(rel, "/")
		for i, name := range path {
			dir := strings.Join(path[:i], "/")
			names, ok := listings[dir]
			if !ok {
				if entries, err := list(dir); err == nil {
					names = map[string]bool{}
					for _, e := range entries {
						names[e] = true
					}
				}
				listings[dir] = names
			}
			if names == nil {
				return true
			}
			if !names[name] {
				return false
			}
		}
		return true
	}
	allowlist, literalInclude, aliveInclude, wildcardInclude := false, false, false, false
	for i := range r.all {
		rule := &r.all[i]
		if rule.Kind == Exclude && rule.Type == TypeAny && rule.Text == "/*" {
			allowlist = true
		}
		if !rule.Literal() {
			wildcardInclude = wildcardInclude || rule.Kind == Include
			continue
		}
		alive := exists(rule.Text[1:])
		if !alive {
			problems = append(problems, Problem{Code: "dead-rule",
				Message: rule.String() + ": no such entry under the root"})
		}
		if rule.Kind == Include {
			literalInclude = true
			aliveInclude = aliveInclude || alive
		}
	}
	if allowlist && literalInclude && !aliveInclude && !wildcardInclude {
		problems = append(problems, Problem{Code: "whole-root", Error: true,
			Message: "exclude /* and none of the includes exists: nothing would be kept"})
	}
	return problems
}

// changesNothing returns the "changes nothing" warnings, a single one when
// no include changes anything
func (r *Rules) changesNothing() []Problem {
	var useless []*Rule
	for _, inc := range r.includes {
		if r.includeChangesNothing(inc) {
			useless = append(useless, inc)
		}
	}
	if len(useless) > 0 && len(useless) == len(r.includes) {
		return []Problem{{Code: "changes-nothing",
			Message: "no include changes anything: did you mean to add exclude /*?"}}
	}
	var problems []Problem
	for _, inc := range useless {
		problems = append(problems, Problem{Code: "changes-nothing", Message: inc.String() + " changes nothing"})
	}
	return problems
}

// includeChangesNothing reports whether a literal include's own path is kept
// without it, for each type the include applies to. For a wildcard include
// the check is approximate (shared spec): no exclude with "**", and no
// exclude as deep as it or less.
func (r *Rules) includeChangesNothing(inc *Rule) bool {
	var others []Rule
	for i := range r.all {
		if &r.all[i] != inc {
			others = append(others, r.all[i])
		}
	}
	if !inc.Literal() {
		for _, rule := range others {
			if rule.Kind == Exclude && (rule.star >= 0 || len(rule.segments) <= len(inc.segments)) {
				return false
			}
		}
		return true
	}
	without := compile(others)
	rel := inc.Text[1:]
	keptAs := func(isDir bool) bool { return without.Decide(rel, isDir).Kept }
	switch inc.Type {
	case TypeFile:
		return keptAs(false)
	case TypeDir:
		return keptAs(true)
	}
	return keptAs(false) && keptAs(true)
}

// Dump returns the expanded rules, groups included, as a rules file
func (r *Rules) Dump() string {
	var b strings.Builder
	b.WriteString("version 1\n")
	if r.types != "" {
		fmt.Fprintf(&b, "types %s\n", r.types)
	}
	for i, rule := range r.all {
		if i == 0 || rule.Group != r.all[i-1].Group {
			if rule.Group == "" {
				b.WriteString("\n# own rules\n")
			} else {
				fmt.Fprintf(&b, "\n# group %s\n", rule.Group)
			}
		}
		b.WriteString(rule.String() + "\n")
	}
	return b.String()
}

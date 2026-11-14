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
//
// Nothing changes a Rules after New: it is safe for concurrent use, and
// several walks can share one. Each walk of a tree with marker rules decides
// with its own copy, see WithMarkers. The *Rule values that List and
// Decision.Rule return are shared by every copy: read only.
type Rules struct {
	types       string
	maxFileSize string    // as written, "" without the directive
	maxBytes    int64     // 0: no limit
	all         []Rule    // own rules first, then the groups'
	paths       ruleIndex // the path rules of all, markers aside
	includes    []*Rule
	markers     []*Rule // exclude marker:NAME rules
	xattrs      []*Rule // exclude xattr:NAME rules
	sizeLimits  []Rule  // the scoped max-file-size lines, expanded
	sizes       ruleIndex

	// markerExists reports whether the folder relDir directly holds an
	// entry named name; nil (markers never match) except in a WithMarkers copy
	markerExists func(relDir, name string) bool
	// xattrExists reports whether the entry rel carries the extended
	// attribute name; nil (attribute rules never match) except in a
	// WithXattrs copy
	xattrExists func(rel, name string) bool
}

// ruleIndex files rules by what decideLevel tries at a level
type ruleIndex struct {
	literal  map[string][]*Rule // literal rules by path
	byName   map[string][]*Rule // wildcard rules by the name a level must have: /**/node_modules
	byExt    map[string][]*Rule // wildcard rules by the extension a level must have: f:/**/*.log
	wildcard []*Rule            // the other wildcard rules, tried at each level
}

func newRuleIndex() ruleIndex {
	return ruleIndex{literal: map[string][]*Rule{}, byName: map[string][]*Rule{}, byExt: map[string][]*Rule{}}
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
	if f.MaxFileSize != "" {
		bytes, err := ParseSize(f.MaxFileSize)
		if err != nil {
			return nil, &Error{Code: "bad-directive", Rule: "max-file-size " + f.MaxFileSize, Reason: err.Error()}
		}
		r.maxFileSize, r.maxBytes = f.MaxFileSize, bytes
	}
	for _, l := range f.SizeLimits {
		bytes, err := ParseSize(l.Size)
		if err != nil {
			return nil, &Error{Code: "bad-directive", Rule: l.String(), Reason: err.Error()}
		}
		rules, err := parseSizeRule(l.Rule)
		if err != nil {
			return nil, err
		}
		for _, rule := range rules {
			rule.size, rule.sizeBytes = l.Size, bytes
			r.sizeLimits = append(r.sizeLimits, rule)
		}
	}
	r.sizes = newRuleIndex()
	for i := range r.sizeLimits {
		r.sizeLimits[i].pos = i
		r.sizes.add(&r.sizeLimits[i])
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// MaxFileSize returns the default size limit: the bare max-file-size
// directive as written ("" without one) and in bytes (0: no limit). It
// applies after the rules, to kept non-folders only, where no scoped line
// does, see Limit and TooLarge.
func (r *Rules) MaxFileSize() (text string, bytes int64) {
	return r.maxFileSize, r.maxBytes
}

// SizeLimits returns the scoped max-file-size lines, expanded
func (r *Rules) SizeLimits() []*Rule {
	limits := make([]*Rule, len(r.sizeLimits))
	for i := range r.sizeLimits {
		limits[i] = &r.sizeLimits[i]
	}
	return limits
}

// Limit returns the size limit of a kept non-folder entry: the scoped
// max-file-size line that decides it, nil for the default. The closest
// match wins, the depth being the level its rule matched at; on one level
// the tiers of decideLevel apply, and lines still tied give the strictest
// limit, none being the loosest.
func (r *Rules) Limit(rel string) *Rule {
	if len(r.sizeLimits) == 0 || rel == "" {
		return nil
	}
	names := strings.Split(rel, "/")
	end := len(rel)
	for d := len(names); d >= 1; d-- {
		if d < len(names) {
			end -= len(names[d]) + 1
		}
		if rule := r.sizes.limitLevel(names[:d], rel[:end], d < len(names)); rule != nil {
			return rule
		}
	}
	return nil
}

// TooLarge reports whether a kept non-folder entry is left out by its size
// limit (see Limit): it is larger than it (an entry of exactly that size is
// kept)
func (r *Rules) TooLarge(rel string, size int64) bool {
	limit := r.maxBytes
	if rule := r.Limit(rel); rule != nil {
		limit = rule.sizeBytes
	}
	return limit > 0 && size > limit
}

// limitLevel returns the scoped line that decides the level q (path qPath):
// of the first tier with a match, the strictest
func (x *ruleIndex) limitLevel(q []string, qPath string, isDir bool) *Rule {
	strictest := func(rules ...[]*Rule) *Rule {
		var best *Rule
		for _, rules := range rules {
			for _, rule := range rules {
				if rule.fits(isDir) && rule.match(q) && (best == nil || best.sizeBytes == 0 || rule.sizeBytes > 0 && rule.sizeBytes < best.sizeBytes) {
					best = rule
				}
			}
		}
		return best
	}
	name := q[len(q)-1]
	if rule := strictest(x.literal[qPath]); rule != nil {
		return rule
	}
	if rule := strictest(x.byName[name]); rule != nil {
		return rule
	}
	return strictest(x.byExt[extension(name)], x.wildcard)
}

func compile(all []Rule) *Rules {
	r := &Rules{all: all, paths: newRuleIndex()}
	for i := range r.all {
		rule := &r.all[i]
		rule.pos = i
		switch {
		case rule.Marker != "":
			r.markers = append(r.markers, rule)
			continue
		case rule.Xattr != "":
			r.xattrs = append(r.xattrs, rule)
			continue
		}
		r.paths.add(rule)
		if rule.Kind == Include {
			r.includes = append(r.includes, rule)
		}
	}
	return r
}

// add files a path rule: a literal one by its path, a wildcard one by what
// its last name requires of an entry's name: being NAME, or ending with
// .EXT (*.EXT, no "." in EXT, which is then the name's last "." on).
// decideLevel only tries the rules filed under the path, the name and the
// extension of a level, and the others.
func (x *ruleIndex) add(rule *Rule) {
	if rule.Literal() {
		x.literal[rule.Text[1:]] = append(x.literal[rule.Text[1:]], rule)
		return
	}
	last := rule.segments[len(rule.segments)-1]
	switch {
	case last.parts == nil:
		x.byName[last.text] = append(x.byName[last.text], rule)
	case len(last.parts) == 2 && last.parts[0] == "" && strings.LastIndexByte(last.parts[1], '.') == 0:
		x.byExt[last.parts[1]] = append(x.byExt[last.parts[1]], rule)
	default:
		x.wildcard = append(x.wildcard, rule)
	}
}

// extension returns a name from its last "." on, "" without a "."
func extension(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i:]
	}
	return ""
}

// List returns the combined rules, the filter's own first, then its groups',
// duplicates removed. Decide's Decision.Rule points into this list.
func (r *Rules) List() []*Rule {
	list := make([]*Rule, len(r.all))
	for i := range r.all {
		list[i] = &r.all[i]
	}
	return list
}

// WithMarkers returns a copy of r that decides with exists as the tree
// lookup its marker rules need: exists reports whether the folder relDir
// ("" for the root) directly holds an entry named name. The copy shares the
// compiled rules, and r is unchanged. Without a lookup markers never match:
// Markers says whether a filter has any.
//
// exists runs on the goroutine that calls Decide or SkipDir: when several
// goroutines decide with the same copy, it must be safe for concurrent use.
func (r *Rules) WithMarkers(exists func(relDir, name string) bool) *Rules {
	c := *r
	c.markerExists = exists
	return &c
}

// WithXattrs returns a copy that decides exclude xattr:NAME rules with
// exists, which says whether the entry rel carries the attribute name. The
// same conditions as WithMarkers apply.
func (r *Rules) WithXattrs(exists func(rel, name string) bool) *Rules {
	c := *r
	c.xattrExists = exists
	return &c
}

// Xattrs returns the attribute names of the xattr rules (exclude xattr:NAME)
func (r *Rules) Xattrs() []string {
	var names []string
	for _, x := range r.xattrs {
		names = append(names, x.Xattr)
	}
	return names
}

// attrBy returns the xattr rule that leaves out the entry rel: it carries
// the attribute
func (r *Rules) attrBy(rel string) *Rule {
	if r.xattrExists == nil {
		return nil
	}
	for _, x := range r.xattrs {
		if r.xattrExists(rel, x.Xattr) {
			return x
		}
	}
	return nil
}

// Markers returns the entry names of the marker rules (exclude marker:NAME)
func (r *Rules) Markers() []string {
	var names []string
	for _, m := range r.markers {
		names = append(names, m.Marker)
	}
	return names
}

// markedBy returns the marker rule that leaves out the folder relDir: it
// holds the marker's entry. A marker named except doesn't count: the
// marker entry itself is decided as if its folder had no such marker.
func (r *Rules) markedBy(relDir, except string) *Rule {
	if r.markerExists == nil {
		return nil
	}
	for _, m := range r.markers {
		if m.Marker != except && r.markerExists(relDir, m.Marker) {
			return m
		}
	}
	return nil
}

// isMarkerName reports whether an entry named name is a marker entry
func (r *Rules) isMarkerName(name string) bool {
	for _, m := range r.markers {
		if m.Marker == name {
			return true
		}
	}
	return false
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
		for _, other := range r.paths.literal[inc.Text[1:]] {
			if other.Kind == Exclude {
				errs = append(errs, &Error{Code: "contradiction", Rule: inc.Text,
					Reason: "both excluded and included: delete one"})
				break
			}
		}
	}
	// Two literal scoped limits on the same path with different sizes
	for _, rules := range r.sizes.literal {
		for i, a := range rules {
			for _, b := range rules[:i] {
				if a.Type == b.Type && a.sizeBytes != b.sizeBytes {
					errs = append(errs, &Error{Code: "contradiction", Rule: a.String(),
						Reason: "two limits on the same path (" + b.String() + "): delete one"})
				}
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
// checked first, then each folder above it. A folder holding a marker entry
// (see WithMarkers), or an entry carrying an attribute (see WithXattrs),
// counts as a named wildcard exclude at its own level (see decideLevel),
// except for the marker entry itself, decided as if its folder had no
// marker.
func (r *Rules) Decide(rel string, isDir bool) Decision {
	if rel == "" {
		return Decision{Kept: true}
	}
	names := strings.Split(rel, "/")
	except := ""
	if r.isMarkerName(names[len(names)-1]) {
		except = names[len(names)-1]
	}
	end := len(rel)
	for d := len(names); d >= 1; d-- {
		if d < len(names) {
			end -= len(names[d]) + 1
		}
		levelIsDir := isDir || d < len(names)
		var marked func() *Rule
		if levelIsDir && len(r.markers) > 0 || len(r.xattrs) > 0 {
			path, levelExcept := rel[:end], ""
			if d == len(names)-1 {
				levelExcept = except
			}
			marked = func() *Rule {
				if levelIsDir {
					if m := r.markedBy(path, levelExcept); m != nil {
						return m
					}
				}
				return r.attrBy(path)
			}
		}
		if rule := r.decideLevel(names[:d], rel[:end], levelIsDir, marked); rule != nil {
			return Decision{Kept: rule.Kind == Include, Level: d, Rule: rule}
		}
	}
	return Decision{Kept: true}
}

// decideLevel returns the rule that decides the level q (path qPath), or nil
// when no rule matches it. A literal rule beats a wildcard rule, and a named
// wildcard (/**/.cache) beats the other wildcards (/.*, /**/*.log). The
// folder's marker (marked, nil without markers) is a named wildcard exclude:
// a named include ties with it and wins. In a tier an include beats an
// exclude, then the first rule in order wins.
func (r *Rules) decideLevel(q []string, qPath string, isDir bool, marked func() *Rule) *Rule {
	var exclude *Rule
	for _, rule := range r.paths.literal[qPath] {
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
	name := q[len(q)-1]
	if rule := r.pick(q, isDir, r.paths.byName[name]); rule != nil {
		return rule
	}
	if marked != nil {
		if m := marked(); m != nil {
			return m
		}
	}
	return r.pick(q, isDir, r.paths.byExt[extension(name)], r.paths.wildcard)
}

// pick returns the rule of a tier that decides the level q: its first
// include that matches, else its first exclude, nil when none matches
func (r *Rules) pick(q []string, isDir bool, tier ...[]*Rule) *Rule {
	var include, exclude *Rule
	for _, rules := range tier {
		for _, rule := range rules {
			if rule.Kind == Include {
				if (include == nil || rule.pos < include.pos) && rule.fits(isDir) && rule.match(q) {
					include = rule
				}
			} else if include == nil && (exclude == nil || rule.pos < exclude.pos) && rule.fits(isDir) && rule.match(q) {
				exclude = rule
			}
		}
	}
	if include != nil {
		return include
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
// out, no include can match below it, and it holds no marker entry that is
// kept (a marked folder is stored bare with its marker)
func (r *Rules) SkipDir(relDir string) bool {
	if r.Decide(relDir, true).Kept || r.CanMatchBelow(relDir) {
		return false
	}
	for _, m := range r.markers {
		if r.markerExists != nil && r.markerExists(relDir, m.Marker) && r.Decide(relDir+"/"+m.Marker, false).Kept {
			return false
		}
	}
	return true
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
	// Markers on disk, for restic and borg (shared spec): a marker in the
	// root leaves out everything there, and an include at or under a marked
	// folder is never reached. The native matcher keeps the closest match.
	holdsMarker := func(dir string) string {
		exists(dir + "/x") // fills the listing of dir
		for _, m := range r.markers {
			if listings[dir][m.Marker] {
				return m.Marker
			}
		}
		return ""
	}
	if len(r.markers) > 0 {
		if m := holdsMarker(""); m != "" {
			problems = append(problems, Problem{Code: "root-marked",
				Message: "the root holds " + m + ": restic and borg would leave out everything (the native matcher ignores a marker in the root)"})
		}
		for _, inc := range r.includes {
			if !inc.Literal() {
				continue
			}
			names := strings.Split(inc.Text[1:], "/")
			for i := range names {
				dir := strings.Join(names[:i+1], "/")
				if m := holdsMarker(dir); m != "" {
					problems = append(problems, Problem{Code: "include-under-marker",
						Message: inc.String() + ": " + dir + " holds " + m + ", restic and borg never reach the include"})
					break
				}
			}
		}
	}

	for i := range r.sizeLimits {
		rule := &r.sizeLimits[i]
		if rule.Literal() && !exists(rule.Text[1:]) {
			problems = append(problems, Problem{Code: "dead-rule",
				Message: rule.String() + ": no such entry under the root"})
		}
	}
	allowlist, literalInclude, aliveInclude, wildcardInclude := false, false, false, false
	for i := range r.all {
		rule := &r.all[i]
		if rule.Marker != "" {
			continue
		}
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
	if r.maxFileSize != "" {
		fmt.Fprintf(&b, "max-file-size %s\n", r.maxFileSize)
	}
	for _, rule := range r.sizeLimits {
		b.WriteString(rule.String() + "\n")
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

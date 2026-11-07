// Package filterrules reads include and exclude rules in the shared filter
// syntax (ownit's plans/spec_filter_syntax.md, v3) and decides which entries
// of a tree they keep. It uses the standard library only.
package filterrules

import (
	"fmt"
	"slices"
	"strings"
)

// Kind says whether a rule leaves entries out or keeps them.
type Kind int

const (
	Exclude Kind = iota
	Include
)

func (k Kind) String() string {
	if k == Include {
		return "include"
	}
	return "exclude"
}

// Type limits the entries a rule matches.
type Type int

const (
	TypeAny  Type = iota // no prefix: files and folders
	TypeFile             // "f:": anything that is not a folder
	TypeDir              // "d:": folders only
)

func (t Type) prefix() string {
	switch t {
	case TypeFile:
		return "f:"
	case TypeDir:
		return "d:"
	}
	return ""
}

// Rule is one include or exclude, after sugar expansion.
type Rule struct {
	Kind   Kind
	Type   Type
	Text   string // without the type prefix: "/**/*.old", or "marker:NAME"
	Group  string // the built-in group it comes from, "" for a filter's own rules
	Marker string // for "exclude marker:NAME", the entry name; "" for a path rule

	segments []pattern
	star     int // position of the "**" segment, -1 without one
	pos      int // index in the combined rules, for their order

	// For a scoped max-file-size line (see Rules.Limit): the size as written
	// and in bytes (0: none). "" for a path rule.
	size      string
	sizeBytes int64
}

// Size returns a scoped max-file-size rule's limit: as written and in bytes
// (0: none). "" for a path rule.
func (r Rule) Size() (text string, bytes int64) {
	return r.size, r.sizeBytes
}

// pattern is one name of a rule: a literal, or a glob cut at its "*"
type pattern struct {
	text  string
	parts []string // text split at "*", nil without a "*"
}

func newPattern(text string) pattern {
	p := pattern{text: text}
	if strings.Contains(text, "*") {
		p.parts = strings.Split(text, "*")
	}
	return p
}

// match matches one name: "*" matches any run of characters, possibly
// empty. Without "?" or "[", finding each part between two stars at its
// leftmost place is enough, and it is what path.Match computes.
func (p pattern) match(name string) bool {
	if p.parts == nil {
		return p.text == name
	}
	first, last := p.parts[0], p.parts[len(p.parts)-1]
	if !strings.HasPrefix(name, first) {
		return false
	}
	name = name[len(first):]
	for _, mid := range p.parts[1 : len(p.parts)-1] {
		i := strings.Index(name, mid)
		if i < 0 {
			return false
		}
		name = name[i+len(mid):]
	}
	return strings.HasSuffix(name, last)
}

func (r Rule) String() string {
	if r.size != "" {
		return "max-file-size " + r.size + " " + r.Type.prefix() + r.Text
	}
	return r.Kind.String() + " " + r.Type.prefix() + r.Text
}

// Literal reports whether the rule has no "*": it names one path. A marker
// rule names no path.
func (r Rule) Literal() bool {
	return r.Marker == "" && !strings.Contains(r.Text, "*")
}

// Named reports whether a wildcard rule's last name is plain (/**/.cache,
// /*/node_modules): it names the entries it matches, unlike /.* or /**/*.log.
func (r Rule) Named() bool {
	return r.Marker == "" && !r.Literal() && r.segments[len(r.segments)-1].parts == nil
}

func (r *Rule) fits(isDir bool) bool {
	return r.Type == TypeAny || (r.Type == TypeDir) == isDir
}

// match reports whether the rule matches the entry path p, given as names
func (r *Rule) match(p []string) bool {
	if r.star < 0 {
		return len(p) == len(r.segments) && globAll(r.segments, p)
	}
	prefix, suffix := r.segments[:r.star], r.segments[r.star+1:]
	return len(p) >= len(prefix)+len(suffix) &&
		globAll(prefix, p[:len(prefix)]) && globAll(suffix, p[len(p)-len(suffix):])
}

// canMatchBelow reports whether the rule can match an entry below the folder
// dir (the spec's condition for walkers)
func (r *Rule) canMatchBelow(dir []string) bool {
	m := len(dir)
	if r.star < 0 {
		return len(r.segments) > m && globAll(r.segments[:m], dir)
	}
	if r.star <= m {
		return globAll(r.segments[:r.star], dir[:r.star])
	}
	return globAll(r.segments[:m], dir)
}

func globAll(patterns []pattern, names []string) bool {
	for i, p := range patterns {
		if !p.match(names[i]) {
			return false
		}
	}
	return true
}

// Error is a rule or a rules file refused by the parser, or rules refused by
// New. Code is the shared spec's stable code.
type Error struct {
	Code   string
	Rule   string // the rule or line as written
	Reason string
	Line   int // line in a rules file, 0 otherwise
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s: %q: %s", e.Code, e.Rule, e.Reason)
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, msg)
	}
	return msg
}

// ParseRule parses one rule with its sugar: a type prefix, {a,b} braces and
// a "!" exception. One rule as written can give several rules.
func ParseRule(kind Kind, text string) ([]Rule, error) {
	rules, err := parseSugar(kind, text)
	if e, ok := err.(*Error); ok {
		e.Rule = text
	}
	return rules, err
}

func parseSugar(kind Kind, text string) ([]Rule, error) {
	if name, ok := strings.CutPrefix(text, "marker:"); ok {
		return parseMarkers(kind, name)
	}
	typ := TypeAny
	if len(text) >= 2 && text[1] == ':' && isLetter(text[0]) {
		switch text[0] {
		case 'f':
			typ = TypeFile
		case 'd':
			typ = TypeDir
		default:
			return nil, ruleError("bad-type", "the type is f: or d:")
		}
		text = text[2:]
	}
	expanded, err := expandBraces(text)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	for _, text := range expanded {
		split, err := splitExcept(kind, typ, text)
		if err != nil {
			return nil, err
		}
		rules = append(rules, split...)
	}
	return dedupe(rules), nil
}

// parseMarkers parses "exclude marker:NAME" (NAME with braces): a folder
// that directly holds an entry named NAME is left out, stored bare with the
// entry. NAME is one entry name: no "/" or "*".
func parseMarkers(kind Kind, text string) ([]Rule, error) {
	names, err := expandBraces(text)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	for _, name := range names {
		switch {
		case name == "" || strings.ContainsAny(name, "/*"):
			return nil, ruleError("bad-marker", "one entry name: marker:CACHEDIR.TAG")
		case name == "." || name == "..":
			return nil, ruleError("dot-segment", ". and .. are not allowed")
		case name != strings.Trim(name, " "):
			return nil, ruleError("space-edge", "a name doesn't start or end with a space")
		case strings.ContainsFunc(name, isForbidden):
			return nil, ruleError("forbidden-char", "not allowed: ? [ ] { } \\ $ ! and control characters")
		}
		rules = append(rules, Rule{Kind: Exclude, Text: "marker:" + name, Marker: name, star: -1})
	}
	if kind != Exclude {
		return nil, ruleError("bad-marker", "a marker is an exclude")
	}
	return dedupe(rules), nil
}

func isLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func ruleError(code, reason string) *Error {
	return &Error{Code: code, Reason: reason}
}

// expandBraces expands {a,b} like the shell, one level, no nesting
func expandBraces(text string) ([]string, error) {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		if strings.Contains(text, "}") {
			return nil, ruleError("bad-brace", "unmatched }")
		}
		return []string{text}, nil
	}
	end := strings.IndexByte(text[start:], '}')
	if end < 0 {
		return nil, ruleError("bad-brace", "unmatched {")
	}
	end += start
	inner := text[start+1 : end]
	if strings.Contains(inner, "{") {
		return nil, ruleError("bad-brace", "braces don't nest")
	}
	alternatives := strings.Split(inner, ",")
	if slices.Contains(alternatives, "") {
		return nil, ruleError("bad-brace", "empty alternative")
	}
	if strings.Contains(inner, "!") {
		return nil, ruleError("bad-except", "no ! inside braces")
	}
	tails, err := expandBraces(text[end+1:])
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range alternatives {
		for _, tail := range tails {
			out = append(out, text[:start]+a+tail)
		}
	}
	return out, nil
}

// splitExcept expands P/!Q. In an include: include P, exclude P/Q. In an
// exclude: exclude P/*, include P/Q. P may be the root.
func splitExcept(kind Kind, typ Type, text string) ([]Rule, error) {
	i := strings.IndexByte(text, '!')
	if i < 0 {
		rule, err := parseRule(kind, typ, text)
		if err != nil {
			return nil, err
		}
		return []Rule{rule}, nil
	}
	switch {
	case typ != TypeAny:
		return nil, ruleError("bad-except", "no ! on a typed rule: write the exception as its own rule")
	case strings.Contains(text[i+1:], "!"):
		return nil, ruleError("bad-except", "one ! per rule")
	case i == 0 || text[i-1] != '/':
		return nil, ruleError("bad-except", "! starts a name")
	case i == len(text)-1:
		return nil, ruleError("bad-except", "nothing after !")
	}
	prefix, exception := text[:i-1], text[:i-1]+"/"+text[i+1:]
	lines := []Line{{Exclude, prefix + "/*"}, {Include, exception}}
	if kind == Include {
		// "/" alone is not a rule: an include of the root gives no include
		lines = nil
		if prefix != "" {
			lines = append(lines, Line{Include, prefix})
		}
		lines = append(lines, Line{Exclude, exception})
	}
	var rules []Rule
	for _, l := range lines {
		rule, err := parseRule(l.Kind, TypeAny, l.Text)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

const forbiddenChars = "?[]{}\\$!"

// parseRule parses one rule without sugar
func parseRule(kind Kind, typ Type, text string) (Rule, error) {
	if !strings.HasPrefix(text, "/") {
		return Rule{}, ruleError("no-root", "a rule starts with / (/**/NAME for any depth)")
	}
	if strings.Contains(text, " #") || strings.Contains(text, "\t#") {
		return Rule{}, ruleError("comment-in-rule", "a space followed by # starts a comment")
	}
	if strings.HasSuffix(text, "/") {
		return Rule{}, ruleError("trailing-slash", "no trailing / (/NAME/* for a folder's content)")
	}
	names := strings.Split(text[1:], "/")
	var stars []int
	segments := make([]pattern, len(names))
	for i, s := range names {
		segments[i] = newPattern(s)
		switch {
		case s == "":
			return Rule{}, ruleError("empty-segment", "empty name between two /")
		case s == "." || s == "..":
			return Rule{}, ruleError("dot-segment", ". and .. are not allowed")
		case s != strings.Trim(s, " "):
			return Rule{}, ruleError("space-edge", "a name doesn't start or end with a space")
		case s == "**":
			stars = append(stars, i)
		case strings.Contains(s, "**"):
			return Rule{}, ruleError("star-run", "** is a whole name; two * in a row are not allowed")
		case strings.ContainsFunc(s, isForbidden):
			return Rule{}, ruleError("forbidden-char", "not allowed: ? [ ] { } \\ $ ! and control characters")
		}
	}
	star := -1
	if len(stars) > 0 {
		if stars[len(stars)-1] == len(segments)-1 {
			return Rule{}, ruleError("double-star-last", "** is never last (/NAME or /NAME/*)")
		}
		if len(stars) > 1 {
			return Rule{}, ruleError("double-star-twice", "** at most once")
		}
		star = stars[0]
	}
	return Rule{Kind: kind, Type: typ, Text: text, segments: segments, star: star}, nil
}

func isForbidden(c rune) bool {
	return strings.ContainsRune(forbiddenChars, c) || c < 32 || c == 0x7f
}

type ruleKey struct {
	kind Kind
	typ  Type
	text string
}

// dedupe keeps the first of equal rules
func dedupe(rules []Rule) []Rule {
	seen := map[ruleKey]bool{}
	var out []Rule
	for _, r := range rules {
		key := ruleKey{r.Kind, r.Type, r.Text}
		if !seen[key] {
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}

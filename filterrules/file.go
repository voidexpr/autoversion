package filterrules

import (
	"bufio"
	"io"
	"regexp"
	"slices"
	"strings"
)

// File is a rules file: its directives and rule lines. Without a root line
// it applies to a root given by the caller.
type File struct {
	Root        string      // as written ("~", "~/x" or "/x"), "" without a root line
	Types       string      // "strict" or "decay" as written, "" without a types line (strict)
	MaxFileSize string      // as written ("10M", "none"), "" without a max-file-size line (no limit), see ParseSize
	SizeLimits  []SizeLimit // the scoped max-file-size lines, as written
	Uses        []string
	Lines       []Line // rule lines as written
	Rules       []Rule // Lines expanded
}

// SizeLimit is a scoped max-file-size line, "max-file-size SIZE RULE": the
// limit for what RULE covers, see Rules.Limit.
type SizeLimit struct {
	Size string // as written ("1G", "none")
	Rule string // a path rule as written, sugar included
}

func (l SizeLimit) String() string {
	return "max-file-size " + l.Size + " " + l.Rule
}

// AddSizeLimit adds a scoped max-file-size line. RULE is a path rule: no
// marker, no "!" exception.
func (f *File) AddSizeLimit(size, rule string) error {
	if _, err := ParseSize(size); err != nil {
		return &Error{Code: "bad-directive", Rule: "max-file-size " + size + " " + rule, Reason: "max-file-size: " + err.Error()}
	}
	if _, err := parseSizeRule(rule); err != nil {
		return err
	}
	f.SizeLimits = append(f.SizeLimits, SizeLimit{size, rule})
	return nil
}

// parseSizeRule parses the RULE of a scoped max-file-size line
func parseSizeRule(text string) ([]Rule, error) {
	fail := func(reason string) error {
		return &Error{Code: "bad-directive", Rule: "max-file-size", Reason: "max-file-size: " + reason}
	}
	switch {
	case strings.HasPrefix(text, "marker:"):
		return nil, fail("a path rule, not a marker")
	case strings.Contains(text, "!"):
		return nil, fail("no ! in a scoped limit: name the exception on its own line")
	}
	rules, err := ParseRule(Exclude, text)
	if err != nil {
		return nil, fail(err.Error())
	}
	return rules, nil
}

// Line is a rule as written, sugar included.
type Line struct {
	Kind Kind
	Text string
}

func (l Line) String() string {
	return l.Kind.String() + " " + l.Text
}

// Add adds a rule as written, e.g. from a command line flag
func (f *File) Add(kind Kind, text string) error {
	rules, err := ParseRule(kind, text)
	if err != nil {
		return err
	}
	f.Lines = append(f.Lines, Line{kind, text})
	f.Rules = append(f.Rules, rules...)
	return nil
}

// Use adds a built-in group
func (f *File) Use(name string) error {
	return f.UseWith(name, nil)
}

// UseWith adds a filter of named, or a built-in group
func (f *File) UseWith(name string, named Resolver) error {
	if !knownName(name, named) {
		return unknownName(name, named)
	}
	if !slices.Contains(f.Uses, name) {
		f.Uses = append(f.Uses, name)
	}
	return nil
}

// Format writes the file in the rules file format: the directives, then one
// rule per line as written. Comments are not kept.
func (f *File) Format() string {
	var b strings.Builder
	b.WriteString("version 1\n")
	if f.Root != "" {
		b.WriteString("root " + f.Root + "\n")
	}
	if f.Types != "" {
		b.WriteString("types " + f.Types + "\n")
	}
	if f.MaxFileSize != "" {
		b.WriteString("max-file-size " + f.MaxFileSize + "\n")
	}
	for _, l := range f.SizeLimits {
		b.WriteString(l.String() + "\n")
	}
	for _, name := range f.Uses {
		b.WriteString("use " + name + "\n")
	}
	if len(f.Lines) > 0 {
		b.WriteString("\n")
	}
	for _, l := range f.Lines {
		b.WriteString(l.String() + "\n")
	}
	return b.String()
}

var directiveWord = regexp.MustCompile(`^[a-z][a-z-]*$`)

// ParseFile reads a rules file: "version 1" first, then the directives
// (root, types, max-file-size, use), then the rule lines ("exclude RULE",
// "include RULE", or a bare RULE, which is an include). Line order means
// nothing else. A use line names a built-in group.
func ParseFile(r io.Reader) (*File, error) {
	return ParseFileWith(r, nil)
}

// ParseFileWith is ParseFile with use lines naming the filters of named
// too, see Resolver. It checks the names only: NewWith resolves them.
func ParseFileWith(r io.Reader, named Resolver) (*File, error) {
	f := &File{}
	seenVersion := false
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := stripComment(sc.Text())
		if line == "" {
			continue
		}
		fail := func(code, reason string) error {
			return &Error{Code: code, Rule: line, Reason: reason, Line: n}
		}
		word, rest, _ := strings.Cut(line, " ")
		if !seenVersion {
			if word != "version" {
				return nil, fail("bad-directive", "version 1 comes first")
			}
			if rest != "1" {
				return nil, fail("bad-version", "only version 1 is known")
			}
			seenVersion = true
			continue
		}
		switch word {
		case "version", "root", "types", "max-file-size", "use":
			if len(f.Lines) > 0 {
				return nil, fail("bad-directive", "directives come before the rules")
			}
		}
		var err error
		switch word {
		case "version":
			return nil, fail("bad-directive", "version twice")
		case "root":
			if f.Root != "" {
				return nil, fail("bad-directive", "root twice")
			}
			if rest != "~" && !strings.HasPrefix(rest, "~/") && !strings.HasPrefix(rest, "/") {
				return nil, fail("bad-directive", "root is absolute, ~ or ~/...")
			}
			f.Root = rest
		case "types":
			if f.Types != "" {
				return nil, fail("bad-directive", "types twice")
			}
			if rest != "strict" && rest != "decay" {
				return nil, fail("bad-directive", "types strict or types decay")
			}
			f.Types = rest
		case "max-file-size":
			size, rule, scoped := strings.Cut(rest, " ")
			if scoped {
				err = f.AddSizeLimit(size, strings.TrimSpace(rule))
				break
			}
			if f.MaxFileSize != "" {
				return nil, fail("bad-directive", "max-file-size twice")
			}
			if _, err := ParseSize(rest); err != nil {
				return nil, fail("bad-directive", "max-file-size: "+err.Error())
			}
			f.MaxFileSize = rest
		case "use":
			err = f.UseWith(rest, named)
		case "exclude":
			err = f.Add(Exclude, rest)
		case "include":
			err = f.Add(Include, rest)
		default:
			if directiveWord.MatchString(word) {
				return nil, fail("unknown-directive", "exclude, include, a directive or a bare rule")
			}
			err = f.Add(Include, line)
		}
		if err != nil {
			if e, ok := err.(*Error); ok {
				e.Line = n
			}
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !seenVersion {
		return nil, &Error{Code: "bad-directive", Reason: "version 1 comes first"}
	}
	return f, nil
}

// stripComment trims a line and drops its comment: a "#" at the start of the
// line or after a space or tab
func stripComment(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#") {
		return ""
	}
	for _, sep := range []string{" #", "\t#"} {
		if i := strings.Index(line, sep); i >= 0 {
			line = line[:i]
		}
	}
	return strings.TrimSpace(line)
}

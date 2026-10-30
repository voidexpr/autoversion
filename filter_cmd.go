package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicolas/autoversion/filterrules"
	"github.com/spf13/cobra"
)

var (
	filterFileFlag string
	includeFlags   []string
	excludeFlags   []string
	useFlags       []string
	explainFlag    string
	summaryFlag    bool
)

var errDestinationRules = errors.New("the destination has its own rules: give --destination or rules (--filter, --include, --exclude, --use), not both")

// addRuleFlags adds the flags that give rules on the command line
func addRuleFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&filterFileFlag, "filter", "", "rules file (- for stdin), in the shared filter syntax (see filter --help)")
	cmd.Flags().StringArrayVar(&includeFlags, "include", nil, "include rule, e.g. /notes or 'f:/**/*.md' (repeatable)")
	cmd.Flags().StringArrayVar(&excludeFlags, "exclude", nil, "exclude rule, e.g. /Backup or 'f:/**/*.old' (repeatable)")
	cmd.Flags().StringArrayVar(&useFlags, "use", nil, "built-in group, e.g. default (repeatable, see filter groups)")
}

func ruleFlagsGiven() bool {
	return filterFileFlag != "" || len(useFlags)+len(includeFlags)+len(excludeFlags) > 0
}

// addFlagRules adds the rules of the --use, --include and --exclude flags
func addFlagRules(f *filterrules.File) error {
	for _, name := range useFlags {
		if err := f.Use(name); err != nil {
			return fmt.Errorf("--use: %v", err)
		}
	}
	for _, rule := range includeFlags {
		if err := f.Add(filterrules.Include, rule); err != nil {
			return fmt.Errorf("--include: %v", err)
		}
	}
	for _, rule := range excludeFlags {
		if err := f.Add(filterrules.Exclude, rule); err != nil {
			return fmt.Errorf("--exclude: %v", err)
		}
	}
	return nil
}

// readFilterFlag reads the --filter file, or stdin for "-", and parses it. A
// root line in it must name the source, when sourcePath is given, and is
// removed: a destination's rules apply to its marker's source.
func readFilterFlag(sourcePath string) (string, *filterrules.File, error) {
	name := filterFileFlag
	var data []byte
	var err error
	if filterFileFlag == "-" {
		name = "stdin"
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(filterFileFlag)
	}
	if err != nil {
		return "", nil, err
	}
	text := string(data)
	f, err := filterrules.ParseFile(strings.NewReader(text))
	if err != nil {
		return "", nil, fmt.Errorf("%s: %v", name, err)
	}
	if f.Root == "" {
		return text, f, nil
	}
	if sourcePath != "" {
		root := f.Root
		if root == "~" || strings.HasPrefix(root, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil, err
			}
			root = filepath.Join(home, root[1:])
		}
		if resolved, err := resolvePath(root); err != nil || resolved != sourcePath {
			return "", nil, fmt.Errorf("%s: root %s is not the source %s", name, f.Root, sourcePath)
		}
	}
	f.Root = ""
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "root" {
			return strings.Join(slices.Delete(lines, i, i+1), ""), f, nil
		}
	}
	return text, f, nil
}

// addFlagLines adds the --use, --include and --exclude flags to a rules
// file's text, its lines and comments kept: the use lines after its version
// line, if any (directives come before rules), the rules at the end. uses
// are the file's own, not repeated.
func addFlagLines(text string, uses []string) string {
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	lines := strings.SplitAfter(text, "\n")
	at := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Fields(trimmed)[0] == "version" {
			at = i + 1
		}
		break
	}
	var useLines []string
	for _, name := range useFlags {
		if !slices.Contains(uses, name) {
			useLines = append(useLines, "use "+name+"\n")
		}
	}
	out := slices.Concat(lines[:at], useLines, lines[at:])
	for _, rule := range includeFlags {
		out = append(out, filterrules.Line{Kind: filterrules.Include, Text: rule}.String()+"\n")
	}
	for _, rule := range excludeFlags {
		out = append(out, filterrules.Line{Kind: filterrules.Exclude, Text: rule}.String()+"\n")
	}
	return strings.Join(out, "")
}

// rulesText returns the rules given on the command line as a rules file: the
// --filter file (stdin for -) as written, comments kept, plus the --use,
// --include and --exclude flags; or the flags alone, one per line. "" when no
// rules were given.
func rulesText(sourcePath string) (string, error) {
	if !ruleFlagsGiven() {
		return "", nil
	}
	text, f := "", &filterrules.File{}
	if filterFileFlag != "" {
		var err error
		if text, f, err = readFilterFlag(sourcePath); err != nil {
			return "", err
		}
	}
	fileUses := slices.Clone(f.Uses)
	if err := addFlagRules(f); err != nil {
		return "", err
	}
	if len(f.Lines) == 0 && len(f.Uses) == 0 {
		return "", fmt.Errorf("no rules in --filter %s", filterFileFlag)
	}
	if len(f.Uses) == 0 {
		fmt.Fprintln(os.Stderr, "note: no use line or --use, so the built-in groups don't apply (use default for all of them)")
	}
	if filterFileFlag == "" {
		return f.Format(), nil
	}
	return addFlagLines(text, fileUses), nil
}

// rulesFromFlags returns the rules given on the command line (see
// rulesText). Without any, the rules are use default.
func rulesFromFlags(sourcePath string) (*filterrules.Rules, error) {
	text, err := rulesText(sourcePath)
	if err != nil {
		return nil, err
	}
	if text == "" {
		text = defaultRules
	}
	f, err := filterrules.ParseFile(strings.NewReader(text))
	if err != nil {
		return nil, err
	}
	return filterrules.New(f)
}

var filterCmd = &cobra.Command{
	Use:   "filter",
	Short: "Show, test and change the rules saying what is tracked",
	Long: `A destination's rules say which files of its source are tracked. They are
stored in the destination's ` + filterFileName + `, in the filter syntax
shared with ownit. Without the file, they are "use default". Example, for
~/.claude:

  version 1
  use default             # the built-in groups, see filter groups

  exclude /*              # an allowlist: only what is listed below
  /CLAUDE.md
  /settings.json
  /skills/!synced         # include /skills, exclude /skills/synced
  /projects/*/memory

- Every rule starts at the source root (/). * matches within a name, and a
  /**/ segment any number of folders. f: makes a rule match files only.
- A bare line is an include. {a,b} gives one rule per name.
- The closest match wins: the entry itself, then each folder above it. On
  one level, a literal rule beats a wildcard, then an include beats an
  exclude. Line order means nothing.
- Always applied: ` + alwaysAppliedText + `.

Rules on the command line (init, filter scan, dump and set, watch
--report-only), combined:
  --filter FILE                                 a rules file, - for stdin
  --include RULE, --exclude RULE, --use GROUP   repeatable
The version 1 line is optional. Without a use line or --use, the built-in
groups don't apply. Example:
  autoversion filter scan --source ~/.claude --use default --filter - <<'EOF'
  exclude /*
  /CLAUDE.md
  EOF`,
}

var filterScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "List the files of the source the rules keep",
	Long: `Lists the files of the source the rules keep, relative to the source. The
rules are the destination's (--destination), or the ones given (none: use
default).
  --explain PATH  prints what decides PATH: the result, the rule and the level
                  it matches
  --summary       prints the number of kept files and of folders visited (on
                  Linux, the inotify watches a watcher needs)`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requirePaths(false); err != nil {
			return err
		}
		sourcePath, err := resolveSourcePath(sourceFlag)
		if err != nil {
			return err
		}

		var rules *filterrules.Rules
		maxSize := maxFileSize
		if destFlag != "" {
			if ruleFlagsGiven() || cmd.Flags().Changed("max-file-size") {
				return errDestinationRules
			}
			result, err := ValidateSourceAndDestination(sourcePath, destFlag)
			if err != nil {
				return err
			}
			if problems := result.problems(); len(problems) > 0 {
				return errors.New(problems[0])
			}
			rules, maxSize = result.Rules, result.Settings.MaxFileSizeMB
		} else if rules, err = rulesFromFlags(sourcePath); err != nil {
			return err
		}

		s := &sourceRules{root: sourcePath, rules: rules}
		if explainFlag != "" {
			return explain(s, explainFlag, maxSize)
		}
		files := 0
		dirs, _ := s.walk(sourcePath, func(path string, info fs.FileInfo) error {
			if tooLarge(info.Size(), maxSize) {
				return nil
			}
			files++
			if !summaryFlag {
				rel, _ := s.rel(path)
				fmt.Println(rel)
			}
			return nil
		})
		if summaryFlag {
			fmt.Printf("%d files kept, %d folders visited\n", files, dirs)
		}
		return nil
	},
}

// explain prints what decides an entry of the source
func explain(s *sourceRules, path string, maxSizeMB int64) error {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.root, path)
	}
	rel, ok := s.rel(abs)
	if !ok || rel == "" {
		return fmt.Errorf("%s is not an entry of the source %s", path, s.root)
	}

	info, err := os.Lstat(abs)
	isDir := err == nil && info.IsDir()
	what := "file"
	switch {
	case err != nil:
		what = "missing, decided as a file"
	case isDir:
		what = "folder"
	}

	var result string
	switch {
	case err == nil && !isDir && !info.Mode().IsRegular():
		result = "left out: not a regular file (symlinks are never tracked)"
	case !alwaysApplied.Decide(rel, isDir).Kept:
		result = "left out: always applied (" + alwaysAppliedText + ")"
	default:
		d := s.rules.Decide(rel, isDir)
		switch {
		case d.Rule == nil:
			result = "kept: no rule matches it or a folder above it"
		default:
			result = "left out"
			if d.Kept {
				result = "kept"
			}
			by := d.Rule.String()
			if d.Rule.Group != "" {
				by += " (group " + d.Rule.Group + ")"
			}
			level := strings.Join(strings.Split(rel, "/")[:d.Level], "/")
			result = fmt.Sprintf("%s by %s, matching %s", result, by, level)
		}
		if d.Kept && err == nil && !isDir && tooLarge(info.Size(), maxSizeMB) {
			result = fmt.Sprintf("left out: larger than %d MB (%s)", maxSizeMB, result)
		}
	}
	fmt.Printf("%s (%s): %s\n", rel, what, result)
	return nil
}

var filterDumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Print the rules expanded, groups included",
	Long:  "Prints the destination's rules (--destination), or the ones given (none: use default), with sugar and groups expanded.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var rules *filterrules.Rules
		if destFlag != "" {
			if ruleFlagsGiven() {
				return errDestinationRules
			}
			destPath, err := resolvePath(destFlag)
			if err != nil {
				return err
			}
			if marker, err := readMarker(destPath); err != nil || marker == nil {
				return fmt.Errorf("destination %s is not initialized (%v)", destPath, err)
			}
			if rules, err = readRules(destPath); err != nil {
				return err
			}
		} else {
			sourcePath := ""
			if sourceFlag != "" {
				var err error
				if sourcePath, err = resolveSourcePath(sourceFlag); err != nil {
					return err
				}
			}
			var err error
			if rules, err = rulesFromFlags(sourcePath); err != nil {
				return err
			}
		}
		fmt.Printf("# always applied: %s\n", alwaysAppliedText)
		fmt.Print(rules.Dump())
		return nil
	},
}

var filterGroupsCmd = &cobra.Command{
	Use:   "groups",
	Short: "List the built-in groups and their rules",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		for i, g := range filterrules.Groups() {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("# %s: %s (use %s)\n", g.Name, g.About, g.Name)
			for _, rule := range g.Exclude {
				fmt.Println("exclude " + rule)
			}
		}
		return nil
	},
}

var filterSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Replace a destination's rules",
	Long: `Replaces the destination's rules with the ones given: a --filter file
(- for stdin) is stored as written, with the flags added; flags alone are
written one per line. Then the same as filter check.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := rulesDestination()
		if err != nil {
			return err
		}
		text, err := rulesText(result.SourceResolved)
		if err != nil {
			return err
		}
		if text == "" {
			return errors.New("no rules given: --filter FILE, or --use, --include and --exclude (--use default for the built-in groups only)")
		}
		return applyRules(result, text, true)
	},
}

var filterCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check and apply a destination's rules after editing them",
	Long: `Checks the destination's ` + filterFileName + `, after a hand edit:
- validates the rules and prints the warnings (dead rules, includes that
  change nothing)
- prints how many files they keep, and the files the next sync removes from
  the destination
- refuses rules that keep no file while the destination has files, and an
  allowlist none of whose includes exists
- prints the change since the last commit and commits it (git destinations)
- restarts the installed service, if one runs
With --dry-run, only the checks and counts.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := rulesDestination()
		if err != nil {
			return err
		}
		path := filepath.Join(result.DestinationResolved, filterFileName)
		text, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return fmt.Errorf("%s doesn't exist, the rules are use default (write one with filter set)", path)
		}
		if err != nil {
			return err
		}
		return applyRules(result, string(text), false)
	},
}

// rulesDestination validates --destination and the source its marker names,
// for the commands that change its rules. Its current rules may be invalid:
// they are being replaced or fixed.
func rulesDestination() (*PathValidationResult, error) {
	if destFlag == "" {
		return nil, errors.New("--destination is required")
	}
	destPath, err := resolvePath(destFlag)
	if err != nil {
		return nil, err
	}
	marker, err := readMarker(destPath)
	if err != nil {
		return nil, err
	}
	if marker == nil {
		return nil, fmt.Errorf("destination %s is not initialized (see init)", destPath)
	}
	if sourceFlag != "" {
		if sourcePath, err := resolvePath(sourceFlag); err != nil || sourcePath != marker.Source {
			return nil, fmt.Errorf("destination %s belongs to source %s, not %s", destPath, marker.Source, sourceFlag)
		}
	}
	result, err := ValidateSourceAndDestination(marker.Source, destPath)
	if err != nil {
		return nil, err
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("path validation failed: %s", result.Errors[0])
	}
	return result, nil
}

// applyRules checks new rules for a destination and prints what the next
// sync keeps and removes. Then, unless --dry-run, it writes them (replace),
// commits them and restarts the installed service.
func applyRules(result *PathValidationResult, text string, replace bool) error {
	sourcePath, destPath, settings := result.SourceResolved, result.DestinationResolved, result.Settings
	rules, err := checkRules(sourcePath, text)
	if err != nil {
		return err
	}

	kept := map[string]bool{}
	(&sourceRules{root: sourcePath, rules: rules}).walk(sourcePath, func(path string, info fs.FileInfo) error {
		if !tooLarge(info.Size(), settings.MaxFileSizeMB) {
			rel, _ := filepath.Rel(sourcePath, path)
			kept[rel] = true
		}
		return nil
	})
	var removed []string
	inDest := 0
	walkDestination(destPath, "", func(rel string) {
		if kept[rel] {
			inDest++
		} else {
			removed = append(removed, rel)
		}
	})
	if len(kept) == 0 && len(removed) > 0 {
		return fmt.Errorf("the rules keep no file of %s: the next sync would remove all %d files of %s", sourcePath, len(removed), destPath)
	}
	fmt.Printf("Kept: %d files of %s (%d not in the destination yet)\n", len(kept), sourcePath, len(kept)-inDest)
	printRemoved(destPath, removed, settings.Git)

	if dryRun {
		if replace {
			fmt.Printf("\nNew rules:\n%s", text)
		}
		fmt.Println("Dry run: nothing written")
		return nil
	}
	if replace {
		if err := os.WriteFile(filepath.Join(destPath, filterFileName), []byte(text), 0644); err != nil {
			return fmt.Errorf("failed to write rules: %v", err)
		}
	}
	if settings.Git {
		if err := commitRules(destPath); err != nil {
			return err
		}
	} else if replace {
		fmt.Printf("\nRules written to %s\n", filepath.Join(destPath, filterFileName))
	}
	return restartJob(sourcePath, destPath)
}

// printRemoved prints the files the next sync removes from a destination
func printRemoved(destPath string, removed []string, git bool) {
	if len(removed) == 0 {
		fmt.Printf("The next sync removes no file from %s\n", destPath)
		return
	}
	where := "kept in git history"
	if !git {
		where = "a plain mirror: they are not kept anywhere"
	}
	fmt.Printf("The next sync removes %d files from %s (%s):\n", len(removed), destPath, where)
	for i, rel := range removed {
		if i == 5 {
			fmt.Printf("  ... and %d more\n", len(removed)-i)
			break
		}
		fmt.Println("  " + rel)
	}
}

// commitRules prints the change to a destination's rules since the last
// commit and commits it
func commitRules(destPath string) error {
	status, err := gitOutput(destPath, "status", "--porcelain", "--", filterFileName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		fmt.Println("\nRules: no change since the last commit")
		return nil
	}
	diff, err := gitOutput(destPath, "diff", "HEAD", "--", filterFileName)
	if strings.HasPrefix(status, "??") || err != nil {
		text, _ := os.ReadFile(filepath.Join(destPath, filterFileName))
		fmt.Printf("\nNew rules file:\n%s", text)
	} else {
		fmt.Printf("\nChange since the last commit:\n%s", diff)
	}
	if err := Add(destPath, []string{filterFileName}, debug); err != nil {
		return err
	}
	if err := CommitPaths(destPath, "filter: update rules", filterFileName); err != nil {
		return err
	}
	fmt.Println("Committed: filter: update rules")
	return nil
}

func init() {
	addRuleFlags(filterScanCmd)
	filterScanCmd.Flags().Int64Var(&maxFileSize, "max-file-size", defaultMaxFileSizeMB, "maximum file size in MB, without --destination")
	filterScanCmd.Flags().StringVar(&explainFlag, "explain", "", "print what decides this entry (relative to the source, or absolute)")
	filterScanCmd.Flags().BoolVar(&summaryFlag, "summary", false, "print the number of kept files and of folders visited, not the files")
	addRuleFlags(filterDumpCmd)
	addRuleFlags(filterSetCmd)
	filterSetCmd.Flags().BoolVar(&dryRun, "dry-run", false, "only print the checks and counts")
	filterCheckCmd.Flags().BoolVar(&dryRun, "dry-run", false, "only print the checks and counts")

	filterCmd.AddCommand(filterScanCmd, filterDumpCmd, filterSetCmd, filterCheckCmd, filterGroupsCmd)
	rootCmd.AddCommand(filterCmd)
}

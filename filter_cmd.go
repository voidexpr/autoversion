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
	filterFileFlag  string
	includeFlags    []string
	excludeFlags    []string
	useFlags        []string
	maxFileSizeFlag string
	explainFlag     string
	summaryFlag     bool
	noSummaryFlag   bool
)

var errDestinationRules = errors.New("the destination has its own rules: give --destination or rules (--filter, --include, --exclude, --use), not both")

// addRuleFlags adds the flags that give rules on the command line
func addRuleFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&filterFileFlag, "filter", "", "rules file (- for stdin), in the shared filter syntax (see filter --help)")
	cmd.Flags().StringArrayVar(&includeFlags, "include", nil, "include rule, e.g. /notes or 'f:/**/*.md' (repeatable)")
	cmd.Flags().StringArrayVar(&excludeFlags, "exclude", nil, "exclude rule, e.g. /Backup or 'f:/**/*.old' (repeatable)")
	cmd.Flags().StringArrayVar(&useFlags, "use", nil, "built-in group, e.g. default (repeatable, see filter groups)")
	cmd.Flags().StringVar(&maxFileSizeFlag, "max-file-size", "", "size limit, e.g. 10M (units k, m, g; none): the max-file-size line of the rules")
}

func ruleFlagsGiven() bool {
	return filterFileFlag != "" || maxFileSizeFlag != "" || len(useFlags)+len(includeFlags)+len(excludeFlags) > 0
}

// addFlagRules adds the rules of the --max-file-size, --use, --include and
// --exclude flags
func addFlagRules(f *filterrules.File) error {
	if maxFileSizeFlag != "" {
		if f.MaxFileSize != "" {
			return fmt.Errorf("--max-file-size: the --filter file already has max-file-size %s", f.MaxFileSize)
		}
		if _, err := filterrules.ParseSize(maxFileSizeFlag); err != nil {
			return fmt.Errorf("--max-file-size %s: %v", maxFileSizeFlag, err)
		}
		f.MaxFileSize = maxFileSizeFlag
	}
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

// addFlagLines adds the --max-file-size, --use, --include and --exclude
// flags to a rules file's text, its lines and comments kept: the directives
// after its version line, if any (directives come before rules), the rules
// at the end. uses are the file's own, not repeated.
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
	if maxFileSizeFlag != "" {
		useLines = append(useLines, "max-file-size "+maxFileSizeFlag+"\n")
	}
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

// flagRules returns the rules given on the command line, parsed and as a
// rules file: the --filter file (stdin for -) as written, comments kept, plus
// the --use, --include and --exclude flags; or the flags alone, one per line.
// nil and "" when no rules were given.
func flagRules(sourcePath string) (string, *filterrules.File, error) {
	if !ruleFlagsGiven() {
		return "", nil, nil
	}
	text, f := "", &filterrules.File{}
	if filterFileFlag != "" {
		var err error
		if text, f, err = readFilterFlag(sourcePath); err != nil {
			return "", nil, err
		}
	}
	fileUses := slices.Clone(f.Uses)
	if err := addFlagRules(f); err != nil {
		return "", nil, err
	}
	if len(f.Lines) == 0 && len(f.Uses) == 0 && f.MaxFileSize == "" {
		return "", nil, fmt.Errorf("no rules in --filter %s", filterFileFlag)
	}
	if len(f.Uses) == 0 {
		fmt.Fprintln(os.Stderr, "note: no use line or --use, so the built-in groups don't apply (use default for all of them)")
	}
	if filterFileFlag == "" {
		return f.Format(), f, nil
	}
	return addFlagLines(text, fileUses), f, nil
}

// rulesText returns the rules given on the command line as a rules file (see
// flagRules), "" when no rules were given
func rulesText(sourcePath string) (string, error) {
	text, _, err := flagRules(sourcePath)
	return text, err
}

// rulesFromFlags returns the rules given on the command line (see
// flagRules). Without any, the rules are use default.
func rulesFromFlags(sourcePath string) (*filterrules.Rules, error) {
	_, f, err := flagRules(sourcePath)
	if err != nil {
		return nil, err
	}
	if f == nil {
		if f, err = filterrules.ParseFile(strings.NewReader(defaultRules)); err != nil {
			return nil, err
		}
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
  --max-file-size SIZE                          the max-file-size line: files
                                                over SIZE (10M, 500k, 1G,
                                                none) are left out; without
                                                it, no limit
A rules file starts with version 1. Without a use line or --use, the
built-in groups don't apply. Example:
  autoversion filter scan --source ~/.claude --use default --filter - <<'EOF'
  version 1
  exclude /*
  /CLAUDE.md
  EOF`,
}

var filterScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "List the files of the source the rules keep",
	Long: `Lists the files of the source the rules keep, relative to the source, then
a summary on stderr: the number and total size of the kept files, and the
folders visited (on Linux, the inotify watches a watcher needs). The rules
are the destination's (--destination), or the ones given (none: use default).
  --explain PATH  prints what decides PATH: the result, the rule and the level
                  it matches
  --summary       the summary only, on stdout
  --no-summary    the files only`,
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
		var maxSize int64
		if destFlag != "" {
			if ruleFlagsGiven() {
				return errDestinationRules
			}
			result, err := ValidateSourceAndDestination(sourcePath, destFlag)
			if err != nil {
				return err
			}
			if problems := result.problems(); len(problems) > 0 {
				return errors.New(problems[0])
			}
			rules, maxSize = result.Rules, result.MaxFileSize
		} else {
			if rules, err = rulesFromFlags(sourcePath); err != nil {
				return err
			}
			_, maxSize = rules.MaxFileSize()
		}

		s := &sourceRules{root: sourcePath, rules: rules}
		if explainFlag != "" {
			return explain(s, explainFlag, maxSize)
		}
		files, size := 0, int64(0)
		dirs, _ := s.walk(sourcePath, func(path string, info fs.FileInfo) error {
			if tooLarge(info.Size(), maxSize) {
				return nil
			}
			files++
			size += info.Size()
			if !summaryFlag {
				rel, _ := s.rel(path)
				fmt.Println(rel)
			}
			return nil
		})
		if !noSummaryFlag {
			out := cmd.ErrOrStderr()
			if summaryFlag {
				out = os.Stdout
			}
			fmt.Fprintf(out, "%d files kept (%s), %d folders visited\n", files, formatSize(size), dirs)
		}
		return nil
	},
}

// explain prints what decides an entry of the source; maxSize is the size
// limit in bytes (0: none)
func explain(s *sourceRules, path string, maxSize int64) error {
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
	case alwaysExcluded(rel):
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
		if d.Kept && err == nil && !isDir && tooLarge(info.Size(), maxSize) {
			result = fmt.Sprintf("left out: %s, larger than the size limit %s (%s)", formatSize(info.Size()), formatSize(maxSize), result)
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
			marker, err := readMarker(destPath)
			if err != nil {
				return err
			}
			if marker == nil {
				return fmt.Errorf("destination %s is not initialized (see init)", destPath)
			}
			if rules, err = readRules(destPath); err != nil {
				return err
			}
			_, from := sizeLimit(rules, marker.destinationSettings)
			fmt.Printf("# size limit: %s\n", from)
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
			for _, name := range g.Use {
				fmt.Println("use " + name)
			}
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
	maxSize, _ := sizeLimit(rules, settings)
	(&sourceRules{root: sourcePath, rules: rules}).walk(sourcePath, func(path string, info fs.FileInfo) error {
		if !tooLarge(info.Size(), maxSize) {
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
	filterScanCmd.Flags().StringVar(&explainFlag, "explain", "", "print what decides this entry (relative to the source, or absolute)")
	filterScanCmd.Flags().BoolVar(&summaryFlag, "summary", false, "print the summary only (kept files, their total size, folders visited), on stdout")
	filterScanCmd.Flags().BoolVar(&noSummaryFlag, "no-summary", false, "print the files only, no summary on stderr")
	filterScanCmd.MarkFlagsMutuallyExclusive("summary", "no-summary")
	addRuleFlags(filterDumpCmd)
	addRuleFlags(filterSetCmd)
	filterSetCmd.Flags().BoolVar(&dryRun, "dry-run", false, "only print the checks and counts")
	filterCheckCmd.Flags().BoolVar(&dryRun, "dry-run", false, "only print the checks and counts")

	filterCmd.AddCommand(filterScanCmd, filterDumpCmd, filterSetCmd, filterCheckCmd, filterGroupsCmd)
	rootCmd.AddCommand(filterCmd)
}

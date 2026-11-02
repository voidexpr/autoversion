package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nicolas/autoversion/filterrules"
)

// markerFileName marks a directory as an autoversion destination and names its source
const markerFileName = ".autoversion"

// filterFileName holds a destination's rules, in the shared filter syntax
// (filterrules)
const filterFileName = ".autoversion.filter"

// defaultRules are the rules of a destination without a rules file, from
// before init required rules (the file is written at migration)
const defaultRules = "version 1\nuse default\n"

// starterRules is the rules file init suggests when it is given none
const starterRules = "version 1\nmax-file-size 10M\nuse default\nuse nested-git\n"

// destinationSettings are what a destination's marker says besides its source
type destinationSettings struct {
	Git    bool
	Restic bool
	// Legacy: the size limit of a marker written before it was the rules
	// file's max-file-size directive. 0: no limit. Read, kept as stored,
	// never added; it applies while the rules file has no directive.
	MaxFileSizeMB int64
}

var defaultSettings = destinationSettings{Git: true, MaxFileSizeMB: defaultMaxFileSizeMB}

var onOff = map[bool]string{true: "on", false: "off"}

func (s destinationSettings) String() string {
	return fmt.Sprintf("git %s, restic %s", onOff[s.Git], onOff[s.Restic])
}

// sizeLimit returns the size limit in force for a destination, in bytes (0:
// none), and where it comes from: the rules file's max-file-size, else the
// marker's legacy max_file_size_mb
func sizeLimit(rules *filterrules.Rules, settings destinationSettings) (bytes int64, from string) {
	if text, bytes := rules.MaxFileSize(); text != "" {
		return bytes, "max-file-size " + text + " (rules file)"
	}
	if settings.MaxFileSizeMB == 0 {
		return 0, "none (no max-file-size in the rules file)"
	}
	return settings.MaxFileSizeMB << 20, fmt.Sprintf("%d MB (the marker's legacy max_file_size_mb: add max-file-size %dM to the rules file)", settings.MaxFileSizeMB, settings.MaxFileSizeMB)
}

type destinationMarker struct {
	Source string
	destinationSettings
	stored map[string]bool            // the settings its file states, the others are inferred
	other  map[string]json.RawMessage // fields autoversion doesn't know, kept when it's rewritten
}

// readMarker returns the marker of a destination, or nil if it has none.
// Settings missing from it describe the destination as it is: git when it
// has a .git folder, restic when it has a restic repository, and the default
// size limit.
func readMarker(destPath string) (*destinationMarker, error) {
	path := filepath.Join(destPath, markerFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read marker: %v", err)
	}
	invalid := func(reason string) error {
		return fmt.Errorf("invalid marker %s (%s), fix or remove it", path, reason)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, invalid(err.Error())
	}
	marker := &destinationMarker{
		destinationSettings: destinationSettings{
			Git:           IsGitRepo(destPath),
			Restic:        IsResticRepo(destPath),
			MaxFileSizeMB: defaultMaxFileSizeMB,
		},
		stored: map[string]bool{},
		other:  map[string]json.RawMessage{},
	}
	for key, raw := range fields {
		var err error
		switch key {
		case "source":
			err = json.Unmarshal(raw, &marker.Source)
		case "git":
			marker.stored[key] = true
			err = json.Unmarshal(raw, &marker.Git)
		case "restic":
			marker.stored[key] = true
			err = json.Unmarshal(raw, &marker.Restic)
		case "max_file_size_mb":
			marker.stored[key] = true
			err = json.Unmarshal(raw, &marker.MaxFileSizeMB)
			if err == nil && marker.MaxFileSizeMB < 0 {
				err = errors.New("negative")
			}
		default:
			marker.other[key] = raw
		}
		if err != nil {
			return nil, invalid(key + ": " + err.Error())
		}
	}
	if marker.Source == "" {
		return nil, invalid("no source")
	}
	return marker, nil
}

// writeMarker writes a marker with all its settings, and the fields
// autoversion doesn't know as they were
func writeMarker(destPath string, marker *destinationMarker) error {
	type field struct {
		key   string
		value any
	}
	fields := []field{
		{"source", marker.Source},
		{"git", marker.Git},
		{"restic", marker.Restic},
	}
	if marker.stored["max_file_size_mb"] {
		fields = append(fields, field{"max_file_size_mb", marker.MaxFileSizeMB})
	}
	for _, key := range slices.Sorted(maps.Keys(marker.other)) {
		fields = append(fields, field{key, marker.other[key]})
	}

	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, f := range fields {
		key, _ := json.Marshal(f.key)
		value, err := json.Marshal(f.value)
		if err != nil {
			return err
		}
		if i > 0 {
			compact.WriteByte(',')
		}
		compact.Write(key)
		compact.WriteByte(':')
		compact.Write(value)
	}
	compact.WriteByte('}')

	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	return os.WriteFile(filepath.Join(destPath, markerFileName), out.Bytes(), 0644)
}

// readRulesText returns a destination's rules file, or defaultRules with a
// warning when it has none (destinations made before init required rules)
func readRulesText(destPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(destPath, filterFileName))
	if os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: %s has no %s, using the rules \"use default\": write its rules with filter set\n", destPath, filterFileName)
		return defaultRules, nil
	}
	return string(data), err
}

// parseRules parses a destination's rules file and combines its rules. It
// has no root line: the root is the marker's source.
func parseRules(text string) (*filterrules.File, *filterrules.Rules, error) {
	f, err := filterrules.ParseFile(strings.NewReader(text))
	if err != nil {
		return nil, nil, err
	}
	if f.Root != "" {
		return nil, nil, errors.New("no root line in a destination's rules: the root is the marker's source")
	}
	rules, err := filterrules.New(f)
	if err != nil {
		return nil, nil, err
	}
	return f, rules, nil
}

// readRules returns a destination's rules
func readRules(destPath string) (*filterrules.Rules, error) {
	text, err := readRulesText(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules: %v", err)
	}
	_, rules, err := parseRules(text)
	if err != nil {
		return nil, fmt.Errorf("invalid rules in %s: %v", filepath.Join(destPath, filterFileName), err)
	}
	return rules, nil
}

// listDir lists a folder of a tree for filterrules' checks
func listDir(root string) filterrules.ListFunc {
	return func(rel string) ([]string, error) {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names, err
	}
}

// checkRules validates rules before they are stored for a source and
// prints the warnings. An allowlist none of whose includes exists is an
// error: the next sync would remove every file of the destination.
func checkRules(sourcePath, text string) (*filterrules.Rules, error) {
	_, rules, err := parseRules(text)
	if err != nil {
		return nil, fmt.Errorf("invalid rules: %v", err)
	}
	var errs []string
	for _, p := range rules.Check(listDir(sourcePath)) {
		if p.Error {
			errs = append(errs, p.Message)
		} else {
			fmt.Println(p)
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid rules for %s: %s", sourcePath, strings.Join(errs, "; "))
	}
	return rules, nil
}

// MarkerStatus tells whether a destination's marker names the source
type MarkerStatus int

const (
	MarkerMissing MarkerStatus = iota
	MarkerMatches
	MarkerOtherSource
)

// PathValidationResult contains the results of path validation
type PathValidationResult struct {
	SourceInput         string
	SourceResolved      string
	DestinationInput    string
	DestinationResolved string
	DestinationExists   bool
	Marker              MarkerStatus
	MarkerSource        string // source named by the marker, when it names another source
	Errors              []string

	// When the marker names the source
	Settings    destinationSettings
	Rules       *filterrules.Rules
	MaxFileSize int64 // the size limit in force, in bytes (0: none), see sizeLimit
	RulesErr    error // the rules file can't be read or is invalid
}

// ValidateSourceAndDestination validates that source and destination paths are
// compatible and reads the destination marker and rules. The destination
// doesn't have to exist yet (init creates it).
func ValidateSourceAndDestination(sourcePath, destPath string) (*PathValidationResult, error) {
	result := &PathValidationResult{
		SourceInput:      sourcePath,
		DestinationInput: destPath,
	}
	addError := func(format string, args ...interface{}) {
		result.Errors = append(result.Errors, fmt.Sprintf(format, args...))
	}

	resolvedSource, err := resolvePath(sourcePath)
	if err != nil {
		return result, fmt.Errorf("failed to resolve source path: %v", err)
	}
	result.SourceResolved = resolvedSource

	if info, err := os.Stat(resolvedSource); err != nil {
		if !os.IsNotExist(err) {
			return result, fmt.Errorf("failed to stat source path: %v", err)
		}
		addError("source path does not exist: %s", resolvedSource)
	} else if !info.IsDir() {
		addError("source path is not a directory: %s", resolvedSource)
	}

	resolvedDest, err := resolvePath(destPath)
	if err != nil {
		return result, fmt.Errorf("failed to resolve destination path: %v", err)
	}
	result.DestinationResolved = resolvedDest

	if info, err := os.Stat(resolvedDest); err != nil {
		if !os.IsNotExist(err) {
			return result, fmt.Errorf("failed to stat destination path: %v", err)
		}
	} else if !info.IsDir() {
		addError("destination path is not a directory: %s", resolvedDest)
	} else {
		result.DestinationExists = true
	}

	if err := verifyPaths(resolvedSource, resolvedDest); err != nil {
		addError("%v", err)
	}

	if _, err := os.Lstat(filepath.Join(resolvedSource, markerFileName)); err == nil {
		addError("source path (%s) contains %s, it is an autoversion destination (--source and --destination swapped?)", resolvedSource, markerFileName)
	}

	// An outer destination's delete pass would remove an inner one's files
	if parent, found := findInParents(resolvedDest, markerFileName); found {
		addError("destination path is inside another autoversion destination (%s)", parent)
	}

	if result.DestinationExists {
		marker, err := readMarker(resolvedDest)
		switch {
		case err != nil:
			addError("%v", err)
		case marker == nil:
			result.Marker = MarkerMissing
		case marker.Source == resolvedSource:
			result.Marker = MarkerMatches
			result.Settings = marker.destinationSettings
			result.Rules, result.RulesErr = readRules(resolvedDest)
			if result.Rules != nil {
				result.MaxFileSize, _ = sizeLimit(result.Rules, result.Settings)
			}
		default:
			result.Marker = MarkerOtherSource
			result.MarkerSource = marker.Source
		}
	}

	// Git: the marker's setting, or init's --no-git for a new destination
	git := !noGit
	if result.Marker == MarkerMatches {
		git = git && result.Settings.Git
	}
	if git {
		if parent, found := findInParents(resolvedDest, ".git"); found {
			addError("destination path has a parent with a .git repository (%s), this would create nested git repos", parent)
		}
	}

	return result, nil
}

// markerError returns nil if the destination's marker names the source, and
// otherwise an error telling how to initialize the destination
func (r *PathValidationResult) markerError() error {
	switch r.Marker {
	case MarkerMatches:
		return nil
	case MarkerOtherSource:
		return fmt.Errorf("destination %s belongs to source %s, not %s (to switch it: autoversion init --force-new-source --source %s --destination %s)",
			r.DestinationResolved, r.MarkerSource, r.SourceResolved, r.SourceResolved, r.DestinationResolved)
	default:
		return fmt.Errorf("destination %s is not initialized (run: autoversion init --source %s --destination %s)",
			r.DestinationResolved, r.SourceResolved, r.DestinationResolved)
	}
}

// problems returns the validation errors plus what prevents copying files:
// the marker not naming the source, invalid rules, or a missing restic
// repository
func (r *PathValidationResult) problems() []string {
	problems := append([]string{}, r.Errors...)
	if err := r.markerError(); err != nil {
		problems = append(problems, err.Error())
	} else if r.RulesErr != nil {
		problems = append(problems, r.RulesErr.Error())
	} else if r.Settings.Restic && !IsResticRepo(r.DestinationResolved) {
		problems = append(problems, fmt.Sprintf("no restic repository for %s (run: autoversion init --source %s --destination %s)",
			r.DestinationResolved, r.SourceResolved, r.DestinationResolved))
	}
	return problems
}

func (r *PathValidationResult) markerText() string {
	switch {
	case !r.DestinationExists:
		return "none (destination does not exist)"
	case r.Marker == MarkerMatches:
		return "names this source"
	case r.Marker == MarkerOtherSource:
		return "names another source: " + r.MarkerSource
	default:
		return "missing"
	}
}

func printResolved(name, input, resolved string) {
	fmt.Printf("  %-25s %s\n", name+" (input):", input)
	if input != resolved {
		fmt.Printf("  %-25s %s\n", name+" (resolved):", resolved)
	} else {
		fmt.Printf("  %-25s %s\n", name+" (resolved):", "(no symlinks)")
	}
}

// checkPaths validates source and destination for a command that copies
// files, printing the path resolution, the rules' warnings and all problems
// found
func checkPaths(sourcePath, destPath string) (*PathValidationResult, error) {
	result, err := ValidateSourceAndDestination(sourcePath, destPath)
	if err != nil {
		return nil, err
	}

	fmt.Println("Path Resolution:")
	printResolved("source", result.SourceInput, result.SourceResolved)
	printResolved("destination", result.DestinationInput, result.DestinationResolved)
	fmt.Printf("  %-25s %s\n", "marker:", result.markerText())
	fmt.Println()

	problems := result.problems()
	if result.Rules != nil {
		var warnings []string
		for _, p := range result.Rules.Check(listDir(result.SourceResolved)) {
			if p.Error {
				problems = append(problems, "rules: "+p.Message)
			} else {
				warnings = append(warnings, p.String())
			}
		}
		if len(warnings) > 0 {
			fmt.Println("Rules:")
			for _, w := range warnings {
				fmt.Printf("  %s\n", w)
			}
			fmt.Println()
		}
	}

	if len(problems) > 0 {
		fmt.Println("Validation Errors:")
		for _, p := range problems {
			fmt.Printf("  ✗ %s\n", p)
		}
		return nil, fmt.Errorf("path validation failed with %d error(s)", len(problems))
	}
	return result, nil
}

// validatePaths validates source and destination for a command that copies
// files: the result has their resolved paths, the destination's settings
// and its rules
func validatePaths(sourcePath, destPath string) (*PathValidationResult, error) {
	result, err := ValidateSourceAndDestination(sourcePath, destPath)
	if err != nil {
		return nil, err
	}
	if problems := result.problems(); len(problems) > 0 {
		return nil, fmt.Errorf("path validation failed: %s", problems[0])
	}
	return result, nil
}

// settingsFlags are the settings given to init, nil when not given
type settingsFlags struct {
	git    *bool
	restic *bool
}

// apply returns s with the given settings
func (f settingsFlags) apply(s destinationSettings) destinationSettings {
	if f.git != nil {
		s.Git = *f.git
	}
	if f.restic != nil {
		s.Restic = *f.restic
	}
	return s
}

// inferredIn reports whether a given setting is only inferred in a marker,
// which then doesn't state it (written before settings were stored)
func (f settingsFlags) inferredIn(marker *destinationMarker) bool {
	return f.git != nil && !marker.stored["git"] ||
		f.restic != nil && !marker.stored["restic"]
}

// changes lists how the given settings change s
func (f settingsFlags) changes(s destinationSettings) []string {
	n := f.apply(s)
	var changes []string
	if n.Git != s.Git {
		changes = append(changes, "git "+onOff[s.Git]+" → "+onOff[n.Git])
	}
	if n.Restic != s.Restic {
		changes = append(changes, "restic "+onOff[s.Restic]+" → "+onOff[n.Restic])
	}
	return changes
}

// initOptions are the init command's flags
type initOptions struct {
	forceNewSource bool
	// with forceNewSource on a destination whose marker names another
	// source, what its settings become: the marker's (keep) or the
	// defaults (replace), the settings flags applied to either
	keepMarkerSettings    bool
	replaceMarkerSettings bool
	obliterate            bool
	updateSettings        bool
	settings              settingsFlags
	rules                 string // rules file text, "" when no rules were given
}

const markerSettingsFlags = "--force-keep-marker-settings or --force-replace-marker-settings"

// gitToMirrorError refuses to stop versioning a git destination: its .git
// folder and history would stay while files stop being committed
func gitToMirrorError(destPath string) error {
	return fmt.Errorf("destination %s is versioned with git: --no-git would keep its history while files stop being committed (on purpose: remove %s by hand, or start over with --force-obliterate-yes-i-am-sure)",
		destPath, filepath.Join(destPath, ".git"))
}

// initDestination creates or adopts destPath as the destination of
// sourcePath (both resolved), and writes its marker and rules, see the init
// command
func initDestination(sourcePath, destPath string, opts initOptions) error {
	entries, err := os.ReadDir(destPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read destination: %v", err)
	}

	marker, markerErr := readMarker(destPath)
	if markerErr != nil && !opts.obliterate {
		return markerErr
	}
	if marker != nil && marker.Source == sourcePath && !opts.obliterate {
		if opts.rules != "" {
			return fmt.Errorf("destination %s is already initialized: change its rules with filter set --destination %s", destPath, destPath)
		}
		return updateSettings(destPath, marker, opts)
	}

	keptContents := false
	base := defaultSettings
	switch {
	case opts.obliterate:
	case marker != nil:
		if !opts.forceNewSource {
			return fmt.Errorf("destination %s belongs to source %s (--force-new-source with %s switches it to %s, keeping its contents and history)", destPath, marker.Source, markerSettingsFlags, sourcePath)
		}
		if !opts.keepMarkerSettings && !opts.replaceMarkerSettings {
			return fmt.Errorf("destination %s belongs to source %s: say what its settings (%s) become, %s (the defaults, then the flags given)", destPath, marker.Source, marker.destinationSettings, markerSettingsFlags)
		}
		if opts.keepMarkerSettings {
			base = marker.destinationSettings
		}
		keptContents = true
	case len(entries) > 0:
		if !opts.forceNewSource {
			return fmt.Errorf("destination %s is not empty and has no %s marker (--force-new-source uses it for %s, keeping its contents and history)", destPath, markerFileName, sourcePath)
		}
		keptContents = true
	}

	settings := opts.settings.apply(base)
	if !settings.Git && keptContents && IsGitRepo(destPath) {
		return gitToMirrorError(destPath)
	}

	// A destination kept for a new source keeps its rules
	rules := opts.rules
	if rules == "" {
		rules = defaultRules
		if keptContents {
			if rules, err = readRulesText(destPath); err != nil {
				return fmt.Errorf("failed to read rules: %v", err)
			}
		}
	}
	if _, err := checkRules(sourcePath, rules); err != nil {
		return err
	}

	if opts.obliterate {
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(destPath, e.Name())); err != nil {
				return fmt.Errorf("failed to remove %s: %v", filepath.Join(destPath, e.Name()), err)
			}
		}
		if len(entries) > 0 {
			fmt.Printf("Removed everything in %s (%d entries)\n", destPath, len(entries))
		}
		marker = nil
	}

	if err := EnsureDir(destPath); err != nil {
		return fmt.Errorf("failed to create destination: %v", err)
	}
	if err := ensureRepos(destPath, settings); err != nil {
		return err
	}

	newMarker := &destinationMarker{Source: sourcePath, destinationSettings: settings}
	if marker != nil {
		newMarker.other = marker.other
		if opts.keepMarkerSettings {
			newMarker.stored = marker.stored
		}
	}
	if err := writeMarker(destPath, newMarker); err != nil {
		return fmt.Errorf("failed to write marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destPath, filterFileName), []byte(rules), 0644); err != nil {
		return fmt.Errorf("failed to write rules: %v", err)
	}
	if settings.Git {
		if err := Add(destPath, []string{markerFileName, filterFileName}, debug); err != nil {
			return err
		}
		if err := CommitPaths(destPath, "init: source "+sourcePath, markerFileName, filterFileName); err != nil {
			return err
		}
	}
	fmt.Printf("Initialized destination %s for source %s\n", destPath, sourcePath)

	if keptContents {
		if settings.Git {
			fmt.Printf("The next sync makes %s match %s: files not in the source are removed (kept in git history)\n", destPath, sourcePath)
		} else {
			fmt.Printf("The next sync makes %s match %s: files not in the source are deleted\n", destPath, sourcePath)
		}
	}
	return nil
}

// ensureRepos creates the git and restic repositories the settings ask for,
// when the destination doesn't have them yet
func ensureRepos(destPath string, settings destinationSettings) error {
	if settings.Git && !IsGitRepo(destPath) {
		if err := Init(destPath, debug); err != nil {
			return err
		}
		fmt.Printf("Initialized git repository at %s\n", destPath)
	}
	if settings.Restic && !IsResticRepo(destPath) {
		if err := InitRestic(destPath, debug); err != nil {
			return fmt.Errorf("failed to initialize restic: %v", err)
		}
	}
	return nil
}

// updateSettings is init on a destination already initialized for the
// source: the settings given must be the marker's, unless --update-settings
func updateSettings(destPath string, marker *destinationMarker, opts initOptions) error {
	settings := opts.settings.apply(marker.destinationSettings)
	if marker.Git && !settings.Git {
		return gitToMirrorError(destPath)
	}
	changes := opts.settings.changes(marker.destinationSettings)
	if len(changes) > 0 && !opts.updateSettings {
		return fmt.Errorf("destination %s has other settings: %s (--update-settings changes them)", destPath, strings.Join(changes, ", "))
	}
	if settings.Git && !marker.Git {
		if parent, found := findInParents(destPath, ".git"); found {
			return fmt.Errorf("destination path has a parent with a .git repository (%s), this would create nested git repos", parent)
		}
	}

	if err := ensureRepos(destPath, settings); err != nil {
		return err
	}

	// A setting given but only inferred is stored, with the same value: from
	// now on it doesn't depend on what the destination holds (a .git folder)
	inferred := opts.settings.inferredIn(marker)
	if len(changes) == 0 && !inferred {
		fmt.Printf("Destination %s is already initialized for %s\n", destPath, marker.Source)
		return nil
	}
	wasGit := marker.Git
	marker.destinationSettings = settings
	if err := writeMarker(destPath, marker); err != nil {
		return fmt.Errorf("failed to write marker: %v", err)
	}
	message := "init: settings stored in the marker"
	if len(changes) > 0 {
		message = "init: " + strings.Join(changes, ", ")
	}
	if settings.Git {
		if err := Add(destPath, []string{markerFileName}, debug); err != nil {
			return err
		}
		if err := CommitPaths(destPath, message, markerFileName); err != nil {
			return err
		}
	}
	if len(changes) == 0 {
		fmt.Printf("Stored the settings of %s in its marker\n", destPath)
		return nil
	}
	fmt.Printf("Updated the settings of %s: %s\n", destPath, strings.Join(changes, ", "))
	if settings.Git && !wasGit {
		fmt.Println("The next sync commits every file")
	}
	return restartJob(marker.Source, destPath)
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// markerFileName marks a directory as an autoversion destination and names its source
const markerFileName = ".autoversion"

type destinationMarker struct {
	Source string `json:"source"`
}

// readMarker returns the marker of a destination, or nil if it has none
func readMarker(destPath string) (*destinationMarker, error) {
	path := filepath.Join(destPath, markerFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read marker: %v", err)
	}
	var marker destinationMarker
	if err := json.Unmarshal(data, &marker); err != nil || marker.Source == "" {
		return nil, fmt.Errorf("invalid marker %s, fix or remove it", path)
	}
	return &marker, nil
}

func writeMarker(destPath, sourcePath string) error {
	data, err := json.MarshalIndent(destinationMarker{Source: sourcePath}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destPath, markerFileName), append(data, '\n'), 0644)
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
}

// ValidateSourceAndDestination validates that source and destination paths are
// compatible and reads the destination marker. The destination doesn't have
// to exist yet (init creates it).
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
	if !noGit {
		if parent, found := findInParents(resolvedDest, ".git"); found {
			addError("destination path has a parent with a .git repository (%s), this would create nested git repos", parent)
		}
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
		default:
			result.Marker = MarkerOtherSource
			result.MarkerSource = marker.Source
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
// the marker not naming the source, or a missing restic repository
func (r *PathValidationResult) problems() []string {
	problems := append([]string{}, r.Errors...)
	if err := r.markerError(); err != nil {
		problems = append(problems, err.Error())
	} else if useRestic && !IsResticRepo(r.DestinationResolved) {
		problems = append(problems, fmt.Sprintf("no restic repository for %s (run: autoversion init --restic --source %s --destination %s)",
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
// files, printing the path resolution and all problems found
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

	if problems := result.problems(); len(problems) > 0 {
		fmt.Println("Validation Errors:")
		for _, p := range problems {
			fmt.Printf("  ✗ %s\n", p)
		}
		return nil, fmt.Errorf("path validation failed with %d error(s)", len(problems))
	}
	return result, nil
}

// validatePaths validates source and destination for a command that copies
// files and returns their resolved paths
func validatePaths(sourcePath, destPath string) (absSourcePath, absDestPath string, err error) {
	result, err := ValidateSourceAndDestination(sourcePath, destPath)
	if err != nil {
		return "", "", err
	}
	if problems := result.problems(); len(problems) > 0 {
		return "", "", fmt.Errorf("path validation failed: %s", problems[0])
	}
	return result.SourceResolved, result.DestinationResolved, nil
}

// initDestination creates or adopts destPath as the destination of
// sourcePath (both resolved) and writes its marker, see the init command
func initDestination(sourcePath, destPath string, forceNewSource, obliterate bool) error {
	entries, err := os.ReadDir(destPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read destination: %v", err)
	}

	marker, markerErr := readMarker(destPath)
	if markerErr != nil && !obliterate {
		return markerErr
	}

	keptContents := false
	switch {
	case obliterate:
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(destPath, e.Name())); err != nil {
				return fmt.Errorf("failed to remove %s: %v", filepath.Join(destPath, e.Name()), err)
			}
		}
		if len(entries) > 0 {
			fmt.Printf("Removed everything in %s (%d entries)\n", destPath, len(entries))
		}
		marker = nil
	case marker != nil && marker.Source == sourcePath:
		// Already initialized, only a missing git or restic repository is added
	case marker != nil:
		if !forceNewSource {
			return fmt.Errorf("destination %s belongs to source %s (--force-new-source switches it to %s, keeping its contents and history)", destPath, marker.Source, sourcePath)
		}
		keptContents = true
	case len(entries) > 0:
		if !forceNewSource {
			return fmt.Errorf("destination %s is not empty and has no %s marker (--force-new-source uses it for %s, keeping its contents and history)", destPath, markerFileName, sourcePath)
		}
		keptContents = true
	}

	if err := EnsureDir(destPath); err != nil {
		return fmt.Errorf("failed to create destination: %v", err)
	}
	if !noGit && !IsGitRepo(destPath) {
		if err := Init(destPath, debug); err != nil {
			return err
		}
		fmt.Printf("Initialized git repository at %s\n", destPath)
	}
	if useRestic && !IsResticRepo(destPath) {
		if err := InitRestic(destPath, debug); err != nil {
			return fmt.Errorf("failed to initialize restic: %v", err)
		}
	}

	if marker != nil && marker.Source == sourcePath {
		fmt.Printf("Destination %s is already initialized for %s\n", destPath, sourcePath)
		return nil
	}

	if err := writeMarker(destPath, sourcePath); err != nil {
		return fmt.Errorf("failed to write marker: %v", err)
	}
	if !noGit {
		if err := Add(destPath, []string{markerFileName}, debug); err != nil {
			return err
		}
		if err := CommitPaths(destPath, "init: source "+sourcePath, markerFileName); err != nil {
			return err
		}
	}
	fmt.Printf("Initialized destination %s for source %s\n", destPath, sourcePath)

	if keptContents {
		if noGit {
			fmt.Printf("The next sync makes %s match %s: files not in the source are deleted\n", destPath, sourcePath)
		} else {
			fmt.Printf("The next sync makes %s match %s: files not in the source are removed (kept in git history)\n", destPath, sourcePath)
		}
	}
	return nil
}

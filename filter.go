package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Compiled regex patterns for file exclusions
var excludePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\.(log|dump|swp|swpx|swo|pid|lock|beam|bea#)$`),
	regexp.MustCompile(`/\d+$`), // vim/fs temp files
	regexp.MustCompile(`[~]$`),
	regexp.MustCompile(`^(\.|\.\.)$`),
	regexp.MustCompile(`\.(out|stdout|err|stderr|depend|dropbox)`),
	regexp.MustCompile(`\.(log|old|temp|bak|tmp)$`),
	regexp.MustCompile(`\.(DS_Store|nfs)$`),
	regexp.MustCompile(`\.(o|obj|so|a|lib|sym|dylib|rlib|rmeta|dll|exe|pdb|ilk|exp|gch|pch|d)$`),
	regexp.MustCompile(`\.(class|jar|war|ear)$`),
	regexp.MustCompile(`\.(git|hg|svn|cvs)(/|$)`),
	regexp.MustCompile(`\.tmp\.\d+$`),
	regexp.MustCompile(`\.tmp\.\d+\.\d+$`),
	regexp.MustCompile(`\.(venv|pytest_cache|playwright-mcp)/`),
	regexp.MustCompile(`node_modules/`),
	regexp.MustCompile(`target/debug/`),
	regexp.MustCompile(`target/release/`),
	regexp.MustCompile(`/\.gradle/`),
	regexp.MustCompile(`/\.xcworkspace/`),
	regexp.MustCompile(`/DerivedData/`),
	regexp.MustCompile(`/\.build/`),
	regexp.MustCompile(`/dist/`),
	regexp.MustCompile(`/build/`),
	regexp.MustCompile(`/out/`),
	regexp.MustCompile(`/\.cache/`),
	regexp.MustCompile(`/\.next/`),
	regexp.MustCompile(`/\.nuxt/`),
	regexp.MustCompile(`/\.output/`),
	regexp.MustCompile(`/\.npm/`),
	regexp.MustCompile(`/\.yarn/`),
	regexp.MustCompile(`/bower_components/`),
	regexp.MustCompile(`/\.pnpm-store/`),
	regexp.MustCompile(`/\.idea/`),
	regexp.MustCompile(`/\.vscode/`),
	regexp.MustCompile(`\.iml$`),
	regexp.MustCompile(`Thumbs\.db$`),
	regexp.MustCompile(`desktop\.ini$`),
	regexp.MustCompile(`/\.Spotlight-V100/`),
	regexp.MustCompile(`/\.Trashes/`),
	regexp.MustCompile(`/playwright-report/`),
	regexp.MustCompile(`/test-results/`),
	regexp.MustCompile(`__pycache__`),
	regexp.MustCompile(`/venv/`),
	regexp.MustCompile(`\.(pyc|wal)$`),
	regexp.MustCompile(`pgdata/`),
	regexp.MustCompile(`\.pgdb/`),
}

var includeExtensions map[string]bool
var excludeDirs []string
var sourceRoot string

// reportedLargeFiles holds the large files already logged, a file being
// written would otherwise be reported on every event
var reportedLargeFiles = map[string]bool{}

func SetIncludeExtensions(exts string) {
	if exts == "" {
		return
	}
	includeExtensions = make(map[string]bool)
	for _, ext := range strings.Split(exts, ",") {
		ext = strings.TrimSpace(ext)
		ext = strings.TrimLeft(ext, ".")
		if ext != "" {
			includeExtensions["."+ext] = true
		}
	}
}

func SetExcludeDirs(dirs []string) error {
	excludeDirs = nil
	for _, d := range dirs {
		d = filepath.Clean(d)
		if d == "." {
			continue
		}
		if filepath.IsAbs(d) {
			return fmt.Errorf("--exclude-dir: %q must be a relative path", d)
		}
		if d == ".." || strings.HasPrefix(d, ".."+string(filepath.Separator)) {
			return fmt.Errorf("--exclude-dir: %q escapes source directory", d)
		}
		excludeDirs = append(excludeDirs, d+string(filepath.Separator))
	}
	return nil
}

func SetSourceRoot(root string) {
	sourceRoot = root
}

// IsExcludedPath returns true if the path falls under an excluded directory.
// Works for both files and directories.
func IsExcludedPath(path string) bool {
	if len(excludeDirs) == 0 || sourceRoot == "" {
		return false
	}
	rel, err := filepath.Rel(sourceRoot, path)
	if err != nil {
		return false
	}
	// Normalize: "Backup/foo.txt" or "Backup" both need to match "Backup/"
	// Adding separator ensures "Backup" matches but "Backups" does not
	rel = rel + string(filepath.Separator)
	for _, dir := range excludeDirs {
		if strings.HasPrefix(rel, dir) {
			return true
		}
	}
	return false
}

// IsExcludedDir returns true if nothing under the directory can be tracked, so
// walks and watchers can skip it entirely. The built-in patterns match full
// paths, so a pattern matching "dir/" also matches every file below it.
// --only-include-extensions bypasses the patterns: then only --exclude-dir applies.
func IsExcludedDir(path string) bool {
	if IsExcludedPath(path) {
		return true
	}
	if len(includeExtensions) > 0 {
		return false
	}
	dirPath := path + string(filepath.Separator)
	for _, pattern := range excludePatterns {
		if pattern.MatchString(dirPath) {
			return true
		}
	}
	return false
}

// ShouldTrack returns true if the file should be tracked based on filters.
// maxSizeMB is the maximum file size in megabytes (0 = no limit)
// isDelete should be true if checking a deleted file (skips file existence checks)
func ShouldTrack(path string, maxSizeMB int64, isDelete bool) (bool, error) {
	// Path filters first: they need no file access, and excluded files are
	// often temporary ones already gone (e.g. build outputs)
	if !matchesPathFilters(path) {
		return false, nil
	}

	// For delete events, rely on the path filters only
	if isDelete {
		return true, nil
	}

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Removed before its event was handled, its delete event follows
			if debug {
				logSkip("[%s] skipping vanished file: %s\n", nowTS(), path)
			}
			return false, nil
		}
		return false, err
	}

	// Exclude directories
	if info.Mode().IsDir() {
		return false, nil
	}

	// Check file size
	if maxSizeMB > 0 {
		maxBytes := maxSizeMB * 1024 * 1024
		if info.Size() > maxBytes {
			if !reportedLargeFiles[path] {
				reportedLargeFiles[path] = true
				logSkip("[%s] skipping large file: %s (%d MB > %d MB)\n",
					nowTS(), path, info.Size()/1024/1024, maxSizeMB)
			}
			return false, nil
		}
	}

	return true, nil
}

// matchesPathFilters applies the filters that only look at the path:
// --exclude-dir, the source root marker, then --only-include-extensions or
// the built-in patterns
func matchesPathFilters(path string) bool {
	if IsExcludedPath(path) {
		return false
	}

	// A marker at the source root would overwrite the destination's marker
	if sourceRoot != "" && path == filepath.Join(sourceRoot, markerFileName) {
		return false
	}

	if len(includeExtensions) > 0 {
		return includeExtensions[filepath.Ext(path)]
	}

	for _, pattern := range excludePatterns {
		if pattern.MatchString(path) {
			return false
		}
	}

	return true
}

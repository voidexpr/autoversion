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
var includeFiles []string
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

// cleanRelativePath cleans a path given with flag, which must be relative to
// the source and stay inside it
func cleanRelativePath(flag, p string) (string, error) {
	p = filepath.Clean(p)
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("%s: %q must be a relative path", flag, p)
	}
	if p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: %q escapes source directory", flag, p)
	}
	return p, nil
}

func SetExcludeDirs(dirs []string) error {
	excludeDirs = nil
	for _, d := range dirs {
		d, err := cleanRelativePath("--exclude-dir", d)
		if err != nil {
			return err
		}
		if d == "." {
			continue
		}
		excludeDirs = append(excludeDirs, d+string(filepath.Separator))
	}
	return nil
}

func SetIncludeFiles(files []string) error {
	includeFiles = nil
	for _, f := range files {
		f, err := cleanRelativePath("--only-include-file", f)
		if err != nil {
			return err
		}
		if f == "." {
			return fmt.Errorf("--only-include-file: %q is not a file path", f)
		}
		includeFiles = append(includeFiles, f)
	}
	return nil
}

func SetSourceRoot(root string) {
	sourceRoot = root
}

// relToSource returns path relative to the source root, ok false outside it
func relToSource(path string) (string, bool) {
	if sourceRoot == "" {
		return "", false
	}
	rel, err := filepath.Rel(sourceRoot, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return rel, true
}

// isIncludedFile returns true if path is one of the --only-include-file files
func isIncludedFile(path string) bool {
	rel, ok := relToSource(path)
	if !ok {
		return false
	}
	for _, f := range includeFiles {
		if rel == f {
			return true
		}
	}
	return false
}

// leadsToIncludedFile returns true if dir is the source root or contains one
// of the --only-include-file files
func leadsToIncludedFile(dir string) bool {
	rel, ok := relToSource(dir)
	if !ok {
		return false
	}
	if rel == "." {
		return true
	}
	for _, f := range includeFiles {
		if f != rel && hasPathPrefix(f, rel) {
			return true
		}
	}
	return false
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
// With --only-include-file, only the directories leading to those files are kept.
func IsExcludedDir(path string) bool {
	if IsExcludedPath(path) {
		return true
	}
	if len(includeFiles) > 0 {
		return !leadsToIncludedFile(path)
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

// tooLarge reports whether a file is over the size limit (0: no limit)
func tooLarge(size, maxSizeMB int64) bool {
	return maxSizeMB > 0 && size > maxSizeMB*1024*1024
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
	if tooLarge(info.Size(), maxSizeMB) {
		if !reportedLargeFiles[path] {
			reportedLargeFiles[path] = true
			logSkip("[%s] skipping large file: %s (%d MB > %d MB)\n",
				nowTS(), path, info.Size()/1024/1024, maxSizeMB)
		}
		return false, nil
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

	// A marker or rules file at the source root would overwrite the destination's
	if sourceRoot != "" && (path == filepath.Join(sourceRoot, markerFileName) || path == filepath.Join(sourceRoot, filterFileName)) {
		return false
	}

	// Files named explicitly bypass the extension filter and the built-in patterns
	if len(includeFiles) > 0 {
		return isIncludedFile(path)
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

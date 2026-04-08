package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
	regexp.MustCompile(`\.tmp$`),
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

func SetExcludeDirs(dirs string) error {
	excludeDirs = nil
	if dirs == "" {
		return nil
	}
	for _, d := range strings.Split(dirs, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		if d == "." {
			continue
		}
		if filepath.IsAbs(d) {
			return fmt.Errorf("--exclude-dirs: %q must be a relative path", d)
		}
		if d == ".." || strings.HasPrefix(d, ".."+string(filepath.Separator)) {
			return fmt.Errorf("--exclude-dirs: %q escapes source directory", d)
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

// ShouldTrack returns true if the file should be tracked based on filters.
// maxSizeMB is the maximum file size in megabytes (0 = no limit)
// isDelete should be true if checking a deleted file (skips file existence checks)
func ShouldTrack(path string, maxSizeMB int64, isDelete bool) (bool, error) {
	if !isDelete {
		// Get file info
		info, err := os.Lstat(path)
		if err != nil {
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
				logSkip("[%s] skipping large file: %s (%d MB > %d MB)\n",
					time.Now().Format("2006-01-02 15:04:05"), path, info.Size() / 1024 / 1024, maxSizeMB)
				return false, nil
			}
		}
	}
	// For delete events, rely on pattern matching only

	if IsExcludedPath(path) {
		return false, nil
	}

	if len(includeExtensions) > 0 {
		return includeExtensions[filepath.Ext(path)], nil
	}

	for _, pattern := range excludePatterns {
		if pattern.MatchString(path) {
			return false, nil
		}
	}

	return true, nil
}

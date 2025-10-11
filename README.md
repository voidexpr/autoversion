# Autoversion

A lightweight Go program that tracks file versions in real-time using git as the backing store. Autoversion monitors directories for changes and automatically commits them, making it easy to see what changed and restore files to any point in time.

## Features

- **Smart filtering** - Excludes log files, temp files, build artifacts, `node_modules/`, etc.
- **Efficient copying** - Uses `cp -pc` on macOS for COW optimization and timestamp preservation
- **File comparison** - Only copies files that have changed (compares mtime + size)
- **Deletion tracking** - Detects and removes deleted files from the repo
- **Retry logic** - Git operations retry indefinitely with 5s delays on failure
- **Relative paths** - Maintains directory structure relative to watch root

## Installation

### Build from source

```bash
go build -o autoversion
```

### Run tests

```bash
# Run all tests
go test

# Run tests with verbose output
go test -v
```

## Usage

### Filter Command

Scan a directory and list files that would be tracked:

```bash
./autoversion filter PATH [--max-file-size MB]
```

Example:
```bash
./autoversion filter ~/projects/myapp
./autoversion filter ~/projects/myapp --max-file-size 5
```

### Init Command

Create and initialize a git repository for versioned files:

```bash
./autoversion init --repo PATH
```

Example:
```bash
./autoversion init --repo ~/autoversion_repos/myapp
```

### Scan Command

Scan a source directory, copy filtered files to the repo, and commit changes:

```bash
./autoversion scan --repo REPO_PATH SOURCE_PATH [--max-file-size MB]
```

Example:
```bash
./autoversion scan --repo ~/autoversion_repos/myapp ~/projects/myapp
```

This command:
1. Walks the source directory and finds files to track
2. Compares each file with the version in the repo (mtime + size)
3. Copies only changed files
4. Detects files deleted from source and removes them from repo
5. Creates a git commit with message "detected N changes"

### Watch Command

Monitor a directory for changes in real-time:

```bash
./autoversion watch --repo REPO_PATH SOURCE_PATH [--buffer-time SECONDS] [--max-file-size MB]
```

Example:
```bash
./autoversion watch --repo ~/autoversion_repos/myapp ~/projects/myapp
./autoversion watch --repo ~/repos/data ~/data --buffer-time 10
```

This command:
1. Runs an initial scan to capture any missed changes
2. Starts watching the source directory for file system events
3. Buffers events for the specified duration (default: 5 seconds)
4. Collapses multiple operations on the same file (keeps last operation)
5. Periodically processes buffered events and commits changes
6. Handles SIGINT/SIGTERM gracefully (flushes buffer before exiting)

## File Filtering

Autoversion automatically excludes:

**Extensions:**
- Logs: `.log`, `.dump`, `.out`, `.stdout`, `.stderr`
- Temp files: `.swp`, `.swpx`, `.swo`, `.tmp`, `.temp`, `.bak`, `.old`, `~` suffix
- System: `.DS_Store`, `.nfs`, `.pid`, `.lock`
- Build artifacts: `.o`, `.so`, `.a`, `.sym`, `.dylib`, `.rlib`, `.rmeta`, `.pyc`, `.wal`

**Directories:**
- Version control: `.git/`, `.hg/`, `.svn/`, `.cvs/`
- Dependencies: `node_modules/`, `__pycache__/`, `.venv/`
- Build: `target/debug/`
- Test/cache: `.pytest_cache/`, `.playwright-mcp/`

**Other:**
- Directories (not tracked)
- Files ending in numeric vim temp pattern (`/\d+$`)
- Files larger than `--max-file-size` (default: 10MB)

## Configuration

### Default Values

- `--max-file-size`: 10 MB
- `--buffer-time`: 5 seconds (for watch command)

## Examples

### Track a project directory

```bash
# Initialize repo
./autoversion init --repo ~/repos/myproject_versions

# Do initial scan
./autoversion scan --repo ~/repos/myproject_versions ~/projects/myproject

# View git history
cd ~/repos/myproject_versions
git log --oneline
git diff HEAD~1 HEAD
```

### Check what files would be tracked

```bash
./autoversion filter ~/projects/myproject
```

### Exclude large files

```bash
./autoversion scan --repo ~/repos/data --max-file-size 1 ~/projects/data
```

## Implementation Status

- ✅ **Phase 1**: Filter command with comprehensive file filtering
- ✅ **Phase 2**: Init and scan commands with git integration
- ✅ **Phase 3**: Event buffer with collapsing logic
- ✅ **Phase 4**: Watch command with real-time monitoring
- ⏳ **Phase 5**: Testing and refinement (ongoing)

## Development

### Project Structure

```
autoversion/
├── main.go              # CLI entry point and commands
├── filter.go            # File filtering logic
├── filter_test.go       # Filter tests
├── buffer.go            # Event buffer with collapsing
├── buffer_test.go       # Buffer tests
├── watcher.go           # File system watcher wrapper
├── fileops.go           # File operations (copy, delete)
└── git.go               # Git wrapper with retry logic
```

All code is in a single `package main` for simplicity - this is a small tool (~800 lines) that doesn't need package separation.

### Testing Philosophy

- Unit tests for filter package cover all exclusion patterns
- Integration tests verify git operations
- Real-world testing with actual directory structures

## Design Decisions

### Why not support .gitignore?

Autoversion uses explicit filter rules rather than parsing .gitignore because:
1. Cleaner and more predictable behavior
2. No external dependencies
3. Different use case - tracking files vs. git ignoring
4. Avoids complexity of nested .gitignore files and negation patterns

### Why git as backing store?

- Familiar interface (git log, git diff)
- Mature, reliable version control
- Easy to browse history
- Standard tooling for viewing changes

### Why compare mtime + size instead of content?

- Performance - avoid reading entire files
- Good enough heuristic for detecting changes
- Editors typically update mtime on save
- Size change almost always means content change

## License

See project documentation for licensing information.

# Autoversion

A lightweight Go program that tracks file versions in real-time using git as the backing store. Autoversion monitors directories for changes and automatically commits them, making it easy to see what changed and restore files to any point in time.

## Features

- **Smart filtering** - Excludes log files, temp files, build artifacts, `node_modules/`, etc.
- **Efficient copying** - Uses `cp -pc` on macOS for COW optimization and timestamp preservation
- **File comparison** - Only copies files that have changed (compares mtime + size)
- **Deletion tracking** - Detects and removes deleted files from the target
- **Automatic path mapping** - Preserves directory structure from source to target
- **Relative paths** - Maintains directory structure relative to watch root

## File System notification Methods

* github.com/fsnotify/fsnotify
  * cross platform
  * uses kqueue on MacOS which requires 1 file handle per watched file/dir so is unusable for the current use case
  * not adding FSEvents soon: https://github.com/fsnotify/fsnotify/issues/11#issuecomment-1279133120
    * points out to another tool not super happy with FSEvents: https://watchexec.github.io/docs/macos-fsevents.html
      * but out of order / extra events are probably fine for our use case
* https://github.com/fsnotify/fsevents
  * not updated much
  * Claude is worried: not a lot of code examples, MacOS only
* CLI from brew on MacOS used by older scripts: fswatch
  * uses FSEvents and that was fine
* https://github.com/rjeczalik/notify
  * found by Claude but not updated in 2 years (as of fall 2025) and build is failing on github

### FS Events

File Content/State Change Flags

- ItemCreated (0x100): File or directory was created
- ItemModified (0x1000): File content was modified
- ItemRemoved (0x200): File or directory was removed
- ItemRenamed (0x800): File or directory was renamed

Metadata Change Flags (Don't change file content)

- ItemInodeMetaMod (0x400): Inode metadata was modified (permissions, ownership, etc.)
- ItemFinderInfoMod (0x2000): macOS Finder information was modified (label, comments, etc.)
- ItemChangeOwner (0x4000): File ownership changed
- ItemXattrMod (0x8000): Extended attributes (xattrs) were modified
- ItemCloned (0x400000): The file was cloned (macOS copy-on-write operation)

Item Type Identification Flags

- ItemIsFile (0x10000): The item is a file
- ItemIsDir (0x20000): The item is a directory
- ItemIsSymlink (0x40000): The item is a symbolic link

Special System Flags

- MustScanSubDirs (0x1): Events were coalesced - requires full directory rescan
- UserDropped (0x2): Client-side buffer overflow - events were lost
- KernelDropped (0x4): Kernel-side buffer overflow - events were lost
- EventIDsWrapped (0x8): Event ID counter wrapped (very rare)
- HistoryDone (0x10): End of historical events marker
- RootChanged (0x20): Root directory being watched changed
- Mount (0x40): Volume was mounted
- Unmount (0x80): Volume was unmounted

Your Specific Issue: flags=0x410000

Your event has:
- 0x410000 = 0x400000 (bit 22) + 0x10000 (ItemIsFile)

Looking at the hex value 0x410000:
- 0x010000 = ItemIsFile
- 0x400000 = This is an undocumented flag bit

The flag 0x400000 is NOT in the standard fsevents documentation. This could be:
1. An internal macOS flag for Dropbox/iCloud sync operations
2. A flag related to APFS features (cloning, snapshots)
3. A flag for system metadata operations


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

Initialize a git repository for a source directory:

```bash
./autoversion init --base BASE_PATH SOURCE_PATH
```

The target path is computed as: `BASE_PATH + (SOURCE_PATH - $HOME)` for paths under home, or `BASE_PATH + SOURCE_PATH` for other paths.

Example:
```bash
# Source: ~/projects/myapp -> Target: ~/autoversion_base/projects/myapp
./autoversion init --base ~/autoversion_base ~/projects/myapp

# Source: /var/log -> Target: ~/autoversion_base/var/log
./autoversion init --base ~/autoversion_base /var/log
```

### Sync Command

Sync a source directory to its target repository:

```bash
./autoversion sync [--dry-run] --base BASE_PATH SOURCE_PATH [--max-file-size MB]
```

Example:
```bash
./autoversion sync --base ~/autoversion_base ~/projects/myapp
./autoversion sync --dry-run --base ~/autoversion_base ~/projects/myapp
```

This command:
1. Computes target path from base and source
2. Walks the source directory and finds files to track
3. Compares each file with the version in the target (mtime + size)
4. Copies only changed files
5. Detects files deleted from source and removes them from target
6. Creates a git commit with message "scanned N changes"

### Watch Command

Monitor a directory for changes in real-time:

```bash
./autoversion watch [--dry-run] --base BASE_PATH SOURCE_PATH [--buffer-time SECONDS] [--max-file-size MB]
```

Example:
```bash
./autoversion watch --base ~/autoversion_base ~/projects/myapp
./autoversion watch --base ~/autoversion_base ~/projects/myapp --buffer-time 10
```

This command:
1. Computes target path from base and source
2. Runs an initial sync to capture any missed changes
3. Starts watching the source directory for file system events
4. Buffers events for the specified duration (default: 5 seconds)
5. Collapses multiple operations on the same file (keeps last operation)
6. Periodically processes buffered events and commits changes
7. Handles SIGINT/SIGTERM gracefully (flushes buffer before exiting)

## Path Computation

Autoversion automatically computes the target path from the base and source:

- **For paths under $HOME**: `target = base + (source - $HOME)`
  - Example: `~/projects/myapp` with base `~/repos` → target `~/repos/projects/myapp`

- **For paths outside $HOME**: `target = base + source`
  - Example: `/var/log` with base `~/repos` → target `~/repos/var/log`

This design allows you to track multiple source directories under a single base repository.

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
- All symlinks (both files and directories)
- Directories (not tracked directly)
- Files ending in numeric vim temp pattern (`/\d+$`)
- Files larger than `--max-file-size` (default: 10MB)

## Configuration

### Default Values

- `--max-file-size`: 10 MB
- `--buffer-time`: 5 seconds (for watch command)

### Dry Run Mode

Use `--dry-run` with sync or watch commands to see what would be done without actually executing:
- Shows all copy/delete operations
- Skips actual file operations and git commits
- Only shows warnings (not errors) for uninitialized targets

## Examples

### Track a project directory

```bash
# Initialize target
./autoversion init --base ~/repos ~/projects/myproject

# Do initial sync
./autoversion sync --base ~/repos ~/projects/myproject

# View git history
cd ~/repos/projects/myproject
git log --oneline
git diff HEAD~1 HEAD
```

### Track multiple projects under one base

```bash
# Initialize base once
mkdir ~/repos

# Track different projects
./autoversion init --base ~/repos ~/projects/webapp
./autoversion init --base ~/repos ~/projects/backend
./autoversion init --base ~/repos ~/dev/scripts

# All tracked under ~/repos with preserved structure:
# ~/repos/projects/webapp/
# ~/repos/projects/backend/
# ~/repos/dev/scripts/
```

### Check what files would be tracked

```bash
./autoversion filter ~/projects/myproject
```

### Dry run before syncing

```bash
./autoversion sync --dry-run --base ~/repos ~/projects/myproject
```

## Implementation Status

- ✅ **Phase 1**: Filter command with comprehensive file filtering
- ✅ **Phase 2**: Init and sync commands with git integration
- ✅ **Phase 3**: Event buffer with collapsing logic
- ✅ **Phase 4**: Watch command with real-time monitoring
- ✅ **Phase 5**: Automatic path computation and repository organization
- ⏳ **Phase 6**: Testing and refinement (ongoing)

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
└── git.go               # Git wrapper
```

All code is in a single `package main` for simplicity - this is a small tool that doesn't need package separation.

### Testing Philosophy

- Unit tests for filter package cover all exclusion patterns
- Integration tests verify git operations
- Real-world testing with actual directory structures

## Design Decisions

### Why automatic path computation?

The automatic path computation (`base + (source - $HOME)`) provides several benefits:
1. Organizes all tracked directories under one base
2. Preserves meaningful directory structure
3. Makes it easy to track multiple projects
4. Simplifies backup and management

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

## Implementation issues

### File rename

Given this trace:

DEBUG: fsevents: raw event path=/Users/nicolas/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md.tmp.18812.1760578375713, flags=0x19900, ID=235348040, mapped type: %!s(main.EventType=2), flags decoded: [ItemCreated ItemRenamed ItemModified ItemXattrMod ItemIsFile]
DEBUG: fsevents: event sent to channel
DEBUG: fsevents: raw event path=/Users/nicolas/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md, flags=0x10800, ID=235348041, mapped type: %!s(main.EventType=2), flags decoded: [ItemRenamed ItemIsFile]
DEBUG: fsevents: event sent to channel
[2025-10-15 18:32:55] event skip   deleted  /Users/nicolas/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md.tmp.18812.1760578375713
[2025-10-15 18:32:55] event BUFFER deleted  /Users/nicolas/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md
DEBUG: fsevents: raw event path=/Users/nicolas/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md, flags=0x410400, ID=235348192, mapped type: %!s(main.EventType=4), flags decoded: [ItemInodeMetaMod ItemIsFile ItemCloned]
DEBUG: fsevents: event can be ignored (only type flags)
DEBUG: fsevents: raw event path=/Users/nicolas/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md, flags=0x418400, ID=235348524, mapped type: %!s(main.EventType=4), flags decoded: [ItemInodeMetaMod ItemXattrMod ItemIsFile ItemCloned]
DEBUG: fsevents: event can be ignored (only type flags)
[2025-10-15 18:33:01] remove '/private/tmp/autoversion_test_new/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md'
git commad in /private/tmp/autoversion_test_new/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor args: [rm -- PLAN_PAGINATION.md]
fatal: pathspec 'PLAN_PAGINATION.md' did not match any files
Error:  git rm failed: exit status 128

Here is an example of a file rename:

EBUG: fsevents: raw event path=/Users/nicolas/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md.tmp.18812.1760578375713, flags=0x19900, ID=235348040, mapped type: %!s(main.EventType=2), flags decoded: [ItemCreated ItemRenamed ItemModified ItemXattrMod ItemIsFile]
DEBUG: fsevents: event sent to channel
DEBUG: fsevents: raw event path=/Users/nicolas/Library/CloudStorage/Dropbox/nicmac/projects/duckdb-editor/PLAN_PAGINATION.md, flags=0x10800, ID=235348041, mapped type: %!s(main.EventType=2), flags decoded: [ItemRenamed ItemIsFile]
DEBUG: fsevents: event sent to channel

To figure out source/target or rename it's required to check which file exists or not and make a guess (race conditions always possible)

## License

See project documentation for licensing information.

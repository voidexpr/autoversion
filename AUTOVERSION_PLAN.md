# Autoversion

## Goal

A simple golang program to track file version in real time as they are edited so it's easy to see what changes were made and what a file was at a point in time.
Use git as the repository of these versions for familiarity.

## Terminology

* **source** (`--source`): the directory being monitored
* **destination** (`--destination`): the directory files are mirrored into, a git repository unless `--no-git`. It is marked by a `.autoversion` file naming its source, written by `init`

The design and implementation phases further down use the original names (`--repo`, `scan`, target repo). The current commands are documented in README.md.

## CLI

autoversion filter --source PATH

    scan recursively all the files under the path provided and print the files to be versioned based on internal filters. Do not copy anything

autoversion init --source PATH1 --destination PATH2

    create PATH2 if doesn't exist
    run git init . in PATH2
    write PATH2/.autoversion naming PATH1 and commit it

autoversion sync --source PATH1 --destination PATH2

    process all files from PATH1, filter them as needed and copy them into PATH2, do git commit with 'scanned files' as the message. Refuses to run unless PATH2/.autoversion names PATH1

autoversion watch --source PATH1 --destination PATH2 [--buffer-time SECONDS]

    monitor for changes in PATH1, if the file is matched by internal filters copy to PATH2 and periodically run git add/git commit. Also handle deleted files (delete in repo)

    to end the process: CONTROL-C or send SIGTERM.

    Note that before watching for changes the 'sync' command is ran to pickup any changes that might have been forgotten since last time changes were watched.

    --buffer-time SECONDS  (default: 3)

        how long to wait when a file is changed before copying it out. This allows to collapse multiple edits or avoiding doing a delete/copy when a file is overwritten.

### Options for watch and filter

--max-file-size XXX

    don't track files more than this number (unit is megabytes)

## Specification

Basic scenarios:
* a file is added: we want to copy it to the target git repo
* a file is modified: want copy the file to the target git repo

We need to handle the following special scenario:
* text editor delete and re-create the same file: we want to detect a simple file change
* temporary files are created and deleted quickly: no need to capture them
* a file is deleted: we want to delete it in the target git repo

## Implementation

The basic approach is to buffer changed files in memory and every X seconds copy them to the target to avoid too frequent changes and git commit at the target.

The memory buffering should be a list of:
* filename, new|deleted|updated, timestamp

Then at a given timer all files whose timestamp is older than X seconds are collapsed into a single operation (i.e. dedup on filename and keep last operation): copy or delete in target repo.

For example if file1 had an update, a deletion, another update then there is one operation to perform: copy. But if the last operation is a delete then the file in the target repo should be deleted (`git rm`).

### Filtering

It is important to only track files that matter. So we want to exclude anything matching this Perl regexp:

```perl
     /\.(log|dump|swp|swpx|swo|pid|lock|beam|bea#)$/
||  /\/\d+$/  # vim/fs temp files
|| /[~]$/
|| /^(\.|\.\.)$/
|| /\.(out|stdout|err|stderr|depend|dropbox)/
|| /\.(log|old|temp)$/
|| /.(DS_Store|nfs)$/
|| /\.(o|so|a|sym)$/
|| /\.(git|hg|svn|cvs)\//
|| /\.(venv|pytest_cache|playwright-mcp)/
|| /node_modules\//
|| /__pycache__/
|| /\.(pyc|wal)$/
|| -d
```

We also want to exclude any file bigger than 10MB (configurable on the CLI)

### Git command issues

If a git command fails then the best is to retry later. Some possible failures:
* git add might fail because the file is in .gitignore
    * best is to ignore and handle the other files and do a git commit
* git commit can fail if there is a pending one
    * best is to wait and retry later
    * use ps to figure out which process tree might be running a command in the same directory for investigation

### Copy optimization

On MacOS the plan is to use 'cp -pc' so that timestamps are preserved (-p) and the file doesn't take more space on the file system by cloning it (-c).

## Design

### Architecture

The program will be structured into these main components:

1. **CLI Layer** (`main.go`)
   - Command parsing using `flag` package (stdlib)
   - Commands: `filter`, `init`, `scan`, `watch`
   - Configuration management (repo path from `--repo`, buffer time, max file size)
   - Error/skip messages logged to stderr

2. **File Filtering** (`filter/filter.go`)
   - Regex-based exclusion rules (using `regexp` package)
   - File size checking
   - Directory detection
   - Single responsibility: determine if a file should be tracked

3. **File System Watcher** (`watcher/watcher.go`)
   - Use `github.com/fsnotify/fsnotify` (cross-platform, well-maintained)
   - Recursively watch source directory
   - Emit file events: Created, Modified, Deleted
   - Filter events early to avoid buffering unwanted files

4. **Event Buffer** (`buffer/buffer.go`)
   - In-memory slice: `[]Event{filepath, action, timestamp}`
   - Simplified: can just keep last operation per file (overwrite in map internally)
   - Timer-based flushing (configurable buffer-time)
   - Event collapsing on flush:
     - Only return events older than buffer-time threshold
     - For each unique filepath, keep only the last operation
     - Take action based on last operation type (copy for Create/Modified, git rm for Delete)
   - Thread-safe (use mutex)

5. **Git Operations** (`git/git.go`)
   - Wrapper around git commands via `os/exec`
   - Operations: init, add, commit, rm
   - Retry logic: wait 5s and retry indefinitely on failure
   - Check for pending commits before attempting new ones
   - Ignore .gitignore failures on `git add` (skip those files, continue with others)
   - All commit messages: "detected N changes"

6. **File Operations** (`fileops/fileops.go`)
   - Copy files from source to repo (relative paths from watch root)
   - Use `cp -pc` on macOS for timestamp preservation and COW optimization
   - Use fallback copy on other platforms
   - Delete files from target repo
   - Create parent directories as needed

### Data Flow

#### Filter Command
```
source path → recursive walk → filter check → print matching files
```

#### Init Command
```
validate repo path → create if needed → git init
```

#### Scan Command
```
1. Walk source path, apply filter
2. For each source file: compare with target (filename, mtime, size)
   - If not in target OR different → copy to target
3. Walk target repo
   - If file not in source → git rm from target
4. git add . → git commit "detected N changes"
```

#### Watch Command
```
1. Run 'scan' command first to capture any missed changes
2. Start watching:
   source path → fsnotify events → filter → buffer (append to array)
                                               ↓
                                       timer triggers (every buffer-time)
                                               ↓
                                       flush: sort by file+timestamp, keep last op per file
                                               ↓
                                       copy/delete files (relative paths)
                                               ↓
                                       git add affected paths (retry on failure)
                                               ↓
                                       git commit "detected N changes" (retry on failure)
```

### External Dependencies

- `github.com/fsnotify/fsnotify` - File system notifications (cross-platform)
  - Mature, 9k+ stars, used by major projects (Kubernetes, Hugo, etc.)
  - Supports Linux (inotify), macOS (FSEvents), Windows (ReadDirectoryChangesW)

All other functionality uses Go stdlib:
- `flag` - CLI parsing
- `os`, `os/exec` - File operations and git commands
- `path/filepath` - Path manipulation
- `regexp` - Filter patterns
- `sync` - Mutex for buffer
- `time` - Timers and timestamps

## Plan

### Implementation Phases

#### Phase 1: Core Infrastructure & Filter Command
**Goal:** Establish project structure and implement file filtering logic

1. Initialize Go module and project structure
   ```
   autoversion/
   ├── main.go
   ├── filter/
   │   └── filter.go
   └── fileops/
       └── fileops.go
   ```

2. Implement `filter` package
   - Compile regex patterns from specification
   - `ShouldTrack(path string, maxSize int64) bool` function
   - Unit tests for all filter rules (test files ending in _test.go)

3. Implement `filter` command
   - Parse CLI args (PATH, optional --max-file-size)
   - Recursively walk directory
   - Apply filters and print matching files
   - Test with various directory structures

**Deliverable:** `autoversion filter PATH` works correctly

#### Phase 2: Git Wrapper & Init/Scan Commands
**Goal:** Git operations and basic file copying

1. Implement `git` package
   - `Init(repoPath string) error`
   - `Add(repoPath string, paths []string) error` - handles .gitignore errors gracefully
   - `Commit(repoPath string, message string) error` - checks for pending commits
   - `Remove(repoPath string, paths []string) error`
   - Retry logic: sleep 5s and retry indefinitely on commit/add failures

2. Implement `fileops` package
   - `CopyFile(src, dst, repoRoot string) error` - copies to relative path under repoRoot
   - `CopyFileOptimized(src, dst string) error` - uses `cp -pc` on macOS
   - `FileInfo(path string) (mtime, size, exists)` - for comparison in scan
   - `DeleteFile(path string) error`
   - `EnsureDir(path string) error`

3. Implement `init` command
   - Parse `--repo PATH`
   - Create directory if needed
   - Call git.Init()

4. Implement `scan` command
   - Parse `--repo PATH1 scan PATH2`
   - Walk PATH2 with filter, collect matching files
   - For each file: compare mtime+size with target, copy if different
   - Walk PATH1 to find files not in PATH2 source → git rm those
   - Git add and commit with "detected N changes" message

**Deliverable:** `autoversion init` and `autoversion scan` work correctly

#### Phase 3: Event Buffer & File Change Detection
**Goal:** Buffering logic for collapsing file events

1. Implement `buffer` package
   - Event types: Created, Modified, Deleted
   - Event struct: `{Path string, Action EventType, Timestamp time.Time}`
   - `EventBuffer` struct with mutex and map: `map[string]Event`
   - `Add(path string, eventType EventType)` - overwrites existing entry (keeps last)
   - `Flush(olderThan time.Duration) []Event` - returns events older than threshold
     - Returns slice of events where timestamp < now - olderThan
     - Removes returned events from internal map
   - Unit tests for add/flush behavior

2. Timer-based flushing
   - Configurable flush interval (buffer-time)
   - Only flush events older than buffer-time

**Deliverable:** Buffer package with comprehensive tests

#### Phase 4: Watch Command & File System Monitoring
**Goal:** Real-time file monitoring with buffering

1. Implement `watcher` package
   - Initialize fsnotify watcher
   - Recursively add directories to watch
   - Handle new directory creation (add to watch list dynamically)
   - Apply filter early to avoid buffering unwanted files
   - Channel-based event emission

2. Implement `watch` command
   - Parse `--repo PATH2 watch PATH1 --buffer-time SECONDS`
   - First run `scan` internally to capture missed changes
   - Initialize watcher on PATH1
   - Receive events and add to buffer (thread-safe)
   - Timer goroutine to flush buffer every buffer-time seconds
   - For each batch of flushed events:
     - Copy/delete files in PATH2 (relative paths from PATH1)
     - Collect all affected paths
     - Git add affected paths (retry every 5s on failure)
     - Git commit "detected N changes" (retry every 5s on failure)
   - Handle SIGINT/SIGTERM gracefully (flush buffer, final commit)

3. Signal handling
   - Catch CTRL-C and SIGTERM
   - Flush all remaining events
   - Final git commit if any changes
   - Clean exit

**Deliverable:** `autoversion watch` works with buffering and git commits

#### Phase 5: Testing & Refinement

1. Integration tests
   - Create test scenarios with file creation/modification/deletion
   - Verify git history is correct
   - Test edge cases (rapid edits, editor behavior)

2. Performance testing
   - Large directory trees
   - High frequency of changes
   - Memory usage monitoring

3. Documentation
   - Usage examples in README.md
   - Document all commands and options

**Deliverable:** Tested, documented tool ready for use

### Development Notes

- Each phase should be completed and tested before moving to next
- Run `go test ./...` after each package implementation
- Keep commits small and focused
- Use `--max-file-size` default of 10MB unless overridden
- Use `--buffer-time` default of 5 seconds unless overridden





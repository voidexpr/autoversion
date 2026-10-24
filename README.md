# Autoversion

A lightweight Go tool that tracks file versions in real-time using git as the backing store. Autoversion monitors directories for changes and automatically commits them, making it easy to see what changed and restore files to any point in time.

## Quick Start

### Prerequisites

- Go ≥ 1.25 (for building from source, required by `go.mod`)
- macOS: Xcode Command Line Tools (clang, used for FSEvents and to build the launcher app)
- git (required at runtime)
- restic (optional, for backup functionality)

### Install as a background service (macOS)

```bash
# Build
make build

# Initialize the destination once, then install and start watching
./autoversion init --source ~/projects/myapp --destination ~/repos/myapp
./autoversion install --source ~/projects/myapp --destination ~/repos/myapp

# Grant Full Disk Access (required for some directories):
# System Settings > Privacy & Security > Full Disk Access
# Add: ~/Applications/Autoversion.app
```

The `install` command automatically:
- Creates `~/Applications/Autoversion.app`, a small signed launcher that runs the autoversion binary (built by `make-fda-app.sh`, needs clang from the Xcode Command Line Tools)
- Creates and starts the launchd service, which runs autoversion through the app so it inherits the app's Full Disk Access
- On subsequent runs: reloads the service if the binary or flags changed

Rebuilding the binary in place keeps the app and its Full Disk Access grant. The app is only rebuilt, and needs a new grant, if the binary moves to another path.

### Managing the service

```bash
# Check status
./autoversion status --source ~/projects/myapp --destination ~/repos/myapp

# Uninstall
./autoversion uninstall --source ~/projects/myapp --destination ~/repos/myapp
```

## Development Workflow

When modifying autoversion with running services:

```bash
# Rebuild and reinstall (auto-reloads if service is running)
make build && ./autoversion install --source ~/projects/myapp --destination ~/repos/myapp
```

The install command detects when the binary or configuration flags have changed and automatically reloads running services.

### Build and test

```bash
make build
make test
make clean
make install    # copies binary to /usr/local/bin
make uninstall  # removes from /usr/local/bin
make watch-check  # real watch on a temp source (git and --no-git): file and directory changes, no copy loop
```

To try the Linux (inotify) watcher from macOS, `make docker-test` builds the image, then runs `init` and `watch` in a container. `SOURCE` (default `test_watch`) is mounted read-only at `/source`, `DESTINATION` (default: a new `/tmp/autoversion_docker_test/<timestamp>-<random>` directory) at `/destination`. Stop it with Ctrl+C.

```bash
make docker-test
make docker-test SOURCE=~/projects/myapp DESTINATION=/tmp/myapp_copy
make docker-shell SOURCE=~/projects/myapp   # bash with the Go toolchain, same mounts (both optional)
```

To run the Go tests, or any command, on Linux against the current source (mounted at `/src`, no image rebuild after code changes), build the `autoversion-dev` image once, and again when `go.mod` changes:

```bash
make docker-dev-build
make docker-go-test                                   # go test ./... on Linux
make docker-go-test GOTEST_ARGS='-v -run TestShouldTrack'
make docker-sh CMD='go build -o /tmp/av . && /tmp/av filter --source /src'
make docker-watch-check                               # watch-check on Linux
```

`docker-go-test` and `docker-sh` only run containers. They set a git identity, keep the Go build cache in the `autoversion-go-cache` volume, and pass `CMD` to `sh -c` as typed.

### Manual commands (for debugging)

```bash
# Create/update the launcher app manually
./autoversion bundle

# Remove the launcher app
./autoversion unbundle
```

## Directory Mirroring (without git)

Use `--no-git` to maintain a filtered copy of a directory without any git versioning. This is useful when you just want a clean mirror that excludes build artifacts, temp files, and other noise.

```bash
# Initialize the mirror once (writes the marker, no git repository)
./autoversion init --no-git --source ~/projects/myapp --destination /tmp/mirror/myapp

# One-time mirror
./autoversion sync --no-git --source ~/projects/myapp --destination /tmp/mirror/myapp

# Real-time mirror (watches for changes)
./autoversion watch --no-git --source ~/projects/myapp --destination /tmp/mirror/myapp

# Install as a background service
./autoversion install --no-git --source ~/projects/myapp --destination /tmp/mirror/myapp
```

The same file filtering rules apply — symlinks, large files, `node_modules/`, `.git/`, etc. are all excluded.

Combine with `--only-include-extensions` to mirror only specific file types:

```bash
# Mirror only documentation and data files
./autoversion sync --no-git --only-include-extensions md,csv,txt --source ~/projects/myapp --destination /tmp/mirror/myapp
```

### Excluding directories

Use `--exclude-dir` (repeatable) to skip specific subdirectories. Paths are relative to the source directory:

```bash
# Watch ~/SyncDatabox but skip Backup and archives
./autoversion watch --exclude-dir Backup --exclude-dir archives --source ~/SyncDatabox --destination ~/repos/SyncDatabox

# Nested paths work too
./autoversion sync --exclude-dir nicmac/local_projects --exclude-dir tmp/cache --source ~/SyncDatabox --destination ~/repos/SyncDatabox

# Combine with other flags
./autoversion install --exclude-dir Backup --only-include-extensions md,csv --source ~/SyncDatabox --destination ~/repos/SyncDatabox
```

Only top-level matches are excluded: `--exclude-dir Backup` with source `~/foo` skips `~/foo/Backup` but not `~/foo/nested/Backup`. For the nested case, pass `--exclude-dir nested/Backup`.

Excluded directories are skipped entirely — no files are copied, no watches are set up, and no events are processed for anything under them. The flag is persisted in launchd plists, so `install` preserves it across reboots.

Re-running `install` with different `--exclude-dir` values automatically updates the service configuration and reloads it.

Paths must be relative and cannot escape the source directory. Absolute paths (`/tmp`) and parent traversals (`../outside`) are rejected.

## Commands

### install

Install as a launchd background service. The destination must be initialized for the source first (see **init**):

```bash
./autoversion install --source SOURCE_PATH --destination DEST_PATH [flags]
```

Flags: `--max-file-size MB`, `--buffer-time SECONDS`, `--no-git`, `--restic`, `--no-login`, `--dry-run`, `--debug`, `--exclude-dir` (repeatable), `--only-include-extensions`

`install` prints the same path resolution and checks as **check-paths**, runs an initial sync, then writes and loads the plist (`watch --source SOURCE_PATH --destination DEST_PATH --log-dir …`). The job ID is built from the source and destination paths. The command is idempotent — run it again after rebuilding or changing flags to update and reload the service.

**Logs.** The service writes `~/Library/Logs/autoversion/<job>/stdout.log` and `stderr.log` (`watch --log-dir`, set by `install`). Each file is rotated once it reaches 10 MB, keeping 5 old copies (`stdout.log.1` … `stdout.log.5`, `.1` being the most recent). Sizes are checked every minute. A log file removed by hand is recreated within a minute. Reloading the service through `install` clears `stderr.log` and its rotated copies. Filtered-out events are only logged with `--debug`, and a file over `--max-file-size` is reported once per run.

**Restart policy.** The service is configured with `KeepAlive={SuccessfulExit=false}` and `ThrottleInterval=21600`, so launchd relaunches only on non-zero exits and at most once every 6 hours. A healthy service that crashes after hours of uptime relaunches immediately (the throttle is measured spawn-to-spawn). A broken service attempting to crash-loop is capped at four auto-restart attempts per day. Explicit `launchctl unload && load` bypasses the throttle.

### uninstall

Stop and remove the launchd service:

```bash
./autoversion uninstall --source SOURCE_PATH --destination DEST_PATH
```

`uninstall`, `status` and `install-check` only use the paths to find the job, so they work after the source or destination is gone.

### status

Without flags, list all services with their source and destination. With `--source` and `--destination`, show the service status, uptime, and log locations:

```bash
./autoversion status
./autoversion status --source SOURCE_PATH --destination DEST_PATH
```

### install-check

Show detailed service status including PID, uptime, and recent log output:

```bash
./autoversion install-check --source SOURCE_PATH --destination DEST_PATH
```

### Linux

The `install`, `uninstall`, and `status` commands are macOS-only — they rely on launchd. On Linux, run autoversion in the foreground instead:

```bash
./autoversion init --source ~/projects/myapp --destination ~/repos/myapp
./autoversion watch --source ~/projects/myapp --destination ~/repos/myapp
```

Use systemd, screen, or tmux to keep it running in the background.

#### inotify watch limit

On Linux, `watch` uses inotify, which needs one watch per directory (excluded directories don't use one). The number of watches per user, shared by all of that user's processes, is limited by `fs.inotify.max_user_watches` (often 8192). When the limit is reached, `watch` stops with an error giving the limit and how many directories still need a watch, at startup or when a new directory is created later. Check and raise the limit:

```bash
cat /proc/sys/fs/inotify/max_user_watches
find SOURCE_PATH -type d | wc -l    # upper bound, also counts excluded directories
sudo sysctl -w fs.inotify.max_user_watches=524288
```

Each watch in use takes about 1 KB of kernel memory. `sysctl -w` doesn't survive a reboot: put `fs.inotify.max_user_watches=524288` in `/etc/sysctl.d/`, or on Synology DSM add a triggered boot-up task in Task Scheduler (user root) that runs the `sysctl -w` command. Alternatively, skip large directories with `--exclude-dir`.

### watch

Run in foreground (useful for debugging):

```bash
./autoversion watch --source SOURCE_PATH --destination DEST_PATH [--dry-run] [--buffer-time SECONDS] [--log-dir DIR]
```

A directory moved into, out of, or within the source, or created (Linux), is rescanned: its files are copied, and destination files no longer in the source are removed.

The destination's marker is checked at startup only. If the destination disappears while watching (renamed, volume unmounted), `watch` exits with `destination disappeared: DEST_PATH` instead of recreating it; under launchd the restarted service then fails at the startup check until the destination is back.

With `--log-dir DIR`, stdout and stderr go to `DIR/stdout.log` and `DIR/stderr.log`, rotated by size (see **Logs** under `install`). Filtered-out events (`event skip`) are only printed with `--debug`, and so are files removed before their event is handled (`skipping vanished file`, common with build outputs).

Only print file events, without copying anything (no `--destination`, no initial scan). Each event is shown as `track` (passes the filters) or `skip`; add `--debug` to also see the raw FSEvents flags:

```bash
./autoversion watch --report-only --source SOURCE_PATH
```

This can be used instead of `inotifywait -m -r` (Linux) or `fswatch` (macOS) to see what changes under a directory, with autoversion's filters (`--exclude-dir`, `--only-include-extensions`, `--max-file-size`, built-in exclusions) applied, and to check what a `watch` would pick up before installing it.

### sync

One-time sync without watching:

```bash
./autoversion sync --source SOURCE_PATH --destination DEST_PATH [--dry-run]
```

### init

Initialize a destination for a source. Files under `SOURCE_PATH` are mirrored directly into `DEST_PATH` (`SOURCE_PATH/rel` → `DEST_PATH/rel`):

```bash
./autoversion init --source SOURCE_PATH --destination DEST_PATH [--no-git] [--restic]
    [--force-new-source | --force-obliterate-yes-i-am-sure]
```

`init` creates the destination if needed, runs `git init` (unless `--no-git`), initializes restic (with `--restic`), and writes the `.autoversion` marker naming the source (see **Destination Marker**). With git, the marker is committed on its own (`init: source SOURCE_PATH`).

| destination | default | `--force-new-source` | `--force-obliterate-yes-i-am-sure` |
|---|---|---|---|
| missing or empty | initialize | initialize | initialize |
| marker names this source | nothing to do | nothing to do | wipe, initialize |
| marker names another source | error | rewrite the marker, keep contents and `.git` | wipe, initialize |
| not empty, no marker | error | write the marker, keep contents and `.git` | wipe, initialize |

- After `--force-new-source` the next sync makes the destination match the source: files not in the source are removed (`git rm`, kept in history; with `--no-git` deleted for good).
- `--force-obliterate-yes-i-am-sure` removes everything inside the destination, `.git` included. The restic repository next to it is not touched.
- The two flags are mutually exclusive.

### filter

List files that would be tracked:

```bash
./autoversion filter --source SOURCE_PATH [--max-file-size MB]
```

### check-paths

Validate source and destination paths before installing:

```bash
./autoversion check-paths --source SOURCE_PATH --destination DEST_PATH
```

Shows symlink resolution and the marker status, and validates:
- Source and destination can't be the same, `$HOME` or `/`
- Neither path can be inside the other
- The source can't contain a `.autoversion` marker (catches swapped `--source` / `--destination`)
- No parent of the destination can contain a `.autoversion` marker (an outer destination's sync would delete the inner one's files), or a `.git` repository unless `--no-git` (prevents nested repos)
- The destination must have a marker naming the source (and a restic repository with `--restic`)

## Destination Marker

`init` writes `DEST_PATH/.autoversion`, a small JSON file naming the source (absolute path, symlinks resolved):

```json
{
  "source": "/Users/me/projects/myapp"
}
```

`sync`, `watch` and `install` refuse to copy anything unless the marker exists and names their source. This catches typos in either path (a mistyped destination is never silently created) and prevents two sources from syncing into the same destination. The marker is never deleted or overwritten by a sync, and a `.autoversion` file at the root of a source is not tracked. With git, the marker is versioned like the other files, so `git log -p .autoversion` shows which source the destination belonged to over time.

## File Filtering

Autoversion excludes:

**Extensions:** `.log`, `.dump`, `.swp`, `.tmp`, `.bak`, `.pyc`, `.o`, `.so`, `.DS_Store`, etc.

**Directories:** `.git/`, `node_modules/`, `__pycache__/`, `.venv/`, `target/debug/`

**Other:** Symlinks, files > 10MB (configurable), vim temp files

Directories matched by these exclusions or by `--exclude-dir` are skipped entirely: scans don't walk into them, and on Linux they don't use an inotify watch. With `--only-include-extensions` the built-in exclusions don't apply, so only `.git` and `--exclude-dir` directories are skipped.

**Single files:** `--only-include-file PATH` (repeatable, relative to the source) tracks only those files, even if a built-in exclusion matches them; `--max-file-size` still applies. Only the directories leading to them are scanned and, on Linux, watched. It can't be combined with `--exclude-dir` or `--only-include-extensions`. Removing a file from the list removes its copy at the next sync (kept in git history). For example, to version one file of a busy directory:

```bash
./autoversion init --source ~/.claude --destination ~/backup/claude_settings
./autoversion install --only-include-file settings.json --source ~/.claude --destination ~/backup/claude_settings
```

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--source` | (none) | Source directory to track (all commands except `bundle`/`unbundle`; optional for `status`) |
| `--destination` | (none) | Destination directory, initialized by `init` (not used by `filter` and `watch --report-only`) |
| `--force-new-source` | false | `init`: use a non-empty destination, or one initialized for another source, keeping its contents and history |
| `--force-obliterate-yes-i-am-sure` | false | `init`: remove everything in the destination, `.git` included, then initialize it |
| `--max-file-size` | 10 MB | Maximum file size to track |
| `--buffer-time` | 3 seconds | Event buffer duration (watch/install) |
| `--dry-run` | false | Show what would be done without executing |
| `--report-only` | false | Only print file events, copy nothing (watch only, no `--destination` needed) |
| `--only-include-extensions` | (none) | Only include files with these extensions (comma-separated, e.g. `md,csv,txt`) |
| `--exclude-dir` | (none) | Exclude a directory relative to source; repeatable (e.g. `--exclude-dir Backup --exclude-dir tmp`) |
| `--only-include-file` | (none) | Only track this file, relative to source; repeatable (see **Single files**) |
| `--no-git` | false | Disable git entirely (plain directory mirror) |
| `--restic` | false | Enable restic backup |
| `--no-login` | false | Don't start service at login (install only) |
| `--debug` | false | Enable debug logging, including filtered-out events |
| `--log-dir` | (none) | Write rotated `stdout.log` / `stderr.log` in this directory (watch; set by install) |

---

## Design Decisions

### Why an explicit destination and a marker?

Earlier versions took `--base` and computed the destination as `base + (source - $HOME)`, creating and `git init`-ing it on the fly. That kept several sources under one root, but the destination was implicit and a typo silently started a new repository. With an explicit `--destination` and the `.autoversion` marker:
1. The command line says where files go
2. A destination is only created by `init`, never by a sync
3. A destination belongs to one source; switching it is an explicit `init --force-new-source`

### Why git as backing store?

- Familiar interface (git log, git diff)
- Easy to browse history and restore files
- Standard tooling for viewing changes

### Why compare mtime + size instead of content?

- Performance - avoid reading entire files
- Good enough heuristic for detecting changes
- Editors typically update mtime on save

### Why not support .gitignore?

Autoversion uses explicit filter rules rather than parsing .gitignore because:
1. Cleaner and more predictable behavior
2. Different use case - tracking files vs. git ignoring
3. Avoids complexity of nested .gitignore files

## Technical Notes

### File System Notification Methods

Libraries considered:

* `github.com/fsnotify/fsnotify`
  * Cross platform
  * Uses kqueue on macOS which requires 1 file handle per watched file/dir - unusable for large directories
  * Not adding FSEvents soon: https://github.com/fsnotify/fsnotify/issues/11

* `github.com/fsnotify/fsevents` (currently used)
  * macOS only, uses FSEvents API
  * Not updated frequently but works well

* `github.com/rjeczalik/notify`
  * Not updated in 2+ years, build failing on GitHub

* `fswatch` (CLI from brew)
  * Uses FSEvents, worked well in older scripts

### Project Structure

```
autoversion/
├── main.go           # CLI entry point and commands
├── destination.go    # Path validation, .autoversion marker, init
├── filter.go         # File filtering logic
├── buffer.go         # Event buffer with collapsing
├── watcher.go        # File system watcher wrapper
├── fileops.go        # File operations (copy, delete)
├── git.go            # Git wrapper
├── restic.go         # Restic backup integration
├── launchd.go        # macOS launchd integration
├── make-fda-app.sh   # Builds the Full Disk Access launcher app (embedded in the binary)
├── log.go            # Logging helpers
└── Makefile          # Build targets
```

### FSEvents on macOS

Uses `github.com/fsnotify/fsevents` for file system monitoring.

**File Content/State Change Flags:**
- `ItemCreated` (0x100): File or directory was created
- `ItemModified` (0x1000): File content was modified
- `ItemRemoved` (0x200): File or directory was removed
- `ItemRenamed` (0x800): File or directory was renamed

**Metadata Change Flags (don't change file content):**
- `ItemInodeMetaMod` (0x400): Inode metadata was modified (permissions, ownership, etc.)
- `ItemFinderInfoMod` (0x2000): macOS Finder information was modified (label, comments, etc.)
- `ItemChangeOwner` (0x4000): File ownership changed
- `ItemXattrMod` (0x8000): Extended attributes (xattrs) were modified
- `ItemCloned` (0x400000): The file was cloned (macOS copy-on-write operation)

**Item Type Identification Flags:**
- `ItemIsFile` (0x10000): The item is a file
- `ItemIsDir` (0x20000): The item is a directory
- `ItemIsSymlink` (0x40000): The item is a symbolic link

**Special System Flags:**
- `MustScanSubDirs` (0x1): Events were coalesced - requires full directory rescan
- `UserDropped` (0x2): Client-side buffer overflow - events were lost
- `KernelDropped` (0x4): Kernel-side buffer overflow - events were lost
- `EventIDsWrapped` (0x8): Event ID counter wrapped (very rare)
- `HistoryDone` (0x10): End of historical events marker
- `RootChanged` (0x20): Root directory being watched changed
- `Mount` (0x40): Volume was mounted
- `Unmount` (0x80): Volume was unmounted

**Undocumented flag 0x400000:** Sometimes appears combined with `ItemIsFile` (e.g., flags=0x410000). This could be related to Dropbox/iCloud sync operations, APFS features (cloning, snapshots), or system metadata operations.

### Known Issues

**Changes during the initial scan** (accepted behavior): `watch` starts the watcher after its initial scan, so a file changed while the scan runs is only picked up by its next change or the next start. autoversion is meant to catch changes made by people and agents, which rarely happen in the seconds after it starts (at login or reboot). On Linux, an inotify watch limit error also shows only after the scan.

**File rename detection**: FSEvents reports renames as two separate events (old path renamed, new path renamed). The code checks which file exists to determine the operation, which can have race conditions.

Example trace showing a file rename:

```
DEBUG: fsevents: raw event path=.../PLAN.md.tmp.18812.1760578375713, flags=0x19900, mapped type: 2, flags decoded: [ItemCreated ItemRenamed ItemModified ItemXattrMod ItemIsFile]
DEBUG: fsevents: raw event path=.../PLAN.md, flags=0x10800, mapped type: 2, flags decoded: [ItemRenamed ItemIsFile]
[2025-10-15 18:32:55] event skip   deleted  .../PLAN.md.tmp.18812.1760578375713
[2025-10-15 18:32:55] event BUFFER deleted  .../PLAN.md
```

The temp file was renamed to the final file. FSEvents reports both paths with `ItemRenamed`. To determine source/target, the code checks which file exists - this can race with other operations.


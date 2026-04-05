# Autoversion

A lightweight Go tool that tracks file versions in real-time using git as the backing store. Autoversion monitors directories for changes and automatically commits them, making it easy to see what changed and restore files to any point in time.

## Quick Start

### Install as a background service (macOS)

```bash
# Build
make build

# Install and start watching a directory
./autoversion install --base ~/repos ~/projects/myapp

# Grant Full Disk Access (required for some directories):
# System Preferences > Privacy & Security > Full Disk Access
# Add: ~/Applications/Autoversion.app
```

The `install` command automatically:
- Creates the app bundle for Full Disk Access support
- Copies it to `~/Applications`
- Creates and starts the launchd service
- On subsequent runs: reloads the service if the binary or flags changed

### Managing the service

```bash
# Check status
./autoversion status --base ~/repos ~/projects/myapp

# Uninstall
./autoversion uninstall --base ~/repos ~/projects/myapp
```

## Development Workflow

When modifying autoversion with running services:

```bash
# Rebuild and reinstall (auto-reloads if service is running)
make build && ./autoversion install --base ~/repos ~/projects/myapp
```

The install command detects when the binary or configuration flags have changed and automatically reloads running services.

### Build and test

```bash
make build
make test
make clean
make install    # copies binary to /usr/local/bin
make uninstall  # removes from /usr/local/bin
```

### Manual commands (for debugging)

```bash
# Create/update app bundle manually
./autoversion bundle

# Remove app bundles
./autoversion unbundle
```

## Directory Mirroring (without git)

Use `--no-git` to maintain a filtered copy of a directory without any git versioning. This is useful when you just want a clean mirror that excludes build artifacts, temp files, and other noise.

```bash
# One-time mirror
./autoversion sync --base /tmp/mirror --no-git ~/projects/myapp

# Real-time mirror (watches for changes)
./autoversion watch --base /tmp/mirror --no-git ~/projects/myapp

# Install as a background service
./autoversion install --base /tmp/mirror --no-git ~/projects/myapp
```

The same file filtering rules apply — symlinks, large files, `node_modules/`, `.git/`, etc. are all excluded.

Combine with `--only-include-extensions` to mirror only specific file types:

```bash
# Mirror only documentation and data files
./autoversion sync --base /tmp/mirror --no-git --only-include-extensions md,csv,txt ~/projects/myapp
```

### Excluding directories

Use `--exclude-dirs` to skip specific subdirectories. Paths are relative to the source directory, comma-separated:

```bash
# Watch ~/SyncDatabox but skip Backup and archives
./autoversion watch --base /tmp/mirror --exclude-dirs Backup,archives ~/SyncDatabox

# Nested paths work too
./autoversion sync --base /tmp/mirror --exclude-dirs nicmac/local_projects,tmp/cache ~/SyncDatabox

# Combine with other flags
./autoversion install --base ~/repos --exclude-dirs Backup --only-include-extensions md,csv ~/SyncDatabox
```

Excluded directories are skipped entirely — no files are copied, no watches are set up, and no events are processed for anything under them. The flag is persisted in launchd plists, so `install` preserves it across reboots.

Re-running `install` with different `--exclude-dirs` values automatically updates the service configuration and reloads it.

Paths must be relative and cannot escape the source directory. Absolute paths (`/tmp`) and parent traversals (`../outside`) are rejected.

## Commands

### install

Install as a launchd background service (requires `--base`):

```bash
./autoversion install --base BASE_PATH SOURCE_PATH [flags]
```

Flags: `--max-file-size MB`, `--buffer-time SECONDS`, `--no-git`, `--restic`, `--no-login`, `--dry-run`, `--debug`, `--exclude-dirs`, `--only-include-extensions`

The command is idempotent — run it again after rebuilding or changing flags to update and reload the service.

### uninstall

Stop and remove the launchd service:

```bash
./autoversion uninstall --base BASE_PATH SOURCE_PATH
```

### status

Show service status, uptime, and log locations:

```bash
./autoversion status --base BASE_PATH SOURCE_PATH
```

### install-check

Show detailed service status including PID, uptime, and recent log output:

```bash
./autoversion install-check --base BASE_PATH SOURCE_PATH
```

### watch

Run in foreground (useful for debugging):

```bash
./autoversion watch --base BASE_PATH SOURCE_PATH [--dry-run] [--buffer-time SECONDS]
```

### sync

One-time sync without watching:

```bash
./autoversion sync --base BASE_PATH SOURCE_PATH [--dry-run]
```

### init

Initialize a git repository for a source directory:

```bash
./autoversion init --base BASE_PATH SOURCE_PATH
```

### filter

List files that would be tracked:

```bash
./autoversion filter PATH [--max-file-size MB]
```

### check-paths

Validate source and base paths before installing:

```bash
./autoversion check-paths --base BASE_PATH SOURCE_PATH
```

Shows symlink resolution and validates:
- Source and base paths can't be the same
- Source path can't be inside base path
- Base path can't be inside source path
- Base directory itself can't be a git repository
- No parent of base can have a .git repository (prevents nested repos)

## Path Computation

Autoversion computes the target path automatically:

- **Paths under $HOME**: `target = base + (source - $HOME)`
  - `~/projects/myapp` with base `~/repos` → `~/repos/projects/myapp`

- **Paths outside $HOME with common ancestor**: `target = base + (source - commonAncestor)`
  - `/tmp/myapp` with base `/tmp/backups` → `/tmp/backups/myapp` (common ancestor: `/tmp`)

- **Paths outside $HOME with no common ancestor**: `target = base + source`
  - `/var/log` with base `~/repos` → `~/repos/var/log`

## File Filtering

Autoversion excludes:

**Extensions:** `.log`, `.dump`, `.swp`, `.tmp`, `.bak`, `.pyc`, `.o`, `.so`, `.DS_Store`, etc.

**Directories:** `.git/`, `node_modules/`, `__pycache__/`, `.venv/`, `target/debug/`

**Other:** Symlinks, files > 10MB (configurable), vim temp files

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--max-file-size` | 10 MB | Maximum file size to track |
| `--buffer-time` | 3 seconds | Event buffer duration (watch/install) |
| `--dry-run` | false | Show what would be done without executing |
| `--only-include-extensions` | (none) | Only include files with these extensions (comma-separated, e.g. `md,csv,txt`) |
| `--exclude-dirs` | (none) | Exclude directories relative to source (comma-separated, e.g. `Backup,tmp`) |
| `--no-git` | false | Disable git entirely (plain directory mirror) |
| `--restic` | false | Enable restic backup |
| `--no-login` | false | Don't start service at login (install only) |
| `--debug` | false | Enable debug logging |

---

## Design Decisions

### Why automatic path computation?

The automatic path computation (`base + (source - $HOME)`) provides several benefits:
1. Organizes all tracked directories under one base
2. Preserves meaningful directory structure
3. Makes it easy to track multiple projects

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
├── filter.go         # File filtering logic
├── buffer.go         # Event buffer with collapsing
├── watcher.go        # File system watcher wrapper
├── fileops.go        # File operations (copy, delete)
├── git.go            # Git wrapper
├── restic.go         # Restic backup integration
├── launchd.go        # macOS launchd integration
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

**File rename detection**: FSEvents reports renames as two separate events (old path renamed, new path renamed). The code checks which file exists to determine the operation, which can have race conditions.

Example trace showing a file rename:

```
DEBUG: fsevents: raw event path=.../PLAN.md.tmp.18812.1760578375713, flags=0x19900, mapped type: 2, flags decoded: [ItemCreated ItemRenamed ItemModified ItemXattrMod ItemIsFile]
DEBUG: fsevents: raw event path=.../PLAN.md, flags=0x10800, mapped type: 2, flags decoded: [ItemRenamed ItemIsFile]
[2025-10-15 18:32:55] event skip   deleted  .../PLAN.md.tmp.18812.1760578375713
[2025-10-15 18:32:55] event BUFFER deleted  .../PLAN.md
```

The temp file was renamed to the final file. FSEvents reports both paths with `ItemRenamed`. To determine source/target, the code checks which file exists - this can race with other operations.

## License

See project documentation for licensing information.

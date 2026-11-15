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
./autoversion init --git --use default --use nested-git --max-file-size 10M --source ~/projects/myapp --destination ~/repos/myapp
./autoversion install --source ~/projects/myapp --destination ~/repos/myapp

# Grant Full Disk Access (required for some directories):
# System Settings > Privacy & Security > Full Disk Access
# Add: ~/Applications/Autoversion.app
```

The `install` command automatically:
- Creates `~/Applications/Autoversion.app`, a small signed launcher that runs the autoversion binary (built by `make-fda-app.sh`, needs clang from the Xcode Command Line Tools)
- Creates and starts the launchd service, which runs autoversion through the app so it inherits the app's Full Disk Access
- On subsequent runs: reloads the service if the binary, its flags, or the destination's settings or rules changed

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

The install command detects when the binary, its flags, or the destination's settings or rules have changed and automatically reloads running services.

### Build and test

```bash
make build
make test
make clean
make install    # copies binary to /usr/local/bin
make uninstall  # removes from /usr/local/bin
make watch-check  # real watch on a temp source (git, --no-git, allowlist): file and directory changes, rules, no copy loop
make filter       # regenerate filters/README.md from the group files (make test fails while it is stale)
go test -run XXX -bench BenchmarkWalk -benchtime 5x .   # walk a generated tree with three rule sets: time, memory, disk lookups
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
make docker-sh CMD='go build -o /tmp/av . && /tmp/av filter scan --source /src'
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

Initialize the destination with `--mirror` to keep a filtered copy of a directory without git versioning: a clean mirror without build artifacts, temp files and other noise. The setting is stored in the marker, so `sync`, `watch` and `install` take no flag for it.

```bash
./autoversion init --mirror --use default --use nested-git --source ~/projects/myapp --destination /tmp/mirror/myapp
./autoversion sync --source ~/projects/myapp --destination /tmp/mirror/myapp     # one-time mirror
./autoversion watch --source ~/projects/myapp --destination /tmp/mirror/myapp    # real-time mirror
./autoversion install --source ~/projects/myapp --destination /tmp/mirror/myapp  # background service
```

The rules apply as with git (see **filter**). On a plain mirror, the files the rules stop keeping are deleted for good at the next sync: `filter set --dry-run` lists them first.

### Choosing what is tracked

The rules are stored in the destination (see **filter**). Common cases:

```bash
# Everything but the built-in exclusions, repositories inside the source and files over 10 MB
./autoversion init --git --use default --use nested-git --max-file-size 10M \
    --source ~/SyncDatabox --destination ~/repos/SyncDatabox

# Skip folders of the source: rules start at the source root (/**/ for any depth)
./autoversion init --git --use default --use nested-git --exclude /Backup --exclude /nicmac/local_projects \
    --source ~/SyncDatabox --destination ~/repos/SyncDatabox

# Only some file types
./autoversion init --mirror --use default --exclude '/*' --include 'f:/**/*.{md,csv,txt}' \
    --source ~/projects/myapp --destination /tmp/mirror/myapp

# Only a few files and folders of a busy tree (the ~/.claude example of filter)
./autoversion init --git --filter claude.filter --source ~/.claude --destination ~/backup/claude_settings

# Change the rules of a destination: check first, then apply (restarts the running service)
./autoversion filter set --destination ~/repos/SyncDatabox --use default --exclude /Backup --exclude /archives --dry-run
./autoversion filter set --destination ~/repos/SyncDatabox --use default --exclude /Backup --exclude /archives
```

A folder the rules leave out, with nothing kept below it, is skipped entirely: scans don't enter it, on Linux it uses no inotify watch, and its events are only logged (with `--debug` or `--report-only`).

### Example: a few things of `~/.claude`

`~/.claude` is large and busy (about 6000 folders and 40000 files, most changing in `file-history/` and session transcripts). Only the instructions, settings, skills and memories are worth versioning. The rules file (`claude.filter`), as in **filter**:

```
version 1
use default

exclude /*
/CLAUDE.md
/settings.json
/settings.local.json
/skills/!synced
/projects/*/memory
```

```bash
# What the rules keep, and the folders a watch visits (about 200, not 6000)
./autoversion filter scan --source ~/.claude --filter claude.filter
./autoversion filter scan --source ~/.claude --filter claude.filter --summary
# Why an entry is kept or left out
./autoversion filter scan --source ~/.claude --filter claude.filter --explain plugins/cache/x/CLAUDE.md

# A new destination
./autoversion init --git --filter claude.filter --source ~/.claude --destination ~/backup/claude
./autoversion install --source ~/.claude --destination ~/backup/claude

# Or an existing one: check, then apply (commits the rules, restarts the service)
./autoversion filter set --destination ~/backup/claude --filter claude.filter --dry-run
./autoversion filter set --destination ~/backup/claude --filter claude.filter
```

Later edits go in `~/backup/claude/.autoversion.filter`, followed by `filter check --destination ~/backup/claude`.

### Migrating from the flags of earlier versions

The rules and settings moved from command line flags to the destination. `sync`, `watch` and `install` refuse the old flags with a hint. Existing destinations keep working: without a rules file their rules are `use default`, and a marker without settings describes the destination as it is (git when it has a `.git` folder, 10 MB).

| earlier flag | now |
|---|---|
| `--exclude-dir X` | `filter set --use default --exclude /X` |
| `--only-include-extensions md,csv` | `filter set --use default --exclude '/*' --include 'f:/**/*.{md,csv}'` |
| `--only-include-file F` | `filter set --use default --exclude '/*' --include /F` |
| `--max-file-size N` | a `max-file-size NM` line in the rules file: `filter set --destination D --filter - < rules` or `filter set ... --max-file-size NM` with the rule flags. A marker's old `max_file_size_mb` field is kept but ignored |
| `--no-git`, `--restic` | `init --mirror`, `init --restic`: `init` stores them in the marker, and requires one of `--git`, `--restic`, `--mirror` (`--mirror` is refused on a destination with a `.git` folder). `sync`, `watch`, `install` and `check-paths` refuse them |

Then run `install` again: the plist loses the old flags, and the service is reloaded. Differences in the built-in exclusions are listed in `plans/feature_include_exclude_rules.md` (translation table): lock files such as `Cargo.lock`, `.vscode/` and names made only of digits are now tracked.

### Migrating to explicit init (2026-11)

`init` now requires how the destination versions (`--git`, `--restic`, `--mirror`) and its rules, the size limit is the rules file's `max-file-size` line, and `.git` folders are left out by `use nested-git`, not by default (a git destination still never copies them). A destination from before must be moved over: its marker's `max_file_size_mb` is ignored, and a destination without a rules file is refused by `sync`, `watch` and `install`. With its files and history untouched:

```bash
rm DEST_PATH/.autoversion
./autoversion init --force-new-source --git --source SOURCE_PATH --destination DEST_PATH --filter - <<'EOF'
version 1
max-file-size 10M
use default
use nested-git
EOF
./autoversion install --source SOURCE_PATH --destination DEST_PATH   # an installed job: new plist, service reloaded
```

`--mirror` for a plain mirror. Do a mirror before building the new binary for its job, or right after: with `use default` alone the new binary copies `.git` folders into it. `init` on an initialized destination accepts the same rules again, so a script can keep running `init` before `install`.

## Commands

### install

Install as a launchd background service. The destination must be initialized for the source first (see **init**):

```bash
./autoversion install --source SOURCE_PATH --destination DEST_PATH [flags]
```

Flags: `--buffer-time SECONDS`, `--no-login`, `--dry-run`, `--debug`. The settings (git, restic, max file size) and the rules are the destination's: the service reads them when it starts, and `filter set` / `filter check` restart it.

`install` prints the same path resolution and checks as **check-paths**, runs an initial sync, then writes and loads the plist (`watch --source SOURCE_PATH --destination DEST_PATH --buffer-time … --log-dir …`). The plist also holds a hash of the marker and the rules file (`AUTOVERSION_CONFIG`), so a change to either changes the plist. The job ID is built from the source and destination paths. The command is idempotent — run it again after rebuilding or changing flags, settings or rules to update and reload the service. `filter set`, `filter check` and `init --update-settings` restart a running service themselves.

**Logs.** The service writes `~/Library/Logs/autoversion/<job>/stdout.log` and `stderr.log` (`watch --log-dir`, set by `install`). Each file is rotated once it reaches 10 MB, keeping 5 old copies (`stdout.log.1` … `stdout.log.5`, `.1` being the most recent). Sizes are checked every minute. A log file removed by hand is recreated within a minute. Reloading the service through `install` clears `stderr.log` and its rotated copies. Filtered-out events are only logged with `--debug`, and a file over the size limit is reported once per run.

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
./autoversion init --git --use default --use nested-git --max-file-size 10M --source ~/projects/myapp --destination ~/repos/myapp
./autoversion watch --source ~/projects/myapp --destination ~/repos/myapp
```

Use systemd, screen, or tmux to keep it running in the background.

#### inotify watch limit

On Linux, `watch` uses inotify, which needs one watch per directory (folders the rules skip don't use one; `filter scan --summary` counts the others). The number of watches per user, shared by all of that user's processes, is limited by `fs.inotify.max_user_watches` (often 8192). When the limit is reached, `watch` stops with an error giving the limit and how many directories still need a watch, at startup or when a new directory is created later. Check and raise the limit:

```bash
cat /proc/sys/fs/inotify/max_user_watches
./autoversion filter scan --source SOURCE_PATH --destination DEST_PATH --summary   # folders visited = watches
sudo sysctl -w fs.inotify.max_user_watches=524288
```

Each watch in use takes about 1 KB of kernel memory. `sysctl -w` doesn't survive a reboot: put `fs.inotify.max_user_watches=524288` in `/etc/sysctl.d/`, or on Synology DSM add a triggered boot-up task in Task Scheduler (user root) that runs the `sysctl -w` command. Alternatively, exclude large folders with `filter set`.

### watch

Run in foreground (useful for debugging):

```bash
./autoversion watch --source SOURCE_PATH --destination DEST_PATH [--dry-run] [--buffer-time SECONDS] [--log-dir DIR]
```

A directory moved into, out of, or within the source, or created (Linux), is rescanned: its files are copied, and destination files no longer in the source are removed.

The destination's marker is checked at startup only. If the destination disappears while watching (renamed, volume unmounted), `watch` exits with `destination disappeared: DEST_PATH` instead of recreating it; under launchd the restarted service then fails at the startup check until the destination is back.

With `--log-dir DIR`, stdout and stderr go to `DIR/stdout.log` and `DIR/stderr.log`, rotated by size (see **Logs** under `install`). Filtered-out events (`event skip`) are only printed with `--debug`, and so are files removed before their event is handled (`skipping vanished file`, common with build outputs).

The settings and the rules are the destination's: `watch` refuses rule flags (`--max-file-size` included) without `--report-only`, and prints the rules' warnings at startup.

Only print file events, without copying anything (no `--destination`, no initial scan). Each event is shown as `track` (kept by the rules) or `skip`; add `--debug` to also see the raw FSEvents flags. The rules are given on the command line (none: `use default`, no size limit):

```bash
./autoversion watch --report-only --source SOURCE_PATH [--filter FILE | --use GROUP --include RULE --exclude RULE --max-file-size SIZE ...]
```

This can be used instead of `inotifywait -m -r` (Linux) or `fswatch` (macOS) to see what changes under a directory, with rules applied, and to check what a `watch` would pick up before installing it.

### sync

One-time sync without watching:

```bash
./autoversion sync --source SOURCE_PATH --destination DEST_PATH [--dry-run]
```

### init

Initialize a destination for a source. Files under `SOURCE_PATH` are mirrored directly into `DEST_PATH` (`SOURCE_PATH/rel` → `DEST_PATH/rel`):

```bash
./autoversion init --source SOURCE_PATH --destination DEST_PATH (--git [--restic] | --restic | --mirror)
    (--filter FILE | --use GROUP --include RULE --exclude RULE --max-file-size SIZE ...)
    [--force-new-source [--force-keep-marker-settings | --force-replace-marker-settings] | --force-obliterate-yes-i-am-sure]
    [--update-settings]
```

`init` creates the destination if needed, runs `git init` (with `--git`), initializes restic (with `--restic`), and writes two files (see **Destination Marker**):
- `.autoversion`, the marker: the source and the settings. `init` requires one of `--git` (with or without `--restic`), `--restic`, or `--mirror`, a plain copy.
- `.autoversion.filter`, the rules (see **filter**). `init` requires them: the `--filter` file as written, or the rule flags one per line, `--max-file-size SIZE` as a `max-file-size SIZE` line. Without them the error shows a starter file: `version 1`, `max-file-size 10M`, `use default`, `use nested-git`.

With git, both are committed on their own (`init: source SOURCE_PATH`). Invalid rules, or an allowlist none of whose includes exists, stop `init` before anything is created.

| destination | default | `--force-new-source` | `--force-obliterate-yes-i-am-sure` |
|---|---|---|---|
| missing or empty | initialize | initialize | initialize |
| marker names this source | settings check (below) | settings check (below) | wipe, initialize |
| marker names another source | error | rewrite the marker, keep contents, rules and `.git`; needs a settings flag (below) | wipe, initialize |
| not empty, no marker | error | write the marker, keep contents and `.git` | wipe, initialize |

- A marker naming another source also has settings. `--force-new-source` then needs one of:
  - `--force-keep-marker-settings`: the marker's settings, then the settings flags given
  - `--force-replace-marker-settings`: the defaults, then the settings flags given
- After `--force-new-source` the next sync makes the destination match the source: files not in the source are removed (`git rm`, kept in history; on a mirror deleted for good).
- `--force-obliterate-yes-i-am-sure` removes everything inside the destination, `.git` included. The restic repository next to it is not touched.
- The two flags are mutually exclusive.

On a destination already initialized for the source:
- Settings that are not given, or equal to the marker's, change nothing. A missing git or restic repository the settings ask for is created.
- A setting given that the marker doesn't state yet (a marker from before settings were stored) is written to it, without `--update-settings`: `init --mirror` writes `"git": false`. From then on it doesn't depend on what the destination holds.
- Different settings are an error listing them, unless `--update-settings`: then the marker is updated and committed, and a running service is restarted. From a plain mirror to git, `git init` runs and the next sync commits every file.
- Switching from git to a plain mirror (`--mirror` on a git destination) is always refused: the `.git` history would stay while files stop being committed. To do it on purpose, remove `DEST_PATH/.git` by hand, or start over with `--force-obliterate-yes-i-am-sure`.
- Rules are an error: they change with `filter set`.

### filter

A destination's rules say which files of its source are tracked. They are stored in `DEST_PATH/.autoversion.filter`, in the filter syntax shared with targy (`~/projects/targy/plans/spec_filter_syntax.md`). Without that file the rules are `use default`. Example, versioning a few things of `~/.claude`:

```
version 1
max-file-size 10M       # files over it are left out (none, or no line: no limit)
use default             # the built-in groups, see filter groups

exclude /*              # an allowlist: only what is listed below
/CLAUDE.md
/settings.json
/settings.local.json
/skills/!synced         # include /skills, exclude /skills/synced
/projects/*/memory
```

- Directives come first: `max-file-size SIZE` (`10M`, `500k`, `1GB`, `2T`, `4096`, `none`; binary units `k`, `m`, `g`, `t`, with or without `b`, either case, bytes without a unit; a file of exactly SIZE is kept), `use GROUP`, and for other tools `root` and `types`. `max-file-size SIZE RULE` scopes a limit to what a path rule covers (`max-file-size none /**/.git`, `max-file-size 1G f:/**/*.pack`), any number of them: the closest match sets a kept file's limit, ranked like rules, lines still tied give the strictest; no match, the bare line. A scoped `none` on a folder doesn't lift a limit matching the file itself. Prototype of a spec change, see `plans/spec_scoped_max_file_size.md`.
- Every rule starts at the source root (`/`). `*` matches within a name, a `/**/` segment any number of folders. `f:` makes a rule match files only (`f:/**/*.old` keeps a folder named `x.old`).
- A bare line is an include. `{a,b}` gives one rule per name. `P/!Q` is an exception: in an include, `P` minus `P/Q`; in an exclude, `P`'s content except `P/Q`.
- `exclude marker:NAME` leaves out every folder that directly holds an entry named NAME (`marker:{CACHEDIR.TAG,pyvenv.cfg}` for cache folders and virtualenvs), with everything in it except that entry: the folder is stored bare with the marker. It counts as a named wildcard exclude at the folder's own level: a literal or a named include on the folder (`/tools/env`, `/**/env`) wins, an include ending in a wildcard (`/venvs/*`) loses. Presence is what counts, never the file's content.
- `exclude xattr:NAME` leaves out every entry carrying the extended attribute NAME, a folder with everything below it: `xattr:com.apple.metadata:com_apple_backup_excludeItem` is what Time Machine skips (the **time-machine** group). Decided like a marker, on the entry itself. Marker and attribute rules are optimizations: a tool that can't check them (rsync for markers, every tool for attributes) keeps the entry and targy's converter warns; they never make a tool drop what the filter keeps. Prototype of a spec change, see `plans/spec_xattr_rules.md`.
- The closest match wins: the entry itself is checked first, then each folder above it. On one level, a literal rule beats a wildcard, a named wildcard (`/**/.cache`, `/*/node_modules`: the last name is plain) beats one ending in a wildcard (`/.*`, `/**/*.log`), then an include beats an exclude. The tier goes by shape, not intent: `f:/**/*.log` ties with `/.*` and the include wins, so suffix junk at the root survives a dotfiles allowlist. Line order means nothing. Nothing is implied: an allowlist says `exclude /*`.
- Always applied: symlinks, and `/.autoversion` and `/.autoversion.filter` at the source root. A git destination also applies `use nested-git` whatever its rules say (git would record a copied `.git` as a nested repository, not its files); `filter dump --destination` and `--explain` show it. A mirror copies `.git` folders unless its rules say `use nested-git`.

Rules on the command line (`init`, `filter scan`, `filter dump`, `filter set`, `watch --report-only`): `--filter FILE` (a rules file, `-` for stdin; a `root` line must name the source), `--use GROUP`, `--include RULE` and `--exclude RULE` (repeatable), and `--max-file-size SIZE`. They combine: `init` and `filter set` store the file as written, comments kept, with the directives of the flags after its version line and their rules at the end. Without a `use` line or `--use`, the built-in groups don't apply. A rules file starts with `version 1`. For example:

```bash
./autoversion filter scan --source ~/.claude --use default --summary --filter - <<'EOF'
version 1
exclude /*
/CLAUDE.md
/skills/!synced
EOF
```

Quote `'EOF'`, so the shell leaves `$`, `*` and backticks alone.

```bash
# List the files the rules keep (the destination's, or the ones given; none: use default), then a summary on stderr
./autoversion filter scan --source SOURCE_PATH [--destination DEST_PATH | rules]
# What decides one entry: the result, the rule (and its group) and the level it matches
./autoversion filter scan --source SOURCE_PATH [--destination DEST_PATH | rules] --explain PATH
# The summary alone on stdout: number and total size of the kept files, and folders visited (on
# Linux, the inotify watches a watcher needs). Without --summary it follows the files, on stderr.
./autoversion filter scan --source SOURCE_PATH [--destination DEST_PATH | rules] --summary
# The files only
./autoversion filter scan --source SOURCE_PATH [--destination DEST_PATH | rules] --no-summary

# The rules expanded, groups included
./autoversion filter dump [--destination DEST_PATH | rules]
# The built-in groups and their rules, ready to copy into a file (also filters/README.md)
./autoversion filter groups

# Replace a destination's rules, or check them after editing the file by hand
./autoversion filter set --destination DEST_PATH rules [--dry-run]
./autoversion filter check --destination DEST_PATH [--dry-run]
```

`filter set` stores a `--filter` file as written (comments kept) with the flags added, or the flags alone one per line, then does what `filter check` does:
- validates the rules and prints the warnings: dead rules (no such path under the source), includes that change nothing, non-ASCII names
- prints how many files the rules keep, and the files the next sync removes from the destination (kept in git history, or, on a plain mirror, not kept anywhere)
- refuses rules that keep no file while the destination has files, and an allowlist none of whose includes exists (both usually a typo)
- prints the change since the last commit and commits it (`filter: update rules`)
- restarts the installed service, if it runs

With `--dry-run`, only the checks and counts.

### check-paths

Validate source and destination paths before installing:

```bash
./autoversion check-paths --source SOURCE_PATH --destination DEST_PATH
```

Shows symlink resolution and the marker status, and validates:
- Source and destination can't be the same, `$HOME` or `/`
- Neither path can be inside the other
- The source can't contain a `.autoversion` marker (catches swapped `--source` / `--destination`)
- No parent of the destination can contain a `.autoversion` marker (an outer destination's sync would delete the inner one's files), or a `.git` repository unless the destination is a plain mirror (prevents nested repos)
- The destination must have a marker naming the source (and a restic repository when the marker says restic)
- The destination's rules must be valid, and an allowlist must have an include that exists; the other warnings are printed

## Destination Marker

`init` writes `DEST_PATH/.autoversion`, a small JSON file naming the source (absolute path, symlinks resolved) and the settings:

```json
{
  "source": "/Users/me/projects/myapp",
  "git": true,
  "restic": false
}
```

`sync`, `watch` and `install` refuse to copy anything unless the marker exists and names their source. This catches typos in either path (a mistyped destination is never silently created) and prevents two sources from syncing into the same destination.

- Settings missing from a marker (markers written before settings were stored) describe the destination as it is: `git` when it has a `.git` folder, `restic` when it has a restic repository. They are worked out each time, until `init` is given the setting and stores it.
- A `max_file_size_mb` field, from before the size limit was the rules file's `max-file-size` line, is kept like any unknown field and ignored. `filter dump --destination` prints the size limit.
- Fields autoversion doesn't know are kept when the marker is rewritten.
- The marker and the rules file (`.autoversion.filter`) are never deleted or overwritten by a sync, and files with these names at the root of a source are not tracked.
- With git, both are versioned like the other files: `git log -p .autoversion` shows which source the destination belonged to over time, and `git log -p .autoversion.filter` every rule change.

## File Filtering

What is tracked is decided by the destination's rules (see **filter**), then:
- symlinks and other files that are not regular files are never tracked
- files over the rules file's `max-file-size` are skipped (`max-file-size 10M`: an integer with a unit `k`, `m` or `g`, binary, either case, or `none`; a file of exactly that size is kept). Without the line there is no limit
- `/.autoversion` and `/.autoversion.filter` at the source root are always left out
- a git destination leaves out `.git` folders and files at any level, whatever its rules say (`use nested-git`, applied by the git destination). A plain mirror copies them unless its rules say `use nested-git`; a sync then removes stale copies like any file, and restic backs them up, only the destination's own `.git` is skipped

The built-in groups are rules files, `filters/NAME.filter`, compiled into the binary; other tools can read them as they are. [filters/README.md](filters/README.md) lists every rule, generated from them (`filter groups` prints the same). In short, `use default` leaves out:
- **os:** `.DS_Store`, `Thumbs.db`, `desktop.ini`, `.nfs*`, `.localized` files; `.Spotlight-V100`, `.Trashes`, `.dropbox`, `.fseventsd`
- **editors:** `*.swp`, `*~`, Vim's `4913`, `*.iml` files; `.idea`
- **temp:** `*.tmp`, `*.tmp.*`, `*.temp`, `*.bak` files; git's locks and in-flight objects under `.git` (`*.lock`, `objects/*/tmp_obj_*`, `objects/pack/tmp_pack_*`), which a mirror copying `.git` would otherwise copy and remove again; Chromium's `.org.chromium.Chromium.*` temp files
- **logs:** `*.log`, `*.out`, `*.err`, `*.pid` files
- **build:** the content of `build/`, `dist/` and `out/`; `target/debug`, `target/release` and Maven's and sbt's `target/classes`, `target/streams`, …; `.build`, `DerivedData`, `zig-out`, `cdk.out`, `_mdfmt`, per-platform `bin/linux-*`, `bin/darwin-*`, …; `*.o`, `*.so`, `*.dylib`, `*.class`, `*.jar`, `*.pyc`, … files
- **deps:** `node_modules`, `bower_components`, `.pnpm-store`, `.local/share/pnpm`, `.npm`, `.venv`, `venv`, `__pycache__`, `__pypackages__`, `.gradle`, `.direnv`, `go/pkg/mod`, `.modcache`, `toolchain/zig*`
- **caches:** `.cache`, `.next`, `.nuxt`, `.output`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `.tox`, `.nox`, `.parcel-cache`, `.turbo`, `.svelte-kit`, `.angular`, `.zig-cache`, `.gocache`, `.evidence/template`, `.evidence/meta`, `.playwright-mcp`, `playwright-report`, `test-results`; Chromium and Electron profile caches `Code Cache`, `GPUCache`, `DawnCache`, `ShaderCache`, …
- **data:** `pgdata`, `*.pgdb`; `*.wal` files
- **vcs:** `.hg`, `.svn`, `.cvs`

Opt-in groups, not in `default` (`use default` and `use nested-git`, for example):
- **nested-git:** repositories inside the source: `.git` folders and files (worktrees, submodules) and bare `*.git` repositories, at any level. A git destination applies it always; a mirror only with the line, so a backup of a work area that must keep its repositories leaves it out.
- **music**, **photos**, **videos**, and **media** for the three: media files by extension (`*.mp3`, `*.heic`, `*.mov`, …), lowercase and uppercase, files only. Only excludes: "only my photos" is an allowlist, `exclude /*` and `f:/**/*.{jpg,png}`.
- **nas:** Synology metadata, `@eaDir` folders and `*@SynoResource`, `*@SynoEAStream` files
- **extra-tools:** dependencies, caches and build outputs of less common tools: `.dart_tool`, `.stack-work`, `elm-stuff`, `.cpcache`, `.shadow-cljs`, `.godot`, `_build`, `Pods`, `.terraform`, `.terragrunt-cache`, `Carthage/Build`, `Carthage/Checkouts`, `renv/library`, `.yarn/cache`, `.yarn/unplugged`, `.moon/cache`, `.packages` files. Opt-in because some projects commit `Pods` or `.yarn/cache`, and `_build` is a generic name.
- **installers:** app install files: `*.dmg` files, `*.pkg` and `*.mpkg` files and folders (older installers are bundles)
- **home-caches:** tool caches in a home folder: `.m2/repository`, `.nuget/packages`, `.kube/cache`, `.kube/http-cache`, `.vagrant.d/boxes` (`go/pkg/mod` is in **deps**)
- **toolchains:** what a tool reinstalls itself: `.rustup/toolchains`, `.pyenv/versions`, `.pyenv/shims`, `.nvm/versions`, `.cargo/registry`, `.local/pipx`, `.local/share/uv`, `.local/share/virtualenvs`, `.bun/install/cache`, `.vscode/extensions`, `.duckdb/extensions`, `.local/share/claude`, `.terraform.d/plugin-cache`, …
- **local-state:** `.local/state`, the XDG state folder (logs, history, recently-used lists), and herdr's `.config/herdr/session-snapshots`
- **vms:** container and virtual machine disks: `.docker/sandboxes`, `.docker/buildx`, `.colima`, `.lima`, `.container-platform`, a project's `.vagrant`
- **agents:** regenerable state of coding agents: `.claude/debug`, `.claude/file-history`, `.claude/shell-snapshots`, `.claude/telemetry`, …, `.codex/cache`, `.codex/plugins`, …, ateam's `.ateam/{cache,logs}`. Transcripts, memories and config files stay in.
- **cache-markers:** `exclude marker:{CACHEDIR.TAG,pyvenv.cfg,CMakeCache.txt}`: any folder tagged as a cache, a virtualenv or a CMake build tree by a file it holds, wherever it is. Not in **default**: restic and borg honor markers, rsync doesn't, so a filter previewed here would keep more there.
- **junk:** one line for everything a backup can do without: `default`, `extra-tools`, `home-caches`, `toolchains`, `local-state`, `vms`, `agents`, `cache-markers`, `trash`, `browser-profiles`, `nas`, plus `vendor` and `deps` folders (asimov excludes them next to `go.mod`, `composer.json`, `Gemfile`, `mix.exs`; often committed, so not in `default`).
- **browser-profiles:** what a Chromium or Chrome user data folder re-downloads or regenerates: component updater folders (`component_crx_cache`, `PKIMetadata`, `CertificateRevocation`, …), `Crashpad`, `BrowserMetrics`, `Service Worker`, `blob_storage`, a profile's `Cache`, `Network Persistent State`, `Local State` (rewritten on every start). Cookies, local storage and preferences stay, so a scraping profile keeps its logins. Opt-in because `Service Worker` and `blob_storage` hold site data some want.
- **db-dumps:** database dumps, `*.dump`, `*.dmp`, `*.sql.gz`, `*.sql.bz2`, `*.sql.xz`, `*.sql.zst` files, for a backup that leaves them to the database's own backup. Not in `default` or `junk`: a dump is a backup worth keeping. Plain `*.sql` is source.
- **time-machine:** `exclude xattr:com.apple.metadata:com_apple_backup_excludeItem`: what apps (Xcode, Docker, Parallels) and `tmutil addexclusion` told Time Machine not to back up. Not in `junk`: people mark big media as not for Time Machine and still want it elsewhere. On Linux the attribute never exists.
- **trash:** `.Trash`, a group of its own so other filters (targy's `common`) can take it; not in `default`, a backup of a home may want it.

A group rule names what it matches (`/**/.cache`), so it beats an include ending in a wildcard on the same level: an allowlist of the dot entries of a home folder, `exclude /*`, `include /.*` and `use junk`, leaves out `.cache`, `.Trash` and the rest at the root (in the spec since 2026-11-06, see `plans/spec_named_wildcard_tier.md`). Suffix rules like `*.log` don't name their entries, so such files at the root stay in.

Rules for file names only apply to files (`f:`): a folder named `x.tmp` or `conf.d` is kept. Lock files (`Cargo.lock`, `uv.lock`) and `.vscode/` are tracked.

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--source` | (none) | Source directory to track (all commands except `bundle`/`unbundle`; optional for `status`) |
| `--destination` | (none) | Destination directory, initialized by `init` (optional for `filter scan` and `filter dump`, not used by `watch --report-only`) |
| `--force-new-source` | false | `init`: use a non-empty destination, or one initialized for another source, keeping its contents and history |
| `--force-keep-marker-settings`, `--force-replace-marker-settings` | false | `init --force-new-source` on a marker naming another source: keep its settings, or start from the defaults (one is required) |
| `--force-obliterate-yes-i-am-sure` | false | `init`: remove everything in the destination, `.git` included, then initialize it |
| `--update-settings` | false | `init`: on an initialized destination, write settings different from the marker's |
| `--filter` | (none) | Rules file (`init`, `filter scan`, `dump`, `set`) |
| `--include`, `--exclude` | (none) | Include or exclude rule, repeatable (`init`, `filter scan`, `dump`, `set`) |
| `--use` | (none) | Built-in group, repeatable (`init`, `filter scan`, `dump`, `set`; see `filter groups`) |
| `--explain`, `--summary`, `--no-summary` | (none) | `filter scan`: what decides one entry; the summary only, on stdout; the files only (by default the summary follows the files, on stderr) |
| `--max-file-size` | (none) | Size limit, `10M`, `500k`, `1GB`, `2T`, `4096` (bytes) or `none`: the `max-file-size` line of the rules, with the other rule flags (`init`, `filter scan`, `dump`, `set`, `watch --report-only`) |
| `--buffer-time` | 3 seconds | Event buffer duration (watch/install) |
| `--dry-run` | false | Show what would be done without executing |
| `--report-only` | false | Only print file events, copy nothing (watch only, no `--destination` needed) |
| `--git`, `--restic`, `--mirror` | false | `init`: how the destination versions, required: git, restic (or both), or a plain copy (stored in the marker) |
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

### Using the filter library from another Go module

The filter syntax lives in two packages other tools can import, both standard library only: `filterrules` (parser, matcher, checks) and `filters` (the built-in groups, embedded; `filterrules` imports it). dvgo uses them to apply the same rules to its scans. The importing module needs Go 1.25 or later. The repository is private, so Go must fetch it with git instead of the module proxy:

```bash
go env -w 'GOPRIVATE=github.com/nicad/*'
go get github.com/nicad/autoversion@main      # in the importing module
```

The API, all in `filterrules`:
- `ParseFile(r)` reads a rules file into a `File` (`Root`, `Types`, `MaxFileSize` as written, `Uses`, `Lines`, `Rules`); `File.Use`, `File.Add` and `File.Format` build one from flags. `ParseRule` expands one rule's sugar.
- `New(file)` combines the file's rules with its groups and validates them (`whole-root`, `contradiction`).
- Deciding goes through a `Tree`, bound to one tree on disk: `rules.Bind(root)` reads markers with `lstat` and attributes with `lgetxattr` (`DiskLookups`); `rules.BindWith(lookups)` takes another `Lookups`, `filterrules.NoLookups` being the explicit "marker and xattr rules never match". There is no way to decide without one, so a tool can't skip those rules by forgetting them.
- A walker builds a `Dir` per folder it enters, from `tree.Root()` down with `dir.Child(name, list)`, where `list` returns the folder's listing: called at most once, and only when a marker rule has to be checked, so marker rules cost no syscall and a folder a rule leaves out is never listed; `nil` means "ask the lookups". `dir.File(name)` decides a non-folder entry, `dir.Skip()` says whether the walker can skip the folder, `dir.Decision()` is the folder's own. A `Dir` is read only once built: goroutines of a parallel walker share their parent's `Dir`. Live memory is one `Dir` per folder in flight.
- `tree.Decide(rel, isDir)` and `tree.SkipDir(relDir)` answer for one path, building the folders above it on the way: for `explain`, a watch event, tests. `Rules.CanMatchBelow(relDir)` says whether an include can match under a folder. `Rules.Markers()` and `Rules.Xattrs()` list the marker and attribute rules. The `*Rule` values of `List` and `Decision.Rule` are shared: read only.
- `Rules.MaxFileSize()` and `Rules.TooLarge(size)` for the size limit; `ParseSize` for a `10M`-style value.
- `Rules.Check(list)` finds dead rules and includes that change nothing, given a function that lists a folder; `Rules.Dump()` prints the expanded rules.
- `Rules.List()` returns the combined rules, the filter's own first, then its groups'; a `Decision.Rule` points into it.
- `Groups()`, `GroupNames()`, `Group(name)` for the built-in groups. A group file that doesn't load never panics: `Group` and `New` return its error, for that group and the groups using it only; `Groups()` leaves it out, and `filter groups` reports it.

Entry paths are relative to the root with `/` separators, no leading or trailing `/`; the root itself is `""`. What is autoversion's policy and not in the library: symlinks are never tracked, `/.autoversion` and `/.autoversion.filter` at the root are never copied, and a git destination always applies `nested-git`. The spec is targy's `plans/spec_filter_syntax.md`; `filterrules/testdata/filter_conformance.json` is its test suite, copied unchanged. The group files of `filters/` are shared the other way: targy copies them with `make groups-sync`, and its tools tests fail until it does, so a group change is to be announced to targy along with any conformance change. Tools targy forwards the size to (restic, rsync) take the unit letter only: targy strips the `b` (`4KB` → `4K`).

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


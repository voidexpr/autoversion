# Plan: include/exclude rules, and the destination config in the marker

**Status: proposal, not implemented.** Breaking change: jobs are re-migrated.

## Problem

- Three filter flags with different, partly exclusive semantics:
  - `--exclude-dir DIR` (top-level only)
  - `--only-include-extensions md,csv` (also disables the built-in exclusions)
  - `--only-include-file PATH` (exact paths, can't be combined with the other two)
- They can't express what large trees like `~/.claude` need. Measured on
  2026-10-24: 6254 directories, ~39600 files.
  - one-off files: `CLAUDE.md`, `settings.json`, `settings.local.json`
  - a name at any depth: `MEMORY.md` (20 copies, all in
    `projects/<project>/memory/`)
  - a whole directory: `skills/` (69 dirs, 323 files)
  - Careful: `CLAUDE.md` also exists under `plugins/` (2 copies), so it must
    only match at the root.
- The built-in exclusions are regexes on the full path, invisible to the user,
  and can't be listed or turned off one by one.
- The filters, `--no-git` and `--max-file-size` must be repeated identically
  on `init`, `sync`, `watch` and `install`. A manual
  `sync --source ~/.claude --destination …/claude_settings` without the job's
  `--only-include-file` copies all of `~/.claude` into the destination.

## Goals

- One rule model for everything: whole trees, directories, single files,
  names at any depth, extensions.
- gitignore-like patterns: familiar, explainable.
- Large trees stay cheap: skip directories that can't contain tracked files
  (scan, Linux watches).
- A destination knows what it mirrors: source, rules and git mode are stored
  in its marker, so every command applies the same ones.
- Tools to see and debug the rules.

Not goals: regex patterns, re-including files below an excluded directory,
case-insensitive matching (see Later).

## Part 1: rules

### Flags

| flag | meaning |
|---|---|
| `--include PATTERN` | repeatable. When at least one is given, only matching files are tracked |
| `--exclude PATTERN` | repeatable. Matching files and directories are never tracked |
| `--no-default-excludes` | drop the built-in exclusions (below) |
| `--no-default-exclude PATTERN` | repeatable. Drop one built-in exclusion, by its text (e.g. `build/`) |
| `--max-file-size MB` | unchanged |

Removed: `--exclude-dir`, `--only-include-extensions`, `--only-include-file`.

### Pattern syntax

Like `.gitignore`, without `!` negation (there's `--exclude` instead).
Patterns use `/` and are matched against paths relative to the source.

| pattern | matches |
|---|---|
| `MEMORY.md` | a file or directory named `MEMORY.md` at any depth |
| `*.md` | any name ending in `.md`, at any depth |
| `/CLAUDE.md` | `CLAUDE.md` at the source root only |
| `skills/` | any directory named `skills`, and everything below it |
| `/skills/` | the `skills` directory at the root, and everything below it |
| `projects/*/memory/MEMORY.md` | contains a `/`, so anchored at the root |
| `**/memory/` | any directory named `memory` at any depth |
| `docs/**/*.md` | `.md` files anywhere below the root `docs` directory |

- No `/`, or only a trailing one: the pattern matches a name at any depth.
- A `/` at the start or in the middle: anchored at the source root.
- A trailing `/`: matches directories only. Everything below is matched too.
- `*` and `?` match within one path component; `**` matches any number of
  components; `[abc]` is a character class; `\` escapes.
- Matching is case-sensitive.

### Which files are tracked

A file is tracked when all of these hold:
1. It is not always-excluded: a symlink, anything under `.git/`, or the
   `.autoversion` marker at the source root.
2. No `--exclude` or built-in exclusion matches the file or one of its
   directories.
3. No `--include` was given, or an `--include` matches the file or one of its
   directories.
4. It is not larger than `--max-file-size`.

Consequences, so the rules stay order-independent:
- Excludes always win over includes.
- As in git, nothing below an excluded directory can be included again.
  `--include '*.md'` doesn't reach into `node_modules/`.
- To track something a built-in rule excludes, drop that rule with
  `--no-default-exclude`, or all of them with `--no-default-excludes`.

This changes two current behaviors:
- `--only-include-extensions` turned the built-in exclusions off. Now
  `--include '*.md'` keeps them.
- `--only-include-file` overrode the built-in exclusions. Now
  `--include /x.log` doesn't. `filter --explain` shows why, and
  `--no-default-exclude '*.log'` fixes it.

### Built-in exclusions

The same as today, written as patterns so they can be listed
(`filter --rules`) and dropped one by one:

- editor and temp files: `*.swp` `*.swpx` `*.swo` `*~` `*.tmp` `*.tmp.*`
  `*.temp` `*.bak` `*.old`, plus names made only of digits (vim/fs temp
  files, a special rule)
- logs and runtime files: `*.log` `*.dump` `*.pid` `*.lock` `*.out`
  `*.stdout` `*.err` `*.stderr` `*.wal` `*.depend`
- build outputs: `*.o` `*.obj` `*.so` `*.a` `*.lib` `*.sym` `*.dylib`
  `*.rlib` `*.rmeta` `*.dll` `*.exe` `*.pdb` `*.ilk` `*.exp` `*.gch` `*.pch`
  `*.d` `*.class` `*.jar` `*.war` `*.ear` `*.pyc` `*.beam` `*.iml`
- directories: `node_modules/` `__pycache__/` `.venv/` `venv/`
  `.pytest_cache/` `.playwright-mcp/` `**/target/debug/` `**/target/release/`
  `.gradle/` `*.xcworkspace/` `DerivedData/` `.build/` `build/` `dist/` `out/`
  `.cache/` `.next/` `.nuxt/` `.output/` `.npm/` `.yarn/` `bower_components/`
  `.pnpm-store/` `.idea/` `.vscode/` `playwright-report/` `test-results/`
  `pgdata/` `*.pgdb/` `.hg/` `.svn/` `.cvs/` `.dropbox.cache/`
  `.Spotlight-V100/` `.Trashes/`
- system files: `.DS_Store` `Thumbs.db` `desktop.ini` `.nfs*`

Small differences from today's regexes: a name containing `.out`, `.err` or
`.dropbox` anywhere (e.g. `notes.output.md`) is no longer excluded, only the
suffixes listed above.

### Skipping directories (performance)

A directory is skipped by the scan, by directory rescans and by the Linux
watcher when:
- an exclusion matches it, or
- includes are given and none of them can match anything below it.

Anchored includes allow skipping almost everything. With
`--include /skills/ --include '/projects/*/memory/MEMORY.md'`, only `skills/`,
`projects/` with its subfolders, and the `projects/*/memory/` folders are
visited, about 250 of 6254 directories.

An unanchored include (`MEMORY.md`, `*.md`) can match at any depth, so every
non-excluded directory is visited. That is fine for `~/.claude`, but not for
trees like the NAS backup (260k directories). The README will recommend
anchored patterns for large trees.

On macOS, FSEvents streams are always recursive: the stream still covers the
source, and events are dropped by the path rules before any file access.

### Inspecting the rules

- `filter --source S [rules]` lists the tracked files (as today).
- `filter … --rules` prints the effective rules: includes, excludes and the
  built-in exclusions in use.
- `filter … --explain PATH` tells whether PATH is tracked and which rule
  decided. For example:
  `excluded by built-in node_modules/ (directory projects/x/node_modules)`, or
  `not included: no --include matches`.
- `filter … --summary` gives the number of tracked files and of directories
  visited. On Linux, that's the number of inotify watches needed.

## Part 2: the destination config in the marker

The rules, `--no-git`, `--restic` and `--max-file-size` describe the
destination, not one run. They move into the `.autoversion` marker, so
`sync`, `watch`, `install` and `check-paths` read them from there. That way a
manual command can't apply different rules by mistake.

### Marker format

```json
{
  "source": "/Users/nicolas/SyncDatabox/nicmac/.claude",
  "git": true,
  "restic": false,
  "include": ["/CLAUDE.md", "/settings.json", "/settings.local.json", "/skills/", "/projects/*/memory/MEMORY.md"],
  "exclude": [],
  "default_excludes": true,
  "no_default_exclude": [],
  "max_file_size_mb": 10
}
```

Missing fields take their defaults, so current markers (only `source`) stay
valid with git, the built-in exclusions and 10 MB.

### Commands

| command | config |
|---|---|
| `init` | takes the config flags and writes them to the marker |
| `sync`, `watch`, `install`, `check-paths` | read the marker; config flags are an error ("set with init") |
| `filter`, `watch --report-only` | without `--destination`: take the config flags (to try rules); with `--destination`: read the marker |

`init` on a destination that is already initialized:

| case | result |
|---|---|
| same config | no-op |
| different config, no `--update-config` | error, shows old and new config |
| different config, `--update-config` | rewrites the marker, commits it (`config: …`), shows the change |
| `--no-git` on a git destination, or the reverse | error. Turning git on for a mirror is done with `--force-obliterate-yes-i-am-sure`, or by hand |

With `--update-config`, `init` is declarative, so a job script can run it on
every install. The next sync applies new rules: files no longer tracked are
removed from the destination (`git rm`, kept in history).

### install

- The plist only has runtime flags:
  `watch --source S --destination D --log-dir … [--buffer-time N] [--debug]`.
- `install` adds a hash of the marker config to the plist
  (`EnvironmentVariables` → `AUTOVERSION_CONFIG`). After `init --update-config`,
  re-running `install` sees a different plist and reloads the job, through
  the existing reload logic.

## Examples

Job script columns become `NAME|SOURCE|DESTINATION|INIT FLAGS|INSTALL FLAGS`:
```
"projects|$HOME/projects|$B/projects||"
"dotfiles|$HOME/dotfiles|$B/dotfiles||"
"notes|$HOME/notes|$B/notes||"
"sync|$HOME/Dropbox/Sync|$B/Sync||"
"sublime|$HOME/Dropbox/Apps/Sublime|$B/Sublime||"
"claude|$HOME/.claude|$B/claude|--include /CLAUDE.md --include /settings.json --include /settings.local.json --include /skills/ --include /projects/*/memory/|"
"dropbox-projects|$HOME/projects|$HOME/Dropbox/Backup/autoversion/projects|--no-git|--buffer-time 45"
```
`install` = `init --update-config INIT FLAGS`, then `install INSTALL FLAGS`.
For `claude`, `/projects/*/memory/` keeps the whole memory folders. Use
`/projects/*/memory/MEMORY.md` for the index files only.

Other examples:
- markdown and CSV only: `--include '*.md' --include '*.csv'`
- a tree minus a folder: `--exclude /Backup/`
- the NAS backup, without the Claude job folders:
  `--exclude /SyncDatabox/nicmac/.claude/jobs/`

## Implementation notes

- New `rules.go`:
  - parse a pattern into components, with flags for anchored and
    directory-only; unanchored patterns get a leading `**`
  - match a relative path component by component, with `**` and
    `path.Match` for the others
  - "can match below this directory": component-wise prefix match, so
    directories can be skipped
  - a `Rules` struct built from the config (includes, excludes, built-ins),
    with `Tracked(rel)`, `SkipDir(rel)` and `Explain(rel)`
- Replaces `matchesPathFilters`, `IsExcludedPath`, `IsExcludedDir`,
  `excludePatterns`, `includeExtensions`, `excludeDirs` and `includeFiles`.
  `ShouldTrack` keeps the symlink, directory, vanished-file and size checks.
- The marker gets the new fields and the `init --update-config` logic.
  `ValidateSourceAndDestination` returns the config.
- `watch`, `sync` and `install` build the `Rules` from the marker.
  `generatePlist` drops the config flags and adds the config hash.
- Tests:
  - a pattern table: each syntax row above, plus `**` edge cases
  - the precedence rules
  - skipping directories (the `~/.claude`-like tree above)
  - `--explain`
  - the `init` config cases
  - old markers read with defaults
  - plist reload on a config change
  - `check_watch.sh` with an `--include` mode
- Size: about 300–400 lines of code plus tests. The matcher is the only
  intricate part.

## Migration

1. Build. Every running job fails at its next restart until reinstalled,
   because the plist flags change.
2. For `claude_settings` (it becomes `claude`, with a new destination and the
   rules above), and for `dropbox-projects` (`--no-git` now in its marker), run
   `init --update-config` with their config. The other markers need no change.
3. Re-run `./autoversion-install.sh install` with the new columns, then
   `./autoversion-install.sh jobs`.
4. NAS: rebuild. `watch --report-only` takes `--include` / `--exclude`.

## Later

- A rules file (`--rules FILE`) for long lists.
- Narrowing the macOS FSEvents stream to the anchored include roots.
- Case-insensitive matching (APFS is case-insensitive by default).
- Re-including a file below an excluded directory.

## Open questions

1. gitignore anchoring (no `/` matches at any depth), or "always anchored
   unless it starts with `**/`"? Proposal: gitignore, as it is familiar.
2. Part 2 (config in the marker): do it now with Part 1, or later? Part 1
   alone keeps today's flags-on-every-command model.
3. Built-in exclusions can't be overridden by an include, only dropped with
   `--no-default-exclude(s)`. OK?

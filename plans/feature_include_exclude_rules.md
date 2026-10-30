# Plan: filter rules in the shared syntax, stored with the destination

**Status: in implementation (v5, 2026-10-29).** v5 follows the shared spec
v3: `d:` is implemented, the `types` line is accepted, and rule changes get
safety checks (see Decisions). Breaking change: jobs are re-migrated (see
Migration).

## For the implementer: read first

**Inputs:**
- This plan: the authority for how autoversion uses the rules (storage,
  commands, built-ins, code changes, migration).
- `~/projects/ownit/plans/spec_filter_syntax.md`, final version (given
  separately): the authority for the syntax, matching, sugar, decision
  rule, validation and error codes. Where it disagrees with this plan's
  summary of the syntax, the shared spec wins. Tell the user about any
  disagreement.
- ownit's `tests/filter_conformance.json`: copy it unchanged into
  `filterrules/testdata/`, and record the ownit commit it came from in a
  comment in the test.

**Project rules** (from `CLAUDE.md` and the user's preferences):
- Run the tests before and after each change: `make test`, and
  `make docker-go-test` for the Linux code. `gofmt -l .` must be clean.
- No new dependency: the matcher uses the standard library only.
- Document every new or changed command in `README.md`.
- Use `./claude_kill`, not `kill`.
- Commit only when the user asks. Messages go through a heredoc
  (`git commit -F - <<'EOF'`), never `$(…)`, and end with the
  `Co-Authored-By` line.
- Ask before expanding the scope beyond this plan.
- Tests never touch real files: everything lives under `t.TempDir()`,
  `TestMain` points `HOME` at a temp folder and turns off git's global and
  system config, and the job restart (`launchctl`) is a stub in tests.
- Test binaries are built under `build/` (`make watch-check` does).
  `make build` replaces the `./autoversion` that installed jobs run.

**Testing limits in the Claude sandbox:**
- FSEvents doesn't start in the sandbox, so `make watch-check` (macOS) is
  run by the user. Linux runs work through `make docker-go-test`,
  `make docker-sh CMD='…'` and `make docker-watch-check`.
- The user builds Docker images (`make docker-dev-build`).

**Related plan:** `feature_remote_destination.md` adds a `remotes` field to
the same marker. The marker must be read and rewritten without losing
fields autoversion doesn't know about.

## Motivation: `~/.claude`

`~/.claude` is a large, busy tree. Measured on 2026-10-24: 6254 folders and
about 39600 files. About 90 files change per hour, mostly in `file-history/`
and in the session transcripts under `projects/`. Only a few things in it
are worth versioning:

| what | where | count |
|---|---|---|
| global instructions | `CLAUDE.md` at the root only. Two more copies exist under `plugins/`, which belong to the plugins. | 1 file |
| settings | `settings.json`, `settings.local.json` | 2 files |
| skills | `skills/`, except `skills/synced/` (synced from elsewhere) | up to 69 folders, 323 files |
| memories | `projects/<project>/memory/`: `MEMORY.md` plus one file per memory | 20 folders |

What autoversion has today can't express this:
- `--only-include-file` takes exact paths only: no folders, no wildcards.
- `--exclude-dir` and `--only-include-extensions` don't help.
- A gitignore-style `CLAUDE.md` would also match the two plugin copies.

With the shared syntax, the destination's filter file is:

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

- `exclude /*` makes it an allowlist. Nothing is implied: without it, the
  bare lines (includes) would change nothing.
- `/skills/!synced` is sugar for `include /skills` plus
  `exclude /skills/synced`.
- Each entry is decided by the closest rule, checking the entry first and
  then the folders above it:

| entry | result | closest match |
|---|---|---|
| `CLAUDE.md` | kept | `/CLAUDE.md` (a literal include beats `/*` on the same level) |
| `plugins/cache/x/CLAUDE.md` | left out | `plugins` (`/*`) |
| `history.jsonl` | left out | `/*` |
| `skills/pdf/SKILL.md` | kept | `/skills` |
| `skills/synced/foo/SKILL.md` | left out | `skills/synced` (exclude, closer than `/skills`) |
| `skills/pdf/node_modules/x.js` | left out | `skills/pdf/node_modules` (`default`'s `/**/node_modules`, closer than `/skills`) |
| `skills/pdf/x.pyc` | left out | the entry itself (`default`'s `f:/**/*.pyc`) |
| `projects/-Users-x/memory/MEMORY.md` | kept | `projects/-Users-x/memory` (include) |
| `projects/-Users-x/abc123.jsonl` | left out | `projects` (`/*`) |
| `file-history/...` | left out | `file-history` (`/*`) |

- **Cost:** the scan and the Linux watcher skip a left-out folder when no
  include can match below it. Here they visit:
  - the root
  - `skills/` (up to 69 folders, minus `synced/`)
  - `projects/` and its 155 subfolders
  - the 20 `memory/` folders

  That's about 250 of 6254. `file-history/`, `plugins/` and
  `skills/synced/` are never entered.
- **macOS:** FSEvents still reports the whole tree. Events are dropped by
  the rules before any file access.

Other jobs:
- `projects`, `dotfiles`, `notes`, `sync`, `sublime`: the whole tree minus
  the built-ins. Their filter file is `version 1` and `use default`, or
  there is no file.
- A tree minus one folder: `exclude /Backup`.
- Markdown and CSV only (today's `--only-include-extensions`):
  `exclude /*` and `f:/**/*.{md,csv}`.
  - A root-level `a.md` is kept: a wildcard include wins its tie with `/*`.
  - Deeper `.md` files are kept: the include matches the file, closer than
    `/*`.
  - As today, this reaches into `node_modules/`, because the include on
    the file is closer than `/**/node_modules`. Every folder is visited
    (`**` can match anywhere).

## The syntax, in short

The shared spec has the exact definitions. What matters most here:
- **Rooted:** every rule starts with `/`, the source. `/**/x` means at any
  depth.
- **Wildcards:** `*` stays within one name; `**` is a whole segment, at
  most once, never last. There is no `?`, no `[…]` and no escapes.
- **Shorthands:** `{a,b}` braces, and `!` for exceptions.
- **Closest match wins:**
  - The entry itself is checked first, then each folder above it.
  - On the same level, a literal rule beats a wildcard rule.
  - Between two wildcards, the include wins.
  - Two literal rules on one path are a `contradiction` error.
- **No order:** rules and groups combine as unions. Adding an include never
  leaves out more, and adding an exclude never keeps more.
- **Nothing is implied:** an allowlist says `exclude /*`.
- **Wildcard includes** (`/projects/*/memory`) are fine for autoversion's
  native matcher. Only converters to other tools refuse them.

### Entry type prefix: `f:` and `d:`

A rule may start with a type prefix, before its leading `/` (shared spec,
"Types"):
- No prefix: a **path** rule. It matches files and folders alike.
- `f:/**/*.old`: matches **files only** (anything that is not a folder;
  links are never tracked anyway). A folder named `foo.old` is not
  matched, so it and its content stay.
- `d:/**/build`: matches **folders only**. A file named `build` stays.
  Implemented because the shared spec and its conformance suite have it.
  The built-in groups don't use it: they use `f:` where a rule must only
  apply to files, and no prefix otherwise.

Matching a typed rule:
- An `f:` rule takes part only at the entry's own level, and only when the
  entry is not a folder. When a folder level is checked (an entry's
  parent, grandparent, …, or a folder itself), `f:` rules are ignored.
- A `d:` rule takes part at every folder level, the folders above an entry
  included.
- Otherwise a typed rule behaves like any rule: same syntax and braces
  (`f:/**/{*.tmp,*.bak}`), and the same literal-or-wildcard ties.
- No `!` on a typed line (`bad-except`): write the exception as its own
  line.
- A deleted entry has no type. A delete event is decided as a file. A
  removed folder's files get their own events or a directory rescan, so
  nothing is missed.

Where it's written:
- In a filter file: `exclude f:/**/*.old`, `include f:/**/*.md`, or a bare
  line `f:/**/*.md`.
- In flags: `--exclude 'f:/**/*.old'`.

### The `types` line

`types strict` (the default) or `types decay` tells ownit's converters what
restic and borg do with typed rules. autoversion matches the rules itself,
and its restic mode backs up the destination, which only holds kept files.
So the line is accepted, kept in the file and shown by `filter dump`, and
changes nothing.

## Where the configuration lives

The rules and the settings describe the destination, not one run. They are
stored in the destination, so every command applies the same ones.

- **`D/.autoversion`** (the marker, JSON): the source and the settings that
  aren't rules.
  ```json
  {"source": "/Users/nicolas/SyncDatabox/nicmac/.claude", "git": true, "restic": false, "max_file_size_mb": 10}
  ```
  - Missing fields describe what the destination is today, so current
    markers (only `source`) stay valid:
    - `git`: whether `D/.git` exists. A destination made with `--no-git`
      has none.
    - `restic`: whether its restic repository exists.
    - `max_file_size_mb`: 10.
  - Unknown fields (e.g. `remotes` from `feature_remote_destination.md`)
    are kept when the marker is rewritten.
- **`D/.autoversion.filter`:** a filter file in the shared format, without
  a `root` line (the root is the marker's source).
  - No file means `use default`, so current destinations keep working.
  - With git, both files are committed: `git log -p .autoversion.filter`
    shows every rule change.
- Both files are kept by the delete pass. Files with these names at the
  source root are never tracked.

## Rule input: flags for simple cases, a file for complex ones

Commands that take rules (`init`, `filter set`, `filter scan`,
`filter dump`, `watch --report-only`) accept:
- `--filter FILE`: a filter file in the shared format. A `root` line, if
  any, must resolve to the source.
- `--include RULE`, `--exclude RULE`: repeatable. A rule may have the `f:`
  prefix.
- `--use GROUP`: repeatable.

Flags and a file combine as a union (no order). With neither, the rules are
`use default`. `init` and `filter set` write the combined rules to
`D/.autoversion.filter`, normalized (sugar kept as written, one rule per
line, `use` lines first).

## Commands

Names follow ownit's `filter dump` and `filter scan` where the meaning is
the same.

| command | what it does |
|---|---|
| `init --source S --destination D [rules] [--no-git] [--restic] [--max-file-size MB] [--update-settings]` | creates or adopts the destination (as today), writes the marker and `.autoversion.filter`, validates, commits. On an existing destination, see "Changing settings" |
| `sync`, `watch`, `install` | read the marker and the filter file. Rule flags, `--no-git`, `--restic` and `--max-file-size` are errors here: "set with init or filter set" |
| `check-paths` | as today, plus the rules' validation result |
| `filter scan --source S [rules \| --destination D] [--explain PATH] [--summary]` | lists the kept files (was `filter`). `--explain PATH` prints the result, the level and the rule that decided it. `--summary` prints the number of kept files and of folders visited (= inotify watches on Linux) |
| `filter dump [rules \| --destination D]` | prints the expanded rules, groups included |
| `filter set --destination D rules [--dry-run]` | replaces `.autoversion.filter`, then the same as `filter check` |
| `filter check --destination D [--dry-run]` | after a hand edit: validates, prints the warnings, the change since the last commit and the counts (see "Safety checks"), commits (`filter: …`), and restarts the installed job if there is one |
| `filter groups` | lists the built-in groups and their rules, ready to copy into a file |
| `watch --report-only --source S [rules]` | NAS checks: no destination, so the rules are given |

### Changing settings

`init` on a destination that is already initialized for the source:
- **Settings equal to the marker's** (or none given): no change, as today.
- **Different settings** (`--max-file-size`, `--no-git`, `--restic`):
  - Without `--update-settings`: an error listing the differences.
  - With it: the marker is updated and committed. This works like
    `--force-new-source` for the source.
- **Git to plain mirror** (`--no-git` on a git destination): refused, even
  with `--update-settings`. The `.git` folder and its history would stay
  while files stop being committed. The error says how to do it on
  purpose: remove `D/.git` by hand, or start over with
  `--force-obliterate-yes-i-am-sure`.
- **Plain mirror to git** (no `--no-git` on a mirror, with
  `--update-settings`): `git init`; the next sync commits everything.
- Rules aren't settings: they change with `filter set` or `filter check`.
  Rules given to `init` on an existing destination are an error that
  points to `filter set`.

### Safety checks on rule changes

A sync removes from the destination every file the rules no longer keep.
With git, the history keeps them. On a plain mirror they are gone. So:
- **Allowlist with nothing alive** (shared spec): `exclude /*` with
  literal includes, none of which exists under the source, and no wildcard
  include. Usually a typo (`/CLAUD.md`). An error in `init`, `filter set`,
  `filter check` and `check-paths`. Only a warning when `sync` and `watch`
  start: by then the files may really have been deleted from the source,
  and mirroring that is right.
- **Nothing kept:** `filter set` and `filter check` refuse rules that keep
  no file of the source while the destination has files. This catches a
  typo in a wildcard include (`f:/**/*.mdd`).
- **Counts:** `filter set` and `filter check` print the number of kept
  files and of files the next sync removes from the destination, with the
  first few paths. On a plain mirror the line says the removed files are
  not kept anywhere.
- **`--dry-run`** on `filter set` and `filter check`: the checks and the
  counts, without writing, committing or restarting the job.

### Notes

- Validation errors stop the command. Warnings are printed and don't stop
  it:
  - dead literal rules (no such path under the source)
  - includes that change nothing
  - non-ASCII characters in a rule
- Removed flags: `--exclude-dir`, `--only-include-extensions`,
  `--only-include-file`.
- **install:** the plist keeps only runtime flags (`--buffer-time`,
  `--debug`, `--log-dir`, `--no-login`), plus a hash of the marker and the
  filter file (`EnvironmentVariables` → `AUTOVERSION_CONFIG`). After a
  change, re-running `install` sees a different plist and reloads the job,
  through the existing reload logic.

## Built-ins

### Always applied (not a group, can't be removed)

- `/**/.git` folders: a nested repository's `.git` must never be copied
  into a git destination.
- Symlinks: never followed, never tracked.
- `/.autoversion` and `/.autoversion.filter` at the source root.

### Groups

Each group is one named, code-defined set of rules, used with `use NAME`.
`default` uses all of them. All are excludes.

| group | rules |
|---|---|
| `os` | `f:/**/{.DS_Store,Thumbs.db,desktop.ini,.nfs*}`, `/**/{.Spotlight-V100,.Trashes,.dropbox,.dropbox.cache}` |
| `editors` | `f:/**/{*.swp,*.swpx,*.swo,*~,4913,*.iml}`, `/**/.idea` |
| `temp` | `f:/**/{*.tmp,*.tmp.*,*.temp,*.bak,*.old}` |
| `logs` | `f:/**/{*.log,*.out,*.stdout,*.err,*.stderr,*.pid,*.dump}` |
| `build` | `/**/{build,dist,out}/*`, `/**/target/{debug,release}`, `/**/{.build,DerivedData}`, `f:/**/{*.o,*.obj,*.so,*.a,*.lib,*.sym,*.dylib,*.rlib,*.rmeta,*.dll,*.exe,*.pdb,*.ilk,*.exp,*.gch,*.pch,*.d,*.depend}`, `f:/**/{*.class,*.jar,*.war,*.ear,*.pyc,*.beam}` |
| `deps` | `/**/{node_modules,bower_components,.pnpm-store,.npm,.venv,venv,__pycache__,.gradle}` |
| `caches` | `/**/{.cache,.next,.nuxt,.output,.pytest_cache,.playwright-mcp,playwright-report,test-results}` |
| `data` | `/**/{pgdata,*.pgdb}`, `f:/**/*.wal` |
| `vcs` | `/**/{.hg,.svn,.cvs,*.git}` (`*.git`: bare repositories) |
| `default` | `use os editors temp logs build deps caches data vcs` |

- **File rules (`f:`):** suffixes and file names, as today. A folder named
  `x.old`, `x.log` or `conf.d` is kept.
- **Folder rules:** they leave out the whole folder, and the scan never
  enters it. These names are never file names in practice.
- **`build`, `dist` and `out`** use `/x/*`. They're common words, so a file
  with that name stays.
- **Data, not code:** groups are defined as data (e.g.
  `filterrules/groups.json`, embedded), so they can be shared with ownit
  later.

### Translation from today's regexes

Today's patterns are regexes on the absolute path. A pattern ending with
`$` only applies to files. A pattern with `/name/` only applies to folders.

| today | new | change |
|---|---|---|
| `\.(log\|dump\|swp\|swpx\|swo\|pid\|lock\|beam\|bea#)$` | `logs`: `f:*.log` `f:*.dump` `f:*.pid`; `editors`: `f:*.swp` `f:*.swpx` `f:*.swo`; `build`: `f:*.beam` | `*.lock` dropped: today `Cargo.lock`, `uv.lock` and `flake.lock` are left out of the projects backup (a bug). `*.bea#` dropped (origin unknown) |
| `/\d+$` (file names made only of digits) | `editors`: `f:4913` | Not expressible. `4913` is Vim's write test file, the real case. A file named `2024` is no longer left out |
| `[~]$` | `editors`: `f:*~` | |
| `^(\.\|\.\.)$` | — | never matched an absolute path |
| `\.(out\|stdout\|err\|stderr\|depend\|dropbox)` (anywhere in the path) | `logs`: `f:*.out` `f:*.stdout` `f:*.err` `f:*.stderr`; `build`: `f:*.depend`; `os`: `.dropbox` `.dropbox.cache` | Suffixes and names only: `notes.output.md` and `x.errors/` are now kept |
| `\.(log\|old\|temp\|bak\|tmp)$` | `temp`: `f:*.old` `f:*.temp` `f:*.bak` `f:*.tmp` | |
| `\.(DS_Store\|nfs)$` | `os`: `f:.DS_Store` `f:.nfs*` | `.nfs*` is the real NFS temp name (`.nfs0000…`); `*.nfs` matched nothing |
| `\.(o\|obj\|…\|pch\|d)$` | `build`: `f:` rules | |
| `\.(class\|jar\|war\|ear)$` | `build`: `f:` rules | |
| `\.(git\|hg\|svn\|cvs)(/\|$)` | always `/**/.git`; `vcs`: `.hg` `.svn` `.cvs` `*.git` | |
| `\.tmp\.\d+$`, `\.tmp\.\d+\.\d+$` | `temp`: `f:*.tmp.*` | Broader: any suffix after `.tmp.` (atomic writes like `PLAN.md.tmp.18812.1760578375713`) |
| `\.(venv\|pytest_cache\|playwright-mcp)/` | `deps`: `.venv`; `caches`: `.pytest_cache` `.playwright-mcp` | |
| `node_modules/`, `__pycache__`, `pgdata/` (anywhere) | `/**/node_modules`, `/**/__pycache__`, `/**/pgdata` | Exact names: `my_node_modules/` is now kept |
| `target/debug/`, `target/release/` | `build`: `/**/target/{debug,release}` | |
| `/\.xcworkspace/` | — | Matched only a folder named exactly `.xcworkspace`: never in practice |
| `/\.gradle/` `/DerivedData/` `/\.build/` | `deps`, `build` | |
| `/dist/` `/build/` `/out/` | `build`: `/**/{build,dist,out}/*` | Same meaning: folder content only |
| `/\.cache/` `/\.next/` `/\.nuxt/` `/\.output/` `/playwright-report/` `/test-results/` | `caches` | A file named like these is now left out too (unlikely) |
| `/\.npm/` `/bower_components/` `/\.pnpm-store/` `/venv/` | `deps` | |
| `/\.yarn/`, `/\.vscode/` | — | No longer excluded: `.yarn/releases` and `.vscode/settings.json` are often committed project files |
| `/\.idea/`, `\.iml$` | `editors` | |
| `Thumbs\.db$` `desktop\.ini$` | `os`: `f:` rules | |
| `/\.Spotlight-V100/` `/\.Trashes/` | `os` | |
| `\.(pyc\|wal)$` | `build`: `f:*.pyc`; `data`: `f:*.wal` | |
| `\.pgdb/` | `data`: `*.pgdb` | |

Nothing is left out that isn't today, apart from the broader `*.tmp.*` and
folder rules also matching a file of the same name (unlikely names).
Everything else leaves out the same or less.

## Implementation

Four milestones. Each ends with `make test` and `make docker-go-test`
passing, `gofmt` clean, and a commit when the user asks.

### M1: the `filterrules` package

A new folder `filterrules/`: its own Go package in this module, standard
library only, with no dependency on autoversion's code. It can move to a
shared module when another project needs it.

Suggested API (paths are relative to the root, `/`-separated, no leading
`/`; the root itself is `""` and always kept):

```go
type Kind int  // Exclude, Include
type Type int  // Any (no prefix: files and folders), File ("f:"), Dir ("d:")

type Rule struct {
	Kind  Kind
	Type  Type
	Text  string // after sugar expansion, without the type prefix
	Group string // the built-in group it comes from, "" for the filter's own rules
	// parsed segments, ** position
}

type Error struct{ Code, Rule, Reason string; Line int } // codes from the shared spec

func ParseRule(kind Kind, text string) ([]Rule, error) // sugar may give several rules
func ParseFile(r io.Reader) (*File, error)            // version, root, types, use, rule lines
func (f *File) Add(kind Kind, text string) error      // a rule from a flag
func (f *File) Use(name string) error                 // a group from a flag
func Group(name string) ([]Rule, error)               // built-in groups, "default" included

type Rules struct{ /* literal rules in a map by path, wildcard rules in a list */ }

func New(f *File) (*Rules, error)                       // resolves groups, validates (whole-root, contradiction)
func (r *Rules) Decide(rel string, isDir bool) Decision // Kept, Level, Rule: the closest-match walk
func (r *Rules) CanMatchBelow(relDir string) bool       // an include can match below this folder
func (r *Rules) SkipDir(relDir string) bool             // left out, and nothing below can be kept
func (r *Rules) Check(list func(rel string) ([]string, error)) []Problem // warnings, and the allowlist with nothing alive (error)
func (r *Rules) Dump() string                           // expanded rules, for filter dump
```

Tests:
- the conformance suite (`testdata/filter_conformance.json`)
- every row of the `~/.claude` table
- one case per row of the translation table
- `f:` cases: a file matched at its level; a folder of the same name not
  matched; a folder level of an entry below it ignored. `d:` cases: the
  reverse, and a folder level of an entry below it matched
- parse errors with their codes, `contradiction`, `whole-root`
- `SkipDir` never skips a folder that holds a kept entry (checked on every
  test tree)

### M2: configuration in the destination

`destination.go`:
- The marker gets `git`, `restic` and `max_file_size_mb`. Missing fields
  mean the defaults. Unknown fields are kept when it's rewritten (decode
  into a `map[string]json.RawMessage` alongside the typed fields).
- `.autoversion.filter`: read (missing means `use default`), write, commit
  with the marker.
- `initDestination` writes both. `--force-new-source` and
  `--force-obliterate-yes-i-am-sure` keep their meaning. It also handles
  `--update-settings` as described in "Changing settings", including the
  refused switch from git to a plain mirror.
- `ValidateSourceAndDestination` returns the settings and the parsed
  rules, and adds rule errors to its problems.

`main.go`:
- Rule input (`--filter`, `--include`, `--exclude`, `--use`) on `init`,
  `filter scan`, `filter dump`, `filter set` and `watch --report-only`.
- The `filter` command becomes `filter scan`, `dump`, `set`, `check` and
  `groups`.
- `filter check` and `filter set` restart the installed job, through the
  same steps as `install`'s reload.
- `filter check` and `filter set` run the safety checks and print the
  counts (see "Safety checks on rule changes"), and take `--dry-run`.

### M3: use the rules everywhere

- **`filter.go`:** remove `excludePatterns`, `includeExtensions`,
  `excludeDirs`, `includeFiles`, `SetIncludeExtensions`, `SetExcludeDirs`,
  `SetIncludeFiles`, `IsExcludedPath`, `IsExcludedDir`,
  `matchesPathFilters` and their helpers.
  - Add `SetFilter(root string, rules *filterrules.Rules)` in place of
    `SetSourceRoot`, plus `trackedPath(path string, isDir bool) bool` and
    `skipDir(path string) bool`. They apply the always-applied rules first.
  - `ShouldTrack` keeps its symlink, folder, vanished-file and size checks,
    with the size limit taken from the marker.
- **Callers:**
  - `scanPaths`, `expandDirChanges`, the `filter scan` walk and
    `addRecursive` (`watcher_other.go`) use `skipDir`.
  - The watch loop and the macOS watcher's early filter use `trackedPath`.
    A delete event is decided as a file (`isDir` false).
  - `EventDirChanged` uses `skipDir`.
- **Removed flags:** `--exclude-dir`, `--only-include-extensions`,
  `--only-include-file`. `sync`, `watch` and `install` reject rule flags,
  `--no-git`, `--restic` and `--max-file-size`, and read the marker
  instead. `--no-git`, `--restic` and `--max-file-size` stay on `init`.

### M4: install, docs, checks

- **`launchd.go`:** `generatePlist` drops the rule flags, `--no-git`,
  `--restic` and `--max-file-size`, and adds the config hash in
  `EnvironmentVariables`. `parsePlistPaths` is unchanged.
- **README:**
  - rewrite "File Filtering", "Excluding directories", "Single files",
    "Directory Mirroring" and the Configuration table
  - document the `filter` subcommands and the filter file
  - add the `~/.claude` example
- **`scripts/check_watch.sh`:** create the destination with `init` and
  rules, and add an allowlist run (`exclude /*` and a few includes). The
  Makefile targets run it.
- **Plans:** mark this plan done and note any differences from it.

### Tests to update or remove

- `filter_test.go`: rewrite on top of `trackedPath`/`skipDir`. Remove the
  tests of the removed functions. Keep the symlink, size, vanished-file and
  marker cases.
- `main_test.go`:
  - `TestScanPaths_IncludeFiles` becomes an allowlist test.
  - `TestExpandDirChanges` keeps its cases (`node_modules`, `*.log`).
- `watcher_other_test.go`: `TestWatcher_IncludeFilesWatchOnlyTheirDirectories`
  becomes an allowlist test of `skipDir`.
- `launchd_test.go`: the new `generatePlist` arguments and the hash.
- `destination_test.go`: the marker fields (defaults, unknown fields kept),
  the filter file, `init` with rules.

### Done when

- The conformance suite and every table in this plan pass as tests.
- `make test`, `make docker-go-test` and `make docker-watch-check` pass.
  The user runs `make watch-check` on macOS.
- `filter scan --summary` on the `~/.claude` example shows about 250
  folders visited.
- The README documents every command and flag in this plan.

## Migration (run by the user after M4)

1. Build. Every running job fails at its next restart until it's
   reinstalled, because its plist flags are gone.
2. `claude_settings`: `filter set --destination … --filter FILE` with the
   `~/.claude` file above. The next sync adds the skills and memory files.
3. The other jobs need nothing:
   - no filter file means `use default`
   - `dropbox-projects` has no `.git`, so its missing `git` field reads as
     a plain mirror
4. The job script loses its rule flags, `--no-git` and `--max-file-size`
   (they live in the destination now). Then
   `./autoversion-install.sh install` and `jobs`.
5. NAS: rebuild. `watch --report-only` takes `--filter FILE` or rule flags.

## Decisions (2026-10-29)

- No prefix means a path rule (files and folders). `f:` means files only.
  `d:` was reserved; the second batch below implements it.
- `f:` with a `!` exception: settled by the spec, see the second batch.
- Settings of an existing destination change with
  `init --update-settings`. Switching from git to a plain mirror is always
  refused.
- `.yarn` and `.vscode` are no longer excluded. `*.lock` is dropped.
- The matcher is built in `filterrules/` here, and moved out when another
  project needs it.

## Decisions (2026-10-29, second batch, with the shared spec v3)

- `d:` is implemented (the spec and its conformance suite have it). The
  groups don't use it.
- `f:` with `!` is `bad-except` (the spec).
- The `types` line is accepted and kept, with no effect (see "The `types`
  line").
- `default` is the union of the nine groups: the spec says built-in Rules
  use nothing.
- `contradiction`: a literal include and a literal exclude with the same
  path, whatever their types, as in ownit's reference matcher.
- "Changes nothing" for a literal include is checked for each type the
  include applies to: as a file for `f:`, as a folder for `d:`, both for
  an untyped include. ownit did the same in f1aa055, and the spec says so.
- `whole-root` is only for an untyped `/*` without any include and an
  untyped `/**/*`: `f:/*` keeps every folder, `d:/*` every file (spec,
  f1aa055). Likewise, only an untyped `exclude /*` makes an allowlist for
  the "nothing alive" check.
- The conformance suite is copied from ownit d721a7f. It now also has the
  `sugar`, `invalid_files` and `invalid_filters` sections, all tested.
- Name patterns are matched by a precompiled matcher (1.6 µs per entry
  with `default`, about half of today's regexes), tested against
  `path.Match`.
- Allowlist with nothing alive, nothing kept, counts and `--dry-run`: see
  "Safety checks on rule changes".
- `make watch-check` builds into `build/watch-check/`, so it no longer
  replaces the `./autoversion` that installed jobs run.

# Plan: filter rules in the shared syntax, stored with the destination

**Status: proposal v3, 2026-10-29. Not implemented.** Breaking change: jobs
are re-migrated.

The syntax and its meaning are defined in
`~/projects/ownit/plans/spec_filter_syntax.md` (v3), the source of truth
shared by ownit, autoversion and minimon. This plan says how autoversion uses
it: where the rules live, the commands, the built-in groups, the code and the
migration. One addition is proposed for the shared spec: `exclude-files`
(see below).

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
| `skills/pdf/x.pyc` | left out | the entry itself (`default`'s `/**/*.pyc`) |
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
  the path rules before any file access, so this costs little.

Other jobs, for comparison:
- `projects`, `dotfiles`, `notes`, `sync`, `sublime`: the whole tree minus
  the built-ins. The filter file is `version 1` and `use default`, or there
  is no file.
- A tree minus one folder: `exclude /Backup`.
- Markdown and CSV only (today's `--only-include-extensions`):
  `exclude /*` and `/**/*.{md,csv}`.
  - A root-level `a.md` is kept: a wildcard include wins its tie with `/*`.
  - Deeper `.md` files are kept: the include matches the file, closer than
    `/*`.
  - As today, this reaches into `node_modules/`, because the include on
    the file is closer than `/**/node_modules`. Every folder is visited
    (`**` can match anywhere).
  - A wildcard include: native only, which is fine for autoversion.

## The syntax, in short

Read `spec_filter_syntax.md` for the exact definitions, sugar, error codes
and conformance suite. What matters most:
- **Rooted:** every rule starts with `/`, the source. `/**/x` means at any
  depth.
- **Wildcards:** `*` stays within one name, `**` is a whole segment (at
  most once, never last). There is no `?`, no `[…]` and no escapes.
- **Shorthands:** `{a,b}` braces, and `!` for exceptions.
- **No folder-only rules:** `/**/build/*` leaves out a build folder's
  content but keeps a file named `build`.
- **Closest match wins.** On the same level, a literal rule beats a
  wildcard rule; between two wildcards, the include wins. Two literal rules
  on one path are a `contradiction` error.
- **No order:** rules and groups combine as unions. Adding an include never
  leaves out more, and adding an exclude never keeps more.
- **Nothing is implied:** an allowlist says `exclude /*`.
- **Wildcard includes** (`/projects/*/memory`) are fine for autoversion's
  native matcher. Only the converters to other tools refuse them.

### Proposed addition: `exclude-files`

`exclude-files RULE` is an exclude that matches files only, never folders.
Today's built-ins work that way (`*.old`, `*.bak`, `*.log` leave out files,
not a folder named `x.old`), and a backup of a folder named
`osxphotos.old/` must not silently lose it.

- **Matching:** the rule takes part only at the entry's own level, and only
  when the entry is a file. When a folder level is checked, it's ignored.
  Otherwise it behaves like `exclude`: same syntax, same literal-or-wildcard
  ties.
- **Rules file:** a third keyword, `exclude-files /**/*.old`.
- **Converters, preliminary, to settle in the shared spec:**
  - gitignore and rsync have directory-only patterns (a trailing `/`). So
    an extra line that keeps folders of that name (`!/**/*.old/` in
    gitignore, `+ /**/*.old/` in rsync) can express it. Where that line
    goes in each tool's ordered list needs the same care as the `**`
    split.
  - restic, borg: no file type in patterns, so refuse. Writing it as a
    plain `exclude` would leave out more (data loss).
- autoversion only needs the native matcher, so it can use `exclude-files`
  right away. This is to be proposed to ownit's spec before the
  implementation.

## Where the rules live

The rules describe the destination, not one run. So they are stored in the
destination, and every command applies the same ones.

- `D/.autoversion` (the marker, JSON) gets the settings that aren't rules:
  ```json
  {"source": "/Users/nicolas/SyncDatabox/nicmac/.claude", "git": true, "max_file_size_mb": 10}
  ```
  Missing fields keep today's defaults: git, 10 MB.
- `D/.autoversion.filter`: a filter file in the shared format, without a
  `root` line. The root is the marker's source.
  - No file means `use default`, so current destinations keep working.
  - With git, both files are committed: `git log -p .autoversion.filter`
    shows every rule change.
- Both files are kept by the delete pass. Files with these names at the
  source root are never tracked.

## Input: flags for simple cases, a file for complex ones

Wherever rules are taken (`init`, `filter set`, `filter scan`,
`filter dump`, `watch --report-only`):
- `--filter FILE`: a filter file (shared format; a `root` line, if any,
  must match the source).
- `--include RULE`, `--exclude RULE`, `--exclude-files RULE`, `--use GROUP`:
  repeatable, for simple cases.

Flags and a file combine as a union (no order). With neither, the rules are
`use default`. `init` and `filter set` write the combined rules to
`D/.autoversion.filter`.

## Commands

Names follow ownit's `filter dump` / `filter scan` where the meaning is the
same.

| command | what it does |
|---|---|
| `init --source S --destination D [rules] [--no-git] [--max-file-size MB]` | creates the destination, writes the marker and `.autoversion.filter`, validates |
| `sync`, `watch`, `install`, `check-paths` | read the destination's rules; they take no rule input |
| `filter scan --source S [rules \| --destination D] [--explain PATH] [--summary]` | lists the kept files (was `filter`). `--explain` prints the result, the level and the deciding rule. `--summary` prints the counts of kept files and of folders visited (= inotify watches on Linux) |
| `filter dump [rules \| --destination D]` | prints the expanded rules, groups included |
| `filter set --destination D rules` | replaces `.autoversion.filter`, then the same as `filter check` |
| `filter check --destination D` | after a hand edit: validates, prints the warnings and the change since the last commit, commits it (`filter: …`), and restarts the installed job if there is one |
| `filter groups` | lists the built-in groups with their rules, ready to copy into a file |
| `watch --report-only --source S [rules]` | NAS checks: no destination, so the rules are given |

- Validation errors stop the command. Warnings are printed: dead literal
  rules (no such path under the source), and includes that change nothing.
- Removed: `--exclude-dir`, `--only-include-extensions`,
  `--only-include-file`.
- `install` writes only runtime flags in the plist (`--buffer-time`,
  `--debug`, `--log-dir`), plus a hash of the marker and the filter file
  (`EnvironmentVariables` → `AUTOVERSION_CONFIG`). After a rule change,
  re-running `install` reloads the job, through the existing reload logic.

## Built-ins in the new syntax

### Always applied (not a group, can't be removed)

- `/**/.git` folders: a nested repository's `.git` must never be copied
  into a git destination.
- Symlinks, never followed.
- `/.autoversion` and `/.autoversion.filter` at the source root.

### Groups

Each group is one named, code-defined Rules, used with `use NAME`.
`default` uses all of them. `x` is `exclude`, `xf` is `exclude-files`.

| group | rules |
|---|---|
| `os` | xf `/**/{.DS_Store,Thumbs.db,desktop.ini,.nfs*}`; x `/**/{.Spotlight-V100,.Trashes,.dropbox,.dropbox.cache}` |
| `editors` | xf `/**/{*.swp,*.swpx,*.swo,*~,4913,*.iml}`; x `/**/.idea` |
| `temp` | xf `/**/{*.tmp,*.tmp.*,*.temp,*.bak,*.old}` |
| `logs` | xf `/**/{*.log,*.out,*.stdout,*.err,*.stderr,*.pid,*.dump}` |
| `build` | x `/**/{build,dist,out}/*`, `/**/target/{debug,release}`, `/**/{.build,DerivedData}`; xf `/**/{*.o,*.obj,*.so,*.a,*.lib,*.sym,*.dylib,*.rlib,*.rmeta,*.dll,*.exe,*.pdb,*.ilk,*.exp,*.gch,*.pch,*.d,*.depend}`, `/**/{*.class,*.jar,*.war,*.ear,*.pyc,*.beam}` |
| `deps` | x `/**/{node_modules,bower_components,.pnpm-store,.npm,.venv,venv,__pycache__,.gradle}` |
| `caches` | x `/**/{.cache,.next,.nuxt,.output,.pytest_cache,.playwright-mcp,playwright-report,test-results}` |
| `data` | x `/**/{pgdata,*.pgdb}`; xf `/**/*.wal` |
| `vcs` | x `/**/{.hg,.svn,.cvs}`, `/**/*.git` (bare repositories) |
| `default` | `use os editors temp logs build deps caches data vcs` |

- **File-only rules:** suffix and file-name rules are `exclude-files`, as
  today. A folder named `x.old`, `x.log` or `conf.d` is kept.
- **Folder rules:** they leave out the whole folder, and the scan never
  enters it. Their names are never file names in practice.
- **`build`, `dist` and `out`** use `/x/*`: they're common words, so a
  file with that name stays.
- **Shared data:** group contents are data, kept next to the conformance
  suite so that ownit and autoversion can share them, with a commit noted
  when copied.

### Translation from today's regexes

Today's patterns are regexes on the absolute path. A pattern ending with
`$` only applies to files. A pattern with `/name/` only applies to folders.

| today | new | change |
|---|---|---|
| `\.(log\|dump\|swp\|swpx\|swo\|pid\|lock\|beam\|bea#)$` | xf `logs`: `*.log` `*.dump` `*.pid`; `editors`: `*.swp` `*.swpx` `*.swo`; `build`: `*.beam` | `*.lock` dropped: today `Cargo.lock`, `uv.lock` and `flake.lock` are left out of the projects backup (a bug). `*.bea#` dropped (origin unknown) |
| `/\d+$` (file names made only of digits) | xf `editors`: `4913` | Not expressible. `4913` is Vim's write test file, the real case. A file named `2024` is no longer left out |
| `[~]$` | xf `editors`: `*~` | |
| `^(\.\|\.\.)$` | — | never matched an absolute path |
| `\.(out\|stdout\|err\|stderr\|depend\|dropbox)` (anywhere in the path) | xf `logs`: `*.out` `*.stdout` `*.err` `*.stderr`; xf `build`: `*.depend`; x `os`: `.dropbox` `.dropbox.cache` | Suffixes and names only: `notes.output.md` and `x.errors/` are now kept |
| `\.(log\|old\|temp\|bak\|tmp)$` | xf `temp`: `*.old` `*.temp` `*.bak` `*.tmp` | |
| `\.(DS_Store\|nfs)$` | xf `os`: `.DS_Store` `.nfs*` | `.nfs*` is the real NFS temp name (`.nfs0000…`); `*.nfs` matched nothing |
| `\.(o\|obj\|…\|pch\|d)$` | xf `build` | |
| `\.(class\|jar\|war\|ear)$` | xf `build` | |
| `\.(git\|hg\|svn\|cvs)(/\|$)` | always `/**/.git`; x `vcs`: `.hg` `.svn` `.cvs` `*.git` | |
| `\.tmp\.\d+$`, `\.tmp\.\d+\.\d+$` | xf `temp`: `*.tmp.*` | Broader: any suffix after `.tmp.` (atomic writes like `PLAN.md.tmp.18812.1760578375713`) |
| `\.(venv\|pytest_cache\|playwright-mcp)/` | x `deps`: `.venv`; `caches`: `.pytest_cache` `.playwright-mcp` | |
| `node_modules/`, `__pycache__`, `pgdata/` (anywhere) | x `/**/node_modules`, `/**/__pycache__`, `/**/pgdata` | Exact names: `my_node_modules/` is now kept |
| `target/debug/`, `target/release/` | x `build`: `/**/target/{debug,release}` | |
| `/\.xcworkspace/` | — | Matched only a folder named exactly `.xcworkspace`: never in practice |
| `/\.gradle/` `/DerivedData/` `/\.build/` | x `deps`, `build` | |
| `/dist/` `/build/` `/out/` | x `build`: `/**/{build,dist,out}/*` | Same meaning: folder content only |
| `/\.cache/` `/\.next/` `/\.nuxt/` `/\.output/` `/playwright-report/` `/test-results/` | x `caches` | A file named like these is now left out too (unlikely) |
| `/\.npm/` `/bower_components/` `/\.pnpm-store/` `/venv/` | x `deps` | |
| `/\.yarn/`, `/\.vscode/` | — | No longer excluded: `.yarn/releases` and `.vscode/settings.json` are often committed project files |
| `/\.idea/`, `\.iml$` | x/xf `editors` | |
| `Thumbs\.db$` `desktop\.ini$` | xf `os` | |
| `/\.Spotlight-V100/` `/\.Trashes/` | x `os` | |
| `\.(pyc\|wal)$` | xf `build`: `*.pyc`; `data`: `*.wal` | |
| `\.pgdb/` | x `data`: `*.pgdb` | |

With `exclude-files`, nothing is left out that isn't today, apart from the
broader `*.tmp.*` and the folder rules also matching a file of the same name
(unlikely names). Everything else leaves out the same or less.

## Implementation

- **A package inside autoversion:** `filterrules/`, its own Go package in
  this module, with no dependency on autoversion's code. It can move to a
  shared module when another project needs it.
  - `Parse`: rules and filter files, sugar expanded, errors with the spec's
    codes
  - `Rules`: groups and `use`
  - `Decide(entry, isFile) (kept bool, level int, rule)`: the closest-match
    walk, with `exclude-files` at the entry's level only
  - `CanMatchBelow(folder)`: the walker rule from the spec
  - validation: `whole-root`, `contradiction`, includes that change
    nothing, dead literal rules
  - tests: ownit's `tests/filter_conformance.json`, copied unchanged with
    its commit noted, plus the group data and `exclude-files` cases
- **autoversion:**
  - The rules replace `matchesPathFilters`, `IsExcludedPath`,
    `IsExcludedDir`, `excludePatterns`, `includeExtensions`, `excludeDirs`
    and `includeFiles`. `ShouldTrack` keeps the symlink, folder,
    vanished-file and size checks.
  - The scan, `expandDirChanges` and the Linux watcher use
    `CanMatchBelow` to skip folders. The macOS watcher drops events with
    `Decide` before any file access (a removed entry's type is unknown, so
    `exclude-files` rules are assumed to apply to it).
  - The marker reads and writes the new fields. `init` and `filter set`
    write the filter file. `ValidateSourceAndDestination` returns both.
  - `generatePlist` drops the rule flags and adds the config hash.
- **Speed:** an entry costs depth × rules. To keep watch events cheap,
  literal rules sit in a map by path, and only wildcard rules are tried one
  by one.
- **Tests:**
  - the conformance suite
  - the `~/.claude` table above
  - the translation table (one case per row)
  - the marker and filter-file cases (missing file means `use default`)
  - `init`, `filter set`, `filter check`, `filter scan --explain`
  - the plist reload on a rule change
  - `check_watch.sh` with an allowlist mode
- **Size:** the matcher about 400 lines plus tests; autoversion's side about
  300.

## Migration

1. Build. Every running job fails at its next restart until it's
   reinstalled, because the rule flags are gone from the plist.
2. `claude_settings`: `filter set --filter FILE` with the `~/.claude` file
   above. The next sync adds the skills and memory files. Keep or rename
   the destination (its history is only `settings.json` so far).
3. `dropbox-projects`: the marker gets `"git": false` (`init` or by hand).
4. The other jobs need nothing: no filter file means `use default`.
5. The job script loses its rule flags. `./autoversion-install.sh install`,
   then `jobs`.
6. NAS: rebuild. `watch --report-only` takes `--filter FILE` or rule flags.

## Open question

`exclude-files` in the shared spec: propose it to ownit as written above
(keyword, matching, converters)?

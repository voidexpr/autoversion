# Plan: explicit defaults

**Status: approved 2026-11-01, in progress.** Decisions taken in
conversation on 2026-10-31 and 2026-11-01; the ownit spec owner has
accepted the `max-file-size` directive (its reply is summarized under
"Spec").

## Goal

A destination's rules file says everything about what is tracked. Nothing
is decided by a hidden default or a hardcoded rule, except the two things
that are the destination's own bookkeeping.

- The size limit is a directive in the rules file, `max-file-size 10M`,
  not a marker setting.
- `.git` folders are left out by a named group, `nested-git`, that a
  rules file uses or not. A git destination applies it always, on top of
  its rules, and shows it.
- `init` says how the destination versions: `--git`, `--restic`, both, or
  `--mirror`. No flag is an error.
- `init` requires rules. No rules is an error showing a starter file.
- Still implicit, on purpose: symlinks are never tracked; `/.autoversion`
  and `/.autoversion.filter` at the source root are never copied.

## Spec (ownit, accepted 2026-11-01)

- `max-file-size SIZE`, at most once, before the rules. SIZE is an integer
  with a unit `k`, `m` or `g`, binary, either case, examples write `10M`.
  `none` means no limit, the same as no directive. Anything else is
  `bad-directive`. Not allowed in a used Rules file (`bad-directive`).
- Applies after the rules, to kept non-folders only. An include never
  overrides it. The size is the entry's own, never followed through a
  link. An entry of unknown size (a deleted file) is decided by the rules
  alone. Exactly SIZE is kept ("larger than").
- rsync `--max-size=SIZE`, restic `--exclude-larger-than SIZE`. borg,
  gitignore and Syncthing: warning `max-file-size-unsupported`.
- Conformance: a case carries `"max_file_size": "4k"` and a `"sizes"`
  map, file entry to bytes; its `tools` list names the implementations
  that apply the limit. Not landed yet (b19fbdd unchanged); when it
  lands, ownit's `test_autoversion` runs the cases through our binary.

## Today, and what changes

| | today | after |
|---|---|---|
| size limit | marker `max_file_size_mb`, 10 by default, `--max-file-size MB` on init, scan, watch | `max-file-size SIZE` line in the rules file; no line, no limit; `--max-file-size SIZE` is a rule flag like `--use`, written into the file |
| `.git` | hardcoded `alwaysExcluded`, every command | group `nested-git` (`exclude /**/.git`); git destinations apply it always and show it; mirrors and restic only with `use nested-git` |
| `init` versioning | git unless `--no-git`; `--restic` adds restic | `--git`, `--restic`, both, or `--mirror`; none: error |
| `init` rules | none: writes `use default` | none: error with a starter file |
| `--max-file-size` on `watch --report-only`, `filter scan` | a number of MB | a SIZE, a rule flag; gone from `watch`/`sync` with a destination |
| marker | source, git, restic, max_file_size_mb | source, git, restic; `max_file_size_mb` read as legacy until migrated |

## Resolution of the size limit during migration

The rules file wins. Without a directive, the marker's `max_file_size_mb`
applies if the marker has one, else no limit. `filter dump --destination`
prints the limit in force and where it comes from. Once every destination
is migrated (M4), the marker field is only read, never written.

## Git destinations and `nested-git`

- `readRules`/`validatePaths` return the destination's rules; the
  constraint is applied in `sourceRules`, which gets a flag `nestedGit`
  set from the marker's `git`. `tracked`, `skipDir` and `walk` check it
  before the rules, like `alwaysExcluded` does today for the marker and
  rules file.
- Shown: `filter dump --destination` prints `# applied by the git
  destination: use nested-git`; `--explain` says `left out: nested-git,
  applied by the git destination`; `filter scan --destination` applies it.
- `filter scan`/`filter dump` without a destination: the rules given,
  nothing added.
- Mirrors copying `.git`: `walkDestination` skips only the destination's
  own root `.git` (today every `.git`, so stale nested copies would never
  be removed); `BackupRestic` excludes only the root `.git` (today
  `--exclude .git`, any depth).

## Implementation

Each milestone ends with `make test`, `make docker-go-test`, `gofmt -l .`
clean, and a commit. The binary under test goes to `build/dev/autoversion`;
no `make build`, no `make install`, no launchctl.

### M1: `max-file-size` directive

- `filterrules`: `File.MaxFileSize` (bytes, -1 for none/absent?) parsed
  from the directive, `Format` and `Dump` write it back, `bad-directive`
  for every malformed form and for a second one. A used group file with
  the directive is refused by the loader with `bad-directive`.
  `Rules.MaxFileSize()`; `Rules.TooLarge(size int64) bool`.
- `filterrules.ParseSize("10M")` and `FormatSize` for messages.
- Conformance test harness reads `sizes` and `max_file_size` when
  present (the file itself stays at b19fbdd until ownit lands the cases).
- The `--max-file-size SIZE` rule flag: added to the rules text like
  `use` lines (after the version line); with a `--filter` file that has
  the directive: error, "max-file-size twice". Removed from `watch` and
  `sync` with a destination (they read the destination's rules).
- `scanPaths`, `watch`, `ShouldTrack`, `explain`, `applyRules` take the
  limit from the rules (fallback to the marker as above).
- README: directive, flag, fallback.

### M2: `nested-git`

- `filters/nested-git.filter`: `# nested-git: repositories inside the
  source`, `exclude /**/.git`. `make filter`.
- `alwaysExcluded` keeps only the marker and rules file at the root.
- `sourceRules.nestedGit`, set from the marker's `git` by `useDestination`,
  `filter scan --destination`, `filter check`, `applyRules`, `install`.
- `walkDestination` and `BackupRestic` as above.
- Tests: a mirror copies `a/.git/x` and removes a stale `b/.git/y`; a git
  destination never copies `.git` even with `include /**/.git` in its
  rules; `--explain` text; `filter dump --destination` on both.
- README: the group, the constraint, mirrors and `.git`.

### M3: explicit `init`

- Flags `--git`, `--restic`, `--mirror`; `--mirror` is exclusive with the
  other two; none given: error naming the three. `--no-git` removed
  (unknown flag is fine: no plist carries init flags).
- Rules required: no `--filter` and no rule flag: error printing a
  starter file (`version 1`, `max-file-size 10M`, `use default`,
  `use nested-git`). `defaultRules` is no longer written; `readRulesText`
  keeps returning it for a destination without a rules file, with a
  warning naming `filter set`, until M4 removes it.
- `--update-settings` and `--force-keep/replace-marker-settings` keep
  working with the new flags.
- README: init section, table, examples, migration section.

### M4: migration and cleanup

- Per job, two commands, destination files and history untouched:
  ```
  rm DEST/.autoversion
  autoversion init --source SRC --destination DEST --filter RULES.filter --git --force-new-source
  ```
  The seven jobs of `~/dotfiles/bin/autoversion-install.sh` (read, not
  edited by me): six git destinations under `DESTINATION_BASE`, and
  `dropbox-projects`, a `--no-git` mirror of `~/projects` into Dropbox,
  which becomes `--mirror`. It keeps skipping `.git` at every level
  (decided 2026-11-01): its rules say `use nested-git` like the git jobs.
  No current job copies `.git`; the mirror fixes in M2 are for future
  jobs.
- Script changes for the user to make: `INIT_FLAGS` says `--git` or
  `--mirror`; `job_rules` gives every job a rules file (today only
  `claude_settings` has one, the others rely on `use default`), each with
  `max-file-size 10M`, `use default` and `use nested-git` for the git
  jobs; `init` gets `--filter -` from `job_rules`, since it now requires
  rules, and `set_rules` stays for later changes.
- After the jobs run on the new marker: `max_file_size_mb` no longer
  written; the `readRulesText` default and its warning removed; a marker
  with the field still reads.
- Plan note: what differed. Conformance file: copy ownit's when the size
  cases land, note the commit.

### Done when

- `make test`, `make docker-go-test` pass; `make filter` is a no-op.
- `filter scan --filter -` on a rules file with `max-file-size 4k` leaves
  out an 8k file and keeps a 4k one (what ownit's `test_autoversion` will
  do).
- A git destination with `include /**/.git` in its rules still copies no
  `.git`; `filter dump` says why.
- A mirror job copies `.git` folders and cleans stale ones.
- `init` without a versioning flag or without rules fails with the
  messages above.

## Not in this plan

- A size qualifier on rules (per-path limits).
- Symlinks as a directive.
- Bundling or snapshotting `.git` before copying (mid-run copies are
  accepted, 2026-10-31).

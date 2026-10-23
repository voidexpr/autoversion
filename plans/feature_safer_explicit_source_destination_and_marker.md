# Plan: explicit --source / --destination and a destination marker

## Problem

- The CLI takes the source as a positional argument and `--base` as a flag.
  Neither name says where files end up: the destination is computed as
  `base + (source - $HOME)` (with two more cases outside `$HOME`) and is only
  shown by `check-paths` and `install`.
- `sync`, `watch` and `install` create and `git init` any missing target
  (`validateTargetPath`), so a typo in the source or base silently starts a
  new repo.
- Nothing prevents two sources from syncing into the same destination, or
  source and destination from being swapped.

## Decisions

- CLI uses `--source S --destination D`. Files under S are mirrored directly
  into D (`S/rel` → `D/rel`). No positional path arguments.
- `--base` and the automatic path computation are removed. No deprecated
  alias: the installed launchd jobs are reinstalled (see Migration).
- D is marked by a `D/.autoversion` file, created only by `init`. Commands
  that copy files refuse to run unless the marker exists and names S.
- `init` is the explicit destination initialization (no separate
  `destination-init` command).
- The marker is versioned in git, so its content (the source) is tracked over
  time.
- `watch` checks the marker at startup only. If D disappears while watching,
  autoversion fails loudly instead of recreating it.

## CLI

`--source` and `--destination` replace `--base` as root persistent flags.

| command | `--source` | `--destination` | marker |
|---|---|---|---|
| `filter` | required | - | - |
| `init` | required | required | creates / updates |
| `sync`, `watch` | required | required | required, must match S |
| `watch --report-only` | required | - | - |
| `install` | required | required | required, must match S |
| `check-paths` | required | required | reported, missing or mismatch is an error |
| `uninstall`, `install-check`, `status` (detail) | required | required | not checked, must work when D is gone |
| `status` (no flags) | - | - | lists all jobs |

## Marker file `D/.autoversion`

- Content: JSON `{"source": "<absolute source path, symlinks resolved>"}`.
- Match: `--source` is resolved the same way and compared as a string.
- Versioned in git: when git is enabled, `init` commits the marker on its own
  (`git add .autoversion`, message `init: source <S>`), so every source change
  is a separate commit and `git log -p .autoversion` shows its history. Other
  pending changes in D are left for the next sync.
- Must survive syncing:
  - the delete pass in `scanPaths` (walks D and removes every file not in S;
    today it only skips `.git`) skips `D/.autoversion`
  - a `.autoversion` file at the root of S is never tracked (S having one
    means S is a destination, which validation refuses anyway)
- Checked by `sync`, `watch` and `install` at startup only.

## Destination disappearing while watching

Today, if D is renamed or unmounted while `watch` runs:
- `CopyFile` recreates it (`EnsureDir` = `MkdirAll` on the file's parent) and
  copies into it. With `--no-git` the mirror then silently continues in the
  wrong place (e.g. on the internal disk instead of an unmounted volume). With
  git, `git add` runs in a folder without `.git` and fails, or, if a parent
  folder is a git repo, adds the files to that repo.
- `Remove` with `--no-git` treats the missing files as "already gone".

Change: D is never recreated, and nothing is checked up front per batch.
- `CopyFile` creates missing parent directories only if D exists; otherwise
  it returns `errDestMissing`. The check only runs when a directory has to be
  created.
- `Remove` checks D only when a removal fails (git command error, or file not
  found with `--no-git`), and returns `errDestMissing` if D is gone.
- `processEvents` and `scanPaths` stop on `errDestMissing` (today they log
  copy and remove errors and continue). `watch` logs
  `destination <D> disappeared` and exits non-zero. launchd restarts it at
  most once per 6 h (KeepAlive + ThrottleInterval), and the restarted `watch`
  fails at the startup marker check until D is back.

## `init`

```
autoversion init --source S --destination D [--no-git] [--restic]
    [--force-new-source | --force-obliterate-yes-i-am-sure]
```

| D state | default | `--force-new-source` | `--force-obliterate-yes-i-am-sure` |
|---|---|---|---|
| missing | create, git init, write + commit marker | same | same |
| empty | git init, write + commit marker | same | same |
| marker names S | no-op ("already initialized") | no-op | wipe, re-init |
| marker names another source | error | rewrite + commit marker, keep contents and `.git` | wipe, re-init |
| not empty, no marker | error | write + commit marker, keep contents and `.git` | wipe, re-init |

- git init is skipped with `--no-git`, and when `D/.git` already exists. With
  `--no-git` the marker is written but not committed.
- `--restic` initializes the restic repo (today done on the fly by
  `validateTargetPath`). `sync` / `watch` with `--restic` then refuse if it
  is missing.
- After `--force-new-source` the next sync makes D match S: files not in S
  are removed (`git rm`, kept in history; with `--no-git` deleted for good).
  `init` prints this.
- `--force-obliterate-yes-i-am-sure` removes everything inside D, `.git`
  included, then initializes it like an empty D. The restic siblings
  (`<parent>/.restic_<name>`, `<parent>/.restic_password_<name>`) are not
  touched. Path validation still applies (D can't be `$HOME`, `/`, S, …).
- The two override flags are mutually exclusive.

## Validation

`ValidateSourceAndDestination` replaces `ValidateSourceAndBase`,
`validatePaths` and `validateTargetPath`. It returns the resolved paths, the
errors, and the marker status (missing / matches / names another source).

- S exists and is a directory.
- D is resolved with `resolvePath` (D may not exist yet for `init`).
- `verifyPaths` checks: neither is `$HOME` or `/`, not the same path, neither
  inside the other.
- No parent of D contains `.autoversion`, or `.git` when git is enabled. An
  outer destination's delete pass would remove an inner one's files.
- `S/.autoversion` must not exist (catches swapped flags).

Removed: `computeTargetPath`, `longestCommonDir`, the "base is a git repo"
check (D is the repo now), and the silent create + git init in
`validateTargetPath`.

## launchd

- `generatePlist` emits `watch --source S --destination D --log-dir <dir> …`;
  the positional source and the `basePath` comment go away. `--log-dir` is
  unchanged.
- `parsePlistPaths` reads the values after `--source` and `--destination`.
  An old-format plist fails to parse: `status` lists it without a source and
  `install` skips the collision check, same as for any parse error today.
- `jobIdentifier(source, destination)` keeps its formula. With D set to the
  old computed target, job IDs, plist names and log directories stay the
  same.
- `install` no longer creates or initializes D. It requires a matching
  marker, then runs the initial sync and loads the job as today.
- Output: `Target:` → `Destination:`; the `Remove:` hint in
  `printStatusInfo` becomes `autoversion uninstall --source S --destination D`.

## Terminology

| external | internal |
|---|---|
| `--source` | `sourcePath` (resolved: `absSourcePath`), `sourceRoot` in `CopyFile` |
| `--destination` | `destPath` (resolved: `absDestPath`), `destRoot` in `CopyFile` |

"target" is renamed to "dest" in code (`targetPath`, `targetRoot`,
`targetInfo`, `PathValidationResult.Target*`, `computeResticPaths` params)
and to "destination" in user-facing messages. "base" disappears.

## Docs and build files

- README: Quick Start, Managing the service, Manual commands, Directory
  Mirroring, Excluding directories, every command in Commands, the
  check-paths list. Replace "Path Computation" with a section on the marker
  and `init` flags. Replace "Why automatic path computation?" with "Why a
  destination marker?". Add the `init` flags to the Configuration table.
- `AUTOVERSION_PLAN.md`: Terminology and CLI sections.
- Makefile `docker-test`: run `init --source /input --destination /output`
  first, then `watch --source /input --destination /output --buffer-time 1`
  (two `docker run`, the image's entrypoint is `./autoversion`).
- cobra `Use` / `Short` / `Long` texts and error messages.

## Tests

- `paths_test.go`: remove `TestLongestCommonDir` and `TestComputeTargetPath`.
  `TestValidateSourceAndBase` → `TestValidateSourceAndDestination`: the
  existing cases plus D missing, S has a marker (swapped), parent of D has a
  marker, and marker missing / matching / naming another source.
- `init`: one test per row of the table above, the mutually exclusive flags,
  and the marker commit (`git log .autoversion` has one commit per source).
- `scanPaths`: marker not deleted, source-root `.autoversion` not copied,
  sync refuses without a matching marker.
- D removed after startup: `CopyFile` and `Remove` (git and `--no-git`)
  return `errDestMissing` and D is not recreated; `processEvents` and
  `scanPaths` return the error.
- `launchd_test.go`: `generatePlist` → `parsePlistPaths` round trip.

## Migration of the installed jobs

Each destination is set to the job's current computed target, so the
existing repos and their history are used in place and the job IDs don't
change. The log directories (`~/Library/Logs/autoversion/<job id>`, with
their rotated files) are derived from the job ID, so they don't move either.

| source | destination | flags |
|---|---|---|
| `~/Dropbox/notes` | `~/Data/Backup/autoversion/Dropbox/notes` | `--max-file-size 10 --buffer-time 3` |
| `~/Dropbox/Sync` | `~/Data/Backup/autoversion/Dropbox/Sync` | `--max-file-size 10 --buffer-time 3` |
| `~/SyncDatabox/dotfiles` | `~/Data/Backup/autoversion/SyncDatabox/dotfiles` | `--max-file-size 10 --buffer-time 3` |
| `~/SyncDatabox/nicmac/projects` | `~/Data/Backup/autoversion/SyncDatabox/nicmac/projects` | `--max-file-size 10 --buffer-time 3` |
| `~/SyncDatabox/nicmac/projects` | `~/Dropbox/Backup/nicmac/autoversion/SyncDatabox/nicmac/projects` | `--max-file-size 10 --buffer-time 45 --no-git` |

Steps (run outside the Claude sandbox: it can't read `~/Data` or `~/Dropbox`):

1. Uninstall each job. The launcher app runs the repo binary, so a job
   restarted after the rebuild fails on `--base` (exit 1, no data change).
   The new binary can uninstall the old jobs too
   (`autoversion uninstall --source S --destination D`), since the job IDs
   are the same.
2. Build and run the tests.
3. For each job:
   1. `autoversion check-paths --source S --destination D` (only error
      expected: destination not initialized)
   2. `autoversion init --force-new-source --source S --destination D` (add
      `--no-git` for the Dropbox mirror). Writes and commits only the marker;
      the existing history is untouched.
   3. `autoversion install --source S --destination D <flags>`
4. `autoversion status` lists the 5 jobs with their sources.

The first sync after `init` should find few or no changes, since the jobs
kept D in step with S until step 1. A one-off script (not committed) runs
steps 3–4.

## Out of scope

- Moving the restic repo inside D (it stays a sibling of D; none of the
  current jobs use restic).
- A `--base` alias or support for old-format plists.
- Linux / systemd.

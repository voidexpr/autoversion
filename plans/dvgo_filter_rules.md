# Plan (for dvgo): the shared filter rules in dvgo

**Status: draft (2026-11-02), written in the autoversion repo because its
author could not write to `~/projects/dvgo`. Move it to `dvgo/plans/`.**

## Context

Decided on 2026-11-02 (conversation in autoversion):

- autoversion evolves into a file copy tool and owns the filter library,
  `filterrules` and `filters` (the built-in groups), standard library
  only, with ownit's spec and conformance suite behind it.
- minimon stays on OS metrics.
- dvgo is the tool that measures and shows file hierarchies: it scans with
  its parallel walker, serves the UI, and now reads the shared filter
  syntax instead of its own gitignore-style excludes.

What dvgo has today (`pkg/dvgo`): a parallel walker (`walkDirectories`,
`GOMAXPROCS*2` workers over a directory queue, `scanDirectory` lstat's
every entry, hard links counted once, symlinks to folders not followed,
symlink files counted by their own size), a tree of directories only
(`Node`: name, size, count, mtime, children) folded below `MaxDepth` 10,
`scan.json`, and `estimate --exclude-from` applying gitignore patterns to
directories of a stored scan (`Exclude`). No tests.

What the library gives (see autoversion's README, "Using the filter
library from another Go module"): `ParseFile`, `New`, `Rules.Decide(rel,
isDir)`, `Rules.SkipDir`, `Rules.CanMatchBelow`, `Rules.MaxFileSize`,
`Rules.TooLarge`, `Rules.Check`, the groups.

## Goal

1. **Live scans with rules:** `dvgo scan PATH --filter FILE` (and the
   default scan-and-serve) walks with the rules applied: every rule
   honored, file rules and `max-file-size` included, since the walker
   sees every entry. The resulting `scan.json` is the backup set as the
   filter defines it, viewable in the UI before any backup runs.
2. **Stored scans with rules:** `dvgo estimate [FILE] --filter RULES`
   applies the rules to a scan that holds directories only. Directory
   rules are exact; the rules that need files are listed as not
   evaluated, never silently ignored.
3. **A file listing:** `dvgo files PATH --filter FILE`, the equivalent of
   `autoversion filter scan`, on the parallel walker: kept paths relative
   to `PATH` on stdout, a summary on stderr.
4. **Parity:** a test that runs dvgo and `autoversion filter scan` on the
   same tree with the same rules and compares what they keep.

## Decisions to take in dvgo

- **Dependency:** `import "github.com/nicolas/autoversion/filterrules"`.
  Local checkouts: a `go.work` over `~/projects/autoversion` and
  `~/projects/dvgo`, or `replace github.com/nicolas/autoversion =>
  ../autoversion` in dvgo's go.mod. dvgo's `go` directive moves from 1.22
  to 1.25 (`filterrules` uses 1.25).
- **`root` line:** a rules file with `root PATH` must name the scanned
  path (resolved), as autoversion requires; otherwise an error. Without
  a `root` line the rules apply to the scanned path.
- **Symlinks:** dvgo keeps counting symlink files, as a disk usage tool
  and as restic and borg do. autoversion never copies them. The parity
  test compares trees without symlinks, or dvgo takes a flag to leave
  them out. Pick one in M2 (proposal: a note in the usage text, no flag).
- **Hard links:** unchanged, counted once. autoversion copies each link
  as a file. Parity compares counts of kept paths, not sizes.
- **`nested-git`:** a group like any other in dvgo. The "git destination
  always applies it" rule is autoversion's.
- **`--exclude-from`:** keep it one release with a deprecation line in
  the usage, then remove it. The ownit spec's gitignore converter is the
  way back if a gitignore list is all someone has.
- **Command name for the listing:** `files` (fits "dvgo measures file
  hierarchies"; also the name of the minimon plan this takes over). A
  directory literally named `files` is written `./files`, as for `scan`.

## Applying rules to a directory-only tree (estimate)

The tree node of a directory holds the subtree's size and count. Its own
files are `direct = size - sum(children.size)`, same for count. The
depth cap folds deeper content into the node at depth 10.

For each directory `d` (path `rel`, depth first from the scanned root):

- `Decide(rel, true).Kept` and no exclude below it: keep as is.
- Left out, and `!CanMatchBelow(rel)`: prune, like today's `Exclude`.
- Left out, but an include can match below: keep the node as a
  container, drop its `direct` part (its own files are left out: the
  closest match for them is `rel`), recurse into children.
- Kept: recurse into children, subtract what they lose.

Then the report. On a directory-only tree a rule can only match
directories, so:

- `f:` rules, and `max-file-size`: not evaluated, listed by rule.
- Untyped rules with a wildcard last name (`*.log`, `*~`): applied to
  directories only; the files they would exclude are not counted. Listed
  as "applied to folders only".
- Literal untyped rules (`/**/.cache`, `/Library/Caches`): exact.
- Rules deeper than the folded depth: cannot match, listed.

The printed summary keeps today's shape (total, excluded, remaining) and
adds "not evaluated: N rules (file rules), the estimate is a lower bound
of what the filter removes".

## Live scan with rules (scan, files)

`walkDirectories` gets the rules:

- Before pushing a subdirectory: `SkipDir(rel)` skips it entirely (left
  out and nothing below can be kept); a directory left out but with a
  possible include below is walked, its own files left out, as above.
- Per non-directory entry: `Decide(rel, false).Kept`, then
  `TooLarge(size)` with the entry's logical size (`st_size`, not the
  allocated size: the spec's size is the file's own).
- The tree model then only receives kept entries, so `scan.json` and the
  UI show the filtered set with no further change. `scan_time` and the
  rest stay.
- `files`: the same walk with a sink that prints `rel` for each kept
  entry. Order is nondeterministic from the parallel walker: sort per
  directory and print directories in the order they finish, or collect
  and sort at the end (10M paths fit; proposal: per directory, with a
  `--sorted` flag for a global sort). Summary on stderr: `N files kept
  (SIZE), M folders visited`, the same line as autoversion.

Entry paths given to the library: relative to the scanned path, `/`
separated, no leading or trailing slash; the root itself is `""`.

## Tests

dvgo has none today, so M1 starts the test setup:

- `pkg/dvgo`: unit tests on a synthetic `ScanResult` for the estimate
  algorithm (prune, container with kept descendants, direct part
  removed, the report categories), and on the walker with a temp tree
  (kept and left-out entries, `max-file-size`, a skipped folder not
  entered).
- Parity: a test that builds a temp tree, writes a rules file, runs
  `dvgo files` in-process and `autoversion filter scan --filter FILE
  --no-summary` as a subprocess (skipped when the binary is not in
  PATH), and compares the sorted lists without symlinks.
- Conformance: optional, the library already runs ownit's suite; dvgo
  needs no copy.

## Milestones

Each ends with `go vet`, `go test ./...`, `gofmt -l .` clean and a commit.
`make build` writes `bin/dvgo`; `make dev` links it to `~/bin/dv`, so a
work-in-progress binary goes elsewhere.

### M1: dependency and estimate

- go.mod: Go 1.25, the dependency, the workspace or replace documented
  in the README.
- `estimate [FILE] --filter RULES`: the algorithm and the report above.
  `--exclude-from` stays, marked deprecated in the usage.
- Tests for the estimate algorithm.

### M2: live scan and files

- `scan --filter`, the default command with `--filter`, `files PATH
  --filter FILE [--sorted]`.
- Walker hooks, the symlink decision, the summary line.
- Walker tests and the parity test.

### M3: docs

- README and usage: the syntax is autoversion's, link to its README
  section and to `filters/README.md` for the groups; the directory-only
  limits of `estimate`; the deprecation of `--exclude-from`.

### Done when

- `dvgo scan ~ --filter backup.filter` serves what the filter keeps, and
  `dvgo files ~ --filter backup.filter | wc -l` equals `autoversion
  filter scan --source ~ --filter backup.filter --no-summary | wc -l`
  (symlinks aside).
- `dvgo estimate --filter backup.filter` on an old `scan.json` prints the
  directory estimate and lists the rules it could not evaluate.

## Later (not in this plan)

- borg and restic snapshots as scans: `restic ls --json` and `borg list
  --json-lines` list every file, enough to build the same tree, with
  files, so rules apply fully to a snapshot.
- Files in the scan format, all or above a size, so `estimate` can
  evaluate file rules offline. The minimon `files` plan's manifest notes
  (streaming, no repeated prefixes, subtree skipping) apply if it comes
  to that.
- The views of the minimon plan: biggest files, by extension, by age,
  duplicates, diffs between scans.

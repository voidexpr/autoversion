# filterrules API: the walker drives, no silent skip of markers and xattrs

Status: planned 2026-11-14. Benchmarks before and after are at the end.

## Problem

dvgo built its rules with `filterrules.New` and called `Rules.Decide`
without `WithMarkers` or `WithXattrs`. Nothing told anyone: `use
cache-markers` and `use time-machine` were no-ops in dvgo. The lookups are
optional extras and not calling them is the default.

The marker lookup is cached per walk in autoversion (one `lstat` per
folder per marker name). The xattr lookup added in 34b3242 caches per
entry, which for files is waste: a file is decided once, and the cache
would hold one entry per file (about 150 k entries, 20 MB, on the
projects tree).

Every file re-matches all its ancestor levels on each `Decide`.

## Design

The walker drives, the library keeps one node per folder.

- `tree := rules.Bind(root)`, once per scan. `dir := tree.Root(entries)`,
  `child := dir.Child(name, entries)`, `child.File(name, isDir, hasXattr)`.
  A node holds its folder's decision, computed once.
- `entries` is the folder listing the walker already read (`[]string` of
  names). Markers become a map lookup: zero syscalls. `nil` means "I don't
  have it": the library does one `lstat` per marker name, as today.
- `hasXattr` is `func(name string) bool`. The caller passes what it has;
  `nil` means the library calls `lgetxattr` itself, only when the rules
  have an `xattr:` line. `filterrules.NoXattrs` is the explicit opt-out.
- There is no way to decide an entry without a node, so no silent skip.
  The shortest correct code for a walker is `tree.Root(entries)` and
  `Child(name, entries)`, which it has in hand.
- `tree.Decide(rel, isDir)` stays for the path-based cases: `explain`, a
  watch event, tests. It builds the chain from the root with `ReadDir` (or
  `lstat` per marker) and `lgetxattr` per level, uncached: one event, one
  chain, as today.
- dvgo's walk is single scans; autoversion's watch binds a fresh tree per
  event, since a marker or attribute can appear or go between events.

## Memory

- Nodes are immutable and hold a parent pointer. Live memory is the
  nodes of folders in flight: `depth` for a sequential walk, `depth ×
  workers` for a parallel one. About 100 bytes each. Finished subtrees are
  garbage.
- No cache maps. Minus the marker cache (~25 k entries, ~3 MB on the
  projects tree), minus the per-entry xattr cache (~20 MB).

## Syscalls

| rule in use | today | proposed |
|---|---|---|
| none | 0 | 0 |
| markers | 1 `lstat` per folder per name | 0 with the listing given, else the same |
| xattr | 1 `lgetxattr` per folder and per file per name | the same, or 0 where the caller passes it; never read twice |

The library never stats on its own otherwise; `isDir` comes from the
walker's dirent.

## CPU

A file matches its own level, then reads its parent node's decision.
Today it re-matches every ancestor level: fewer rule matches per file,
proportional to depth.

## Parallel scanners

Nodes are read-only after creation. Sibling goroutines share the parent
node without a lock. No shared mutable state.

## Changes

- `filterrules`: `Tree` and `Dir` types; `Rules.Decide`, `SkipDir`,
  `CanMatchBelow` move to `Tree`/`Dir`; `WithMarkers` and `WithXattrs` go.
  `NoXattrs` sentinel. About 150 lines.
- autoversion: `sourceRules.walk` becomes a `ReadDir` loop building nodes
  and drops the marker cache; `tracked`, `skipDir` and `explain` use
  `tree.Decide`. The watcher's resync and events bind per call.
- Tests: ~20 call sites; the conformance runner feeds the case's tree as
  listings and `xattrs` as the lookup.
- dvgo: `rules.Bind(abs)` and the node calls in its walker, plus `go get`.

## Benchmark

`BenchmarkWalk` in `filter_bench_test.go`: a generated tree (depth 4,
about 2 000 folders and 40 000 files, `CACHEDIR.TAG` in every 20th folder,
the Time Machine attribute on every 50th folder and every 200th file),
walked with three rule sets: `use default`, `use default` + `use
cache-markers`, both + `use time-machine`. Reports ns/op, allocs/op and
lookups/op (marker `lstat`s and `lgetxattr`s the library asked for). Plus
`filter scan --summary` timings on `~/projects` with the same three sets.

### Before

(filled in below)

### After

(filled in below)

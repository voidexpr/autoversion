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

- `tree := rules.Bind(root)`, once per scan. `dir := tree.Root()`,
  `child := dir.Child(name, list)`, `child.File(name)`. A node holds its
  folder's decision, computed once.
- `list` returns the folder listing (names). The library calls it at most
  once, and only when no path rule decides the folder and the rules have
  marker lines; the walker reads the folder in it and reuses the result
  when it enters the folder. Markers then cost no syscall, and a folder a
  rule leaves out is never listed (the review of 32da595 found the first
  version listing every subfolder before asking Skip). `nil` means "I
  don't have it": the library does one `lstat` per marker name, as today.
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

### Before (34b3242 plus the counter, macOS, 2 800 folders, 56 000 files)

| rule set | ns/op | kept | lookups/op | B/op | allocs/op |
|---|---|---|---|---|---|
| default | 237 ms | 44 940 | 0 | 36.5 MB | 404 k |
| markers | 241 ms | 40 348 | 8 400 | 49.3 MB | 908 k |
| markers+xattr | 999 ms | 39 760 | 63 084 | 80.3 MB | 1 271 k |

Markers: 8 400 lookups is one `lstat` per folder per marker name (2 800 ×
3). Xattr: one `lgetxattr` per folder and per kept-folder file, about 13 µs
each on APFS; that is the 760 ms. The 31 MB between default and
markers+xattr are the two caches.

`filter scan --summary` on `~/projects` (28 k folders, 160 k files kept):
`use default` 4.0 s (first run, cold), `+ cache-markers` 2.0 s,
`+ time-machine` 6.6 s.

### After (same tree, same machine; with the review's fixes and the lazy listing)

| rule set | ns/op | kept | lookups/op | B/op | allocs/op |
|---|---|---|---|---|---|
| default | 210 ms (−11%) | 44 940 | 0 | 31.9 MB (−13%) | 351 k (−13%) |
| markers | 198 ms (−18%) | 40 348 | 0 (was 8 400) | 30.8 MB (−38%) | 338 k (−63%) |
| markers+xattr | 942 ms (−6%) | 39 760 | 55 055 (−13%) | 42.8 MB (−47%) | 550 k (−57%) |

- Markers cost nothing: the listing the walker read answers them.
- Xattr is one `lgetxattr` per folder and per file reaching that tier,
  ~13 µs each on APFS, inherent to the rule: the caller can pass the
  answer instead (`Lookups`), the library never reads one twice.
- Memory: no cache maps; per folder one `Dir` (parent pointer, path,
  names, decision, a `[]bool` per marker rule) and the listing names the
  walker holds while in the folder. A file's path slice lives on the stack
  up to depth 31; its path string is built only for an xattr lookup.
- Of the remaining allocations, 80% are the OS listing (`ReadDir`,
  `unixDirent`) and the `lstat` behind `DirEntry.Info` per kept file, as
  before.

`filter scan --summary` on `~/projects`, old and new binaries alternated,
three rounds each (warm cache): `use default` old 4.1–4.4 s, new 4.0 s;
`+ cache-markers` old 4.4–4.5 s, new 3.9–4.2 s, and after the lazy
listing old 2.1–6.4 s, new 1.5–4.1 s; `+ time-machine` old 7.9–9.5 s, new
6.7–7.3 s (160 k `lgetxattr` calls either way).

## dvgo

Its scanner builds one `Dir` per folder from the listing it reads, and
decides files from it; a parallel scanner shares the parent `Dir` across
goroutines. `rules.Bind(abs)` replaces nothing it had: `Rules.Decide` is
gone, so the old code doesn't compile. `go get github.com/voidexpr/autoversion@<commit>`.

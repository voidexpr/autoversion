# Plan: filter groups as reusable files

**Status: done (2026-10-31).** See "Decisions" and "What differed" at the
end.

## Goal

- Built-in groups live in plain files, `filters/NAME.filter`, that other
  tools can read.
- The README shows the current groups, and can't go stale.
- New groups, not used by default: `music`, `photos`, `videos`, `media`,
  `nas`.
- No added runtime cost, and no change to `default`.

## Which way around: the files are the source of truth

| option | how | for | against |
|---|---|---|---|
| A. `groups.json` stays, `make filter` generates the files | as asked first | no loader change | two copies of every rule; generated files to commit and keep fresh; JSON is a format nothing else reads |
| **B. `filters/*.filter` is the source, embedded in the binary** | `//go:embed` | one copy, in the shared syntax; the existing parser reads it; nothing generated but a README block | one parse per process (below) |
| C. `filters/*.filter` is the source, `go generate` makes Go tables | generator | no parse at all | a generator and generated code to keep fresh, to save about 40 µs per process |

**Decision: B.**

## Runtime cost

Measured on 2026-10-31 (M-series Mac, `default` has 81 rules):

| what | when | today | with B |
|---|---|---|---|
| load the groups | once per process | 54 µs, JSON, in every process, even `status` | about 42 µs, only when rules use a group |
| `New` for `use default` | once per process | 10 µs | same |
| read `filters/` from disk | | no | no: the files are compiled in |
| decide one entry, `default` | every file and event | 1691 ns | same: `default` has the same rules |

- So B costs less than today at startup, and nothing per event. It isn't
  literally zero: the files are parsed once per process, which is accepted
  (see Decisions).
- `media` and `nas` cost nothing unless a job uses them:
  - `use nas` adds 3 rules. Two extra rules measured about 14 ns per entry.
  - `use media` adds 91 file rules, about 0.6 µs per file (estimate from
    that measure).
- The binary works anywhere: `filters/` is only needed to build.

## Layout

`go:embed` can't reach a parent folder, so `filterrules/` can't embed
`../filters`. `filters/` gets one small Go file:

```go
// filters/filters.go
package filters

import "embed"

//go:embed *.filter
var FS embed.FS
```

`filterrules` imports it. It stays standard library only. When
`filterrules` moves to a shared module, `filters/` moves with it.

A group file is a rules file in the shared format. Its first line is
`# NAME: what it is`, which `filter groups` shows:

```
# os: operating system and sync client files
version 1

exclude f:/**/{.DS_Store,Thumbs.db,desktop.ini,.nfs*}
exclude /**/{.Spotlight-V100,.Trashes,.dropbox,.dropbox.cache}
```

A group that combines others only has `use` lines:

```
# default: the groups applied unless a filter says otherwise
version 1

use os
use editors
use temp
use logs
use build
use deps
use caches
use data
use vcs
```

No file has a `root` or `types` line: those are a filter's choice.

## The groups

The nine groups of `groups.json` and `default` move as they are.

New, not in `default`. A NAS job writes `use default` and `use nas`:

```
# nas: Synology metadata
version 1

exclude /**/@eaDir
exclude f:/**/{*@SynoResource,*@SynoEAStream}
```

The media groups are all `f:` (a folder named `x.mp3` stays):

```
# music: audio files
version 1

exclude f:/**/{*.mp3,*.m4a,*.m4b,*.aac,*.flac,*.alac,*.wav,*.aif,*.aiff,*.ogg,*.oga,*.opus,*.wma}
exclude f:/**/{*.MP3,*.M4A,*.M4B,*.AAC,*.FLAC,*.ALAC,*.WAV,*.AIF,*.AIFF,*.OGG,*.OGA,*.OPUS,*.WMA}
```

```
# photos: images and camera raw files
version 1

exclude f:/**/{*.jpg,*.jpeg,*.png,*.heic,*.heif,*.gif,*.tif,*.tiff,*.bmp,*.webp,*.dng,*.cr2,*.cr3,*.nef,*.arw,*.orf,*.raf,*.rw2}
exclude f:/**/{*.JPG,*.JPEG,*.PNG,*.HEIC,*.HEIF,*.GIF,*.TIF,*.TIFF,*.BMP,*.WEBP,*.DNG,*.CR2,*.CR3,*.NEF,*.ARW,*.ORF,*.RAF,*.RW2}
```

```
# videos: video files
version 1

exclude f:/**/{*.mp4,*.m4v,*.mov,*.avi,*.mkv,*.webm,*.wmv,*.flv,*.mpg,*.mpeg,*.3gp,*.m2ts,*.vob,*.ogv}
exclude f:/**/{*.MP4,*.M4V,*.MOV,*.AVI,*.MKV,*.WEBM,*.WMV,*.FLV,*.MPG,*.MPEG,*.3GP,*.M2TS,*.MTS,*.VOB,*.OGV}
```

```
# media: music, photos and videos
version 1

use music
use photos
use videos
```

- **Both cases are listed.** Rules match byte for byte, and the syntax has
  no case-insensitive form. Cameras often write `IMG_1.HEIC` or `CLIP.MOV`.
  Mixed case (`a.Jpg`) isn't matched: a known limit, leaving more rather
  than less.
- **Left out on purpose**, because they match source files:
  - `*.ts` and lowercase `*.mts`: TypeScript. Only the camera form `*.MTS`
    is listed.
  - `*.mod`: it would match `go.mod`.
  - `*.raw`, `*.svg`, `*.ico`, `*.psd`: generic, or project assets.
- **Only exclude rules.** "Only my media" is an allowlist the user writes:
  `exclude /*` and `f:/**/*.{jpg,png}`.

## Loader

`filterrules/groups.go` reads `filters.FS` in place of `groups.json`.
- **Loaded on first use**, with `sync.OnceValue`. It can't stay a package
  variable: `ParseFile` checks `use` names against the groups, which would
  be an initialization cycle. Commands that use no group parse nothing.
- **`File.Use` checks the name without parsing**, by looking for
  `NAME.filter` in `filters.FS`. Otherwise the load would re-enter itself.
  So the parser takes the name check as a parameter: `ParseFile` passes
  that lookup, and the loader passes the same one.
- A rule's `Group` is the file it's written in. `default` keeps showing
  `group deps`, as `--explain` does today.
- A bad embedded file panics, as today. The tests load every file, and the
  Docker build runs them.
- `Groups()` returns them alphabetically (the order of the folder), so
  `filter groups` changes order. It also prints the `use` lines of
  `default` and `media`.

## `make filter` and the README

The README's hand-written group list is replaced by a block between
markers, with the same text as `autoversion filter groups`:

```
<!-- filter-groups:start, generated by make filter -->
...
<!-- filter-groups:end -->
```

- **The check is a test**, `TestREADMEFilterGroups`: it compares the block
  with the groups, and fails with "run make filter".
- **`make filter`** runs the same test with `-update`:
  `go test -count=1 -run TestREADMEFilterGroups . -args -update`. It adds no
  code to the binary.
- It's the only test that writes outside a temp folder: only `README.md`,
  only between the markers, only with `-update`.
- **No CI workflow.** `make test`, `make docker-go-test` and the Docker
  build, which runs `go test`, all catch a stale README.

## Other tools

- `filters/*.filter` can be read as they are by anything that reads the
  shared syntax.
- `filter dump --use NAME` prints any group flat, without `use` lines.
- **For the ownit spec:**
  - It says the groups live in `filterrules/groups.json`: that becomes
    `filters/`.
  - It says `use` can't name a file and code-defined Rules use nothing. A
    tool reading `default.filter` or `media.filter` directly must resolve
    `use NAME` as `NAME.filter` beside it, so the spec needs a line for
    that. Until then, those tools use `filter dump`.
  - The media rules and `nas`'s `*@Syno…` rules are typed, so restic and
    borg need `types decay`. A folder named `x.mp3` is then left out there
    too.

## Effect on existing jobs

None:
- **M1:** the same rules, in the same order (a test proves it).
- **M2:** the new groups are opt-in, and `default` doesn't change.

Using `nas` on an existing destination is a rules change like any other:
`filter set --dry-run` lists what the next sync removes.

## Implementation

Each milestone ends with `make test` and `make docker-go-test` passing,
`gofmt` clean, and a commit when asked.

### M1: move the groups to files, same rules

- Add `filters/filters.go` and one file per group.
- Change the loader. Remove `groups.json`.
- Before removing it, a test compares every group's rules with the JSON's,
  in order. Removed in the same commit, with the JSON.
- Update `TestGroups` for the alphabetical order.
- New test: every file has the `# NAME: ` line, a name matching its file,
  `version 1`, only excludes, no `root` or `types`.

### M2: new groups

- Add `music`, `photos`, `videos`, `media`, `nas`.
- Tests:
  - left out with `use media`: `a.mp3`, `a.MP3`, `IMG.HEIC`, `clip.mov`,
    `clip.MOV`, `CLIP.MTS`
  - kept with `use media`: `x.ts`, `x.mts`, `go.mod`, `logo.svg`, `a.Jpg`,
    `a.move`, `x.mp3/file`
  - left out with `use nas`: `@eaDir/x`, `a/@eaDir/x`, `a.jpg@SynoResource`,
    `a.jpg@SynoEAStream`
  - kept with `use nas`: a folder `x@SynoResource/` and its content
  - kept with `default`: `a.jpg`, `a.mp4`, `@eaDir/x`, `a.jpg@SynoResource`

### M3: `make filter`, README

- `TestREADMEFilterGroups` and the `filter` target. The name doesn't clash
  with the `filters/` folder.
- README: the generated block, how to read the files, the new groups.
- `feature_include_exclude_rules.md`: note that `groups.json` became
  `filters/`.

### Done when

- `make test` and `make docker-go-test` pass. `make filter` leaves the
  README unchanged when run twice.
- `filter groups` works from another folder, with `filters/` out of reach.
- Benchmarks: the load is no slower than today's 54 µs, and `Decide` with
  `default` matches today's, within noise.

## Not in this plan

- Case-insensitive rules: a change to the shared syntax.
- Reading group files from disk at runtime, or user-defined groups.
- Include versions of the media groups.
- A CI workflow.

## Decisions (2026-10-31)

- Option B. Parsing the embedded files once per process, at startup, is
  accepted: about 42 µs, less than today, nothing per event.
- Videos: `*.mov`. `*.move` isn't listed: not a video extension, and it
  matches Move language sources.
- No CI workflow.
- Synology goes in a new `nas` group, with `@eaDir`, `*@SynoResource` and
  `*@SynoEAStream`, not in `os`: it's a rare case, so `default` stays as
  it is. `*@SynoEAStream` is from memory, not checked on a NAS.

## What differed (2026-10-31, after M3)

- **README:** not a generated block in README.md. Its hand-written digest
  stays, and links to `filters/README.md`, fully generated by `make filter`
  (`TestFiltersDoc` with `-update`), one section per group with a link to
  its file. `make test` fails while it is stale.
- **No name-check parameter.** `File.Use` checks `NAME.filter` in
  `filters.FS` directly (`groupExists`), which avoids the re-entry just as
  well; `ParseFile`'s signature is unchanged. `GroupNames()` also lists the
  folder without loading, so an `unknown-rules` error raised while the
  groups load can't re-enter the load.
- **Cycle check** in the loader: `use` chains are resolved recursively
  with a stack, and a cycle (a group using itself included) is an error
  naming the chain. The loader takes an `fs.FS`, so the tests feed it bad
  files (no header, wrong name, include, root, types, unknown use, cycles)
  with `testing/fstest`.
- **M1 in two commits:** the JSON comparison test and the files first
  (cfa9dae), then the JSON and the test removed (a8780eb).
- **`default`'s about** is "the groups applied unless a filter says
  otherwise" (was "all the groups above", wrong once alphabetical).
- **Load benchmark**, `BenchmarkLoadGroups`, measured after M3 (same Mac):

  | what | µs | allocs |
  |---|---|---|
  | JSON loader, 10 groups, 81 rules (commit 6b83821) | 54 | 746 |
  | files, the same 10 groups | 63 | 925 |
  | files, all 15 groups, 172 rules | 133 | 1768 |
  | `Decide` with `default` | 1.66 (was 1.76) | 1 |

  So the format costs about 9 µs; the rest is parsing the media groups'
  brace lists (rule parsing alone is 59 µs of the 133). The plan's "no
  slower than 54 µs" doesn't hold as written, but the load now runs only
  in processes that use a group, where it ran in every process before,
  and nothing changed per event.

### For the ownit spec

Done in the spec on 2026-10-31. It resolves `use NAME` to `NAME.filter`
beside the file (not `filter dump`), names the cycle error `use-cycle`,
and lets an implementation refuse `root` and `types` in a used file with
`bad-directive`; the loader uses those codes. What was asked:

- Built-in groups live in autoversion's `filters/NAME.filter`, not
  `filterrules/groups.json` (its "Built-in groups" paragraph).
- A group file may hold `use` lines (`default`, `media`). A tool reading
  the files directly resolves `use NAME` as `NAME.filter` beside it. The
  spec says `use` can't name a file and code-defined Rules use nothing:
  it needs a line for built-in groups that use others, or those tools
  run `autoversion filter dump --use NAME`.
- New groups `music`, `photos`, `videos`, `media` and `nas`. Their rules
  and `nas`'s `*@Syno…` rules are typed (`f:`), so restic and borg need
  `types decay`, and a folder named `x.mp3` is then left out there too.
- `filters/README.md` lists every rule, generated.

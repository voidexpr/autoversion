# Feature: use a filter the other way, exclude or include

**Status: proposal, not started (2026-11-03).** Needs ownit's agreement
for the group file part (A).

## Problem

The built-in groups only exclude. Using one the other way means copying
its rules:

- **Keep only media:** an allowlist made of `media`'s extensions, plus a
  few more (`*.pdf`, `*.md`, documents). Today: `exclude /*` and the
  extension list written again as includes (README says so for `media`).
- **Measure, then delete:** take some excludes (`nas`, `caches`, `default`),
  count the files they match and the space they use in an existing
  folder, then delete them, without rewriting the rules.

## Findings

- **Complement of an exclude-only group.** `exclude /*` plus the group's
  rules turned into includes keeps exactly what the group leaves out:
  - any level of a path matched by a group rule now has an include, and
    an include beats `exclude /*` at the same level;
  - with no match, `exclude /*` decides at the first level.
  Every built-in group only excludes, so this holds for all of them.
- **Includes alone change nothing.** An include-only group needs its
  `exclude /*`, carried by the group or written by each filter. Two such
  groups share it: duplicate rules are removed.
- **Flipping a filter that has includes is not its opposite.** Entries no
  rule matches are kept both ways, `exclude /*` becomes `include /*`, and
  at a level where both kinds match the include wins both ways. The
  opposite of a whole filter is "what it leaves out" (D), not a flip.
- **Markers can't flip:** `include marker:NAME` is invalid (`bad-marker`).
  No group has a marker today.
- **An allowlist ignores the folder excludes above its files.** Checked
  with `use default`, `exclude /*` and `include f:/**/{*.jpg,*.png}`:
  `node_modules/pkg/logo.png` is kept. The file's own level is the closest
  match, and its include wins over default's `node_modules` exclude. Only
  a literal exclude (`exclude f:/node_modules/pkg/logo.png`) takes it out;
  `f:/**/node_modules/**/*.png` is refused (`**` once per rule). So "only
  media" keeps icons in `node_modules` and thumbnails in `.cache`.
- **Shared spec:** ownit's `plans/spec_filter_syntax.md` says group files
  hold exclude rules only, and ownit copies `os`, `nas`, `caches` and
  `deps` verbatim (`make groups-sync`). Allowing includes in a group file
  is ownit's decision.
- **ownit's restic and borg converters refuse wildcard includes**
  (`include-wildcard`). An include group works with the native matcher
  (autoversion, dvgo), not with those converters.

## Designs

| | How | Spec change | Lists written twice |
|---|---|---|---|
| A. Generated `only-*` files | `make filter` writes `only-NAME.filter` from `NAME.filter`: `exclude /*` and the same rules as includes | Group files may hold includes | No: generated, a test fails while one is stale |
| B. `only NAME` directive | `only media` in a rules file expands to the same rules, `filter dump` shows them | A new directive, in ownit's Python too | No |
| C. Hand-written `only-media.filter`, replacing `media.filter` | Copy the lists as includes | Group files may hold includes | Yes, and the exclude version is gone |
| D. `filter scan --left-out` | List what the rules leave out | None, autoversion only | Not applicable |

C loses "back up everything but media", the reason `media` was added. No
job or ownit filter uses `media` today.

## Proposal

- **Measure and delete: D.** `filter scan --use nas --left-out` lists what
  `nas` matches. It works with any rules: a group, `default`, a job's
  `.autoversion.filter`.
  - Print the top-most left-out entry: a folder once, `node_modules/`,
    not its files. The summary gives the count and total size of the
    files under them, as today for kept files.
  - `-0` for NUL-separated output, to pipe to `xargs -0 rm -rf`.
    autoversion itself deletes nothing.
  - It walks every folder: nothing can be skipped.
- **Keep only media: A**, once ownit agrees, for the media groups only:
  `only-music`, `only-photos`, `only-videos`, and `only-media` using the
  three. An allowlist of `build` or `logs` has little use. Extra rules
  combine: `use only-media` and `include f:/**/{*.pdf,*.md}`.

## For ownit

- May group files hold includes, for generated `only-*` allowlists
  (`exclude /*` plus includes)?
- 2e4aba1 changed `caches` and `deps`, which ownit's `common` uses:
  `groups-sync` fails until the copies are refreshed. New in them:
  `.mypy_cache`, `.ruff_cache` (already in `common`), `.tox`, `.nox`,
  `.parcel-cache`, `.turbo`, `.svelte-kit`, `.angular`, `.zig-cache`,
  `__pypackages__`, `.direnv`, `go/pkg/mod`.

## Order

1. D: `--left-out` on `filter scan`, README, tests.
2. The prompt for ownit.
3. After ownit's reply: A, with `filters/README.md` regenerated and the
   group tests extended.

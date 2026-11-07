# Spec draft: scoped max-file-size

Status: draft revised by autoversion on 2026-11-06 from ownit's proposal,
implemented here to find issues before the spec changes.

## Syntax

```
max-file-size SIZE [RULE]
```

- Without RULE, the default limit, as today: at most once, `none` lifts
  it. No default: no limit.
- With RULE, the limit for what RULE covers: the entry itself, or
  everything under a folder it matches, like any folder rule. Several
  scoped lines are allowed. RULE is a path rule (`/x`, `/**/.git`, `f:`
  and `d:` allowed, `{a,b}` sugar gives one scoped line per name);
  `marker:` is `bad-directive`.
- Directives come before the rules, as today; a scoped line after a rule
  is `bad-directive`. Used Rules files may not carry it, as today.

## Deciding the limit

The limit of a kept non-folder entry is decided like the entry itself:
among the scoped lines whose RULE matches the entry or one of its
ancestors, the closest match wins, the depth being the level RULE matched
at. On one level, the tiers of rules apply: a literal RULE beats a
wildcard, a named wildcard (`/**/.git`) beats one ending in a wildcard
(`f:/**/*.pack`). Lines still tied give the strictest limit, `none` being
the loosest. No line order, as for rules. No scoped line matches: the
default. The limit applies after the rules decided the entry is kept, as
today; folders are never limited.

Two literal scoped lines on the same path with different sizes are a
`contradiction`, refused at validation. The same size twice is fine.

## Tools

Tools with one global limit (restic `--exclude-larger-than`, rsync
`--max-size`) receive the loosest of all the limits, `none` meaning no
option, with a warning `max-file-size-scoped`: "restic keeps files up to
SIZE everywhere: the scoped limits can't be expressed". Loosest rather
than the default, so a tool never drops what the filter keeps; the
warning names what it costs, since a single `none` line means the tool
gets no limit at all and keeps what the default leaves out. borg,
gitignore and Syncthing keep their existing warning.

Walkers: nothing changes, a folder's size never matters.

## Messages

An entry left out by a scoped limit, or kept by a scoped `none`, is
explained by that line: `left out: 913 MB, larger than the limit 10M
(max-file-size 10M)` and `kept: 913 MB, within max-file-size none /**/.git`.

## The case at hand

```
version 1
max-file-size 10M
max-file-size none /**/.git
use junk
```

A 1 GB DuckDB file under a project is left out, a 913 MB pack under
`.git` is kept.

## Conformance cases (native and autoversion)

1. default `10M`, `none /**/.git`: `a/big.db` (20M) out,
   `a/.git/objects/p.pack` (20M) in.
2. default `10M`, `1G /data`, `100M /data/raw`: `data/x` (500M) in,
   `data/raw/y` (500M) out, `data/raw/z` (50M) in: the closer line wins.
3. no default, `1M /small`: `big.iso` (2G) in, `small/f` (2M) out: a
   scoped line alone.
4. default `10M`, `1G f:/**/*.pack`, `none /**/.git`: `a/.git/x.pack`
   (2G) out and `b/w.pack` (500M) in, since the entry's own match is the
   closest; `a/.git/y.bin` (2G) in by the folder's `none`; `b/z.pack` (2G)
   out. Easy to get wrong: a `none` on `.git` doesn't lift a limit set on
   the file itself.
6. ties, unit-tested here: `1M /**/*.bin` and `2M f:/**/*.bin` give 1M
   (strictest of a tier); `none /**/*.bin` and `2M /**/*.bin` give 2M
   (`none` is the loosest); `1G /**/data` and `none /**/data` give 1G.
5. `max-file-size 10M marker:CACHEDIR.TAG` is `bad-directive`; a second
   bare line is `bad-directive`, as today; `max-file-size 1M /x` and
   `max-file-size 2M /x` is `contradiction`.

Conformance file: `max_file_size` stays a string for a default alone, or
becomes a list of lines as written (`["10M", "none /**/.git"]`).

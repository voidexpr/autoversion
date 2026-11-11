# Spec proposal: a named wildcard beats one ending in a wildcard

Status: accepted by targy on 2026-11-06, in the spec
(`plans/spec_filter_syntax.md`, "Deciding an entry", "All the cases",
"Known limits") and in its Python matcher at 21c3c68; autoversion's matcher
at 86d7f9a. Both pass the two conformance cases below.

## The problem

An allowlist of the dot entries of a home folder:

```
version 1
use junk
exclude /*
include /.*
```

keeps `.cache`, `.Trash`, `.dropbox` and everything below them. `include
/.*` and the group rule `exclude /**/.cache` both match `.cache` at the
root, both are wildcards, and the spec resolves two wildcards to the
include. Rules that match deeper (`/**/.local/state`, a `CACHEDIR.TAG`
marker in `.cache/uv`) still win, so the result looks arbitrary: 1.0 M
files kept with `use junk`, 110 k with the markers, 6 k once `.cache` and
`.Trash` are named literally. The literal list is what every home filter
ends up carrying by hand.

## The change

In "Deciding an entry", the tie paragraph becomes:

```
On the same level, when an include and an exclude both match:
- a literal rule (no `*`, no `**`) beats a wildcard rule;
- a wildcard rule whose last name is plain (`/**/.cache`,
  `/*/node_modules`: it names the entries it matches) beats one whose
  last name has a wildcard (`/.*`, `/a/*`, `f:/**/*.log`);
- otherwise the include wins;
- two literal rules name the same path: that is a contradiction, refused
  at validation (`contradiction`).

Nothing finer: `/a/*/x` doesn't beat `/**/x`, and `/a/*` doesn't beat
`/**/*.log`.
```

A folder holding a marker entry counts as a named wildcard exclude: a
literal or a named include on the folder (`/**/env`) ties with it and wins,
an include ending in a wildcard (`/venvs/*`) loses. Marker rules name a
folder by its content, as precisely as `/**/name` does by its name.

Known limit (in the spec): the tier goes by shape, not intent. Suffix junk
(`f:/**/*.log`, `*.swp`) ties with `/.*` and the include wins, so such
junk at the root survives the dotfiles allowlist. An author of a wildcard
include has to know which junk rules are named. Not fixed: ranking by
characters would be the specificity ranking the spec refuses.

The spec's current example, "`/a/*` doesn't beat `/**/b`", flips: `/**/b`
wins. The guarantees hold: no order, one answer, local, predictable growth
(a rule added still only removes or only adds).

## Consequences

- The dotfiles allowlist works as written: `use junk` leaves out `.cache`,
  `.Trash` and the other root-level junk.
- `exclude /**/.git` against `include /projects/*` on the same level now
  excludes, the direction people expect from a group.
- Converters: nothing to change. They only take literal includes, which
  beat every wildcard already, so the two wildcard tiers never meet in an
  ordered list (spec, "Why this gives the closest-match result").
- autoversion's implementation already indexed wildcard rules by their
  last name; the change is to rank that index first (`decideLevel`).

## Conformance cases added

```
tie: a named wildcard exclude beats an include ending in a wildcard; two unnamed: the include wins
  tree     .cache/ .cache/x .vimrc notes logs/ logs/app.log logs/old.log
  exclude  /* /**/.cache /**/*.log
  include  /.* /logs/*
  kept     .vimrc logs/ logs/app.log logs/old.log

markers: a marked folder beats an include ending in a wildcard, a named include on it wins
  tree     venvs/ venvs/a/ venvs/a/pyvenv.cfg venvs/a/bin/ venvs/a/bin/python venvs/b/ venvs/b/x
           tools/ tools/env/ tools/env/pyvenv.cfg tools/env/bin/ tools/env/bin/run
  exclude  /* marker:pyvenv.cfg
  include  /venvs/* /**/env
  kept     venvs/ venvs/a/ venvs/a/pyvenv.cfg venvs/b/ venvs/b/x
           tools/ tools/env/ tools/env/pyvenv.cfg tools/env/bin/ tools/env/bin/run
```

Both run on `native` and `autoversion` only, like the existing literal tie
case. The one unit test that encoded the old example (`/a/*` versus
`/**/x`) now expects the exclude.

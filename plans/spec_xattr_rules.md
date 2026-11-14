# Spec draft: attribute rules, `exclude xattr:NAME`

Status: drafted and prototyped by autoversion on 2026-11-13, to propose to
targy (`plans/spec_filter_syntax.md`, next to marker rules).

## Why

macOS apps and users mark what Time Machine must not back up with an
extended attribute on the entry itself:

```
xattr -p com.apple.metadata:com_apple_backup_excludeItem PATH   # com.apple.backupd
tmutil addexclusion PATH                                        # sets it
```

Xcode, Docker, Parallels and Homebrew mark their caches and disks that
way. No backup tool honors it: not restic, borg, rsync or Syncthing. The
native matchers can (autoversion, dvgo, targy).

## Syntax

```
exclude xattr:NAME
```

NAME is an extended attribute name, as `xattr -l` prints it, one per
rule; `{a,b}` gives one rule per name. Only an exclude, like a marker
(`bad-xattr` otherwise, or for an empty NAME).

## Deciding an entry

An entry carrying the attribute counts as a named wildcard exclude at its
own level: a folder with the attribute takes everything below it, a file
takes itself. Presence is what counts, never the value. Like a marker, a
literal or a named include on the entry wins, an include ending in a
wildcard loses, and an include below a marked folder matches closer and
wins (the known limit of includes under excludes). The root is never
decided by its attribute.

Walkers skip a folder left out by its attribute when no include can
match below it, as for any excluded folder.

## Tools

No tool can check an attribute. Converters emit nothing for the rule and
warn `xattr-unsupported`: "restic can't check xattr:NAME: the entries it
marks are kept". Same shape as `marker-unsupported` for rsync.

Add to the spec, for both kinds:

> Marker and attribute rules are optimizations. A tool that can't check
> them keeps the entry, and the converter warns. They never make a tool
> drop what the filter keeps.

## Group

`time-machine`, opt-in, one line:

```
exclude xattr:com.apple.metadata:com_apple_backup_excludeItem
```

Not in `default` or `junk`: people mark big media as "not for Time
Machine" and still want it in another backup. The filter's author knows.

## Out of scope

`tmutil addexclusion -p` stores a path exclusion in a system plist, not
on the entry; a rule can't see it, and `tmutil isexcluded` per path is
too slow for a walk.

## Conformance cases (native and autoversion)

The case format gets `"xattrs": {"entry": ["name", ...]}`.

1. `exclude xattr:com.apple.metadata:com_apple_backup_excludeItem`;
   tree `build/`, `build/x`, `big.iso`, `keep.txt`, `src/`, `src/a.c`;
   xattrs on `build/` and `big.iso`: kept `keep.txt`, `src/`, `src/a.c`.
2. same rule with `include /tools/env` and `include /**/cache/keep`;
   tree `tools/`, `tools/env/` (marked), `tools/env/bin`, `data/`,
   `data/cache/` (marked), `data/cache/keep`, `data/cache/x`: kept
   `tools/`, `tools/env/`, `tools/env/bin`, `data/`, `data/cache/`,
   `data/cache/keep`: the literal include on the folder wins, and the
   include below the marked folder matches closer.
3. invalid: `xattr:` is `bad-xattr`, `include xattr:a` is `bad-xattr`.

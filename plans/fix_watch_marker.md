# Bug: watch ignores a marker appearing or disappearing

**Status: open, delayed.** Found by the code review of 1c40088 (2026-11-02).

## Symptom

`exclude marker:NAME` leaves out a folder that directly holds NAME. The full
scan applies it correctly. `watch` does not: when the marker entry appears or
disappears, only the marker's own event is processed. The other entries of its
folder keep their old state until the next full scan (restart or resync).

- Marker appears: `cache/data.bin` was copied, then a tool writes
  `cache/CACHEDIR.TAG`. The tag is copied, `data.bin` stays in the destination
  and in git.
- Marker disappears: `pyvenv.cfg` is deleted from `.venv`. The folder's files
  are now kept but not copied until each one changes.
- Linux only: `addRecursive` skips the subfolders of a marked folder
  (`skipDir` is true for them). When the marker goes, they stay unwatched until
  watch restarts, so their changes produce no event at all.

Tools usually write the marker around the time they create the folder, so the
first case is the common one: files written before the marker get copied.

## Repro

```
mkdir -p /tmp/av/src/cache /tmp/av/dst
printf 'version 1\nexclude marker:CACHEDIR.TAG\n' |
  ./autoversion init --mirror --source /tmp/av/src --destination /tmp/av/dst --filter -
./autoversion watch --buffer-time 1 --source /tmp/av/src --destination /tmp/av/dst &
echo data > /tmp/av/src/cache/data.bin   # copied
sleep 3
touch /tmp/av/src/cache/CACHEDIR.TAG     # tag copied, dst/cache/data.bin stays
```

## Cause

- `main.go` watch loop: an event is decided for its own path only
  (`ShouldTrack`, or `skipDir` for `EventDirChanged`). Nothing knows that a
  marker entry changes the decision of its siblings.
- `watcher_other.go` `addRecursive`: watches are added once, from the rules at
  that time. Nothing re-adds them when a folder stops being marked.

## Fix proposal

- In the watch loop, an event whose base name is a marker name
  (`Rules.Markers()`) also queues `EventDirChanged` for its parent folder.
  `expandDirChanges` already does the rest: it walks the source folder
  (created events for what is now kept) and the destination folder (deleted
  events for what is no longer kept). The marker entry itself is covered by
  that walk.
- Linux watcher: on an event for a marker name, call `addRecursive` on the
  parent folder. It skips folders already watched, so it only adds what the
  marker's removal uncovered. Optionally `forgetDir` the subfolders that a new
  marker now skips, to give watches back.
- macOS needs nothing more: FSEvents watches the whole tree.

## Tests

- Watch tests in temp folders, on both platforms: marker appears after a file
  was copied (file deleted from the destination), marker removed (folder files
  copied without touching them), Linux: a file created under a formerly marked
  subfolder is copied.
- `make test`, `make docker-go-test` (FSEvents doesn't start in the sandbox:
  the macOS run is yours, `make watch-check`).

## Related, from the same review, not in scope

- `holdsMarker` uses `os.Lstat`, case-insensitive on default APFS: a
  `cachedir.tag` counts as `CACHEDIR.TAG`, and the folder is then left out
  entirely (the entry isn't the marker name, so it isn't kept). An exact match
  needs a folder listing.
- Outside a walk the marker cache is off: each `Decide` does one `Lstat` per
  marker rule per ancestor folder.
- A folder holding two different marker entries: each leaves the other out,
  so the folder isn't stored bare. Matches restic; the spec wording is unclear
  (targy).
- A marker entry that is a symlink counts as a marker, but symlinks are never
  copied: the folder vanishes from the destination.
- The Go conformance test runs only `native` cases. `autoversion` cases are
  checked only by targy's pytest against a built binary.

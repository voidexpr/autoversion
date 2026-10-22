# Bug: deletions of already-copied files are ignored (macOS)

## Symptom

Deleting a file that autoversion has already copied to the target is not
detected while watching: no `event BUFFER deleted` line, no `git rm`. The file
stays in the target until the next restart, whose initial scan removes it.
Files created and deleted before autoversion copied them (e.g. temp files) are
detected fine, which hid the problem.

## Repro

```
mkdir -p /tmp/av/src /tmp/av/base
./autoversion watch --debug --no-git --buffer-time 1 --base /tmp/av/base /tmp/av/src &
echo hi > /tmp/av/src/file.txt   # detected, copied
sleep 3
rm /tmp/av/src/file.txt          # ignored
```

Debug output (macOS 26, same with binaries built against SDK 26.5 and 27.0):

```
flags=0x19100   mapped type: EventCreated     # create
flags=0x419100  mapped type: EventCanIgnore   # after autoversion's copy
flags=0x419300  mapped type: EventCanIgnore   # the rm
```

`0x419300` = ItemCloned (0x400000) | ItemIsFile | ItemXattrMod | ItemModified
| ItemRemoved (0x200) | ItemCreated.

## Cause

- `CopyFile` copies with `cp -p -c` (APFS clone). That sets the ItemCloned flag
  on the source file.
- FSEvents flags are cumulative for a path, so every later event for that file
  also carries ItemCloned (and the earlier Created/Modified flags).
- `mapFSEventFlagsWithDetails` in `watcher_darwin.go` returns `EventCanIgnore`
  for any event with ItemCloned *before* checking ItemRemoved, so the delete is
  dropped.

The clone check dates from 4e65d5f (2025-10-15).

## Likely also affected

In-place modifications (append, `truncate`, editors that don't save via
rename) of a file after its first copy carry ItemCloned too and are probably
ignored as well. Saves that replace the file (new inode) are not affected.
To confirm with the same repro using `echo more >> file.txt`.

## Proposed fix

- Don't ignore ItemCloned events. Since flags are cumulative they can't tell
  what just happened, so decide from the file system, like `EventRenamed`
  already does: file missing → deleted, file present → created/modified.
- Autoversion's own clone copies then produce an event for the source; the
  copy step skips files that are unchanged (`FilesEqual`), so this must not
  cause a copy/commit loop. Verify.
- Tests: create → delete, create → append, and no extra commit caused by
  autoversion's own copy.

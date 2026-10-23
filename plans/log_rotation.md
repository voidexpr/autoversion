# Plan: log rotation and size limits

**Status: done** in a530c9c. Existing installs got the new plist when they
were reinstalled by the source/destination migration (2026-10-23), and the
oversized logs are gone. One follow-up, see the end.

## Problem

launchd services write to `~/Library/Logs/autoversion/<job>/stdout.log` and
`stderr.log` with no size limit. On 2026-10-21 the
`SyncDatabox_nicmac_projects_to_Data_Backup` job's stdout.log was 18 GB and
the `..._to_Dropbox_Backup` one 1.4 GB.

In a 5 MB sample of the 18 GB file, most lines were noise:

| lines | kind |
|------:|------|
| 21078 | `event skip ...` (logSkip, every filtered event) |
|  3540 | `skipping large file ...` (repeated for the same files) |
|   744 | `event BUFFER ...` |
|   719 | `git command ...` |

## Why external rotation doesn't work

launchd opens StandardOutPath/StandardErrorPath once when it spawns the job
and keeps the fd. newsyslog (or any rename-based rotation) moves the file,
and the long-running watcher keeps writing into the renamed file until the
job restarts. newsyslog has no copy-truncate mode.

## Proposal

1. **Log less**
   - `event skip` only with `--debug`.
   - `skipping large file` once per path per run.
   - Keep `event BUFFER`, copies, removes and git commits at the default level.
   - No ANSI color codes when the output isn't a terminal.
2. **Autoversion manages its own log files**
   - `watch --log-dir <dir>` (set by `install` to the job's log dir) opens
     `stdout.log` / `stderr.log` itself and `dup2`s them onto fd 1 and 2, so
     no call site changes and Go runtime panics land in `stderr.log` too.
   - Every minute: rotate a file at 10 MB (keep 5: `stdout.log.1` … `.5`,
     rename-based, no lines lost), reopen it if it was removed or replaced.
   - Logging errors are reported (once per distinct error) and never stop
     the watcher: output keeps going to the previous file.
   - The plist's StandardOutPath/StandardErrorPath stay the same files, so
     early startup errors and launcher errors still land there, and
     `status` / `install-check` keep reading the same paths.
   - `install` clears `stderr.log` and its rotated copies on reload.
   - `dup2` from `golang.org/x/sys/unix` (already an indirect dependency)
     since `syscall.Dup2` doesn't exist on linux/arm64.
3. **Existing installs**: `install` regenerates the plist with the new flag.
   Remove the existing oversized logs by hand, otherwise the first rotation
   keeps them as `stdout.log.1`.

Rejected: lumberjack / an `io.Writer` (needs a pipe or changing every call
site, misses panics), truncating launchd's file in place (relies on
O_APPEND, loses lines during the copy), newsyslog + signal (root config and
a pid file), unified logging (cgo, info level not persisted), rotating in
the launcher (rebuilding it needs a new Full Disk Access grant).

## Tests

- Rotates at the limit and keeps N files; no rotation below the limit.
- Reopens a log file removed by hand.
- `install` helper removes `stderr.log` and its rotations only.
- `event skip` not logged without `--debug`.
- A large file is reported once, not on every event.
- No color codes when writing to a file.

## Follow-up: vanished files logged as errors (open)

Found 2026-10-23, a few hours after the migration: the `SyncDatabox_nicmac_projects`
jobs' `stderr.log` had 1321 and 381 lines, all
`failed to check tracking for .../lctl/target/debug/incremental/...`. Rust
incremental builds create and delete those files quickly. `ShouldTrack`
`Lstat`s a created/modified file before applying the exclusion patterns, so a
file already gone returns an error, which `watch` logs, even though
`target/debug/` is excluded.

Fix to discuss: apply `--exclude-dir` and the patterns before the `Lstat`, and
treat a file that no longer exists as not tracked without logging an error
(its delete event follows).

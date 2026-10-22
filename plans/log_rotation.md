# Plan: log rotation and size limits

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
   - `event skip` and `skipping large file` only with `--debug`, or once per
     path (large files: remember paths already reported).
   - Keep `event BUFFER`, copies, removes and git commits at the default level.
2. **Autoversion manages its own log files**
   - New flag, e.g. `--log-dir <dir>` (set by `install` to the current log
     dir): write `stdout.log` / `stderr.log` there through a size-capped
     writer (rotate at e.g. 10 MB, keep 5: `stdout.log.1` … `.5`).
   - Small in-house writer, or ask before adding a dependency such as
     lumberjack.
   - The plist's StandardOutPath/StandardErrorPath stay as a fallback for
     early startup errors (before the log files are opened), so they stay
     small.
   - `status` / `install-check` keep reading the same paths.
3. **Existing installs**: `install` regenerates the plist with the new flag.

## Tests

- Writer rotates at the limit and keeps N files.
- `event skip` not logged without `--debug`.
- A large file is reported once, not on every event.

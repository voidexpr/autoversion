# Plan: push destinations to a remote over ssh

**Status: proposal, not implemented.** Phase 0 (a push script, no change to
autoversion) can be set up now.

## Use cases

1. Back up work to another device (the NAS), close to real time. Dropbox is
   limited by the macOS sync engine, and it corrupts local workspaces that
   use git.
2. Work on a remote server while editing files locally: local edits are
   copied to the server automatically.

The machine is a laptop: lid closed, no network in a backpack, random Wi-Fi.
After a reboot, it's fine if pushes only work after a login or after an ssh
key is unlocked.

## Decision: keep the local destination, push it

The local destination stays as it is today. A remote is a copy of it,
updated by pushes.

- Local versioning doesn't depend on the network: a commit every few
  seconds, offline too.
- The local git repository is the queue: the unpushed commits are what the
  remote is missing. Nothing new to persist, and it survives reboots.
- Batching comes for free: one push sends all new commits as one compressed
  pack.
- The remote needs sshd and git only (rsync for use case 2).

How deep each option goes:

| option | depth |
|---|---|
| git push of the local destination (use case 1) | shallow: phase 0 is a script, phase 1 ~300 lines + tests |
| offline, sleep, retries | shallow to medium: the queue is git |
| auth after a reboot | shallow: left to ssh and `~/.ssh/config` |
| rsync of the local destination to a workspace (use case 2) | medium, ~250 lines, rsync ≥ 3.1 on both sides |
| push only on some networks | reachability or a hook: shallow. Hotspot / Low Data Mode: medium (cgo). Wi-Fi name: avoid |
| no local destination, write to the remote directly | deep: remote stat per file, a persistent queue, versions made offline are lost, probably a remote agent |
| two-way sync | very deep, not planned: use mutagen or unison |

## Phase 0: git remote and a launchd push script

No change to autoversion. Covers use case 1.

### Setup, once per destination

On the NAS, an append-only bare repository: a compromised laptop or a bug
can add commits, but can't rewrite or delete history.

```bash
git init --bare /volume1/autoversion/mac/projects.git
cd /volume1/autoversion/mac/projects.git
git config receive.denyNonFastForwards true
git config receive.denyDeletes true
```

The NAS runs `git gc --auto` after pushes (`receive.autogc`, on by default).
NAS snapshots of `/volume1/autoversion` add protection against anything
that has a shell on the NAS.

On the laptop:
- An ssh `Host nas` entry in `~/.ssh/config` with `HostName`, `User`,
  `IdentityFile` and `IdentitiesOnly yes`. See **Auth** for the key.
- `cd ~/Data/Backup/autoversion/projects`, then
  `git remote add nas ssh://nas/volume1/autoversion/mac/projects.git`.
- The first push by hand, in a terminal: `git push nas HEAD`. It accepts
  the host key, and can take a while for a long history.

### Script

Pushes every destination under `AV_ROOT` that has a remote named
`AV_REMOTE` and commits the remote doesn't have yet. Tested on 2026-10-26
against local bare repositories: first push, up to date (no push attempted),
unreachable (one retry after 30 s, logged once), rejected (logged once), back
online. Not tested: over ssh, and the notification (osascript is blocked in
the sandbox the test ran in).

```bash
#!/bin/bash
# Pushes the autoversion destinations under AV_ROOT that have a git remote
# named AV_REMOTE. Run by launchd every 5 minutes: a run with nothing new to
# push only reads local refs. Logs state changes only.
set -u

: "${AV_ROOT:=$HOME/Data/Backup/autoversion}"
: "${AV_REMOTE:=nas}"
: "${AV_STATE:=$HOME/Library/Logs/autoversion-push}"
: "${AV_STALE_DAYS:=2}"

export GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ConnectTimeout=10 \
 -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
 -o ControlMaster=auto -o ControlPersist=60 \
 -o ControlPath=$HOME/Library/Caches/autoversion-ssh-%C"
export GIT_TERMINAL_PROMPT=0

mkdir -p "$AV_STATE"
ts() { date '+%Y-%m-%d %H:%M:%S'; }

# notify_stale NAME: at most once a day, when NAME wasn't pushed for AV_STALE_DAYS
notify_stale() {
    local last
    last=$(cat "$AV_STATE/$1.pushed" 2>/dev/null || echo 0)
    [ $(( $(date +%s) - last )) -lt $(( AV_STALE_DAYS * 86400 )) ] && return
    [ -n "$(find "$AV_STATE/$1.notified" -mtime -1 2>/dev/null)" ] && return
    touch "$AV_STATE/$1.notified"
    osascript -e "display notification \"$1: not pushed to $AV_REMOTE for $AV_STALE_DAYS days\" with title \"autoversion\""
}

retried=0
for repo in "$AV_ROOT"/*/; do
    name=$(basename "$repo")
    cd "$repo" && git remote get-url "$AV_REMOTE" >/dev/null 2>&1 || continue
    branch=$(git symbolic-ref --short HEAD 2>/dev/null) || continue
    # A push moves refs/remotes/REMOTE/BRANCH, so this check needs no network
    [ "$(git rev-parse HEAD 2>/dev/null)" = "$(git rev-parse -q --verify "refs/remotes/$AV_REMOTE/$branch")" ] && continue

    out=$(git push -q "$AV_REMOTE" "$branch" 2>&1); status=$?
    if [ $status -eq 128 ] && [ $retried -eq 0 ]; then
        # 128: no connection or no access. Right after a wake the network may not be up yet
        retried=1
        sleep 30
        out=$(git push -q "$AV_REMOTE" "$branch" 2>&1); status=$?
    fi

    case $status in
    0)
        date +%s > "$AV_STATE/$name.pushed"
        rm -f "$AV_STATE/offline" "$AV_STATE/$name.failed"
        ;;
    128)
        # All destinations push to the same host: skip the others
        [ -e "$AV_STATE/offline" ] || echo "[$(ts)] $AV_REMOTE unreachable: $out"
        touch "$AV_STATE/offline"
        notify_stale "$name"
        break
        ;;
    *)
        # Rejected, e.g. not a fast-forward: needs a look
        [ -e "$AV_STATE/$name.failed" ] || echo "[$(ts)] $name: push to $AV_REMOTE failed: $out"
        touch "$AV_STATE/$name.failed"
        notify_stale "$name"
        ;;
    esac
done
```

`AV_STATE/<name>.pushed` holds the time of the last successful push.

### launchd job

`~/Library/LaunchAgents/autoversion.push.plist`, loaded with
`launchctl load ~/Library/LaunchAgents/autoversion.push.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>autoversion.push</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>/Users/nicolas/bin/autoversion-push.sh</string>
	</array>
	<key>StartCalendarInterval</key>
	<array>
		<dict><key>Minute</key><integer>0</integer></dict>
		<dict><key>Minute</key><integer>5</integer></dict>
		<!-- … one dict per 5 minutes … -->
		<dict><key>Minute</key><integer>55</integer></dict>
	</array>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>/Users/nicolas/Library/Logs/autoversion-push/push.log</string>
	<key>StandardErrorPath</key>
	<string>/Users/nicolas/Library/Logs/autoversion-push/push.log</string>
</dict>
</plist>
```

Full Disk Access shouldn't be needed: the job only reads the destinations,
and `~/Data` isn't a protected folder.

### Real time vs overhead

- **Checking is free, only pushing costs.** A run with nothing new compares
  two local refs per destination, no network. A run over 3 test
  destinations took 0.09 s, one of them pushing to a local bare repository.
  So check often.
- **The push interval barely changes the bytes sent.** Every commit is
  pushed either way, as deltas. Pushing every 5 minutes instead of once a
  day only adds ssh handshakes and ref negotiation: a few KB and about a
  second per push. Pushing once a day saves almost nothing and loses up to
  a day of work if the laptop is lost. Every 5 minutes means at most ~5
  minutes missing on the NAS, plus the time offline. Local history keeps
  every commit anyway.
- **`StartCalendarInterval`, not `StartInterval`.** From
  `man launchd.plist`: `StartInterval` misses firings while the system
  sleeps, and calendar intervals missed during sleep are run once at wake.
  That gives a push on wake for free. `WatchPaths` (start on a ref change)
  is described there as "highly discouraged … race-prone".
- **At wake the network may not be up yet:** one retry after 30 s. After
  that, a connection failure stops the run, as the other destinations use
  the same host.
- **Never hang.** launchd doesn't start a second instance while one runs,
  so a hung ssh would block all later pushes. `ConnectTimeout` and
  `ServerAlive*` handle dead connections after sleep, and `BatchMode` never
  prompts.
- **One ssh connection per run.** `ControlMaster` / `ControlPersist` make
  all destinations of a run share one connection.
- **No conflict with the watch job.** `git push` reads objects and refs.
  It doesn't use the index, so it never meets autoversion's
  `git add`/`commit` on `index.lock`.
- **Network policy for free:** if `nas` is only reachable on the home LAN,
  runs elsewhere fail fast (`ConnectTimeout`), and pushes resume at home.
  With Tailscale or a VPN, pushes work from anywhere.
- **Quiet logs:** only state changes are logged (unreachable, rejected), not
  every run.
- **Silent failure is the main risk** of a backup job. A notification at
  most once a day when a destination wasn't pushed for 2 days.

### Auth

The shell's `SSH_AUTH_SOCK` points to herdr's agent socket. launchd jobs
don't get the shell's environment, so the job only sees what
`~/.ssh/config` names. Options:

- **A dedicated key without a passphrase**, restricted on the NAS
  (recommended for this job). In the NAS `authorized_keys`:
  `command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict ssh-ed25519 …`
  (if git-shell is available on the NAS). With the append-only repository,
  a stolen key can only add commits.
- **A key with a passphrase stored in the keychain** (`UseKeychain yes`,
  `AddKeysToAgent yes`). Logging in unlocks it after a reboot.
- **An agent that needs unlocking** (1Password, Secretive, herdr), named by
  `IdentityAgent` in the `Host nas` entry. Pushes fail as "unreachable"
  until it's unlocked, then resume.

## Phase 1: `--remote` in autoversion (git)

What it adds over phase 0:
- a push within ~30 s of a commit instead of the next 5-minute slot;
- an immediate push on wake or network change;
- remote state in `status`;
- one place for the configuration;
- the remote created by `init`.

### UX

```bash
autoversion init --source ~/projects --destination ~/Data/Backup/autoversion/projects \
    --remote git+ssh://nas/volume1/autoversion/mac/projects.git
autoversion install --source ~/projects --destination ~/Data/Backup/autoversion/projects

autoversion push --source … --destination …   # push now, in the foreground
autoversion status                            # adds per remote: unpushed commits, last push, last error
```

- The URL scheme selects the transport (`git+ssh`, later `rsync+ssh`),
  behind a `Remote` interface. That's the plug point. No external plugin
  protocol.
- ssh settings stay in `~/.ssh/config`: the URL host can be a `Host` alias.
- `init --remote` is the only interactive step. It runs ssh without
  `BatchMode`, so the host key can be accepted and a passphrase typed. It
  checks for git on the remote, creates the append-only bare repository,
  adds the git remote `autoversion`, and does the first push.

### Configuration

In the marker, together with Part 2 of `feature_include_exclude_rules.md`:

```json
{
  "source": "/Users/nicolas/projects",
  "remotes": [
    { "name": "nas", "url": "git+ssh://nas/volume1/autoversion/mac/projects.git", "push_delay_s": 30 }
  ]
}
```

Without Part 2, `--remote` and `--push-delay` are persisted in the plist like
the other flags.

### Pushing

- Local commits keep the `--buffer-time` cadence.
- One pusher goroutine per remote. The watch loop calls `Kick()` after each
  commit and never waits for a push. A blocked loop could make FSEvents drop
  events and force a full rescan.
- When to push:
  - `push_delay` after the last commit (default 30 s for git);
  - at the latest 5 minutes after the oldest unpushed commit. Same rule as
    `maxEventAge` in `buffer.go`.
- **Remote errors never stop `watch`.** Today an error from `processEvents`
  exits (`main.go:456`), and with `ThrottleInterval=21600` launchd would
  leave the job dead for up to 6 hours.
- Errors, classified from the exit code and ssh's stderr:
  - unreachable: backoff 30 s → 10 min;
  - auth (`Permission denied`): retry every 10 min;
  - host key (`Host key verification failed`): no automatic retry,
    `init` fixes it;
  - rejected (not a fast-forward): no automatic retry.
  Each is logged once per state change.
- Wake: compare the wall clock with the monotonic clock between ticks. Go's
  monotonic clock on macOS should not advance during sleep (to verify). On
  wake: reset the backoff and push.
- Each push runs with `exec.CommandContext` and a timeout, plus the ssh
  options of phase 0.
- On SIGTERM: one last push with a short timeout.

### Network policy

1. Reachability, as in phase 0 (default).
2. `--remote-only-if 'CMD'`: push only when CMD exits 0. Any rule can be
   scripted.
3. Later: skip expensive or Low Data Mode networks (iPhone hotspot) with
   Network.framework's path monitor, through cgo like FSEvents. It uses the
   per-network setting macOS already has.

Not planned: a Wi-Fi name allowlist. Recent macOS needs Location Services
permission to read the SSID.

### status

- Unpushed commits: `git rev-list --count autoversion/<branch>..HEAD`, read
  from the destination. Works when the job isn't running.
- Last push, last error and state: `remote-state.json` in the job's log
  directory, written by the pusher.

### Code changes

- New `remote.go`:
  - URL → transport, the `Remote` interface and `gitRemote`;
  - the ssh options;
  - error classes;
  - the pusher (debounce, backoff, wake, `--remote-only-if`, state file).
- `main.go`: `Kick()` after commits in `processEvents` and `scanPaths`,
  the last push on shutdown, the `push` command.
- `git.go`: run git with an environment (`GIT_SSH_COMMAND`), push, count
  unpushed commits.
- `destination.go`: the `remotes` marker field and remote setup in `init`.
  `check-paths` only warns when a remote is unreachable (offline is
  normal).
- `launchd.go`: remote state in `status` and `install-check`.
- Tests, no network:
  - a fake `ssh` script that runs the remote command locally, against a
    bare repository in a temp directory;
  - a fake ssh that exits 255 to test the error classes and backoff;
  - optional: an sshd container next to the docker targets.

## Phase 2: rsync to a workspace (use case 2)

- `--remote rsync+ssh://devbox/home/nicolas/app`. Needs a git destination,
  which gives local history of the edits as a bonus.
- Files to send: `git diff --name-status -z <pushed>..HEAD` in the local
  destination. `<pushed>` is `refs/autoversion/pushed/<name>`, moved after
  each successful push. Without it (first push), all of `git ls-files` and
  no deletions.
- `rsync -a --files-from=- --from0 --delete-missing-args --backup --backup-dir=…`.
  The backup dir is outside the workspace and keeps the remote files that
  were replaced.
- Only files autoversion pushed are deleted on the remote, never a `--delete`
  of the tree. The `scanPaths` delete pass (`main.go:668`) pointed at a
  workspace would remove its `node_modules`, build outputs and `.git`.
- Local wins: remote edits of tracked files are overwritten (and kept in the
  backup dir). Nothing flows back.
- rsync exit 24 (source file vanished) counts as success: the next diff
  includes the deletion.
- `push_delay` defaults to 0, with a short `--buffer-time` (1 s) for that job.
- `/usr/bin/rsync` on macOS is openrsync ("2.6.9 compatible", checked on
  2026-10-26), without `--delete-missing-args`. Homebrew rsync (3.5.1
  installed) isn't on launchd's PATH: `install` resolves its absolute path.
- Remote marker: `.autoversion` naming `host:source` in the remote
  directory, checked before each push after a reconnect. To exclude from
  the remote project's git (`.git/info/exclude`).

## Not planned

- Writing to the remote directly, without a local destination.
- Two-way sync, or watching the remote.

## Open questions

1. Phase 0 may be enough for use case 1. Is phase 1 worth it for "push
   within 30 s" and `status`?
2. Is a local git destination always OK when a remote is set? It's the
   largest simplification here, and phase 2 depends on it.
3. Use case 2: does anything on the server change tracked files
   (formatters, codegen, `git checkout`)? Where is git run for that project?
   The source's `.git` isn't mirrored, so a clone on the server shows the
   edits as uncommitted changes.
4. Is the NAS reachable from anywhere (Tailscale, VPN) or only on the LAN?
   That decides whether a network policy is needed at all.
5. Configuration in the marker: wait for Part 2 of
   `feature_include_exclude_rules.md`?
6. Homebrew rsync as a dependency for phase 2?

#!/bin/bash
# Runs `autoversion watch` on a temp source and checks that file and directory
# changes reach the destination, and that autoversion's own copies don't
# trigger more copies.
# Usage: scripts/check_watch.sh [autoversion binary] [init/watch flags, e.g. --no-git]
set -u
av=${1:-./autoversion}
shift
repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/av_check_watch.XXXXXX")
src=$tmp/src
dst=$tmp/dst
outside=$tmp/outside
log=$tmp/watch.log
mkdir -p "$src" "$outside"
export GIT_AUTHOR_NAME=autoversion-check GIT_AUTHOR_EMAIL=check@localhost
export GIT_COMMITTER_NAME=autoversion-check GIT_COMMITTER_EMAIL=check@localhost

echo "== watch check $*"
"$av" init "$@" --source "$src" --destination "$dst" > /dev/null || exit 1
"$av" watch "$@" --buffer-time 1 --source "$src" --destination "$dst" > "$log" 2>&1 &
pid=$!
sleep 2

failures=0
# check DESCRIPTION COMMAND...
check() {
	local desc=$1
	shift
	if "$@"; then
		echo "ok    $desc"
	else
		echo "FAIL  $desc"
		failures=$((failures + 1))
	fi
}
# Buffer time (1s), flush tick and commit
settle() { sleep 4; }
# no_files DIR: DIR has no files left (--no-git keeps empty directories)
no_files() { [ -z "$(find "$1" -type f 2> /dev/null)" ]; }

echo hi > "$src/file.txt"; settle
check "created file copied" cmp -s "$src/file.txt" "$dst/file.txt"

echo more >> "$src/file.txt"; settle
check "appended file copied" cmp -s "$src/file.txt" "$dst/file.txt"

echo r > "$src/old-name.txt"; settle
mv "$src/old-name.txt" "$src/new-name.txt"; settle
check "file renamed: new name copied" cmp -s "$src/new-name.txt" "$dst/new-name.txt"
check "file renamed: old name removed" test ! -e "$dst/old-name.txt"

mkdir "$src/newdir"; echo new > "$src/newdir/new.txt"; settle
check "file in a new directory copied" cmp -s "$src/newdir/new.txt" "$dst/newdir/new.txt"

mkdir -p "$outside/moved/sub"; echo m > "$outside/moved/sub/m.txt"
mv "$outside/moved" "$src/"; settle
check "directory moved in: files copied" cmp -s "$src/moved/sub/m.txt" "$dst/moved/sub/m.txt"

mv "$src/moved" "$src/renamed"; settle
check "directory renamed: files copied" cmp -s "$src/renamed/sub/m.txt" "$dst/renamed/sub/m.txt"
check "directory renamed: old files removed" no_files "$dst/moved"

mv "$src/renamed" "$outside/"; settle
check "directory moved out: files removed" no_files "$dst/renamed"

rm -rf "$src/newdir"; settle
check "directory deleted: files removed" no_files "$dst/newdir"
mkdir "$src/newdir"; sleep 2; echo again > "$src/newdir/again.txt"; settle
check "directory recreated: new file copied" cmp -s "$src/newdir/again.txt" "$dst/newdir/again.txt"

rm "$src/file.txt"; settle
check "deleted file removed" test ! -e "$dst/file.txt"

copies=$(grep -c "copy '" "$log")
check "one copy per change ($copies copies, want 8)" test "$copies" -eq 8

"$repo/claude_kill" -INT "$pid" > /dev/null
wait "$pid" 2> /dev/null

if [ "$failures" -gt 0 ]; then
	echo "$failures check(s) failed, watch log and files kept in $tmp"
	exit 1
fi
echo "All checks passed"
rm -rf "$tmp"

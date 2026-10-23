#!/bin/bash
# Runs `autoversion watch` on a temp source and checks that file changes reach
# the destination: create, append, a file in a new directory, delete, and that
# autoversion's own copies don't trigger more copies.
# Usage: scripts/check_watch.sh [autoversion binary]   (default: ./autoversion)
set -u
av=${1:-./autoversion}
repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/av_check_watch.XXXXXX")
src=$tmp/src
dst=$tmp/dst
log=$tmp/watch.log
mkdir -p "$src"
export GIT_AUTHOR_NAME=autoversion-check GIT_AUTHOR_EMAIL=check@localhost
export GIT_COMMITTER_NAME=autoversion-check GIT_COMMITTER_EMAIL=check@localhost

"$av" init --source "$src" --destination "$dst" > /dev/null || exit 1
"$av" watch --buffer-time 1 --source "$src" --destination "$dst" > "$log" 2>&1 &
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

echo hi > "$src/file.txt"; settle
check "created file copied" cmp -s "$src/file.txt" "$dst/file.txt"

echo more >> "$src/file.txt"; settle
check "appended file copied" cmp -s "$src/file.txt" "$dst/file.txt"

mkdir "$src/newdir"; echo new > "$src/newdir/new.txt"; settle
check "file in a new directory copied" cmp -s "$src/newdir/new.txt" "$dst/newdir/new.txt"

rm "$src/file.txt"; settle
check "deleted file removed" test ! -e "$dst/file.txt"

copies=$(grep -c "copy '" "$log")
check "one copy per change ($copies copies, want 3)" test "$copies" -eq 3

"$repo/claude_kill" -INT "$pid" > /dev/null
wait "$pid" 2> /dev/null

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures check(s) failed, watch log and files kept in $tmp"
	exit 1
fi
echo "All checks passed"
rm -rf "$tmp"

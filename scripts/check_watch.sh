#!/bin/bash
# Runs `autoversion watch` on a temp source and checks that file and directory
# changes reach the destination, and that autoversion's own copies don't
# trigger more copies. With --allowlist, the destination's rules keep a few
# folders only, and the checks are that only their files reach it.
# Usage: scripts/check_watch.sh [autoversion binary] [--allowlist] [init flags, e.g. --no-git]
set -u
av=${1:-./autoversion}
shift
mode=default
if [ "${1:-}" = --allowlist ]; then
	mode=allowlist
	shift
fi
repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/av_check_watch.XXXXXX")
src=$tmp/src
dst=$tmp/dst
outside=$tmp/outside
log=$tmp/watch.log
mkdir -p "$src" "$outside"
export GIT_AUTHOR_NAME=autoversion-check GIT_AUTHOR_EMAIL=check@localhost
export GIT_COMMITTER_NAME=autoversion-check GIT_COMMITTER_EMAIL=check@localhost

echo "== watch check $mode $*"
if [ "$mode" = allowlist ]; then
	cat > "$tmp/rules.filter" << 'EOF'
version 1
use default

exclude /*
/keep
/notes/!private
/projects/*/memory
EOF
	"$av" init --filter "$tmp/rules.filter" "$@" --source "$src" --destination "$dst" > /dev/null || exit 1
else
	"$av" init "$@" --source "$src" --destination "$dst" > /dev/null || exit 1
fi
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
# no_files DIR: DIR has no files left (--no-git keeps empty directories)
no_files() { [ -z "$(find "$1" -type f 2> /dev/null)" ]; }
# dst_files: the destination's files, without .git and autoversion's own
dst_files() {
	(cd "$dst" && find . -type f ! -path './.git/*' ! -name .autoversion ! -name .autoversion.filter | sed 's|^\./||' | sort | tr '\n' ' ')
}

default_checks() {
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
}

allowlist_checks() {
	mkdir "$src/keep"; echo k > "$src/keep/a.txt"; settle
	check "kept folder created: file copied" cmp -s "$src/keep/a.txt" "$dst/keep/a.txt"

	echo t > "$src/top.txt"
	mkdir "$src/other"; echo o > "$src/other/b.txt"; settle
	check "files outside the kept folders not copied" test ! -e "$dst/top.txt" -a ! -e "$dst/other"

	mkdir -p "$src/notes/private"; echo n > "$src/notes/n.md"; echo p > "$src/notes/private/p.md"; settle
	check "exception: kept file copied" cmp -s "$src/notes/n.md" "$dst/notes/n.md"
	check "exception: excepted folder not copied" test ! -e "$dst/notes/private"

	mkdir -p "$src/projects/x/memory"; echo m > "$src/projects/x/memory/m.md"; echo j > "$src/projects/x/s.jsonl"; settle
	check "wildcard include: new folders, file copied" cmp -s "$src/projects/x/memory/m.md" "$dst/projects/x/memory/m.md"
	check "wildcard include: file beside it not copied" test ! -e "$dst/projects/x/s.jsonl"

	mkdir "$src/keep/node_modules"; echo x > "$src/keep/node_modules/x.js"; settle
	check "default groups still apply in a kept folder" test ! -e "$dst/keep/node_modules"

	mkdir -p "$outside/moved" "$outside/moved2"; echo m2 > "$outside/moved/m2.txt"; echo z > "$outside/moved2/z.txt"
	mv "$outside/moved" "$src/keep/"; mv "$outside/moved2" "$src/other/"; settle
	check "directory moved into a kept folder: files copied" cmp -s "$src/keep/moved/m2.txt" "$dst/keep/moved/m2.txt"
	check "directory moved into a left-out folder: not copied" test ! -e "$dst/other"

	rm "$src/keep/a.txt"; settle
	check "deleted file removed" test ! -e "$dst/keep/a.txt"

	files=$(dst_files)
	check "only kept files in the destination ($files)" test "$files" = "keep/moved/m2.txt notes/n.md projects/x/memory/m.md "

	copies=$(grep -c "copy '" "$log")
	check "one copy per change ($copies copies, want 4)" test "$copies" -eq 4
}

"${mode}_checks"

"$repo/claude_kill" -INT "$pid" > /dev/null
wait "$pid" 2> /dev/null

if [ "$failures" -gt 0 ]; then
	echo "$failures check(s) failed, watch log and files kept in $tmp"
	exit 1
fi
echo "All checks passed"
rm -rf "$tmp"

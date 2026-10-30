#!/usr/bin/env bash
set -euo pipefail
binary_dir="$(swift build --show-bin-path)"
executable="$binary_dir/lazyxcode"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
cd "$work_dir"
"$executable" --help | grep -q 'Usage: lazyxcode'
"$executable" --version | grep -q '^lazyxcode '
"$executable" --snapshot | grep -q 'Example.xcworkspace'
"$executable" --help | grep -q 'lazyxcode cache'
export XDG_CACHE_HOME="$work_dir/cache" XDG_STATE_HOME="$work_dir/state"
"$executable" cache status | grep -q 'DerivedData in'
"$executable" cache prune | grep -q 'Nothing to remove'
"$executable" cache clear --all | grep -q 'No DerivedData to clear'
set +e
"$executable" --invalid > invalid.txt 2>&1
invalid_status=$?
"$executable" > empty.txt 2>&1
empty_status=$?
"$executable" cache clear > cache-clear.txt 2>&1
cache_clear_status=$?
"$executable" cache bogus > cache-bogus.txt 2>&1
cache_bogus_status=$?
set -e
test "$invalid_status" = 2
test "$empty_status" = 1
test "$cache_clear_status" = 1
test "$cache_bogus_status" = 2
grep -q 'clear --all' cache-clear.txt
grep -q 'unknown argument' invalid.txt
grep -q 'no .xcworkspace or .xcodeproj' empty.txt

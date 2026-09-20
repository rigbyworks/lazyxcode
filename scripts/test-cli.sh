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
set +e
"$executable" --invalid > invalid.txt 2>&1
invalid_status=$?
"$executable" > empty.txt 2>&1
empty_status=$?
set -e
test "$invalid_status" = 2
test "$empty_status" = 1
grep -q 'unknown argument' invalid.txt
grep -q 'no .xcworkspace or .xcodeproj' empty.txt

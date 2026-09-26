#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version_source=Sources/LazyXcode/BuildVersion.swift
backup="$(mktemp)"
cp "$version_source" "$backup"
trap 'cp "$backup" "$version_source"; rm -f "$backup"' EXIT
python3 - <<'PY'
import os
import pathlib
import re
version = os.environ.get("VERSION", "dev")
if not re.fullmatch(r"[A-Za-z0-9.+-]+", version):
    raise SystemExit("VERSION must contain only letters, numbers, dots, plus signs, and hyphens")
pathlib.Path("Sources/LazyXcode/BuildVersion.swift").write_text(
    'enum BuildVersion {\n    static let value = "' + version + '"\n}\n'
)
PY
swift build --disable-sandbox -c release --product lazyxcode
binary_dir="$(swift build --disable-sandbox -c release --show-bin-path)"
install -m 755 "$binary_dir/lazyxcode" lazyxcode

#!/usr/bin/env bash
# Run on a disposable CI host with current Homebrew build prerequisites.
set -euo pipefail
export HOMEBREW_NO_AUTO_UPDATE=1
root="$(git rev-parse --show-toplevel)"
work_dir="$(mktemp -d)"
tap=rigbyworks/release-test
formula="$tap/lazyxcode"
if brew list --formula lazyxcode >/dev/null 2>&1 || brew tap | grep -qx "$tap"; then
  echo "Run on a clean host without lazyxcode or the release-test tap" >&2
  exit 1
fi
cleanup() {
  brew uninstall --force "$formula" >/dev/null 2>&1 || true
  brew untap "$tap" >/dev/null 2>&1 || true
  brew untrust --formula "$formula" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT
mkdir -p "$work_dir/tap/Formula"
git archive --format=tar.gz --prefix=lazyxcode-0.0.0/ HEAD > "$work_dir/lazyxcode-0.0.0.tar.gz"
checksum="$(shasum -a 256 "$work_dir/lazyxcode-0.0.0.tar.gz" | cut -d ' ' -f 1)"
sed -e 's/@TAG@/v0.0.0/g' -e "s/@SHA256@/$checksum/g" \
  -e "s|https://github.com/rigbyworks/lazyxcode/archive/refs/tags/v0.0.0.tar.gz|file://$work_dir/lazyxcode-0.0.0.tar.gz|" \
  "$root/scripts/homebrew-formula.rb.tmpl" > "$work_dir/tap/Formula/lazyxcode.rb"
git -C "$work_dir/tap" init -b main
git -C "$work_dir/tap" add Formula/lazyxcode.rb
git -C "$work_dir/tap" -c user.name=CI -c user.email=ci@example.invalid commit -m 'test: package source fixture'
brew trust --formula "$formula"
brew tap "$tap" "file://$work_dir/tap"
brew style "$formula"
brew install --build-from-source "$formula"
brew test "$formula"
brew reinstall --build-from-source "$formula"
brew test "$formula"
cp "$work_dir/lazyxcode-0.0.0.tar.gz" "$work_dir/lazyxcode-0.0.1.tar.gz"
sed 's/lazyxcode-0.0.0.tar.gz/lazyxcode-0.0.1.tar.gz/' "$work_dir/tap/Formula/lazyxcode.rb" > "$(brew --repository "$tap")/Formula/lazyxcode.rb"
brew upgrade --build-from-source "$formula"
test "$(lazyxcode --version)" = 'lazyxcode v0.0.1'
brew test "$formula"
export XDG_STATE_HOME="$work_dir/state"
mkdir -p "$XDG_STATE_HOME/lazyxcode"
printf 'preserve\n' > "$XDG_STATE_HOME/lazyxcode/sentinel"
brew uninstall "$formula"
test "$(cat "$XDG_STATE_HOME/lazyxcode/sentinel")" = preserve

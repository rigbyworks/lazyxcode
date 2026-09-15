#!/usr/bin/env bash
set -euo pipefail

tag="${1:?usage: update-homebrew.sh vMAJOR.MINOR.PATCH}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Expected a stable vMAJOR.MINOR.PATCH tag" >&2
  exit 2
fi
: "${GH_TOKEN:?Set GH_TOKEN to a token with contents and pull-request access to rigbyworks/homebrew-tap}"
gh release view "$tag" --repo rigbyworks/lazyxcode --json isDraft,isPrerelease --jq 'if .isDraft or .isPrerelease then error("Expected a published stable release") else empty end'
script_dir="$(cd "$(dirname "$0")" && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
curl --fail --location --retry 3 "https://github.com/rigbyworks/lazyxcode/archive/refs/tags/$tag.tar.gz" -o "$work_dir/source.tar.gz"
checksum="$(shasum -a 256 "$work_dir/source.tar.gz" | cut -d ' ' -f 1)"
gh repo clone rigbyworks/homebrew-tap "$work_dir/tap" -- --quiet
cd "$work_dir/tap"
branch="mwahlig/release-lazyxcode-$tag"
if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
  git fetch origin "$branch"
  git checkout -b "$branch" FETCH_HEAD
else
  git checkout -b "$branch"
fi
mkdir -p Formula
sed -e "s/@TAG@/$tag/g" -e "s/@SHA256@/$checksum/g" "$script_dir/homebrew-formula.rb.tmpl" > Formula/lazyxcode.rb
if ! git diff --quiet -- Formula/lazyxcode.rb || ! git ls-files --error-unmatch Formula/lazyxcode.rb >/dev/null 2>&1; then
  git add Formula/lazyxcode.rb
  git -c user.name='github-actions[bot]' -c user.email='41898282+github-actions[bot]@users.noreply.github.com' commit -m "chore: update lazyxcode to $tag"
  git push origin "$branch"
fi
printf 'Update lazyxcode to %s from its tagged source archive.\n\nVerify source installation and package tests before merging.\n' "$tag" > "$work_dir/body.md"
pr="$(gh pr list --repo rigbyworks/homebrew-tap --head "$branch" --state open --json number --jq '.[0].number // empty')"
if [[ -n "$pr" ]]; then
  gh pr edit "$pr" --repo rigbyworks/homebrew-tap --title "lazyxcode $tag" --body-file "$work_dir/body.md"
elif ! gh pr list --repo rigbyworks/homebrew-tap --head "$branch" --state merged --json number --jq '.[].number' | grep -q .; then
  gh pr create --repo rigbyworks/homebrew-tap --base main --head "$branch" --title "lazyxcode $tag" --body-file "$work_dir/body.md"
fi

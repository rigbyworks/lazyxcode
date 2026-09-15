# Releasing lazyxcode

Releases are tagged source distributions. The Homebrew formula builds the binary locally; no signing, notarization, or binary-upload step is required.

## One-time setup

- Keep `rigbyworks/lazyxcode` and `rigbyworks/homebrew-tap` public.
- Add the `HOMEBREW_TAP_TOKEN` Actions secret to `rigbyworks/lazyxcode`. Use a fine-grained GitHub token restricted to `rigbyworks/homebrew-tap`, with Contents and Pull requests read/write access. The ordinary source repository Actions token cannot push to the separate tap.
- Keep Actions enabled on both repositories. Require successful CI before merging release changes and successful Package checks before merging formula updates.

## Publish a version

1. Merge the release changes to `main` after CI passes. Use stable tags such as `v0.1.0`; never move an existing release tag.
2. Run `make check` and `make build VERSION=v0.1.0`. Check `./lazyxcode --version` and `./lazyxcode --help` outside a project.
3. Verify a simulator build/run, tests, result browsing, and coverage with the supported Xcode versions. Exercise Device Hub and legacy Simulator. Confirm the selected phone opens without an unwanted external-display window.
4. Tag the verified commit and publish notes:

   ```sh
   git tag -a v0.1.0 -m 'Release v0.1.0'
   git push origin v0.1.0
   gh release create v0.1.0 --verify-tag --title 'lazyxcode v0.1.0' --notes-file release-notes.md
   ```

5. The Release workflow validates the tag, reruns checks, computes the source archive checksum, and opens a PR in the tap. Review the formula URL, checksum, and version, then merge after Package checks pass. No auto-merge is enabled.
6. On a clean Apple Silicon Mac, run `brew install rigbyworks/tap/lazyxcode`, `lazyxcode --version`, and `brew test rigbyworks/tap/lazyxcode`.

Use `brew update && brew upgrade lazyxcode` to verify subsequent releases. Uninstalling with `brew uninstall lazyxcode` must preserve preferences, history, and caches.

## Retry a tap update

After fixing credentials or a transient failure, run the Release workflow manually with the existing tag. The version-specific branch and PR are reused. For a local retry, use an authenticated `gh` session with access to the tap, or set `GH_TOKEN` to the tap token in your shell. Run `gh auth setup-git`, then execute:

```sh
bash scripts/update-homebrew.sh v0.1.0
```

The formula template lives in `scripts/homebrew-formula.rb.tmpl`. Fix that template before cutting a new release. The release workflow reads the template at the release tag; a correction to an already published formula can be made directly in a tap PR without moving the source tag.

## v0.1.0 rename

The executable and all new state/cache paths use `lazyxcode`. Cloud credentials use the `LAZYXCODE_` prefix. There is no compatibility alias or migration from `lazy-xcode`; old data is not modified or deleted. Call this out in the first release notes.

## Run the disposable integration check

```sh
LAZYXCODE_RELEASE_SMOKE=1 go test ./internal/xcode -run '^TestReleaseSmoke$' -v -count=1 -timeout 15m
```

This creates a fixture project and an iPhone 16 simulator, runs the real build, install, attached launch, XCTest, result-inspection, and coverage commands, then deletes the simulator. An iOS runtime must already be installed. CI runs it with Xcode 16.3 to exercise the minimum supported version and legacy Simulator. Run it locally with Xcode 27 to cover Device Hub as well.

CI also runs `bash scripts/test-homebrew.sh` on a clean macOS host. It packages the current commit in a temporary tap, checks install/reinstall, upgrades between two local fixture versions, and verifies uninstall preserves state. It does not publish test versions. Homebrew requires Command Line Tools compatible with the host macOS even when full Xcode is installed.

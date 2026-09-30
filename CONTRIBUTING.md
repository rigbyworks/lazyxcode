# Contributing to lazyxcode

Thanks for helping out. Bug reports, fixes, and small features are all welcome. For a larger change, open an issue first so we can agree on the approach before you spend time on it.

## Setup

You need macOS 15 or newer and a Swift 6.4 toolchain, which ships with Xcode 27. The Xcode that lazyxcode drives can be a different install, as long as it's 16.3 or newer.

```sh
git clone https://github.com/rigbyworks/lazyxcode.git
cd lazyxcode
swift build
swift run lazyxcode --snapshot
```

`--snapshot` renders a sample workspace without Xcode or a real terminal, which is a quick way to check that the build works. To try your changes against a real project, run the debug binary from that project's directory:

```sh
cd /path/to/your/app
/path/to/lazyxcode/.build/debug/lazyxcode
```

## Project layout

| Path | Contents |
| --- | --- |
| `Sources/LazyXcodeCore` | Xcode commands, process cancellation, build activities, storage, results, Xcode Cloud networking |
| `Sources/LazyXcode` | Workspace state and the Swift-TUI views |
| `Tests/LazyXcodeCoreTests` | Core logic tests, using stand-ins for the command runner and URL session |
| `Tests/LazyXcodeTests` | UI tests that render the real views and send the same key events the terminal does |
| `scripts/` | Build, release, and smoke test scripts |

## Tests

```sh
make test     # unit and UI tests
make check    # formatting, tests, build, and CLI checks
```

These need no simulator, credentials, or network access after packages resolve. Run `make check` before you open a PR. CI runs it along with `make terminal-smoke`.

Slower checks, when your change touches them:

| Command | What it covers |
| --- | --- |
| `make terminal-smoke` | Drives the real terminal runtime in a PTY against a throwaway project |
| `LAZYXCODE_DEVICE_SMOKE=1 make terminal-smoke` | Also creates a simulator through the Devices window, then deletes it |
| `make smoke` | Builds, launches, and tests a fixture app on a temporary simulator. Needs an installed iOS runtime. |

To export rendered UI snapshots while testing, set `LAZYXCODE_SNAPSHOT_DIR=/tmp/lazyxcode-snapshots` when running `swift test`.

## Code style

The repository uses `swift format` with the settings in `.swift-format`. Fix formatting with:

```sh
swift format format --in-place --recursive Sources Tests Package.swift
```

Match the surrounding code. Keep UI behavior covered by a test in `Tests/LazyXcodeTests`, and put anything that shells out to Xcode behind the command runner so it stays testable.

## Pull requests

- Keep each PR to one change. Small PRs get reviewed faster.
- Use a short, imperative title, like "Add simulator filtering to the Devices window".
- Describe what changed and how you tested it. For UI changes, include a screenshot or a snippet of the terminal output.
- Update `README.md` if you add or change a keybinding or user-facing behavior.

## Reporting bugs

Use the bug report template when you [open an issue](https://github.com/rigbyworks/lazyxcode/issues/new/choose). The output of these commands helps a lot:

```sh
lazyxcode --version
sw_vers -productVersion
xcodebuild -version
```

Press `i` in lazyxcode to find the log for the activity that failed. Remove anything private before you attach it.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).

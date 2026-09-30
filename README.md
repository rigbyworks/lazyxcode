# lazyxcode

A keyboard-driven terminal UI for building, running, and testing Xcode projects. Run it in a terminal next to your editor and skip the Xcode window for everyday build, run, and test loops.

Built in Swift with [Swift-TUI](https://github.com/SwiftTUI/swift-tui).

## Getting started

### Requirements

- macOS 15 or newer
- Full Xcode 16.3 or newer for the projects you build, with first-launch setup done and the simulator runtimes you need installed
- To build lazyxcode from source, a Swift 6.4 toolchain (included with Xcode 27)

### Install with Homebrew

```sh
brew install rigbyworks/tap/lazyxcode
```

### Build from source

```sh
git clone https://github.com/rigbyworks/lazyxcode.git
cd lazyxcode
make install
```

This installs to `~/.local/bin`. Add it to your `PATH`, or pick another directory with `make install BINDIR=/your/bin`.

### Run it

```sh
cd /path/to/your/app
lazyxcode
```

lazyxcode finds the `.xcworkspace` or `.xcodeproj` in the current directory. If there are several, it asks which one to open and remembers your choice.

## Features

- **Build and run** on simulators, physical devices, and the Mac. App console output streams into the Output pane.
- **Concise build output** with one row per build phase, timings, and deduplicated diagnostics. Press `v` for the raw log.
- **Tests** for the whole scheme, unit or UI only, or a single test. Rerun only the failures.
- **Results and coverage** browsing, drilling from target to file to function, with comparisons against earlier runs.
- **Simulator and device management** to list, filter, create, and open simulators without leaving the terminal.
- **Concurrent builds** for different scheme and destination pairs, each with its own DerivedData.
- **Xcode Cloud** (optional, read-only): browse runs, logs, test results, and artifacts.
- **Fast startup**: schemes and destinations are cached, then refreshed in the background.

## Usage

The workspace has three panes: **Build** (scheme and destination), **Activity** (history of builds and tests), and **Output**. Press `1`, `2`, or `3` to focus one, or `?` for help at any time.

1. In Build, pick a **Scheme** and **Target** with the arrow keys and Enter. Type to filter any picker.
2. Press `b` to build or `r` to build and run.
3. Press `t` to choose which tests to run. Coverage is on by default.
4. Select a finished test activity in Activity and press Enter to see failures, coverage, and attachments, or rerun failed tests.
5. Press `:` to search every available action.

### Simulators and devices

Press `d` to open the Devices window. It lists every simulator and physical device Xcode knows about.

- `/` filters by name, OS, kind, or state.
- Enter opens actions for the highlighted device, like opening a simulator or making it the build target.
- `n` creates a simulator from an installed runtime. If no runtimes are listed, install one in Xcode > Settings > Components.

### Xcode Cloud

Cloud mode needs an App Store Connect team API key. Set these before launching:

```sh
export LAZYXCODE_ASC_ISSUER_ID="<issuer id>"
export LAZYXCODE_ASC_KEY_ID="<key id>"
export LAZYXCODE_ASC_PRIVATE_KEY_PATH="$HOME/.private_keys/AuthKey_<key id>.p8"
```

Press `m` to switch to Cloud, then choose a product and workflow. Cloud mode only reads data. It can't start, cancel, or rerun builds. lazyxcode never writes the key or its tokens to disk, and warns if other users can read the key file.

### Choosing an Xcode

lazyxcode uses whichever Xcode `DEVELOPER_DIR` or `xcode-select` points to.

## Keybindings

Press `?` in the app for the full list.

| Key | Action |
| --- | --- |
| `1` `2` `3` | Focus Build, Activity, Output |
| `Tab` / `Shift-Tab` | Cycle panes |
| `↑` `↓` / `j` `k` | Move or scroll |
| `g` / `G` | Jump to top / bottom and follow output |
| `PgUp` / `PgDn` | Move ten rows |
| `Enter` | Choose, or open results |
| `Esc` | Back or cancel |
| `b` | Build |
| `r` | Build and run (refresh runs in Cloud mode) |
| `t` | Run tests |
| `x` | Cancel the selected activity |
| `c` | Clear this project's DerivedData |
| `R` | Reload schemes and destinations |
| `d` | Simulators and devices |
| `v` | Toggle concise / raw output |
| `y` | Copy output |
| `[` / `]` | Older / newer raw log page |
| `o` | Open the project in Xcode |
| `i` | Show log, result, and project paths |
| `m` | Switch Local / Cloud |
| `L` | Load older Cloud runs |
| `a` | Download a Cloud artifact |
| `:` | Search actions |
| `?` | Help |
| `q` / `Ctrl-C` | Quit |

The terminal must be at least 44×10. Below 80 columns, the panes become tabs.

## Troubleshooting

- **The package won't compile.** Check `swift --version`. Building lazyxcode requires Swift 6.4.
- **Discovery or builds fail.** Check `xcode-select -p` and `xcodebuild -version`. Select a full Xcode install rather than the Command Line Tools, make sure the scheme is shared, and install a compatible simulator runtime. Then press `R`.
- **A scheme or device is missing.** Press `R` to skip the cache and query Xcode again.
- **Results or coverage are missing.** Press `i` for the full log path, or `v` to read the raw output.

### Where lazyxcode stores data

| What | Location |
| --- | --- |
| Preferences, history, logs, test results | `$XDG_STATE_HOME/lazyxcode` (default `~/.local/state/lazyxcode`) |
| DerivedData and Cloud artifacts | `$XDG_CACHE_HOME/lazyxcode` (default `~/.cache/lazyxcode`) |

lazyxcode keeps the newest 100 activities. `c` clears only this project's managed DerivedData. Xcode's global DerivedData is never touched.

## Contributing

Issues and pull requests are welcome. For larger changes, open an issue first so we can agree on the approach.

```sh
swift build
swift run lazyxcode --snapshot   # render a sample workspace without Xcode
make test                        # unit and terminal UI tests
make check                       # formatting, tests, build, and CLI checks
```

Run `make check` before opening a PR. To fix formatting:

```sh
swift format format --in-place --recursive Sources Tests Package.swift
```

The default tests don't need a simulator, credentials, or network access. `make terminal-smoke` drives the real terminal against a throwaway project, and `make smoke` runs a simulator integration test.

The code is split into two modules:

- `Sources/LazyXcodeCore` runs Xcode commands and handles build activities, storage, results, and Xcode Cloud networking.
- `Sources/LazyXcode` holds the workspace state and the Swift-TUI views.

Maintainers: see [RELEASING.md](RELEASING.md) for the release process.

## License

[MIT](LICENSE) © Rigby Works.

The Braille spinner frames are adapted from [throbber-widgets-tui](https://github.com/arkbig/throbber-widgets-tui) under the [zlib license](LICENSES/throbber-widgets-tui.txt).

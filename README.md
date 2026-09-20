# lazyxcode

A keyboard-first terminal app for building, running, and testing Xcode projects, written in Swift with [Swift-TUI](https://github.com/SwiftTUI/swift-tui).

Run it beside your editor. Choose a project, scheme, and destination, then follow local builds, app output, tests, and coverage. An optional read-only Cloud mode browses Xcode Cloud runs and artifacts.

## Build and install

You need macOS 15 or newer and a Swift 6.4 toolchain to compile lazyxcode. Xcode 27 includes a suitable compiler. The Xcode projects you work on can use a separately selected Xcode 16.3 or newer. Complete Xcode's first-launch setup and install the simulator runtimes your projects need.

```sh
git clone https://github.com/rigbyworks/lazyxcode.git
cd lazyxcode
swift --version
make install
```

The executable goes into `~/.local/bin`. Add that directory to your shell's `PATH`, or choose a different directory with `make install BINDIR=/your/bin`. A source checkout reports `lazyxcode dev`; release builds use `make build VERSION=v0.1.0`.

```sh
export PATH="$HOME/.local/bin:$PATH"
lazyxcode --version
lazyxcode --help
```

The Homebrew release formula in `scripts/homebrew-formula.rb.tmpl` builds with Xcode 27 or newer. Published versions are available through `brew install rigbyworks/tap/lazyxcode`; the tap changes only when a release is published.

## Use it with a project

```sh
cd /path/to/your/app
lazyxcode
```

Discovery checks the current directory for `.xcworkspace` and `.xcodeproj` directories. Workspaces appear first. A picker opens when there are several containers and no remembered choice.

1. In Build, select Scheme or Target with the arrow keys and press Enter. Type in a picker to filter its rows.
2. Press `b` to build or `r` to build and run. Simulator and device launches stream their console output until the app exits or you cancel the activity.
3. Press `t` for all, unit, UI, or individual tests. Coverage is enabled by default and remembered per project.
4. Select a completed test activity and press Enter to inspect results, coverage, attachments, or rerun failed tests.

Destinations refresh every 15 seconds and when you open the destination picker. Discovery respects the selected scheme and excludes unavailable simulators and placeholder destinations. `DEVELOPER_DIR` and `xcode-select` determine which Xcode tools run.

## Keyboard reference

| Key | Action |
| --- | --- |
| `1`, `2`, `3` | Focus Build, Activity, Output |
| `Tab`, `Shift-Tab` | Cycle panes |
| arrows, `j`, `k` | Select or scroll |
| `g`, `G` | First/last activity or top/follow output |
| Page Up, Page Down | Move ten rows |
| Enter | Choose a setting or inspect results |
| Esc | Back or cancel result loading |
| `b` | Build locally |
| `r` | Build and run locally; refresh Cloud runs |
| `t` | Test scopes, individual discovery, coverage toggle |
| `x` | Cancel the selected local activity or pending download/load |
| `c` | Clear managed DerivedData when no activities are active |
| `R` | Reload local schemes and destinations |
| `m` | Switch Local / Cloud |
| `L` | Load older Cloud runs |
| `a` | Download a Cloud artifact |
| `v` | Toggle concise/raw output |
| `y` | Copy displayed output |
| `[`, `]` | Older/newer local raw log page |
| `o` | Open the project in Xcode |
| `i` | Show complete status, project, log, and result paths |
| `:` | Search actions |
| `?` | Keyboard help |
| `q`, Ctrl-C | Quit and cancel active commands |

Inside a picker, typing filters the list, arrows select, Enter chooses, and Esc goes back. Pickers open over the workspace and retain the current selection. Cache clearing and quitting with active work require confirmation. The terminal needs at least 44 columns and 10 rows. Short windows collapse the unfocused Build pane.

## Builds, logs, and tests

Different scheme/destination pairs can run concurrently. Each pair has a separate incremental DerivedData directory. A duplicate build is rejected while its cache is in use. Starting another activity for a pair with an attached running app stops that app's launch command first.

Simulator launches boot the device, open Device Hub or Simulator from the selected Xcode, install the app, and attach to its console. Physical devices use `devicectl`. Mac builds open the generated app.

Concise output shows one row per build phase, with accumulated wall-clock timings, deduplicated source diagnostics, grouped test suites, and the attached app console. Timings are stored in the log using the Go-compatible progress format, so reopening an activity preserves them. Repeated compiler work and long compiler commands do not fill the concise summary or trigger a summary-limit notice.

The live raw and console windows retain up to 2,000 lines or 256 KiB, with explicit notices when older content is omitted. Complete output stays on disk. Scrolling pauses the displayed snapshot; `G` resumes following. Raw pages read 64 KiB at a time and expose every retained byte without silently dropping lines. Local and Cloud modes keep separate scroll and result-detail state.

Individual-test discovery creates a cancellable activity using the same cache lock as a build. Selecting a parameterized test runs all its arguments. Failed-test reruns retain the original scheme, destination, and coverage choice; an empty failure list never starts the entire suite.

Result menus browse test cases, show readable test failures, source locations, runs, and activities, export attachments, and open result bundles in Xcode. Coverage navigation supports targets, files, and functions. Comparisons use an earlier run of the same project, scheme, and destination, with each baseline's scope shown in the picker. Result details and coverage require Xcode 16.3 or newer.

## Xcode Cloud

Set a team App Store Connect API key before starting lazyxcode:

```sh
export LAZYXCODE_ASC_ISSUER_ID="<issuer id>"
export LAZYXCODE_ASC_KEY_ID="<key id>"
export LAZYXCODE_ASC_PRIVATE_KEY_PATH="$HOME/.private_keys/AuthKey_<key id>.p8"
```

Press `m`, then choose a product and optional workflow. Local workflows do not require credentials. The client signs ten-minute ES256 tokens in memory and sends GET requests only. It warns when the key file is readable by other users. Key material and tokens are never saved in project preferences or logs.

The newest 25 runs load first. Press `L` for older runs, or move past the last row. Cloud mode refreshes every 15 seconds while a visible run is active and every 60 seconds otherwise. Failures preserve the last successful data and mark it stale. Transient failures and rate limits retry with backoff; authentication and permission failures are reported directly.

Output includes workflow, branch, commit, actions, diagnostics, test summaries, and artifact names. Press `v` to download log artifacts, `a` to download other artifacts, or Enter to inspect a test-result archive. Downloads verify reported sizes and publish complete files atomically. Artifact requests never receive the App Store Connect token. Archive extraction rejects path traversal, symlinks, unsupported entry types, multiple result bundles, and more than 4 GiB of expanded content.

Cloud mode cannot change workflows, start builds, cancel builds, or rerun tests. Downloaded artifacts have no retention limit. API keys cannot be changed inside the app; restart after changing environment variables.

## State and compatibility

Preferences, the newest 100 activities plus older active activities, logs, and test results live below `$XDG_STATE_HOME/lazyxcode`, or `~/.local/state/lazyxcode`. DerivedData and Cloud artifacts live below `$XDG_CACHE_HOME/lazyxcode`, or `~/.cache/lazyxcode`.

The Swift rewrite reads the earlier Go version's preferences and history. It uses the same project and cache hashes. Activities left active by a previous process become cancelled on load. Expired history entries remove only their managed logs and results.

Clearing the cache removes this project's managed DerivedData. Logs, results, project files, Cloud artifacts, and Xcode's global DerivedData remain available. Data from the older command name `lazy-xcode` is not migrated.

## Development

```sh
swift build
swift run lazyxcode --snapshot
make test
make check
make build
```

`make check` checks Swift formatting, runs tests, builds the executable, and checks the CLI outside a project. Format edits with `swift format format --in-place --recursive Sources Tests Package.swift`.

`Sources/LazyXcodeCore` owns Xcode commands, process cancellation, build activities, storage, results, and Cloud networking. `Sources/LazyXcode` owns the observable workspace state and Swift-TUI views. The command runner and URL sessions are replaceable test boundaries. UI tests render the real views at normal and 44×10 sizes and drive the same keyboard handlers used by the terminal. Parity tests cover phase aggregation and retained timings, concise diagnostics and test suites, paused output, mode state, confirmations, simulator test startup, command-stream separation, and Cloud pagination. Set `LAZYXCODE_SNAPSHOT_DIR=/tmp/lazyxcode-snapshots` when running `swift test` to export build-output and target-picker renderer snapshots.

The default tests need no simulator, credentials, or network after package resolution. `make terminal-smoke` drives the real terminal runtime against a disposable project. `make smoke` opts into a disposable simulator integration test. See [RELEASING.md](RELEASING.md) for release and package checks.

## Troubleshooting

Check `swift --version` if the package cannot compile. Swift-TUI 0.13.5 requires Swift 6.4; a compiler from an older Xcode is insufficient.

Check `xcode-select -p` and `xcodebuild -version` if discovery or builds fail. Select full Xcode with `DEVELOPER_DIR`, share the scheme, and install a compatible simulator runtime. Press `R` to reload. If a test bundle is missing coverage or results, its complete log remains available through `i` and raw output.

## License

[MIT](LICENSE), copyright Rigby Works.

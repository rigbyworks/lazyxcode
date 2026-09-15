# lazyxcode

A keyboard-first terminal UI for building, running, and testing Xcode projects.

Keep your editor open and run your app from the terminal. `lazyxcode` finds your Xcode projects, schemes, and compatible destinations, then puts build controls, activity history, and live output in three panes. It uses your installed Xcode tools, so you can build and test without keeping the Xcode app open.

[Installation](#installation) · [Getting started](#getting-started) · [Keyboard shortcuts](#navigation) · [Contributing](#contributing) · [MIT license](LICENSE)

## Features

- Build and run on simulators, connected Apple devices, or your Mac.
- Follow build progress, warnings, errors, and app console output. Switch to the full raw log when you need it.
- Run XCTest and Swift Testing suites or individual tests, inspect failures and attachments, and rerun failed tests.
- Browse code coverage and compare it with earlier runs.
- Keep activity history, logs, and test results across sessions, with separate incremental build caches for each project, scheme, and destination.
- Browse Xcode Cloud builds, diagnostics, test results, and artifacts in an optional read-only mode.

## Contents

- [Requirements](#requirements)
- [Installation](#installation)
- [Getting started](#getting-started)
- [Navigation](#navigation)
- [Builds and output](#builds-and-output)
- [Test results and coverage](#test-results-and-coverage)
- [Xcode Cloud](#xcode-cloud)
- [State and cache](#state-and-cache)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [Upgrading from lazy-xcode](#upgrading-from-lazy-xcode)
- [License](#license)

## Requirements

- An Apple Silicon Mac running macOS 15 or newer.
- Full Xcode 16.3 or newer, with first-launch setup completed. The standalone Command Line Tools are not enough.
- An Xcode project or workspace with a shared scheme and a compatible destination. Install the simulator runtime you want to use through Xcode.
- Go 1.25 or newer to build from source.

`lazyxcode` uses the Xcode installation selected by `xcode-select` or `DEVELOPER_DIR`. Check your selection with:

```sh
xcode-select -p
xcodebuild -version
```

## Installation

### Build from source

Install [Go](https://go.dev/doc/install), then clone and build the project:

```sh
git clone https://github.com/rigbyworks/lazyxcode.git
cd lazyxcode
make install
```

This installs `lazyxcode` into `~/.local/bin`. Add that directory to your shell's `PATH` if it is not already there:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Add the same line to your shell configuration, such as `~/.zshrc`, to keep it for new terminals. To use another install directory, run `make install BINDIR=/your/bin/directory`.

Check the installed command:

```sh
lazyxcode --version
lazyxcode --help
```

A checkout build reports `lazyxcode dev`. To update it, pull the latest changes in your checkout and run `make install` again.

### Homebrew

Install the latest release from the Rigby Works tap:

```sh
brew install rigbyworks/tap/lazyxcode
```

Then update with `brew update && brew upgrade lazyxcode`, or remove it with `brew uninstall lazyxcode`. Uninstalling leaves saved preferences, history, and caches on disk. Check [releases](https://github.com/rigbyworks/lazyxcode/releases) for availability and release notes.

## Getting started

Run `lazyxcode` from the directory that directly contains your `.xcworkspace` or `.xcodeproj`:

```sh
cd /path/to/your/app
lazyxcode
```

If more than one project or workspace is present, choose one in the startup picker. Workspaces appear first, and `lazyxcode` remembers your selection.

1. Press `1` to focus the Build pane. Use the arrow keys or `j` and `k` to select the scheme or target row, then press `Enter` to choose a value.
2. Press `r` to build and run, or `b` to build without launching. Build progress appears in Output. A successful run launches the app on your selected destination.
3. Press `t` to choose which tests to run. Select a completed test activity and press `Enter` to browse results or coverage.
4. Press `?` for keyboard help, or `:` to search available actions. Press `q` to quit.

Local builds do not require an App Store Connect API key. Set up [Xcode Cloud credentials](#credentials) only if you want to browse cloud builds.

## Navigation

| Key | Local mode | Cloud mode |
| --- | --- | --- |
| `1`, `2`, `3` | Focus Build, Activity, or Output | Same |
| `Tab`, `Shift-Tab` | Cycle panes | Same |
| `m` | Switch to Cloud mode | Switch to Local mode |
| `o` | Open the selected project or workspace in Xcode | Same |
| `Enter` | Select a scheme/target, inspect a test activity, or open actions for displayed results | Select a product/workflow or browse test-result artifacts |
| `b` | Build without launching | Unavailable (read-only) |
| `r` | Build and run | Refresh build runs |
| `t` | Run all, unit, UI, or individual tests; toggle coverage | Unavailable (read-only) |
| `Esc` | Return from test details or coverage to the log; go back in result pickers | Return from result details; go back in pickers |
| `x` | Cancel the selected active activity | Cancel the current artifact download |
| `c` | Clear managed DerivedData | Unavailable (read-only) |
| `R` | Reload schemes and targets | Unavailable (read-only) |
| `L` | Unused | Load older build runs |
| `a` | Unused | Download an artifact from the selected run |
| `v` | Toggle concise or raw output | Toggle structured details or raw logs |
| `y` | Copy the displayed output | Copy the displayed output |
| `j`, `k`, arrows | Navigate or scroll | Same; moving past the last run loads older runs |
| `g`, `G` | First/last activity or top/follow output | Same |
| `:` | Search available actions | Same, with Cloud actions only |
| `i` | Show full status and project details | Show connection details and complete warnings |
| `?` | Show keyboard help | Same |
| `q`, `Ctrl-C` | Quit | Same |

The footer shows a few actions for the focused pane. Press `:` to search the full action menu; existing shortcuts still work. Status messages appear in the header, and `i` opens their complete text in a wrapping, scrollable details view.

Local build targets refresh in the background every 15 seconds and whenever you open the target picker. Newly available devices appear without restarting, including while the picker is open or a build is running. Refreshes preserve your selection and search text; a failed discovery keeps the last known list until a later refresh succeeds.

Cloud configuration stays compact: Product, Workflow, connection status, and refresh timing. Select the connection row or press `i` to inspect warnings, errors, and downloads. Full connection warnings also appear in concise Output, including when there are no build runs.

## Builds and output

Distinct scheme/target pairs can build or test concurrently. Successful simulator builds boot the selected simulator, open the device window, install the generated app, and launch it. The window uses Device Hub from the selected Xcode, respecting `DEVELOPER_DIR` or `xcode-select`. If that Xcode does not include Device Hub, it uses the same Xcode's Simulator.app. Physical-device builds deploy and launch through `devicectl`; Mac builds launch the generated app directly. Simulator and physical-device launches remain attached and stream the app's standard output and error, including `print` output, until the app exits. Press `x` to stop the running app. Starting another build for the same scheme and target stops the attached app first; other duplicate activities are rejected because they share an incremental DerivedData cache.

The output pane is concise by default. It shows live, wall-clock build phases, a deduplicated list of warnings and errors using compact `file:line:column — message` entries, and the attached app console after launch. Press `v` to inspect the complete raw transcript and Xcode's detailed command timing summary; persisted logs retain build and runtime output together with phase timings.

Test output follows the same concise/raw model. Concise mode groups XCTest and Swift Testing results by suite, shows the currently running test, pass/failure counts, suite durations, and source-linked failures.

## Test results and coverage

Press `t` to run a test scope or choose **Individual Test**. Individual-test discovery builds and enumerates the selected scheme's enabled tests, then opens a searchable picker. It appears as a cancellable activity and shares the normal scheme/destination build lock. Press `Enter` on a completed discovery activity to reopen its picker. Both XCTest and Swift Testing are supported; selecting a parameterized test runs all its arguments.

Code coverage is on by default. Toggle it in the `t` menu; the choice is remembered per project or workspace. Coverage adds test instrumentation and can affect runtime, so turn it off when measuring uninstrumented performance.

Select a completed test activity and press `Enter` to:

- Browse test results, including status and duration. Select a test to show its failure details, source locations, and individual runs in Output.
- Rerun failed tests. Reruns use the original activity's scheme, destination, and coverage setting, and rebuild changed sources. An empty failure list never falls back to running the whole suite.
- Browse coverage by target, source file, and function.
- Compare coverage with an earlier run of the same project, scheme, and destination. The report shows percentage-point changes and added or removed targets, files, and functions. The baseline picker includes each run's test scope because running different subsets changes coverage.
- Open the complete result bundle in Xcode.

While viewing a test's details, press `Enter` for activities, attachment export, or **Run This Test**. Exported screenshots and other attachments open with their default macOS application. `Esc` returns to the activity log, and `v` returns to concise/raw log output. The normal Output scrolling and copy controls also work for test details and coverage. In nested pickers, `Esc` goes back; during a result load it cancels the command.

Result inspection and attachment export require Xcode 16.3 or newer. Missing coverage, older history without a retained bundle, and runs that stop before producing a bundle show an explanation while their logs remain available.

The layout adapts to smaller terminal panes. Narrow panes keep the controls and output side by side, while short panes collapse the unfocused Build or Activity section to its title and expand the focused section.

## Xcode Cloud

Press `m` to switch the three panes between Local and Cloud. Cloud mode is read-only: it lists recent Xcode Cloud build runs for a product, optionally filtered by workflow, and shows each run's branch and commit, actions, structured diagnostics, test results, and artifacts. Switching modes never stops or interrupts local builds, and each mode keeps its own selection, scroll position, and concise/raw preference.

### Credentials

Cloud mode reads an App Store Connect team API key from the environment:

```sh
export LAZYXCODE_ASC_ISSUER_ID="<issuer id>"
export LAZYXCODE_ASC_KEY_ID="<key id>"
export LAZYXCODE_ASC_PRIVATE_KEY_PATH="$HOME/.private_keys/AuthKey_<key id>.p8"
```

Create the key in App Store Connect under Users and Access > Integrations > App Store Connect API. Use a dedicated key with the lowest role that can read Xcode Cloud data (Developer is sufficient) rather than an Admin key, download the `.p8` file once, and keep it readable only by your user (`chmod 600`). `lazyxcode` warns when the file is group- or world-readable.

The private key is read only to sign short-lived ES256 JSON Web Tokens in memory; it is never copied into preferences, logs, error messages, status text, or the cache. The client issues GET requests only and cannot start, cancel, or edit anything in Xcode Cloud. Individual API keys and Keychain-backed key storage are not supported yet.

When the variables are missing, Cloud mode shows setup guidance and Local mode remains fully usable. Press `r` in Cloud mode to retry after fixing the configuration.

### Browsing runs

The first visit to Cloud mode lists the products visible to the key. A sole product is selected automatically; otherwise a searchable picker opens, and the choice is remembered per container together with an optional workflow filter. The activity pane shows the newest 25 runs with a status (`WAIT`, `RUN`, `OK`, `FAIL`, `STOP`, `SKIP`), build number, workflow, and duration. Moving past the last row loads and selects the next older run; press `L` to load another page directly.

Runs refresh automatically every 15 seconds while any visible run is active and every 60 seconds otherwise, only while Cloud mode is visible. Press `r` for an immediate refresh. API failures keep the last successful data on screen and mark it stale; transient errors and rate limits are retried with backoff, while authentication and permission failures are reported without retrying.

The output pane shows structured details for the selected run: workflow, source and destination branch and commit, actions with durations, deduplicated diagnostics, test summaries with failing tests, and artifact metadata. Press `v` to download the run's log artifacts on demand and show them as text; archived logs are expanded per file, and non-text artifacts are left on disk with their path shown. Press `a` to pick any artifact, including `.xcresult` bundles and archives, and download it to the managed cache. Downloads stream to a temporary file, verify the size Apple reports, and are renamed atomically; `x` cancels the download in progress and removes the partial file.

Press `Enter` on a Cloud run to pick a test-result artifact. The first selection downloads it; press `Enter` again after download to browse its results, activities, attachments, and any recorded coverage. Cloud inspection is read-only. Failed-test reruns and history-based coverage comparisons are available in Local mode. Result archives expand inside the artifact cache and reject unsafe paths, unsupported file types, multiple result bundles, or more than 4 GiB of expanded content.

### Limitations

- Cloud mode cannot start or cancel builds or edit workflows.
- Branch, pull-request, status, and date filters are not available yet.
- Only artifact metadata is cached in memory; run history is fetched again on the next launch.
- Downloaded artifacts stay in the cache until you delete them; there is no retention limit yet.
- Apple's API scope claim syntax for relationship routes has not been verified, so tokens are minted without a `scope` claim and rely on the key's role for least privilege.

## State and cache

The newest 100 activity records, plus any older active activities, and their complete logs are stored below `$XDG_STATE_HOME/lazyxcode` or `~/.local/state/lazyxcode`. Incremental DerivedData is isolated per project, scheme, and target below `$XDG_CACHE_HOME/lazyxcode` or `~/.cache/lazyxcode`. Cloud artifacts are downloaded to artifact-specific directories below `projects/<project-hash>/cloud/<run-id>/artifacts/`, with filenames sanitized to stay inside that directory.

Local test bundles and exported attachments live under the project state directory in `results/<activity-id>/` and expire with their history records. Test discovery saves its test list there as well.

The cache action deletes only DerivedData managed by `lazyxcode`; it does not delete build history, logs, retained test results, downloaded cloud artifacts, project files, or Xcode's global DerivedData. The preferences file also remembers the selected Xcode Cloud product and workflow per container; files written by earlier versions load unchanged.

## Troubleshooting

### Xcode is missing or the wrong version is selected

Open Xcode once to finish setup. If `xcode-select -p` points to the standalone Command Line Tools or another Xcode installation, select the full Xcode installation for your terminal session:

```sh
export DEVELOPER_DIR="/Applications/Xcode.app/Contents/Developer"
xcodebuild -version
lazyxcode
```

Adjust the path if you installed Xcode elsewhere.

### No project, scheme, or destination appears

- Start in the directory that directly contains the `.xcworkspace` or `.xcodeproj`. Discovery does not search subdirectories.
- Check that the scheme is shared in Xcode and belongs to the selected project or workspace. Press `R` to reload schemes and targets.
- Check that the selected scheme supports your destination and that the required simulator runtime is installed. For a physical device, check its connection and availability in Xcode.

### The shell cannot find lazyxcode

For a source install, check that `~/.local/bin` is in your `PATH`, or add the custom `BINDIR` you used during installation. Run `command -v lazyxcode` to see which executable your shell finds.

## Contributing

Bug reports, documentation improvements, and pull requests are welcome. [Open an issue](https://github.com/rigbyworks/lazyxcode/issues) to report a problem or discuss a feature. For larger changes, describe the proposal in an issue before starting implementation.

For bug reports, include:

- Your `lazyxcode --version`, macOS version, and `xcodebuild -version` output.
- The steps to reproduce the problem, what you expected, and what happened.
- Whether the problem affects a simulator, physical device, Mac, or Xcode Cloud.
- Relevant output or a screenshot. Remove credentials and private project details before posting.

### Develop locally

Fork the repository and clone your fork, or use the checkout from the [source installation](#build-from-source). The project is written in Go. From the repository root, run:

```sh
make build
./lazyxcode --help
make test
make check
```

`make build` writes the executable to the repository root. To try your changes against an Xcode project, run that executable from the app's directory:

```sh
cd /path/to/your/app
/path/to/lazyxcode/lazyxcode
```

`make test` runs the Go tests. `make check` checks formatting, runs `go vet`, runs tests with the race detector, and checks that the project builds. Format changed Go files with `gofmt` before running it.

Tests use fake command runners, headless gocui instances, and local HTTP test servers for the App Store Connect API. The default suite does not need an available simulator or external network access after Go dependencies are downloaded. For the opt-in integration test that builds and runs a real simulator app, see [RELEASING.md](RELEASING.md#run-the-disposable-integration-check).

Keep pull requests focused, explain the behavior change, and include how you tested it. Add or update tests when changing behavior. For UI changes, include a screenshot or recording when it helps show the result.

Release maintainers can find the release and Homebrew workflow in [RELEASING.md](RELEASING.md).

## Upgrading from lazy-xcode

The command is now `lazyxcode`. Update aliases and scripts, and remove an obsolete `lazy-xcode` executable manually after confirming its location with `command -v lazy-xcode`.

Cloud environment variables now start with `LAZYXCODE_`, replacing `LAZY_XCODE_`. Existing state and cache directories named `lazy-xcode` are left untouched. The renamed tool starts with fresh preferences and history under `lazyxcode`; no automatic migration or old-name fallback is performed.

## License

[MIT](LICENSE), copyright Rigby Works.

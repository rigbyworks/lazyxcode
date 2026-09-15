# lazy-xcode

`lazy-xcode` is a keyboard-first terminal interface for building, running, and testing Xcode schemes without keeping Xcode open. It discovers the current directory's Xcode containers, shared schemes, test targets, and scheme-compatible simulators, connected devices, and the current Mac, then streams each activity into its own retained log. An optional read-only Cloud mode browses recent Xcode Cloud builds for the same project.

## Requirements

- macOS with Xcode and the command-line tools installed
- Go 1.25 or newer when building from source

## Install

```sh
make install
```

Run `lazy-xcode` from a directory containing a top-level `.xcworkspace` or `.xcodeproj`. When more than one is present, workspaces are listed first in a startup picker and the selection is remembered.

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
export LAZY_XCODE_ASC_ISSUER_ID="<issuer id>"
export LAZY_XCODE_ASC_KEY_ID="<key id>"
export LAZY_XCODE_ASC_PRIVATE_KEY_PATH="$HOME/.private_keys/AuthKey_<key id>.p8"
```

Create the key in App Store Connect under Users and Access > Integrations > App Store Connect API. Use a dedicated key with the lowest role that can read Xcode Cloud data (Developer is sufficient) rather than an Admin key, download the `.p8` file once, and keep it readable only by your user (`chmod 600`). `lazy-xcode` warns when the file is group- or world-readable.

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

The newest 100 activity records, plus any older active activities, and their complete logs are stored below `$XDG_STATE_HOME/lazy-xcode` or `~/.local/state/lazy-xcode`. Incremental DerivedData is isolated per project, scheme, and target below `$XDG_CACHE_HOME/lazy-xcode` or `~/.cache/lazy-xcode`. Cloud artifacts are downloaded to artifact-specific directories below `projects/<project-hash>/cloud/<run-id>/artifacts/`, with filenames sanitized to stay inside that directory.

Local test bundles and exported attachments live under the project state directory in `results/<activity-id>/` and expire with their history records. Test discovery saves its test list there as well.

The cache action deletes only DerivedData managed by `lazy-xcode`; it does not delete build history, logs, retained test results, downloaded cloud artifacts, project files, or Xcode's global DerivedData. The preferences file also remembers the selected Xcode Cloud product and workflow per container; files written by earlier versions load unchanged.

## Development

```sh
make test
make check
make build
```

Tests use fake command runners, headless gocui instances, and `httptest` servers for the App Store Connect API, so they do not require an available simulator or network access.

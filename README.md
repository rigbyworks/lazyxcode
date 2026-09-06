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
| `Enter` | Select a scheme or target | Select a product or workflow filter |
| `b` | Start a build | Unavailable (read-only) |
| `t` | Run all, unit, or UI tests | Unavailable (read-only) |
| `x` | Cancel the selected active activity | Cancel the current artifact download |
| `c` | Clear managed DerivedData | Unavailable (read-only) |
| `r` | Reload schemes and targets | Refresh build runs |
| `L` | Unused | Load older build runs |
| `a` | Unused | Download an artifact from the selected run |
| `v` | Toggle concise or raw output | Toggle structured details or raw logs |
| `y` | Copy the displayed output | Copy the displayed output |
| `j`, `k`, arrows | Navigate or scroll | Same; moving past the last run loads older runs |
| `g`, `G` | First/last activity or top/follow output | Same |
| `?` | Show help | Same |
| `q`, `Ctrl-C` | Quit | Same |

Distinct scheme/target pairs can build or test concurrently. Successful simulator builds boot the selected simulator, open Simulator.app, install the generated app, and launch it. Physical-device builds deploy and launch through `devicectl`; Mac builds launch the generated app directly. Simulator and physical-device launches remain attached and stream the app's standard output and error, including `print` output, until the app exits. Press `x` to stop the running app. Starting another build for the same scheme and target stops the attached app first; other duplicate activities are rejected because they share an incremental DerivedData cache.

The output pane is concise by default. It shows live, wall-clock build phases, a deduplicated list of warnings and errors using compact `file:line:column — message` entries, and the attached app console after launch. Press `v` to inspect the complete raw transcript and Xcode's detailed command timing summary; persisted logs retain build and runtime output together with phase timings.

Test output follows the same concise/raw model. Concise mode groups XCTest and Swift Testing results by suite, shows the currently running test, pass/failure counts, suite durations, and source-linked failures.

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

### Limitations

- Cloud mode cannot start or cancel builds or edit workflows.
- Branch, pull-request, status, and date filters are not available yet.
- Only artifact metadata is cached in memory; run history is fetched again on the next launch.
- Downloaded artifacts stay in the cache until you delete them; there is no retention limit yet.
- Apple's API scope claim syntax for relationship routes has not been verified, so tokens are minted without a `scope` claim and rely on the key's role for least privilege.

## State and cache

The newest 100 activity records and their complete logs are stored below `$XDG_STATE_HOME/lazy-xcode` or `~/.local/state/lazy-xcode`. Incremental DerivedData is isolated per project, scheme, and target below `$XDG_CACHE_HOME/lazy-xcode` or `~/.cache/lazy-xcode`. Cloud artifacts are downloaded to artifact-specific directories below `projects/<project-hash>/cloud/<run-id>/artifacts/`, with filenames sanitized to stay inside that directory.

The cache action deletes only DerivedData managed by `lazy-xcode`; it does not delete build history, logs, downloaded cloud artifacts, project files, or Xcode's global DerivedData. The preferences file also remembers the selected Xcode Cloud product and workflow per container; files written by earlier versions load unchanged.

## Development

```sh
make test
make check
make build
```

Tests use fake command runners, headless gocui instances, and `httptest` servers for the App Store Connect API, so they do not require an available simulator or network access.

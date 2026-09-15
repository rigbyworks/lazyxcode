# Xcode Cloud support plan

## Goal

Add a read-only Xcode Cloud mode to `lazyxcode` that lets a developer browse recent cloud builds without leaving the existing three-pane workflow.

The first release will support:

- Selecting an Xcode Cloud product and optionally filtering by workflow.
- Listing recent build runs with status, build number, branch, commit, and duration.
- Polling active runs and refreshing settled history on demand.
- Showing build actions, structured issues, and test results for the selected run.
- Showing and downloading artifacts, including build logs and `.xcresult` bundles when Apple exposes them for the run.
- Switching between local and cloud activity without stopping or losing local work.

Starting or cancelling cloud builds, editing workflows, and managing App Store Connect credentials interactively are not part of the first release. The App Store Connect integration should use read-only credentials and make no mutating API requests.

## Recommended UI

Use the screenshot's Local/Cloud tab concept rather than adding a fourth pane. The selected mode controls all three existing panes:

```text
 lazyxcode | Fortyfive.xcodeproj
╭─ [Local]  Cloud [1] ─────────╮╭─ Output [3] - CONCISE - #245 ─────────────╮
│  Container  Fortyfive...     ││ XCODE CLOUD BUILD                         │
│> Scheme     Fortyfive   [>]  ││ Workflow   Pull Request                   │
│  Target     iPhone ...  [>]  ││ Branch     feature/example                │
│                              ││ Commit     a1b2c3d                         │
│  [b] Build & run  [t] Test   ││                                            │
╰──────────────────────────────╯│ ACTIONS                                    │
╭─ Local Activity [2] ─────────╮│  ✓ Build and Analyze             2m14s    │
│OK     #001 iPhone ...  1m27s ││  ✗ Test                         11m03s    │
╰──────────────────────────────╯╰────────────────────────────────────────────╯
```

In Cloud mode:

```text
╭─ Local  [Cloud] [1] ─────────╮
│> Product   Fortyfive    [>]  │
│  Workflow  All          [>]  │
│                              │
│  Read-only  [r] Refresh      │
│  Updated 8s ago              │
╰──────────────────────────────╯
╭─ Cloud Activity [2] ─────────╮
│RUN    #245 Pull Request 3m12s│
│FAIL   #244 Main        12m08s│
│OK     #243 Release      8m31s│
╰──────────────────────────────╯
```

### Interaction

- Keep `1`, `2`, and `3` as pane-focus shortcuts.
- Add `m` to switch between Local and Cloud mode from any pane. The active tab is bracketed in the top-left title.
- Preserve separate selections, output scroll positions, and concise/raw preferences for each mode.
- In Local mode, retain all current keys and behavior.
- In Cloud mode, `Enter` selects Product or Workflow, `r` refreshes runs, `v` toggles structured details/raw logs, and `a` opens the artifact picker.
- In Cloud mode, do not route `b`, `t`, `x`, or `c` to local actions. Omit those actions from the contextual footer rather than allowing an invisible local mutation.
- Switching modes must not cancel local builds or stop their event processing.
- If Cloud is not configured, switching to it should show setup guidance in the Cloud and Output panes while Local remains fully usable.

The Activity pane should favor information that remains distinguishable at narrow widths. Use status, build number, workflow, and duration in the row; put branch and commit details in Output. Workflow names should truncate before status or duration.

## Design decision

Two designs are plausible.

### Option A: One generalized activity model

Replace `model.BuildRecord` with a provider-neutral activity interface and have local builds and cloud runs implement it. Activity and Output would render through shared methods.

Advantages:

- One list/rendering path.
- Makes a future mixed Local+Cloud activity feed straightforward.

Costs:

- Forces remote runs into local concepts such as `Simulator`, `DerivedDataKey`, `LogPath`, and process cancellation.
- Requires a history migration and a broad rewrite of the build manager, store, and rendering tests before any cloud data is visible.
- Either produces a shallow interface with many provider checks or hides too much provider-specific detail behind presentation methods.

### Option B: Parallel local and cloud state

Keep `BuildRecord`, the local manager, and local persistence intact. Add a distinct cloud model/client and let the TUI choose the active state and renderer based on mode.

Advantages:

- Preserves shipped local behavior and stored history.
- Keeps App Store Connect pagination, polling, relationships, and artifact downloads inside one cloud-specific module.
- Lets structured cloud details render directly instead of being converted into fake `xcodebuild` text.
- Gives tests a clear HTTP boundary without changing the local executor boundary.

Costs:

- Activity navigation and output selection have two small mode-specific branches.
- A future combined feed would require a later presentation-level abstraction.

Choose Option B. There is a real provider boundary but not yet a real requirement for a combined feed. Duplication should be limited to state selection and the top-level render dispatch; shared formatting helpers can still be reused where the data semantics match.

## Architecture

### `internal/xcodecloud`

Add a deep module that hides App Store Connect authentication, JSON:API wire types, relationship traversal, pagination, retries, and artifact download URLs.

Its TUI-facing contract should be approximately:

```go
type Service interface {
	ListProducts(context.Context) ([]Product, error)
	ListWorkflows(context.Context, string) ([]Workflow, error)
	ListBuildRuns(context.Context, BuildRunQuery) (BuildRunPage, error)
	GetBuildDetails(context.Context, string) (BuildDetails, error)
	DownloadArtifact(context.Context, Artifact, string) error
}
```

The concrete client should accept an `http.Client` and token source. The interface exists for the TUI test seam; App Store Connect response structs and URLs must not leak through it.

Suggested files:

- `internal/xcodecloud/auth.go`: credential loading and short-lived ES256 JWT creation.
- `internal/xcodecloud/client.go`: public client operations and request/error handling.
- `internal/xcodecloud/wire.go`: private JSON:API request/response types.
- `internal/xcodecloud/model.go`: product, workflow, run, action, issue, test result, and artifact domain types.
- `internal/xcodecloud/client_test.go`: `httptest.Server` contract and pagination tests.
- `internal/xcodecloud/auth_test.go`: key parsing and JWT claim/signature tests.

### API resources

Use the App Store Connect API relationships in this order:

1. List `ciProducts`.
2. List workflows for the selected product.
3. List build runs for the product, or for the selected workflow when a filter is active.
4. For the selected run, list its build actions.
5. For each action, list issues, test results where applicable, and artifact metadata.
6. Follow the artifact download URL only when raw logs are requested or the user explicitly downloads an artifact.

All collection handling must follow the API's returned pagination links. Do not construct cursors or assume that one page contains all actions, issues, tests, or artifacts.

`GetBuildDetails` should perform independent action-detail requests with a small concurrency bound and context cancellation. Selecting another run must cancel enrichment for the old selection. Preserve API ordering for actions and sort issues/tests only when the API does not provide a useful stable order.

### Authentication

Support App Store Connect team API keys in the first release:

- `LAZYXCODE_ASC_ISSUER_ID`
- `LAZYXCODE_ASC_KEY_ID`
- `LAZYXCODE_ASC_PRIVATE_KEY_PATH`

The `.p8` private key remains in the user-controlled file and is read only when creating the token source. Do not copy it into preferences, logs, errors, artifacts, or project state. Warn when the file is group/world readable.

Generate ES256 JWTs in memory with the App Store Connect audience and a short lifetime, refreshing before expiration. Use a dedicated least-privilege App Store Connect API key. Limit token scope to the GET routes used by this feature where Apple's current scope syntax permits it; verify the exact relationship-route scope behavior during the API spike before fixing the claim list. No POST, PATCH, or DELETE methods should exist on the client.

Use a maintained JWT implementation rather than hand-encoding ECDSA signatures. Authentication errors should distinguish missing configuration, unreadable/malformed key, expired/rejected token, and insufficient App Store Connect role without exposing credential contents.

Individual App Store Connect API keys and Keychain-backed private-key storage can be added later. Keeping credential loading behind a token-source interface prevents that addition from affecting the cloud API client or TUI.

### Cloud domain model

Keep cloud types separate from `model.BuildRecord`. At minimum, model:

```text
Product
  ID, Name

Workflow
  ID, Name, Disabled

BuildRun
  ID, Number, WorkflowID/Name
  ExecutionProgress, CompletionStatus, StartReason, CancelReason
  CreatedAt, StartedAt, FinishedAt
  SourceBranch, SourceCommit, DestinationBranch, DestinationCommit, IsPullRequest
  ErrorCount, WarningCount, TestFailureCount

BuildDetails
  Run
  Actions[]

Action
  ID, Name, Type, RequiredToPass
  ExecutionProgress, CompletionStatus, StartedAt, FinishedAt
  Issues[], TestResults[], Artifacts[]

Issue
  Type, Category, Message, File, Line, Column

TestResult
  ClassName, Name, Status, Message, File, Line
  Destinations[] with device, OS, status, duration

Artifact
  ID, Name, Type, Size, DownloadURL
```

Normalize Apple's execution/completion combinations into display-only cloud statuses such as `WAIT`, `RUN`, `OK`, `FAIL`, and `STOP`. Do not reuse `model.Phase.Active()` because local lifecycle phases such as boot/install/run have different meaning.

### TUI state

Add a `mode` field and a contained `cloudState` to `tui.App`. Leave the current local fields in place to avoid a broad refactor.

`cloudState` should own:

- Products, selected product, workflows, and selected workflow.
- Build runs, selected run, and the next-page link/cursor.
- Details and raw-log caches keyed by run ID.
- Independent loading/error state, last refresh time, output-follow/verbosity state, and request generation/cancellation.

Add `internal/tui/cloud.go` for cloud loading, selection, polling, and artifact actions. Keep `render.go` responsible for presentation, but dispatch early to focused `renderLocal...` and `renderCloud...` functions rather than scattering provider checks through every formatter.

Persist only the selected cloud product ID and workflow ID per local container in `preferences.json`. Additive fields should remain readable by the current preferences loader; bump the preferences version and test loading an existing version-1 file. If a stored product or workflow no longer exists, clear the stale selection and show the picker.

### Refresh and polling

- Load products the first time Cloud mode is opened.
- Auto-select a sole product; otherwise use the existing searchable overlay picker.
- Default Workflow to `All Workflows`; remember an explicit workflow filter.
- Fetch the newest 25 runs initially.
- Retain the next-page link and load another page when the user requests more. Do not discard already loaded rows during pagination.
- Poll every 15 seconds while any visible run is active.
- Poll every 60 seconds while Cloud mode is visible and all visible runs are settled.
- Stop cloud polling while Local mode is visible, but keep the last cloud state for instant switching.
- `r` performs an immediate first-page refresh and reconciles by run ID while preserving the selected run when it still exists.
- Never overlap refreshes for the same selection. Cancel requests when mode, product, workflow, or selected run changes.

API failures should leave the last successful data visible with a stale/error status. Use bounded retry with jitter only for transient transport errors, `429`, and `5xx`; respect `Retry-After`. Do not retry authentication or permission failures automatically.

## Output behavior

### Concise mode

Render structured API data instead of passing cloud content through the local transcript parser:

```text
XCODE CLOUD BUILD #245
Workflow       Pull Request
Source         feature/example @ a1b2c3d
Destination    main @ d4e5f6a
Started        Sep 4, 10:42 AM
Status         Failed after 11m03s

ACTIONS (3)
  ✓ Build and Analyze                    2m14s
  ✗ Test                                8m41s
  - Archive                              skipped

DIAGNOSTICS (2)
  error: LoginTests.swift:42:7 - XCTAssertTrue failed
  warning: App.swift:18:5 - value was never used

TESTS
  128 passed, 1 failed
  ✗ LoginTests.testInvalidLogin          1.3s

ARTIFACTS (3)
  Build logs                         1.8 MB
  TestResults.xcresult             142.0 MB
  App.xcarchive                    210.4 MB
```

Use ASCII `-` in stored/generated text and existing visual glyphs only where the current TUI already supports them. Deduplicate identical structured issues by severity, source location, and message.

### Raw mode

On the first `v` toggle for a selected run:

1. Find log artifacts across the run's actions.
2. Download them in the background into the managed cloud cache.
3. Show progress without blocking the UI.
4. Render textual logs with action headings when available.
5. If Apple's artifact is not directly renderable text, show its downloaded path and direct the user to the artifact action rather than treating binary/archive bytes as terminal output.

Cache raw content per run so repeated toggles do not redownload it during the process. A refresh that changes a running build's artifact metadata should invalidate only affected cached entries.

### Artifact picker

`a` opens the existing overlay UI with artifacts grouped/labeled by action. Enter downloads the selected artifact to:

```text
$XDG_CACHE_HOME/lazyxcode/projects/<project-hash>/cloud/<run-id>/artifacts/<safe-filename>
```

Fall back to `~/.cache` consistently with current cache behavior. Stream downloads to a temporary file, verify the received byte count when Apple provides a size, and rename atomically. Sanitize filenames and prevent path traversal. Existing files with matching metadata can be reused; incomplete temporary files must be removed after cancellation or failure.

The status bar should report the final path. Automatic opening/exporting is outside the first release.

## Error and empty states

Cloud mode needs explicit, nonfatal states for:

- Credentials not configured.
- Private key unreadable or malformed.
- Authentication rejected or API role insufficient.
- No Xcode Cloud products visible to the key.
- Product has no workflows.
- Workflow has no build runs.
- Selected run has no actions, issues, tests, logs, or artifacts.
- API rate limiting or transient unavailability with stale data retained.
- Artifact URL expired; refetch metadata once, then report failure.

Errors belong in the Cloud panel/status bar and selected Output view. Do not terminate the application for a cloud-only failure.

## Implementation phases

### Phase 0: API contract spike

- Capture sanitized fixtures for products, workflows, build runs, actions, issues, test results, and artifacts from a real read-only account.
- Verify sort/filter parameters, relationship pagination, status combinations, artifact types/content, download URL expiration behavior, and GET-only JWT scope syntax against the current App Store Connect API.
- Record any unavailable fields and adjust the domain model before TUI work begins.

Exit criterion: fixtures cover one active run, one successful run, one failed test run, pagination, no-artifact state, `401`, `403`, `429`, and `5xx`.

### Phase 1: Authentication and client

- Add credential loading, private-key validation, JWT caching/refresh, and redacted errors.
- Implement the read-only client and generic JSON:API pagination helper.
- Map wire responses into stable cloud domain models.
- Implement retry/rate-limit handling and cancellable streamed artifact downloads.
- Test exclusively through `httptest.Server` and fixture responses.

Exit criterion: a small non-TUI integration command/test can list products, runs, and selected-run details and can download an artifact without exposing secrets.

### Phase 2: Mode, configuration, and activity list

- Add Local/Cloud mode switching and contextual titles/footer.
- Add product/workflow pickers using the existing overlay pattern.
- Persist product/workflow IDs per container.
- Render recent cloud runs and preserve independent Local/Cloud selection state.
- Add manual refresh and pagination/load-more behavior.

Exit criterion: users can switch modes repeatedly while a local fake build continues, select cloud configuration, and browse fixture-backed runs in headless TUI tests.

### Phase 3: Details and polling

- Load actions and structured details when run selection changes.
- Render run metadata, diagnostics, test summaries/results, and artifact metadata.
- Add active/idle polling, reconciliation by run ID, request cancellation, and stale-data behavior.
- Ensure late responses from old product/workflow/run selections cannot overwrite current state.

Exit criterion: an active fixture run progresses to a terminal state without losing selection, and failed action/test details appear in concise Output.

### Phase 4: Logs, artifacts, and documentation

- Add lazy raw-log retrieval and the artifact download picker.
- Add safe managed cache paths, atomic downloads, size checks, and cancellation cleanup.
- Document App Store Connect key creation expectations, required environment variables, least-privilege guidance, controls, cache location, and API limitations in `README.md`.
- Update help text and the navigation table.

Exit criterion: logs render when textual, arbitrary artifacts download safely, `.xcresult` bundles are retained for the planned result explorer, and the complete documented setup works from a clean shell.

## Test strategy

### Client tests

- Parse EC private keys and reject wrong key types or malformed PEM.
- Verify JWT algorithm, audience, issuer, key ID, lifetime, refresh behavior, and route scopes without logging the token.
- Verify authorization headers and GET-only requests.
- Follow multi-page links and handle missing/optional relationships.
- Map every known execution/completion status without panicking on unknown future values.
- Handle malformed JSON, Apple error documents, cancellation, timeouts, `401`, `403`, `429` with `Retry-After`, and `5xx`.
- Stream and atomically finalize artifact downloads; reject unsafe filenames and clean partial files.

### Store tests

- Load version-1 preferences with empty cloud maps.
- Round-trip product/workflow selections independently per container.
- Produce safe cloud cache paths for hostile run IDs and filenames.
- Leave local build history and DerivedData clearing behavior unchanged.

### TUI tests

- Render the selected Local/Cloud tab and contextual footer at wide, narrow, and minimum supported sizes.
- Preserve local and cloud selections independently across mode switches.
- Prove that cloud keys cannot invoke local build/test/stop/cache actions.
- Auto-select a sole product and open a picker for multiple products/workflows.
- Render active, success, failure, cancelled, empty, loading, stale, and permission-error states.
- Cancel old detail requests and ignore late responses after selection changes.
- Reconcile polled runs without duplicate rows or unexpected cursor jumps.
- Render structured diagnostics/test results and lazy raw-log states.
- Exercise artifact picker filtering, download progress, success, cancellation, and failure.

### Verification commands

Run the existing repository checks after every phase:

```sh
make test
make check
```

Before release, perform one manual read-only smoke test against App Store Connect and verify local build/test behavior with no cloud environment variables set.

## Acceptance criteria

- Existing users who do not configure Xcode Cloud see no startup failure or local workflow regression.
- `m` switches the three panes between Local and Cloud, and switching never interrupts a local activity.
- A configured user can choose a product and workflow and see recent runs ordered newest first.
- Active cloud runs update without manual reload and settle to the correct terminal result.
- Selected runs show branch/commit metadata, actions, structured issues, tests, and available artifacts.
- Raw logs and artifacts are downloaded only on demand, without loading complete artifacts into memory.
- Credentials and JWTs never enter preferences, state files, logs, status text, or test snapshots.
- Cloud API errors remain confined to Cloud mode and preserve last-known successful data.
- App Store Connect requests are read-only and use least-privilege, short-lived authorization.
- `make check` passes, including race tests.

## Deferred follow-ups

- Start and cancel Xcode Cloud builds after a separate mutation/confirmation design.
- Keychain-backed private keys and individual App Store Connect API key support.
- Branch, pull-request, status, and date filters.
- Opening App Store Connect build pages in a browser.
- Offline persistence of cloud run metadata.
- A combined Local+Cloud activity feed.
- Structured `.xcresult` browsing shared by local and downloaded cloud results.
- Artifact retention limits and an explicit cloud-cache cleanup UI.

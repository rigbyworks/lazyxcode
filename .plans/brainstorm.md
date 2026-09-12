# Additional function ideas

The ideas below build on the current scheme, simulator, build, test, launch, log, and DerivedData workflows. Command references were checked against the Apple tools included with Xcode 26.6.

## Strong next additions

### Result bundle explorer

Save an `.xcresult` for every build and test with `xcodebuild -resultBundlePath`, then add a structured result view beside the raw log. It could show build issues, failed tests, failure messages, destinations, attachments, and available diagnostics without making the user search the transcript.

Apple tools: `xcresulttool get build-results`, `xcresulttool get test-results`, `xcresulttool get log`, `xcresulttool get content-availability`, and `xcresulttool export`.

### Rerun failed tests

Extract failed test identifiers from the selected result bundle and offer "Rerun failures" as an activity action. Pass the identifiers back through `-only-testing:` so a large suite does not need to run again.

Apple tools: `xcresulttool get test-results` and `xcodebuild -only-testing:`.

### Test case picker

Expand the existing unit/UI target picker into a suite and test-case tree. Xcode can enumerate the tests for a scheme or test plan as JSON without running them, which avoids parsing source files or relying on naming conventions.

Apple tools: `xcodebuild -enumerate-tests -test-enumeration-style hierarchical -test-enumeration-format json` and `-only-testing:`.

### Flaky-test and stress modes

Offer presets such as "Retry failures up to 3 times," "Run 20 times," and "Run until failure." Show each iteration and preserve the first failing result. A process-relaunch toggle would help expose state leakage between runs.

Apple tools: `xcodebuild -retry-tests-on-failure`, `-test-iterations`, `-run-tests-until-failure`, and `-test-repetition-relaunch-enabled`.

### Code coverage browser

Add an opt-in coverage run, then show target, file, and function coverage in the TUI. Keep a selected successful run as a baseline and highlight coverage gains and regressions in later runs.

Apple tools: `xcodebuild -enableCodeCoverage YES`, `xccov view`, and `xccov diff`.

### Build settings inspector

Provide a searchable view of resolved build settings for the selected scheme, configuration, SDK, and destination. Add a diff mode between Debug and Release or between two schemes. This would make signing, deployment-target, search-path, and compiler-flag problems much easier to diagnose.

Apple tools: `xcodebuild -showBuildSettings -json` and `-configuration`.

### Swift package status and resolution

Show the packages pinned in `Package.resolved`, resolve dependencies on demand, and display dependency-resolution output as its own activity. Include a strict mode that refuses versions not already recorded in `Package.resolved`, which is useful for reproducing CI locally.

Apple tools: `xcodebuild -resolvePackageDependencies`, `-onlyUsePackageVersionsFromResolvedFile`, `-skipPackageUpdates`, and `-clonedSourcePackagesDirPath`.

### Simulator action menu

Add contextual actions for boot, shutdown, reboot, erase, clone, rename, create, and delete. Erase and delete should require confirmation and should be unavailable while an activity uses that simulator.

Apple tools: `xcrun simctl boot`, `shutdown`, `reboot`, `erase`, `clone`, `rename`, `create`, and `delete`.

### App lifecycle controls

Once an app has been built, allow install-only, launch, terminate, uninstall, and reinstall without rebuilding. Show installed app metadata and offer shortcuts to its data, group, and logs containers.

Apple tools: `xcrun simctl install`, `launch`, `terminate`, `uninstall`, `appinfo`, `listapps`, and `get_app_container`.

### Live app logs

Add a retained activity that streams unified logs for the selected app and simulator. Useful filters would include process, subsystem, category, and log level. A separate button could collect a simulator diagnostic archive when ordinary logs are not enough.

Apple tools: `xcrun simctl spawn <device> log stream` and `xcrun simctl diagnose`.

### Screenshot and screen recording

Capture simulator screenshots or start and stop a screen recording from the TUI. Store the files with the corresponding activity and display their paths in the output pane. A "clean status bar" preset would produce consistent screenshots for reviews and App Store material.

Apple tools: `xcrun simctl io screenshot`, `xcrun simctl io recordVideo`, and `xcrun simctl status_bar override`.

### Deep-link and push testing

Keep named launch scenarios for URLs and APNs payload files. The user could choose a scenario, launch the app if needed, then send the URL or push notification to the selected simulator.

Apple tools: `xcrun simctl openurl` and `xcrun simctl push`.

### Permission presets

Create reusable permission states such as "fresh install," "all denied," "location granted," or "photos granted." Warn that granting permissions directly can hide missing usage descriptions or broken request flows.

Apple tools: `xcrun simctl privacy grant`, `revoke`, and `reset`.

### Location scenarios

Save common coordinates and routes, then start or stop simulated location changes from the selected simulator. This would pair well with test presets for geofencing, mapping, and region-dependent app behavior.

Apple tools: `xcrun simctl location`.

## Testing and diagnostics

### Build-for-testing cache

Split compilation from execution with a "Prepare tests" action and a faster "Run prepared tests" action. This is useful when repeatedly changing simulator state or test filters without changing code.

Apple tools: `xcodebuild build-for-testing`, `test-without-building`, `-xctestrun`, and `-testProductsPath`.

### Test matrix runner

Select several compatible simulators and run one test request across all of them. Summarize outcomes by OS and device, with controls for test parallelism and maximum concurrent destinations.

Apple tools: repeated `xcodebuild -destination` arguments, `-parallel-testing-enabled`, `-parallel-testing-worker-count`, and `-maximum-concurrent-test-simulator-destinations`.

### Test plan and configuration picker

Discover test plans and let the user select a plan, configuration, language, and region before starting a run. This would cover localization and configuration variants without creating more schemes.

Apple tools: `xcodebuild -showTestPlans`, `-testPlan`, `-only-test-configuration`, `-testLanguage`, and `-testRegion`.

### Sanitizer build presets

Add Address Sanitizer, Thread Sanitizer, and Undefined Behavior Sanitizer toggles to temporary build profiles. Make the active sanitizer visible in activity history so results are not confused with ordinary runs.

Apple tools: `xcodebuild -enableAddressSanitizer`, `-enableThreadSanitizer`, and `-enableUndefinedBehaviorSanitizer`.

### Static analysis

Run the scheme's `analyze` action and present analyzer issues separately from compiler warnings. Compare the result against a chosen baseline to show newly introduced or resolved issues.

Apple tools: `xcodebuild analyze`, `xcresulttool get build-results`, and `xcresulttool compare --analyzer-issues`.

### Performance-test artifacts

Enable diagnostics for performance XCTest runs and export measurements as CSV. Keep recent measurements per test so regressions are visible without opening Instruments.

Apple tools: `xcodebuild -enablePerformanceTestsDiagnostics YES` and `xcresulttool export metrics`.

### Instruments recording presets

Let the user launch Time Profiler, Allocations, Leaks, or another installed Instruments template against the built app. Save `.trace` files alongside activity logs and provide export or symbolication actions.

Apple tools: `xcrun xctrace list templates`, `xctrace record`, `xctrace export`, and `xctrace symbolicate`.

### Result comparison

Choose any retained `.xcresult` as a baseline and compare it with another activity. Summarize test additions and removals, changed failures, build warnings, and analyzer issues.

Apple tools: `xcresulttool compare --summary --tests --test-failures --build-warnings --analyzer-issues`.

### Fixture manager

Install known app-data snapshots, seed photos, videos, contacts, trusted certificates, and pasteboard content. A project-local fixture manifest could make simulator setup repeatable for developers and UI tests.

Apple tools: `xcrun simctl install_app_data`, `addmedia`, `keychain`, `pbcopy`, `pbpaste`, and `pbsync`.

## Device and release workflows

### Physical-device destination support

Include connected iPhones and iPads in the destination picker. Build for the selected device, install the app, launch it with arguments and environment variables, terminate it, and open URLs. Use `devicectl` JSON output rather than parsing its human-readable output.

Apple tools: `xcrun devicectl list devices --json-output`, `devicectl device install app`, and `devicectl device process launch`, `terminate`, and `openURL`.

### Physical-device diagnostics

Add device screenshot or video capture, sysdiagnose collection, process memory warnings, device information, and file-copy actions. These should live in a separate device menu because support varies by device state and developer mode.

Apple tools: `xcrun devicectl device capture`, `sysdiagnose`, `info`, `copy`, and `process sendMemoryWarning`.

### Archive and export pipeline

Run an archive as a retained activity, list generated `.xcarchive` bundles, and export an IPA using a selected export-options plist. Show signing and export errors in concise form. Upload actions should remain an explicit follow-up rather than happening automatically.

Apple tools: `xcodebuild archive`, `-archivePath`, `-exportArchive`, `-exportOptionsPlist`, and `-exportPath`.

### Signing and entitlements inspector

Show the team, signing identity, provisioning profile, bundle identifier, and entitlements for the selected product. Compare requested entitlements with the signed app and flag obvious mismatches before a device install or archive export.

Apple tools: `xcodebuild -showBuildSettings -json`, `codesign -d --entitlements :-`, and `security find-identity -v -p codesigning`.

### Localization import and export

Export XLIFF files for selected languages, optionally including screenshots, then import translated files with a preview and confirmation. Record each operation so import failures remain inspectable.

Apple tools: `xcodebuild -exportLocalizations` and `-importLocalizations`.

### SDK and simulator runtime manager

Show installed SDKs and runtimes, identify unavailable destinations, and offer explicit download or import actions. Downloads are large and mutate the Xcode installation, so they should show size when available and require confirmation.

Apple tools: `xcodebuild -showsdks -json`, `-downloadPlatform`, `-importPlatform`, and `xcrun simctl runtime`.

### XCFramework builder

Provide a guided workflow for selecting built frameworks or libraries for several platforms and combining them into an `.xcframework`. Validate duplicate architectures and missing headers before invoking the command.

Apple tools: `xcodebuild -create-xcframework`.

## Smaller quality-of-life ideas

- Add named run profiles for launch arguments and environment variables through `simctl launch` or `devicectl device process launch`.
- Open a built app's documents, data, or app-group container in Finder using `simctl get_app_container`.
- Toggle simulator appearance, content size, and interface options through `simctl ui`.
- Trigger biometric success or failure on a connected device through `devicectl device simulate biometrics`.
- Add a one-key iCloud synchronization trigger through `simctl icloud_sync`.
- Show and control the simulator pasteboard through `simctl pbcopy` and `pbpaste`.
- Export failed-test screenshots, attachments, and diagnostics through `xcresulttool export attachments` and `export diagnostics`.
- Diff build warnings or test failures against the previous successful activity rather than requiring a manually selected baseline.
- Add a command preview that shows the exact `xcodebuild`, `simctl`, `devicectl`, `xcresulttool`, `xccov`, or `xctrace` invocation before it runs.

## Suggested order

1. Persist `.xcresult` bundles and add the result bundle explorer.
2. Add rerun-failed-tests, test enumeration, and coverage views on top of those bundles.
3. Add simulator app controls, logs, screenshots, deep links, pushes, permissions, and fixtures.
4. Add build settings, package resolution, sanitizers, and static analysis.
5. Add physical-device support after the activity model can represent destinations other than simulators.
6. Add archives, exports, Instruments traces, runtime downloads, and other heavier workflows last.

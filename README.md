# lazy-xcode

`lazy-xcode` is a keyboard-first terminal interface for building and running Xcode schemes without keeping Xcode open. It discovers the current directory's Xcode containers, shared schemes, and scheme-compatible installed simulators, then streams each build into its own retained log.

## Requirements

- macOS with Xcode and the command-line tools installed
- Go 1.25 or newer when building from source

## Install

```sh
make install
```

Run `lazy-xcode` from a directory containing a top-level `.xcworkspace` or `.xcodeproj`. When more than one is present, workspaces are listed first in a startup picker and the selection is remembered.

## Navigation

| Key | Action |
| --- | --- |
| `1`, `2`, `3` | Focus Build, Builds, or Output |
| `Tab`, `Shift-Tab` | Cycle panes |
| `Enter` | Select a scheme or simulator |
| `b` | Start a build |
| `x` | Cancel the selected active build |
| `c` | Clear managed DerivedData |
| `r` | Reload schemes and simulators |
| `v` | Toggle concise or raw build output |
| `j`, `k`, arrows | Navigate or scroll |
| `g`, `G` | First/last build or top/follow output |
| `?` | Show help |
| `q`, `Ctrl-C` | Quit |

Distinct scheme/simulator pairs can build concurrently. Successful builds boot the selected device, open Simulator.app, install the generated app, and launch it. Starting a duplicate pair while it is active is intentionally rejected because that pair shares an incremental DerivedData cache.

The output pane is concise by default. It shows live, wall-clock build phases and a deduplicated list of warnings and errors using compact `file:line:column — message` entries. Press `v` to inspect the complete raw `xcodebuild` transcript and Xcode's detailed command timing summary; persisted logs retain both the raw output and phase timings.

The layout adapts to smaller terminal panes. Narrow panes keep the build controls and output side by side, while short panes collapse the unfocused Build or Builds section to its title and expand the focused section.

## State and cache

The newest 100 build records and their complete logs are stored below `$XDG_STATE_HOME/lazy-xcode` or `~/.local/state/lazy-xcode`. Incremental DerivedData is isolated per project, scheme, and simulator below `$XDG_CACHE_HOME/lazy-xcode` or `~/.cache/lazy-xcode`.

The cache action deletes only DerivedData managed by `lazy-xcode`; it does not delete build history, logs, project files, or Xcode's global DerivedData.

## Development

```sh
make test
make check
make build
```

Tests use fake command runners and headless gocui instances, so they do not require an available simulator.

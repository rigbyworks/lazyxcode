package tui

import (
	"fmt"
	"strings"

	"github.com/jesseduffield/gocui"
)

type uiAction struct {
	key   string
	label string
	run   func(*gocui.Gui, *gocui.View) error
}

func (a *App) availableActions() []uiAction {
	var actions []uiAction
	if a.mode == modeCloud {
		actions = []uiAction{
			{"Enter", "Inspect selected test results", a.openCloudTestActivity},
			{"r", "Refresh Cloud builds", a.refreshCloud},
			{"a", "Download artifact", a.openCloudArtifactPicker},
			{"L", "Load older builds", a.loadOlderCloudRuns},
			{"v", "Toggle raw logs", a.toggleCloudVerbosity},
			{"m", "Switch to Local", a.toggleMode},
		}
		if a.cloud != nil && a.cloud.download != nil {
			actions = append(actions, uiAction{"x", "Cancel artifact download", a.cancelCloudDownload})
		}
	} else {
		actions = []uiAction{
			{"b", "Build", a.startBuild},
			{"r", "Build and run", a.startRun},
			{"t", "Run tests / coverage settings", a.openTestPicker},
			{"Enter", "Inspect selected test results", a.openTestActivity},
			{"v", "Toggle raw logs", a.toggleOutputVerbosity},
			{"c", "Clear managed DerivedData", a.confirmClearCache},
			{"R", "Reload schemes and targets", a.reload},
			{"m", "Switch to Cloud", a.toggleMode},
		}
		if a.manager != nil && a.manager.HasActive() {
			actions = append(actions, uiAction{"x", "Stop selected activity", a.stopBuild})
		}
	}
	actions = append(actions,
		uiAction{"y", "Copy output", a.copyOutput},
		uiAction{"i", "Status and connection details", a.showContextDetails},
		uiAction{"o", "Open project in Xcode", a.openInXcode},
		uiAction{"1", "Focus configuration", a.focusView("build")},
		uiAction{"2", "Focus activity", a.focusView("builds")},
		uiAction{"3", "Focus output", a.focusView("output")},
		uiAction{"?", "Keyboard help", a.showHelp},
		uiAction{"q", "Quit", a.requestQuit},
	)
	if a.mode == modeCloud {
		selected := false
		if a.cloud != nil {
			_, selected = a.cloud.selectedRun()
		}
		if !selected {
			filtered := actions[:0]
			for _, action := range actions {
				if action.key != "Enter" && action.key != "a" && action.key != "v" {
					filtered = append(filtered, action)
				}
			}
			actions = filtered
		}
	}
	return actions
}

func (a *App) showActions(*gocui.Gui, *gocui.View) error {
	title := "Local Actions"
	if a.mode == modeCloud {
		title = "Cloud Actions"
	}
	menu := &overlayState{kind: "actions", title: title}
	for _, action := range a.availableActions() {
		menu.items = append(menu.items, overlayItem{ID: action.key, Label: fmt.Sprintf("%-7s %s", "["+action.key+"]", action.label)})
	}
	a.overlay = menu
	return nil
}

func (a *App) cloudConnectionLabel() string {
	c := a.cloud
	switch {
	case c == nil || c.setupErr != nil || c.service == nil:
		return "Setup required"
	case c.err != "":
		return "! Refresh failed"
	case len(c.warnings) > 0:
		if len(c.warnings) == 1 {
			return "! 1 warning"
		}
		return fmt.Sprintf("! %d warnings", len(c.warnings))
	case c.loading != "":
		return "Connecting..."
	case c.download != nil:
		return "Downloading..."
	default:
		return "Connected"
	}
}

func (a *App) showContextDetails(*gocui.Gui, *gocui.View) error {
	var b strings.Builder
	title := "Status & Details"
	if a.mode == modeCloud && a.cloud != nil {
		if len(a.cloud.warnings) > 0 {
			fmt.Fprintf(&b, "WARNINGS\n\n%s\n\n", strings.Join(a.cloud.warnings, "\n\n"))
		}
		if a.cloud.err != "" {
			fmt.Fprintf(&b, "REFRESH ERROR\n\n%s\n\n", a.cloud.err)
		}
	}
	if a.status != "" {
		fmt.Fprintf(&b, "STATUS\n\n%s\n\n", a.status)
	}
	fmt.Fprintf(&b, "PROJECT\n\n%s\n", a.container.Path)
	if a.mode == modeCloud {
		title = "Cloud Connection"
		c := a.cloud
		fmt.Fprintf(&b, "\nCONNECTION\n\n%s\n", a.cloudConnectionLabel())
		if c == nil || c.setupErr != nil || c.service == nil {
			fmt.Fprintf(&b, "\n%s\n", cloudSetupGuidance(cloudSetupError(c)))
		} else {
			if product, ok := c.selectedProduct(); ok {
				fmt.Fprintf(&b, "Product: %s\n", product.Name)
			}
			workflow := "All Workflows"
			if selected, ok := c.selectedWorkflow(); ok {
				workflow = selected.Name
			}
			fmt.Fprintf(&b, "Workflow: %s\nAccess: read-only\nAuto-refresh: every %s\n", workflow, formatPollInterval(c.pollInterval()))
			if !c.lastRefresh.IsZero() {
				fmt.Fprintf(&b, "Last refreshed: %s\n", c.lastRefresh.Local().Format("Jan 2, 15:04:05"))
			}
			if c.download != nil {
				fmt.Fprintf(&b, "\nDOWNLOAD\n\n%s\n%s / %s\n", c.download.artifact.Name, formatBytes(c.download.written), formatBytes(c.download.artifact.Size))
			}
		}
	} else {
		if a.scheme >= 0 && a.scheme < len(a.schemes) {
			fmt.Fprintf(&b, "\nScheme: %s\n", a.schemes[a.scheme])
		}
		if a.simulator >= 0 && a.simulator < len(a.sims) {
			fmt.Fprintf(&b, "Target: %s\n", a.sims[a.simulator].Label())
		}
		fmt.Fprintf(&b, "Managed build cache: %s\n", formatBytes(a.cacheSize))
	}
	fmt.Fprintln(&b, "\nScroll with j/k or Page Up/Down. Esc to close.")
	a.overlay = &overlayState{kind: "help", title: title, message: b.String()}
	return nil
}

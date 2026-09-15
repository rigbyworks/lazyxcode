package tui

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/jesseduffield/gocui"
	buildmanager "github.com/rigbyworks/lazyxcode/internal/build"
	"github.com/rigbyworks/lazyxcode/internal/model"
)

type binding struct {
	view string
	key  any
	fn   func(*gocui.Gui, *gocui.View) error
}

func (a *App) bindKeys(g *gocui.Gui) error {
	var bindings []binding
	for _, view := range []string{"build", "builds", "output"} {
		bindings = append(bindings,
			binding{view, gocui.KeyTab, a.cycleFocus(1)},
			binding{view, gocui.KeyBacktab, a.cycleFocus(-1)},
			binding{view, 'b', a.byMode(a.startBuild, nil)},
			binding{view, 'r', a.byMode(a.startRun, a.refreshCloud)},
			binding{view, 'R', a.byMode(a.reload, nil)},
			binding{view, 't', a.byMode(a.openTestPicker, nil)},
			binding{view, 'x', a.byMode(a.stopBuild, a.cancelCloudDownload)},
			binding{view, 'c', a.byMode(a.confirmClearCache, nil)},
			binding{view, 'L', a.byMode(nil, a.loadOlderCloudRuns)},
			binding{view, 'v', a.byMode(a.toggleOutputVerbosity, a.toggleCloudVerbosity)},
			binding{view, 'y', a.copyOutput},
			binding{view, 'o', a.openInXcode},
			binding{view, 'a', a.byMode(nil, a.openCloudArtifactPicker)},
			binding{view, 'm', a.toggleMode},
			binding{view, ':', a.showActions},
			binding{view, 'i', a.showContextDetails},
			binding{view, '?', a.showHelp},
			binding{view, 'q', a.requestQuit},
			binding{view, gocui.KeyCtrlC, a.requestQuit},
			binding{view, '1', a.focusView("build")},
			binding{view, '2', a.focusView("builds")},
			binding{view, '3', a.focusView("output")},
		)
	}
	bindings = append(bindings,
		binding{"builds", gocui.KeyEnter, a.byMode(a.openTestActivity, a.openCloudTestActivity)},
		binding{"output", gocui.KeyEnter, a.openTestOutputActions},
		binding{"output", gocui.KeyEsc, a.closeTestOutput},
		binding{"builds", gocui.KeyEsc, a.closeTestOutput},
		binding{"build", 'j', a.moveConfig(1)}, binding{"build", gocui.KeyArrowDown, a.moveConfig(1)},
		binding{"build", 'k', a.moveConfig(-1)}, binding{"build", gocui.KeyArrowUp, a.moveConfig(-1)},
		binding{"build", gocui.KeyEnter, a.byMode(a.openConfigPicker, a.openCloudConfigPicker)},
		binding{"builds", 'j', a.moveBuild(1)}, binding{"builds", gocui.KeyArrowDown, a.moveBuild(1)},
		binding{"builds", 'k', a.moveBuild(-1)}, binding{"builds", gocui.KeyArrowUp, a.moveBuild(-1)},
		binding{"builds", 'g', a.moveBuildTo(false)}, binding{"builds", 'G', a.moveBuildTo(true)},
		binding{"output", 'j', a.scrollOutput(1)}, binding{"output", gocui.KeyArrowDown, a.scrollOutput(1)},
		binding{"output", 'k', a.scrollOutput(-1)}, binding{"output", gocui.KeyArrowUp, a.scrollOutput(-1)},
		binding{"output", gocui.KeyPgdn, a.scrollOutput(10)}, binding{"output", gocui.KeyPgup, a.scrollOutput(-10)},
		binding{"output", 'g', a.outputEnd(false)}, binding{"output", 'G', a.outputEnd(true)},
		binding{"filter", gocui.KeyArrowDown, a.moveOverlay(1)}, binding{"filter", gocui.KeyArrowUp, a.moveOverlay(-1)},
		binding{"filter", gocui.KeyEnter, a.chooseOverlay}, binding{"filter", gocui.KeyEsc, a.closeOverlay},
		binding{"filter", gocui.KeyCtrlC, a.requestQuit},
		binding{"overlay", gocui.KeyArrowDown, a.moveOverlay(1)}, binding{"overlay", gocui.KeyArrowUp, a.moveOverlay(-1)},
		binding{"overlay", 'j', a.moveOverlay(1)}, binding{"overlay", 'k', a.moveOverlay(-1)},
		binding{"overlay", gocui.KeyPgdn, a.moveOverlay(10)}, binding{"overlay", gocui.KeyPgup, a.moveOverlay(-10)},
		binding{"overlay", gocui.MouseWheelDown, a.moveOverlay(3)}, binding{"overlay", gocui.MouseWheelUp, a.moveOverlay(-3)},
		binding{"overlay", 'g', a.moveOverlayTo(false)}, binding{"overlay", 'G', a.moveOverlayTo(true)},
		binding{"overlay", gocui.KeyHome, a.moveOverlayTo(false)}, binding{"overlay", gocui.KeyEnd, a.moveOverlayTo(true)},
		binding{"overlay", gocui.KeyEnter, a.chooseOverlay}, binding{"overlay", gocui.KeyEsc, a.closeOverlay},
		binding{"overlay", 'q', a.closeOverlay}, binding{"overlay", gocui.KeyCtrlC, a.closeOverlay},
	)
	for _, item := range bindings {
		if err := g.SetKeybinding(item.view, item.key, gocui.ModNone, item.fn); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) focusView(name string) func(*gocui.Gui, *gocui.View) error {
	return func(g *gocui.Gui, _ *gocui.View) error {
		a.focus = name
		_, err := g.SetCurrentView(name)
		return err
	}
}

func (a *App) cycleFocus(delta int) func(*gocui.Gui, *gocui.View) error {
	return func(g *gocui.Gui, _ *gocui.View) error {
		order := []string{"build", "builds", "output"}
		index := indexOf(order, a.focus)
		if index < 0 {
			index = 0
		}
		index = (index + delta + len(order)) % len(order)
		a.focus = order[index]
		_, err := g.SetCurrentView(a.focus)
		return err
	}
}

func (a *App) moveConfig(delta int) func(*gocui.Gui, *gocui.View) error {
	return func(*gocui.Gui, *gocui.View) error {
		if a.mode == modeCloud {
			if a.cloud != nil {
				a.cloud.configRow = (a.cloud.configRow + delta + 3) % 3
			}
			return nil
		}
		a.configRow = (a.configRow + delta + 2) % 2
		return nil
	}
}

func (a *App) moveBuild(delta int) func(*gocui.Gui, *gocui.View) error {
	return func(*gocui.Gui, *gocui.View) error {
		if a.mode == modeCloud {
			a.moveCloudRun(delta)
			return nil
		}
		if len(a.records) == 0 {
			return nil
		}
		a.buildIndex = clamp(a.buildIndex+delta, 0, len(a.records)-1)
		a.outputFollow = true
		a.loadSelectedOutput()
		a.outputFollow = true
		return nil
	}
}

func (a *App) moveBuildTo(last bool) func(*gocui.Gui, *gocui.View) error {
	return func(*gocui.Gui, *gocui.View) error {
		if a.mode == modeCloud {
			a.moveCloudRunTo(last)
			return nil
		}
		if len(a.records) == 0 {
			return nil
		}
		a.buildIndex = 0
		if last {
			a.buildIndex = len(a.records) - 1
		}
		a.loadSelectedOutput()
		return nil
	}
}

func (a *App) openConfigPicker(*gocui.Gui, *gocui.View) error {
	if a.loading {
		return nil
	}
	if a.configRow == 0 {
		items := make([]overlayItem, len(a.schemes))
		for i, scheme := range a.schemes {
			items[i] = overlayItem{ID: scheme, Label: scheme}
		}
		a.overlay = &overlayState{kind: "scheme", title: "Select Scheme", items: items, selected: a.scheme}
	} else {
		a.overlay = &overlayState{kind: "simulator", title: "Select Target", items: targetItems(a.sims), selected: a.simulator}
		a.refreshTargets()
	}
	return nil
}

func (a *App) startBuild(*gocui.Gui, *gocui.View) error {
	return a.startBuildOrRun(model.OperationBuild)
}

func (a *App) startRun(*gocui.Gui, *gocui.View) error {
	return a.startBuildOrRun(model.OperationRun)
}

func (a *App) startBuildOrRun(operation model.Operation) error {
	if a.loading || a.manager == nil || len(a.schemes) == 0 || len(a.sims) == 0 {
		a.status = "Build and run are unavailable until discovery completes"
		return nil
	}
	record, err := a.manager.Start(a.ctx, buildmanager.Request{Container: a.container, Scheme: a.schemes[a.scheme], Simulator: a.sims[a.simulator], Operation: operation})
	if err != nil {
		a.status = err.Error()
		return nil
	}
	a.records = append([]model.BuildRecord{record}, a.records...)
	a.buildIndex = 0
	a.outputFollow = true
	a.outputs[record.ID] = ""
	a.status = "Queued " + string(operation) + " #" + shortID(record.ID)
	return nil
}

func (a *App) openTestPicker(*gocui.Gui, *gocui.View) error {
	if a.loading || a.manager == nil || len(a.schemes) == 0 || len(a.sims) == 0 {
		a.status = "Tests are unavailable until discovery completes"
		return nil
	}
	unit, ui := testTargetNames(a.testTargets, model.TestUnit), testTargetNames(a.testTargets, model.TestUI)
	items := []overlayItem{{ID: "all", Label: "All Configured Tests"}}
	if len(unit) > 0 {
		items = append(items, overlayItem{ID: "unit", Label: fmt.Sprintf("Unit Tests      %d targets", len(unit))})
	}
	if len(ui) > 0 {
		items = append(items, overlayItem{ID: "ui", Label: fmt.Sprintf("UI Tests        %d targets", len(ui))})
	}
	items = append(items, overlayItem{ID: "individual", Label: "Choose Individual Test..."})
	coverage := "On"
	if a.testCoverageOff {
		coverage = "Off"
	}
	items = append(items, overlayItem{ID: "coverage", Label: "Code Coverage: " + coverage})
	a.overlay = &overlayState{kind: "test-scope", title: "Run Tests", items: items}
	return nil
}

func (a *App) startTests(scope string) {
	if scope == "coverage" {
		a.testCoverageOff = !a.testCoverageOff
		if a.preferences != nil {
			if err := a.preferences.SetCoverageDisabled(a.container.Path, a.testCoverageOff); err != nil {
				a.status = "Save coverage preference: " + err.Error()
			}
		}
		_ = a.openTestPicker(nil, nil)
		return
	}
	if scope == "individual" {
		a.queueTestRequest(buildmanager.Request{Container: a.container, Scheme: a.schemes[a.scheme], Simulator: a.sims[a.simulator], Operation: model.OperationDiscoverTests, TestScope: "Discover Tests"})
		return
	}
	targets := a.testTargets
	label := "All Tests"
	if scope == "unit" {
		targets, label = filterTestTargets(a.testTargets, model.TestUnit), "Unit Tests"
	} else if scope == "ui" {
		targets, label = filterTestTargets(a.testTargets, model.TestUI), "UI Tests"
	}
	names := make([]string, len(targets))
	for i := range targets {
		names[i] = targets[i].Name
	}
	if scope == "all" {
		names = nil
	}
	a.queueTestRequest(buildmanager.Request{
		Container: a.container, Scheme: a.schemes[a.scheme], Simulator: a.sims[a.simulator],
		Operation: model.OperationTest, TestScope: label, Targets: names, Coverage: !a.testCoverageOff,
	})
}

func testTargetNames(targets []model.TestTarget, kind model.TestKind) []string {
	filtered := filterTestTargets(targets, kind)
	names := make([]string, len(filtered))
	for i := range filtered {
		names[i] = filtered[i].Name
	}
	return names
}

func filterTestTargets(targets []model.TestTarget, kind model.TestKind) []model.TestTarget {
	result := make([]model.TestTarget, 0, len(targets))
	for _, target := range targets {
		if target.Kind == kind {
			result = append(result, target)
		}
	}
	return result
}

func (a *App) stopBuild(*gocui.Gui, *gocui.View) error {
	if a.manager == nil || len(a.records) == 0 {
		return nil
	}
	record := a.records[a.buildIndex]
	if !record.Phase.Active() {
		a.status = "Selected activity is not active"
		return nil
	}
	if a.manager.Stop(record.ID) {
		a.status = "Stopping activity #" + shortID(record.ID) + "..."
	}
	return nil
}

func (a *App) reload(*gocui.Gui, *gocui.View) error {
	if a.manager != nil && a.manager.HasActive() {
		a.status = "Stop active activities before reloading project metadata"
		return nil
	}
	if a.container.Path != "" {
		a.chooseContainer(containerIndex(a.containers, a.container.Path))
	}
	return nil
}

func (a *App) toggleOutputVerbosity(g *gocui.Gui, _ *gocui.View) error {
	a.testOutput = nil
	a.verboseOutput = !a.verboseOutput
	mode := "Concise"
	if a.verboseOutput {
		mode = "Raw"
	}
	a.status = mode + " output"
	if output, err := g.View("output"); err == nil {
		output.SetOrigin(0, 0)
	}
	return nil
}

func (a *App) copyOutput(g *gocui.Gui, _ *gocui.View) error {
	view, err := g.View("output")
	if err != nil {
		a.status = "Output is unavailable"
		return nil
	}
	output := ansiPattern.ReplaceAllString(view.Buffer(), "")
	if strings.TrimSpace(output) == "" {
		a.status = "No output to copy"
		return nil
	}
	copy := a.copyToClipboard
	if copy == nil {
		copy = copyToClipboard
	}
	if err := copy(output); err != nil {
		a.status = "Copy output: " + err.Error()
		return nil
	}
	a.status = "Output copied to clipboard"
	return nil
}

func copyToClipboard(value string) error {
	command := exec.Command("pbcopy")
	command.Stdin = strings.NewReader(value)
	return command.Run()
}

func (a *App) openInXcode(*gocui.Gui, *gocui.View) error {
	if a.container.Path == "" {
		a.status = "No Xcode project or workspace selected"
		return nil
	}
	open := a.openXcode
	if open == nil {
		open = openXcode
	}
	if err := open(a.container.Path); err != nil {
		a.status = "Open in Xcode: " + err.Error()
		return nil
	}
	a.status = "Opened " + a.container.Name + " in Xcode"
	return nil
}

func openXcode(path string) error {
	return exec.Command("open", "-a", "Xcode", path).Run()
}

func (a *App) confirmClearCache(*gocui.Gui, *gocui.View) error {
	if a.project == nil {
		return nil
	}
	if a.manager != nil && a.manager.HasActive() {
		a.status = "Stop active activities before clearing the cache"
		return nil
	}
	a.overlay = &overlayState{
		kind: "confirm-cache", title: "Clear DerivedData?",
		message: "Delete only lazyxcode managed DerivedData.\nBuild history and logs will be kept.\n",
		items:   []overlayItem{{ID: "cancel", Label: "Cancel"}, {ID: "clear", Label: "Clear cache"}},
	}
	return nil
}

func (a *App) showHelp(*gocui.Gui, *gocui.View) error {
	a.overlay = &overlayState{kind: "help", title: "Help", message: strings.TrimSpace(`
Tab / Shift-Tab     Cycle panes
1 / 2 / 3           Focus Build, Activity, Output
m                    Switch between Local and Cloud mode
j / k, arrows       Navigate or scroll
g / G                First/last row or output position
v                    Toggle concise/raw output
y                    Copy displayed output to clipboard
o                    Open project or workspace in Xcode
:                    Search all actions
i                    Show status and connection details
?                    Show this help
q / Ctrl-C           Quit

Local mode
Enter                Select scheme/target or inspect test activity
b                    Build without launching
r                    Build and run
t                    Run all, unit, UI, or individual tests; coverage toggle
Enter in Output      Test actions and coverage comparison
Esc in Output        Return to the activity log
x                    Stop selected active activity
c                    Clear managed DerivedData
R                    Reload schemes and targets

Cloud mode (read-only)
Enter                Select product/workflow or browse test artifacts
r                    Refresh build runs
L                    Load older build runs
a                    Download an artifact
x                    Cancel the current download

Press Esc or q to close.`)}
	return nil
}

func (a *App) requestQuit(*gocui.Gui, *gocui.View) error {
	if a.manager != nil && a.manager.HasActive() {
		a.overlay = &overlayState{
			kind: "confirm-quit", title: "Active Activities",
			message: "Cancel all active builds and tests and quit?\n",
			items:   []overlayItem{{ID: "cancel", Label: "Keep working"}, {ID: "quit", Label: "Cancel activities and quit"}},
		}
		return nil
	}
	return gocui.ErrQuit
}

func (a *App) scrollOutput(delta int) func(*gocui.Gui, *gocui.View) error {
	return func(_ *gocui.Gui, view *gocui.View) error {
		a.outputFollow = false
		if delta > 0 {
			view.ScrollDown(delta)
		} else {
			view.ScrollUp(-delta)
		}
		return nil
	}
}

func (a *App) outputEnd(bottom bool) func(*gocui.Gui, *gocui.View) error {
	return func(_ *gocui.Gui, view *gocui.View) error {
		if !bottom {
			a.outputFollow = false
			view.SetOrigin(0, 0)
			return nil
		}
		scrollOutputToBottom(view)
		a.outputFollow = true
		return nil
	}
}

func scrollOutputToBottom(view *gocui.View) {
	_, height := view.InnerSize()
	view.SetOrigin(0, max(0, view.ViewLinesHeight()-height))
}

func clampOutputOrigin(view *gocui.View) {
	x, y := view.Origin()
	_, height := view.InnerSize()
	maximum := max(0, view.ViewLinesHeight()-height)
	view.SetOrigin(x, clamp(y, 0, maximum))
}

func (a *App) editFilter(view *gocui.View, key gocui.Key, ch rune, mod gocui.Modifier) bool {
	changed := gocui.DefaultEditor.Edit(view, key, ch, mod)
	if a.overlay != nil {
		a.overlay.filter = strings.TrimSpace(view.Buffer())
		a.overlay.selected = 0
	}
	return changed
}

func (a *App) moveOverlay(delta int) func(*gocui.Gui, *gocui.View) error {
	return func(_ *gocui.Gui, view *gocui.View) error {
		if a.overlay == nil {
			return nil
		}
		if a.overlay.kind == "help" {
			a.overlay.origin += delta
			clampOverlayOrigin(a.overlay, view)
			return nil
		}
		items := a.filteredOverlayItems()
		if len(items) > 0 {
			a.overlay.selected = clamp(a.overlay.selected+delta, 0, len(items)-1)
		}
		return nil
	}
}

func (a *App) moveOverlayTo(last bool) func(*gocui.Gui, *gocui.View) error {
	return func(_ *gocui.Gui, view *gocui.View) error {
		if a.overlay == nil {
			return nil
		}
		if a.overlay.kind == "help" {
			a.overlay.origin = 0
			if last && view != nil {
				_, height := view.InnerSize()
				a.overlay.origin = max(0, view.ViewLinesHeight()-height)
			}
			clampOverlayOrigin(a.overlay, view)
			return nil
		}
		items := a.filteredOverlayItems()
		a.overlay.selected = 0
		if last && len(items) > 0 {
			a.overlay.selected = len(items) - 1
		}
		return nil
	}
}

func clampOverlayOrigin(overlay *overlayState, view *gocui.View) {
	if overlay == nil || view == nil {
		return
	}
	_, height := view.InnerSize()
	overlay.origin = clamp(overlay.origin, 0, max(0, view.ViewLinesHeight()-height))
	view.SetOrigin(0, overlay.origin)
}

func (a *App) chooseOverlay(g *gocui.Gui, _ *gocui.View) error {
	if a.overlay == nil {
		return nil
	}
	items := a.filteredOverlayItems()
	if len(items) == 0 || a.overlay.selected >= len(items) {
		if a.overlay.kind == "help" {
			a.overlay = nil
		}
		return nil
	}
	item := items[a.overlay.selected]
	if a.overlay.choose != nil {
		choose := a.overlay.choose
		a.overlay = nil
		choose(item.ID)
		return nil
	}
	kind := a.overlay.kind
	a.overlay = nil
	switch kind {
	case "actions":
		for _, action := range a.availableActions() {
			if action.key == item.ID {
				return action.run(g, nil)
			}
		}
	case "container":
		a.chooseContainer(containerIndex(a.containers, item.ID))
	case "scheme":
		a.scheme = indexOf(a.schemes, item.ID)
		_ = a.preferences.SetScheme(a.container.Path, item.ID)
		a.loadSimulators()
	case "simulator":
		a.simulator = simulatorIndex(a.sims, item.ID)
		_ = a.preferences.SetSimulator(a.container.Path, a.schemes[a.scheme], item.ID)
		a.status = "Selected " + a.sims[a.simulator].Label()
	case "test-scope":
		a.startTests(item.ID)
	case "cloud-product":
		if a.cloud != nil {
			a.selectCloudProduct(cloudProductIndex(a.cloud.products, item.ID))
		}
	case "cloud-workflow":
		a.selectCloudWorkflow(item.ID)
	case "cloud-artifact":
		if a.cloud != nil {
			a.downloadSelectedCloudArtifact(item.ID)
		}
	case "confirm-cache":
		if item.ID == "clear" {
			if err := a.project.ClearCache(); err != nil {
				a.status = "Clear cache: " + err.Error()
			} else {
				a.cacheSize = 0
				a.status = "Managed DerivedData cleared"
			}
		}
	case "confirm-quit":
		if item.ID == "quit" {
			if a.manager != nil {
				a.manager.CancelAll()
			}
			return gocui.ErrQuit
		}
	}
	return nil
}

func (a *App) closeOverlay(*gocui.Gui, *gocui.View) error {
	if a.overlay != nil && a.overlay.kind == "container" && a.container.Path == "" {
		return nil
	}
	if a.overlay != nil {
		if a.overlay.cancel != nil {
			a.overlay.cancel()
		}
		a.overlay = a.overlay.parent
	}
	return nil
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}

func containerIndex(containers []model.Container, path string) int {
	for i := range containers {
		if containers[i].Path == path {
			return i
		}
	}
	return 0
}

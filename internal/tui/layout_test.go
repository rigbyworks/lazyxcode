package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jesseduffield/gocui"
	buildmanager "github.com/mwahlig/lazy-xcode/internal/build"
	"github.com/mwahlig/lazy-xcode/internal/model"
)

func TestHeadlessLayoutMatchesThreePanePlan(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: 120, Height: 36})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{
		gui: g, focus: "build", container: model.Container{Name: "App.xcworkspace"},
		schemes: []string{"App"}, sims: []model.Simulator{{ID: "PHONE", Name: "iPhone 17 Pro", OS: "iOS 26.0"}},
		records: []model.BuildRecord{{ID: "123-001", Scheme: "App", Simulator: model.Simulator{Name: "iPhone 17 Pro"}, Phase: model.PhaseBuilding, StartedAt: time.Now()}},
		outputs: map[string]string{"123-001": "[lazy-xcode] Building App for iPhone 17 Pro\nCompileSwift App.swift\n"}, outputFollow: true,
	}
	if err := a.layout(g); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"header", "build", "builds", "output", "status"} {
		if _, err := g.View(name); err != nil {
			t.Fatalf("missing %s view: %v", name, err)
		}
	}
	buildView, _ := g.View("build")
	if !strings.Contains(buildView.Buffer(), "App") || !strings.Contains(buildView.Buffer(), "iPhone 17 Pro") {
		t.Fatalf("build pane = %q", buildView.Buffer())
	}
	output, _ := g.View("output")
	if !strings.Contains(output.Buffer(), "[lazy-xcode] Building") || strings.Contains(output.Buffer(), "CompileSwift") || !strings.Contains(output.Title, "FOLLOW") {
		t.Fatalf("output pane/title = %q / %q", output.Buffer(), output.Title)
	}
}

func TestSmallTerminalShowsGuard(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, Headless: true, Width: 43, Height: 9})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{gui: g, focus: "build", outputs: map[string]string{}}
	if err := a.layout(g); err != nil {
		t.Fatal(err)
	}
	guard, err := g.View("guard")
	if err != nil || !strings.Contains(guard.Buffer(), "44x10") {
		t.Fatalf("guard = %q, %v", guard.Buffer(), err)
	}
}

func TestNarrowTerminalUsesResponsivePanels(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: 74, Height: 23})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{gui: g, focus: "build", outputs: map[string]string{}}
	if err := a.layout(g); err != nil {
		t.Fatal(err)
	}
	if guard, err := g.View("guard"); err == nil && guard.Visible {
		t.Fatal("74x23 terminal unexpectedly shows the size guard")
	}
	build, _ := g.View("build")
	output, _ := g.View("output")
	_, _, buildRight, _ := build.Dimensions()
	outputLeft, _, _, _ := output.Dimensions()
	if buildRight >= outputLeft {
		t.Fatal("responsive side and output panels overlap")
	}
}

func TestShortTerminalCollapsesUnfocusedSidePanel(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: 60, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{gui: g, focus: "build", outputs: map[string]string{}}
	if err := a.layout(g); err != nil {
		t.Fatal(err)
	}
	builds, _ := g.View("builds")
	if _, height := builds.InnerSize(); height != 0 {
		t.Fatalf("unfocused builds panel inner height = %d, want collapsed", height)
	}
	a.focus = "builds"
	if err := a.layout(g); err != nil {
		t.Fatal(err)
	}
	build, _ := g.View("build")
	builds, _ = g.View("builds")
	if _, height := build.InnerSize(); height != 0 {
		t.Fatalf("unfocused build panel inner height = %d, want collapsed", height)
	}
	if _, height := builds.InnerSize(); height == 0 {
		t.Fatal("focused builds panel did not expand")
	}
}

func TestSimulatorPickerFiltersItems(t *testing.T) {
	a := &App{overlay: &overlayState{
		filter: "ipad",
		items:  []overlayItem{{ID: "phone", Label: "iPhone 17 Pro"}, {ID: "pad", Label: "iPad Pro 13-inch"}},
	}}
	items := a.filteredOverlayItems()
	if len(items) != 1 || items[0].ID != "pad" {
		t.Fatalf("filtered items = %#v", items)
	}
}

func TestBuildEventsAreAppliedInSequence(t *testing.T) {
	record := model.BuildRecord{ID: "build", Phase: model.PhaseBuilding}
	a := &App{outputs: map[string]string{}}
	a.queueBuildEvent(buildmanager.Event{Record: model.BuildRecord{ID: "build", Phase: model.PhaseSucceeded}, Output: "second", Sequence: 2})
	if len(a.records) != 0 {
		t.Fatal("out-of-order event was applied before its predecessor")
	}
	a.queueBuildEvent(buildmanager.Event{Record: record, Output: "first", Sequence: 1})
	if len(a.records) != 1 || a.records[0].Phase != model.PhaseSucceeded {
		t.Fatalf("record = %#v", a.records)
	}
	if a.outputs["build"] != "firstsecond" {
		t.Fatalf("output = %q", a.outputs["build"])
	}
}

func TestSimulatorPickerCursorTracksSelectionAcrossViewport(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: 100, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	items := make([]overlayItem, 37)
	for i := range items {
		items[i] = overlayItem{ID: fmt.Sprint(i), Label: fmt.Sprintf("Simulator %02d", i)}
	}
	a := &App{gui: g, focus: "build", outputs: map[string]string{}, overlay: &overlayState{kind: "simulator", title: "Select Simulator", items: items}}

	assertSelection := func(want int) {
		t.Helper()
		a.overlay.selected = want
		if err := a.layoutOverlay(g, 100, 30); err != nil {
			t.Fatal(err)
		}
		list, err := g.View("overlay")
		if err != nil {
			t.Fatal(err)
		}
		_, origin := list.Origin()
		_, cursor := list.Cursor()
		if got := origin + cursor; got != want {
			t.Fatalf("selection %d rendered at row %d (origin=%d cursor=%d)", want, got, origin, cursor)
		}
	}

	assertSelection(0)
	assertSelection(25)
	assertSelection(36)
	assertSelection(5)
}

func TestConciseBuildOutputKeepsDiagnosticsAndDropsCommands(t *testing.T) {
	raw := `[lazy-xcode] Building App for iPhone 17 Pro
Command line invocation:
    /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild
CompileSwift normal arm64 /tmp/App.swift
/tmp/App.swift:10:5: warning: value was never used
    let unused = value
    ^~~~~~~~~~
SwiftEmitModule normal arm64
** BUILD SUCCEEDED **`
	output := conciseBuildOutput(raw)
	for _, expected := range []string{"[lazy-xcode] Building", "warning: value was never used", "let unused", "BUILD SUCCEEDED"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("concise output missing %q:\n%s", expected, output)
		}
	}
	for _, noise := range []string{"Command line invocation", "xcodebuild", "CompileSwift", "SwiftEmitModule"} {
		if strings.Contains(output, noise) {
			t.Fatalf("concise output retained %q:\n%s", noise, output)
		}
	}
}

func TestBuildOutputFormatsDiagnosticsWithoutChangingText(t *testing.T) {
	plain := `/tmp/App.swift:10:5: warning: value was never used
let unused = value
    ^~~~~~
/tmp/App.swift:20:9: error: cannot find 'missing' in scope
        ^~~~~~~
/tmp/App.swift:20:9: note: did you mean 'existing'?
        ^~~~~~~~
** BUILD FAILED **`
	formatted := formatBuildOutput(plain)
	for _, expected := range []string{
		ansiCyan + "/tmp/App.swift:10:5:" + ansiReset,
		ansiBoldYellow + "warning:" + ansiReset,
		ansiBoldYellow + "    ^~~~~~" + ansiReset,
		ansiBoldRed + "error:" + ansiReset,
		ansiBoldRed + "        ^~~~~~~" + ansiReset,
		ansiCyan + "note:" + ansiReset,
		ansiCyan + "        ^~~~~~~~" + ansiReset,
		ansiBoldRed + "** BUILD FAILED **" + ansiReset,
	} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("formatted output missing %q:\n%s", expected, formatted)
		}
	}
	if got := ansiPattern.ReplaceAllString(formatted, ""); got != plain {
		t.Fatalf("formatting changed transcript:\ngot:  %q\nwant: %q", got, plain)
	}
}

func TestBuildOutputFormatsSuccessAndLifecycleMessages(t *testing.T) {
	plain := "[lazy-xcode] Installing app\n** BUILD SUCCEEDED **"
	formatted := formatBuildOutput(plain)
	if !strings.Contains(formatted, ansiCyan+"[lazy-xcode]"+ansiReset) {
		t.Fatalf("lifecycle prefix not formatted: %q", formatted)
	}
	if !strings.Contains(formatted, ansiBoldGreen+"** BUILD SUCCEEDED **"+ansiReset) {
		t.Fatalf("success marker not formatted: %q", formatted)
	}
}

func TestOutputVerbosityAppearsInPanelTitle(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: 100, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{
		gui: g, focus: "output", outputs: map[string]string{"build": "[lazy-xcode] Building App\nCompileSwift App.swift\n"},
		records: []model.BuildRecord{{ID: "build", Phase: model.PhaseBuilding, StartedAt: time.Now()}}, outputFollow: true,
	}
	if err := a.layout(g); err != nil {
		t.Fatal(err)
	}
	output, _ := g.View("output")
	if !strings.Contains(output.Title, "CONCISE") {
		t.Fatalf("default output title = %q", output.Title)
	}
	if strings.Contains(output.Buffer(), "CompileSwift") {
		t.Fatalf("concise output contains raw command: %q", output.Buffer())
	}
	a.verboseOutput = true
	if err := a.layout(g); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.Title, "RAW") {
		t.Fatalf("raw output title = %q", output.Title)
	}
	if !strings.Contains(output.Buffer(), "CompileSwift") {
		t.Fatalf("raw output omitted command: %q", output.Buffer())
	}
}

func TestOutputScrollingStopsAtRenderedContentBounds(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, Headless: true, Width: 30, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	view, err := g.SetView("output", 0, 0, 29, 9, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		t.Fatal(err)
	}
	view.Wrap = true
	fmt.Fprintln(view, "short output")
	a := &App{outputFollow: true}
	if err := a.scrollOutput(1000)(g, view); err != nil {
		t.Fatal(err)
	}
	if _, origin := view.Origin(); origin != 0 {
		t.Fatalf("short output origin = %d, want 0", origin)
	}
	view.SetOrigin(0, 1000)
	clampOutputOrigin(view)
	if _, origin := view.Origin(); origin != 0 {
		t.Fatalf("clamped short output origin = %d, want 0", origin)
	}

	view.Clear()
	for i := 0; i < 30; i++ {
		fmt.Fprintf(view, "line %02d with enough text to wrap across the view\n", i)
	}
	if err := a.scrollOutput(1000)(g, view); err != nil {
		t.Fatal(err)
	}
	_, height := view.InnerSize()
	_, origin := view.Origin()
	want := max(0, view.ViewLinesHeight()-height)
	if origin != want {
		t.Fatalf("long output origin = %d, want %d", origin, want)
	}
	if err := a.scrollOutput(-1000)(g, view); err != nil {
		t.Fatal(err)
	}
	if _, origin := view.Origin(); origin != 0 {
		t.Fatalf("top origin = %d, want 0", origin)
	}
}

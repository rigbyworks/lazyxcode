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

func TestTargetSummarySeparatesSimulatorsDevicesAndMacs(t *testing.T) {
	targets := []model.Simulator{{ID: "SIM"}, {ID: "PHONE", Physical: true}, {ID: "PAD", Physical: true}, {ID: "MAC", Platform: "macOS"}}
	if got := targetSummary(targets); got != "Ready - 1 simulators, 2 devices, 1 Mac" {
		t.Fatalf("summary = %q", got)
	}
}

func TestTargetPickerIncludesPhysicalDevices(t *testing.T) {
	a := &App{
		configRow: 1,
		sims: []model.Simulator{
			{ID: "SIM", Name: "iPhone 17 Pro", OS: "27.0", State: "Shutdown"},
			{ID: "DEVICE", Name: "Matt's iPhone", State: "Connected", Physical: true},
		},
	}
	if err := a.openConfigPicker(nil, nil); err != nil {
		t.Fatal(err)
	}
	if a.overlay == nil || a.overlay.title != "Select Target" || len(a.overlay.items) != 2 {
		t.Fatalf("target picker = %#v", a.overlay)
	}
	if !strings.Contains(a.overlay.items[1].Label, "Device") || !strings.Contains(a.overlay.items[1].Label, "Connected") {
		t.Fatalf("physical target row = %q", a.overlay.items[1].Label)
	}
}

func TestTargetPickerKeepsColumnsAlignedAndVisible(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: 100, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{
		gui: g, configRow: 1,
		sims: []model.Simulator{
			{ID: "DEVICE", Name: "Matt's iPhone SE (3rd Generation)", State: "Connected", Physical: true},
			{ID: "SIM", Name: "iPad Pro 13-inch (M5)", OS: "27.0", State: "Shutdown"},
		},
	}
	if err := a.openConfigPicker(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.layoutOverlay(g, 100, 30); err != nil {
		t.Fatal(err)
	}
	list, _ := g.View("overlay")
	width, _ := list.InnerSize()
	lines := strings.Split(strings.TrimSpace(list.Buffer()), "\n")
	if len(lines) != 2 {
		t.Fatalf("target rows = %#v", lines)
	}
	for _, line := range lines {
		if len([]rune(line)) > width {
			t.Fatalf("target row exceeds inner width %d: %q", width, line)
		}
	}
	deviceKind, simulatorKind := strings.Index(lines[0], "Device"), strings.Index(lines[1], "Simulator")
	deviceState, simulatorState := strings.Index(lines[0], "Connected"), strings.Index(lines[1], "Shutdown")
	if deviceKind != simulatorKind || deviceState != simulatorState {
		t.Fatalf("columns are misaligned: %#v", lines)
	}
}

func TestTargetPickerSearchFrameDoesNotDeclareOverlap(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: 100, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{gui: g, overlay: &overlayState{kind: "simulator", title: "Select Target"}}
	if err := a.layoutOverlay(g, 100, 30); err != nil {
		t.Fatal(err)
	}
	filter, _ := g.View("filter")
	if filter.Overlaps != 0 {
		t.Fatalf("search frame overlap flags = %d, want none", filter.Overlaps)
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

func TestHelpOverlayNavigationScrollsDocument(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: 100, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{gui: g, focus: "build", outputs: map[string]string{}}
	if err := a.showHelp(g, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.layoutOverlay(g, 100, 30); err != nil {
		t.Fatal(err)
	}
	list, err := g.View("overlay")
	if err != nil {
		t.Fatal(err)
	}
	if list.ViewLinesHeight() <= 0 {
		t.Fatal("help content was not rendered")
	}
	if err := a.moveOverlay(1)(g, list); err != nil {
		t.Fatal(err)
	}
	if err := a.layoutOverlay(g, 100, 30); err != nil {
		t.Fatal(err)
	}
	if _, origin := list.Origin(); origin != 1 {
		t.Fatalf("help origin after scrolling = %d, want 1", origin)
	}
	if err := a.moveOverlayTo(true)(g, list); err != nil {
		t.Fatal(err)
	}
	_, height := list.InnerSize()
	wantBottom := max(0, list.ViewLinesHeight()-height)
	if _, origin := list.Origin(); origin != wantBottom {
		t.Fatalf("help bottom origin = %d, want %d", origin, wantBottom)
	}
	if err := a.moveOverlay(3)(g, list); err != nil {
		t.Fatal(err)
	}
	if _, origin := list.Origin(); origin != wantBottom {
		t.Fatalf("help origin moved past bottom to %d", origin)
	}
	if err := a.moveOverlayTo(false)(g, list); err != nil {
		t.Fatal(err)
	}
	if _, origin := list.Origin(); origin != 0 {
		t.Fatalf("help top origin = %d, want 0", origin)
	}
}

func TestHelpOverlayScrollingKeysAreBound(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, Headless: true, Width: 100, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{}
	if err := a.bindKeys(g); err != nil {
		t.Fatal(err)
	}
	for _, key := range []any{
		'j', 'k', 'g', 'G',
		gocui.KeyArrowDown, gocui.KeyArrowUp,
		gocui.KeyPgdn, gocui.KeyPgup,
		gocui.KeyHome, gocui.KeyEnd,
		gocui.MouseWheelDown, gocui.MouseWheelUp,
	} {
		if err := g.DeleteKeybinding("overlay", key, gocui.ModNone); err != nil {
			t.Fatalf("overlay key %v is not bound: %v", key, err)
		}
	}
}

func TestOpenInXcodeKeyIsBoundInEveryMainView(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, Headless: true, Width: 100, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{}
	if err := a.bindKeys(g); err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"build", "builds", "output"} {
		if err := g.DeleteKeybinding(view, 'o', gocui.ModNone); err != nil {
			t.Fatalf("%s view does not bind o: %v", view, err)
		}
	}
}

func TestTestPickerOffersDiscoveredScopes(t *testing.T) {
	a := &App{
		manager: &buildmanager.Manager{}, schemes: []string{"App"}, sims: []model.Simulator{{ID: "PHONE"}},
		testTargets: []model.TestTarget{
			{Name: "AppTests", Kind: model.TestUnit},
			{Name: "ModelTests", Kind: model.TestUnit},
			{Name: "AppUITests", Kind: model.TestUI},
		},
	}
	if err := a.openTestPicker(nil, nil); err != nil {
		t.Fatal(err)
	}
	if a.overlay == nil || a.overlay.kind != "test-scope" {
		t.Fatalf("test picker = %#v", a.overlay)
	}
	want := []string{"all", "unit", "ui"}
	for i, id := range want {
		if a.overlay.items[i].ID != id {
			t.Fatalf("picker item %d = %#v, want %q", i, a.overlay.items[i], id)
		}
	}
}

func TestTestActivityRowsUseTestSpecificStatuses(t *testing.T) {
	record := model.BuildRecord{ID: "123-001", Operation: model.OperationTest, Phase: model.PhaseSucceeded, Simulator: model.Simulator{Name: "iPhone"}, StartedAt: time.Now()}
	if row := formatBuildRow(record, 50); !strings.HasPrefix(row, "PASS") {
		t.Fatalf("successful test row = %q", row)
	}
	record.Phase = model.PhaseTesting
	if row := formatBuildRow(record, 50); !strings.HasPrefix(row, "TEST") {
		t.Fatalf("active test row = %q", row)
	}
}

func TestConciseBuildOutputSummarizesStepsAndDeduplicatesDiagnostics(t *testing.T) {
	raw := `[lazy-xcode] Building App for iPhone 17 Pro
Command line invocation:
    /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild
[lazy-xcode:step] start 100000 1 Plan build
[lazy-xcode:step] done 1200 1 Plan build
[lazy-xcode:step] start 101200 4 Compile sources
/tmp/App.swift:10:5: warning: value was never used
/tmp/App.swift:10:5: warning: value was never used
2026-08-17 tool[123] warning: metadata was skipped
ld: library 'MissingKit' not found
[lazy-xcode:step] done 3800 4 Compile sources
** BUILD SUCCEEDED **
[lazy-xcode] Installing app`
	output := conciseBuildOutputAt(raw, time.UnixMilli(105000))
	for _, expected := range []string{
		"BUILD STEPS",
		"✓ Plan build",
		"1.2s",
		"✓ Compile sources",
		"3.8s",
		"DIAGNOSTICS (3)",
		"warning: App.swift:10:5 — value was never used",
		"warning: Build — metadata was skipped",
		"error: Linker — library 'MissingKit' not found",
		"DEPLOYMENT",
		"Installing app",
		"BUILD SUCCEEDED",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("concise output missing %q:\n%s", expected, output)
		}
	}
	if strings.Count(output, "value was never used") != 1 {
		t.Fatalf("diagnostic was not deduplicated:\n%s", output)
	}
	for _, noise := range []string{"Command line invocation", "xcodebuild", "/tmp/App.swift"} {
		if strings.Contains(output, noise) {
			t.Fatalf("concise output retained %q:\n%s", noise, output)
		}
	}
}

func TestConciseBuildOutputUpdatesActiveStepElapsedTime(t *testing.T) {
	raw := "[lazy-xcode:step] start 100000 4 Compile sources\n"
	output := conciseBuildOutputAt(raw, time.UnixMilli(104250))
	for _, expected := range []string{"● Compile sources", "4.2s"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("active output missing %q: %s", expected, output)
		}
	}
}

func TestConciseBuildOutputIncludesAppConsoleVerbatim(t *testing.T) {
	raw := `[lazy-xcode] Building App for iPhone 17 Pro
** BUILD SUCCEEDED **
[lazy-xcode] Installing app
[lazy-xcode] Launching com.example.app
[lazy-xcode] App console
User signed in
warning: this is app output, not a build diagnostic`
	output := conciseBuildOutputAt(raw, time.Now())
	for _, expected := range []string{"APP CONSOLE", "User signed in", "warning: this is app output, not a build diagnostic"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("concise output missing %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "DIAGNOSTICS") {
		t.Fatalf("app console line was treated as a build diagnostic:\n%s", output)
	}
}

func TestRunningBuildUsesRunStatus(t *testing.T) {
	record := model.BuildRecord{Phase: model.PhaseRunning}
	if label := recordPhaseLabel(record); label != "RUN" {
		t.Fatalf("running build label = %q, want RUN", label)
	}
}

func TestConciseXCTestOutputShowsSuiteResultsAndFailures(t *testing.T) {
	raw := `[lazy-xcode] Testing App on iPhone 17 Pro (26.0) — Unit Tests
[lazy-xcode:step] start 100000 4 Compile sources
[lazy-xcode:step] done 2400 4 Compile sources
Test Suite 'Selected tests' started at 2026-08-17 10:00:00.000.
Test Suite 'AppTests.xctest' started at 2026-08-17 10:00:00.000.
Test Suite 'LoginTests' started at 2026-08-17 10:00:00.000.
Test Case '-[AppTests.LoginTests testValidLogin]' started.
Test Case '-[AppTests.LoginTests testValidLogin]' passed (0.200 seconds).
Test Case '-[AppTests.LoginTests testInvalidLogin]' started.
/tmp/LoginTests.swift:42:7: error: -[AppTests.LoginTests testInvalidLogin] : XCTAssertTrue failed
Test Case '-[AppTests.LoginTests testInvalidLogin]' failed (1.300 seconds).
Test Suite 'LoginTests' failed at 2026-08-17 10:00:01.500.
Test Suite 'AppTests.xctest' failed at 2026-08-17 10:00:01.500.
Test Suite 'Selected tests' failed at 2026-08-17 10:00:01.500.
** TEST FAILED **`
	output := conciseTestOutputAt(raw, time.UnixMilli(105000))
	for _, expected := range []string{
		"BUILD PREPARATION", "Compile sources", "2.4s", "TEST SUITES (1)",
		"✗ LoginTests", "1 passed, 1 failed", "1.5s",
		"DIAGNOSTICS (1)", "error: LoginTests.swift:42:7", "XCTAssertTrue failed", "TEST FAILED",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("test summary missing %q:\n%s", expected, output)
		}
	}
	for _, aggregate := range []string{"Selected tests", "AppTests.xctest"} {
		if strings.Contains(output, aggregate) {
			t.Fatalf("test summary retained aggregate suite %q:\n%s", aggregate, output)
		}
	}
}

func TestConciseSwiftTestingOutputShowsSuiteResult(t *testing.T) {
	raw := `[lazy-xcode] Testing App on iPhone 17 Pro (26.0) — Unit Tests
◇ Suite SearchResultTests started.
◇ Test lookupCoverArt started.
✔ Test lookupCoverArt passed after 0.001 seconds.
✔ Suite SearchResultTests passed after 0.002 seconds.
✔ Test run with 1 test in 1 suite passed after 0.003 seconds.`
	output := conciseTestOutputAt(raw, time.Now())
	for _, expected := range []string{"TEST SUITES (1)", "✓ SearchResultTests", "passed", "2ms", "Test run with 1 test"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("Swift Testing summary missing %q:\n%s", expected, output)
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
	plain := "[lazy-xcode] Installing app\n  warning: App.swift:10:5 — value was never used\n** BUILD SUCCEEDED **"
	formatted := formatBuildOutput(plain)
	if !strings.Contains(formatted, ansiCyan+"[lazy-xcode]"+ansiReset) {
		t.Fatalf("lifecycle prefix not formatted: %q", formatted)
	}
	if !strings.Contains(formatted, ansiBoldGreen+"** BUILD SUCCEEDED **"+ansiReset) {
		t.Fatalf("success marker not formatted: %q", formatted)
	}
	if !strings.Contains(formatted, ansiBoldYellow+"warning:"+ansiReset+" "+ansiCyan+"App.swift:10:5"+ansiReset) {
		t.Fatalf("concise diagnostic not formatted: %q", formatted)
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

func TestCopyOutputCopiesDisplayedTextWithoutFormatting(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, Headless: true, Width: 30, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	view, err := g.SetView("output", 0, 0, 29, 9, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		t.Fatal(err)
	}
	fmt.Fprint(view, ansiBoldRed+"BUILD FAILED"+ansiReset+"\nApp.swift:10:5")
	var copied string
	a := &App{copyToClipboard: func(value string) error {
		copied = value
		return nil
	}}
	if err := a.copyOutput(g, view); err != nil {
		t.Fatal(err)
	}
	if copied != "BUILD FAILED\nApp.swift:10:5" {
		t.Fatalf("copied output = %q", copied)
	}
	if a.status != "Output copied to clipboard" {
		t.Fatalf("status = %q", a.status)
	}
}

func TestCopyOutputReportsClipboardFailure(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, Headless: true, Width: 30, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	view, err := g.SetView("output", 0, 0, 29, 9, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		t.Fatal(err)
	}
	fmt.Fprint(view, "build output")
	a := &App{copyToClipboard: func(string) error { return fmt.Errorf("clipboard unavailable") }}
	if err := a.copyOutput(g, view); err != nil {
		t.Fatal(err)
	}
	if a.status != "Copy output: clipboard unavailable" {
		t.Fatalf("status = %q", a.status)
	}
}

func TestOpenInXcodeUsesSelectedContainer(t *testing.T) {
	var opened string
	a := &App{
		container: model.Container{Name: "App.xcworkspace", Path: "/tmp/App.xcworkspace"},
		openXcode: func(path string) error {
			opened = path
			return nil
		},
	}
	if err := a.openInXcode(nil, nil); err != nil {
		t.Fatal(err)
	}
	if opened != "/tmp/App.xcworkspace" {
		t.Fatalf("opened path = %q", opened)
	}
	if a.status != "Opened App.xcworkspace in Xcode" {
		t.Fatalf("status = %q", a.status)
	}
}

func TestOpenInXcodeReportsFailure(t *testing.T) {
	a := &App{
		container: model.Container{Name: "App.xcodeproj", Path: "/tmp/App.xcodeproj"},
		openXcode: func(string) error { return fmt.Errorf("Xcode unavailable") },
	}
	if err := a.openInXcode(nil, nil); err != nil {
		t.Fatal(err)
	}
	if a.status != "Open in Xcode: Xcode unavailable" {
		t.Fatalf("status = %q", a.status)
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

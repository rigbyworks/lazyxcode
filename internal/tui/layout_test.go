package tui

import (
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
		outputs: map[string]string{"123-001": "CompileSwift App.swift\n"}, outputFollow: true,
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
	if !strings.Contains(output.Buffer(), "CompileSwift") || !strings.Contains(output.Title, "FOLLOW") {
		t.Fatalf("output pane/title = %q / %q", output.Buffer(), output.Title)
	}
}

func TestSmallTerminalShowsGuard(t *testing.T) {
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, Headless: true, Width: 79, Height: 19})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &App{gui: g, focus: "build", outputs: map[string]string{}}
	if err := a.layout(g); err != nil {
		t.Fatal(err)
	}
	guard, err := g.View("guard")
	if err != nil || !strings.Contains(guard.Buffer(), "80x20") {
		t.Fatalf("guard = %q, %v", guard.Buffer(), err)
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

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mwahlig/lazy-xcode/internal/xcodecloud"
)

func TestContextualFooterFitsWithoutClippingActions(t *testing.T) {
	for _, mode := range []appMode{modeLocal, modeCloud} {
		for _, focus := range []string{"build", "builds", "output"} {
			for _, width := range []int{44, 60, 74, 80, 120, 180} {
				a := &App{mode: mode, focus: focus}
				footer := a.footerKeys(width)
				if len([]rune(footer)) > width || strings.Contains(footer, "…") {
					t.Fatalf("%d %s: %s", width, focus, footer)
				}
				if !strings.Contains(footer, "[:] Actions") || !strings.Contains(footer, "[m]") {
					t.Fatal("missing action discovery", footer)
				}
				if strings.Count(footer, "[") > 5 {
					t.Fatal("footer too busy", footer)
				}
				if focus == "output" && strings.Contains(footer, "[b]") {
					t.Fatal("irrelevant build hint", footer)
				}
			}
		}
	}
}

func TestWarningsStayReadableOutsideConfigurationPanel(t *testing.T) {
	for _, size := range [][2]int{{160, 48}, {74, 23}, {44, 10}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			h := newCloudHarness(t, configuredFake(), size[0], size[1])
			a := h.app
			h.press(a.toggleMode)
			h.drain()
			warning := "private key /Users/example/.config/lazy-xcode/keys/a-very-long-key-file-name.p8 is readable by other users; use chmod 600"
			a.cloud.warnings = []string{warning}
			a.cloud.runs = []xcodecloud.BuildRun{}
			h.layout()
			if strings.Contains(h.view("build").Buffer(), "/Users/") {
				t.Fatal("full warning still in small panel")
			}
			if !strings.Contains(h.view("output").Buffer(), warning) {
				t.Fatal("empty state dropped warning")
			}
			a.cloud.configRow = 2
			h.press(a.openCloudConfigPicker)
			h.layout()
			if a.overlay == nil || a.overlay.title != "Cloud Connection" || !h.view("overlay").Wrap {
				t.Fatal("details not wrapped")
			}
			if !strings.Contains(h.view("overlay").Buffer(), warning) {
				t.Fatal("details lost warning")
			}
			if err := a.moveOverlayTo(true)(h.gui, h.view("overlay")); err != nil {
				t.Fatal(err)
			}
			h.layout()
			if _, origin := h.view("overlay").Origin(); origin == 0 {
				t.Fatal("details cannot scroll")
			}
		})
	}
}

func TestActionMenuFiltersAndInvokesExistingControls(t *testing.T) {
	h := newCloudHarness(t, configuredFake(), 120, 36)
	a := h.app
	h.press(a.showActions)
	a.overlay.filter = "keyboard"
	h.press(a.chooseOverlay)
	if a.overlay == nil || a.overlay.title != "Help" {
		t.Fatal("palette did not invoke help")
	}
	h.press(a.closeOverlay)
	h.press(a.toggleMode)
	h.drain()
	h.press(a.showActions)
	for _, item := range a.overlay.items {
		if item.ID == "b" || item.ID == "t" || item.ID == "c" {
			t.Fatal("Cloud has local mutation", item)
		}
	}
	a.overlay.filter = "connection"
	h.press(a.chooseOverlay)
	if a.overlay == nil || a.overlay.title != "Cloud Connection" {
		t.Fatal("palette did not open details")
	}
}

func TestConnectionRowHasOneSelectionMarker(t *testing.T) {
	h := newCloudHarness(t, configuredFake(), 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	a.cloud.configRow = 1
	h.press(a.moveConfig(1))
	h.layout()
	if a.cloud.configRow != 2 || strings.Count(h.view("build").Buffer(), ">") != 1 {
		t.Fatal(h.view("build").Buffer())
	}
	h.press(a.moveConfig(1))
	if a.cloud.configRow != 0 {
		t.Fatal("configuration rows do not wrap")
	}
}

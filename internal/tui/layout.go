package tui

import (
	"fmt"
	"strings"

	"github.com/jesseduffield/gocui"
	"github.com/rigbyworks/lazyxcode/internal/model"
)

var roundedFrame = []rune{'─', '│', '╭', '╮', '╰', '╯'}

func (a *App) layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	if maxX < 44 || maxY < 10 {
		a.hideViews(g)
		v, err := g.SetView("guard", 0, 0, maxX-1, maxY-1, 0)
		if err != nil && !gocui.IsUnknownView(err) {
			return err
		}
		v.Visible, v.Title = true, " lazyxcode "
		v.Clear()
		fmt.Fprintf(v, "Terminal is too small.\n\nCurrent: %dx%d\nRequired: 44x10", maxX, maxY)
		return nil
	}
	if v, err := g.View("guard"); err == nil {
		v.Visible = false
	}
	left := sidePanelWidth(maxX)
	statusY := maxY - 1
	contentTop, contentBottom := 1, statusY-1
	buildHeight := buildPanelHeight(contentBottom-contentTop+1, a.focus)
	buildBottom := contentTop + buildHeight - 1
	if err := a.ensureView(g, "build", 0, 1, left-1, buildBottom, a.modeTabsTitle(), false); err != nil {
		return err
	}
	if err := a.ensureView(g, "builds", 0, buildBottom+1, left-1, statusY-1, a.activityTitle(), true); err != nil {
		return err
	}
	if err := a.ensureView(g, "output", left, 1, maxX-1, statusY-1, a.outputTitle(), false); err != nil {
		return err
	}
	if err := a.ensureHeader(g, maxX); err != nil {
		return err
	}
	if err := a.ensureFooter(g, statusY, maxX); err != nil {
		return err
	}
	if err := a.render(g); err != nil {
		return err
	}
	if a.overlay != nil {
		if err := a.layoutOverlay(g, maxX, maxY); err != nil {
			return err
		}
	} else {
		a.hideOverlay(g)
	}
	_, err := g.SetCurrentView(a.currentView())
	if err != nil && !gocui.IsUnknownView(err) {
		return err
	}
	return nil
}

func (a *App) modeTabsTitle() string {
	if a.mode == modeCloud {
		return "Local  [Cloud] [1]"
	}
	return "[Local]  Cloud [1]"
}

func (a *App) activityTitle() string {
	if a.mode == modeCloud {
		return "Cloud Activity [2]"
	}
	return "Local Activity [2]"
}

func (a *App) outputTitle() string {
	title := "Output [3]"
	if report := a.selectedTestOutput(); report != nil {
		return "Output [3] - " + report.title + " [Esc] Log"
	}

	if a.mode == modeCloud {
		if a.cloud != nil && a.cloud.verbose {
			title += " - RAW"
		} else {
			title += " - CONCISE"
		}
		if a.cloud != nil {
			if run, ok := a.cloud.selectedRun(); ok {
				title += fmt.Sprintf(" - #%d", run.Number)
			}
		}
		return title
	}
	if a.selectedOutputPage() != nil {
		return title + " - RAW PAGE [ / ]  [G] Live"
	}
	if a.verboseOutput {
		title += " - RAW"
	} else {
		title += " - CONCISE"
	}
	if len(a.records) > 0 && a.buildIndex < len(a.records) {
		title += " - #" + shortID(a.records[a.buildIndex].ID)
		if a.records[a.buildIndex].Phase.Active() && a.outputFollow {
			title += " - FOLLOW"
		}
	}
	return title
}

func sidePanelWidth(width int) int {
	if width >= 80 {
		return clamp(width*38/100, 34, 46)
	}
	return clamp(width*42/100, 22, min(30, width-22))
}

func buildPanelHeight(availableHeight int, focus string) int {
	const collapsedHeight = 2
	if availableHeight >= 12 {
		return 9
	}
	if focus == "build" {
		return max(collapsedHeight, availableHeight-collapsedHeight)
	}
	return collapsedHeight
}

func (a *App) ensureHeader(g *gocui.Gui, maxX int) error {
	v, err := g.SetView("header", -1, -1, maxX, 1, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		return err
	}
	v.Visible, v.Frame = true, false
	v.Clear()
	name := "discovering project..."
	if a.container.Name != "" {
		name = a.container.Name
	}
	if a.mode == modeCloud {
		name += " | Xcode Cloud"
	}
	title := " lazyxcode | " + name
	if maxX >= 74 && a.status != "" {
		title = truncate(title, maxX/2)
		status := truncate(strings.Join(strings.Fields(a.status), " "), maxX-len([]rune(title))-8) + " [i]"
		fmt.Fprint(v, title+strings.Repeat(" ", max(2, maxX-len([]rune(title))-len([]rune(status))-1))+status)
	} else if a.status != "" {
		fmt.Fprint(v, truncate(strings.Join(strings.Fields(a.status), " "), maxX-5)+" [i]")
	} else {
		fmt.Fprint(v, truncate(title, maxX))
	}
	return nil
}

func (a *App) ensureFooter(g *gocui.Gui, y, maxX int) error {
	v, err := g.SetView("status", -1, y-1, maxX, y+1, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		return err
	}
	v.Visible, v.Frame, v.Wrap = true, false, false
	v.Clear()
	fmt.Fprint(v, a.footerKeys(maxX))
	return nil
}

// Contextual hints are admitted as whole items. The action menu and mode
// switch keep their places when the terminal narrows.
func (a *App) footerKeys(width int) string {
	if a.overlay != nil {
		if a.overlay.kind == "help" {
			return " [j/k] Scroll   [Esc] Close"
		}
		return " [Enter] Select   [Esc] Back"
	}
	mode := "[m] Cloud"
	if a.mode == modeCloud {
		mode = "[m] Local"
	}
	anchors := "[:] Actions   " + mode
	var hints []string
	switch a.focus {
	case "builds":
		if a.mode == modeCloud {
			hints = []string{"[Enter] Results", "[a] Files", "[r] Refresh"}
		} else {
			hints = []string{"[r] Run", "[b] Build"}
			if len(a.records) > 0 && a.buildIndex < len(a.records) {
				record := a.records[a.buildIndex]
				if record.Phase.Active() {
					hints = []string{"[x] Stop", "[t] Test"}
				} else if record.OperationKind() == model.OperationTest || record.OperationKind() == model.OperationDiscoverTests {
					hints = []string{"[Enter] Results", "[t] Test"}
				}
			}
		}
	case "output":
		hints = []string{"[j/k] Scroll", "[y] Copy", "[v] Raw"}
		if a.verboseOutput && a.mode == modeLocal || a.mode == modeCloud && a.cloud != nil && a.cloud.verbose {
			hints[2] = "[v] Concise"
		}
		if a.selectedTestOutput() != nil {
			hints = []string{"[Enter] Actions", "[Esc] Log", "[y] Copy"}
		}
	default:
		hints = []string{"[Enter] Edit", "[b] Build", "[t] Test"}
		if a.mode == modeCloud {
			hints = []string{"[Enter] Edit", "[r] Refresh"}
		}
	}
	visible := []string{}
	for _, hint := range hints {
		candidate := " " + strings.Join(append(append([]string{}, visible...), hint), "   ") + "   " + anchors
		if len([]rune(candidate)) <= width {
			visible = append(visible, hint)
		}
	}
	if len(visible) == 0 {
		return " " + anchors
	}
	return " " + strings.Join(visible, "   ") + "   " + anchors
}

func (a *App) ensureView(g *gocui.Gui, name string, x0, y0, x1, y1 int, title string, highlight bool) error {
	v, err := g.SetView(name, x0, y0, x1, y1, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		return err
	}
	v.Visible, v.Title, v.FrameRunes = true, " "+title+" ", roundedFrame
	v.Wrap = name == "output"
	v.Highlight = highlight
	v.SelBgColor = gocui.GetColor("#315d46")
	v.SelFgColor = gocui.GetColor("#ffffff")
	return nil
}

func (a *App) layoutOverlay(g *gocui.Gui, maxX, maxY int) error {
	if a.renderedOverlay != a.overlay {
		a.overlay.initialized = false
		a.renderedOverlay = a.overlay
	}
	width := min(76, maxX-2)
	height := min(16, maxY-2)
	x0, y0 := (maxX-width)/2, (maxY-height)/2
	x1, y1 := x0+width, y0+height
	filterHeight := 3
	filter, err := g.SetView("filter", x0, y0, x1, y0+filterHeight-1, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		return err
	}
	filter.Visible = a.overlay.kind != "help" && a.overlay.kind != "confirm-cache" && a.overlay.kind != "confirm-quit"
	filter.Title = " " + a.overlay.title + " "
	filter.Editable = true
	filter.Editor = gocui.EditorFunc(a.editFilter)
	filter.FrameRunes = roundedFrame
	if !a.overlay.initialized {
		filter.Clear()
		a.overlay.initialized = true
	}
	listY := y0
	if filter.Visible {
		listY = y0 + filterHeight
	}
	list, err := g.SetView("overlay", x0, listY, x1, y1, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		return err
	}
	list.Visible, list.Wrap, list.FrameRunes = true, a.overlay.kind == "help", roundedFrame
	if !filter.Visible {
		list.Title = " " + a.overlay.title + " "
	} else {
		list.Title = " [Enter] Select  [Esc] Cancel "
	}
	list.Highlight = a.overlay.kind != "help"
	list.SelBgColor = gocui.GetColor("#315d46")
	list.SelFgColor = gocui.GetColor("#ffffff")
	return a.renderOverlay(filter, list)
}

func (a *App) hideViews(g *gocui.Gui) {
	for _, name := range []string{"header", "build", "builds", "output", "status", "filter", "overlay"} {
		if v, err := g.View(name); err == nil {
			v.Visible = false
		}
	}
}

func (a *App) hideOverlay(g *gocui.Gui) {
	a.renderedOverlay = nil
	for _, name := range []string{"filter", "overlay"} {
		if v, err := g.View(name); err == nil {
			v.Visible = false
		}
	}
}

func (a *App) currentView() string {
	if a.overlay != nil {
		if a.overlay.kind == "help" || a.overlay.kind == "confirm-cache" || a.overlay.kind == "confirm-quit" {
			return "overlay"
		}
		return "filter"
	}
	return a.focus
}

package tui

import (
	"fmt"

	"github.com/jesseduffield/gocui"
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
		v.Visible, v.Title = true, " lazy-xcode "
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
	fmt.Fprint(v, truncate(" lazy-xcode | "+name, maxX))
	return nil
}

func (a *App) ensureFooter(g *gocui.Gui, y, maxX int) error {
	v, err := g.SetView("status", -1, y-1, maxX, y+1, 0)
	if err != nil && !gocui.IsUnknownView(err) {
		return err
	}
	v.Visible, v.Frame, v.Wrap = true, false, false
	v.Clear()
	keys := a.footerKeys(maxX)
	if a.status != "" {
		keys += "  |  " + a.status
	}
	fmt.Fprint(v, truncate(keys, maxX))
	return nil
}

// footerKeys lists only the actions that apply to the visible mode so that a
// key can never trigger an invisible local mutation from Cloud mode.
func (a *App) footerKeys(maxX int) string {
	if a.mode == modeCloud {
		switch {
		case maxX >= 110:
			return "[Tab] Focus [Enter] Select [o] Xcode [r] Refresh [L] Older [a] Files [v] Raw [y] Copy [m] Local [?] Help [q] Quit"
		case maxX >= 68:
			return " Tab/Enter  o Xcode  r Refresh  L Older  a Files  v Raw  y Copy  m Local  ? Help  q Quit"
		}
		return " o Xcode  r Refresh  L Older  m Local  q Quit"
	}
	switch {
	case maxX >= 110:
		return "[Tab] Focus [Enter] Select [o] Xcode [b] Build [r] Run [t] Test [x] Stop [R] Reload [v] Raw [y] Copy [m] Cloud [?] Help [q] Quit"
	case maxX >= 68:
		return " Tab/Enter  o Xcode  b Build  r Run  t Test  x Stop  R Reload  v Raw  y Copy  m Cloud  ? Help  q Quit"
	}
	return " o Xcode  b Build  r Run  t Test  x Stop  R Reload  m Cloud  q Quit"
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
	list.Visible, list.Wrap, list.FrameRunes = true, false, roundedFrame
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

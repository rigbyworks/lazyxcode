package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jesseduffield/gocui"
	"github.com/mwahlig/lazy-xcode/internal/model"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func (a *App) render(g *gocui.Gui) error {
	buildView, _ := g.View("build")
	buildsView, _ := g.View("builds")
	outputView, _ := g.View("output")
	if buildView == nil || buildsView == nil || outputView == nil {
		return nil
	}
	buildView.Clear()
	container := valueOr(a.container.Name, "-")
	scheme := "Loading..."
	if len(a.schemes) > 0 && a.scheme < len(a.schemes) {
		scheme = a.schemes[a.scheme]
	}
	simulator := "Loading..."
	if !a.loading && len(a.sims) == 0 {
		simulator = "No compatible simulators"
	} else if len(a.sims) > 0 && a.simulator < len(a.sims) {
		simulator = a.sims[a.simulator].Label()
	}
	prefix0, prefix1 := "  ", "  "
	if a.focus == "build" {
		if a.configRow == 0 {
			prefix0 = "> "
		} else {
			prefix1 = "> "
		}
	}
	fmt.Fprintf(buildView, "  Container   %s\n", container)
	fmt.Fprintf(buildView, "%sScheme      %s  [>]\n", prefix0, scheme)
	fmt.Fprintf(buildView, "%sSimulator   %s  [>]\n\n", prefix1, simulator)
	button := "[b] Start build"
	if a.loading || len(a.schemes) == 0 || len(a.sims) == 0 {
		button = "Build unavailable"
	}
	fmt.Fprintf(buildView, "  %s\n", button)
	fmt.Fprintf(buildView, "  Cache: %s       [c] Clear cache", formatBytes(a.cacheSize))

	buildsView.Clear()
	for _, record := range a.records {
		fmt.Fprintln(buildsView, formatBuildRow(record))
	}
	if len(a.records) == 0 {
		fmt.Fprintln(buildsView, "  No builds yet")
	}
	if len(a.records) > 0 {
		if a.buildIndex >= len(a.records) {
			a.buildIndex = len(a.records) - 1
		}
		buildsView.SetCursor(0, a.buildIndex)
		ensureVisible(buildsView, a.buildIndex)
	}

	outputView.Clear()
	if len(a.records) == 0 {
		fmt.Fprintln(outputView, "Select a scheme and simulator, then press b to build.")
	} else {
		a.loadSelectedOutput()
		record := a.records[a.buildIndex]
		output := ansiPattern.ReplaceAllString(a.outputs[record.ID], "")
		fmt.Fprint(outputView, output)
		if record.Error != "" {
			if output != "" && !strings.HasSuffix(output, "\n") {
				fmt.Fprintln(outputView)
			}
			fmt.Fprintf(outputView, "\n[%s] %s\n", phaseLabel(record.Phase), record.Error)
		}
		if record.Phase.Active() && a.outputFollow {
			_, height := outputView.InnerSize()
			lines := strings.Count(outputView.Buffer(), "\n")
			outputView.SetOrigin(0, max(0, lines-height))
		}
	}
	return nil
}

func formatBuildRow(record model.BuildRecord) string {
	return fmt.Sprintf("%-7s #%s %-14s %s", phaseLabel(record.Phase), shortID(record.ID), truncate(record.Simulator.Name, 14), formatDuration(record.Duration(time.Now())))
}

func phaseLabel(phase model.Phase) string {
	switch phase {
	case model.PhaseQueued:
		return "QUEUE"
	case model.PhaseBuilding:
		return "BUILD"
	case model.PhaseBooting:
		return "BOOT"
	case model.PhaseInstalling:
		return "INSTALL"
	case model.PhaseLaunching:
		return "LAUNCH"
	case model.PhaseSucceeded:
		return "OK"
	case model.PhaseCancelled:
		return "STOP"
	default:
		return "FAIL"
	}
}

func shortID(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	if len(id) > 4 {
		return id[len(id)-4:]
	}
	return id
}

func formatDuration(duration time.Duration) string {
	seconds := int(duration.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%02ds", seconds)
	}
	return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(size)
	unit := "B"
	for _, candidate := range units {
		value /= 1024
		unit = candidate
		if value < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func ensureVisible(view *gocui.View, index int) {
	_, height := view.InnerSize()
	_, origin := view.Origin()
	if index < origin {
		view.SetOrigin(0, index)
	} else if index >= origin+height {
		view.SetOrigin(0, index-height+1)
	}
}

func (a *App) renderOverlay(filter, list *gocui.View) error {
	list.Clear()
	itemOffset := 0
	if a.overlay.message != "" {
		fmt.Fprintln(list, a.overlay.message)
		itemOffset = strings.Count(a.overlay.message, "\n") + 1
		if !strings.HasSuffix(a.overlay.message, "\n") {
			itemOffset++
		}
	}
	items := a.filteredOverlayItems()
	if a.overlay.selected >= len(items) {
		a.overlay.selected = max(0, len(items)-1)
	}
	for _, item := range items {
		fmt.Fprintln(list, item.Label)
	}
	if len(items) > 0 {
		list.SetCursor(0, a.overlay.selected+itemOffset)
		ensureVisible(list, a.overlay.selected+itemOffset)
	}
	if filter.Visible && filter.Buffer() == "" && a.overlay.filter != "" {
		filter.Clear()
		fmt.Fprint(filter, a.overlay.filter)
	}
	return nil
}

func (a *App) filteredOverlayItems() []overlayItem {
	if a.overlay == nil || a.overlay.filter == "" {
		if a.overlay == nil {
			return nil
		}
		return a.overlay.items
	}
	needle := strings.ToLower(a.overlay.filter)
	var result []overlayItem
	for _, item := range a.overlay.items {
		if strings.Contains(strings.ToLower(item.Label), needle) {
			result = append(result, item)
		}
	}
	return result
}

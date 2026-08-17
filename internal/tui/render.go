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
	buildWidth, buildHeight := buildView.InnerSize()
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
	button := "[b] Start build"
	if a.loading || len(a.schemes) == 0 || len(a.sims) == 0 {
		button = "Build unavailable"
	}
	if buildHeight >= 7 {
		writeViewLine(buildView, buildWidth, "  Container   "+container)
		writeViewLine(buildView, buildWidth, prefix0+"Scheme      "+scheme+"  [>]")
		writeViewLine(buildView, buildWidth, prefix1+"Simulator   "+simulator+"  [>]")
		writeViewLine(buildView, buildWidth, "")
		writeViewLine(buildView, buildWidth, "  "+button)
		writeViewLine(buildView, buildWidth, "  Cache: "+formatBytes(a.cacheSize)+"  [c] Clear")
	} else if buildHeight > 0 {
		lines := []string{
			"  Project  " + container,
			prefix0 + "Scheme   " + scheme + " [>]",
			prefix1 + "Device   " + simulator + " [>]",
			"  [b] Build  [c] Cache " + formatBytes(a.cacheSize),
		}
		for i := 0; i < min(buildHeight, len(lines)); i++ {
			writeViewLine(buildView, buildWidth, lines[i])
		}
	}

	buildsView.Clear()
	buildsWidth, _ := buildsView.InnerSize()
	for _, record := range a.records {
		fmt.Fprintln(buildsView, formatBuildRow(record, buildsWidth))
	}
	if len(a.records) == 0 {
		fmt.Fprintln(buildsView, "  No builds yet")
	}
	buildsView.Highlight = len(a.records) > 0
	if len(a.records) > 0 {
		if a.buildIndex >= len(a.records) {
			a.buildIndex = len(a.records) - 1
		}
		setListCursor(buildsView, a.buildIndex, len(a.records), 0)
	} else {
		setListCursor(buildsView, 0, 0, 0)
	}

	outputView.Clear()
	if len(a.records) == 0 {
		fmt.Fprintln(outputView, "Select a scheme and simulator, then press b to build.")
	} else {
		a.loadSelectedOutput()
		record := a.records[a.buildIndex]
		output := ansiPattern.ReplaceAllString(a.outputs[record.ID], "")
		if !a.verboseOutput {
			output = conciseBuildOutput(output)
		}
		fmt.Fprint(outputView, output)
		if record.Error != "" {
			if output != "" && !strings.HasSuffix(output, "\n") {
				fmt.Fprintln(outputView)
			}
			fmt.Fprintf(outputView, "\n[%s] %s\n", phaseLabel(record.Phase), record.Error)
		}
		if record.Phase.Active() && a.outputFollow {
			scrollOutputToBottom(outputView)
		} else {
			clampOutputOrigin(outputView)
		}
	}
	return nil
}

func conciseBuildOutput(raw string) string {
	lines := strings.Split(raw, "\n")
	result := make([]string, 0, len(lines)/10)
	contextLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		diagnostic := isBuildDiagnostic(trimmed)
		include := strings.HasPrefix(trimmed, "[lazy-xcode]") ||
			diagnostic ||
			strings.Contains(trimmed, "** BUILD SUCCEEDED **") ||
			strings.Contains(trimmed, "** BUILD FAILED **")
		if diagnostic {
			contextLines = 3
		} else if contextLines > 0 {
			if trimmed == "" {
				contextLines = 0
			} else if line == strings.TrimLeft(line, " \t") && !strings.Contains(strings.ToLower(line), "note:") {
				contextLines = 0
			} else {
				include = true
				contextLines--
			}
		}
		if !include {
			continue
		}
		if trimmed == "" && (len(result) == 0 || result[len(result)-1] == "") {
			continue
		}
		result = append(result, line)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func isBuildDiagnostic(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, " error:") || strings.HasPrefix(lower, "error:") ||
		strings.Contains(lower, " warning:") || strings.HasPrefix(lower, "warning:") ||
		strings.Contains(lower, "fatal error:") || strings.HasPrefix(lower, "ld:") ||
		strings.Contains(lower, "undefined symbols for architecture") ||
		strings.Contains(lower, "duplicate symbol") ||
		strings.Contains(lower, "failed with a nonzero exit code") ||
		strings.HasPrefix(lower, "the following build commands failed:")
}

func formatBuildRow(record model.BuildRecord, width int) string {
	prefix := fmt.Sprintf("%-7s #%s ", phaseLabel(record.Phase), shortID(record.ID))
	duration := formatDuration(record.Duration(time.Now()))
	deviceWidth := max(3, width-len(prefix)-len(duration)-1)
	return truncate(prefix+fmt.Sprintf("%-*s %s", deviceWidth, truncate(record.Simulator.Name, deviceWidth), duration), width)
}

func writeViewLine(view *gocui.View, width int, line string) {
	fmt.Fprintln(view, truncate(line, max(0, width)))
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

func setListCursor(view *gocui.View, index, total, itemOffset int) {
	_, height := view.InnerSize()
	if height < 1 {
		return
	}
	if total == 0 {
		view.SetOrigin(0, 0)
		view.SetCursor(0, 0)
		return
	}
	absoluteRow := itemOffset + clamp(index, 0, total-1)
	_, origin := view.Origin()
	if absoluteRow < origin {
		origin = absoluteRow
	} else if absoluteRow >= origin+height {
		origin = absoluteRow - height + 1
	}
	lastRow := itemOffset + total - 1
	origin = clamp(origin, 0, max(0, lastRow-height+1))
	view.SetOrigin(0, origin)
	view.SetCursor(0, absoluteRow-origin)
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
	setListCursor(list, a.overlay.selected, len(items), itemOffset)
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

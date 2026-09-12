package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mwahlig/lazy-xcode/internal/model"
)

const targetPollInterval = 15 * time.Second

func (a *App) targetTick(now time.Time) {
	if a.mode == modeLocal && (a.lastTargetRefresh.IsZero() || now.Sub(a.lastTargetRefresh) >= targetPollInterval) {
		a.refreshTargets()
	}
}

func (a *App) cancelTargetRefresh() {
	if a.targetRefreshCancel != nil {
		a.targetRefreshCancel()
		a.targetRefreshCancel = nil
	}
	a.lastTargetRefresh = time.Time{}
}

// Refresh only destinations, leaving builds and project metadata in place.
func (a *App) refreshTargets() {
	if a.loading || a.targetRefreshCancel != nil || a.xcode == nil || a.ctx == nil || a.closing.Load() || a.scheme < 0 || a.scheme >= len(a.schemes) {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	a.targetRefreshCancel = cancel
	container, scheme, generation := a.container, a.schemes[a.scheme], a.generation.Load()
	go func() {
		defer cancel()
		targets, err := a.xcode.ListSimulators(ctx, container, scheme)
		a.update(func() {
			if generation != a.generation.Load() {
				return
			}
			a.targetRefreshCancel = nil
			a.lastTargetRefresh = a.clock()
			if err != nil || a.ctx.Err() != nil {
				return
			}
			a.applyTargets(targets)
		})
	}()
}

func (a *App) applyTargets(targets []model.Simulator) {
	selectedID := ""
	if a.simulator >= 0 && a.simulator < len(a.sims) {
		selectedID = a.sims[a.simulator].ID
	}
	a.sims = targets
	a.simulator = max(0, simulatorIndex(targets, selectedID))
	if a.status == "No compatible targets" || strings.HasPrefix(a.status, "Ready - ") {
		switch {
		case len(targets) == 0:
			a.status = "No compatible targets"
		case len(a.testTargets) == 0:
			a.status = targetSummary(targets) + "; no tests discovered"
		default:
			a.status = fmt.Sprintf("%s, %d test targets", targetSummary(targets), len(a.testTargets))
		}
	}
	if a.overlay == nil || a.overlay.kind != "simulator" {
		return
	}
	selectedID = ""
	items := a.filteredOverlayItems()
	if a.overlay.selected >= 0 && a.overlay.selected < len(items) {
		selectedID = items[a.overlay.selected].ID
	}
	a.overlay.items = targetItems(targets)
	a.overlay.selected = 0
	for i, item := range a.filteredOverlayItems() {
		if item.ID == selectedID {
			a.overlay.selected = i
			break
		}
	}
}

func targetItems(targets []model.Simulator) []overlayItem {
	items := make([]overlayItem, len(targets))
	for i, target := range targets {
		items[i] = overlayItem{ID: target.ID, Label: fmt.Sprintf("%-28s %-14s %-10s %s", target.Name, target.OS, target.KindLabel(), target.State)}
	}
	return items
}

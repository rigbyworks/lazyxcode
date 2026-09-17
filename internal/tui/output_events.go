package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	buildmanager "github.com/rigbyworks/lazyxcode/internal/build"
)

type buildUpdate struct {
	event buildmanager.Event
	order uint64
	log   *activityLog
}

// Manager delivers callbacks in sequence. Capture and parse before scheduling
// UI work, so even a blocked UI holds one notification, not one per log chunk.
func (a *App) handleBuildEvent(event buildmanager.Event) {
	if a.closing.Load() {
		return
	}
	a.buildEventMu.Lock()
	if a.captureLogs == nil {
		a.captureLogs = map[string]*activityLog{}
	}
	if a.pendingBuilds == nil {
		a.pendingBuilds = map[string]*buildUpdate{}
	}
	log := a.captureLogs[event.Record.ID]
	if log == nil {
		log = newActivityLog(event.Record.OperationKind())
		a.captureLogs[event.Record.ID] = log
	}
	if event.Output != "" {
		log.append(event.Output)
	}
	if !event.Record.Phase.Active() {
		log.finish()
	}
	// Do not retain event.Output: the ring already owns its bounded copy.
	event.Output = ""
	a.buildEventOrder++
	a.pendingBuilds[event.Record.ID] = &buildUpdate{event: event, log: log, order: a.buildEventOrder}
	schedule := !a.buildUpdateScheduled
	a.buildUpdateScheduled = true
	a.buildEventMu.Unlock()
	if schedule {
		time.AfterFunc(outputRefreshInterval, func() {
			if !a.closing.Load() {
				a.update(a.flushBuildEvents)
			}
		})
	}
}

func (a *App) flushBuildEvents() {
	a.buildEventMu.Lock()
	pending := a.pendingBuilds
	a.pendingBuilds = nil
	for id, update := range pending {
		if !update.event.Record.Phase.Active() {
			delete(a.captureLogs, id)
		}
	}
	a.buildUpdateScheduled = false
	a.buildEventMu.Unlock()
	updates := make([]*buildUpdate, 0, len(pending))
	for _, update := range pending {
		updates = append(updates, update)
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].order < updates[j].order })
	for _, update := range updates {
		a.applyBuildEvent(*update)
	}
}

// Retain active logs and the selected historical log only. Other history can
// be reloaded from disk without accumulating full logs as the user browses.
func (a *App) evictOutputs() {
	keep := map[string]bool{}
	for i, record := range a.records {
		if i == a.buildIndex || record.Phase.Active() {
			keep[record.ID] = true
		}
	}
	for id := range a.outputs {
		if !keep[id] {
			delete(a.outputs, id)
		}
	}
}

func (a *App) loadSelectedOutput() {
	if a.project == nil || len(a.records) == 0 || a.buildIndex >= len(a.records) {
		return
	}
	record := a.records[a.buildIndex]
	if a.pausedOutput != nil && a.pausedOutput.id != record.ID {
		a.pausedOutput = nil
	}
	if a.outputPage != nil && a.outputPage.id != record.ID {
		a.outputPage = nil
	}
	if a.outputLoadingID != "" && a.outputLoadingID != record.ID {
		a.outputLoadCancel()
		delete(a.outputs, a.outputLoadingID)
		a.outputLoadingID = ""
	}
	a.evictOutputs()
	if _, ok := a.outputs[record.ID]; ok {
		return
	}
	if a.outputs == nil {
		a.outputs = map[string]*activityLog{}
	}
	log := newActivityLog(record.OperationKind())
	a.outputs[record.ID] = log
	ctx, cancel := context.WithCancel(a.baseContext())
	a.outputLoadCancel, a.outputLoadingID = cancel, record.ID
	project := a.project
	go func() {
		defer cancel()
		err := project.CopyLog(ctx, record, log)
		log.finish()
		if ctx.Err() != nil {
			return
		}
		a.update(func() {
			if a.outputLoadingID != record.ID || a.outputs[record.ID] != log {
				return
			}
			a.outputLoadingID = ""
			if err != nil {
				a.status = fmt.Sprintf("Read build log: %v", err)
			}
		})
	}()
}

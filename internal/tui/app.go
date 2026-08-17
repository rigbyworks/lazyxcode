package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jesseduffield/gocui"
	buildmanager "github.com/mwahlig/lazy-xcode/internal/build"
	"github.com/mwahlig/lazy-xcode/internal/model"
	"github.com/mwahlig/lazy-xcode/internal/store"
	"github.com/mwahlig/lazy-xcode/internal/xcode"
)

type overlayItem struct {
	ID    string
	Label string
}

type overlayState struct {
	kind        string
	title       string
	items       []overlayItem
	selected    int
	filter      string
	message     string
	initialized bool
}

type App struct {
	ctx         context.Context
	directory   string
	xcode       *xcode.Client
	preferences *store.Preferences
	containers  []model.Container

	gui       *gocui.Gui
	closing   atomic.Bool
	container model.Container
	project   *store.Project
	manager   *buildmanager.Manager
	schemes   []string
	scheme    int
	sims      []model.Simulator
	simulator int

	records       []model.BuildRecord
	buildIndex    int
	outputs       map[string]string
	eventNext     map[string]uint64
	eventQueue    map[string]map[uint64]buildmanager.Event
	focus         string
	configRow     int
	status        string
	cacheSize     int64
	loading       bool
	outputFollow  bool
	verboseOutput bool
	overlay       *overlayState
	generation    atomic.Uint64
}

func Run(ctx context.Context, directory string, client *xcode.Client, preferences *store.Preferences, containers []model.Container) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true})
	if err != nil {
		return err
	}
	defer g.Close()

	a := &App{
		ctx: runContext, directory: directory, xcode: client, preferences: preferences,
		containers: containers, gui: g, focus: "build", outputs: map[string]string{},
		eventNext: map[string]uint64{}, eventQueue: map[string]map[uint64]buildmanager.Event{},
	}
	g.Mouse = true
	g.Highlight = true
	g.SelFrameColor = gocui.ColorGreen
	g.ShowListFooter = true
	g.SupportOverlaps = true
	g.SetManagerFunc(a.layout)
	if err := a.bindKeys(g); err != nil {
		return err
	}
	go a.refreshElapsedTimes(runContext)

	if chosen, ok := a.rememberedContainer(); ok {
		a.chooseContainer(chosen)
	} else if len(containers) == 1 {
		a.chooseContainer(0)
	} else {
		a.openContainerPicker()
	}
	loopErr := g.MainLoop()
	a.closing.Store(true)
	cancel()
	if a.manager != nil {
		a.manager.CancelAll()
	}
	if a.manager != nil {
		a.manager.Wait()
	}
	if loopErr != nil && !gocui.IsQuit(loopErr) {
		return loopErr
	}
	return nil
}

func (a *App) refreshElapsedTimes(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.closing.Load() {
				return
			}
			a.gui.Update(func(*gocui.Gui) error { return nil })
		}
	}
}

func (a *App) rememberedContainer() (int, bool) {
	path := a.preferences.Container(a.directory)
	for i := range a.containers {
		if a.containers[i].Path == path {
			return i, true
		}
	}
	return 0, false
}

func (a *App) update(fn func()) {
	if a.gui == nil || a.closing.Load() {
		return
	}
	a.gui.Update(func(*gocui.Gui) error { fn(); return nil })
}

func (a *App) chooseContainer(index int) {
	if index < 0 || index >= len(a.containers) {
		return
	}
	a.container = a.containers[index]
	a.overlay = nil
	a.loading = true
	a.status = "Loading schemes..."
	_ = a.preferences.SetContainer(a.directory, a.container.Path)
	container := a.container
	generation := a.generation.Add(1)
	go func() {
		project, projectErr := store.NewProject(container)
		var records []model.BuildRecord
		if projectErr == nil {
			records, projectErr = project.Load()
		}
		schemes, schemeErr := a.xcode.ListSchemes(a.ctx, container)
		a.update(func() {
			if generation != a.generation.Load() {
				return
			}
			if projectErr != nil {
				a.loading = false
				a.status = "Load history: " + projectErr.Error()
				return
			}
			a.project = project
			a.records = records
			a.manager = buildmanager.NewManager(a.xcode, project, a.handleBuildEvent)
			if schemeErr != nil {
				a.loading = false
				a.status = schemeErr.Error()
				return
			}
			a.schemes = schemes
			a.scheme = indexOf(schemes, a.preferences.Scheme(a.container.Path))
			if a.scheme < 0 {
				a.scheme = 0
			}
			if len(schemes) == 0 {
				a.loading = false
				a.status = "No schemes found"
				return
			}
			a.loadSimulators()
			a.loadSelectedOutput()
		})
	}()
}

func (a *App) loadSimulators() {
	if len(a.schemes) == 0 || a.scheme >= len(a.schemes) {
		return
	}
	scheme := a.schemes[a.scheme]
	container := a.container
	project := a.project
	a.loading = true
	a.sims = nil
	a.status = "Loading compatible simulators for " + scheme + "..."
	generation := a.generation.Add(1)
	go func() {
		simulators, err := a.xcode.ListSimulators(a.ctx, container, scheme)
		a.update(func() {
			if generation != a.generation.Load() {
				return
			}
			a.loading = false
			if err != nil {
				a.status = err.Error()
				return
			}
			a.sims = simulators
			a.simulator = simulatorIndex(simulators, a.preferences.Simulator(container.Path, scheme))
			if a.simulator < 0 {
				a.simulator = 0
			}
			if len(simulators) == 0 {
				a.status = "No compatible installed simulators"
			} else {
				a.status = fmt.Sprintf("Ready - %d compatible simulators", len(simulators))
			}
			a.refreshCacheSize(project)
		})
	}()
}

func (a *App) refreshCacheSize(project *store.Project) {
	if project == nil {
		return
	}
	go func() {
		size, err := project.CacheSize()
		a.update(func() {
			if err == nil && project == a.project {
				a.cacheSize = size
			}
		})
	}()
}

func indexOf(items []string, value string) int {
	for i, item := range items {
		if item == value {
			return i
		}
	}
	return -1
}

func simulatorIndex(items []model.Simulator, id string) int {
	for i := range items {
		if items[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *App) handleBuildEvent(event buildmanager.Event) {
	a.update(func() {
		a.queueBuildEvent(event)
	})
}

func (a *App) queueBuildEvent(event buildmanager.Event) {
	if event.Sequence == 0 {
		a.applyBuildEvent(event)
		return
	}
	if a.eventQueue == nil {
		a.eventQueue = map[string]map[uint64]buildmanager.Event{}
	}
	if a.eventNext == nil {
		a.eventNext = map[string]uint64{}
	}
	if a.eventQueue[event.Record.ID] == nil {
		a.eventQueue[event.Record.ID] = map[uint64]buildmanager.Event{}
	}
	a.eventQueue[event.Record.ID][event.Sequence] = event
	next := a.eventNext[event.Record.ID]
	if next == 0 {
		next = 1
	}
	for {
		queued, ok := a.eventQueue[event.Record.ID][next]
		if !ok {
			break
		}
		delete(a.eventQueue[event.Record.ID], next)
		a.applyBuildEvent(queued)
		next++
	}
	a.eventNext[event.Record.ID] = next
}

func (a *App) applyBuildEvent(event buildmanager.Event) {
	found := false
	for i := range a.records {
		if a.records[i].ID == event.Record.ID {
			a.records[i] = event.Record
			found = true
			break
		}
	}
	if !found {
		a.records = append([]model.BuildRecord{event.Record}, a.records...)
		a.buildIndex = 0
	}
	if event.Output != "" {
		if a.outputs == nil {
			a.outputs = map[string]string{}
		}
		a.outputs[event.Record.ID] += event.Output
	}
	a.status = statusForRecord(event.Record)
}

func statusForRecord(record model.BuildRecord) string {
	if record.Error != "" {
		return strings.ReplaceAll(record.Error, "\n", " ")
	}
	return fmt.Sprintf("%s - %s on %s", phaseLabel(record.Phase), record.Scheme, record.Simulator.Name)
}

func (a *App) loadSelectedOutput() {
	if a.project == nil || len(a.records) == 0 || a.buildIndex >= len(a.records) {
		return
	}
	record := a.records[a.buildIndex]
	if _, ok := a.outputs[record.ID]; ok {
		return
	}
	data, err := a.project.ReadLog(record)
	if err != nil {
		a.outputs[record.ID] = "Unable to read build log: " + err.Error()
		return
	}
	a.outputs[record.ID] = string(data)
}

func (a *App) openContainerPicker() {
	items := make([]overlayItem, len(a.containers))
	for i, container := range a.containers {
		label := container.Name
		if container.Kind == model.Workspace {
			label += "  [workspace]"
		} else {
			label += "  [project]"
		}
		items[i] = overlayItem{ID: container.Path, Label: label}
	}
	a.overlay = &overlayState{kind: "container", title: "Select Xcode Container", items: items}
}

package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jesseduffield/gocui"
	buildmanager "github.com/rigbyworks/lazyxcode/internal/build"
	"github.com/rigbyworks/lazyxcode/internal/model"
	"github.com/rigbyworks/lazyxcode/internal/store"
	"github.com/rigbyworks/lazyxcode/internal/xcode"
	"github.com/rigbyworks/lazyxcode/internal/xcodecloud"
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
	origin      int
	filter      string
	message     string
	initialized bool
	choose      func(string)
	parent      *overlayState
	cancel      context.CancelFunc
}

type App struct {
	ctx         context.Context
	directory   string
	xcode       *xcode.Client
	preferences *store.Preferences
	containers  []model.Container

	gui              *gocui.Gui
	closing          atomic.Bool
	container        model.Container
	project          *store.Project
	manager          *buildmanager.Manager
	schemes          []string
	scheme           int
	sims             []model.Simulator
	simulator        int
	testTargets      []model.TestTarget
	testCoverageOff  bool
	testOutput       *testOutputView
	discoveringTests string

	outputPage          *outputPage
	targetRefreshCancel context.CancelFunc
	lastTargetRefresh   time.Time

	records              []model.BuildRecord
	buildIndex           int
	outputs              map[string]*activityLog
	buildEventMu         sync.Mutex
	pendingBuilds        map[string]*buildUpdate
	captureLogs          map[string]*activityLog
	buildUpdateScheduled bool
	buildEventOrder      uint64
	outputLoadCancel     context.CancelFunc
	outputLoadingID      string
	renderedOutput       string
	outputFullText       string
	pausedOutput         *outputSnapshot
	renderedOutputView   *gocui.View
	focus                string
	configRow            int
	status               string
	cacheSize            int64
	loading              bool
	outputFollow         bool
	verboseOutput        bool
	overlay              *overlayState
	renderedOverlay      *overlayState
	generation           atomic.Uint64

	mode              appMode
	cloud             *cloudState
	cloudConnect      func() (xcodecloud.Service, []string, error)
	copyToClipboard   func(string) error
	openXcode         func(string) error
	localOutputOrigin int
	dispatch          func(func())
	now               func() time.Time
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
		containers: containers, gui: g, focus: "build", outputs: map[string]*activityLog{},
		copyToClipboard: copyToClipboard,
		openXcode:       openXcode,
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
	if a.outputLoadCancel != nil {
		a.outputLoadCancel()
	}
	cancel()
	if a.manager != nil {
		a.manager.CancelAll()
	}
	if a.cloud != nil {
		a.cloud.cancelAll()
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
			a.gui.Update(func(*gocui.Gui) error {
				a.cloudTick(time.Now())
				a.targetTick(time.Now())
				return nil
			})
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
	if a.dispatch != nil {
		a.dispatch(fn)
		return
	}
	if a.gui == nil || a.closing.Load() {
		return
	}
	a.gui.Update(func(*gocui.Gui) error { fn(); return nil })
}

func (a *App) chooseContainer(index int) {
	if index < 0 || index >= len(a.containers) {
		return
	}
	a.cancelTargetRefresh()
	if a.outputLoadCancel != nil {
		a.outputLoadCancel()
	}
	a.outputLoadingID = ""
	a.outputPage = nil
	a.outputs = map[string]*activityLog{}
	a.schemes = nil
	a.sims = nil
	a.testTargets = nil
	a.container = a.containers[index]
	a.testCoverageOff = a.preferences.CoverageDisabled(a.container.Path)
	a.overlay = nil
	a.loading = true
	a.status = "Loading schemes..."
	_ = a.preferences.SetContainer(a.directory, a.container.Path)
	a.resetCloudSelection()
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
	a.cancelTargetRefresh()
	scheme := a.schemes[a.scheme]
	container := a.container
	project := a.project
	a.loading = true
	a.sims = nil
	a.status = "Loading compatible targets for " + scheme + "..."
	generation := a.generation.Add(1)
	go func() {
		simulators, err := a.xcode.ListSimulators(a.ctx, container, scheme)
		testTargets, testErr := a.xcode.ListTestTargets(container, scheme)
		a.update(func() {
			if generation != a.generation.Load() {
				return
			}
			a.loading = false
			a.lastTargetRefresh = a.clock()
			if err != nil {
				a.status = err.Error()
				return
			}
			a.sims = simulators
			a.testTargets = testTargets
			a.simulator = simulatorIndex(simulators, a.preferences.Simulator(container.Path, scheme))
			if a.simulator < 0 {
				a.simulator = 0
			}
			if len(simulators) == 0 {
				a.status = "No compatible targets"
			} else if testErr != nil || len(testTargets) == 0 {
				a.status = targetSummary(simulators) + "; no tests discovered"
			} else {
				a.status = fmt.Sprintf("%s, %d test targets", targetSummary(simulators), len(testTargets))
			}
			a.refreshCacheSize(project)
		})
	}()
}

func targetSummary(targets []model.Simulator) string {
	devices, macs := 0, 0
	for _, target := range targets {
		if target.IsMac() {
			macs++
		} else if target.Physical {
			devices++
		}
	}
	macLabel := "Macs"
	if macs == 1 {
		macLabel = "Mac"
	}
	return fmt.Sprintf("Ready - %d simulators, %d devices, %d %s", len(targets)-devices-macs, devices, macs, macLabel)
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

func (a *App) applyBuildEvent(update buildUpdate) {
	event := update.event
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
	if a.outputs == nil {
		a.outputs = map[string]*activityLog{}
	}
	a.outputs[event.Record.ID] = update.log
	a.evictOutputs()

	a.status = statusForRecord(event.Record)
	if event.Record.ID == a.discoveringTests && !event.Record.Phase.Active() {
		a.discoveringTests = ""
		if event.Record.Phase == model.PhaseSucceeded && a.mode == modeLocal && a.overlay == nil && len(a.records) > 0 && a.records[a.buildIndex].ID == event.Record.ID {
			a.openDiscoveredTests(event.Record)
		}
	}
}

func statusForRecord(record model.BuildRecord) string {
	if record.Error != "" {
		return strings.ReplaceAll(record.Error, "\n", " ")
	}
	return fmt.Sprintf("%s - %s on %s", recordPhaseLabel(record), record.Scheme, record.Simulator.Name)
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

package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mwahlig/lazy-xcode/internal/model"
	"github.com/mwahlig/lazy-xcode/internal/store"
)

type Executor interface {
	Build(context.Context, io.Writer, model.Container, string, model.Simulator, string) error
	Test(context.Context, io.Writer, model.Container, string, model.Simulator, string, model.TestOptions) error
	Product(context.Context, model.Container, string, model.Simulator, string) (model.Product, error)
	Boot(context.Context, model.Simulator) error
	Install(context.Context, model.Simulator, model.Product) error
	Launch(context.Context, io.Writer, model.Simulator, model.Product) error
}

type Event struct {
	Record   model.BuildRecord
	Output   string
	Sequence uint64
}

type Request struct {
	Container model.Container
	Scheme    string
	Simulator model.Simulator
	Operation model.Operation
	TestScope string
	Targets   []string
	Coverage  bool
}

type Manager struct {
	mu       sync.Mutex
	executor Executor
	store    *store.Project
	jobs     map[string]context.CancelFunc
	active   map[string]string
	phases   map[string]model.Phase
	onEvent  func(Event)
	sequence atomic.Uint64
	events   sync.Map
	wg       sync.WaitGroup
}

func NewManager(executor Executor, projectStore *store.Project, onEvent func(Event)) *Manager {
	return &Manager{
		executor: executor, store: projectStore, jobs: map[string]context.CancelFunc{},
		active: map[string]string{}, phases: map[string]model.Phase{}, onEvent: onEvent,
	}
}

func (m *Manager) Start(parent context.Context, request Request) (model.BuildRecord, error) {
	key := request.Scheme + "\x00" + request.Simulator.ID
	m.mu.Lock()
	if id := m.active[key]; id != "" {
		if m.phases[id] != model.PhaseRunning {
			m.mu.Unlock()
			return model.BuildRecord{}, fmt.Errorf("an activity for %s on %s is already active", request.Scheme, request.Simulator.Name)
		}
		m.jobs[id]()
	}
	id := fmt.Sprintf("%d-%03d", time.Now().UnixMilli(), m.sequence.Add(1))
	derivedData := m.store.DerivedData(request.Scheme, request.Simulator.ID)
	ctx, cancel := context.WithCancel(parent)
	record := model.BuildRecord{
		ID: id, Container: request.Container, Scheme: request.Scheme, Simulator: request.Simulator,
		Phase: model.PhaseQueued, Operation: request.Operation, TestScope: request.TestScope, TestTargets: append([]string(nil), request.Targets...), Coverage: request.Coverage,
		StartedAt: time.Now(), DerivedDataKey: derivedData,
	}
	m.jobs[id] = cancel
	m.active[key] = id
	m.phases[id] = record.Phase
	m.events.Store(id, &atomic.Uint64{})
	m.mu.Unlock()

	logFile, logPath, err := m.store.NewLog(id)
	if err != nil {
		m.release(record, key)
		return model.BuildRecord{}, err
	}
	record.LogPath = logPath
	if request.Operation == model.OperationTest || request.Operation == model.OperationDiscoverTests {
		dir, err := m.store.NewResultDir(id)
		if err != nil {
			logFile.Close()
			m.release(record, key)
			return model.BuildRecord{}, err
		}
		if request.Operation == model.OperationDiscoverTests {
			record.EnumerationPath = filepath.Join(dir, "tests.json")
		} else {
			record.ResultBundlePath = filepath.Join(dir, "Results.xcresult")
		}
	}
	m.persist(record)
	m.emit(Event{Record: record})
	m.wg.Add(1)
	go m.run(ctx, key, record, logFile)
	return record, nil
}

func (m *Manager) run(ctx context.Context, key string, record model.BuildRecord, logFile *os.File) {
	defer m.wg.Done()
	defer logFile.Close()
	defer m.release(record, key)
	writer := &eventWriter{file: logFile, record: &record, emit: m.emit}
	if record.OperationKind() == model.OperationTest || record.OperationKind() == model.OperationDiscoverTests {
		m.runTests(ctx, &record, writer)
		return
	}
	_, _ = fmt.Fprintf(writer, "[lazy-xcode] Building %s for %s\n\n", record.Scheme, record.Simulator.Label())

	if !m.stage(ctx, &record, model.PhaseBuilding, func() error {
		progress := newProgressWriter(writer, time.Now)
		err := m.executor.Build(ctx, progress, record.Container, record.Scheme, record.Simulator, record.DerivedDataKey)
		progress.Finish()
		return err
	}, model.PhaseBuildFailed) {
		return
	}
	if record.OperationKind() == model.OperationBuild {
		m.finish(ctx, &record, model.PhaseSucceeded, nil)
		return
	}
	product, err := m.executor.Product(ctx, record.Container, record.Scheme, record.Simulator, record.DerivedDataKey)
	if err != nil {
		m.finish(ctx, &record, model.PhaseRunFailed, err)
		return
	}
	if record.Simulator.IsMac() {
		_, _ = fmt.Fprintf(writer, "\n[lazy-xcode] Build succeeded; launching on %s\n", record.Simulator.Name)
	} else if record.Simulator.Physical {
		_, _ = fmt.Fprintf(writer, "\n[lazy-xcode] Build succeeded; deploying to %s\n", record.Simulator.Name)
	} else {
		_, _ = fmt.Fprintf(writer, "\n[lazy-xcode] Build succeeded; opening %s in Simulator\n", record.Simulator.Name)
		if !m.stage(ctx, &record, model.PhaseBooting, func() error { return m.executor.Boot(ctx, record.Simulator) }, model.PhaseRunFailed) {
			return
		}
	}
	if !record.Simulator.IsMac() {
		_, _ = fmt.Fprintln(writer, "[lazy-xcode] Installing app")
		if !m.stage(ctx, &record, model.PhaseInstalling, func() error { return m.executor.Install(ctx, record.Simulator, product) }, model.PhaseRunFailed) {
			return
		}
	}
	_, _ = fmt.Fprintln(writer, "[lazy-xcode] Launching "+product.BundleID)
	phase := model.PhaseLaunching
	if !record.Simulator.IsMac() {
		phase = model.PhaseRunning
		_, _ = fmt.Fprintln(writer, "[lazy-xcode] App console")
	}
	if !m.stage(ctx, &record, phase, func() error { return m.executor.Launch(ctx, writer, record.Simulator, product) }, model.PhaseRunFailed) {
		return
	}
	m.finish(ctx, &record, model.PhaseSucceeded, nil)
}

func (m *Manager) runTests(ctx context.Context, record *model.BuildRecord, writer *eventWriter) {
	scope := record.TestScope
	if scope == "" {
		scope = "All Tests"
	}
	_, _ = fmt.Fprintf(writer, "[lazy-xcode] Testing %s on %s — %s\n\n", record.Scheme, record.Simulator.Label(), scope)
	if record.Simulator.IsSimulator() {
		_, _ = fmt.Fprintln(writer, "[lazy-xcode] Opening simulator for tests")
		if !m.stage(ctx, record, model.PhaseBooting, func() error { return m.executor.Boot(ctx, record.Simulator) }, model.PhaseRunFailed) {
			return
		}
	}
	if !m.stage(ctx, record, model.PhaseTesting, func() error {
		progress := newProgressWriter(writer, time.Now)
		err := m.executor.Test(ctx, progress, record.Container, record.Scheme, record.Simulator, record.DerivedDataKey, model.TestOptions{Targets: record.TestTargets, ResultBundlePath: record.ResultBundlePath, EnumerationPath: record.EnumerationPath, Coverage: record.Coverage})
		progress.Finish()
		return err
	}, model.PhaseTestFailed) {
		return
	}
	m.finish(ctx, record, model.PhaseSucceeded, nil)
}

func (m *Manager) stage(ctx context.Context, record *model.BuildRecord, phase model.Phase, action func() error, failure model.Phase) bool {
	record.Phase, record.Error = phase, ""
	m.setPhase(record.ID, phase)
	m.persist(*record)
	m.emit(Event{Record: *record})
	err := action()
	if err != nil {
		m.finish(ctx, record, failure, err)
		return false
	}
	return true
}

func (m *Manager) finish(ctx context.Context, record *model.BuildRecord, phase model.Phase, err error) {
	if errors.Is(ctx.Err(), context.Canceled) {
		phase = model.PhaseCancelled
	}
	now := time.Now()
	record.Phase = phase
	m.setPhase(record.ID, phase)
	record.FinishedAt = &now
	if err != nil && phase != model.PhaseCancelled {
		record.Error = err.Error()
	} else if phase == model.PhaseCancelled {
		record.Error = "cancelled by user"
	}
	m.persist(*record)
	m.emit(Event{Record: *record})
}

func (m *Manager) persist(record model.BuildRecord) {
	if _, err := m.store.Save(record); err != nil {
		record.Error = "save build history: " + err.Error()
		m.emit(Event{Record: record})
	}
}

func (m *Manager) emit(event Event) {
	if m.onEvent != nil {
		if counter, ok := m.events.Load(event.Record.ID); ok {
			event.Sequence = counter.(*atomic.Uint64).Add(1)
		}
		m.onEvent(event)
	}
}

func (m *Manager) release(record model.BuildRecord, key string) {
	m.mu.Lock()
	delete(m.jobs, record.ID)
	delete(m.phases, record.ID)
	if m.active[key] == record.ID {
		delete(m.active, key)
	}
	m.mu.Unlock()
	m.events.Delete(record.ID)
}

func (m *Manager) setPhase(id string, phase model.Phase) {
	m.mu.Lock()
	m.phases[id] = phase
	m.mu.Unlock()
}

func (m *Manager) Stop(id string) bool {
	m.mu.Lock()
	cancel := m.jobs[id]
	m.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (m *Manager) HasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.jobs) > 0
}

func (m *Manager) CancelAll() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.jobs))
	for _, cancel := range m.jobs {
		cancels = append(cancels, cancel)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *Manager) Wait() { m.wg.Wait() }

type eventWriter struct {
	mu     sync.Mutex
	file   *os.File
	record *model.BuildRecord
	emit   func(Event)
}

func (w *eventWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.file.Write(data)
	if n > 0 {
		w.emit(Event{Record: *w.record, Output: string(data[:n])})
	}
	return n, err
}

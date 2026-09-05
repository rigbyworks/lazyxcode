package build

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mwahlig/lazy-xcode/internal/model"
	"github.com/mwahlig/lazy-xcode/internal/store"
)

type fakeExecutor struct {
	started    chan string
	release    chan struct{}
	runRelease chan struct{}
	booted     chan string
	installed  chan string
	launched   chan string
}

func (f *fakeExecutor) Build(ctx context.Context, writer io.Writer, _ model.Container, _ string, simulator model.Simulator, _ string) error {
	f.started <- simulator.ID
	_, _ = io.WriteString(writer, "compile "+simulator.Name+"\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.release:
		return nil
	}
}

func (f *fakeExecutor) Test(ctx context.Context, writer io.Writer, _ model.Container, _ string, simulator model.Simulator, _ string, targets []string) error {
	f.started <- "test:" + simulator.ID
	_, _ = io.WriteString(writer, "ran tests "+strings.Join(targets, ",")+"\n")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.release:
		return nil
	}
}

func (*fakeExecutor) Product(context.Context, model.Container, string, model.Simulator, string) (model.Product, error) {
	return model.Product{AppPath: "/tmp/App.app", BundleID: "com.example.app"}, nil
}
func (f *fakeExecutor) Boot(_ context.Context, simulator model.Simulator) error {
	if f.booted != nil {
		f.booted <- simulator.ID
	}
	return nil
}
func (f *fakeExecutor) Install(_ context.Context, simulator model.Simulator, _ model.Product) error {
	if f.installed != nil {
		f.installed <- simulator.ID
	}
	return nil
}
func (f *fakeExecutor) Launch(ctx context.Context, writer io.Writer, simulator model.Simulator, _ model.Product) error {
	_, _ = io.WriteString(writer, "app console output\n")
	if f.launched != nil {
		f.launched <- simulator.ID
	}
	if f.runRelease != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.runRelease:
		}
	}
	return nil
}

func newTestManager(t *testing.T, executor Executor, onEvent func(Event)) *Manager {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	project, err := store.NewProject(model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"})
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(executor, project, onEvent)
}

func TestManagerRunsDistinctDestinationsConcurrently(t *testing.T) {
	executor := &fakeExecutor{started: make(chan string, 2), release: make(chan struct{})}
	var mu sync.Mutex
	final := map[string]model.Phase{}
	manager := newTestManager(t, executor, func(event Event) {
		mu.Lock()
		final[event.Record.ID] = event.Record.Phase
		mu.Unlock()
	})
	container := model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}
	one, err := manager.Start(context.Background(), Request{Container: container, Scheme: "App", Simulator: model.Simulator{ID: "PHONE", Name: "iPhone"}})
	if err != nil {
		t.Fatal(err)
	}
	two, err := manager.Start(context.Background(), Request{Container: container, Scheme: "App", Simulator: model.Simulator{ID: "PAD", Name: "iPad"}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{<-executor.started: true, <-executor.started: true}
	if !seen["PHONE"] || !seen["PAD"] {
		t.Fatalf("started destinations = %#v", seen)
	}
	if _, err := manager.Start(context.Background(), Request{Container: container, Scheme: "App", Simulator: model.Simulator{ID: "PHONE", Name: "iPhone"}}); err == nil {
		t.Fatal("duplicate active build was accepted")
	}
	close(executor.release)
	manager.Wait()
	mu.Lock()
	defer mu.Unlock()
	if final[one.ID] != model.PhaseSucceeded || final[two.ID] != model.PhaseSucceeded {
		t.Fatalf("final phases = %#v", final)
	}
}

func TestStoppingOneBuildDoesNotStopAnother(t *testing.T) {
	executor := &fakeExecutor{started: make(chan string, 2), release: make(chan struct{})}
	var mu sync.Mutex
	final := map[string]model.Phase{}
	cancelled := make(chan struct{})
	var cancelledOnce sync.Once
	manager := newTestManager(t, executor, func(event Event) {
		mu.Lock()
		final[event.Record.ID] = event.Record.Phase
		mu.Unlock()
		if event.Record.Simulator.ID == "PHONE" && event.Record.Phase == model.PhaseCancelled {
			cancelledOnce.Do(func() { close(cancelled) })
		}
	})
	container := model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}
	one, _ := manager.Start(context.Background(), Request{Container: container, Scheme: "App", Simulator: model.Simulator{ID: "PHONE", Name: "iPhone"}})
	two, _ := manager.Start(context.Background(), Request{Container: container, Scheme: "App", Simulator: model.Simulator{ID: "PAD", Name: "iPad"}})
	<-executor.started
	<-executor.started
	if !manager.Stop(one.ID) {
		t.Fatal("active build was not stopped")
	}
	<-cancelled
	close(executor.release)
	manager.Wait()
	mu.Lock()
	defer mu.Unlock()
	if final[one.ID] != model.PhaseCancelled || final[two.ID] != model.PhaseSucceeded {
		t.Fatalf("final phases = %#v", final)
	}
}

func TestManagerRunsSelectedTestTargetsWithoutDeploying(t *testing.T) {
	executor := &fakeExecutor{started: make(chan string, 1), release: make(chan struct{})}
	var mu sync.Mutex
	var output string
	var final model.BuildRecord
	manager := newTestManager(t, executor, func(event Event) {
		mu.Lock()
		output += event.Output
		final = event.Record
		mu.Unlock()
	})
	record, err := manager.Start(context.Background(), Request{
		Container: model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}, Scheme: "App",
		Simulator: model.Simulator{ID: "PHONE", Name: "iPhone"}, Operation: model.OperationTest,
		TestScope: "Unit Tests", Targets: []string{"AppTests"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started := <-executor.started; started != "test:PHONE" {
		t.Fatalf("started %q, want test operation", started)
	}
	close(executor.release)
	manager.Wait()
	mu.Lock()
	defer mu.Unlock()
	if final.ID != record.ID || final.OperationKind() != model.OperationTest || final.Phase != model.PhaseSucceeded {
		t.Fatalf("final record = %#v", final)
	}
	for _, expected := range []string{"Testing App", "Unit Tests", "ran tests AppTests"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("test output missing %q: %s", expected, output)
		}
	}
}

func TestManagerSkipsSimulatorBootForPhysicalDevice(t *testing.T) {
	executor := &fakeExecutor{started: make(chan string, 1), release: make(chan struct{}), booted: make(chan string, 1)}
	var mu sync.Mutex
	var output string
	manager := newTestManager(t, executor, func(event Event) {
		mu.Lock()
		output += event.Output
		mu.Unlock()
	})
	_, err := manager.Start(context.Background(), Request{
		Container: model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}, Scheme: "App",
		Simulator: model.Simulator{ID: "DEVICE", Name: "Matt's iPhone", Physical: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	<-executor.started
	close(executor.release)
	manager.Wait()
	if len(executor.booted) != 0 {
		t.Fatal("physical device was passed through simulator boot")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(output, "deploying to Matt's iPhone") || strings.Contains(output, "in Simulator") {
		t.Fatalf("output = %q", output)
	}
}

func TestManagerLaunchesMacWithoutBootingOrInstalling(t *testing.T) {
	executor := &fakeExecutor{
		started: make(chan string, 1), release: make(chan struct{}), booted: make(chan string, 1),
		installed: make(chan string, 1), launched: make(chan string, 1),
	}
	var mu sync.Mutex
	var output string
	manager := newTestManager(t, executor, func(event Event) {
		mu.Lock()
		output += event.Output
		mu.Unlock()
	})
	_, err := manager.Start(context.Background(), Request{
		Container: model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}, Scheme: "App",
		Simulator: model.Simulator{ID: "MAC", Name: "My Mac", Platform: "macOS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	<-executor.started
	close(executor.release)
	manager.Wait()
	if len(executor.booted) != 0 || len(executor.installed) != 0 {
		t.Fatalf("Mac deployment called boot/install: booted=%d installed=%d", len(executor.booted), len(executor.installed))
	}
	if launched := <-executor.launched; launched != "MAC" {
		t.Fatalf("launched %q, want MAC", launched)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(output, "launching on My Mac") || strings.Contains(output, "Installing app") {
		t.Fatalf("output = %q", output)
	}
}

func TestManagerStreamsConsoleWhileAppIsRunning(t *testing.T) {
	executor := &fakeExecutor{
		started: make(chan string, 1), release: make(chan struct{}),
		runRelease: make(chan struct{}), launched: make(chan string, 1),
	}
	var mu sync.Mutex
	var output string
	var current model.BuildRecord
	manager := newTestManager(t, executor, func(event Event) {
		mu.Lock()
		output += event.Output
		current = event.Record
		mu.Unlock()
	})
	record, err := manager.Start(context.Background(), Request{
		Container: model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}, Scheme: "App",
		Simulator: model.Simulator{ID: "PHONE", Name: "iPhone"},
	})
	if err != nil {
		t.Fatal(err)
	}
	<-executor.started
	close(executor.release)
	<-executor.launched

	mu.Lock()
	phase, streamed := current.Phase, output
	mu.Unlock()
	if phase != model.PhaseRunning || !manager.HasActive() {
		t.Fatalf("activity phase = %q, active = %t", phase, manager.HasActive())
	}
	for _, expected := range []string{"[lazy-xcode] App console", "app console output"} {
		if !strings.Contains(streamed, expected) {
			t.Fatalf("runtime output missing %q: %s", expected, streamed)
		}
	}

	close(executor.runRelease)
	manager.Wait()
	mu.Lock()
	defer mu.Unlock()
	if current.ID != record.ID || current.Phase != model.PhaseSucceeded {
		t.Fatalf("final record = %#v", current)
	}
}

func TestStartingBuildReplacesRunningAppOnSameDestination(t *testing.T) {
	executor := &fakeExecutor{
		started: make(chan string, 2), release: make(chan struct{}),
		runRelease: make(chan struct{}), launched: make(chan string, 2),
	}
	var mu sync.Mutex
	final := map[string]model.Phase{}
	manager := newTestManager(t, executor, func(event Event) {
		mu.Lock()
		final[event.Record.ID] = event.Record.Phase
		mu.Unlock()
	})
	request := Request{
		Container: model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"}, Scheme: "App",
		Simulator: model.Simulator{ID: "PHONE", Name: "iPhone"},
	}
	first, err := manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	<-executor.started
	close(executor.release)
	<-executor.launched

	second, err := manager.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("start replacement build: %v", err)
	}
	<-executor.started
	<-executor.launched
	close(executor.runRelease)
	manager.Wait()

	mu.Lock()
	defer mu.Unlock()
	if final[first.ID] != model.PhaseCancelled || final[second.ID] != model.PhaseSucceeded {
		t.Fatalf("final phases = %#v", final)
	}
}

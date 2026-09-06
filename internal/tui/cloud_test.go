package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jesseduffield/gocui"
	buildmanager "github.com/mwahlig/lazy-xcode/internal/build"
	"github.com/mwahlig/lazy-xcode/internal/model"
	"github.com/mwahlig/lazy-xcode/internal/store"
	"github.com/mwahlig/lazy-xcode/internal/xcodecloud"
)

type fakeCloud struct {
	mu        sync.Mutex
	products  []xcodecloud.Product
	workflows map[string][]xcodecloud.Workflow
	pages     map[string]xcodecloud.BuildRunPage
	details   map[string]xcodecloud.BuildDetails
	errs      map[string]error
	calls     []string
	block     chan struct{}
	content   map[string]string
	downloads int
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{
		workflows: map[string][]xcodecloud.Workflow{}, pages: map[string]xcodecloud.BuildRunPage{},
		details: map[string]xcodecloud.BuildDetails{}, errs: map[string]error{}, content: map[string]string{},
	}
}

func (f *fakeCloud) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeCloud) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeCloud) wait(ctx context.Context) error {
	if f.block == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.block:
		return nil
	}
}

func (f *fakeCloud) ListProducts(ctx context.Context) ([]xcodecloud.Product, error) {
	f.record("products")
	if err := f.errs["products"]; err != nil {
		return nil, err
	}
	return f.products, nil
}

func (f *fakeCloud) ListWorkflows(ctx context.Context, product string) ([]xcodecloud.Workflow, error) {
	f.record("workflows:" + product)
	if err := f.errs["workflows"]; err != nil {
		return nil, err
	}
	return f.workflows[product], nil
}

func (f *fakeCloud) ListBuildRuns(ctx context.Context, query xcodecloud.BuildRunQuery) (xcodecloud.BuildRunPage, error) {
	key := "runs:" + query.ProductID + ":" + query.WorkflowID
	if query.Cursor != "" {
		key = "cursor:" + query.Cursor
	}
	f.record(key)
	if err := f.wait(ctx); err != nil {
		return xcodecloud.BuildRunPage{}, err
	}
	if err := f.errs["runs"]; err != nil {
		return xcodecloud.BuildRunPage{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pages[key], nil
}

func (f *fakeCloud) GetBuildDetails(ctx context.Context, run string) (xcodecloud.BuildDetails, error) {
	f.record("details:" + run)
	if err := f.wait(ctx); err != nil {
		return xcodecloud.BuildDetails{}, err
	}
	if err := f.errs["details"]; err != nil {
		return xcodecloud.BuildDetails{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	details, ok := f.details[run]
	if !ok {
		return xcodecloud.BuildDetails{}, xcodecloud.ErrNotFound
	}
	return details, nil
}

func (f *fakeCloud) DownloadArtifact(ctx context.Context, artifact xcodecloud.Artifact, destination string, progress func(int64)) error {
	f.record("download:" + artifact.ID)
	f.mu.Lock()
	f.downloads++
	content := f.content[artifact.ID]
	f.mu.Unlock()
	if err := f.wait(ctx); err != nil {
		return err
	}
	if err := f.errs["download"]; err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if progress != nil {
		progress(int64(len(content)))
	}
	return os.WriteFile(destination, []byte(content), 0o600)
}

func timeAt(hour, minute int) time.Time {
	return time.Date(2026, 9, 4, hour, minute, 0, 0, time.UTC)
}

func ptr(t time.Time) *time.Time { return &t }

func sampleRuns() []xcodecloud.BuildRun {
	return []xcodecloud.BuildRun{
		{ID: "run-245", Number: 245, WorkflowID: "wf-pr", WorkflowName: "Pull Request", ExecutionProgress: "RUNNING", StartedAt: ptr(timeAt(10, 42)), SourceBranch: "feature/example", SourceCommit: "a1b2c3d4e5", DestinationBranch: "main", DestinationCommit: "d4e5f6a7b8", IsPullRequest: true, StartReason: "PULL_REQUEST_UPDATE"},
		{ID: "run-244", Number: 244, WorkflowID: "wf-main", WorkflowName: "Main", ExecutionProgress: "COMPLETE", CompletionStatus: "FAILED", StartedAt: ptr(timeAt(9, 0)), FinishedAt: ptr(timeAt(9, 12)), SourceBranch: "main", SourceCommit: "ffff0000aa"},
		{ID: "run-243", Number: 243, WorkflowID: "wf-release", WorkflowName: "Release", ExecutionProgress: "COMPLETE", CompletionStatus: "SUCCEEDED", StartedAt: ptr(timeAt(8, 0)), FinishedAt: ptr(timeAt(8, 8))},
		{ID: "run-242", Number: 242, WorkflowID: "wf-main", WorkflowName: "Main", ExecutionProgress: "COMPLETE", CompletionStatus: "CANCELED", CancelReason: "MANUALLY_BY_USER", StartedAt: ptr(timeAt(7, 0)), FinishedAt: ptr(timeAt(7, 1))},
	}
}

func sampleDetails() xcodecloud.BuildDetails {
	runs := sampleRuns()
	return xcodecloud.BuildDetails{
		Run: runs[1],
		Actions: []xcodecloud.Action{
			{ID: "act-build", Name: "Build and Analyze", Type: "BUILD", ExecutionProgress: "COMPLETE", CompletionStatus: "SUCCEEDED", StartedAt: ptr(timeAt(9, 0)), FinishedAt: ptr(timeAt(9, 2)),
				Issues: []xcodecloud.Issue{{Type: "WARNING", Message: "value was never used", File: "/Volumes/workspace/repository/App/App.swift", Line: 18}, {Type: "WARNING", Message: "value was never used", File: "/Volumes/workspace/repository/App/App.swift", Line: 18}}},
			{ID: "act-test", Name: "Test", Type: "TEST", ExecutionProgress: "COMPLETE", CompletionStatus: "FAILED", StartedAt: ptr(timeAt(9, 2)), FinishedAt: ptr(timeAt(9, 11)),
				Issues: []xcodecloud.Issue{{Type: "TEST_FAILURE", Message: "XCTAssertTrue failed", File: "AppTests/LoginTests.swift", Line: 42}},
				TestResults: []xcodecloud.TestResult{
					{ClassName: "LoginTests", Name: "testValidLogin", Status: "SUCCESS", Destinations: []xcodecloud.TestDestination{{Device: "iPhone 17 Pro", Duration: 400 * time.Millisecond}}},
					{ClassName: "LoginTests", Name: "testInvalidLogin", Status: "FAILURE", Message: "XCTAssertTrue failed", Destinations: []xcodecloud.TestDestination{{Device: "iPhone 17 Pro", Duration: 1300 * time.Millisecond}}},
				},
				Artifacts: []xcodecloud.Artifact{
					{ID: "art-log", Name: "Logs.zip", Type: xcodecloud.ArtifactLogBundle, Size: 1887436, DownloadURL: "https://example.com/logs", ActionID: "act-test", ActionName: "Test"},
					{ID: "art-result", Name: "TestResults.xcresult.zip", Type: xcodecloud.ArtifactResultBundle, Size: 148897792, DownloadURL: "https://example.com/results", ActionID: "act-test", ActionName: "Test"},
				}},
			{ID: "act-archive", Name: "Archive", Type: "ARCHIVE", ExecutionProgress: "COMPLETE", CompletionStatus: "SKIPPED"},
		},
	}
}

type cloudHarness struct {
	t       *testing.T
	app     *App
	gui     *gocui.Gui
	service *fakeCloud
	updates chan func()
}

func newCloudHarness(t *testing.T, service *fakeCloud, width, height int) *cloudHarness {
	t.Helper()
	g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, SupportOverlaps: true, Headless: true, Width: width, Height: height})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	preferences, err := store.NewPreferences()
	if err != nil {
		t.Fatal(err)
	}
	container := model.Container{Kind: model.Project, Name: "App.xcodeproj", Path: "/tmp/App.xcodeproj"}
	project, err := store.NewProject(container)
	if err != nil {
		t.Fatal(err)
	}
	h := &cloudHarness{t: t, gui: g, service: service, updates: make(chan func(), 256)}
	now := timeAt(10, 45)
	h.app = &App{
		gui: g, focus: "build", container: container, preferences: preferences, project: project,
		schemes: []string{"App"}, sims: []model.Simulator{{ID: "PHONE", Name: "iPhone 17 Pro"}},
		outputs: map[string]string{}, now: func() time.Time { return now },
		records: []model.BuildRecord{{ID: "123-001", Scheme: "App", Simulator: model.Simulator{Name: "iPhone 17 Pro"}, Phase: model.PhaseBuilding, StartedAt: time.Now()}},
	}
	h.app.dispatch = func(fn func()) { h.updates <- fn }
	h.app.cloudConnect = func() (xcodecloud.Service, []string, error) {
		if service == nil {
			return nil, nil, xcodecloud.ErrNotConfigured
		}
		return service, nil, nil
	}
	return h
}

// drain applies queued background results until the queue stays empty.
func (h *cloudHarness) drain() {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case fn := <-h.updates:
			fn()
		case <-time.After(50 * time.Millisecond):
			if len(h.updates) == 0 {
				return
			}
		case <-deadline:
			h.t.Fatal("background updates did not settle")
		}
	}
}

func (h *cloudHarness) layout() {
	h.t.Helper()
	if err := h.app.layout(h.gui); err != nil {
		h.t.Fatal(err)
	}
}

func (h *cloudHarness) view(name string) *gocui.View {
	h.t.Helper()
	v, err := h.gui.View(name)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func (h *cloudHarness) press(fn func(*gocui.Gui, *gocui.View) error) {
	h.t.Helper()
	if err := fn(h.gui, nil); err != nil {
		h.t.Fatal(err)
	}
}

func configuredFake() *fakeCloud {
	service := newFakeCloud()
	service.products = []xcodecloud.Product{{ID: "prod-1", Name: "Fortyfive", Type: "APP"}}
	service.workflows["prod-1"] = []xcodecloud.Workflow{{ID: "wf-pr", Name: "Pull Request"}, {ID: "wf-main", Name: "Main"}, {ID: "wf-release", Name: "Release", Disabled: true}}
	service.pages["runs:prod-1:"] = xcodecloud.BuildRunPage{Runs: sampleRuns(), NextCursor: "https://api.example/next"}
	service.details["run-244"] = sampleDetails()
	service.details["run-245"] = xcodecloud.BuildDetails{Run: sampleRuns()[0], Actions: []xcodecloud.Action{{ID: "act-1", Name: "Build", Type: "BUILD", ExecutionProgress: "RUNNING", StartedAt: ptr(timeAt(10, 42))}}}
	service.content["art-log"] = "==== build log ====\nCompileSwift App.swift\nerror: something broke\n"
	return service
}

func TestModeSwitchKeepsLocalAndCloudStateIndependent(t *testing.T) {
	h := newCloudHarness(t, configuredFake(), 120, 36)
	a := h.app
	h.layout()
	if title := h.view("build").Title; !strings.Contains(title, "[Local]") {
		t.Fatalf("local title = %q", title)
	}
	h.press(a.toggleMode)
	h.drain()
	h.layout()
	if title := h.view("build").Title; !strings.Contains(title, "[Cloud]") {
		t.Fatalf("cloud title = %q", title)
	}
	if title := h.view("builds").Title; !strings.Contains(title, "Cloud Activity") {
		t.Fatalf("activity title = %q", title)
	}
	if !strings.Contains(h.view("header").Buffer(), "Xcode Cloud") {
		t.Fatalf("header = %q", h.view("header").Buffer())
	}
	rows := strings.TrimSpace(h.view("builds").Buffer())
	if !strings.Contains(rows, "RUN    #245 Pull Request") || !strings.Contains(rows, "FAIL   #244 Main") || !strings.Contains(rows, "OK     #243 Release") || !strings.Contains(rows, "STOP   #242 Main") {
		t.Fatalf("cloud rows = %q", rows)
	}
	if !strings.Contains(h.view("output").Title, "#245") {
		t.Fatalf("output title = %q", h.view("output").Title)
	}
	if !strings.Contains(h.view("status").Buffer(), "[m] Local") || strings.Contains(h.view("status").Buffer(), "Build") {
		t.Fatalf("cloud footer = %q", h.view("status").Buffer())
	}
	if !strings.Contains(h.view("build").Buffer(), "Fortyfive") || !strings.Contains(h.view("build").Buffer(), "All Workflows") {
		t.Fatalf("cloud config pane = %q", h.view("build").Buffer())
	}

	h.press(a.moveBuild(1))
	h.drain()
	if a.cloud.runIndex != 1 || a.buildIndex != 0 {
		t.Fatalf("cloud selection = %d, local selection = %d", a.cloud.runIndex, a.buildIndex)
	}
	// A local build keeps producing events while Cloud mode is visible.
	a.queueBuildEvent(buildmanager.Event{Record: model.BuildRecord{ID: "123-001", Scheme: "App", Phase: model.PhaseSucceeded, StartedAt: time.Now()}, Output: "done\n", Sequence: 1})
	if a.records[0].Phase != model.PhaseSucceeded || a.outputs["123-001"] != "done\n" {
		t.Fatalf("local event was not applied in cloud mode: %#v", a.records[0])
	}
	h.press(a.toggleMode)
	h.layout()
	if title := h.view("build").Title; !strings.Contains(title, "[Local]") {
		t.Fatalf("title after switching back = %q", title)
	}
	if !strings.Contains(h.view("builds").Buffer(), "#001") {
		t.Fatalf("local rows = %q", h.view("builds").Buffer())
	}
	if a.cloud.runIndex != 1 || len(a.cloud.runs) != 4 {
		t.Fatal("cloud state was lost while in local mode")
	}
	h.press(a.toggleMode)
	if a.cloud.runIndex != 1 || h.service.count("products") != 1 {
		t.Fatalf("re-entering cloud reloaded state: index=%d products=%d", a.cloud.runIndex, h.service.count("products"))
	}
}

func TestCloudKeysNeverInvokeLocalActions(t *testing.T) {
	h := newCloudHarness(t, configuredFake(), 120, 36)
	a := h.app
	a.manager = buildmanager.NewManager(nil, a.project, nil)
	h.press(a.toggleMode)
	h.drain()
	before := len(a.records)
	for _, key := range []func(*gocui.Gui, *gocui.View) error{
		a.byMode(a.startBuild, nil), a.byMode(a.startRun, nil), a.byMode(a.openTestPicker, nil), a.byMode(a.confirmClearCache, nil), a.byMode(a.stopBuild, a.cancelCloudDownload),
	} {
		a.status = ""
		h.press(key)
		if a.status != "Cloud mode is read-only" || a.overlay != nil {
			t.Fatalf("cloud key leaked: status=%q overlay=%v", a.status, a.overlay)
		}
	}
	if len(a.records) != before || a.manager.HasActive() {
		t.Fatal("cloud keys started local work")
	}
	h.press(a.byMode(a.startRun, a.refreshCloud))
	if !strings.HasPrefix(a.status, "Refreshing") {
		t.Fatalf("cloud r status = %q", a.status)
	}
	h.drain()
	if h.service.count("runs:") != 2 {
		t.Fatalf("refresh calls = %d", h.service.count("runs:"))
	}
}

func TestCloudAutoSelectsSoleProduct(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	h.press(h.app.toggleMode)
	h.drain()
	if h.app.overlay != nil || h.app.cloud.product != 0 || h.app.preferences.CloudProduct("/tmp/App.xcodeproj") != "prod-1" {
		t.Fatalf("sole product was not auto-selected: overlay=%v product=%d", h.app.overlay, h.app.cloud.product)
	}
}

func TestCloudOpensPickersForMultipleProductsAndWorkflows(t *testing.T) {
	many := configuredFake()
	many.products = append(many.products, xcodecloud.Product{ID: "prod-2", Name: "Widgets"})
	h2 := newCloudHarness(t, many, 120, 36)
	h2.press(h2.app.toggleMode)
	h2.drain()
	if h2.app.overlay == nil || h2.app.overlay.kind != "cloud-product" || len(h2.app.overlay.items) != 2 {
		t.Fatalf("product picker = %#v", h2.app.overlay)
	}
	h2.app.overlay.selected = 0
	h2.press(h2.app.chooseOverlay)
	h2.drain()
	if h2.app.cloud.product != 0 || len(h2.app.cloud.runs) != 4 {
		t.Fatalf("picker selection failed: product=%d runs=%d", h2.app.cloud.product, len(h2.app.cloud.runs))
	}
	h2.app.cloud.configRow = 1
	h2.press(h2.app.openCloudConfigPicker)
	if h2.app.overlay == nil || h2.app.overlay.kind != "cloud-workflow" || h2.app.overlay.items[0].Label != "All Workflows" || len(h2.app.overlay.items) != 4 {
		t.Fatalf("workflow picker = %#v", h2.app.overlay)
	}
	if !strings.Contains(h2.app.overlay.items[3].Label, "[disabled]") {
		t.Fatalf("disabled workflow label = %q", h2.app.overlay.items[3].Label)
	}
	many.pages["runs:prod-1:wf-main"] = xcodecloud.BuildRunPage{Runs: sampleRuns()[1:2]}
	h2.app.overlay.selected = 2
	h2.press(h2.app.chooseOverlay)
	h2.drain()
	if h2.app.cloud.workflow != 1 || len(h2.app.cloud.runs) != 1 || h2.app.preferences.CloudWorkflow("/tmp/App.xcodeproj") != "wf-main" {
		t.Fatalf("workflow filter = %d runs=%d pref=%q", h2.app.cloud.workflow, len(h2.app.cloud.runs), h2.app.preferences.CloudWorkflow("/tmp/App.xcodeproj"))
	}
}

func TestCloudClearsStaleRememberedSelections(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	_ = h.app.preferences.SetCloudProduct("/tmp/App.xcodeproj", "gone")
	_ = h.app.preferences.SetCloudWorkflow("/tmp/App.xcodeproj", "wf-missing")
	h.press(h.app.toggleMode)
	h.drain()
	if h.app.cloud.product != 0 || h.app.cloud.workflow != -1 {
		t.Fatalf("selection = %d/%d", h.app.cloud.product, h.app.cloud.workflow)
	}
	if h.app.preferences.CloudProduct("/tmp/App.xcodeproj") != "prod-1" || h.app.preferences.CloudWorkflow("/tmp/App.xcodeproj") != "" {
		t.Fatal("stale preferences were not cleared")
	}
}

func TestCloudNotConfiguredShowsGuidanceAndKeepsLocalUsable(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.layout()
	for _, name := range []string{"build", "output"} {
		if buffer := h.view(name).Buffer(); !strings.Contains(buffer, xcodecloud.EnvPrivateKeyPath) {
			t.Fatalf("%s pane lacks setup guidance: %q", name, buffer)
		}
	}
	if !strings.Contains(h.view("builds").Buffer(), "not configured") {
		t.Fatalf("activity pane = %q", h.view("builds").Buffer())
	}
	h.press(a.byMode(a.openConfigPicker, a.openCloudConfigPicker))
	if a.overlay != nil || !strings.Contains(a.status, "not configured") {
		t.Fatalf("unconfigured enter: overlay=%v status=%q", a.overlay, a.status)
	}
	h.press(a.toggleMode)
	h.layout()
	if !strings.Contains(h.view("build").Buffer(), "iPhone 17 Pro") || !strings.Contains(h.view("status").Buffer(), "b Build") && !strings.Contains(h.view("status").Buffer(), "[b] Build") {
		t.Fatalf("local mode unusable: %q / %q", h.view("build").Buffer(), h.view("status").Buffer())
	}
	// Retry after the credentials appear.
	a.cloudConnect = func() (xcodecloud.Service, []string, error) {
		return configuredFake(), []string{"private key is readable by other users"}, nil
	}
	h.press(a.toggleMode)
	h.press(a.byMode(a.startRun, a.refreshCloud))
	h.drain()
	if a.cloud.setupErr != nil || len(a.cloud.runs) != 4 {
		t.Fatalf("retry did not connect: err=%v runs=%d", a.cloud.setupErr, len(a.cloud.runs))
	}
	h.layout()
	if !strings.Contains(h.view("build").Buffer(), "private key is readable") {
		t.Fatalf("credential warning was hidden: %q", h.view("build").Buffer())
	}
}

func TestCloudRendersErrorAndEmptyStatesWithoutLosingData(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	h.layout()
	// Permission error on refresh keeps the last data and marks it stale.
	service.errs["runs"] = &xcodecloud.APIError{Status: 403, Title: "Forbidden"}
	h.press(a.byMode(a.startRun, a.refreshCloud))
	h.drain()
	h.layout()
	if len(a.cloud.runs) != 4 || !a.cloud.stale {
		t.Fatalf("data lost after error: runs=%d stale=%v", len(a.cloud.runs), a.cloud.stale)
	}
	if !strings.Contains(h.view("build").Buffer(), "Stale") || !strings.Contains(h.view("output").Buffer(), "Developer role") {
		t.Fatalf("stale state not rendered: %q / %q", h.view("build").Buffer(), h.view("output").Buffer())
	}
	// Rate limiting is reported the same way.
	service.errs["runs"] = &xcodecloud.APIError{Status: 429}
	h.press(a.byMode(a.startRun, a.refreshCloud))
	h.drain()
	if !strings.Contains(a.status, "rate limit") {
		t.Fatalf("rate limit status = %q", a.status)
	}
	delete(service.errs, "runs")
	// Empty workflow.
	service.pages["runs:prod-1:wf-release"] = xcodecloud.BuildRunPage{}
	a.selectCloudWorkflow("wf-release")
	h.drain()
	h.layout()
	if !strings.Contains(h.view("builds").Buffer(), "No build runs for Release") || !strings.Contains(h.view("output").Buffer(), "No build runs for Release") {
		t.Fatalf("empty state = %q / %q", h.view("builds").Buffer(), h.view("output").Buffer())
	}
	// Loading state while a request is in flight.
	service.block = make(chan struct{})
	a.selectCloudWorkflow("")
	h.layout()
	if !strings.Contains(h.view("builds").Buffer(), "Loading build runs") {
		t.Fatalf("loading state = %q", h.view("builds").Buffer())
	}
	close(service.block)
	h.drain()
	if len(a.cloud.runs) != 4 {
		t.Fatalf("runs after unblocking = %d", len(a.cloud.runs))
	}
}

func TestCloudReportsWhenNoProductsAreVisible(t *testing.T) {
	h := newCloudHarness(t, newFakeCloud(), 120, 36)
	h.press(h.app.toggleMode)
	h.drain()
	h.layout()
	if !strings.Contains(h.view("output").Buffer(), "No Xcode Cloud products") || !strings.Contains(h.view("build").Buffer(), "Unavailable") {
		t.Fatalf("no products output = %q / %q", h.view("output").Buffer(), h.view("build").Buffer())
	}
}

func TestCloudLayoutSurvivesNarrowAndMinimumSizes(t *testing.T) {
	for _, size := range [][2]int{{120, 36}, {74, 23}, {44, 10}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			h := newCloudHarness(t, configuredFake(), size[0], size[1])
			h.press(h.app.toggleMode)
			h.drain()
			h.layout()
			if !strings.Contains(h.view("build").Title, "[Cloud]") {
				t.Fatalf("title = %q", h.view("build").Title)
			}
			buildsWidth, _ := h.view("builds").InnerSize()
			for _, line := range strings.Split(strings.TrimRight(h.view("builds").Buffer(), "\n"), "\n") {
				if len([]rune(line)) > buildsWidth {
					t.Fatalf("row exceeds width %d: %q", buildsWidth, line)
				}
			}
			if !strings.Contains(h.view("status").Buffer(), "m Local") && !strings.Contains(h.view("status").Buffer(), "[m] Local") {
				t.Fatalf("footer = %q", h.view("status").Buffer())
			}
			h.app.focus = "builds"
			h.layout()
		})
	}
}

func TestCloudRunRowTruncatesWorkflowBeforeStatusAndDuration(t *testing.T) {
	run := sampleRuns()[1]
	run.WorkflowName = "An Extremely Long Workflow Name That Will Not Fit"
	row := formatCloudRunRow(run, 30, timeAt(10, 45))
	if len([]rune(row)) > 30 || !strings.HasPrefix(row, "FAIL   #244 ") || !strings.HasSuffix(row, "12m00s") {
		t.Fatalf("row = %q", row)
	}
	active := formatCloudRunRow(sampleRuns()[0], 40, timeAt(10, 45))
	if !strings.HasPrefix(active, "RUN    #245 ") || !strings.HasSuffix(active, "3m00s") {
		t.Fatalf("active row = %q", active)
	}
}

func TestCloudIgnoresLateResponsesFromOldSelections(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	stale := a.cloud.detailGeneration
	a.cloud.detailGeneration++
	a.applyCloudDetails(stale, "run-245", xcodecloud.BuildDetails{Run: xcodecloud.BuildRun{ID: "run-245", Number: 999}}, nil)
	if a.cloud.runs[0].Number != 245 {
		t.Fatal("late detail response overwrote current state")
	}
	staleRuns := a.cloud.generation
	a.cloud.generation++
	a.applyCloudRuns(staleRuns, xcodecloud.BuildRunPage{Runs: []xcodecloud.BuildRun{{ID: "other", Number: 1}}}, nil, false)
	if len(a.cloud.runs) != 4 {
		t.Fatal("late run response overwrote current state")
	}
	// Switching selection cancels the in-flight detail request.
	service.block = make(chan struct{})
	a.moveCloudRun(2)
	if a.cloud.detailLoading != "run-243" {
		t.Fatalf("detail loading = %q", a.cloud.detailLoading)
	}
	first := a.cloud.detailGeneration
	a.moveCloudRun(1)
	if a.cloud.detailGeneration == first || a.cloud.detailLoading != "run-242" {
		t.Fatalf("selection change did not cancel old request: gen=%d loading=%q", a.cloud.detailGeneration, a.cloud.detailLoading)
	}
	close(service.block)
	h.drain()
	if _, ok := a.cloud.details["run-243"]; ok {
		t.Fatal("cancelled detail response was applied")
	}
}

func TestCloudReconcilesPolledRunsAndPagination(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	a.moveCloudRun(1)
	h.drain()
	// Load an older page.
	service.pages["cursor:https://api.example/next"] = xcodecloud.BuildRunPage{Runs: []xcodecloud.BuildRun{
		{ID: "run-242", Number: 242, WorkflowName: "Main", ExecutionProgress: "COMPLETE", CompletionStatus: "CANCELED"},
		{ID: "run-241", Number: 241, WorkflowName: "Main", ExecutionProgress: "COMPLETE", CompletionStatus: "SUCCEEDED"},
	}, NextCursor: "https://api.example/next2"}
	a.moveCloudRunTo(true)
	a.moveCloudRun(1)
	h.drain()
	if len(a.cloud.runs) != 5 || a.cloud.runs[4].ID != "run-241" || a.cloud.nextCursor != "https://api.example/next2" {
		t.Fatalf("after load more: %d runs, cursor %q", len(a.cloud.runs), a.cloud.nextCursor)
	}
	if selected, _ := a.cloud.selectedRun(); selected.ID != "run-241" {
		t.Fatalf("selection after load more = %q, want first older run", selected.ID)
	}
	a.cloud.runIndex = cloudRunIndex(a.cloud.runs, "run-244")
	// A poll returns a newer run and settles the active one.
	updated := sampleRuns()
	updated[0].ExecutionProgress, updated[0].CompletionStatus, updated[0].FinishedAt = "COMPLETE", "SUCCEEDED", ptr(timeAt(10, 50))
	newer := xcodecloud.BuildRun{ID: "run-246", Number: 246, WorkflowName: "Pull Request", ExecutionProgress: "PENDING"}
	service.mu.Lock()
	service.pages["runs:prod-1:"] = xcodecloud.BuildRunPage{Runs: append([]xcodecloud.BuildRun{newer}, updated...), NextCursor: "https://api.example/fresh"}
	service.mu.Unlock()
	a.cloudTick(timeAt(10, 45).Add(cloudActivePollInterval))
	h.drain()
	ids := make([]string, len(a.cloud.runs))
	for i, run := range a.cloud.runs {
		ids[i] = run.ID
	}
	if strings.Join(ids, ",") != "run-246,run-245,run-244,run-243,run-242,run-241" {
		t.Fatalf("reconciled order = %v", ids)
	}
	if a.cloud.runs[1].Status() != xcodecloud.StatusSucceeded {
		t.Fatalf("settled run status = %v", a.cloud.runs[1].Status())
	}
	if a.cloud.runs[a.cloud.runIndex].ID != "run-244" {
		t.Fatalf("selection moved to %s", a.cloud.runs[a.cloud.runIndex].ID)
	}
	if a.cloud.nextCursor != "https://api.example/next2" {
		t.Fatalf("pagination cursor was replaced: %q", a.cloud.nextCursor)
	}
}

func TestCloudCanLoadOlderRunsExplicitly(t *testing.T) {
	service := configuredFake()
	service.pages["cursor:https://api.example/next"] = xcodecloud.BuildRunPage{Runs: []xcodecloud.BuildRun{
		{ID: "run-241", Number: 241, WorkflowName: "Main", ExecutionProgress: "COMPLETE", CompletionStatus: "SUCCEEDED"},
	}}
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()

	h.press(a.byMode(nil, a.loadOlderCloudRuns))
	h.drain()

	if len(a.cloud.runs) != 5 || a.cloud.runs[4].ID != "run-241" {
		t.Fatalf("older runs = %#v", a.cloud.runs)
	}
	if selected, _ := a.cloud.selectedRun(); selected.ID != "run-245" {
		t.Fatalf("explicit load moved selection to %q", selected.ID)
	}
}

func TestCloudTickPollsActiveRunsFasterThanSettledRuns(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	base := timeAt(10, 45)
	a.cloud.lastRefresh = base
	initial := service.count("runs:")
	a.cloudTick(base.Add(cloudActivePollInterval - time.Second))
	h.drain()
	if service.count("runs:") != initial {
		t.Fatal("polled before the active interval elapsed")
	}
	a.cloudTick(base.Add(cloudActivePollInterval))
	h.drain()
	if service.count("runs:") != initial+1 {
		t.Fatal("active runs were not polled at 15 seconds")
	}
	for i := range a.cloud.runs {
		a.cloud.runs[i].ExecutionProgress, a.cloud.runs[i].CompletionStatus = "COMPLETE", "SUCCEEDED"
	}
	a.cloud.lastRefresh = base
	a.cloudTick(base.Add(cloudActivePollInterval))
	h.drain()
	if service.count("runs:") != initial+1 {
		t.Fatal("settled runs were polled at the active interval")
	}
	a.cloudTick(base.Add(cloudIdlePollInterval))
	h.drain()
	if service.count("runs:") != initial+2 {
		t.Fatal("settled runs were not polled at 60 seconds")
	}
	a.mode = modeLocal
	a.cloud.lastRefresh = base
	a.cloudTick(base.Add(time.Hour))
	h.drain()
	if service.count("runs:") != initial+2 {
		t.Fatal("polling continued while Local mode was visible")
	}
}

func TestCloudRefreshesDetailsWhenAnActiveRunCompletes(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	initialDetails := service.count("details:run-245")

	settled := sampleRuns()
	settled[0].ExecutionProgress = "COMPLETE"
	settled[0].CompletionStatus = "SUCCEEDED"
	settled[0].FinishedAt = ptr(timeAt(10, 50))
	service.mu.Lock()
	service.pages["runs:prod-1:"] = xcodecloud.BuildRunPage{Runs: settled}
	service.details["run-245"] = xcodecloud.BuildDetails{
		Run:     settled[0],
		Actions: []xcodecloud.Action{{ID: "act-final", Name: "Archive", ExecutionProgress: "COMPLETE", CompletionStatus: "SUCCEEDED"}},
	}
	service.mu.Unlock()

	h.press(a.byMode(a.startRun, a.refreshCloud))
	h.drain()

	if service.count("details:run-245") != initialDetails+1 {
		t.Fatalf("completed run details were not refreshed: calls=%d", service.count("details:run-245"))
	}
	details := a.cloud.details["run-245"]
	if details.Run.Status() != xcodecloud.StatusSucceeded || len(details.Actions) != 1 || details.Actions[0].Name != "Archive" {
		t.Fatalf("completed run details = %#v", details)
	}
}

func TestCloudConciseOutputShowsStructuredDetails(t *testing.T) {
	h := newCloudHarness(t, configuredFake(), 120, 40)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	a.moveCloudRun(1)
	h.drain()
	h.layout()
	output := ansiPattern.ReplaceAllString(h.view("output").Buffer(), "")
	for _, expected := range []string{
		"XCODE CLOUD BUILD #244", "Workflow       Main", "Source         main @ ffff000", "Status         Failed after 12m00s",
		"ACTIONS (3)", "✓ Build and Analyze", "2m00s", "✗ Test", "9m00s", "- Archive", "skipped",
		"DIAGNOSTICS (2)", "warning: App.swift:18 - value was never used", "error: LoginTests.swift:42 - XCTAssertTrue failed",
		"TESTS", "1 passed, 1 failed", "✗ LoginTests.testInvalidLogin", "1.3s",
		"ARTIFACTS (2)", "Logs.zip", "1.8 MB", "TestResults.xcresult.zip", "142.0 MB",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("concise cloud output missing %q:\n%s", expected, output)
		}
	}
	if strings.Count(output, "value was never used") != 1 {
		t.Fatalf("diagnostics were not deduplicated:\n%s", output)
	}
	if strings.Contains(output, "—") {
		t.Fatalf("generated text should use ASCII separators:\n%s", output)
	}
	styled := formatBuildOutput(a.cloudOutputText(timeAt(10, 45)))
	if !strings.Contains(styled, ansiBoldRed+"error:"+ansiReset) || !strings.Contains(styled, ansiBoldCyan+"ACTIONS (3)"+ansiReset) {
		t.Fatalf("cloud diagnostics were not styled: %q", styled)
	}
}

func TestCloudRawLogsDownloadOnceAndRenderText(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	a.moveCloudRun(1)
	h.drain()
	h.press(a.byMode(a.toggleOutputVerbosity, a.toggleCloudVerbosity))
	h.layout()
	if !strings.Contains(h.view("output").Title, "RAW") || !strings.Contains(h.view("output").Buffer(), "Downloading logs") {
		t.Fatalf("raw loading state = %q / %q", h.view("output").Title, h.view("output").Buffer())
	}
	h.drain()
	h.layout()
	output := h.view("output").Buffer()
	if !strings.Contains(output, "==== Test - Logs.zip ====") || !strings.Contains(output, "CompileSwift App.swift") {
		t.Fatalf("raw log output = %q", output)
	}
	h.press(a.byMode(a.toggleOutputVerbosity, a.toggleCloudVerbosity))
	h.press(a.byMode(a.toggleOutputVerbosity, a.toggleCloudVerbosity))
	h.drain()
	if service.count("download:") != 1 {
		t.Fatalf("logs were downloaded %d times", service.count("download:"))
	}
	if a.verboseOutput {
		t.Fatal("cloud verbosity toggled the local preference")
	}
	// A run without log artifacts explains itself.
	a.moveCloudRun(1)
	h.drain()
	h.layout()
	if !strings.Contains(h.view("output").Buffer(), "Build details are not available yet") && !strings.Contains(h.view("output").Buffer(), "no log artifacts") {
		t.Fatalf("empty raw state = %q", h.view("output").Buffer())
	}
	// Changed artifact metadata invalidates only that run's cache.
	a.moveCloudRun(-1)
	changed := sampleDetails()
	changed.Actions[1].Artifacts[0].Size = 42
	a.applyCloudDetails(a.cloud.detailGeneration, "run-244", changed, nil)
	if _, cached := a.cloud.rawLogs["run-244"]; cached && a.cloud.rawLogs["run-244"].state == rawLogReady && a.cloud.rawLogs["run-244"].artifactKey == logArtifactKey(sampleDetails()) {
		t.Fatal("stale raw log cache survived an artifact change")
	}
	h.drain()
	if service.count("download:") != 2 {
		t.Fatalf("invalidated log was not re-downloaded: %d downloads", service.count("download:"))
	}
}

func TestCloudRawLogsReportFailures(t *testing.T) {
	service := configuredFake()
	service.errs["download"] = errors.New("boom")
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	a.moveCloudRun(1)
	h.drain()
	h.press(a.byMode(a.toggleOutputVerbosity, a.toggleCloudVerbosity))
	h.drain()
	h.layout()
	if !strings.Contains(h.view("output").Buffer(), "Log download failed: boom") {
		t.Fatalf("failure state = %q", h.view("output").Buffer())
	}
}

func TestCloudArtifactPickerDownloadsCancelsAndFails(t *testing.T) {
	service := configuredFake()
	h := newCloudHarness(t, service, 120, 36)
	a := h.app
	h.press(a.toggleMode)
	h.drain()
	h.press(a.byMode(nil, a.openCloudArtifactPicker))
	if a.overlay != nil || !strings.Contains(a.status, "no artifacts") {
		t.Fatalf("active run picker: overlay=%v status=%q", a.overlay, a.status)
	}
	a.moveCloudRun(1)
	h.drain()
	h.press(a.byMode(nil, a.openCloudArtifactPicker))
	if a.overlay == nil || a.overlay.kind != "cloud-artifact" || len(a.overlay.items) != 2 || !strings.Contains(a.overlay.items[1].Label, "Test") || !strings.Contains(a.overlay.items[1].Label, "142.0 MB") {
		t.Fatalf("artifact picker = %#v", a.overlay)
	}
	a.overlay.filter = "xcresult"
	if items := a.filteredOverlayItems(); len(items) != 1 || items[0].ID != "art-result" {
		t.Fatalf("filtered artifacts = %#v", items)
	}
	a.overlay.selected = 0
	service.content["art-result"] = "bundle"
	h.press(a.chooseOverlay)
	if !strings.HasPrefix(a.status, "Downloading TestResults.xcresult.zip") {
		t.Fatalf("download status = %q", a.status)
	}
	h.drain()
	expected, _ := a.project.CloudArtifactPath("run-244", "art-result", "TestResults.xcresult.zip")
	if a.status != "Saved "+expected {
		t.Fatalf("final status = %q", a.status)
	}
	if data, err := os.ReadFile(expected); err != nil || string(data) != "bundle" {
		t.Fatalf("artifact content = %q, %v", data, err)
	}
	// Cancellation.
	service.block = make(chan struct{})
	h.press(a.byMode(nil, a.openCloudArtifactPicker))
	a.overlay.selected = 0
	h.press(a.chooseOverlay)
	h.layout()
	if !strings.Contains(h.view("build").Buffer(), "Downloading Logs.zip") {
		t.Fatalf("download progress not shown: %q", h.view("build").Buffer())
	}
	h.press(a.byMode(a.stopBuild, a.cancelCloudDownload))
	h.drain()
	if !strings.HasPrefix(a.status, "Download cancelled") || a.cloud.download != nil {
		t.Fatalf("cancel status = %q download=%v", a.status, a.cloud.download)
	}
	close(service.block)
	service.block = nil
	// Failure.
	service.errs["download"] = &xcodecloud.APIError{Status: 500, Title: "Boom"}
	h.press(a.byMode(nil, a.openCloudArtifactPicker))
	a.overlay.selected = 0
	h.press(a.chooseOverlay)
	h.drain()
	if !strings.HasPrefix(a.status, "Download failed") || !strings.Contains(a.status, "Boom") {
		t.Fatalf("failure status = %q", a.status)
	}
}

func TestCloudExpiredArtifactLinkIsRefreshedOnce(t *testing.T) {
	service := configuredFake()
	attempts := 0
	var mu sync.Mutex
	expiring := &expiringService{fakeCloud: service, attempt: func() error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts == 1 {
			return xcodecloud.ErrArtifactExpired
		}
		return nil
	}}
	path := filepath.Join(t.TempDir(), "out")
	err := downloadCloudArtifact(context.Background(), expiring, "run-244", sampleDetails().Actions[1].Artifacts[0], path, nil)
	if err != nil || attempts != 2 || service.count("details:run-244") != 1 {
		t.Fatalf("expired retry: err=%v attempts=%d details=%d", err, attempts, service.count("details:run-244"))
	}
}

type expiringService struct {
	*fakeCloud
	attempt func() error
}

func (e *expiringService) DownloadArtifact(ctx context.Context, artifact xcodecloud.Artifact, destination string, progress func(int64)) error {
	if err := e.attempt(); err != nil {
		return err
	}
	return e.fakeCloud.DownloadArtifact(ctx, artifact, destination, progress)
}

func TestRenderLogArtifactHandlesZipTextAndBinary(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "build.log")
	os.WriteFile(text, []byte("line one\nline two"), 0o600)
	rendered := renderLogArtifact(text, xcodecloud.Artifact{Name: "build.log", ActionName: "Build"})
	if !strings.HasPrefix(rendered, "==== Build - build.log ====\n") || !strings.Contains(rendered, "line two\n") {
		t.Fatalf("text rendering = %q", rendered)
	}
	binary := filepath.Join(dir, "app.bin")
	os.WriteFile(binary, []byte("\x00\x01\x02binary"), 0o600)
	if rendered := renderLogArtifact(binary, xcodecloud.Artifact{Name: "app.bin"}); !strings.Contains(rendered, "not a text log") || !strings.Contains(rendered, binary) {
		t.Fatalf("binary rendering = %q", rendered)
	}
	archive := filepath.Join(dir, "Logs.zip")
	writeZip(t, archive, map[string]string{"xcodebuild.log": "CompileSwift\n", "image.png": "\x89PNG\x00", "nested/post-clone.log": "clone ok"})
	rendered = renderLogArtifact(archive, xcodecloud.Artifact{Name: "Logs.zip", ActionName: "Test"})
	for _, expected := range []string{"==== Test - Logs.zip ====", "---- nested/post-clone.log ----", "clone ok", "---- xcodebuild.log ----", "CompileSwift"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("zip rendering missing %q: %q", expected, rendered)
		}
	}
	if strings.Contains(rendered, "PNG") {
		t.Fatalf("binary zip entry was rendered: %q", rendered)
	}
}

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := newZipWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(entry, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

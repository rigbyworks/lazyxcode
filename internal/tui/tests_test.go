package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	buildmanager "github.com/mwahlig/lazy-xcode/internal/build"
	"github.com/mwahlig/lazy-xcode/internal/model"
	"github.com/mwahlig/lazy-xcode/internal/store"
	"github.com/mwahlig/lazy-xcode/internal/xcode"
)

type testToolRunner struct {
	mu    sync.Mutex
	calls []string
	block chan struct{}
}

func (r *testToolRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.block != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-r.block:
		}
	}
	call := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	switch {
	case strings.Contains(call, "test-results tests"):
		return []byte(`{"testNodes":[{"nodeType":"Unit test bundle","name":"AppTests","children":[{"nodeType":"Test Case","name":"testFailure()","nodeIdentifier":"Checks/testFailure()","nodeIdentifierURL":"test://host/Plan/AppTests/Checks/testFailure","result":"Failed","duration":"0.1s"},{"nodeType":"Test Case","name":"testPass()","nodeIdentifier":"Checks/testPass()","nodeIdentifierURL":"test://host/Plan/AppTests/Checks/testPass","result":"Passed"}]}]}`), nil
	case strings.Contains(call, "test-details"):
		return []byte(`{"testName":"testFailure()","testResult":"Failed","duration":"0.1s","testRuns":[{"name":"Expected true","sourceLocation":{"filePath":"/src/App.swift","lineNumber":12}}]}`), nil
	case strings.Contains(call, "view --report"):
		return []byte(`{"lineCoverage":0.5,"coveredLines":5,"executableLines":10,"targets":[{"name":"App","lineCoverage":0.5,"coveredLines":5,"executableLines":10,"files":[{"name":"App.swift","path":"/src/App.swift","lineCoverage":0.5,"coveredLines":5,"executableLines":10,"functions":[{"name":"run()","lineNumber":12,"lineCoverage":0.5,"coveredLines":5,"executableLines":10}]}]}]}`), nil
	case strings.Contains(call, "xccov diff"):
		return []byte(`{"lineCoverageDelta":{"lineCoverageDelta":0.25},"targetDeltas":[{"name":"App","lineCoverageDelta":{"lineCoverageDelta":0.25}}]}`), nil
	}
	return nil, nil
}
func (r *testToolRunner) Stream(_ context.Context, w io.Writer, name string, args ...string) error {
	r.mu.Lock()
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	r.mu.Unlock()
	for i, arg := range args {
		if arg == "-test-enumeration-output-path" {
			return os.WriteFile(args[i+1], []byte(`{"errors":[],"values":[{"enabledTests":[{"identifier":"AppTests/Checks/testFailure()"},{"identifier":"AppTests/Checks/testPass()"}]}]}`), 0600)
		}
	}
	_, err := io.WriteString(w, "test output\n")
	return err
}
func (r *testToolRunner) transcript() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
}

func testResultHarness(t *testing.T, width, height int) (*cloudHarness, *testToolRunner) {
	t.Helper()
	h := newCloudHarness(t, nil, width, height)
	runner := &testToolRunner{}
	h.app.xcode = xcode.New(runner)
	h.app.manager = buildmanager.NewManager(h.app.xcode, h.app.project, h.app.handleBuildEvent)
	t.Cleanup(func() { h.app.manager.CancelAll(); h.app.manager.Wait() })
	dir, err := h.app.project.NewResultDir("123-001")
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(dir, "Results.xcresult")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	h.app.records = []model.BuildRecord{{ID: "123-001", Container: h.app.container, Scheme: "Original", Simulator: model.Simulator{ID: "ORIGINAL-MAC", Name: "My Mac", Platform: "macOS"}, Operation: model.OperationTest, Phase: model.PhaseTestFailed, ResultBundlePath: bundle, Coverage: true, TestScope: "All Tests", StartedAt: time.Now()}}
	h.app.outputs["123-001"] = "original test transcript\n"
	return h, runner
}

func chooseTestMenu(t *testing.T, h *cloudHarness, id string) {
	t.Helper()
	if h.app.overlay == nil {
		t.Fatalf("no menu while choosing %s: %s", id, h.app.status)
	}
	h.app.overlay.filter = ""
	for i, item := range h.app.overlay.items {
		if item.ID == id {
			h.app.overlay.selected = i
			h.press(h.app.chooseOverlay)
			return
		}
	}
	t.Fatalf("no %q action in %+v", id, h.app.overlay.items)
}

func TestResultBrowserUsesOutputAndReturnsToLog(t *testing.T) {
	for _, size := range [][2]int{{120, 36}, {74, 23}, {44, 10}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			h, _ := testResultHarness(t, size[0], size[1])
			a := h.app
			h.layout()
			h.press(a.openTestActivity)
			chooseTestMenu(t, h, "results")
			h.drain()
			h.layout()
			if !strings.Contains(h.view("overlay").Buffer(), "FAIL") || !strings.Contains(h.view("overlay").Buffer(), "PASS") {
				t.Fatal(h.view("overlay").Buffer())
			}
			a.overlay.filter = "failure"
			h.press(a.chooseOverlay)
			h.drain()
			h.layout()
			if !strings.Contains(h.view("output").Buffer(), "/src/App.swift:12") {
				t.Fatal(h.view("output").Buffer())
			}
			if a.focus != "output" || !strings.Contains(h.view("output").Title, "Esc") {
				t.Fatal("details did not use Output")
			}
			h.press(a.openTestOutputActions)
			h.layout()
			chooseTestMenu(t, h, "back")
			h.layout()
			if !strings.Contains(h.view("filter").Buffer(), "failure") {
				t.Fatalf("parent filter lost: %s", h.view("filter").Buffer())
			}
			h.press(a.closeOverlay)
			h.press(a.closeOverlay)
			h.press(a.closeTestOutput)
			h.layout()
			if strings.Contains(h.view("output").Buffer(), "/src/App.swift:12") {
				t.Fatal("details remained after Esc")
			}
		})
	}
}

func TestFailedRerunKeepsHistoricalContext(t *testing.T) {
	h, runner := testResultHarness(t, 120, 36)
	a := h.app
	a.schemes = []string{"Different"}
	a.sims = []model.Simulator{{ID: "OTHER", Platform: "macOS"}}
	h.press(a.openTestActivity)
	chooseTestMenu(t, h, "failed")
	h.drain()
	a.manager.Wait()
	h.drain()
	commands := runner.transcript()
	for _, want := range []string{"-scheme Original", "-destination id=ORIGINAL-MAC", "-only-testing:AppTests/Checks/testFailure()", "-resultBundlePath", "-enableCodeCoverage YES"} {
		if !strings.Contains(commands, want) {
			t.Fatalf("missing %s in %s", want, commands)
		}
	}
	if strings.Contains(commands, "-only-testing:AppTests/Checks/testPass()") || strings.Contains(commands, "-scheme Different") {
		t.Fatal(commands)
	}
}

func TestIndividualDiscoveryThenSelection(t *testing.T) {
	h, runner := testResultHarness(t, 120, 36)
	a := h.app
	a.sims = []model.Simulator{{ID: "MAC", Platform: "macOS"}}
	h.press(a.openTestPicker)
	chooseTestMenu(t, h, "individual")
	a.manager.Wait()
	h.drain()
	h.layout()
	if a.overlay == nil || a.overlay.title != "Run Individual Test" {
		t.Fatalf("discovery did not open picker: %s", a.status)
	}
	a.overlay.filter = "testPass"
	h.press(a.chooseOverlay)
	a.manager.Wait()
	h.drain()
	if !strings.Contains(runner.transcript(), "-only-testing:AppTests/Checks/testPass()") {
		t.Fatal(runner.transcript())
	}
}

func TestCoverageDrilldownAndComparison(t *testing.T) {
	h, runner := testResultHarness(t, 120, 36)
	a := h.app
	before := a.records[0]
	before.ID = "122-001"
	before.ResultBundlePath = "/tmp/before.xcresult"
	before.StartedAt = before.StartedAt.Add(-time.Hour)
	a.records = append(a.records, before)
	h.press(a.openTestActivity)
	chooseTestMenu(t, h, "coverage")
	h.drain()
	h.layout()
	if !strings.Contains(h.view("output").Buffer(), "50.0%") {
		t.Fatal(h.view("output").Buffer())
	}
	h.press(a.openTestOutputActions)
	chooseTestMenu(t, h, "0")
	chooseTestMenu(t, h, "0")
	h.layout()
	for _, want := range []string{"/src/App.swift", "run()", "line 12"} {
		if !strings.Contains(h.view("output").Buffer(), want) {
			t.Fatal(h.view("output").Buffer())
		}
	}
	h.press(a.openTestActivity)
	chooseTestMenu(t, h, "compare")
	chooseTestMenu(t, h, "0")
	h.drain()
	h.layout()
	if !strings.Contains(runner.transcript(), "xccov diff --json /tmp/before.xcresult "+a.records[0].ResultBundlePath) {
		t.Fatal(runner.transcript())
	}
	if !strings.Contains(h.view("output").Buffer(), "+25.00 pp") {
		t.Fatal(h.view("output").Buffer())
	}
}

func TestCancelledResultLoadCannotReplaceNewMenu(t *testing.T) {
	h, runner := testResultHarness(t, 120, 36)
	runner.block = make(chan struct{})
	a := h.app
	h.press(a.openTestActivity)
	chooseTestMenu(t, h, "results")
	h.press(a.closeOverlay)
	h.press(a.openTestPicker)
	wanted := a.overlay
	close(runner.block)
	h.drain()
	if a.overlay != wanted {
		t.Fatal("cancelled request replaced a newer menu")
	}
}

func TestCoverageBaselinesExcludeDifferentContextsAndFutureRuns(t *testing.T) {
	now := time.Now()
	current := model.BuildRecord{ID: "now", Container: model.Container{Path: "/project"}, Scheme: "App", Simulator: model.Simulator{ID: "MAC"}, StartedAt: now}
	valid := current
	valid.ID = "old"
	valid.StartedAt = now.Add(-time.Hour)
	valid.Operation = model.OperationTest
	valid.Phase = model.PhaseSucceeded
	valid.Coverage = true
	valid.ResultBundlePath = "old.xcresult"
	records := []model.BuildRecord{valid}
	for _, mutate := range []func(*model.BuildRecord){func(r *model.BuildRecord) { r.Scheme = "Other" }, func(r *model.BuildRecord) { r.Simulator.ID = "PHONE" }, func(r *model.BuildRecord) { r.Coverage = false }, func(r *model.BuildRecord) { r.Phase = model.PhaseTesting }, func(r *model.BuildRecord) { r.StartedAt = now.Add(time.Hour) }} {
		r := valid
		mutate(&r)
		records = append(records, r)
	}
	if result := coverageBaselines(records, current); len(result) != 1 {
		t.Fatalf("baselines = %+v", result)
	}
}

func TestOldRunRemainsReadableWithoutResults(t *testing.T) {
	h, _ := testResultHarness(t, 120, 36)
	h.app.records[0].ResultBundlePath = ""
	h.press(h.app.openTestActivity)
	if h.app.overlay != nil || !strings.Contains(h.app.status, "older activity") {
		t.Fatal(h.app.status)
	}
	h.layout()
}

func TestCloudResultBrowserIsReadOnlyAndKeepsLocalHistory(t *testing.T) {
	h := newCloudHarness(t, configuredFake(), 120, 36)
	a := h.app
	a.xcode = xcode.New(&testToolRunner{})
	h.press(a.toggleMode)
	h.drain()
	h.press(a.moveBuild(1))
	h.drain()
	localCount := len(a.records)
	path, err := a.project.CloudArtifactPath("run-244", "art-result", "TestResults.xcresult.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	writeZip(t, path, map[string]string{"Results.xcresult/Info.plist": "fixture"})
	h.press(a.openCloudTestActivity)
	chooseTestMenu(t, h, "art-result")
	h.drain()
	for _, item := range a.overlay.items {
		if item.ID == "failed" || item.ID == "compare" {
			t.Fatal("local action in Cloud menu")
		}
	}
	chooseTestMenu(t, h, "results")
	h.drain()
	chooseTestMenu(t, h, "0")
	h.drain()
	h.layout()
	if !strings.Contains(h.view("output").Buffer(), "/src/App.swift:12") {
		t.Fatal(h.view("output").Buffer())
	}
	h.press(a.openTestOutputActions)
	for _, item := range a.overlay.items {
		if item.ID == "run" {
			t.Fatal("Cloud test can be rerun")
		}
	}
	if len(a.records) != localCount {
		t.Fatal("Cloud inspection changed local history")
	}
	h.press(a.closeOverlay)
	h.press(a.closeOverlay)
	h.press(a.closeOverlay)
	h.press(a.closeOverlay)
	h.press(a.toggleMode)
	h.layout()
	if strings.Contains(h.view("output").Buffer(), "/src/App.swift:12") {
		t.Fatal("Cloud output leaked into Local mode")
	}
}

func TestEmptyFailureSelectionNeverRunsAllTests(t *testing.T) {
	h, runner := testResultHarness(t, 120, 36)
	before := len(h.app.records)
	h.app.rerunTests(h.app.records[0], nil, "Failed Tests")
	if len(h.app.records) != before || runner.transcript() != "" {
		t.Fatal("empty selection ran tests")
	}
}

func TestCoveragePreferencePersistsThroughReload(t *testing.T) {
	h, _ := testResultHarness(t, 120, 36)
	a := h.app
	h.press(a.openTestPicker)
	chooseTestMenu(t, h, "coverage")
	prefs, err := store.NewPreferences()
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.CoverageDisabled(a.container.Path) {
		t.Fatal("coverage toggle was not saved")
	}
	if !strings.Contains(a.overlay.items[len(a.overlay.items)-1].Label, "Off") {
		t.Fatal("toggle did not update picker")
	}
}

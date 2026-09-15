package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jesseduffield/gocui"
	buildmanager "github.com/rigbyworks/lazyxcode/internal/build"
	"github.com/rigbyworks/lazyxcode/internal/model"
	"github.com/rigbyworks/lazyxcode/internal/store"
	"github.com/rigbyworks/lazyxcode/internal/xcode"
	"github.com/rigbyworks/lazyxcode/internal/xcodecloud"
)

type testOutputView struct {
	mode     appMode
	recordID string
	title    string
	text     string
	menu     *overlayState
}

func (a *App) selectedTestOutput() *testOutputView {
	if a.testOutput == nil || a.testOutput.mode != a.mode {
		return nil
	}
	if a.mode == modeCloud {
		if a.cloud != nil {
			if run, ok := a.cloud.selectedRun(); ok && run.ID == a.testOutput.recordID {
				return a.testOutput
			}
		}
		return nil
	}
	if len(a.records) > 0 && a.buildIndex < len(a.records) && a.records[a.buildIndex].ID == a.testOutput.recordID {
		return a.testOutput
	}
	return nil
}

func (a *App) closeTestOutput(*gocui.Gui, *gocui.View) error {
	a.testOutput = nil
	a.outputFollow = false
	a.resetOutputScroll()
	return nil
}

func (a *App) resetOutputScroll() {
	if a.gui != nil {
		if v, err := a.gui.View("output"); err == nil {
			v.SetOrigin(0, 0)
		}
	}
}

func (a *App) showTestOutput(record model.BuildRecord, title, text string, menu *overlayState) {
	a.testOutput = &testOutputView{mode: a.mode, recordID: record.ID, title: title, text: text, menu: menu}
	a.overlay = nil
	a.focus, a.outputFollow = "output", false
	a.resetOutputScroll()
	a.status = "Enter: actions  Esc: activity log"
}

func (a *App) openTestOutputActions(g *gocui.Gui, v *gocui.View) error {
	if report := a.selectedTestOutput(); report != nil {
		a.overlay = report.menu
		return nil
	}
	if a.mode == modeCloud {
		return a.openCloudTestActivity(g, v)
	}
	return a.openTestActivity(g, v)
}

func (a *App) queueTestRequest(request buildmanager.Request) {
	if a.mode != modeLocal {
		a.status = "Cloud mode is read-only"
		return
	}
	if a.manager == nil {
		a.status = "Tests are unavailable until discovery completes"
		return
	}
	record, err := a.manager.Start(a.baseContext(), request)
	if err != nil {
		a.status = err.Error()
		return
	}
	a.records = append([]model.BuildRecord{record}, a.records...)
	a.buildIndex, a.outputFollow = 0, true
	a.testOutput = nil
	if a.outputs == nil {
		a.outputs = map[string]string{}
	}
	a.outputs[record.ID] = ""
	a.overlay = nil
	if request.Operation == model.OperationDiscoverTests {
		a.discoveringTests = record.ID
	}
	a.status = "Queued " + strings.ToLower(request.TestScope) + " #" + shortID(record.ID)
}

// Work completes only into the loading overlay that requested it. Esc cancels
// the command and stale replies cannot replace a newer selection or mode.
func (a *App) loadTestView(title string, parent *overlayState, work func(context.Context) (func(), error)) {
	if a.xcode == nil {
		a.status = "Xcode tools are unavailable"
		return
	}
	ctx, cancel := context.WithCancel(a.baseContext())
	pending := &overlayState{kind: "test-loading", title: title, message: "Loading...  Esc to cancel", parent: parent, cancel: cancel}
	a.overlay = pending
	mode, generation := a.mode, a.generation.Load()
	go func() {
		defer cancel()
		apply, err := work(ctx)
		a.update(func() {
			if a.overlay != pending || a.mode != mode || a.generation.Load() != generation {
				return
			}
			if err != nil {
				a.overlay = parent
				a.status = err.Error()
				return
			}
			apply()
		})
	}()
}

func (a *App) openTestActivity(*gocui.Gui, *gocui.View) error {
	if len(a.records) == 0 || a.buildIndex >= len(a.records) {
		return nil
	}
	record := a.records[a.buildIndex]
	if record.Phase.Active() {
		a.status = "Test results are available after the activity finishes"
		return nil
	}
	if record.OperationKind() == model.OperationDiscoverTests {
		if record.Phase == model.PhaseSucceeded {
			a.openDiscoveredTests(record)
		} else {
			a.status = "Test discovery did not complete; press t to try again"
		}
		return nil
	}
	if record.OperationKind() != model.OperationTest {
		a.status = "Select a test activity to inspect results"
		return nil
	}
	if record.ResultBundlePath == "" {
		a.status = "This older activity has no retained result bundle; run tests again to collect results"
		return nil
	}
	if _, err := os.Stat(record.ResultBundlePath); err != nil {
		a.status = "Result bundle is unavailable; the run may have stopped before producing results"
		return nil
	}
	a.overlay = a.testActivityMenu(record)
	return nil
}

func (a *App) testActivityMenu(record model.BuildRecord) *overlayState {
	menu := &overlayState{kind: "test-actions", title: "Test Run #" + shortID(record.ID), items: []overlayItem{
		{ID: "results", Label: "Browse Test Results..."},
		{ID: "failed", Label: "Rerun Failed Tests"},
		{ID: "coverage", Label: "Browse Code Coverage..."},
		{ID: "compare", Label: "Compare Coverage with Earlier Run..."},
		{ID: "open", Label: "Open Result Bundle in Xcode"},
	}}
	if a.mode == modeCloud {
		menu.title = "Cloud Test Results"
		menu.items = []overlayItem{{ID: "results", Label: "Browse Test Results..."}, {ID: "coverage", Label: "Browse Code Coverage..."}, {ID: "open", Label: "Open Result Bundle in Xcode"}}
	}
	menu.choose = func(id string) {
		switch id {
		case "results", "failed":
			a.loadTestView("Test Results", menu, func(ctx context.Context) (func(), error) {
				tests, err := a.xcode.TestResults(ctx, record.ResultBundlePath)
				return func() {
					if id == "failed" {
						var ids []string
						seen := map[string]bool{}
						for _, test := range tests {
							if test.Result == "Failed" && test.Identifier != "" && !seen[test.Identifier] {
								ids = append(ids, test.Identifier)
								seen[test.Identifier] = true
							}
						}
						a.rerunTests(record, ids, "Failed Tests")
					} else {
						a.showTestCases(record, tests, menu, false)
					}
				}, err
			})
		case "coverage":
			a.openCoverage(record, menu)
		case "compare":
			a.openCoverageBaseline(record, menu)
		case "open":
			open := a.openXcode
			if open == nil {
				open = openXcode
			}
			if err := open(record.ResultBundlePath); err != nil {
				a.status = err.Error()
			}
		}
	}
	return menu
}

func (a *App) rerunTests(record model.BuildRecord, ids []string, scope string) {
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			a.status = "This result has no runnable test identifier"
			return
		}
	}
	if len(ids) == 0 {
		a.overlay = a.testActivityMenu(record)
		a.status = "No runnable tests in this selection"
		return
	}
	// The historical destination and scheme are intentional, even if the Build
	// pane has since changed. The normal build action recompiles edited sources.
	a.queueTestRequest(buildmanager.Request{Container: record.Container, Scheme: record.Scheme, Simulator: record.Simulator,
		Operation: model.OperationTest, TestScope: scope, Targets: ids, Coverage: record.Coverage})
}

func (a *App) openDiscoveredTests(record model.BuildRecord) {
	tests, err := xcode.ReadEnumeratedTests(record.EnumerationPath)
	if err != nil {
		a.status = err.Error()
		return
	}
	a.showTestCases(record, tests, nil, true)
}

func (a *App) showTestCases(record model.BuildRecord, tests []xcode.TestCase, parent *overlayState, run bool) {
	if len(tests) == 0 {
		a.overlay = parent
		a.status = "No tests were found in this run"
		return
	}
	menu := &overlayState{kind: "test-cases", title: "Test Results", parent: parent}
	if run {
		menu.title = "Run Individual Test"
	}
	for i, test := range tests {
		label := fmt.Sprintf("%-8s %s  %s", testResultLabel(test.Result), test.Name, test.Duration)
		if run {
			label = test.Name
		}
		menu.items = append(menu.items, overlayItem{ID: strconv.Itoa(i), Label: label})
	}
	menu.choose = func(id string) {
		i, err := strconv.Atoi(id)
		if err != nil || i < 0 || i >= len(tests) {
			return
		}
		test := tests[i]
		if run {
			record.Coverage = !a.testCoverageOff
			a.rerunTests(record, []string{test.Identifier}, test.Name)
			return
		}
		actions := a.testCaseMenu(record, test, menu)
		a.loadTestText(record, "Test Details", actions, menu, func(ctx context.Context) (string, error) {
			return a.xcode.TestDetails(ctx, record.ResultBundlePath, test.ID)
		})
	}
	a.overlay = menu
}

func testResultLabel(result string) string {
	switch result {
	case "Passed":
		return "PASS"
	case "Failed":
		return "FAIL"
	case "Skipped":
		return "SKIP"
	case "Expected Failure":
		return "EXPECTED"
	default:
		return result
	}
}

func (a *App) loadTestText(record model.BuildRecord, title string, actions, parent *overlayState, work func(context.Context) (string, error)) {
	a.loadTestView(title, parent, func(ctx context.Context) (func(), error) {
		text, err := work(ctx)
		return func() { a.showTestOutput(record, title, text, actions) }, err
	})
}

func (a *App) testCaseMenu(record model.BuildRecord, test xcode.TestCase, parent *overlayState) *overlayState {
	menu := &overlayState{kind: "test-case-actions", title: test.Name, parent: parent, items: []overlayItem{
		{ID: "details", Label: "Failure Details and Test Runs"},
		{ID: "activities", Label: "Test Activities"},
		{ID: "attachments", Label: "Export and Browse Attachments..."},
		{ID: "run", Label: "Run This Test"},
		{ID: "back", Label: "Back to Test Results"},
	}}
	if a.mode == modeCloud {
		menu.items = append(menu.items[:3], menu.items[4:]...)
	}
	menu.choose = func(id string) {
		switch id {
		case "back":
			a.overlay = parent
		case "run":
			a.rerunTests(record, []string{test.Identifier}, test.Name)
		case "details", "activities":
			title := "Test Details"
			if id == "activities" {
				title = "Test Activities"
			}
			a.loadTestText(record, title, menu, menu, func(ctx context.Context) (string, error) {
				if id == "activities" {
					return a.xcode.TestActivities(ctx, record.ResultBundlePath, test.ID)
				}
				return a.xcode.TestDetails(ctx, record.ResultBundlePath, test.ID)
			})
		case "attachments":
			a.loadTestView("Attachments", menu, func(ctx context.Context) (func(), error) {
				dir, err := a.xcode.ExportTestAttachments(ctx, record.ResultBundlePath, test.ID)
				if err != nil {
					return nil, err
				}
				var files []overlayItem
				err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if !entry.Type().IsRegular() || entry.Name() == "manifest.json" {
						return nil
					}
					rel, err := filepath.Rel(dir, path)
					if err != nil {
						return err
					}
					files = append(files, overlayItem{ID: path, Label: rel})
					return nil
				})
				return func() {
					if len(files) == 0 {
						a.overlay = menu
						a.status = "No attachments recorded for this test"
						return
					}
					a.overlay = &overlayState{kind: "test-attachments", title: "Attachments", parent: menu, items: files, choose: func(path string) {
						if err := exec.Command("open", path).Run(); err != nil {
							a.status = err.Error()
						} else {
							a.status = "Opened " + filepath.Base(path)
						}
					}}
				}, err
			})
		}
	}
	return menu
}

func coverageLabel(ratio float64, covered, executable int) string {
	if executable == 0 {
		return "    n/a  0 lines"
	}
	return fmt.Sprintf("%6.1f%%  %d/%d lines", ratio*100, covered, executable)
}

func (a *App) openCoverage(record model.BuildRecord, parent *overlayState) {
	a.loadTestView("Code Coverage", parent, func(ctx context.Context) (func(), error) {
		report, err := a.xcode.Coverage(ctx, record.ResultBundlePath)
		return func() {
			menu := &overlayState{kind: "coverage-targets", title: "Coverage by Target", parent: parent}
			var b strings.Builder
			fmt.Fprintf(&b, "Code coverage - %s\n%s\n\n", record.TestScope, coverageLabel(report.LineCoverage, report.CoveredLines, report.ExecutableLines))
			for i, target := range report.Targets {
				label := coverageLabel(target.LineCoverage, target.CoveredLines, target.ExecutableLines) + "  " + target.Name
				fmt.Fprintln(&b, label)
				menu.items = append(menu.items, overlayItem{ID: strconv.Itoa(i), Label: label})
			}
			menu.choose = func(id string) {
				i, err := strconv.Atoi(id)
				if err != nil || i < 0 || i >= len(report.Targets) {
					return
				}
				a.showCoverageFiles(record, report.Targets[i], menu)
			}
			fmt.Fprintln(&b, "\n[Enter] Browse targets and source files")
			a.showTestOutput(record, "Coverage", b.String(), menu)
		}, err
	})
}

func (a *App) showCoverageFiles(record model.BuildRecord, target xcode.CoverageTarget, parent *overlayState) {
	menu := &overlayState{kind: "coverage-files", title: target.Name + " Coverage", parent: parent}
	for i, file := range target.Files {
		menu.items = append(menu.items, overlayItem{ID: strconv.Itoa(i), Label: coverageLabel(file.LineCoverage, file.CoveredLines, file.ExecutableLines) + "  " + file.Path})
	}
	menu.choose = func(id string) {
		i, err := strconv.Atoi(id)
		if err != nil || i < 0 || i >= len(target.Files) {
			return
		}
		file := target.Files[i]
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n%s\n\n", file.Path, coverageLabel(file.LineCoverage, file.CoveredLines, file.ExecutableLines))
		for _, function := range file.Functions {
			fmt.Fprintf(&b, "%s  line %-5d %s\n", coverageLabel(function.LineCoverage, function.CoveredLines, function.ExecutableLines), function.LineNumber, function.Name)
		}
		a.showTestOutput(record, "File Coverage", b.String(), menu)
	}
	a.overlay = menu
}

func coverageBaselines(records []model.BuildRecord, current model.BuildRecord) []model.BuildRecord {
	var result []model.BuildRecord
	for _, record := range records {
		if record.ID == current.ID || record.OperationKind() != model.OperationTest || record.Phase.Active() || record.ResultBundlePath == "" || !record.Coverage {
			continue
		}
		if record.Container.Path != current.Container.Path || record.Scheme != current.Scheme || record.Simulator.ID != current.Simulator.ID || !record.StartedAt.Before(current.StartedAt) {
			continue
		}
		result = append(result, record)
	}
	return result
}

func (a *App) openCoverageBaseline(record model.BuildRecord, parent *overlayState) {
	if !record.Coverage {
		a.overlay = parent
		a.status = "This run did not enable coverage; enable it in the t menu and run tests again"
		return
	}
	records := coverageBaselines(a.records, record)
	if len(records) == 0 {
		a.overlay = parent
		a.status = "No earlier coverage runs for this scheme and destination"
		return
	}
	menu := &overlayState{kind: "coverage-baseline", title: "Compare Coverage - Select Earlier Run", parent: parent,
		message: "Earlier run -> selected run. Different test scopes affect coverage.\n"}
	for i, baseline := range records {
		menu.items = append(menu.items, overlayItem{ID: strconv.Itoa(i), Label: fmt.Sprintf("%s  #%s  %s  %s", baseline.StartedAt.Format("Jan 02 15:04"), shortID(baseline.ID), recordPhaseLabel(baseline), baseline.TestScope)})
	}
	menu.choose = func(id string) {
		i, err := strconv.Atoi(id)
		if err != nil || i < 0 || i >= len(records) {
			return
		}
		before := records[i]
		a.loadTestText(record, "Coverage Comparison", parent, menu, func(ctx context.Context) (string, error) {
			difference, err := a.xcode.CompareCoverage(ctx, before.ResultBundlePath, record.ResultBundlePath)
			heading := fmt.Sprintf("Coverage: #%s -> #%s\n%s -> %s\nChanges are percentage points. Different test scopes affect coverage.\n\n", shortID(before.ID), shortID(record.ID), before.TestScope, record.TestScope)
			return heading + difference, err
		})
	}
	a.overlay = menu
}

// Cloud stays read-only. Downloading is the existing artifact action; Enter
// on a cached result artifact opens the same inspector used by local tests.
func (a *App) openCloudTestActivity(*gocui.Gui, *gocui.View) error {
	c := a.cloud
	if c == nil || a.project == nil {
		return nil
	}
	run, ok := c.selectedRun()
	if !ok {
		return nil
	}
	details, ok := c.details[run.ID]
	if !ok {
		a.status = "Wait for build details to load"
		return nil
	}
	artifacts := map[string]xcodecloud.Artifact{}
	menu := &overlayState{kind: "cloud-test-results", title: fmt.Sprintf("Test Results - Cloud #%d", run.Number)}
	for _, action := range details.Actions {
		for _, artifact := range action.Artifacts {
			if artifact.Type != xcodecloud.ArtifactResultBundle {
				continue
			}
			artifacts[artifact.ID] = artifact
			path, err := a.project.CloudArtifactPath(run.ID, artifact.ID, artifact.Name)
			if err != nil {
				continue
			}
			label := "Download  "
			if _, err := os.Stat(path); err == nil {
				label = "Browse    "
			}
			menu.items = append(menu.items, overlayItem{ID: artifact.ID, Label: label + action.Name + " / " + artifact.Name})
		}
	}
	if len(menu.items) == 0 {
		a.status = "This Cloud run has no result-bundle artifacts"
		return nil
	}
	project := a.project
	menu.choose = func(id string) {
		artifact, ok := artifacts[id]
		if !ok {
			return
		}
		path, err := project.CloudArtifactPath(run.ID, id, artifact.Name)
		if err != nil {
			a.status = err.Error()
			return
		}
		if _, err := os.Stat(path); err != nil {
			a.downloadSelectedCloudArtifact(id)
			return
		}
		a.loadTestView("Cloud Test Results", menu, func(ctx context.Context) (func(), error) {
			bundle, err := store.ExpandResultBundle(ctx, path)
			return func() {
				a.overlay = a.testActivityMenu(model.BuildRecord{ID: run.ID, ResultBundlePath: bundle, TestScope: "Cloud tests"})
				a.overlay.parent = menu
			}, err
		})
	}
	a.overlay = menu
	return nil
}

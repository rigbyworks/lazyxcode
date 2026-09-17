package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jesseduffield/gocui"

	buildmanager "github.com/rigbyworks/lazyxcode/internal/build"
	"github.com/rigbyworks/lazyxcode/internal/model"
)

func TestConsoleBurstSchedulesOneUIUpdate(t *testing.T) {
	updates := make(chan func(), 1001)
	a := &App{dispatch: func(fn func()) { updates <- fn }}
	record := model.BuildRecord{ID: "run", Phase: model.PhaseRunning}
	for i := range 1000 {
		a.handleBuildEvent(buildmanager.Event{Record: record, Output: "console line\n", Sequence: uint64(i + 1)})
	}
	if queued := len(updates); queued > 1 {
		t.Fatalf("console burst scheduled %d UI updates, want at most one", queued)
	}
	select {
	case apply := <-updates:
		apply()
	case <-time.After(time.Second):
		t.Fatal("console update was not scheduled")
	}
	if len(a.records) != 1 || a.records[0].Phase != model.PhaseRunning {
		t.Fatalf("activity = %#v", a.records)
	}
}

func TestLongConsoleKeepsSummaryAndBoundsLiveWindow(t *testing.T) {
	log := newActivityLog(model.OperationRun)
	log.append("[lazyxcode] Building App\n[lazyxcode:step] done 1500 0 Compile\n/src/App.swift:2:3: warning: keep this diagnostic\n[lazyxcode] App console\noldest console line\n")
	for i := range 10000 {
		log.append(fmt.Sprintf("line %05d %s\n", i, strings.Repeat("x", 100)))
	}
	raw := log.text(true, time.Now(), model.PhaseRunning)
	concise := log.text(false, time.Now(), model.PhaseRunning)
	for _, text := range []string{raw, concise} {
		if strings.Contains(text, "oldest console line") || strings.Contains(text, "line 00000") {
			t.Fatal("old console output retained")
		}
		if !strings.Contains(text, "line 09999") || !strings.Contains(text, "Earlier output omitted") {
			t.Fatal("tail or truncation notice missing")
		}
		if len(text) > 270000 || strings.Count(text, "\n") > 2020 {
			t.Fatalf("unbounded window: %d bytes, %d lines", len(text), strings.Count(text, "\n"))
		}
	}
	if !strings.Contains(concise, "Compile") || !strings.Contains(concise, "keep this diagnostic") {
		t.Fatal("eviction lost build summary")
	}
}

func TestFragmentedConsolePreservesUnicodeANSIAndFinalPartialLine(t *testing.T) {
	log := newActivityLog(model.OperationRun)
	transcript := "[lazyxcode] Building App\n\x1b[33m/src/App.swift:2:3: warning: hello\x1b[0m\n[lazyxcode] App console\n日本語\n\x1b[32mfinal ✓\x1b[0m"
	for i := range len(transcript) {
		log.append(transcript[i : i+1])
	}
	log.finish()
	text := log.text(false, time.Now(), model.PhaseSucceeded)
	for _, want := range []string{"warning: App.swift:2:3 — hello", "日本語", "final ✓"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") || !utf8.ValidString(text) {
		t.Fatalf("broken text: %q", text)
	}
}

func TestCompletedLongConsoleLinePreservesUTF8(t *testing.T) {
	for _, chunkRunes := range []int{2000, 1000} {
		for _, newline := range []bool{true, false} {
			t.Run(fmt.Sprintf("chunkRunes=%d/newline=%t", chunkRunes, newline), func(t *testing.T) {
				log := newActivityLog(model.OperationRun)
				log.append("[lazyxcode] App console\nshort line\n")
				for written := 0; written < 2000; written += chunkRunes {
					log.append(strings.Repeat("界", chunkRunes))
				}
				if newline {
					log.append("\n")
				}
				log.finish()
				text := log.text(false, time.Now(), model.PhaseSucceeded)
				if !utf8.ValidString(text) {
					t.Fatal("completed long console line contains invalid UTF-8")
				}
				want := "APP CONSOLE\n" + omittedOutput + "short line\n" + strings.Repeat("界", 1365)
				if text != want {
					t.Fatal("completed long console line did not preserve the bounded tail and preceding line")
				}
			})
		}
	}
}

func TestHugeUnterminatedLineIsBoundedAndRecoversAtNewline(t *testing.T) {
	log := newActivityLog(model.OperationRun)
	log.append("[lazyxcode] App console\n")
	chunk := strings.Repeat("界", 10000)
	for range 100 {
		log.append(chunk)
	}
	for _, verbose := range []bool{false, true} {
		text := log.text(verbose, time.Now(), model.PhaseRunning)
		if len(text) > 5000 || !utf8.ValidString(text) {
			t.Fatalf("unbounded or invalid long line: %d bytes", len(text))
		}
	}
	log.append("\nnormal line\n")
	if !strings.Contains(log.text(false, time.Now(), model.PhaseRunning), "normal line") {
		t.Fatal("failed to recover after oversized line")
	}
}

func TestTestSummarySurvivesRawEvictionAndCountsOnce(t *testing.T) {
	log := newActivityLog(model.OperationTest)
	log.append("[lazyxcode] Testing App\nTest Suite 'Checks' started\nTest Case '-[AppTests.Checks testPass]' started\nTest Case '-[AppTests.Checks testPass]' passed (0.25 seconds)\n")
	for range 5000 {
		log.append("irrelevant test console output\n")
	}
	log.append("** TEST SUCCEEDED **")
	log.finish()
	for range 3 {
		text := log.text(false, time.Now(), model.PhaseSucceeded)
		if !strings.Contains(text, "1 passed") || !strings.Contains(text, "TEST SUCCEEDED") {
			t.Fatalf("summary = %s", text)
		}
	}
}

func TestDelayedUIKeepsOneNotificationAndFinalState(t *testing.T) {
	updates := make(chan func(), 100)
	a := &App{dispatch: func(fn func()) { updates <- fn }}
	record := model.BuildRecord{ID: "run", Phase: model.PhaseRunning}
	a.handleBuildEvent(buildmanager.Event{Record: record, Output: "[lazyxcode] App console\n"})
	var apply func()
	select {
	case apply = <-updates:
	case <-time.After(time.Second):
		t.Fatal("no update")
	}
	// Hold the dispatched callback while capture continues past both buffer limits.
	for i := range 10000 {
		a.handleBuildEvent(buildmanager.Event{Record: record, Output: fmt.Sprintf("line %d\n", i)})
	}
	record.Phase = model.PhaseCancelled
	a.handleBuildEvent(buildmanager.Event{Record: record, Output: "final fragment"})
	if len(updates) != 0 {
		t.Fatalf("queued %d extra callbacks behind the blocked UI", len(updates))
	}
	apply()
	if len(a.records) != 1 || a.records[0].Phase != model.PhaseCancelled {
		t.Fatalf("final state: %#v", a.records)
	}
	text := a.outputs[record.ID].text(false, time.Now(), record.Phase)
	if !strings.HasSuffix(text, "final fragment") || strings.Contains(text, "line 0\n") {
		t.Fatalf("incorrect tail: %s", text)
	}
	if len(a.captureLogs) != 0 {
		t.Fatal("completed capture was not released")
	}
}

func TestCaptureAndRenderingCanRunConcurrently(t *testing.T) {
	log := newActivityLog(model.OperationRun)
	log.append("[lazyxcode] App console\n")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			log.append("concurrent output\n")
		}
		log.finish()
	}()
	for range 20 {
		log.formatted(false, time.Now(), model.PhaseRunning)
	}
	<-done
}

func TestRawLogPagingHasNoGapsAndReturnsToLive(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	file, path, err := a.project.NewLog("pages")
	if err != nil {
		t.Fatal(err)
	}
	var transcript strings.Builder
	for i := range 6000 {
		fmt.Fprintf(&transcript, "line %05d %s\n", i, strings.Repeat("x", 80))
	}
	if _, err = file.WriteString(transcript.String()); err != nil {
		t.Fatal(err)
	}
	file.Close()
	record := model.BuildRecord{ID: "pages", Phase: model.PhaseSucceeded, LogPath: path}
	a.records = []model.BuildRecord{record}
	var pages []string
	for {
		if err = a.olderOutputPage(h.gui, nil); err != nil {
			t.Fatal(err)
		}
		page := a.selectedOutputPage()
		if page == nil {
			t.Fatalf("no page: %s", a.status)
		}
		pages = append(pages, page.log.raw.text())
		if page.start == 0 {
			break
		}
	}
	var assembled strings.Builder
	for i := len(pages) - 1; i >= 0; i-- {
		assembled.WriteString(pages[i])
	}
	if assembled.String() != transcript.String() {
		t.Fatalf("paging lost or duplicated content: %d vs %d bytes", assembled.Len(), transcript.Len())
	}
	for range len(pages) {
		a.newerOutputPage(h.gui, nil)
	}
	if a.selectedOutputPage() != nil || !a.outputFollow {
		t.Fatal("did not return to live output")
	}
}

func BenchmarkConsoleSteadyState(b *testing.B) {
	for _, historyLines := range []int{10000, 100000} {
		b.Run(fmt.Sprintf("history_%d", historyLines), func(b *testing.B) {
			log := newActivityLog(model.OperationRun)
			log.append("[lazyxcode] App console\n")
			line := "[app] steady console output with enough text to exercise wrapping and formatting\n"
			for range historyLines {
				log.append(line)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for range 10 {
					log.append(line)
				}
				log.formatted(false, time.Unix(1, 0), model.PhaseRunning)
			}
		})
	}
}

func conciseBuildOutputAt(raw string, now time.Time) string {
	log := newActivityLog(model.OperationBuild)
	log.append(raw)
	log.finish()
	return log.text(false, now, model.PhaseBuilding)
}

func conciseTestOutputAt(raw string, now time.Time, phase model.Phase) string {
	log := newActivityLog(model.OperationTest)
	log.append(raw)
	log.finish()
	return log.text(false, now, phase)
}

func logWithText(text string) *activityLog {
	log := newActivityLog(model.OperationBuild)
	log.append(text)
	return log
}

func TestHistoricalLoadPreservesSummaryAndEvictsOtherHistory(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	file, path, err := a.project.NewLog("history")
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString("[lazyxcode] Building Saved\n/src/Saved.swift:1:1: warning: saved diagnostic\n[lazyxcode] App console\n")
	for range 6000 {
		file.WriteString("historical output\n")
	}
	file.WriteString("last historical line\n")
	file.Close()
	a.records = []model.BuildRecord{{ID: "history", Phase: model.PhaseSucceeded, LogPath: path}, {ID: "old", Phase: model.PhaseSucceeded}, {ID: "active", Phase: model.PhaseRunning}}
	a.outputs = map[string]*activityLog{"old": logWithText("old"), "active": logWithText("active")}
	a.loadSelectedOutput()
	h.drain()
	text := a.outputs["history"].text(false, time.Now(), model.PhaseSucceeded)
	if !strings.Contains(text, "saved diagnostic") || !strings.Contains(text, "last historical line") {
		t.Fatalf("history load = %s", text)
	}
	if a.outputs["old"] != nil || a.outputs["active"] == nil {
		t.Fatal("cache must retain active logs and evict unselected history")
	}
}

func TestSummaryLimitsRemainBounded(t *testing.T) {
	log := newActivityLog(model.OperationTest)
	for i := range 1000 {
		log.append(fmt.Sprintf("Test Suite 'Suite%d' started\n/src/App.swift:%d:1: warning: diagnostic %d\n", i, i, i))
	}
	text := log.text(false, time.Now(), model.PhaseTesting)
	if !strings.Contains(text, "TEST SUITES (256)") || !strings.Contains(text, "DIAGNOSTICS (256)") || !strings.Contains(text, "Summary limit reached") {
		t.Fatal("summary did not enforce or explain its bounds")
	}
}

func BenchmarkConsoleLayout(b *testing.B) {
	for _, history := range []int{10000, 100000} {
		b.Run(fmt.Sprintf("history_%d", history), func(b *testing.B) {
			g, err := gocui.NewGui(gocui.NewGuiOpts{OutputMode: gocui.OutputTrue, Headless: true, Width: 120, Height: 36})
			if err != nil {
				b.Fatal(err)
			}
			defer g.Close()
			log := newActivityLog(model.OperationRun)
			log.append("[lazyxcode] App console\n")
			line := "[app] steady console output with enough text to exercise wrapping and formatting\n"
			for range history {
				log.append(line)
			}
			a := &App{gui: g, focus: "output", outputFollow: true, records: []model.BuildRecord{{ID: "run", Phase: model.PhaseRunning}}, outputs: map[string]*activityLog{"run": log}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				log.append(fmt.Sprintf("%08d %s", i, line))
				if err = a.layout(g); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestFollowRendersViewportAndScrollFreezesRecentWindow(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	a.project = nil
	a.records = []model.BuildRecord{{ID: "run", Phase: model.PhaseRunning}}
	log := newActivityLog(model.OperationRun)
	log.append("[lazyxcode] App console\n")
	for i := range 3000 {
		log.append(fmt.Sprintf("console %04d\n", i))
	}
	a.outputs = map[string]*activityLog{"run": log}
	a.outputFollow = true
	h.layout()
	view := h.view("output")
	_, height := view.InnerSize()
	if count := strings.Count(view.Buffer(), "\n"); count > height+2 {
		t.Fatalf("follow materialized %d lines for %d rows", count, height)
	}
	var copied string
	a.copyToClipboard = func(text string) error { copied = text; return nil }
	if err := a.copyOutput(h.gui, view); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(copied, "console 1000") || !strings.Contains(copied, "console 2999") {
		t.Fatal("copy lost the retained window")
	}
	a.scrollOutput(-1)(h.gui, view)
	h.layout()
	frozen := view.Buffer()
	if !strings.Contains(frozen, "console 1000") {
		t.Fatal("scroll did not expand recent history")
	}
	log.append("new output while paused\n")
	h.layout()
	if view.Buffer() != frozen {
		t.Fatal("stream moved the paused output")
	}
	a.outputEnd(true)(h.gui, view)
	h.layout()
	if !strings.Contains(view.Buffer(), "new output while paused") {
		t.Fatal("follow did not resume")
	}
}

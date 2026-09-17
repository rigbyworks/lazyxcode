package tui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jesseduffield/gocui"
	"github.com/rigbyworks/lazyxcode/internal/model"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

var diagnosticLocationPattern = regexp.MustCompile(`^(.+:\d+:\d+:)(.*)$`)

var sourceDiagnosticPattern = regexp.MustCompile(`^(.+):(\d+):(\d+):\s+(fatal error|error|warning):\s+(.+)$`)

var conciseDiagnosticPattern = regexp.MustCompile(`^(\s*)(fatal error|error|warning):\s+(.+?)\s+(—|-)\s+(.+)$`)

var (
	xctestSuiteStart  = regexp.MustCompile(`^Test Suite '(.+)' started`)
	xctestSuiteFinish = regexp.MustCompile(`^Test Suite '(.+)' (passed|failed)`)
	xctestCaseStart   = regexp.MustCompile(`^Test Case '(.+)' started`)
	xctestCaseFinish  = regexp.MustCompile(`^Test Case '(.+)' (passed|failed) \(([0-9.]+) seconds\)`)
	xctestCaseSuite   = regexp.MustCompile(`^[-+]\[[^.]+\.([^ ]+) `)
	swiftSuiteStart   = regexp.MustCompile(`^[◇◆] Suite "?(.+?)"? started`)
	swiftSuiteFinish  = regexp.MustCompile(`^[✔✘] Suite "?(.+?)"? (passed|failed) after ([0-9.]+) seconds`)
	swiftTestIssue    = regexp.MustCompile(`^✘ Test .+ recorded an issue at (.+):(\d+):(\d+):\s+(.+)$`)
)

const (
	ansiReset      = "\x1b[0m"
	ansiBoldRed    = "\x1b[1;31m"
	ansiBoldYellow = "\x1b[1;33m"
	ansiBoldGreen  = "\x1b[1;32m"
	ansiBoldCyan   = "\x1b[1;36m"
	ansiCyan       = "\x1b[36m"
)

func (a *App) render(g *gocui.Gui) error {
	buildView, _ := g.View("build")
	buildsView, _ := g.View("builds")
	outputView, _ := g.View("output")
	if buildView == nil || buildsView == nil || outputView == nil {
		return nil
	}
	if a.mode == modeCloud {
		a.renderedOutputView = nil
		return a.renderCloud(buildView, buildsView, outputView)
	}
	return a.renderLocal(buildView, buildsView, outputView)
}

func (a *App) renderLocal(buildView, buildsView, outputView *gocui.View) error {
	buildView.Clear()
	buildWidth, buildHeight := buildView.InnerSize()
	container := valueOr(a.container.Name, "-")
	scheme := "Loading..."
	if len(a.schemes) > 0 && a.scheme < len(a.schemes) {
		scheme = a.schemes[a.scheme]
	}
	simulator := "Loading..."
	if !a.loading && len(a.sims) == 0 {
		simulator = "No compatible targets"
	} else if len(a.sims) > 0 && a.simulator < len(a.sims) {
		simulator = a.sims[a.simulator].Label()
	}
	prefix0, prefix1 := "  ", "  "
	if a.focus == "build" {
		if a.configRow == 0 {
			prefix0 = "> "
		} else {
			prefix1 = "> "
		}
	}
	button := "[b] Build   [r] Run   [t] Test"
	if a.loading || len(a.schemes) == 0 || len(a.sims) == 0 {
		button = "Build and tests unavailable"
	}
	if buildHeight >= 7 {
		writeViewLine(buildView, buildWidth, "  Container   "+container)
		writeViewLine(buildView, buildWidth, prefix0+"Scheme      "+scheme+"  [>]")
		writeViewLine(buildView, buildWidth, prefix1+"Target      "+simulator+"  [>]")
		writeViewLine(buildView, buildWidth, "")
		writeViewLine(buildView, buildWidth, "  "+button)
		writeViewLine(buildView, buildWidth, "  Cache: "+formatBytes(a.cacheSize)+"  [c] Clear")
	} else if buildHeight > 0 {
		lines := []string{
			"  Project  " + container,
			prefix0 + "Scheme   " + scheme + " [>]",
			prefix1 + "Device   " + simulator + " [>]",
			"  [b] Build  [r] Run  [t] Test  [c] Cache " + formatBytes(a.cacheSize),
		}
		for i := 0; i < min(buildHeight, len(lines)); i++ {
			writeViewLine(buildView, buildWidth, lines[i])
		}
	}

	buildsView.Clear()
	buildsWidth, _ := buildsView.InnerSize()
	for _, record := range a.records {
		fmt.Fprintln(buildsView, formatBuildRow(record, buildsWidth))
	}
	if len(a.records) == 0 {
		fmt.Fprintln(buildsView, "  No activity yet")
	}
	buildsView.Highlight = len(a.records) > 0
	if len(a.records) > 0 {
		if a.buildIndex >= len(a.records) {
			a.buildIndex = len(a.records) - 1
		}
		setListCursor(buildsView, a.buildIndex, len(a.records), 0)
	} else {
		setListCursor(buildsView, 0, 0, 0)
	}

	output := "Select a scheme and target, then press b to build."
	var record model.BuildRecord
	if len(a.records) > 0 {
		a.loadSelectedOutput()
		record = a.records[a.buildIndex]
		if report := a.selectedTestOutput(); report != nil {
			output = formatBuildOutput(report.text)
		} else if paused := a.selectedOutputSnapshot(); paused != nil {
			output = paused.text
		} else {
			if page := a.selectedOutputPage(); page != nil {
				output = page.log.formatted(true, time.Now(), record.Phase)
			} else if log := a.outputs[record.ID]; log != nil {
				output = log.formatted(a.verboseOutput, time.Now(), record.Phase)
			} else {
				output = "Loading output..."
			}
			if record.OperationKind() == model.OperationTest && !record.Phase.Active() && record.ResultBundlePath != "" {
				output += "\n[Enter] Results, rerun failures, coverage\n"
			}
			if record.OperationKind() == model.OperationDiscoverTests && record.Phase == model.PhaseSucceeded {
				output += "\n[Enter] Choose a test to run\n"
			}
			if record.Error != "" {
				output += fmt.Sprintf("\n%s[%s] %s%s\n", ansiBoldRed, phaseLabel(record.Phase), record.Error, ansiReset)
			}
		}
	}
	a.outputFullText = output
	if a.outputFollow && a.selectedTestOutput() == nil && a.selectedOutputPage() == nil {
		_, height := outputView.InnerSize()
		output = lastOutputLines(output, height+1)
	}
	// Keep gocui's cells and wrapping cache on timer ticks and unrelated input.
	if a.renderedOutputView != outputView || a.renderedOutput != output {
		outputView.Clear()
		fmt.Fprint(outputView, output)
		a.renderedOutputView, a.renderedOutput = outputView, output
	}
	if a.outputFollow && a.selectedTestOutput() == nil && a.selectedOutputPage() == nil {
		scrollOutputToBottom(outputView)
	} else {
		clampOutputOrigin(outputView)
	}

	return nil
}

type diagnosticSeverity int

const (
	diagnosticNone diagnosticSeverity = iota
	diagnosticWarning
	diagnosticError
	diagnosticNote
)

func formatBuildOutput(output string) string {
	lines := strings.Split(output, "\n")
	severity := diagnosticNone
	for i, line := range lines {
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "** build failed **") || strings.Contains(lower, "** test failed **"):
			lines[i] = ansiBoldRed + line + ansiReset
			severity = diagnosticNone
		case strings.Contains(lower, "** build succeeded **") || strings.Contains(lower, "** test succeeded **"):
			lines[i] = ansiBoldGreen + line + ansiReset
			severity = diagnosticNone
		case strings.HasPrefix(line, "✔ Test run with "):
			lines[i] = ansiBoldGreen + line + ansiReset
		case strings.HasPrefix(line, "✘ Test run with "):
			lines[i] = ansiBoldRed + line + ansiReset
		case isOutputHeading(line):
			lines[i] = ansiBoldCyan + line + ansiReset
			severity = diagnosticNone
		case strings.HasPrefix(line, "  ✓ "):
			lines[i] = colorToken(line, "✓", ansiBoldGreen)
		case strings.HasPrefix(line, "  ● "):
			lines[i] = colorToken(line, "●", ansiCyan)
		case strings.HasPrefix(line, "  ✗ "):
			lines[i] = colorToken(line, "✗", ansiBoldRed)
		case conciseDiagnosticPattern.MatchString(line):
			lines[i], severity = formatConciseDiagnosticLine(line)
		case diagnosticToken(lower, "fatal error:") >= 0:
			lines[i] = formatDiagnosticLine(line, "fatal error:", ansiBoldRed)
			severity = diagnosticError
		case diagnosticToken(lower, "error:") >= 0:
			lines[i] = formatDiagnosticLine(line, "error:", ansiBoldRed)
			severity = diagnosticError
		case diagnosticToken(lower, "warning:") >= 0:
			lines[i] = formatDiagnosticLine(line, "warning:", ansiBoldYellow)
			severity = diagnosticWarning
		case diagnosticToken(lower, "note:") >= 0:
			lines[i] = formatDiagnosticLine(line, "note:", ansiCyan)
			severity = diagnosticNote
		case isDiagnosticPointer(line) && severity != diagnosticNone:
			color := ansiCyan
			if severity == diagnosticWarning {
				color = ansiBoldYellow
			} else if severity == diagnosticError {
				color = ansiBoldRed
			}
			lines[i] = color + line + ansiReset
		case strings.HasPrefix(strings.TrimSpace(line), "[lazyxcode]"):
			lines[i] = colorPrefix(line, "[lazyxcode]", ansiCyan)
			severity = diagnosticNone
		case strings.TrimSpace(line) == "":
			severity = diagnosticNone
		}
	}
	return strings.Join(lines, "\n")
}

func isOutputHeading(line string) bool {
	switch line {
	case "BUILD STEPS", "BUILD PREPARATION", "DEPLOYMENT", "APP CONSOLE", "XCODE CLOUD", "TESTS":
		return true
	}
	for _, prefix := range []string{"DIAGNOSTICS", "TEST SUITES", "XCODE CLOUD BUILD", "ACTIONS (", "ARTIFACTS ("} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func formatConciseDiagnosticLine(line string) (string, diagnosticSeverity) {
	match := conciseDiagnosticPattern.FindStringSubmatch(line)
	if match == nil {
		return line, diagnosticNone
	}
	color, severity := ansiBoldYellow, diagnosticWarning
	if match[2] == "error" || match[2] == "fatal error" {
		color, severity = ansiBoldRed, diagnosticError
	}
	formatted := match[1] + color + match[2] + ":" + ansiReset + " " + ansiCyan + match[3] + ansiReset + " " + match[4] + " " + match[5]
	return formatted, severity
}

func formatDiagnosticLine(line, token, color string) string {
	formatted := line
	if match := diagnosticLocationPattern.FindStringSubmatch(line); match != nil {
		formatted = ansiCyan + match[1] + ansiReset + match[2]
	}
	return colorToken(formatted, token, color)
}

func diagnosticToken(lower, token string) int {
	index := strings.Index(lower, token)
	if index < 0 {
		return -1
	}
	if index == 0 || lower[index-1] == ' ' || lower[index-1] == ':' {
		return index
	}
	return -1
}

func colorToken(line, token, color string) string {
	lower := strings.ToLower(line)
	index := strings.Index(lower, token)
	if index < 0 {
		return line
	}
	return line[:index] + color + line[index:index+len(token)] + ansiReset + line[index+len(token):]
}

func colorPrefix(line, prefix, color string) string {
	index := strings.Index(line, prefix)
	if index < 0 {
		return line
	}
	return line[:index] + color + prefix + ansiReset + line[index+len(prefix):]
}

func isDiagnosticPointer(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return strings.Trim(trimmed, "^~ ") == ""
}

type conciseDiagnostic struct {
	severity string
	file     string
	line     string
	column   string
	message  string
}

type conciseStep struct {
	name       string
	order      int
	duration   time.Duration
	startedAt  time.Time
	inProgress bool
}

func (s *outputSummary) observeBuild(line string) {
	trimmed := strings.TrimSpace(line)
	if s.observeStep(trimmed) {
		return
	}
	if diagnostic, ok := parseConciseDiagnostic(line); ok {
		s.addDiagnostic(diagnostic)
		return
	}

	if strings.HasPrefix(trimmed, "[lazyxcode]") {
		if strings.Contains(trimmed, " Building ") {
			s.intro = trimmed
		} else if strings.Contains(trimmed, "Build succeeded;") || strings.Contains(trimmed, "Installing app") || strings.Contains(trimmed, "Launching ") {
			if len(s.deployment) < 8 {
				s.deployment = append(s.deployment, trimmed)
			}
		}
		return
	}
	if strings.Contains(trimmed, "** BUILD SUCCEEDED **") || strings.Contains(trimmed, "** BUILD FAILED **") {
		s.buildOutcome = trimmed
	}
}

func (s *outputSummary) buildText(now time.Time) string {
	result := make([]string, 0, 16)

	if s.intro != "" {
		result = append(result, s.intro, "")
	}
	if len(s.steps) > 0 {
		steps := append([]conciseStep(nil), s.steps...)
		sort.SliceStable(steps, func(i, j int) bool { return steps[i].order < steps[j].order })
		result = append(result, "BUILD STEPS")
		for _, step := range steps {
			duration := step.duration
			marker := "✓"
			if step.inProgress {
				marker = "●"
				duration += max(time.Duration(0), now.Sub(step.startedAt))
			}
			result = append(result, fmt.Sprintf("  %s %-22s %8s", marker, step.name, formatStepDuration(duration)))
		}
	}

	if len(s.diagnostics) > 0 {
		if len(result) > 0 {
			result = append(result, "")
		}
		result = append(result, fmt.Sprintf("DIAGNOSTICS (%d)", len(s.diagnostics)))
		for _, diagnostic := range s.diagnostics {
			location := diagnostic.file
			if diagnostic.line != "" {
				location += ":" + diagnostic.line + ":" + diagnostic.column
			}
			if location == "" {
				location = "Build"
			}
			result = append(result, fmt.Sprintf("  %s: %s — %s", diagnostic.severity, location, diagnostic.message))
		}
	}

	if len(s.deployment) > 0 {
		if len(result) > 0 {
			result = append(result, "")
		}
		result = append(result, "DEPLOYMENT")
		for _, line := range s.deployment {
			result = append(result, "  "+line)
		}
	}
	if s.buildOutcome != "" {
		if len(result) > 0 {
			result = append(result, "")
		}
		result = append(result, s.buildOutcome)
	}
	return strings.Join(result, "\n")
}

type conciseTestSuite struct {
	name        string
	status      string
	currentTest string
	passed      int
	failed      int
	duration    time.Duration
}

func (s *outputSummary) ensureSuite(name string) int {
	name = strings.TrimSpace(name)
	if index, ok := s.suiteIndexes[name]; ok {
		return index
	}
	if len(s.suites) >= maxSummaryEntries {
		s.truncated = true
		return -1
	}
	index := len(s.suites)
	s.suiteIndexes[name] = index
	s.suites = append(s.suites, conciseTestSuite{name: name})
	return index
}

func (s *outputSummary) observeTest(line string) {
	trimmed := strings.TrimSpace(line)
	if s.observeStep(trimmed) {
		return
	}
	if strings.HasPrefix(trimmed, "[lazyxcode]") && strings.Contains(trimmed, " Testing ") {
		s.intro = trimmed
		return
	}
	if match := xctestSuiteStart.FindStringSubmatch(trimmed); match != nil {
		index := s.ensureSuite(match[1])
		if index < 0 {
			return
		}
		s.suites[index].status = "running"
		return
	}
	if match := xctestSuiteFinish.FindStringSubmatch(trimmed); match != nil {
		index := s.ensureSuite(match[1])
		if index < 0 {
			return
		}
		s.suites[index].status = match[2]
		return
	}
	if match := xctestCaseStart.FindStringSubmatch(trimmed); match != nil {
		name := testSuiteForCase(match[1])
		index := s.ensureSuite(name)
		if index < 0 {
			return
		}
		s.suites[index].status, s.suites[index].currentTest = "running", testName(match[1])
		return
	}
	if match := xctestCaseFinish.FindStringSubmatch(trimmed); match != nil {
		index := s.ensureSuite(testSuiteForCase(match[1]))
		if index < 0 {
			return
		}
		if match[2] == "passed" {
			s.suites[index].passed++
		} else {
			s.suites[index].failed++
		}
		s.suites[index].duration += parseSeconds(match[3])
		s.suites[index].currentTest = ""
		return
	}
	if match := swiftSuiteStart.FindStringSubmatch(trimmed); match != nil {
		index := s.ensureSuite(match[1])
		if index < 0 {
			return
		}
		s.suites[index].status = "running"
		return
	}
	if match := swiftSuiteFinish.FindStringSubmatch(trimmed); match != nil {
		index := s.ensureSuite(match[1])
		if index < 0 {
			return
		}
		s.suites[index].status = match[2]
		s.suites[index].duration = parseSeconds(match[3])
		return
	}
	if match := swiftTestIssue.FindStringSubmatch(trimmed); match != nil {
		diagnostic := conciseDiagnostic{severity: "error", file: filepath.Base(match[1]), line: match[2], column: match[3], message: match[4]}
		s.addDiagnostic(diagnostic)
		return
	}
	if diagnostic, ok := parseConciseDiagnostic(line); ok {
		s.addDiagnostic(diagnostic)
	}
	if strings.Contains(trimmed, "** TEST SUCCEEDED **") || strings.Contains(trimmed, "** TEST FAILED **") ||
		(strings.HasPrefix(trimmed, "✔ Test run with ") || strings.HasPrefix(trimmed, "✘ Test run with ")) {
		s.outcome = trimmed
	}
}

func (s *outputSummary) testText(now time.Time, phase model.Phase) string {
	suites := append([]conciseTestSuite(nil), s.suites...)
	steps := append([]conciseStep(nil), s.steps...)
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].order < steps[j].order })
	diagnostics, intro, outcome := s.diagnostics, s.intro, s.outcome

	if !phase.Active() {
		for index := range suites {
			if suites[index].status != "running" && suites[index].status != "" {
				continue
			}
			suites[index].currentTest = ""
			switch {
			case suites[index].failed > 0:
				suites[index].status = "failed"
			case phase == model.PhaseSucceeded:
				suites[index].status = "passed"
			default:
				suites[index].status = "stopped"
			}
		}
	}

	result := make([]string, 0, 16)
	if intro != "" {
		result = append(result, intro, "")
	}
	if len(steps) > 0 {
		result = append(result, "BUILD PREPARATION")
		result = append(result, formatConciseSteps(steps, now)...)
		result = append(result, "")
	}
	visibleSuites := make([]conciseTestSuite, 0, len(suites))
	for _, suite := range suites {
		if !isAggregateTestSuite(suite.name) || suite.passed+suite.failed > 0 {
			visibleSuites = append(visibleSuites, suite)
		}
	}
	result = append(result, fmt.Sprintf("TEST SUITES (%d)", len(visibleSuites)))
	if len(visibleSuites) == 0 {
		result = append(result, "  ● Waiting for test suites...")
	} else {
		for _, suite := range visibleSuites {
			marker := "○"
			if suite.status == "passed" {
				marker = "✓"
			} else if suite.status == "failed" || suite.failed > 0 {
				marker = "✗"
			} else if suite.status == "running" || suite.status == "" {
				marker = "●"
			}
			details := suite.status
			if suite.passed+suite.failed > 0 {
				details = fmt.Sprintf("%d passed", suite.passed)
				if suite.failed > 0 {
					details += fmt.Sprintf(", %d failed", suite.failed)
				}
			} else if details == "" {
				details = "running"
			}
			if suite.duration > 0 {
				details += "  " + formatStepDuration(suite.duration)
			}
			result = append(result, fmt.Sprintf("  %s %-28s %s", marker, suite.name, details))
			if suite.currentTest != "" {
				result = append(result, "      ↳ "+suite.currentTest)
			}
		}
	}
	appendDiagnosticSummary(&result, diagnostics)
	if outcome != "" {
		result = append(result, "", outcome)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func formatConciseSteps(steps []conciseStep, now time.Time) []string {
	result := make([]string, 0, len(steps))
	for _, step := range steps {
		duration, marker := step.duration, "✓"
		if step.inProgress {
			marker = "●"
			duration += max(time.Duration(0), now.Sub(step.startedAt))
		}
		result = append(result, fmt.Sprintf("  %s %-22s %8s", marker, step.name, formatStepDuration(duration)))
	}
	return result
}

func appendDiagnosticSummary(result *[]string, diagnostics []conciseDiagnostic) {
	if len(diagnostics) == 0 {
		return
	}
	*result = append(*result, "", fmt.Sprintf("DIAGNOSTICS (%d)", len(diagnostics)))
	for _, diagnostic := range diagnostics {
		location := diagnostic.file
		if diagnostic.line != "" {
			location += ":" + diagnostic.line + ":" + diagnostic.column
		}
		if location == "" {
			location = "Test run"
		}
		*result = append(*result, fmt.Sprintf("  %s: %s — %s", diagnostic.severity, location, diagnostic.message))
	}
}

func testSuiteForCase(identifier string) string {
	if match := xctestCaseSuite.FindStringSubmatch(identifier); match != nil {
		return match[1]
	}
	return "Tests"
}

func testName(identifier string) string {
	if index := strings.LastIndex(identifier, " "); index >= 0 {
		return strings.TrimSuffix(identifier[index+1:], "]")
	}
	return identifier
}

func parseSeconds(value string) time.Duration {
	seconds, _ := strconv.ParseFloat(value, 64)
	return time.Duration(seconds * float64(time.Second))
}

func isAggregateTestSuite(name string) bool {
	lower := strings.ToLower(name)
	return lower == "all tests" || lower == "selected tests" || strings.HasSuffix(lower, ".xctest")
}

type progressEvent struct {
	name      string
	order     int
	startedAt time.Time
	duration  time.Duration
	done      bool
}

func parseProgressMarker(line string) (progressEvent, bool) {
	parts := strings.SplitN(line, " ", 5)
	if len(parts) != 5 || parts[0] != "[lazyxcode:step]" {
		return progressEvent{}, false
	}
	value, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return progressEvent{}, false
	}
	order, err := strconv.Atoi(parts[3])
	if err != nil {
		return progressEvent{}, false
	}
	event := progressEvent{name: parts[4], order: order}
	switch parts[1] {
	case "start":
		event.startedAt = time.UnixMilli(value)
	case "done":
		event.duration = time.Duration(value) * time.Millisecond
		event.done = true
	default:
		return progressEvent{}, false
	}
	return event, true
}

func parseConciseDiagnostic(line string) (conciseDiagnostic, bool) {
	trimmed := strings.TrimSpace(line)
	if match := sourceDiagnosticPattern.FindStringSubmatch(trimmed); match != nil {
		return conciseDiagnostic{
			severity: match[4], file: filepath.Base(match[1]), line: match[2], column: match[3], message: match[5],
		}, true
	}
	lowerTrimmed := strings.ToLower(trimmed)
	if strings.HasPrefix(lowerTrimmed, "ld:") {
		return conciseDiagnostic{severity: "error", file: "Linker", message: strings.TrimSpace(trimmed[len("ld:"):])}, true
	}
	for _, fragment := range []string{
		"undefined symbols for architecture",
		"duplicate symbol",
		"failed with a nonzero exit code",
		"the following build commands failed:",
	} {
		if strings.Contains(lowerTrimmed, fragment) {
			return conciseDiagnostic{severity: "error", message: trimmed}, true
		}
	}
	lower := strings.ToLower(line)
	for _, severity := range []string{"fatal error", "error", "warning"} {
		token := severity + ":"
		index := diagnosticToken(lower, token)
		if index < 0 {
			continue
		}
		message := strings.TrimSpace(line[index+len(token):])
		if message != "" {
			return conciseDiagnostic{severity: severity, message: message}, true
		}
	}
	return conciseDiagnostic{}, false
}

func formatStepDuration(duration time.Duration) string {
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
	if duration < time.Minute {
		return fmt.Sprintf("%.1fs", duration.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(duration.Minutes()), int(duration.Seconds())%60)
}

func formatBuildRow(record model.BuildRecord, width int) string {
	prefix := fmt.Sprintf("%-7s #%s ", recordPhaseLabel(record), shortID(record.ID))
	duration := formatDuration(record.Duration(time.Now()))
	deviceWidth := max(3, width-len(prefix)-len(duration)-1)
	return truncate(prefix+fmt.Sprintf("%-*s %s", deviceWidth, truncate(record.Simulator.Name, deviceWidth), duration), width)
}

func recordPhaseLabel(record model.BuildRecord) string {
	if record.OperationKind() == model.OperationDiscoverTests {
		if record.Phase == model.PhaseSucceeded {
			return "TESTS"
		}
		if record.Phase == model.PhaseTesting {
			return "LIST"
		}
	}
	if record.OperationKind() == model.OperationTest {
		switch record.Phase {
		case model.PhaseSucceeded:
			return "PASS"
		case model.PhaseTesting:
			return "TEST"
		case model.PhaseBooting:
			return "BOOT"
		case model.PhaseCancelled:
			return "STOP"
		case model.PhaseQueued:
			return "QUEUE"
		default:
			return "FAIL"
		}
	}
	return phaseLabel(record.Phase)
}

func writeViewLine(view *gocui.View, width int, line string) {
	fmt.Fprintln(view, truncate(line, max(0, width)))
}

func phaseLabel(phase model.Phase) string {
	switch phase {
	case model.PhaseQueued:
		return "QUEUE"
	case model.PhaseBuilding:
		return "BUILD"
	case model.PhaseTesting:
		return "TEST"
	case model.PhaseBooting:
		return "BOOT"
	case model.PhaseInstalling:
		return "INSTALL"
	case model.PhaseLaunching:
		return "LAUNCH"
	case model.PhaseRunning:
		return "RUN"
	case model.PhaseSucceeded:
		return "OK"
	case model.PhaseCancelled:
		return "STOP"
	default:
		return "FAIL"
	}
}

func shortID(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	if len(id) > 4 {
		return id[len(id)-4:]
	}
	return id
}

func formatDuration(duration time.Duration) string {
	seconds := int(duration.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%02ds", seconds)
	}
	return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(size)
	unit := "B"
	for _, candidate := range units {
		value /= 1024
		unit = candidate
		if value < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func setListCursor(view *gocui.View, index, total, itemOffset int) {
	_, height := view.InnerSize()
	if height < 1 {
		return
	}
	if total == 0 {
		view.SetOrigin(0, 0)
		view.SetCursor(0, 0)
		return
	}
	absoluteRow := itemOffset + clamp(index, 0, total-1)
	_, origin := view.Origin()
	if absoluteRow < origin {
		origin = absoluteRow
	} else if absoluteRow >= origin+height {
		origin = absoluteRow - height + 1
	}
	lastRow := itemOffset + total - 1
	origin = clamp(origin, 0, max(0, lastRow-height+1))
	view.SetOrigin(0, origin)
	view.SetCursor(0, absoluteRow-origin)
}

func (a *App) renderOverlay(filter, list *gocui.View) error {
	list.Clear()
	width, _ := list.InnerSize()
	itemOffset := 0
	if a.overlay.message != "" {
		fmt.Fprintln(list, a.overlay.message)
		itemOffset = strings.Count(a.overlay.message, "\n") + 1
		if !strings.HasSuffix(a.overlay.message, "\n") {
			itemOffset++
		}
	}
	items := a.filteredOverlayItems()
	if a.overlay.selected >= len(items) {
		a.overlay.selected = max(0, len(items)-1)
	}
	for _, item := range items {
		label := item.Label
		if a.overlay.kind == "simulator" {
			if index := simulatorIndex(a.sims, item.ID); index >= 0 {
				label = formatTargetRow(a.sims[index], width)
			}
		}
		fmt.Fprintln(list, truncate(label, width))
	}
	if a.overlay.kind == "help" {
		clampOverlayOrigin(a.overlay, list)
		list.SetCursor(0, 0)
	} else {
		setListCursor(list, a.overlay.selected, len(items), itemOffset)
	}
	if filter.Visible && filter.Buffer() == "" && a.overlay.filter != "" {
		filter.Clear()
		fmt.Fprint(filter, a.overlay.filter)
	}
	return nil
}

func formatTargetRow(target model.Simulator, width int) string {
	const osWidth, kindWidth, stateWidth = 7, 9, 9
	nameWidth := width - osWidth - kindWidth - stateWidth - 3
	if nameWidth < 8 {
		return truncate(strings.Join([]string{target.Name, target.OS, target.KindLabel(), target.State}, " "), width)
	}
	return fmt.Sprintf("%-*s %-*s %-*s %s",
		nameWidth, truncate(target.Name, nameWidth),
		osWidth, truncate(target.OS, osWidth),
		kindWidth, target.KindLabel(),
		truncate(target.State, stateWidth),
	)
}

func (a *App) filteredOverlayItems() []overlayItem {
	if a.overlay == nil || a.overlay.filter == "" {
		if a.overlay == nil {
			return nil
		}
		return a.overlay.items
	}
	needle := strings.ToLower(a.overlay.filter)
	var result []overlayItem
	for _, item := range a.overlay.items {
		if strings.Contains(strings.ToLower(item.Label), needle) {
			result = append(result, item)
		}
	}
	return result
}

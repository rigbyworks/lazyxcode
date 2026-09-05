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
	"github.com/mwahlig/lazy-xcode/internal/model"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

var diagnosticLocationPattern = regexp.MustCompile(`^(.+:\d+:\d+:)(.*)$`)

var sourceDiagnosticPattern = regexp.MustCompile(`^(.+):(\d+):(\d+):\s+(fatal error|error|warning):\s+(.+)$`)

var conciseDiagnosticPattern = regexp.MustCompile(`^(\s*)(fatal error|error|warning):\s+(.+?)\s+—\s+(.+)$`)

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
	button := "[b] Build & run   [t] Test"
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
			"  [b] Build  [t] Test  [c] Cache " + formatBytes(a.cacheSize),
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

	outputView.Clear()
	if len(a.records) == 0 {
		fmt.Fprintln(outputView, "Select a scheme and target, then press b to build.")
	} else {
		a.loadSelectedOutput()
		record := a.records[a.buildIndex]
		output := ansiPattern.ReplaceAllString(a.outputs[record.ID], "")
		if !a.verboseOutput {
			if record.OperationKind() == model.OperationTest {
				output = conciseTestOutputAt(output, time.Now())
			} else {
				output = conciseBuildOutputAt(output, time.Now())
			}
		}
		fmt.Fprint(outputView, formatBuildOutput(output))
		if record.Error != "" {
			if output != "" && !strings.HasSuffix(output, "\n") {
				fmt.Fprintln(outputView)
			}
			fmt.Fprintf(outputView, "\n%s[%s] %s%s\n", ansiBoldRed, phaseLabel(record.Phase), record.Error, ansiReset)
		}
		if record.Phase.Active() && a.outputFollow {
			scrollOutputToBottom(outputView)
		} else {
			clampOutputOrigin(outputView)
		}
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
		case line == "BUILD STEPS" || line == "BUILD PREPARATION" || strings.HasPrefix(line, "DIAGNOSTICS") || strings.HasPrefix(line, "TEST SUITES") || line == "DEPLOYMENT" || line == "APP CONSOLE":
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
		case strings.HasPrefix(strings.TrimSpace(line), "[lazy-xcode]"):
			lines[i] = colorPrefix(line, "[lazy-xcode]", ansiCyan)
			severity = diagnosticNone
		case strings.TrimSpace(line) == "":
			severity = diagnosticNone
		}
	}
	return strings.Join(lines, "\n")
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
	formatted := match[1] + color + match[2] + ":" + ansiReset + " " + ansiCyan + match[3] + ansiReset + " — " + match[4]
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

func conciseBuildOutputAt(raw string, now time.Time) string {
	lines := strings.Split(raw, "\n")
	diagnostics := make([]conciseDiagnostic, 0)
	diagnosticKeys := map[string]bool{}
	steps := make([]conciseStep, 0)
	stepIndexes := map[string]int{}
	deployment := make([]string, 0, 3)
	appConsole := make([]string, 0)
	result := make([]string, 0, 16)
	intro := ""
	buildOutcome := ""
	inAppConsole := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[lazy-xcode] App console" {
			inAppConsole = true
			continue
		}
		if inAppConsole {
			appConsole = append(appConsole, line)
			continue
		}
		if event, ok := parseProgressMarker(trimmed); ok {
			index, exists := stepIndexes[event.name]
			if !exists {
				index = len(steps)
				stepIndexes[event.name] = index
				steps = append(steps, conciseStep{name: event.name, order: event.order})
			}
			if event.done {
				steps[index].duration = event.duration
				steps[index].inProgress = false
			} else {
				steps[index].startedAt = event.startedAt
				steps[index].inProgress = true
			}
			continue
		}
		if diagnostic, ok := parseConciseDiagnostic(line); ok {
			key := diagnostic.severity + "\x00" + diagnostic.file + "\x00" + diagnostic.line + "\x00" + diagnostic.column + "\x00" + diagnostic.message
			if !diagnosticKeys[key] {
				diagnosticKeys[key] = true
				diagnostics = append(diagnostics, diagnostic)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[lazy-xcode]") {
			if strings.Contains(trimmed, " Building ") {
				intro = trimmed
			} else if strings.Contains(trimmed, "Build succeeded;") || strings.Contains(trimmed, "Installing app") || strings.Contains(trimmed, "Launching ") {
				deployment = append(deployment, trimmed)
			}
			continue
		}
		if strings.Contains(trimmed, "** BUILD SUCCEEDED **") || strings.Contains(trimmed, "** BUILD FAILED **") {
			buildOutcome = trimmed
		}
	}

	if intro != "" {
		result = append(result, intro, "")
	}
	if len(steps) > 0 {
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

	if len(diagnostics) > 0 {
		if len(result) > 0 {
			result = append(result, "")
		}
		result = append(result, fmt.Sprintf("DIAGNOSTICS (%d)", len(diagnostics)))
		for _, diagnostic := range diagnostics {
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

	if len(deployment) > 0 {
		if len(result) > 0 {
			result = append(result, "")
		}
		result = append(result, "DEPLOYMENT")
		for _, line := range deployment {
			result = append(result, "  "+line)
		}
	}
	if buildOutcome != "" {
		if len(result) > 0 {
			result = append(result, "")
		}
		result = append(result, buildOutcome)
	}
	if inAppConsole {
		for len(appConsole) > 0 && appConsole[len(appConsole)-1] == "" {
			appConsole = appConsole[:len(appConsole)-1]
		}
		if len(result) > 0 {
			result = append(result, "")
		}
		result = append(result, "APP CONSOLE")
		if len(appConsole) == 0 {
			result = append(result, "  Waiting for app output...")
		} else {
			result = append(result, appConsole...)
		}
	}
	if len(result) == 0 {
		return "Waiting for build activity..."
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

func conciseTestOutputAt(raw string, now time.Time) string {
	lines := strings.Split(raw, "\n")
	steps := collectConciseSteps(lines)
	suites := make([]conciseTestSuite, 0)
	suiteIndexes := map[string]int{}
	diagnostics := make([]conciseDiagnostic, 0)
	diagnosticKeys := map[string]bool{}
	intro, outcome := "", ""

	ensureSuite := func(name string) int {
		name = strings.TrimSpace(name)
		if index, ok := suiteIndexes[name]; ok {
			return index
		}
		index := len(suites)
		suiteIndexes[name] = index
		suites = append(suites, conciseTestSuite{name: name})
		return index
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[lazy-xcode]") && strings.Contains(trimmed, " Testing ") {
			intro = trimmed
			continue
		}
		if match := xctestSuiteStart.FindStringSubmatch(trimmed); match != nil {
			index := ensureSuite(match[1])
			suites[index].status = "running"
			continue
		}
		if match := xctestSuiteFinish.FindStringSubmatch(trimmed); match != nil {
			index := ensureSuite(match[1])
			suites[index].status = match[2]
			continue
		}
		if match := xctestCaseStart.FindStringSubmatch(trimmed); match != nil {
			name := testSuiteForCase(match[1])
			index := ensureSuite(name)
			suites[index].status, suites[index].currentTest = "running", testName(match[1])
			continue
		}
		if match := xctestCaseFinish.FindStringSubmatch(trimmed); match != nil {
			index := ensureSuite(testSuiteForCase(match[1]))
			if match[2] == "passed" {
				suites[index].passed++
			} else {
				suites[index].failed++
			}
			suites[index].duration += parseSeconds(match[3])
			suites[index].currentTest = ""
			continue
		}
		if match := swiftSuiteStart.FindStringSubmatch(trimmed); match != nil {
			index := ensureSuite(match[1])
			suites[index].status = "running"
			continue
		}
		if match := swiftSuiteFinish.FindStringSubmatch(trimmed); match != nil {
			index := ensureSuite(match[1])
			suites[index].status = match[2]
			suites[index].duration = parseSeconds(match[3])
			continue
		}
		if match := swiftTestIssue.FindStringSubmatch(trimmed); match != nil {
			diagnostic := conciseDiagnostic{severity: "error", file: filepath.Base(match[1]), line: match[2], column: match[3], message: match[4]}
			key := diagnostic.severity + "\x00" + diagnostic.file + "\x00" + diagnostic.line + "\x00" + diagnostic.column + "\x00" + diagnostic.message
			if !diagnosticKeys[key] {
				diagnosticKeys[key] = true
				diagnostics = append(diagnostics, diagnostic)
			}
			continue
		}
		if diagnostic, ok := parseConciseDiagnostic(line); ok {
			key := diagnostic.severity + "\x00" + diagnostic.file + "\x00" + diagnostic.line + "\x00" + diagnostic.column + "\x00" + diagnostic.message
			if !diagnosticKeys[key] {
				diagnosticKeys[key] = true
				diagnostics = append(diagnostics, diagnostic)
			}
		}
		if strings.Contains(trimmed, "** TEST SUCCEEDED **") || strings.Contains(trimmed, "** TEST FAILED **") ||
			(strings.HasPrefix(trimmed, "✔ Test run with ") || strings.HasPrefix(trimmed, "✘ Test run with ")) {
			outcome = trimmed
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
			marker := "✓"
			if suite.status == "running" || suite.status == "" {
				marker = "●"
			} else if suite.status == "failed" || suite.failed > 0 {
				marker = "✗"
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

func collectConciseSteps(lines []string) []conciseStep {
	steps := make([]conciseStep, 0)
	indexes := map[string]int{}
	for _, line := range lines {
		event, ok := parseProgressMarker(strings.TrimSpace(line))
		if !ok {
			continue
		}
		index, exists := indexes[event.name]
		if !exists {
			index = len(steps)
			indexes[event.name] = index
			steps = append(steps, conciseStep{name: event.name, order: event.order})
		}
		if event.done {
			steps[index].duration, steps[index].inProgress = event.duration, false
		} else {
			steps[index].startedAt, steps[index].inProgress = event.startedAt, true
		}
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].order < steps[j].order })
	return steps
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
	if len(parts) != 5 || parts[0] != "[lazy-xcode:step]" {
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
	setListCursor(list, a.overlay.selected, len(items), itemOffset)
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

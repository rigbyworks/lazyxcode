package tui

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rigbyworks/lazyxcode/internal/model"
)

// Disk retains the complete transcript. These limits apply only to the live UI.
const (
	maxLogBytes           = 256 << 10
	maxLogLines           = 2000
	maxLogLineBytes       = 4096
	maxSummaryEntries     = 256
	outputRefreshInterval = 50 * time.Millisecond
)

const omittedOutput = "[Earlier output omitted from this window; the complete transcript is in the log file.]\n"

// textWindow is a byte ring with a second limit on line count. Appending never
// copies the existing transcript or retains the caller's backing storage.
type textWindow struct {
	data               []byte
	start, size, lines int
	truncated          bool
}

func (w *textWindow) append(text string) {
	if text == "" {
		return
	}
	if w.data == nil {
		w.data = make([]byte, maxLogBytes)
	}
	if len(text) >= len(w.data) {
		w.start, w.size, w.lines = 0, 0, 0
		w.truncated = true
		text = text[len(text)-len(w.data):]
	}
	for w.size+len(text) > len(w.data) {
		w.dropByte()
	}
	end := (w.start + w.size) % len(w.data)
	n := copy(w.data[end:], text)
	copy(w.data, text[n:])
	w.size += len(text)
	w.lines += strings.Count(text, "\n")
	for w.lines > maxLogLines {
		w.dropByte()
	}
}

func (w *textWindow) dropByte() {
	if w.data[w.start] == '\n' {
		w.lines--
	}
	w.start = (w.start + 1) % len(w.data)
	w.size--
	w.truncated = true
}

func (w *textWindow) text() string {
	if w.size == 0 {
		return ""
	}
	data := make([]byte, w.size)
	n := copy(data, w.data[w.start:min(len(w.data), w.start+w.size)])
	copy(data[n:], w.data[:w.size-n])
	// A byte limit can split a UTF-8 rune. Do not expose a broken leading rune.
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	return string(data)
}

type outputSummary struct {
	intro, buildOutcome, outcome string
	deployment                   []string
	steps                        []conciseStep
	stepIndexes                  map[string]int
	diagnostics                  []conciseDiagnostic
	diagnosticKeys               map[string]bool
	suites                       []conciseTestSuite
	suiteIndexes                 map[string]int
	truncated                    bool
}

func (s *outputSummary) observeStep(line string) bool {
	event, ok := parseProgressMarker(line)
	if !ok {
		return false
	}
	if s.stepIndexes == nil {
		s.stepIndexes = map[string]int{}
	}
	index, exists := s.stepIndexes[event.name]
	if !exists {
		if len(s.steps) >= maxSummaryEntries {
			s.truncated = true
			return true
		}
		index = len(s.steps)
		s.stepIndexes[event.name] = index
		s.steps = append(s.steps, conciseStep{name: event.name, order: event.order})
	}
	if event.done {
		s.steps[index].duration, s.steps[index].inProgress = event.duration, false
	} else {
		s.steps[index].startedAt, s.steps[index].inProgress = event.startedAt, true
	}
	return true
}

func (s *outputSummary) addDiagnostic(d conciseDiagnostic) {
	if s.diagnosticKeys == nil {
		s.diagnosticKeys = map[string]bool{}
	}
	key := d.severity + "\x00" + d.file + "\x00" + d.line + "\x00" + d.column + "\x00" + d.message
	if s.diagnosticKeys[key] {
		return
	}
	if len(s.diagnostics) >= maxSummaryEntries {
		s.truncated = true
		return
	}
	s.diagnosticKeys[key] = true
	s.diagnostics = append(s.diagnostics, d)
}

// activityLog is shared by capture and the UI. Parsing happens once per complete
// line; snapshots and formatting are cached until content or a live timer changes.
type activityLog struct {
	mu            sync.Mutex
	operation     model.Operation
	raw, console  textWindow
	summary       outputSummary
	partial       string
	longLine      bool
	inConsole     bool
	revision      uint64
	cacheRevision uint64
	cacheSecond   int64
	cachePhase    model.Phase
	cacheVerbose  bool
	cached        string
}

func newActivityLog(operation model.Operation) *activityLog {
	return &activityLog{operation: operation, summary: outputSummary{suiteIndexes: map[string]int{}}}
}

func (l *activityLog) append(text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.raw.append(text)
	l.revision++
	for text != "" {
		end := strings.IndexByte(text, '\n')
		fragment := text
		if end >= 0 {
			fragment = text[:end]
		}
		if len(fragment)+len(l.partial) > maxLogLineBytes {
			l.longLine = true
			if len(fragment) >= maxLogLineBytes {
				l.partial = ""
				fragment = fragment[len(fragment)-maxLogLineBytes:]
			} else {
				l.partial = l.partial[len(l.partial)+len(fragment)-maxLogLineBytes:]
			}
		}
		if l.partial == "" {
			l.partial = strings.Clone(fragment)
		} else {
			l.partial += fragment
		}
		if end < 0 {
			break
		}
		l.observeLine(l.partial, true)
		l.partial, l.longLine = "", false
		text = text[end+1:]
	}
}

func (l *activityLog) observeLine(line string, newline bool) {
	line = ansiPattern.ReplaceAllString(strings.Clone(line), "")
	if l.inConsole {
		if l.longLine {
			l.console.truncated = true
		}
		if newline {
			line += "\n"
		}
		l.console.append(line)
		return
	}
	if l.longLine {
		l.summary.truncated = true
		return
	}
	if strings.TrimSpace(line) == "[lazyxcode] App console" {
		l.inConsole = true
	} else if l.operation == model.OperationTest {
		l.summary.observeTest(line)
	} else {
		l.summary.observeBuild(line)
	}
}

func (l *activityLog) finish() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.partial != "" {
		l.observeLine(l.partial, false)
		l.partial, l.longLine = "", false
		l.revision++
	}
}

func (l *activityLog) Write(data []byte) (int, error) { l.append(string(data)); return len(data), nil }

func (l *activityLog) text(verbose bool, now time.Time, phase model.Phase) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.textLocked(verbose, now, phase)
}

func (l *activityLog) textLocked(verbose bool, now time.Time, phase model.Phase) string {
	if verbose {
		text := ansiPattern.ReplaceAllString(l.raw.text(), "")
		lines := strings.Split(text, "\n")
		shortened := false
		for i, line := range lines {
			if len(line) > maxLogLineBytes {
				line = line[len(line)-maxLogLineBytes:]
				for len(line) > 0 && !utf8.RuneStart(line[0]) {
					line = line[1:]
				}
				lines[i] = "[long line truncated] " + line
				shortened = true
			}
		}
		text = strings.Join(lines, "\n")
		if l.raw.truncated || shortened {
			text = omittedOutput + text
		}
		return text
	}
	var text string
	if l.operation == model.OperationTest {
		text = l.summary.testText(now, phase)
	} else {
		text = l.summary.buildText(now)
	}
	if l.summary.truncated {
		text += "\n[Summary limit reached; see the complete log for remaining details.]"
	}
	if l.inConsole {
		partial := l.partial
		for len(partial) > 0 && !utf8.RuneStart(partial[0]) {
			partial = partial[1:]
		}
		console := l.console.text() + ansiPattern.ReplaceAllString(partial, "")
		console = strings.TrimRight(console, "\n")
		if l.console.truncated || l.longLine {
			console = omittedOutput + console
		}
		if console == "" {
			console = "  Waiting for app output..."
		}
		if text != "" {
			text += "\n\n"
		}
		text += "APP CONSOLE\n" + console
	}
	if text == "" {
		return "Waiting for build activity..."
	}
	return text
}

func (l *activityLog) formatted(verbose bool, now time.Time, phase model.Phase) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	second := int64(0)
	if !verbose && phase.Active() && !l.inConsole {
		for _, step := range l.summary.steps {
			if step.inProgress {
				second = now.Unix()
				break
			}
		}
	}
	if l.cached != "" && l.cacheRevision == l.revision && l.cacheVerbose == verbose && l.cachePhase == phase && l.cacheSecond == second {
		return l.cached
	}
	l.cached = formatBuildOutput(l.textLocked(verbose, now, phase))
	l.cacheRevision, l.cacheVerbose, l.cachePhase, l.cacheSecond = l.revision, verbose, phase, second
	return l.cached
}

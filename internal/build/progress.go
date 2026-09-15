package build

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"
)

const progressMarker = "[lazyxcode:step]"

type buildStepDefinition struct {
	name     string
	prefixes []string
}

var buildStepDefinitions = []buildStepDefinition{
	{name: "Resolve packages", prefixes: []string{"Resolve Package Graph", "Prepare packages", "ComputePackagePrebuildTargetDependencyGraph"}},
	{name: "Plan build", prefixes: []string{"ComputeTargetDependencyGraph"}},
	{name: "Prepare build", prefixes: []string{"CreateBuildDescription", "CreateBuildDirectory"}},
	{name: "Compile resources", prefixes: []string{"CompileAssetCatalog", "CompileStoryboard", "CompileXIB", "CompileMetalFile", "ProcessXCFramework"}},
	{name: "Compile sources", prefixes: []string{"SwiftDriver", "SwiftCompile", "SwiftEmitModule", "CompileSwift", "CompileC ", "CompileCPlusPlus", "CompilePrecompiledHeader"}},
	{name: "Link", prefixes: []string{"Ld ", "Libtool ", "CreateUniversalBinary"}},
	{name: "Package", prefixes: []string{"CopySwiftLibs", "ExtractAppIntentsMetadata", "GenerateDSYMFile", "Touch "}},
	{name: "Sign", prefixes: []string{"CodeSign "}},
	{name: "Validate", prefixes: []string{"Validate ", "RegisterExecutionPolicyException", "RegisterWithLaunchServices"}},
}

type progressWriter struct {
	destination io.Writer
	now         func() time.Time
	buffer      []byte
	step        int
	startedAt   time.Time
	durations   []time.Duration
	finished    bool
}

func newProgressWriter(destination io.Writer, now func() time.Time) *progressWriter {
	return &progressWriter{destination: destination, now: now, step: -1, durations: make([]time.Duration, len(buildStepDefinitions))}
}

func (w *progressWriter) Write(data []byte) (int, error) {
	w.buffer = append(w.buffer, data...)
	for {
		newline := bytes.IndexByte(w.buffer, '\n')
		if newline < 0 {
			return len(data), nil
		}
		line := string(w.buffer[:newline])
		lineLength := newline + 1
		n, err := w.destination.Write(w.buffer[:lineLength])
		w.buffer = w.buffer[n:]
		if err != nil {
			return len(data), err
		}
		if n != lineLength {
			return len(data), io.ErrShortWrite
		}
		w.observe(line)
	}
}

func (w *progressWriter) Finish() {
	if w.finished {
		return
	}
	w.finished = true
	if len(w.buffer) > 0 {
		line := string(w.buffer)
		_, _ = w.destination.Write(w.buffer)
		newStep := buildStep(line)
		if (newStep >= 0 && newStep != w.step) || (w.step >= 0 && !w.startedAt.IsZero()) {
			_, _ = io.WriteString(w.destination, "\n")
		}
		if newStep >= 0 && newStep != w.step {
			w.observe(line)
		}
		w.buffer = nil
	}
	w.finishStep(w.now())
}

func (w *progressWriter) observe(line string) {
	if isTestExecutionStart(line) {
		w.finishStep(w.now())
		w.step = -1
		return
	}
	step := buildStep(line)
	if step < 0 || step == w.step {
		return
	}
	now := w.now()
	w.finishStep(now)
	w.step = step
	w.startedAt = now
	_, _ = fmt.Fprintf(w.destination, "%s start %d %d %s\n", progressMarker, now.UnixMilli(), step, buildStepDefinitions[step].name)
}

func isTestExecutionStart(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "Test Suite '") ||
		strings.HasPrefix(trimmed, "◇ Test run started") ||
		strings.HasPrefix(trimmed, "◆ Test run started")
}

func (w *progressWriter) finishStep(now time.Time) {
	if w.step < 0 || w.startedAt.IsZero() {
		return
	}
	w.durations[w.step] += max(time.Duration(0), now.Sub(w.startedAt))
	_, _ = fmt.Fprintf(w.destination, "%s done %d %d %s\n", progressMarker, w.durations[w.step].Milliseconds(), w.step, buildStepDefinitions[w.step].name)
	w.startedAt = time.Time{}
}

func buildStep(line string) int {
	trimmed := strings.TrimSpace(line)
	for index, definition := range buildStepDefinitions {
		for _, prefix := range definition.prefixes {
			if strings.HasPrefix(trimmed, prefix) {
				return index
			}
		}
	}
	return -1
}

package tui

import (
	"fmt"
	"strings"

	"github.com/jesseduffield/gocui"
)

type outputSnapshot struct {
	id      string
	verbose bool
	text    string
}

func (a *App) selectedOutputSnapshot() *outputSnapshot {
	if a.outputFollow || a.pausedOutput == nil || len(a.records) == 0 || a.buildIndex >= len(a.records) {
		return nil
	}
	if a.pausedOutput.id != a.records[a.buildIndex].ID || a.pausedOutput.verbose != a.verboseOutput {
		return nil
	}
	return a.pausedOutput
}

func (a *App) pauseOutput(view *gocui.View) {
	if a.mode == modeLocal && a.outputFollow && a.selectedTestOutput() == nil && a.selectedOutputPage() == nil && len(a.records) > 0 && a.outputFullText != "" {
		a.pausedOutput = &outputSnapshot{id: a.records[a.buildIndex].ID, verbose: a.verboseOutput, text: a.outputFullText}
		// Expand the bounded window only when the user starts scrolling. Preserve
		// the bottom anchor before applying their scroll delta.
		view.Clear()
		fmt.Fprint(view, a.outputFullText)
		a.renderedOutput = a.outputFullText
		scrollOutputToBottom(view)
	}
	a.outputFollow = false
}

// At least height logical lines fill height screen rows, even when wrapped.
// Keeping only these lines in follow mode avoids rebuilding thousands of cells
// that gocui would immediately scroll past.
func lastOutputLines(text string, lines int) string {
	end := len(text)
	if end > 0 && text[end-1] == '\n' {
		end--
	}
	for range lines {
		index := strings.LastIndexByte(text[:end], '\n')
		if index < 0 {
			return text
		}
		end = index
	}
	return text[end+1:]
}

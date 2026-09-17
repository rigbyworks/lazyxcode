package tui

import (
	"bytes"
	"fmt"

	"github.com/jesseduffield/gocui"
)

type outputPage struct {
	id         string
	start, end int64
	newerEnds  []int64
	log        *activityLog
}

func (a *App) selectedOutputPage() *outputPage {
	if a.mode != modeLocal || !a.verboseOutput || len(a.records) == 0 || a.buildIndex >= len(a.records) || a.outputPage == nil || a.outputPage.id != a.records[a.buildIndex].ID {
		return nil
	}
	return a.outputPage
}

func (a *App) olderOutputPage(g *gocui.Gui, _ *gocui.View) error {
	end := int64(-1)
	var newer []int64
	if page := a.selectedOutputPage(); page != nil {
		if page.start == 0 {
			a.status = "Beginning of log"
			return nil
		}
		end = page.start
		newer = append(append([]int64(nil), page.newerEnds...), page.end)
	}
	return a.readOutputPage(g, end, newer)
}

func (a *App) newerOutputPage(g *gocui.Gui, _ *gocui.View) error {
	page := a.selectedOutputPage()
	if page == nil {
		return nil
	}
	if len(page.newerEnds) == 0 {
		a.outputPage = nil
		a.outputFollow = true
		a.status = "Following latest output"
		return nil
	}
	last := len(page.newerEnds) - 1
	return a.readOutputPage(g, page.newerEnds[last], page.newerEnds[:last])
}

func (a *App) readOutputPage(g *gocui.Gui, end int64, newer []int64) error {
	if a.project == nil || len(a.records) == 0 || a.buildIndex >= len(a.records) {
		return nil
	}
	record := a.records[a.buildIndex]
	data, start, end, err := a.project.ReadLogPage(record, end, maxLogBytes)
	if err != nil {
		a.status = fmt.Sprintf("Read log page: %v", err)
		return nil
	}
	// Leave complete lines at page boundaries where possible. Offsets include
	// every discarded byte in the preceding page, so paging never skips output.
	if start > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 && newline < len(data)-1 {
			start += int64(newline + 1)
			data = data[newline+1:]
		}
	}
	for lines := bytes.Count(data, []byte{'\n'}); lines > maxLogLines; lines-- {
		n := bytes.IndexByte(data, '\n') + 1
		start += int64(n)
		data = data[n:]
	}
	log := newActivityLog(record.OperationKind())
	log.append(string(data))
	a.outputPage = &outputPage{id: record.ID, start: start, end: end, newerEnds: newer, log: log}
	a.pausedOutput = nil
	a.verboseOutput, a.outputFollow = true, false
	a.testOutput = nil
	a.status = "Log page: [ older, ] newer, G latest"
	if g != nil {
		if view, err := g.View("output"); err == nil {
			view.SetOrigin(0, 0)
		}
	}
	return nil
}

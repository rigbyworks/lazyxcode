package xcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TestCase keeps Apple's result lookup ID separate from xcodebuild's filter.
// Result identifiers may omit the target, while identifier URLs disambiguate it.
type TestCase struct {
	ID         string
	Identifier string
	Name       string
	Result     string
	Duration   string
}

type resultNode struct {
	Identifier string       `json:"nodeIdentifier"`
	URL        string       `json:"nodeIdentifierURL"`
	Type       string       `json:"nodeType"`
	Name       string       `json:"name"`
	Details    string       `json:"details"`
	Result     string       `json:"result"`
	Duration   string       `json:"duration"`
	Children   []resultNode `json:"children"`
	Source     struct {
		Path string `json:"filePath"`
		Line int    `json:"lineNumber"`
	} `json:"sourceLocation"`
}

func (c *Client) TestResults(ctx context.Context, path string) ([]TestCase, error) {
	data, err := c.runner.Output(ctx, "xcrun", "xcresulttool", "get", "test-results", "tests", "--path", path, "--compact")
	if err != nil {
		return nil, commandError("read test results (requires Xcode 16.3 or newer)", data, err)
	}
	var report struct {
		Nodes []resultNode `json:"testNodes"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse test results: %w", err)
	}
	if report.Nodes == nil {
		return nil, errors.New("result bundle contains no test tree")
	}
	var tests []TestCase
	var walk func([]resultNode, string)
	walk = func(nodes []resultNode, target string) {
		for _, node := range nodes {
			bundle := target
			if node.Type == "Unit test bundle" || node.Type == "UI test bundle" {
				bundle = strings.TrimSuffix(node.Name, ".xctest")
			}
			if node.Type == "Test Case" {
				identifier := node.Identifier
				if identifier != "" && bundle != "" && !strings.HasPrefix(identifier, bundle+"/") {
					identifier = bundle + "/" + identifier
				}
				id := node.URL
				if id == "" {
					id = node.Identifier
				}
				if id != "" {
					name := identifier
					if name == "" {
						name = node.Name
					}
					tests = append(tests, TestCase{ID: id, Identifier: identifier, Name: name, Result: node.Result, Duration: node.Duration})
				}
				// Repetitions and argument runs belong to this test, not separate filters.
				continue
			}
			walk(node.Children, bundle)
		}
	}
	walk(report.Nodes, "")
	sort.SliceStable(tests, func(i, j int) bool { return tests[i].Name < tests[j].Name })
	return tests, nil
}

func (c *Client) TestDetails(ctx context.Context, path, id string) (string, error) {
	data, err := c.runner.Output(ctx, "xcrun", "xcresulttool", "get", "test-results", "test-details", "--path", path, "--test-id", id, "--compact")
	if err != nil {
		return "", commandError("read test details", data, err)
	}
	var detail struct {
		Name        string       `json:"testName"`
		Description string       `json:"testDescription"`
		Result      string       `json:"testResult"`
		Duration    string       `json:"duration"`
		Runs        []resultNode `json:"testRuns"`
	}
	if err := json.Unmarshal(data, &detail); err != nil {
		return "", fmt.Errorf("parse test details: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s  %s\n\n", detail.Name, detail.Result, detail.Duration)
	if detail.Description != "" {
		fmt.Fprintln(&b, detail.Description)
	}
	var walk func([]resultNode, int)
	walk = func(nodes []resultNode, depth int) {
		for _, n := range nodes {
			fmt.Fprintf(&b, "%s%s", strings.Repeat("  ", min(depth, 12)), n.Name)
			if n.Result != "" {
				fmt.Fprintf(&b, "  [%s]", n.Result)
			}
			if n.Details != "" && n.Details != n.Name {
				fmt.Fprintf(&b, "  %s", n.Details)
			}
			if n.Source.Path != "" {
				fmt.Fprintf(&b, "  %s:%d", n.Source.Path, n.Source.Line)
			}
			fmt.Fprintln(&b)
			walk(n.Children, depth+1)
		}
	}
	walk(detail.Runs, 0)
	return b.String(), nil
}

func (c *Client) TestActivities(ctx context.Context, path, id string) (string, error) {
	data, err := c.runner.Output(ctx, "xcrun", "xcresulttool", "get", "test-results", "activities", "--path", path, "--test-id", id, "--compact")
	if err != nil {
		return "", commandError("read test activities", data, err)
	}
	type activity struct {
		Title       string `json:"title"`
		Failed      bool   `json:"isAssociatedWithFailure"`
		Attachments []struct {
			Name string `json:"name"`
		} `json:"attachments"`
		Children []json.RawMessage `json:"childActivities"`
	}
	type run struct {
		Device struct {
			Name string `json:"deviceName"`
		} `json:"device"`
		Config struct {
			Name string `json:"configurationName"`
		} `json:"testPlanConfiguration"`
		Activities []json.RawMessage `json:"activities"`
	}
	var report struct {
		Runs json.RawMessage `json:"testRuns"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return "", err
	}
	var runs []run
	if err := json.Unmarshal(report.Runs, &runs); err != nil {
		var single run
		if err := json.Unmarshal(report.Runs, &single); err != nil {
			return "", fmt.Errorf("parse test activities: %w", err)
		}
		runs = []run{single}
	}
	var b strings.Builder
	var walk func([]json.RawMessage, int) error
	walk = func(nodes []json.RawMessage, depth int) error {
		for _, raw := range nodes {
			var n activity
			if err := json.Unmarshal(raw, &n); err != nil {
				return err
			}
			indent := strings.Repeat("  ", min(depth, 12))
			marker := ""
			if n.Failed {
				marker = " [failure]"
			}
			fmt.Fprintf(&b, "%s%s%s\n", indent, n.Title, marker)
			for _, attachment := range n.Attachments {
				fmt.Fprintf(&b, "%s  Attachment: %s\n", indent, attachment.Name)
			}
			if err := walk(n.Children, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range runs {
		fmt.Fprintf(&b, "%s  %s\n\n", r.Device.Name, r.Config.Name)
		if err := walk(r.Activities, 0); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "No activities recorded for this test.\n", nil
	}
	return b.String(), nil
}

func (c *Client) ExportTestAttachments(ctx context.Context, bundle, id string) (string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(bundle), "attachments-")
	if err != nil {
		return "", err
	}
	data, err := c.runner.Output(ctx, "xcrun", "xcresulttool", "export", "attachments", "--path", bundle, "--test-id", id, "--output-path", dir)
	if err != nil {
		os.RemoveAll(dir)
		return "", commandError("export test attachments", data, err)
	}
	return dir, nil
}

// Coverage is the xccov report hierarchy. Ratios range from zero to one.
type Coverage struct {
	LineCoverage    float64          `json:"lineCoverage"`
	CoveredLines    int              `json:"coveredLines"`
	ExecutableLines int              `json:"executableLines"`
	Targets         []CoverageTarget `json:"targets"`
}
type CoverageTarget struct {
	Name            string         `json:"name"`
	LineCoverage    float64        `json:"lineCoverage"`
	CoveredLines    int            `json:"coveredLines"`
	ExecutableLines int            `json:"executableLines"`
	Files           []CoverageFile `json:"files"`
}
type CoverageFile struct {
	Name            string             `json:"name"`
	Path            string             `json:"path"`
	LineCoverage    float64            `json:"lineCoverage"`
	CoveredLines    int                `json:"coveredLines"`
	ExecutableLines int                `json:"executableLines"`
	Functions       []CoverageFunction `json:"functions"`
}
type CoverageFunction struct {
	Name            string  `json:"name"`
	LineNumber      int     `json:"lineNumber"`
	LineCoverage    float64 `json:"lineCoverage"`
	CoveredLines    int     `json:"coveredLines"`
	ExecutableLines int     `json:"executableLines"`
}

func (c *Client) Coverage(ctx context.Context, bundle string) (Coverage, error) {
	data, err := c.runner.Output(ctx, "xcrun", "xccov", "view", "--report", "--json", bundle)
	if err != nil {
		return Coverage{}, commandError("read coverage (the run must have code coverage enabled)", data, err)
	}
	var report Coverage
	if err := json.Unmarshal(data, &report); err != nil {
		return report, fmt.Errorf("parse coverage: %w", err)
	}
	if report.Targets == nil {
		return report, errors.New("no coverage report is available for this run")
	}
	sort.SliceStable(report.Targets, func(i, j int) bool { return report.Targets[i].Name < report.Targets[j].Name })
	for i := range report.Targets {
		sort.SliceStable(report.Targets[i].Files, func(a, b int) bool { return report.Targets[i].Files[a].Path < report.Targets[i].Files[b].Path })
	}
	return report, nil
}

func (c *Client) CompareCoverage(ctx context.Context, before, after string) (string, error) {
	data, err := c.runner.Output(ctx, "xcrun", "xccov", "diff", "--json", before, after)
	if err != nil {
		return "", commandError("compare coverage", data, err)
	}
	return formatCoverageDifference(data)

}

// ReadEnumeratedTests reads xcodebuild's flat JSON output, not its build log.
func ReadEnumeratedTests(path string) ([]TestCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read discovered tests: %w", err)
	}
	var report struct {
		Errors []json.RawMessage `json:"errors"`
		Values []struct {
			Enabled []struct {
				Identifier string `json:"identifier"`
			} `json:"enabledTests"`
		} `json:"values"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse discovered tests: %w", err)
	}
	if len(report.Errors) > 0 {
		return nil, fmt.Errorf("test discovery reported errors: %s", report.Errors)
	}
	if report.Values == nil {
		return nil, errors.New("test discovery returned no test plans")
	}
	var tests []TestCase
	seen := map[string]bool{}
	for _, value := range report.Values {
		for _, test := range value.Enabled {
			if test.Identifier != "" && !seen[test.Identifier] {
				seen[test.Identifier] = true
				tests = append(tests, TestCase{ID: test.Identifier, Identifier: test.Identifier, Name: test.Identifier})
			}
		}
	}
	sort.Slice(tests, func(i, j int) bool { return tests[i].Name < tests[j].Name })
	return tests, nil
}

type coverageDelta struct {
	Ratio      float64 `json:"lineCoverageDelta"`
	Covered    int     `json:"coveredLinesDelta"`
	Executable int     `json:"executableLinesDelta"`
}
type coverageChange struct {
	Name             string            `json:"name"`
	Path             string            `json:"documentLocation"`
	Delta            coverageDelta     `json:"lineCoverageDelta"`
	Targets          []coverageChange  `json:"targetDeltas"`
	Files            []coverageChange  `json:"fileDeltas"`
	Functions        []coverageChange  `json:"functionDeltas"`
	AddedTargets     []json.RawMessage `json:"addedTargets"`
	RemovedTargets   []json.RawMessage `json:"removedTargets"`
	AddedFiles       []json.RawMessage `json:"addedFiles"`
	RemovedFiles     []json.RawMessage `json:"removedFiles"`
	AddedFunctions   []json.RawMessage `json:"addedFunctions"`
	RemovedFunctions []json.RawMessage `json:"removedFunctions"`
}

func formatCoverageDifference(data []byte) (string, error) {
	var report coverageChange
	if err := json.Unmarshal(data, &report); err != nil {
		return "", fmt.Errorf("parse coverage comparison: %w", err)
	}
	var b strings.Builder
	var walk func(coverageChange, int) error
	walk = func(change coverageChange, depth int) error {
		label := change.Name
		if change.Path != "" {
			label = change.Path
		}
		if depth == 0 {
			label = "Overall"
		}
		indent := strings.Repeat("  ", depth)
		fmt.Fprintf(&b, "%s%+7.2f pp  %s  (%+d covered, %+d executable lines)\n", indent, change.Delta.Ratio*100, label, change.Delta.Covered, change.Delta.Executable)
		for _, group := range []struct {
			label  string
			values []json.RawMessage
		}{
			{"Added target", change.AddedTargets}, {"Removed target", change.RemovedTargets},
			{"Added file", change.AddedFiles}, {"Removed file", change.RemovedFiles},
			{"Added function", change.AddedFunctions}, {"Removed function", change.RemovedFunctions},
		} {
			for _, raw := range group.values {
				var name string
				if json.Unmarshal(raw, &name) != nil {
					var entry struct {
						Name     string `json:"name"`
						Path     string `json:"path"`
						Location string `json:"documentLocation"`
					}
					if err := json.Unmarshal(raw, &entry); err != nil {
						return err
					}
					name = entry.Name
					if entry.Path != "" {
						name = entry.Path
					}
					if entry.Location != "" {
						name = entry.Location
					}
				}
				fmt.Fprintf(&b, "%s  %s: %s\n", indent, group.label, name)
			}
		}
		for _, children := range [][]coverageChange{change.Targets, change.Files, change.Functions} {
			for _, child := range children {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(report, 0); err != nil {
		return "", fmt.Errorf("parse changed coverage items: %w", err)
	}
	return b.String(), nil
}

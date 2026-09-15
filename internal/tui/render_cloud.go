package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jesseduffield/gocui"
	"github.com/rigbyworks/lazyxcode/internal/xcodecloud"
)

func (a *App) renderCloud(buildView, buildsView, outputView *gocui.View) error {
	c := a.cloud
	now := a.clock()
	buildView.Clear()
	buildWidth, buildHeight := buildView.InnerSize()
	for _, line := range a.cloudConfigLines(buildHeight, now) {
		if strings.Contains(line, "! ") {
			fmt.Fprintf(buildView, "%s%s%s\n", ansiBoldYellow, truncate(line, buildWidth), ansiReset)
		} else {
			writeViewLine(buildView, buildWidth, line)
		}
	}

	buildsView.Clear()
	buildsWidth, _ := buildsView.InnerSize()
	runs := []xcodecloud.BuildRun(nil)
	if c != nil {
		runs = c.runs
	}
	for _, run := range runs {
		fmt.Fprintln(buildsView, formatCloudRunRow(run, buildsWidth, now))
	}
	if len(runs) == 0 {
		fmt.Fprintln(buildsView, "  "+a.cloudEmptyActivityMessage())
	}
	buildsView.Highlight = len(runs) > 0
	if len(runs) > 0 {
		if c.runIndex >= len(runs) {
			c.runIndex = len(runs) - 1
		}
		setListCursor(buildsView, c.runIndex, len(runs), 0)
	} else {
		setListCursor(buildsView, 0, 0, 0)
	}

	outputView.Clear()
	text := a.cloudOutputText(now)
	if report := a.selectedTestOutput(); report != nil {
		text = report.text
	}
	fmt.Fprint(outputView, formatBuildOutput(text))
	if c != nil && c.resetOutput {
		c.resetOutput = false
		outputView.SetOrigin(0, 0)
	} else {
		clampOutputOrigin(outputView)
	}
	return nil
}

func (a *App) cloudConfigLines(height int, now time.Time) []string {
	c := a.cloud
	if c == nil || c.setupErr != nil || c.service == nil {
		return []string{"  Setup required", "  See Output for steps", "", "  [i] Connection details", "  [r] Retry"}
	}

	product := "Select a product..."
	if selected, ok := c.selectedProduct(); ok {
		product = selected.Name
	} else if c.loading != "" && len(c.products) == 0 {
		product = "Loading..."
	} else if c.err != "" && len(c.products) == 0 {
		product = "Unavailable"
	}
	workflow := "All Workflows"
	if selected, ok := c.selectedWorkflow(); ok {
		workflow = selected.Name
	} else if c.product >= 0 && !c.workflowsLoaded && c.loading != "" {
		workflow = "Loading..."
	}
	prefix0, prefix1 := "  ", "  "
	if a.focus == "build" {
		if c.configRow == 0 {
			prefix0 = "> "
		} else if c.configRow == 1 {
			prefix1 = "> "
		}
	}
	prefix2 := "  "
	if a.focus == "build" && c.configRow == 2 {
		prefix2 = "> "
	}
	lines := []string{
		prefix0 + "Product   " + product,
		prefix1 + "Workflow  " + workflow,
		"",
		prefix2 + a.cloudConnectionLabel() + "  [i]",
		"  Read-only / sync " + formatPollInterval(c.pollInterval()),
	}
	if height >= 7 {
		lines = append(lines, "  "+a.cloudUpdatedLine(now))
	}
	return lines

}

func formatPollInterval(interval time.Duration) string {
	if interval < time.Minute {
		return fmt.Sprintf("%ds", int(interval.Seconds()))
	}
	return fmt.Sprintf("%dm", int(interval.Minutes()))
}

func (a *App) cloudUpdatedLine(now time.Time) string {
	c := a.cloud
	switch {
	case c.loadingMore:
		return "Loading more runs..."
	case c.loading != "":
		return "Syncing..."
	case c.download != nil:
		return "Received " + formatBytes(c.download.written)

	case c.err != "" && c.stale:
		return "Stale / refresh failed"
	case c.err != "":
		return "Refresh failed"
	case c.lastRefresh.IsZero():
		return "Not refreshed yet"
	}
	return "Updated " + formatAgo(now.Sub(c.lastRefresh))
}

func formatAgo(elapsed time.Duration) string {
	seconds := int(elapsed.Seconds())
	switch {
	case seconds < 1:
		return "just now"
	case seconds < 60:
		return fmt.Sprintf("%ds ago", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm ago", seconds/60)
	}
	return fmt.Sprintf("%dh ago", seconds/3600)
}

func (a *App) cloudEmptyActivityMessage() string {
	c := a.cloud
	switch {
	case c == nil || c.setupErr != nil || c.service == nil:
		return "Cloud is not configured"
	case c.loading != "":
		return c.loading
	case c.product < 0 && c.err != "":
		return c.err
	case c.product < 0:
		return "Select a product"
	case c.err != "":
		return "No runs loaded - " + c.err
	case !c.workflowsLoaded:
		return "Loading workflows..."
	}
	if workflow, ok := c.selectedWorkflow(); ok {
		return "No build runs for " + workflow.Name
	}
	if len(c.workflows) == 0 {
		return "Product has no workflows"
	}
	return "No build runs yet"
}

func (a *App) cloudOutputText(now time.Time) string {
	text := a.cloudRunOutputText(now)
	if a.cloud != nil && len(a.cloud.warnings) > 0 && !a.cloud.verbose {
		text += "\n\nCONNECTION WARNINGS\n\n" + strings.Join(a.cloud.warnings, "\n\n") + "\n\n[i] Connection details"
	}
	return text
}

func (a *App) cloudRunOutputText(now time.Time) string {
	c := a.cloud
	if c == nil || c.setupErr != nil || c.service == nil {
		return cloudSetupGuidance(cloudSetupError(c))
	}
	if c.product < 0 {
		if c.err != "" {
			return "XCODE CLOUD\n\n" + c.err + "\n\nPress r to retry."
		}
		if c.loading != "" {
			return "XCODE CLOUD\n\n" + c.loading
		}
		return "XCODE CLOUD\n\nPress Enter in the Cloud pane to select a product."
	}
	run, ok := c.selectedRun()
	if !ok {
		text := "XCODE CLOUD\n\n" + a.cloudEmptyActivityMessage()
		if c.err == "" && c.loading == "" && c.workflowsLoaded {
			text += "\n\nPress r to refresh."
		}
		return text
	}
	var text string
	if c.verbose {
		text = formatCloudRawOutput(run, c.rawLogs[run.ID], c.details[run.ID], c.detailLoading == run.ID)
	} else {
		details, have := c.details[run.ID]
		text = formatCloudDetails(run, details, have, c.detailLoading == run.ID, c.detailErrors[run.ID], now)
	}
	if c.err != "" {
		text = strings.TrimRight(text, "\n") + "\n\n[cloud] " + c.err + "\n"
	}
	return text
}

func cloudSetupGuidance(err error) string {
	lines := []string{"XCODE CLOUD", "", cloudErrorMessage(err), ""}
	lines = append(lines,
		"Create a team API key in App Store Connect under Users and Access >",
		"Integrations > App Store Connect API. The Developer role is enough for",
		"read-only Xcode Cloud access. Download the .p8 file once and keep it",
		"readable only by your user (chmod 600).",
		"",
		"Then export these variables before starting lazyxcode:",
		"",
		"  export "+xcodecloud.EnvIssuerID+"=<issuer id>",
		"  export "+xcodecloud.EnvKeyID+"=<key id>",
		"  export "+xcodecloud.EnvPrivateKeyPath+"=~/.private_keys/AuthKey_<key id>.p8",
		"",
		"Press r to retry, or m to return to Local mode. Local builds and tests",
		"remain fully available while Cloud is unconfigured.",
	)
	return strings.Join(lines, "\n")
}

func formatCloudRunRow(run xcodecloud.BuildRun, width int, now time.Time) string {
	prefix := fmt.Sprintf("%-6s #%d ", run.Status(), run.Number)
	duration := "-"
	if elapsed := run.Duration(now); elapsed > 0 {
		duration = formatDuration(elapsed)
	}
	workflow := run.WorkflowName
	if workflow == "" {
		workflow = "Workflow"
	}
	workflowWidth := max(3, width-len(prefix)-len(duration)-1)
	return truncate(prefix+fmt.Sprintf("%-*s %s", workflowWidth, truncate(workflow, workflowWidth), duration), width)
}

func cloudStatusMarker(status xcodecloud.Status) string {
	switch status {
	case xcodecloud.StatusSucceeded:
		return "✓"
	case xcodecloud.StatusFailed:
		return "✗"
	case xcodecloud.StatusRunning, xcodecloud.StatusWaiting:
		return "●"
	}
	return "-"
}

func cloudRunStatusLine(run xcodecloud.BuildRun, now time.Time) string {
	duration := formatStepDuration(run.Duration(now))
	switch run.Status() {
	case xcodecloud.StatusWaiting:
		return "Waiting to start"
	case xcodecloud.StatusRunning:
		return "Running for " + duration
	case xcodecloud.StatusSucceeded:
		return "Succeeded in " + duration
	case xcodecloud.StatusFailed:
		if run.Duration(now) > 0 {
			return "Failed after " + duration
		}
		return "Failed"
	case xcodecloud.StatusCancelled:
		if run.CancelReason != "" {
			return "Cancelled (" + humanizeReason(run.CancelReason) + ")"
		}
		return "Cancelled"
	case xcodecloud.StatusSkipped:
		return "Skipped"
	}
	if run.CompletionStatus != "" {
		return humanizeReason(run.CompletionStatus)
	}
	return "Unknown"
}

func humanizeReason(value string) string {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", " "))
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func formatCloudDetails(run xcodecloud.BuildRun, details xcodecloud.BuildDetails, have, loading bool, detailErr string, now time.Time) string {
	if have {
		run = details.Run
	}
	lines := []string{fmt.Sprintf("XCODE CLOUD BUILD #%d", run.Number)}
	workflow := run.WorkflowName
	if workflow == "" {
		workflow = "-"
	}
	lines = append(lines, "Workflow       "+workflow)
	if run.SourceBranch != "" || run.SourceCommit != "" {
		lines = append(lines, "Source         "+joinBranchCommit(run.SourceBranch, run.SourceCommit))
	}
	if run.DestinationBranch != "" || run.DestinationCommit != "" {
		lines = append(lines, "Destination    "+joinBranchCommit(run.DestinationBranch, run.DestinationCommit))
	}
	if message := firstLine(run.CommitMessage); message != "" {
		lines = append(lines, "Commit         "+message)
	}
	if run.StartReason != "" {
		reason := humanizeReason(run.StartReason)
		if run.IsPullRequest {
			reason += " (pull request)"
		}
		lines = append(lines, "Reason         "+reason)
	}
	started := run.CreatedAt
	if run.StartedAt != nil {
		started = *run.StartedAt
	}
	if !started.IsZero() {
		lines = append(lines, "Started        "+started.Local().Format("Jan 2, 3:04 PM"))
	}
	lines = append(lines, "Status         "+cloudRunStatusLine(run, now))
	if !have {
		lines = append(lines, "")
		switch {
		case detailErr != "":
			lines = append(lines, "Details unavailable: "+detailErr)
		case loading:
			lines = append(lines, "Loading build details...")
		default:
			lines = append(lines, "Build details have not been loaded.")
		}
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", fmt.Sprintf("ACTIONS (%d)", len(details.Actions)))
	if len(details.Actions) == 0 {
		lines = append(lines, "  No actions reported yet")
	}
	for _, action := range details.Actions {
		status := action.Status()
		detail := formatStepDuration(action.Duration(now))
		switch status {
		case xcodecloud.StatusSkipped:
			detail = "skipped"
		case xcodecloud.StatusWaiting:
			detail = "waiting"
		case xcodecloud.StatusCancelled:
			detail = "cancelled"
		case xcodecloud.StatusUnknown:
			detail = strings.ToLower(action.CompletionStatus)
		}
		if action.Duration(now) == 0 && (status == xcodecloud.StatusSucceeded || status == xcodecloud.StatusFailed) {
			detail = strings.ToLower(string(status))
		}
		lines = append(lines, fmt.Sprintf("  %s %-36s %8s", cloudStatusMarker(status), truncate(action.Name, 36), detail))
	}

	diagnostics := collectCloudDiagnostics(details)
	if len(diagnostics) > 0 {
		lines = append(lines, "", fmt.Sprintf("DIAGNOSTICS (%d)", len(diagnostics)))
		lines = append(lines, diagnostics...)
	}

	hasTests := false
	passed, failed, skipped := 0, 0, 0
	var failures []string
	for _, action := range details.Actions {
		if !strings.EqualFold(action.Type, "TEST") {
			continue
		}
		hasTests = true
		for _, result := range action.TestResults {
			switch {
			case strings.EqualFold(result.Status, "SKIPPED"):
				skipped++
			case result.Passed():
				passed++
			default:
				failed++
				name := result.Name
				if result.ClassName != "" {
					name = result.ClassName + "." + result.Name
				}
				failures = append(failures, fmt.Sprintf("  ✗ %-36s %8s", truncate(name, 36), formatStepDuration(result.Duration())))
			}
		}
	}
	if hasTests {
		lines = append(lines, "", "TESTS")
		if passed+failed+skipped == 0 {
			if run.Status().Active() {
				lines = append(lines, "  Waiting for test results...")
			} else {
				lines = append(lines, "  No test results reported")
			}
		} else {
			summary := fmt.Sprintf("  %d passed, %d failed", passed, failed)
			if skipped > 0 {
				summary += fmt.Sprintf(", %d skipped", skipped)
			}
			lines = append(lines, summary)
			lines = append(lines, failures...)
		}
	}

	var artifacts []string
	for _, action := range details.Actions {
		for _, artifact := range action.Artifacts {
			artifacts = append(artifacts, fmt.Sprintf("  %-36s %10s", truncate(artifact.Name, 36), formatBytes(artifact.Size)))
		}
	}
	lines = append(lines, "", fmt.Sprintf("ARTIFACTS (%d)", len(artifacts)))
	if len(artifacts) == 0 {
		if run.Status().Active() {
			lines = append(lines, "  Artifacts appear when actions finish")
		} else {
			lines = append(lines, "  No artifacts available")
		}
	} else {
		lines = append(lines, artifacts...)
		lines = append(lines, "", "Press a to download an artifact, v for raw logs.")
	}
	return strings.Join(lines, "\n")
}

func joinBranchCommit(branch, commit string) string {
	switch {
	case branch != "" && commit != "":
		return branch + " @ " + shortCommit(commit)
	case branch != "":
		return branch
	}
	return shortCommit(commit)
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

// collectCloudDiagnostics flattens structured issues across actions and
// deduplicates identical entries by severity, location, and message.
func collectCloudDiagnostics(details xcodecloud.BuildDetails) []string {
	var lines []string
	seen := map[string]bool{}
	for _, action := range details.Actions {
		for _, issue := range action.Issues {
			severity := issue.Severity()
			location := filepath.Base(issue.File)
			if issue.Line > 0 {
				location += fmt.Sprintf(":%d", issue.Line)
				if issue.Column > 0 {
					location += fmt.Sprintf(":%d", issue.Column)
				}
			}
			if issue.File == "" {
				location = action.Name
			}
			message := firstLine(issue.Message)
			if message == "" {
				message = humanizeReason(issue.Category)
			}
			key := severity + "\x00" + location + "\x00" + message
			if seen[key] {
				continue
			}
			seen[key] = true
			lines = append(lines, fmt.Sprintf("  %s: %s - %s", severity, location, message))
		}
	}
	return lines
}

func formatCloudRawOutput(run xcodecloud.BuildRun, entry *cloudRawLog, details xcodecloud.BuildDetails, loadingDetails bool) string {
	header := fmt.Sprintf("XCODE CLOUD BUILD #%d - RAW LOGS\n\n", run.Number)
	if entry == nil {
		if loadingDetails {
			return header + "Loading build details before fetching logs..."
		}
		if len(details.Actions) == 0 {
			return header + "Build details are not available yet; press v again once they load."
		}
		return header + "Preparing log download..."
	}
	switch entry.state {
	case rawLogLoading:
		progress := "Downloading logs..."
		if entry.progress > 0 {
			progress = "Downloading logs... " + formatBytes(entry.progress)
			if entry.total > 0 {
				progress += " of " + formatBytes(entry.total)
			}
		}
		return header + progress
	case rawLogEmpty:
		if run.Status().Active() {
			return header + "Log artifacts appear when actions finish. Concise output is available with v."
		}
		return header + "This run exposes no log artifacts. Press a to see the available artifacts."
	case rawLogFailed:
		return header + "Log download failed: " + entry.err + "\n\nPress v twice to retry."
	}
	if strings.TrimSpace(entry.text) == "" {
		return header + "Downloaded logs are empty. Files: " + strings.Join(entry.paths, ", ")
	}
	return header + entry.text
}

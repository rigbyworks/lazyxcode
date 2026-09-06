// Package xcodecloud provides read-only access to Xcode Cloud products,
// workflows, build runs, and artifacts through the App Store Connect API.
//
// The package hides authentication, JSON:API wire types, relationship
// traversal, pagination, and retries behind the Service interface.
package xcodecloud

import (
	"context"
	"strings"
	"time"
)

// Service is the TUI-facing contract for read-only Xcode Cloud access.
type Service interface {
	ListProducts(context.Context) ([]Product, error)
	ListWorkflows(context.Context, string) ([]Workflow, error)
	ListBuildRuns(context.Context, BuildRunQuery) (BuildRunPage, error)
	GetBuildDetails(context.Context, string) (BuildDetails, error)
	DownloadArtifact(context.Context, Artifact, string, func(int64)) error
}

// Product is an Xcode Cloud product (usually one app).
type Product struct {
	ID   string
	Name string
	Type string
}

// Workflow is an Xcode Cloud workflow that belongs to a product.
type Workflow struct {
	ID       string
	Name     string
	Disabled bool
}

// Status is a display-only normalization of Apple's execution progress and
// completion status pairs.
type Status string

const (
	StatusWaiting   Status = "WAIT"
	StatusRunning   Status = "RUN"
	StatusSucceeded Status = "OK"
	StatusFailed    Status = "FAIL"
	StatusCancelled Status = "STOP"
	StatusSkipped   Status = "SKIP"
	StatusUnknown   Status = "?"
)

// Active reports whether the status describes work that is still in progress.
func (s Status) Active() bool { return s == StatusWaiting || s == StatusRunning }

// NormalizeStatus maps an execution progress and completion status pair to a
// display status. Unknown future values map to StatusUnknown.
func NormalizeStatus(executionProgress, completionStatus string) Status {
	switch strings.ToUpper(strings.TrimSpace(executionProgress)) {
	case "PENDING":
		return StatusWaiting
	case "RUNNING":
		return StatusRunning
	}
	switch strings.ToUpper(strings.TrimSpace(completionStatus)) {
	case "SUCCEEDED":
		return StatusSucceeded
	case "FAILED", "ERRORED":
		return StatusFailed
	case "CANCELED", "CANCELLED":
		return StatusCancelled
	case "SKIPPED":
		return StatusSkipped
	}
	return StatusUnknown
}

// BuildRun is one execution of a workflow.
type BuildRun struct {
	ID                   string
	Number               int
	WorkflowID           string
	WorkflowName         string
	ExecutionProgress    string
	CompletionStatus     string
	StartReason          string
	CancelReason         string
	CreatedAt            time.Time
	StartedAt            *time.Time
	FinishedAt           *time.Time
	SourceBranch         string
	SourceCommit         string
	CommitMessage        string
	DestinationBranch    string
	DestinationCommit    string
	IsPullRequest        bool
	ErrorCount           int
	WarningCount         int
	AnalyzerWarningCount int
	TestFailureCount     int
}

// Status returns the normalized display status of the run.
func (r BuildRun) Status() Status {
	return NormalizeStatus(r.ExecutionProgress, r.CompletionStatus)
}

// Duration returns how long the run has been executing, using now for runs
// that have not finished.
func (r BuildRun) Duration(now time.Time) time.Duration {
	start := r.CreatedAt
	if r.StartedAt != nil {
		start = *r.StartedAt
	}
	if start.IsZero() {
		return 0
	}
	end := now
	if r.FinishedAt != nil {
		end = *r.FinishedAt
	} else if !r.Status().Active() {
		return 0
	}
	return max(0, end.Sub(start))
}

// BuildRunQuery selects the runs to list. Cursor is an opaque continuation
// value returned by a previous page; when it is set the other fields are ignored.
type BuildRunQuery struct {
	ProductID  string
	WorkflowID string
	Limit      int
	Cursor     string
}

// BuildRunPage is one page of build runs plus the cursor for the next page.
type BuildRunPage struct {
	Runs       []BuildRun
	NextCursor string
}

// BuildDetails is a run together with its actions and their details.
type BuildDetails struct {
	Run     BuildRun
	Actions []Action
}

// Action is one step of a build run, such as build, analyze, test, or archive.
type Action struct {
	ID                   string
	Name                 string
	Type                 string
	RequiredToPass       bool
	ExecutionProgress    string
	CompletionStatus     string
	StartedAt            *time.Time
	FinishedAt           *time.Time
	ErrorCount           int
	WarningCount         int
	AnalyzerWarningCount int
	TestFailureCount     int
	Issues               []Issue
	TestResults          []TestResult
	Artifacts            []Artifact
}

// Status returns the normalized display status of the action.
func (a Action) Status() Status {
	return NormalizeStatus(a.ExecutionProgress, a.CompletionStatus)
}

// Duration returns how long the action has run, using now while it is active.
func (a Action) Duration(now time.Time) time.Duration {
	if a.StartedAt == nil {
		return 0
	}
	end := now
	if a.FinishedAt != nil {
		end = *a.FinishedAt
	} else if !a.Status().Active() {
		return 0
	}
	return max(0, end.Sub(*a.StartedAt))
}

// Issue is a structured diagnostic reported by an action.
type Issue struct {
	Type     string
	Category string
	Message  string
	File     string
	Line     int
	Column   int
}

// Severity returns a lower-case severity label derived from the issue type.
func (i Issue) Severity() string {
	switch strings.ToUpper(i.Type) {
	case "ERROR", "TEST_FAILURE":
		return "error"
	case "WARNING", "ANALYZER_WARNING":
		return "warning"
	}
	return strings.ToLower(strings.ReplaceAll(i.Type, "_", " "))
}

// TestResult is one test case reported by a test action.
type TestResult struct {
	ClassName    string
	Name         string
	Status       string
	Message      string
	File         string
	Line         int
	Destinations []TestDestination
}

// Passed reports whether every destination succeeded.
func (t TestResult) Passed() bool {
	switch strings.ToUpper(t.Status) {
	case "SUCCESS", "EXPECTED_FAILURE", "SKIPPED":
		return true
	}
	return false
}

// Duration returns the longest destination duration for the test.
func (t TestResult) Duration() time.Duration {
	var longest time.Duration
	for _, destination := range t.Destinations {
		longest = max(longest, destination.Duration)
	}
	return longest
}

// TestDestination is the result of one test on one device.
type TestDestination struct {
	Device   string
	OS       string
	Status   string
	Duration time.Duration
}

// Artifact file types reported by App Store Connect.
const (
	ArtifactArchive           = "ARCHIVE"
	ArtifactArchiveExport     = "ARCHIVE_EXPORT"
	ArtifactLogBundle         = "LOG_BUNDLE"
	ArtifactResultBundle      = "RESULT_BUNDLE"
	ArtifactTestProducts      = "TEST_PRODUCTS"
	ArtifactXcodebuildProduct = "XCODEBUILD_PRODUCTS"
)

// Artifact is a downloadable file produced by an action.
type Artifact struct {
	ID          string
	Name        string
	Type        string
	Size        int64
	DownloadURL string
	ActionID    string
	ActionName  string
}

// IsLog reports whether the artifact holds build logs.
func (a Artifact) IsLog() bool {
	if strings.EqualFold(a.Type, ArtifactLogBundle) {
		return true
	}
	lower := strings.ToLower(a.Name)
	return strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".txt")
}

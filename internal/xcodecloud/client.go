package xcodecloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the App Store Connect API origin.
const DefaultBaseURL = "https://api.appstoreconnect.apple.com"

const (
	defaultRunLimit    = 25
	maxPageLimit       = 200
	maxResponseBytes   = 32 << 20
	defaultAttempts    = 4
	maxRetryDelay      = 30 * time.Second
	detailConcurrency  = 4
	defaultHTTPTimeout = 60 * time.Second
)

// API errors. APIError values unwrap to these sentinels by HTTP status.
var (
	ErrUnauthorized    = errors.New("App Store Connect rejected the API token")
	ErrForbidden       = errors.New("the App Store Connect API key lacks permission for this request")
	ErrNotFound        = errors.New("App Store Connect resource not found")
	ErrRateLimited     = errors.New("App Store Connect rate limit reached")
	ErrArtifactExpired = errors.New("artifact download link has expired")
)

// APIError is a non-success response from App Store Connect.
type APIError struct {
	Status int
	Code   string
	Title  string
	Detail string
}

func (e *APIError) Error() string {
	message := fmt.Sprintf("App Store Connect returned %d", e.Status)
	if e.Title != "" {
		message += " " + e.Title
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

func (e *APIError) Unwrap() error {
	switch e.Status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return nil
}

// Client is a read-only App Store Connect client. It only issues GET requests.
type Client struct {
	http        *http.Client
	tokens      TokenSource
	base        *url.URL
	userAgent   string
	attempts    int
	sleep       func(context.Context, time.Duration) error
	jitter      func() float64
	now         func() time.Time
	concurrency int
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client used for every request.
func WithHTTPClient(client *http.Client) Option { return func(c *Client) { c.http = client } }

// WithBaseURL points the client at a different API origin, such as a test server.
func WithBaseURL(base string) Option {
	return func(c *Client) {
		if parsed, err := url.Parse(base); err == nil {
			c.base = parsed
		}
	}
}

// WithSleep replaces the retry delay function.
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.sleep = sleep }
}

// WithMaxAttempts bounds retries for transient failures.
func WithMaxAttempts(attempts int) Option {
	return func(c *Client) { c.attempts = max(1, attempts) }
}

// NewClient builds a client that authenticates with the token source.
func NewClient(tokens TokenSource, options ...Option) *Client {
	base, _ := url.Parse(DefaultBaseURL)
	c := &Client{
		http:        &http.Client{Timeout: defaultHTTPTimeout},
		tokens:      tokens,
		base:        base,
		userAgent:   "lazyxcode",
		attempts:    defaultAttempts,
		sleep:       sleepContext,
		jitter:      rand.Float64,
		now:         time.Now,
		concurrency: detailConcurrency,
	}
	for _, option := range options {
		option(c)
	}
	return c
}

// Connect loads credentials from the environment and returns a ready client
// together with non-fatal warnings.
func Connect(getenv func(string) string) (*Client, []string, error) {
	credentials, err := LoadCredentials(getenv)
	if err != nil {
		return nil, nil, err
	}
	source, warnings, err := NewKeyTokenSource(credentials, time.Now)
	if err != nil {
		return nil, warnings, err
	}
	return NewClient(source), warnings, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ListProducts returns every Xcode Cloud product visible to the key.
func (c *Client) ListProducts(ctx context.Context) ([]Product, error) {
	var products []Product
	err := c.collect(ctx, c.endpoint("/v1/ciProducts", url.Values{"limit": {strconv.Itoa(maxPageLimit)}}), func(doc *document) error {
		items, err := doc.resources()
		if err != nil {
			return err
		}
		for _, item := range items {
			var attributes ciProductAttributes
			if err := json.Unmarshal(item.Attributes, &attributes); err != nil {
				return fmt.Errorf("parse product %s: %w", item.ID, err)
			}
			products = append(products, Product{ID: item.ID, Name: attributes.Name, Type: attributes.ProductType})
		}
		return nil
	})
	return products, err
}

// ListWorkflows returns the workflows of a product.
func (c *Client) ListWorkflows(ctx context.Context, productID string) ([]Workflow, error) {
	if productID == "" {
		return nil, errors.New("product ID is required")
	}
	var workflows []Workflow
	path := "/v1/ciProducts/" + url.PathEscape(productID) + "/workflows"
	err := c.collect(ctx, c.endpoint(path, url.Values{"limit": {strconv.Itoa(maxPageLimit)}}), func(doc *document) error {
		items, err := doc.resources()
		if err != nil {
			return err
		}
		for _, item := range items {
			var attributes ciWorkflowAttributes
			if err := json.Unmarshal(item.Attributes, &attributes); err != nil {
				return fmt.Errorf("parse workflow %s: %w", item.ID, err)
			}
			workflows = append(workflows, Workflow{
				ID: item.ID, Name: attributes.Name,
				Disabled: attributes.IsEnabled != nil && !*attributes.IsEnabled,
			})
		}
		return nil
	})
	return workflows, err
}

// ListBuildRuns returns one page of runs, newest first.
func (c *Client) ListBuildRuns(ctx context.Context, query BuildRunQuery) (BuildRunPage, error) {
	target := query.Cursor
	if target == "" {
		limit := query.Limit
		if limit <= 0 {
			limit = defaultRunLimit
		}
		limit = min(limit, maxPageLimit)
		values := url.Values{
			"sort":    {"-number"},
			"limit":   {strconv.Itoa(limit)},
			"include": {"workflow,sourceBranchOrTag,destinationBranch"},
		}
		switch {
		case query.WorkflowID != "":
			target = c.endpoint("/v1/ciWorkflows/"+url.PathEscape(query.WorkflowID)+"/buildRuns", values)
		case query.ProductID != "":
			target = c.endpoint("/v1/ciProducts/"+url.PathEscape(query.ProductID)+"/buildRuns", values)
		default:
			return BuildRunPage{}, errors.New("product or workflow ID is required")
		}
	}
	doc, err := c.getDocument(ctx, target)
	if err != nil {
		return BuildRunPage{}, err
	}
	items, err := doc.resources()
	if err != nil {
		return BuildRunPage{}, err
	}
	included := doc.includedByRef()
	page := BuildRunPage{NextCursor: doc.Links.Next}
	for _, item := range items {
		run, err := mapBuildRun(item, included)
		if err != nil {
			return BuildRunPage{}, err
		}
		page.Runs = append(page.Runs, run)
	}
	return page, nil
}

func mapBuildRun(item resource, included map[resourceRef]resource) (BuildRun, error) {
	var attributes ciBuildRunAttributes
	if err := json.Unmarshal(item.Attributes, &attributes); err != nil {
		return BuildRun{}, fmt.Errorf("parse build run %s: %w", item.ID, err)
	}
	run := BuildRun{
		ID: item.ID, Number: attributes.Number,
		ExecutionProgress: attributes.ExecutionProgress, CompletionStatus: attributes.CompletionStatus,
		StartReason: attributes.StartReason, CancelReason: attributes.CancelReason,
		StartedAt: attributes.StartedDate, FinishedAt: attributes.FinishedDate,
		IsPullRequest: attributes.IsPullRequestBuild,
	}
	if attributes.CreatedDate != nil {
		run.CreatedAt = *attributes.CreatedDate
	}
	if attributes.SourceCommit != nil {
		run.SourceCommit = attributes.SourceCommit.CommitSha
		run.CommitMessage = attributes.SourceCommit.Message
	}
	if attributes.DestinationCommit != nil {
		run.DestinationCommit = attributes.DestinationCommit.CommitSha
	}
	if counts := attributes.IssueCounts; counts != nil {
		run.ErrorCount, run.WarningCount = counts.Errors, counts.Warnings
		run.AnalyzerWarningCount, run.TestFailureCount = counts.AnalyzerWarnings, counts.TestFailures
	}
	if ref, ok := item.Relationships["workflow"].ref(); ok {
		run.WorkflowID = ref.ID
		if workflow, found := included[ref]; found {
			var workflowAttributes ciWorkflowAttributes
			if json.Unmarshal(workflow.Attributes, &workflowAttributes) == nil {
				run.WorkflowName = workflowAttributes.Name
			}
		}
	}
	run.SourceBranch = gitReferenceName(item.Relationships["sourceBranchOrTag"], included)
	run.DestinationBranch = gitReferenceName(item.Relationships["destinationBranch"], included)
	return run, nil
}

func gitReferenceName(rel relationship, included map[resourceRef]resource) string {
	ref, ok := rel.ref()
	if !ok {
		return ""
	}
	item, found := included[ref]
	if !found {
		return ""
	}
	var attributes scmGitReferenceAttributes
	if json.Unmarshal(item.Attributes, &attributes) != nil {
		return ""
	}
	if attributes.Name != "" {
		return attributes.Name
	}
	return attributes.CanonicalName
}

// GetBuildDetails returns the run with its actions, issues, test results, and
// artifact metadata. Action detail requests run concurrently with a small bound
// and stop when ctx is cancelled.
func (c *Client) GetBuildDetails(ctx context.Context, runID string) (BuildDetails, error) {
	if runID == "" {
		return BuildDetails{}, errors.New("build run ID is required")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	runPath := "/v1/ciBuildRuns/" + url.PathEscape(runID)
	doc, err := c.getDocument(ctx, c.endpoint(runPath, url.Values{"include": {"workflow,sourceBranchOrTag,destinationBranch"}}))
	if err != nil {
		return BuildDetails{}, err
	}
	items, err := doc.resources()
	if err != nil {
		return BuildDetails{}, err
	}
	if len(items) != 1 {
		return BuildDetails{}, fmt.Errorf("build run %s not found", runID)
	}
	run, err := mapBuildRun(items[0], doc.includedByRef())
	if err != nil {
		return BuildDetails{}, err
	}
	details := BuildDetails{Run: run}
	err = c.collect(ctx, c.endpoint(runPath+"/actions", url.Values{"limit": {strconv.Itoa(maxPageLimit)}}), func(doc *document) error {
		items, err := doc.resources()
		if err != nil {
			return err
		}
		for _, item := range items {
			var attributes ciBuildActionAttributes
			if err := json.Unmarshal(item.Attributes, &attributes); err != nil {
				return fmt.Errorf("parse build action %s: %w", item.ID, err)
			}
			action := Action{
				ID: item.ID, Name: attributes.Name, Type: attributes.ActionType, RequiredToPass: attributes.IsRequiredToPass,
				ExecutionProgress: attributes.ExecutionProgress, CompletionStatus: attributes.CompletionStatus,
				StartedAt: attributes.StartedDate, FinishedAt: attributes.FinishedDate,
			}
			if counts := attributes.IssueCounts; counts != nil {
				action.ErrorCount, action.WarningCount = counts.Errors, counts.Warnings
				action.AnalyzerWarningCount, action.TestFailureCount = counts.AnalyzerWarnings, counts.TestFailures
			}
			details.Actions = append(details.Actions, action)
		}
		return nil
	})
	if err != nil {
		return BuildDetails{}, err
	}
	if err := c.enrichActions(ctx, details.Actions); err != nil {
		return BuildDetails{}, err
	}
	return details, nil
}

func (c *Client) enrichActions(ctx context.Context, actions []Action) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, max(1, c.concurrency))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}
	for i := range actions {
		wg.Add(1)
		go func(action *Action) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				fail(ctx.Err())
				return
			}
			defer func() { <-semaphore }()
			if err := c.loadActionDetails(ctx, action); err != nil {
				fail(err)
			}
		}(&actions[i])
	}
	wg.Wait()
	return firstErr
}

func (c *Client) loadActionDetails(ctx context.Context, action *Action) error {
	base := "/v1/ciBuildActions/" + url.PathEscape(action.ID)
	limit := url.Values{"limit": {strconv.Itoa(maxPageLimit)}}
	err := c.collect(ctx, c.endpoint(base+"/issues", limit), func(doc *document) error {
		items, err := doc.resources()
		if err != nil {
			return err
		}
		for _, item := range items {
			var attributes ciIssueAttributes
			if err := json.Unmarshal(item.Attributes, &attributes); err != nil {
				return fmt.Errorf("parse issue %s: %w", item.ID, err)
			}
			issue := Issue{Type: attributes.IssueType, Category: attributes.Category, Message: attributes.Message}
			if attributes.FileSource != nil {
				issue.File, issue.Line = attributes.FileSource.Path, attributes.FileSource.LineNumber
			}
			action.Issues = append(action.Issues, issue)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if strings.EqualFold(action.Type, "TEST") {
		err = c.collect(ctx, c.endpoint(base+"/testResults", limit), func(doc *document) error {
			items, err := doc.resources()
			if err != nil {
				return err
			}
			for _, item := range items {
				var attributes ciTestResultAttributes
				if err := json.Unmarshal(item.Attributes, &attributes); err != nil {
					return fmt.Errorf("parse test result %s: %w", item.ID, err)
				}
				result := TestResult{ClassName: attributes.ClassName, Name: attributes.Name, Status: attributes.Status, Message: attributes.Message}
				if attributes.FileSource != nil {
					result.File, result.Line = attributes.FileSource.Path, attributes.FileSource.LineNumber
				}
				for _, destination := range attributes.DestinationTestResults {
					result.Destinations = append(result.Destinations, TestDestination{
						Device: destination.DeviceName, OS: destination.OSVersion, Status: destination.Status,
						Duration: time.Duration(destination.Duration * float64(time.Second)),
					})
				}
				action.TestResults = append(action.TestResults, result)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return c.collect(ctx, c.endpoint(base+"/artifacts", limit), func(doc *document) error {
		items, err := doc.resources()
		if err != nil {
			return err
		}
		for _, item := range items {
			var attributes ciArtifactAttributes
			if err := json.Unmarshal(item.Attributes, &attributes); err != nil {
				return fmt.Errorf("parse artifact %s: %w", item.ID, err)
			}
			action.Artifacts = append(action.Artifacts, Artifact{
				ID: item.ID, Name: attributes.FileName, Type: attributes.FileType, Size: attributes.FileSize,
				DownloadURL: attributes.DownloadURL, ActionID: action.ID, ActionName: action.Name,
			})
		}
		return nil
	})
}

// DownloadArtifact streams the artifact to destination through a temporary
// file in the same directory, verifies the byte count when Apple reports a
// size, and renames atomically. Existing files with a matching size are reused.
// Cancelled or failed downloads leave no temporary file behind.
func (c *Client) DownloadArtifact(ctx context.Context, artifact Artifact, destination string, progress func(int64)) error {
	if progress == nil {
		progress = func(int64) {}
	}
	if artifact.DownloadURL == "" {
		return errors.New("artifact has no download URL")
	}
	target, err := url.Parse(artifact.DownloadURL)
	if err != nil {
		return fmt.Errorf("invalid artifact download URL: %w", err)
	}
	if target.Scheme != "https" && !(target.Scheme == "http" && c.base.Scheme == "http") {
		return fmt.Errorf("refusing artifact download over %q", target.Scheme)
	}
	if info, statErr := os.Stat(destination); statErr == nil && info.Mode().IsRegular() && artifact.Size > 0 && info.Size() == artifact.Size {
		progress(info.Size())
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= c.attempts; attempt++ {
		retry, delay, err := c.downloadOnce(ctx, target.String(), artifact.Size, destination, progress)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry || attempt == c.attempts || ctx.Err() != nil {
			break
		}
		if err := c.sleep(ctx, c.retryDelay(attempt, delay)); err != nil {
			return err
		}
	}
	return lastErr
}

func (c *Client) downloadOnce(ctx context.Context, target string, size int64, destination string, progress func(int64)) (bool, time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, 0, err
	}
	request.Header.Set("User-Agent", c.userAgent)
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return false, 0, ctx.Err()
		}
		return true, 0, fmt.Errorf("download artifact: %w", err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
	case response.StatusCode == http.StatusBadRequest, response.StatusCode == http.StatusForbidden, response.StatusCode == http.StatusGone:
		return false, 0, fmt.Errorf("%w (HTTP %d)", ErrArtifactExpired, response.StatusCode)
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		return true, parseRetryAfter(response.Header.Get("Retry-After"), c.now()), fmt.Errorf("download artifact: HTTP %d", response.StatusCode)
	default:
		return false, 0, fmt.Errorf("download artifact: HTTP %d", response.StatusCode)
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".tmp-*")
	if err != nil {
		return false, 0, err
	}
	tempName := temp.Name()
	cleanup := func() {
		temp.Close()
		os.Remove(tempName)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return false, 0, err
	}
	written, err := io.Copy(&progressWriter{writer: temp, progress: progress}, response.Body)
	if err != nil {
		cleanup()
		if ctx.Err() != nil {
			return false, 0, ctx.Err()
		}
		return true, 0, fmt.Errorf("download artifact: %w", err)
	}
	if size > 0 && written != size {
		cleanup()
		return true, 0, fmt.Errorf("download artifact: received %d of %d bytes", written, size)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return false, 0, err
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempName)
		return false, 0, err
	}
	if err := os.Rename(tempName, destination); err != nil {
		os.Remove(tempName)
		return false, 0, err
	}
	return false, 0, nil
}

type progressWriter struct {
	writer   io.Writer
	progress func(int64)
	total    int64
}

func (w *progressWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.total += int64(n)
	w.progress(w.total)
	return n, err
}

func (c *Client) endpoint(path string, values url.Values) string {
	target := *c.base
	target.Path = strings.TrimSuffix(c.base.Path, "/") + path
	target.RawQuery = values.Encode()
	return target.String()
}

// collect follows the API's own pagination links until the collection ends.
func (c *Client) collect(ctx context.Context, target string, visit func(*document) error) error {
	seen := map[string]bool{}
	for target != "" {
		if seen[target] {
			return errors.New("App Store Connect pagination loop detected")
		}
		seen[target] = true
		doc, err := c.getDocument(ctx, target)
		if err != nil {
			return err
		}
		if err := visit(doc); err != nil {
			return err
		}
		target = doc.Links.Next
	}
	return nil
}

func (c *Client) getDocument(ctx context.Context, target string) (*document, error) {
	resolved, err := c.resolve(target)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 1; attempt <= c.attempts; attempt++ {
		doc, retry, delay, err := c.attempt(ctx, resolved)
		if err == nil {
			return doc, nil
		}
		lastErr = err
		if !retry || attempt == c.attempts || ctx.Err() != nil {
			break
		}
		if err := c.sleep(ctx, c.retryDelay(attempt, delay)); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// resolve turns a path or link into an absolute URL on the API origin and
// refuses links that would send the bearer token elsewhere.
func (c *Client) resolve(target string) (string, error) {
	if strings.HasPrefix(target, "/") {
		return c.base.ResolveReference(&url.URL{Path: target}).String(), nil
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("invalid App Store Connect link: %w", err)
	}
	if parsed.Scheme != c.base.Scheme || !strings.EqualFold(parsed.Host, c.base.Host) {
		return "", fmt.Errorf("refusing to follow App Store Connect link to %q", parsed.Host)
	}
	return parsed.String(), nil
}

func (c *Client) attempt(ctx context.Context, target string) (*document, bool, time.Duration, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, false, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, false, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, 0, ctx.Err()
		}
		return nil, true, 0, fmt.Errorf("App Store Connect request failed: %w", redactURLError(err))
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, 0, ctx.Err()
		}
		return nil, true, 0, fmt.Errorf("read App Store Connect response: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var doc document
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, false, 0, fmt.Errorf("parse App Store Connect response: %w", err)
		}
		return &doc, false, 0, nil
	}
	apiErr := &APIError{Status: response.StatusCode}
	var errorDoc errorDocument
	if json.Unmarshal(body, &errorDoc) == nil && len(errorDoc.Errors) > 0 {
		apiErr.Code, apiErr.Title, apiErr.Detail = errorDoc.Errors[0].Code, errorDoc.Errors[0].Title, errorDoc.Errors[0].Detail
	}
	retry := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	return nil, retry, parseRetryAfter(response.Header.Get("Retry-After"), c.now()), apiErr
}

func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

func (c *Client) retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, maxRetryDelay)
	}
	base := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
	jitter := time.Duration(c.jitter() * float64(base))
	return min(base+jitter, maxRetryDelay)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return max(0, time.Duration(seconds)*time.Second)
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(0, when.Sub(now))
	}
	return 0
}

package xcodecloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type staticTokens struct{ token string }

func (s staticTokens) Token(context.Context) (string, error) { return s.token, nil }

type testServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	routes   map[string]http.HandlerFunc
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	server := &testServer{routes: map[string]http.HandlerFunc{}}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.requests = append(server.requests, r)
		handler := server.routes[r.URL.Path]
		server.mu.Unlock()
		if handler == nil {
			http.Error(w, `{"errors":[{"status":"404","code":"NOT_FOUND","title":"The specified resource does not exist","detail":"There is no resource at "+r.URL.Path}]}`, http.StatusNotFound)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *testServer) route(path string, handler http.HandlerFunc) { s.routes[path] = handler }

func (s *testServer) json(path, body string) {
	s.route(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, strings.ReplaceAll(body, "{{base}}", s.URL))
	})
}

func (s *testServer) requestCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, request := range s.requests {
		if request.URL.Path == path {
			count++
		}
	}
	return count
}

func (s *testServer) client(t *testing.T, options ...Option) (*Client, *[]time.Duration) {
	t.Helper()
	sleeps := &[]time.Duration{}
	options = append([]Option{
		WithBaseURL(s.URL), WithHTTPClient(s.Client()),
		WithSleep(func(_ context.Context, delay time.Duration) error { *sleeps = append(*sleeps, delay); return nil }),
	}, options...)
	return NewClient(staticTokens{token: "test-token"}, options...), sleeps
}

const productsPageOne = `{"data":[{"type":"ciProducts","id":"prod-1","attributes":{"name":"Fortyfive","productType":"APP"}}],
"links":{"self":"{{base}}/v1/ciProducts?limit=200","next":"{{base}}/v1/ciProducts/page-two?cursor=AbC&limit=200"}}`

const productsPageTwo = `{"data":[{"type":"ciProducts","id":"prod-2","attributes":{"name":"Widgets","productType":"FRAMEWORK"}}],
"links":{"self":"{{base}}/v1/ciProducts?cursor=AbC&limit=200"}}`

const workflowsPage = `{"data":[
{"type":"ciWorkflows","id":"wf-pr","attributes":{"name":"Pull Request","isEnabled":true}},
{"type":"ciWorkflows","id":"wf-release","attributes":{"name":"Release","isEnabled":false}}]}`

const buildRunsPage = `{"data":[
{"type":"ciBuildRuns","id":"run-245","attributes":{"number":245,"createdDate":"2026-09-04T10:41:00Z","startedDate":"2026-09-04T10:42:00Z","finishedDate":null,
 "sourceCommit":{"commitSha":"a1b2c3d4e5f6","message":"Fix login"},"destinationCommit":{"commitSha":"d4e5f6a7b8c9"},"isPullRequestBuild":true,
 "issueCounts":{"analyzerWarnings":0,"errors":0,"testFailures":0,"warnings":2},"executionProgress":"RUNNING","completionStatus":null,"startReason":"PULL_REQUEST_UPDATE"},
 "relationships":{"workflow":{"data":{"type":"ciWorkflows","id":"wf-pr"}},"sourceBranchOrTag":{"data":{"type":"scmGitReferences","id":"ref-feature"}},"destinationBranch":{"data":{"type":"scmGitReferences","id":"ref-main"}}}},
{"type":"ciBuildRuns","id":"run-244","attributes":{"number":244,"createdDate":"2026-09-04T09:00:00Z","startedDate":"2026-09-04T09:00:10Z","finishedDate":"2026-09-04T09:12:18Z",
 "sourceCommit":{"commitSha":"ffff0000aaaa"},"isPullRequestBuild":false,"issueCounts":{"analyzerWarnings":0,"errors":1,"testFailures":1,"warnings":0},
 "executionProgress":"COMPLETE","completionStatus":"FAILED","startReason":"GIT_REF_CHANGE"},
 "relationships":{"workflow":{"data":{"type":"ciWorkflows","id":"wf-main"}},"sourceBranchOrTag":{"data":{"type":"scmGitReferences","id":"ref-main"}},"destinationBranch":{"data":null}}},
{"type":"ciBuildRuns","id":"run-243","attributes":{"number":243,"executionProgress":"COMPLETE","completionStatus":"CANCELED","startReason":"MANUAL","cancelReason":"MANUALLY_BY_USER"},
 "relationships":{"workflow":{"data":{"type":"ciWorkflows","id":"wf-release"}}}},
{"type":"ciBuildRuns","id":"run-242","attributes":{"number":242,"executionProgress":"PENDING"},"relationships":{}},
{"type":"ciBuildRuns","id":"run-241","attributes":{"number":241,"executionProgress":"COMPLETE","completionStatus":"TELEPORTED"},"relationships":{}}],
"included":[
{"type":"ciWorkflows","id":"wf-pr","attributes":{"name":"Pull Request","isEnabled":true}},
{"type":"ciWorkflows","id":"wf-main","attributes":{"name":"Main","isEnabled":true}},
{"type":"scmGitReferences","id":"ref-feature","attributes":{"canonicalName":"refs/heads/feature/example","name":"feature/example","kind":"BRANCH"}},
{"type":"scmGitReferences","id":"ref-main","attributes":{"canonicalName":"refs/heads/main","name":"main","kind":"BRANCH"}}],
"links":{"self":"{{base}}/v1/ciProducts/prod-1/buildRuns?limit=25&sort=-number","next":"{{base}}/v1/ciProducts/prod-1/buildRuns?cursor=NEXT&limit=25&sort=-number"}}`

const runDetail = `{"data":{"type":"ciBuildRuns","id":"run-244","attributes":{"number":244,"createdDate":"2026-09-04T09:00:00Z","startedDate":"2026-09-04T09:00:10Z","finishedDate":"2026-09-04T09:12:18Z",
 "sourceCommit":{"commitSha":"ffff0000aaaa"},"executionProgress":"COMPLETE","completionStatus":"FAILED","startReason":"GIT_REF_CHANGE"},
 "relationships":{"workflow":{"data":{"type":"ciWorkflows","id":"wf-main"}},"sourceBranchOrTag":{"data":{"type":"scmGitReferences","id":"ref-main"}}}},
"included":[{"type":"ciWorkflows","id":"wf-main","attributes":{"name":"Main"}},{"type":"scmGitReferences","id":"ref-main","attributes":{"name":"main","kind":"BRANCH"}}]}`

const actionsPageOne = `{"data":[
{"type":"ciBuildActions","id":"act-build","attributes":{"name":"Build and Analyze","actionType":"BUILD","startedDate":"2026-09-04T09:00:10Z","finishedDate":"2026-09-04T09:02:24Z",
 "issueCounts":{"errors":0,"warnings":1},"executionProgress":"COMPLETE","completionStatus":"SUCCEEDED","isRequiredToPass":true}}],
"links":{"next":"{{base}}/v1/ciBuildRuns/run-244/actions/page-two?cursor=X"}}`

const actionsPageTwo = `{"data":[
{"type":"ciBuildActions","id":"act-test","attributes":{"name":"Test","actionType":"TEST","startedDate":"2026-09-04T09:02:30Z","finishedDate":"2026-09-04T09:11:11Z",
 "issueCounts":{"errors":1,"testFailures":1},"executionProgress":"COMPLETE","completionStatus":"FAILED","isRequiredToPass":true}},
{"type":"ciBuildActions","id":"act-archive","attributes":{"name":"Archive","actionType":"ARCHIVE","executionProgress":"COMPLETE","completionStatus":"SKIPPED"}}]}`

const buildIssues = `{"data":[{"type":"ciIssues","id":"issue-1","attributes":{"issueType":"WARNING","message":"value was never used","category":"Swift Compiler Warning","fileSource":{"path":"/Volumes/workspace/repository/App/App.swift","lineNumber":18}}}]}`

const testIssues = `{"data":[{"type":"ciIssues","id":"issue-2","attributes":{"issueType":"TEST_FAILURE","message":"XCTAssertTrue failed","fileSource":{"path":"AppTests/LoginTests.swift","lineNumber":42}}}]}`

const testResults = `{"data":[
{"type":"ciTestResults","id":"test-1","attributes":{"className":"LoginTests","name":"testValidLogin","status":"SUCCESS","destinationTestResults":[{"deviceName":"iPhone 17 Pro","osVersion":"26.0","status":"SUCCESS","duration":0.4}]}},
{"type":"ciTestResults","id":"test-2","attributes":{"className":"LoginTests","name":"testInvalidLogin","status":"FAILURE","message":"XCTAssertTrue failed","fileSource":{"path":"AppTests/LoginTests.swift","lineNumber":42},
 "destinationTestResults":[{"deviceName":"iPhone 17 Pro","osVersion":"26.0","status":"FAILURE","duration":1.3}]}}]}`

const testArtifacts = `{"data":[
{"type":"ciArtifacts","id":"art-log","attributes":{"fileType":"LOG_BUNDLE","fileName":"Logs.zip","fileSize":1887436,"downloadUrl":"{{base}}/download/Logs.zip"}},
{"type":"ciArtifacts","id":"art-result","attributes":{"fileType":"RESULT_BUNDLE","fileName":"TestResults.xcresult.zip","fileSize":148897792,"downloadUrl":"{{base}}/download/TestResults.zip"}}]}`

const emptyCollection = `{"data":[]}`

func installDetailFixtures(server *testServer) {
	server.json("/v1/ciBuildRuns/run-244", runDetail)
	server.json("/v1/ciBuildRuns/run-244/actions", actionsPageOne)
	server.json("/v1/ciBuildRuns/run-244/actions/page-two", actionsPageTwo)
	server.json("/v1/ciBuildActions/act-build/issues", buildIssues)
	server.json("/v1/ciBuildActions/act-build/artifacts", emptyCollection)
	server.json("/v1/ciBuildActions/act-test/issues", testIssues)
	server.json("/v1/ciBuildActions/act-test/testResults", testResults)
	server.json("/v1/ciBuildActions/act-test/artifacts", testArtifacts)
	server.json("/v1/ciBuildActions/act-archive/issues", emptyCollection)
	server.json("/v1/ciBuildActions/act-archive/artifacts", emptyCollection)
}

func TestListProductsFollowsPaginationLinksWithReadOnlyRequests(t *testing.T) {
	server := newTestServer(t)
	server.json("/v1/ciProducts", productsPageOne)
	server.json("/v1/ciProducts/page-two", productsPageTwo)
	client, _ := server.client(t)
	products, err := client.ListProducts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 2 || products[0].ID != "prod-1" || products[0].Name != "Fortyfive" || products[1].Name != "Widgets" {
		t.Fatalf("products = %#v", products)
	}
	if len(server.requests) != 2 {
		t.Fatalf("requests = %d", len(server.requests))
	}
	for _, request := range server.requests {
		if request.Method != http.MethodGet {
			t.Fatalf("non-GET request %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("headers = %v", request.Header)
		}
	}
	if server.requests[1].URL.Query().Get("cursor") != "AbC" {
		t.Fatalf("second page URL = %s", server.requests[1].URL)
	}
}

func TestListWorkflowsMapsDisabledState(t *testing.T) {
	server := newTestServer(t)
	server.json("/v1/ciProducts/prod-1/workflows", workflowsPage)
	client, _ := server.client(t)
	workflows, err := client.ListWorkflows(context.Background(), "prod-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(workflows) != 2 || workflows[0].Name != "Pull Request" || workflows[0].Disabled || !workflows[1].Disabled {
		t.Fatalf("workflows = %#v", workflows)
	}
}

func TestListBuildRunsMapsRelationshipsAndStatuses(t *testing.T) {
	server := newTestServer(t)
	server.json("/v1/ciProducts/prod-1/buildRuns", buildRunsPage)
	client, _ := server.client(t)
	page, err := client.ListBuildRuns(context.Background(), BuildRunQuery{ProductID: "prod-1"})
	if err != nil {
		t.Fatal(err)
	}
	query := server.requests[0].URL.Query()
	if query.Get("sort") != "-number" || query.Get("limit") != "25" || !strings.Contains(query.Get("include"), "workflow") {
		t.Fatalf("query = %v", query)
	}
	if !strings.Contains(page.NextCursor, "cursor=NEXT") {
		t.Fatalf("next cursor = %q", page.NextCursor)
	}
	if len(page.Runs) != 5 {
		t.Fatalf("runs = %d", len(page.Runs))
	}
	active := page.Runs[0]
	if active.Number != 245 || active.Status() != StatusRunning || active.WorkflowName != "Pull Request" || active.SourceBranch != "feature/example" ||
		active.DestinationBranch != "main" || active.SourceCommit != "a1b2c3d4e5f6" || active.DestinationCommit != "d4e5f6a7b8c9" || !active.IsPullRequest ||
		active.WarningCount != 2 || active.CommitMessage != "Fix login" {
		t.Fatalf("active run = %#v", active)
	}
	failed := page.Runs[1]
	if failed.Status() != StatusFailed || failed.WorkflowName != "Main" || failed.SourceBranch != "main" || failed.DestinationBranch != "" || failed.ErrorCount != 1 || failed.TestFailureCount != 1 {
		t.Fatalf("failed run = %#v", failed)
	}
	if failed.Duration(time.Now()) != 12*time.Minute+8*time.Second {
		t.Fatalf("failed duration = %v", failed.Duration(time.Now()))
	}
	if page.Runs[2].Status() != StatusCancelled || page.Runs[2].WorkflowName != "" || page.Runs[2].WorkflowID != "wf-release" {
		t.Fatalf("cancelled run = %#v", page.Runs[2])
	}
	if page.Runs[3].Status() != StatusWaiting || page.Runs[4].Status() != StatusUnknown {
		t.Fatalf("pending/unknown statuses = %v %v", page.Runs[3].Status(), page.Runs[4].Status())
	}
}

func TestListBuildRunsUsesWorkflowRouteAndOpaqueCursor(t *testing.T) {
	server := newTestServer(t)
	server.json("/v1/ciWorkflows/wf-pr/buildRuns", buildRunsPage)
	server.json("/v1/ciProducts/prod-1/buildRuns", `{"data":[]}`)
	client, _ := server.client(t)
	if _, err := client.ListBuildRuns(context.Background(), BuildRunQuery{ProductID: "prod-1", WorkflowID: "wf-pr", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if server.requests[0].URL.Path != "/v1/ciWorkflows/wf-pr/buildRuns" || server.requests[0].URL.Query().Get("limit") != "10" {
		t.Fatalf("workflow request = %s", server.requests[0].URL)
	}
	page, err := client.ListBuildRuns(context.Background(), BuildRunQuery{Cursor: server.URL + "/v1/ciProducts/prod-1/buildRuns?cursor=NEXT&limit=25"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 0 || page.NextCursor != "" || server.requests[1].URL.Query().Get("cursor") != "NEXT" {
		t.Fatalf("cursor request = %s, page = %#v", server.requests[1].URL, page)
	}
	if _, err := client.ListBuildRuns(context.Background(), BuildRunQuery{Cursor: "https://evil.example/v1/ciProducts"}); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("foreign cursor error = %v", err)
	}
}

func TestGetBuildDetailsEnrichesActionsInApiOrder(t *testing.T) {
	server := newTestServer(t)
	installDetailFixtures(server)
	client, _ := server.client(t)
	details, err := client.GetBuildDetails(context.Background(), "run-244")
	if err != nil {
		t.Fatal(err)
	}
	if details.Run.Number != 244 || details.Run.WorkflowName != "Main" || details.Run.SourceBranch != "main" {
		t.Fatalf("run = %#v", details.Run)
	}
	names := make([]string, len(details.Actions))
	for i, action := range details.Actions {
		names[i] = action.Name
	}
	if strings.Join(names, ",") != "Build and Analyze,Test,Archive" {
		t.Fatalf("action order = %v", names)
	}
	build, test, archive := details.Actions[0], details.Actions[1], details.Actions[2]
	if build.Status() != StatusSucceeded || build.Duration(time.Now()) != 2*time.Minute+14*time.Second || len(build.Issues) != 1 || build.Issues[0].Severity() != "warning" || build.Issues[0].Line != 18 {
		t.Fatalf("build action = %#v", build)
	}
	if test.Status() != StatusFailed || len(test.Issues) != 1 || test.Issues[0].Severity() != "error" || len(test.TestResults) != 2 || len(test.Artifacts) != 2 {
		t.Fatalf("test action = %#v", test)
	}
	if test.TestResults[1].Passed() || test.TestResults[1].Duration() != 1300*time.Millisecond || test.TestResults[1].Destinations[0].Device != "iPhone 17 Pro" {
		t.Fatalf("failed test = %#v", test.TestResults[1])
	}
	if !test.Artifacts[0].IsLog() || test.Artifacts[0].ActionName != "Test" || test.Artifacts[1].Size != 148897792 || test.Artifacts[1].IsLog() {
		t.Fatalf("artifacts = %#v", test.Artifacts)
	}
	if archive.Status() != StatusSkipped || len(archive.TestResults) != 0 {
		t.Fatalf("archive action = %#v", archive)
	}
	if server.requestCount("/v1/ciBuildActions/act-build/testResults") != 0 {
		t.Fatal("test results were requested for a non-test action")
	}
}

func TestGetBuildDetailsStopsWhenCancelled(t *testing.T) {
	server := newTestServer(t)
	installDetailFixtures(server)
	started := make(chan struct{}, 1)
	server.route("/v1/ciBuildActions/act-build/issues", func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	})
	client, _ := server.client(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.GetBuildDetails(ctx, "run-244")
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetBuildDetails did not stop after cancellation")
	}
}

func TestClientRetriesTransientFailuresAndHonoursRetryAfter(t *testing.T) {
	server := newTestServer(t)
	var calls atomic.Int32
	server.route("/v1/ciProducts", func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			http.Error(w, `{"errors":[{"status":"503","title":"Service Unavailable"}]}`, http.StatusServiceUnavailable)
		case 2:
			w.Header().Set("Retry-After", "2")
			http.Error(w, `{"errors":[{"status":"429","title":"Too Many Requests"}]}`, http.StatusTooManyRequests)
		default:
			_, _ = fmt.Fprint(w, strings.ReplaceAll(productsPageTwo, "{{base}}", server.URL))
		}
	})
	client, sleeps := server.client(t)
	products, err := client.ListProducts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || calls.Load() != 3 {
		t.Fatalf("products = %#v after %d calls", products, calls.Load())
	}
	if len(*sleeps) != 2 || (*sleeps)[0] < 500*time.Millisecond || (*sleeps)[0] > time.Second || (*sleeps)[1] != 2*time.Second {
		t.Fatalf("retry delays = %v", *sleeps)
	}
}

func TestClientGivesUpAfterBoundedTransientRetries(t *testing.T) {
	server := newTestServer(t)
	var calls atomic.Int32
	server.route("/v1/ciProducts", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, `{"errors":[{"status":"500","title":"Internal Server Error"}]}`, http.StatusInternalServerError)
	})
	client, _ := server.client(t, WithMaxAttempts(3))
	_, err := client.ListProducts(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 500 || calls.Load() != 3 {
		t.Fatalf("error = %v after %d calls", err, calls.Load())
	}
}

func TestClientDoesNotRetryAuthenticationOrPermissionFailures(t *testing.T) {
	server := newTestServer(t)
	server.route("/v1/ciProducts", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errors":[{"status":"401","code":"NOT_AUTHORIZED","title":"Authentication credentials are missing or invalid.","detail":"Provide a properly configured and signed bearer token"}]}`, http.StatusUnauthorized)
	})
	server.route("/v1/ciProducts/prod-1/workflows", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errors":[{"status":"403","code":"FORBIDDEN_ERROR","title":"This request is forbidden for security reasons"}]}`, http.StatusForbidden)
	})
	client, sleeps := server.client(t)
	_, err := client.ListProducts(context.Background())
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "Authentication credentials") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("401 error = %v", err)
	}
	if _, err := client.ListWorkflows(context.Background(), "prod-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("403 error = %v", err)
	}
	if _, err := client.GetBuildDetails(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 error = %v", err)
	}
	if len(server.requests) != 3 || len(*sleeps) != 0 {
		t.Fatalf("requests = %d, sleeps = %v", len(server.requests), *sleeps)
	}
}

func TestClientRefusesForeignPaginationLinksAndMalformedJSON(t *testing.T) {
	server := newTestServer(t)
	server.json("/v1/ciProducts", `{"data":[],"links":{"next":"https://attacker.example/v1/ciProducts?cursor=1"}}`)
	server.json("/v1/ciProducts/prod-1/workflows", `{"data": [`)
	client, _ := server.client(t)
	if _, err := client.ListProducts(context.Background()); err == nil || !strings.Contains(err.Error(), "attacker.example") {
		t.Fatalf("foreign link error = %v", err)
	}
	if len(server.requests) != 1 {
		t.Fatalf("token was sent to %d hosts", len(server.requests))
	}
	if _, err := client.ListWorkflows(context.Background(), "prod-1"); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("malformed JSON error = %v", err)
	}
}

func TestClientReportsTimeoutsWithoutRetryingAfterCancellation(t *testing.T) {
	server := newTestServer(t)
	server.route("/v1/ciProducts", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	client, sleeps := server.client(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.ListProducts(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || len(*sleeps) != 0 {
		t.Fatalf("timeout error = %v, sleeps = %v", err, *sleeps)
	}
}

func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestDownloadArtifactStreamsAndFinalizesAtomically(t *testing.T) {
	server := newTestServer(t)
	payload := strings.Repeat("log line\n", 1000)
	server.route("/download/Logs.zip", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("bearer token was sent to the artifact host")
		}
		_, _ = fmt.Fprint(w, payload)
	})
	client, _ := server.client(t)
	destination := filepath.Join(t.TempDir(), "run", "artifacts", "Logs.zip")
	var progress []int64
	artifact := Artifact{Name: "Logs.zip", Size: int64(len(payload)), DownloadURL: server.URL + "/download/Logs.zip"}
	if err := client.DownloadArtifact(context.Background(), artifact, destination, func(n int64) { progress = append(progress, n) }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != payload {
		t.Fatalf("downloaded content mismatch: %v", err)
	}
	info, _ := os.Stat(destination)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode = %v", info.Mode().Perm())
	}
	if len(progress) == 0 || progress[len(progress)-1] != int64(len(payload)) {
		t.Fatalf("progress = %v", progress)
	}
	if files := tempFiles(t, filepath.Dir(destination)); len(files) != 0 {
		t.Fatalf("temporary files remain: %v", files)
	}
	if err := client.DownloadArtifact(context.Background(), artifact, destination, nil); err != nil {
		t.Fatal(err)
	}
	if server.requestCount("/download/Logs.zip") != 1 {
		t.Fatal("matching artifact was downloaded again")
	}
}

func TestDownloadArtifactRejectsShortResponsesAndExpiredLinks(t *testing.T) {
	server := newTestServer(t)
	server.route("/download/short", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "partial") })
	server.route("/download/expired", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "expired", http.StatusForbidden) })
	client, _ := server.client(t, WithMaxAttempts(2))
	dir := t.TempDir()
	short := Artifact{Name: "short.bin", Size: 100, DownloadURL: server.URL + "/download/short"}
	err := client.DownloadArtifact(context.Background(), short, filepath.Join(dir, "short.bin"), nil)
	if err == nil || !strings.Contains(err.Error(), "received 7 of 100 bytes") {
		t.Fatalf("short download error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "short.bin")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("short download left a destination file")
	}
	if files := tempFiles(t, dir); len(files) != 0 {
		t.Fatalf("temporary files remain: %v", files)
	}
	expired := Artifact{Name: "expired.bin", DownloadURL: server.URL + "/download/expired"}
	if err := client.DownloadArtifact(context.Background(), expired, filepath.Join(dir, "expired.bin"), nil); !errors.Is(err, ErrArtifactExpired) {
		t.Fatalf("expired link error = %v", err)
	}
	insecure := Artifact{Name: "x", DownloadURL: "ftp://example.com/x"}
	if err := client.DownloadArtifact(context.Background(), insecure, filepath.Join(dir, "x"), nil); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("insecure scheme error = %v", err)
	}
}

func TestDownloadArtifactCleansUpAfterCancellation(t *testing.T) {
	server := newTestServer(t)
	started := make(chan struct{}, 1)
	server.route("/download/slow", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "first chunk")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	})
	client, _ := server.client(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.DownloadArtifact(ctx, Artifact{Name: "slow.bin", Size: 1000, DownloadURL: server.URL + "/download/slow"}, filepath.Join(dir, "slow.bin"), nil)
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download did not stop after cancellation")
	}
	if files := tempFiles(t, dir); len(files) != 0 {
		t.Fatalf("temporary files remain after cancellation: %v", files)
	}
}

func TestNormalizeStatusCoversKnownCombinations(t *testing.T) {
	cases := []struct {
		progress, completion string
		want                 Status
	}{
		{"PENDING", "", StatusWaiting},
		{"RUNNING", "", StatusRunning},
		{"COMPLETE", "SUCCEEDED", StatusSucceeded},
		{"COMPLETE", "FAILED", StatusFailed},
		{"COMPLETE", "ERRORED", StatusFailed},
		{"COMPLETE", "CANCELED", StatusCancelled},
		{"COMPLETE", "SKIPPED", StatusSkipped},
		{"COMPLETE", "SOMETHING_NEW", StatusUnknown},
		{"", "", StatusUnknown},
	}
	for _, c := range cases {
		if got := NormalizeStatus(c.progress, c.completion); got != c.want {
			t.Fatalf("NormalizeStatus(%q, %q) = %v, want %v", c.progress, c.completion, got, c.want)
		}
	}
	if !StatusRunning.Active() || StatusFailed.Active() {
		t.Fatal("Active() misclassified statuses")
	}
}

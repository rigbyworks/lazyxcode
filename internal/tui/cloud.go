package tui

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jesseduffield/gocui"
	"github.com/rigbyworks/lazyxcode/internal/xcodecloud"
)

type appMode int

const (
	modeLocal appMode = iota
	modeCloud
)

const (
	cloudRunPageSize        = 25
	cloudActivePollInterval = 15 * time.Second
	cloudIdlePollInterval   = 60 * time.Second
	cloudProgressStep       = 1 << 20
	cloudMaxLogBytes        = 16 << 20
	cloudMaxLogEntryBytes   = 8 << 20
)

const (
	rawLogLoading = "loading"
	rawLogReady   = "ready"
	rawLogEmpty   = "empty"
	rawLogFailed  = "failed"
)

type cloudRawLog struct {
	state       string
	text        string
	paths       []string
	err         string
	artifactKey string
	progress    int64
	total       int64
}

type cloudDownload struct {
	runID    string
	artifact xcodecloud.Artifact
	path     string
	written  int64
	cancel   context.CancelFunc
}

// cloudState owns everything Cloud mode needs. Local fields on App stay
// untouched so that switching modes never interrupts local activity.
type cloudState struct {
	service  xcodecloud.Service
	setupErr error
	warnings []string

	products        []xcodecloud.Product
	product         int
	workflows       []xcodecloud.Workflow
	workflow        int
	workflowsLoaded bool

	runs       []xcodecloud.BuildRun
	runIndex   int
	nextCursor string
	paged      bool
	advanceRun string

	details       map[string]xcodecloud.BuildDetails
	detailErrors  map[string]string
	detailLoading string
	rawLogs       map[string]*cloudRawLog

	loading     string
	loadingMore bool
	announce    bool
	err         string
	stale       bool
	lastRefresh time.Time
	verbose     bool
	configRow   int
	resetOutput bool
	savedOrigin int

	generation       uint64
	detailGeneration uint64
	cancelRuns       context.CancelFunc
	cancelDetails    context.CancelFunc
	cancelLogs       map[string]context.CancelFunc
	download         *cloudDownload
}

func newCloudState() *cloudState {
	return &cloudState{
		product: -1, workflow: -1,
		details: map[string]xcodecloud.BuildDetails{}, detailErrors: map[string]string{},
		rawLogs: map[string]*cloudRawLog{}, cancelLogs: map[string]context.CancelFunc{},
	}
}

func (c *cloudState) cancelAll() {
	c.cancelRequests()
	for id, cancel := range c.cancelLogs {
		cancel()
		delete(c.cancelLogs, id)
	}
	if c.download != nil {
		c.download.cancel()
	}
}

func (c *cloudState) cancelRequests() {
	if c.cancelRuns != nil {
		c.cancelRuns()
		c.cancelRuns = nil
	}
	if c.cancelDetails != nil {
		c.cancelDetails()
		c.cancelDetails = nil
	}
	c.detailLoading = ""
}

func (c *cloudState) selectedRun() (xcodecloud.BuildRun, bool) {
	if len(c.runs) == 0 || c.runIndex < 0 || c.runIndex >= len(c.runs) {
		return xcodecloud.BuildRun{}, false
	}
	return c.runs[c.runIndex], true
}

func (c *cloudState) selectedProduct() (xcodecloud.Product, bool) {
	if c.product < 0 || c.product >= len(c.products) {
		return xcodecloud.Product{}, false
	}
	return c.products[c.product], true
}

func (c *cloudState) selectedWorkflow() (xcodecloud.Workflow, bool) {
	if c.workflow < 0 || c.workflow >= len(c.workflows) {
		return xcodecloud.Workflow{}, false
	}
	return c.workflows[c.workflow], true
}

func (c *cloudState) hasActiveRuns() bool {
	for _, run := range c.runs {
		if run.Status().Active() {
			return true
		}
	}
	return false
}

func (c *cloudState) pollInterval() time.Duration {
	if c.hasActiveRuns() {
		return cloudActivePollInterval
	}
	return cloudIdlePollInterval
}

func (a *App) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *App) baseContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) connectCloud() (xcodecloud.Service, []string, error) {
	if a.cloudConnect != nil {
		return a.cloudConnect()
	}
	client, warnings, err := xcodecloud.Connect(os.Getenv)
	if err != nil {
		return nil, warnings, err
	}
	return client, warnings, nil
}

// toggleMode switches the three panes between Local and Cloud without
// touching local builds or their event processing.
func (a *App) toggleMode(g *gocui.Gui, _ *gocui.View) error {
	var output *gocui.View
	if g != nil {
		output, _ = g.View("output")
	}
	if a.mode == modeLocal {
		if output != nil {
			_, a.localOutputOrigin = output.Origin()
		}
		a.mode = modeCloud
		a.enterCloud()
		if output != nil && a.cloud != nil {
			output.SetOrigin(0, a.cloud.savedOrigin)
		}
		if a.cloud != nil && a.cloud.setupErr == nil && a.cloud.loading == "" {
			a.status = a.cloudSummaryStatus()
		}
		return nil
	}
	if output != nil && a.cloud != nil {
		_, a.cloud.savedOrigin = output.Origin()
	}
	a.mode = modeLocal
	if output != nil {
		output.SetOrigin(0, a.localOutputOrigin)
	}
	a.status = "Local mode"
	return nil
}

func (a *App) enterCloud() {
	if a.cloud == nil {
		a.cloud = newCloudState()
		service, warnings, err := a.connectCloud()
		a.cloud.service, a.cloud.warnings, a.cloud.setupErr = service, warnings, err
		if err != nil {
			a.status = cloudErrorMessage(err)
			return
		}
		if len(warnings) > 0 {
			a.status = "Warning: " + warnings[0]
		}
		a.loadCloudProducts()
		return
	}
	c := a.cloud
	if c.setupErr != nil || c.service == nil {
		a.status = cloudErrorMessage(c.setupErr)
		return
	}
	if c.product < 0 && c.loading == "" && a.overlay == nil {
		a.loadCloudProducts()
	}
}

func (a *App) cloudSummaryStatus() string {
	c := a.cloud
	if c == nil {
		return ""
	}
	if c.err != "" {
		return c.err
	}
	product, ok := c.selectedProduct()
	if !ok {
		return "Cloud mode - select a product"
	}
	active := 0
	for _, run := range c.runs {
		if run.Status().Active() {
			active++
		}
	}
	return fmt.Sprintf("Cloud - %s: %d runs, %d active (read-only)", product.Name, len(c.runs), active)
}

func (a *App) rememberedCloudProduct() string {
	if a.preferences == nil {
		return ""
	}
	return a.preferences.CloudProduct(a.container.Path)
}

func (a *App) rememberedCloudWorkflow() string {
	if a.preferences == nil {
		return ""
	}
	return a.preferences.CloudWorkflow(a.container.Path)
}

func (a *App) rememberCloudProduct(id string) {
	if a.preferences != nil {
		_ = a.preferences.SetCloudProduct(a.container.Path, id)
	}
}

func (a *App) rememberCloudWorkflow(id string) {
	if a.preferences != nil {
		_ = a.preferences.SetCloudWorkflow(a.container.Path, id)
	}
}

// resetCloudSelection forgets the product and workflow choice after the local
// container changes, because cloud selections are remembered per container.
func (a *App) resetCloudSelection() {
	c := a.cloud
	if c == nil || c.service == nil {
		return
	}
	c.cancelAll()
	c.generation++
	c.detailGeneration++
	c.product, c.workflows, c.workflow, c.workflowsLoaded = -1, nil, -1, false
	c.runs, c.runIndex, c.nextCursor, c.paged = nil, 0, "", false
	c.details, c.detailErrors, c.rawLogs = map[string]xcodecloud.BuildDetails{}, map[string]string{}, map[string]*cloudRawLog{}
	c.loading, c.loadingMore, c.err, c.stale, c.download = "", false, "", false, nil
	if a.mode == modeCloud {
		a.loadCloudProducts()
	}
}

func (a *App) loadCloudProducts() {
	c := a.cloud
	if c == nil || c.service == nil {
		return
	}
	c.cancelRequests()
	c.generation++
	generation := c.generation
	ctx, cancel := context.WithCancel(a.baseContext())
	c.cancelRuns = cancel
	c.loading, c.err = "Loading Xcode Cloud products...", ""
	a.status = c.loading
	service := c.service
	go func() {
		products, err := service.ListProducts(ctx)
		cancel()
		a.update(func() { a.applyCloudProducts(generation, products, err) })
	}()
}

func (a *App) applyCloudProducts(generation uint64, products []xcodecloud.Product, err error) {
	c := a.cloud
	if c == nil || generation != c.generation {
		return
	}
	c.loading = ""
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.err = cloudErrorMessage(err)
		a.status = c.err
		return
	}
	c.products = products
	if len(products) == 0 {
		c.err = "No Xcode Cloud products are visible to this API key"
		a.status = c.err
		return
	}
	remembered := a.rememberedCloudProduct()
	index := cloudProductIndex(products, remembered)
	if index < 0 && remembered != "" {
		a.rememberCloudProduct("")
	}
	if index < 0 && len(products) == 1 {
		index = 0
	}
	if index >= 0 {
		a.selectCloudProduct(index)
		return
	}
	a.openCloudProductPicker()
	a.status = "Select an Xcode Cloud product"
}

func cloudProductIndex(products []xcodecloud.Product, id string) int {
	if id == "" {
		return -1
	}
	for i := range products {
		if products[i].ID == id {
			return i
		}
	}
	return -1
}

func cloudWorkflowIndex(workflows []xcodecloud.Workflow, id string) int {
	if id == "" {
		return -1
	}
	for i := range workflows {
		if workflows[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *App) selectCloudProduct(index int) {
	c := a.cloud
	if c == nil || index < 0 || index >= len(c.products) {
		return
	}
	c.cancelAll()
	c.product = index
	a.rememberCloudProduct(c.products[index].ID)
	c.workflows, c.workflow, c.workflowsLoaded = nil, -1, false
	c.runs, c.runIndex, c.nextCursor, c.paged = nil, 0, "", false
	c.details, c.detailErrors, c.rawLogs = map[string]xcodecloud.BuildDetails{}, map[string]string{}, map[string]*cloudRawLog{}
	c.err, c.stale, c.lastRefresh = "", false, time.Time{}
	c.resetOutput = true
	a.loadCloudWorkflows()
}

func (a *App) loadCloudWorkflows() {
	c := a.cloud
	product, ok := c.selectedProduct()
	if !ok {
		return
	}
	c.cancelRequests()
	c.generation++
	generation := c.generation
	ctx, cancel := context.WithCancel(a.baseContext())
	c.cancelRuns = cancel
	c.loading = "Loading workflows for " + product.Name + "..."
	a.status = c.loading
	service := c.service
	go func() {
		workflows, err := service.ListWorkflows(ctx, product.ID)
		cancel()
		a.update(func() { a.applyCloudWorkflows(generation, workflows, err) })
	}()
}

func (a *App) applyCloudWorkflows(generation uint64, workflows []xcodecloud.Workflow, err error) {
	c := a.cloud
	if c == nil || generation != c.generation {
		return
	}
	c.loading = ""
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.err = cloudErrorMessage(err)
		a.status = c.err
		return
	}
	c.workflows, c.workflowsLoaded = workflows, true
	remembered := a.rememberedCloudWorkflow()
	c.workflow = cloudWorkflowIndex(workflows, remembered)
	if c.workflow < 0 && remembered != "" {
		a.rememberCloudWorkflow("")
	}
	c.announce = true
	a.loadCloudRuns(true)
}

func (a *App) selectCloudWorkflow(id string) {
	c := a.cloud
	if c == nil {
		return
	}
	c.workflow = cloudWorkflowIndex(c.workflows, id)
	if c.workflow < 0 {
		id = ""
	}
	a.rememberCloudWorkflow(id)
	c.announce = true
	c.resetOutput = true
	a.loadCloudRuns(true)
}

func (a *App) cloudRunQuery() xcodecloud.BuildRunQuery {
	c := a.cloud
	query := xcodecloud.BuildRunQuery{Limit: cloudRunPageSize}
	if product, ok := c.selectedProduct(); ok {
		query.ProductID = product.ID
	}
	if workflow, ok := c.selectedWorkflow(); ok {
		query.WorkflowID = workflow.ID
	}
	return query
}

// loadCloudRuns fetches the first page of runs. With reset it discards the
// current list and any in-flight request; otherwise it refreshes in place and
// never overlaps another refresh for the same selection.
func (a *App) loadCloudRuns(reset bool) {
	c := a.cloud
	if c == nil || c.service == nil || c.product < 0 {
		return
	}
	if !reset && (c.loading != "" || c.loadingMore) {
		return
	}
	if reset {
		c.cancelRequests()
		c.runs, c.runIndex, c.nextCursor, c.paged, c.loadingMore = nil, 0, "", false, false
	}
	c.generation++
	generation := c.generation
	ctx, cancel := context.WithCancel(a.baseContext())
	c.cancelRuns = cancel
	if len(c.runs) == 0 {
		c.loading = "Loading build runs..."
	} else {
		c.loading = "Refreshing build runs..."
	}
	if c.announce {
		a.status = c.loading
	}
	query := a.cloudRunQuery()
	service := c.service
	go func() {
		page, err := service.ListBuildRuns(ctx, query)
		cancel()
		a.update(func() { a.applyCloudRuns(generation, page, err, false) })
	}()
}

func (a *App) refreshCloud(*gocui.Gui, *gocui.View) error {
	c := a.cloud
	if c == nil || c.setupErr != nil || c.service == nil {
		a.cloud = nil
		a.enterCloud()
		return nil
	}
	if c.product < 0 {
		if c.loading == "" {
			a.loadCloudProducts()
		}
		return nil
	}
	if c.loading != "" || c.loadingMore {
		a.status = "A cloud request is already in progress"
		return nil
	}
	c.announce = true
	a.loadCloudRuns(false)
	return nil
}

func (a *App) loadMoreCloudRuns(advanceRun string) {
	c := a.cloud
	if c == nil || c.nextCursor == "" || c.loading != "" || c.loadingMore {
		return
	}
	c.loadingMore = true
	c.advanceRun = advanceRun
	generation := c.generation
	ctx, cancel := context.WithCancel(a.baseContext())
	previousCancel := c.cancelRuns
	c.cancelRuns = func() {
		cancel()
		if previousCancel != nil {
			previousCancel()
		}
	}
	a.status = "Loading older build runs..."
	query := xcodecloud.BuildRunQuery{Cursor: c.nextCursor}
	service := c.service
	go func() {
		page, err := service.ListBuildRuns(ctx, query)
		cancel()
		a.update(func() { a.applyCloudRuns(generation, page, err, true) })
	}()
}

func (a *App) loadOlderCloudRuns(*gocui.Gui, *gocui.View) error {
	c := a.cloud
	if c == nil || len(c.runs) == 0 {
		return nil
	}
	if c.nextCursor == "" {
		a.status = "No older build runs are available"
		return nil
	}
	advanceRun := ""
	if c.runIndex == len(c.runs)-1 {
		advanceRun = c.runs[c.runIndex].ID
	}
	a.loadMoreCloudRuns(advanceRun)
	return nil
}

func (a *App) applyCloudRuns(generation uint64, page xcodecloud.BuildRunPage, err error, more bool) {
	c := a.cloud
	if c == nil || generation != c.generation {
		return
	}
	if more {
		c.loadingMore = false
	} else {
		c.loading = ""
	}
	advanceRun := c.advanceRun
	c.advanceRun = ""
	announce := c.announce
	c.announce = false
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.err = cloudErrorMessage(err)
		c.stale = len(c.runs) > 0
		if !more {
			c.lastRefresh = a.clock()
		}
		a.status = c.err
		return
	}
	c.err, c.stale = "", false
	selectedID := ""
	if run, ok := c.selectedRun(); ok {
		selectedID = run.ID
	}
	if more {
		c.runs = mergeCloudRuns(c.runs, page.Runs, false)
		c.nextCursor, c.paged = page.NextCursor, true
	} else {
		c.runs = mergeCloudRuns(c.runs, page.Runs, true)
		// Keep the deeper cursor once older pages were loaded so a refresh
		// never rewinds pagination to the first page's continuation.
		if !c.paged {
			c.nextCursor = page.NextCursor
		}
		c.lastRefresh = a.clock()
	}
	if selectedID != "" {
		if index := cloudRunIndex(c.runs, selectedID); index >= 0 {
			c.runIndex = index
		}
	}
	if more && selectedID == advanceRun {
		if index := cloudRunIndex(c.runs, advanceRun); index >= 0 && index+1 < len(c.runs) {
			c.runIndex = index + 1
			c.resetOutput = true
		}
	}
	if len(c.runs) > 0 {
		c.runIndex = clamp(c.runIndex, 0, len(c.runs)-1)
	} else {
		c.runIndex = 0
	}
	if announce || a.status == "" || strings.HasPrefix(a.status, "Loading") || strings.HasPrefix(a.status, "Refreshing") {
		a.status = a.cloudSummaryStatus()
	}
	a.ensureCloudDetails(!more)
}

// mergeCloudRuns reconciles a fetched page with the rows already loaded.
// Rows are matched by run ID, so polling never duplicates a run, and rows
// beyond the fetched page are kept so pagination is never discarded.
func mergeCloudRuns(existing, page []xcodecloud.BuildRun, firstPage bool) []xcodecloud.BuildRun {
	result := make([]xcodecloud.BuildRun, 0, len(existing)+len(page))
	seen := map[string]bool{}
	if firstPage {
		for _, run := range page {
			if !seen[run.ID] {
				seen[run.ID] = true
				result = append(result, run)
			}
		}
		for _, run := range existing {
			if !seen[run.ID] {
				seen[run.ID] = true
				result = append(result, run)
			}
		}
	} else {
		updates := map[string]xcodecloud.BuildRun{}
		for _, run := range page {
			updates[run.ID] = run
		}
		for _, run := range existing {
			if updated, ok := updates[run.ID]; ok {
				run = updated
			}
			if !seen[run.ID] {
				seen[run.ID] = true
				result = append(result, run)
			}
		}
		for _, run := range page {
			if !seen[run.ID] {
				seen[run.ID] = true
				result = append(result, run)
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Number != result[j].Number {
			return result[i].Number > result[j].Number
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

func cloudRunIndex(runs []xcodecloud.BuildRun, id string) int {
	for i := range runs {
		if runs[i].ID == id {
			return i
		}
	}
	return -1
}

// cloudTick drives polling from the one-second UI ticker. It only runs while
// Cloud mode is visible and never overlaps an in-flight refresh.
func (a *App) cloudTick(now time.Time) {
	c := a.cloud
	if a.mode != modeCloud || c == nil || c.service == nil || c.product < 0 || !c.workflowsLoaded {
		return
	}
	if c.loading != "" || c.loadingMore {
		return
	}
	if c.lastRefresh.IsZero() || now.Sub(c.lastRefresh) >= c.pollInterval() {
		a.loadCloudRuns(false)
	}
}

func (a *App) moveCloudRun(delta int) {
	c := a.cloud
	if c == nil || len(c.runs) == 0 {
		return
	}
	next := c.runIndex + delta
	if next >= len(c.runs) && c.nextCursor != "" {
		a.loadMoreCloudRuns(c.runs[c.runIndex].ID)
	}
	next = clamp(next, 0, len(c.runs)-1)
	if next != c.runIndex {
		c.runIndex = next
		c.resetOutput = true
	}
	a.ensureCloudDetails(false)
}

func (a *App) moveCloudRunTo(last bool) {
	c := a.cloud
	if c == nil || len(c.runs) == 0 {
		return
	}
	next := 0
	if last {
		next = len(c.runs) - 1
	}
	if next != c.runIndex {
		c.runIndex = next
		c.resetOutput = true
	}
	a.ensureCloudDetails(false)
}

// ensureCloudDetails loads details for the selected run when they are missing.
// With refreshActive it also reloads details for a run that is still active.
func (a *App) ensureCloudDetails(refreshActive bool) {
	c := a.cloud
	run, ok := c.selectedRun()
	if !ok {
		return
	}
	if c.detailLoading == run.ID {
		return
	}
	details, have := c.details[run.ID]
	needsRefresh := refreshActive && (run.Status().Active() || details.Run.Status().Active())
	if have && !needsRefresh {
		if c.verbose {
			a.ensureCloudRawLog(run.ID)
		}
		return
	}
	if !have {
		if _, failed := c.detailErrors[run.ID]; failed && !refreshActive {
			return
		}
	}
	a.loadCloudDetails(run.ID)
}

func (a *App) loadCloudDetails(runID string) {
	c := a.cloud
	if c == nil || c.service == nil || runID == "" {
		return
	}
	if c.cancelDetails != nil {
		c.cancelDetails()
	}
	c.detailGeneration++
	generation := c.detailGeneration
	ctx, cancel := context.WithCancel(a.baseContext())
	c.cancelDetails = cancel
	c.detailLoading = runID
	service := c.service
	go func() {
		details, err := service.GetBuildDetails(ctx, runID)
		cancel()
		a.update(func() { a.applyCloudDetails(generation, runID, details, err) })
	}()
}

func (a *App) applyCloudDetails(generation uint64, runID string, details xcodecloud.BuildDetails, err error) {
	c := a.cloud
	if c == nil || generation != c.detailGeneration {
		return
	}
	c.detailLoading = ""
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.detailErrors[runID] = cloudErrorMessage(err)
		return
	}
	delete(c.detailErrors, runID)
	previous, had := c.details[runID]
	c.details[runID] = details
	if index := cloudRunIndex(c.runs, runID); index >= 0 {
		c.runs[index] = details.Run
	}
	if had && logArtifactKey(previous) != logArtifactKey(details) {
		a.invalidateCloudRawLog(runID)
	}
	if c.verbose {
		if run, ok := c.selectedRun(); ok && run.ID == runID {
			a.ensureCloudRawLog(runID)
		}
	}
}

func (a *App) invalidateCloudRawLog(runID string) {
	c := a.cloud
	if cancel, ok := c.cancelLogs[runID]; ok {
		cancel()
		delete(c.cancelLogs, runID)
	}
	delete(c.rawLogs, runID)
}

func logArtifacts(details xcodecloud.BuildDetails) []xcodecloud.Artifact {
	var result []xcodecloud.Artifact
	for _, action := range details.Actions {
		for _, artifact := range action.Artifacts {
			if artifact.IsLog() {
				result = append(result, artifact)
			}
		}
	}
	return result
}

func logArtifactKey(details xcodecloud.BuildDetails) string {
	var builder strings.Builder
	for _, artifact := range logArtifacts(details) {
		fmt.Fprintf(&builder, "%s:%d;", artifact.ID, artifact.Size)
	}
	return builder.String()
}

func (a *App) toggleCloudVerbosity(g *gocui.Gui, _ *gocui.View) error {
	a.testOutput = nil
	c := a.cloud
	if c == nil {
		return nil
	}
	c.verbose = !c.verbose
	c.resetOutput = true
	if c.verbose {
		a.status = "Raw output"
		if run, ok := c.selectedRun(); ok {
			a.ensureCloudRawLog(run.ID)
		}
	} else {
		a.status = "Concise output"
	}
	return nil
}

// ensureCloudRawLog downloads the run's log artifacts once and caches the
// rendered text for the rest of the process.
func (a *App) ensureCloudRawLog(runID string) {
	c := a.cloud
	if runID == "" || c.service == nil {
		return
	}
	if _, ok := c.rawLogs[runID]; ok {
		return
	}
	details, ok := c.details[runID]
	if !ok {
		return
	}
	artifacts := logArtifacts(details)
	key := logArtifactKey(details)
	if len(artifacts) == 0 {
		c.rawLogs[runID] = &cloudRawLog{state: rawLogEmpty, artifactKey: key}
		return
	}
	if a.project == nil {
		c.rawLogs[runID] = &cloudRawLog{state: rawLogFailed, err: "no project cache is available", artifactKey: key}
		return
	}
	var total int64
	for _, artifact := range artifacts {
		total += artifact.Size
	}
	entry := &cloudRawLog{state: rawLogLoading, artifactKey: key, total: total}
	c.rawLogs[runID] = entry
	ctx, cancel := context.WithCancel(a.baseContext())
	c.cancelLogs[runID] = cancel
	service, project := c.service, a.project
	go func() {
		defer cancel()
		var text strings.Builder
		var paths []string
		var failure error
		var completed int64
		for _, artifact := range artifacts {
			path, err := project.CloudArtifactPath(runID, artifact.ID, artifact.Name)
			if err == nil {
				base := completed
				err = downloadCloudArtifact(ctx, service, runID, artifact, path, a.throttledProgress(func(n int64) { entry.progress = base + n }))
			}
			if err != nil {
				failure = err
				break
			}
			completed += artifact.Size
			paths = append(paths, path)
			text.WriteString(renderLogArtifact(path, artifact))
		}
		a.update(func() { a.applyCloudRawLog(runID, entry, text.String(), paths, failure) })
	}()
}

func (a *App) applyCloudRawLog(runID string, entry *cloudRawLog, text string, paths []string, err error) {
	c := a.cloud
	if c == nil || c.rawLogs[runID] != entry {
		return
	}
	delete(c.cancelLogs, runID)
	entry.paths = paths
	if err != nil {
		if errors.Is(err, context.Canceled) {
			delete(c.rawLogs, runID)
			return
		}
		entry.state, entry.err = rawLogFailed, cloudErrorMessage(err)
		return
	}
	entry.state, entry.text = rawLogReady, text
	if run, ok := c.selectedRun(); ok && run.ID == runID && c.verbose {
		c.resetOutput = true
	}
}

// throttledProgress forwards byte counts to the UI loop at most once per
// megabyte so large downloads do not flood the event queue.
func (a *App) throttledProgress(apply func(int64)) func(int64) {
	var reported int64
	return func(n int64) {
		if n-reported < cloudProgressStep {
			return
		}
		reported = n
		a.update(func() { apply(n) })
	}
}

// downloadCloudArtifact downloads an artifact and, when the link has expired,
// refetches the run's metadata once before giving up.
func downloadCloudArtifact(ctx context.Context, service xcodecloud.Service, runID string, artifact xcodecloud.Artifact, path string, progress func(int64)) error {
	err := service.DownloadArtifact(ctx, artifact, path, progress)
	if !errors.Is(err, xcodecloud.ErrArtifactExpired) {
		return err
	}
	details, detailErr := service.GetBuildDetails(ctx, runID)
	if detailErr != nil {
		return fmt.Errorf("%w; refreshing metadata failed: %v", err, detailErr)
	}
	for _, action := range details.Actions {
		for _, fresh := range action.Artifacts {
			if fresh.ID == artifact.ID && fresh.DownloadURL != "" {
				return service.DownloadArtifact(ctx, fresh, path, progress)
			}
		}
	}
	return err
}

func renderLogArtifact(path string, artifact xcodecloud.Artifact) string {
	heading := "==== " + artifact.ActionName
	if artifact.ActionName == "" {
		heading = "===="
	}
	heading += " - " + artifact.Name + " ====\n"
	file, err := os.Open(path)
	if err != nil {
		return heading + "Unable to read downloaded artifact: " + err.Error() + "\n\n"
	}
	defer file.Close()
	header := make([]byte, 8192)
	n, _ := io.ReadFull(file, header)
	header = header[:n]
	if bytes.HasPrefix(header, []byte("PK\x03\x04")) {
		return heading + renderZipLog(path) + "\n"
	}
	if bytes.IndexByte(header, 0) >= 0 {
		return heading + fmt.Sprintf("%s is not a text log. Downloaded to %s; press a to manage artifacts.\n\n", artifact.Name, path)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return heading + "Unable to read downloaded artifact: " + err.Error() + "\n\n"
	}
	data, err := io.ReadAll(io.LimitReader(file, cloudMaxLogBytes))
	if err != nil {
		return heading + "Unable to read downloaded artifact: " + err.Error() + "\n\n"
	}
	text := string(data)
	if int64(len(data)) >= cloudMaxLogBytes {
		text += "\n[log truncated]\n"
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return heading + text + "\n"
}

func renderZipLog(path string) string {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "Unable to open log archive: " + err.Error() + "\n"
	}
	defer reader.Close()
	entries := make([]*zip.File, 0, len(reader.File))
	for _, entry := range reader.File {
		if !entry.FileInfo().IsDir() && isTextualLogName(entry.Name) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	if len(entries) == 0 {
		return fmt.Sprintf("Log archive contains no text logs. Downloaded to %s; press a to manage artifacts.\n", path)
	}
	var builder strings.Builder
	var total int64
	for _, entry := range entries {
		builder.WriteString("---- " + entry.Name + " ----\n")
		if entry.UncompressedSize64 > cloudMaxLogEntryBytes {
			builder.WriteString("[entry too large to display]\n\n")
			continue
		}
		content, err := entry.Open()
		if err != nil {
			builder.WriteString("[unable to read entry: " + err.Error() + "]\n\n")
			continue
		}
		data, err := io.ReadAll(io.LimitReader(content, cloudMaxLogEntryBytes))
		content.Close()
		if err != nil {
			builder.WriteString("[unable to read entry: " + err.Error() + "]\n\n")
			continue
		}
		if bytes.IndexByte(data[:min(len(data), 1024)], 0) >= 0 {
			builder.WriteString("[binary entry skipped]\n\n")
			continue
		}
		total += int64(len(data))
		builder.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
		if total >= cloudMaxLogBytes {
			builder.WriteString("[log truncated]\n")
			break
		}
	}
	return builder.String()
}

func isTextualLogName(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".log", ".txt", ".md", ".json", ".xml", ".plist"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return !strings.Contains(lower[strings.LastIndex(lower, "/")+1:], ".")
}

func (a *App) openCloudConfigPicker(*gocui.Gui, *gocui.View) error {
	c := a.cloud
	if c != nil && c.configRow == 2 {
		return a.showContextDetails(nil, nil)
	}
	if c == nil || c.setupErr != nil || c.service == nil {
		a.status = cloudErrorMessage(cloudSetupError(c))
		return nil
	}
	if c.configRow == 0 {
		a.openCloudProductPicker()
	} else {
		a.openCloudWorkflowPicker()
	}
	return nil
}

func cloudSetupError(c *cloudState) error {
	if c == nil {
		return xcodecloud.ErrNotConfigured
	}
	if c.setupErr != nil {
		return c.setupErr
	}
	return xcodecloud.ErrNotConfigured
}

func (a *App) openCloudProductPicker() {
	c := a.cloud
	if len(c.products) == 0 {
		if c.loading == "" {
			a.loadCloudProducts()
		}
		return
	}
	items := make([]overlayItem, len(c.products))
	for i, product := range c.products {
		label := product.Name
		if product.Type != "" {
			label += "  [" + strings.ToLower(product.Type) + "]"
		}
		items[i] = overlayItem{ID: product.ID, Label: label}
	}
	a.overlay = &overlayState{kind: "cloud-product", title: "Select Xcode Cloud Product", items: items, selected: max(0, c.product)}
}

func (a *App) openCloudWorkflowPicker() {
	c := a.cloud
	if c.product < 0 {
		a.status = "Select a product first"
		return
	}
	if !c.workflowsLoaded {
		a.status = "Workflows are still loading"
		return
	}
	items := make([]overlayItem, 0, len(c.workflows)+1)
	items = append(items, overlayItem{ID: "", Label: "All Workflows"})
	for _, workflow := range c.workflows {
		label := workflow.Name
		if workflow.Disabled {
			label += "  [disabled]"
		}
		items = append(items, overlayItem{ID: workflow.ID, Label: label})
	}
	a.overlay = &overlayState{kind: "cloud-workflow", title: "Filter Workflow", items: items, selected: c.workflow + 1}
}

func (a *App) openCloudArtifactPicker(*gocui.Gui, *gocui.View) error {
	c := a.cloud
	if c == nil || c.service == nil {
		a.status = cloudErrorMessage(cloudSetupError(c))
		return nil
	}
	run, ok := c.selectedRun()
	if !ok {
		a.status = "Select a build run first"
		return nil
	}
	details, ok := c.details[run.ID]
	if !ok {
		if message, failed := c.detailErrors[run.ID]; failed {
			a.status = "Build details unavailable: " + message
		} else {
			a.status = "Build details are still loading"
		}
		return nil
	}
	var items []overlayItem
	for _, action := range details.Actions {
		for _, artifact := range action.Artifacts {
			items = append(items, overlayItem{ID: artifact.ID, Label: fmt.Sprintf("%-20s %-36s %10s", truncate(action.Name, 20), truncate(artifact.Name, 36), formatBytes(artifact.Size))})
		}
	}
	if len(items) == 0 {
		a.status = fmt.Sprintf("Build #%d has no artifacts", run.Number)
		return nil
	}
	a.overlay = &overlayState{kind: "cloud-artifact", title: fmt.Sprintf("Download Artifact - #%d", run.Number), items: items}
	return nil
}

func (a *App) downloadSelectedCloudArtifact(artifactID string) {
	c := a.cloud
	run, ok := c.selectedRun()
	if !ok {
		return
	}
	details := c.details[run.ID]
	var artifact xcodecloud.Artifact
	found := false
	for _, action := range details.Actions {
		for _, candidate := range action.Artifacts {
			if candidate.ID == artifactID {
				artifact, found = candidate, true
			}
		}
	}
	if !found {
		a.status = "Artifact is no longer available"
		return
	}
	if c.download != nil {
		a.status = "Wait for the current download to finish or press x to cancel it"
		return
	}
	if a.project == nil {
		a.status = "No project cache is available for downloads"
		return
	}
	path, err := a.project.CloudArtifactPath(run.ID, artifact.ID, artifact.Name)
	if err != nil {
		a.status = "Download failed: " + err.Error()
		return
	}
	ctx, cancel := context.WithCancel(a.baseContext())
	download := &cloudDownload{runID: run.ID, artifact: artifact, path: path, cancel: cancel}
	c.download = download
	a.status = "Downloading " + artifact.Name + "..."
	service := c.service
	go func() {
		defer cancel()
		err := downloadCloudArtifact(ctx, service, run.ID, artifact, path, a.throttledProgress(func(n int64) { download.written = n }))
		a.update(func() { a.applyCloudDownload(download, err) })
	}()
}

func (a *App) applyCloudDownload(download *cloudDownload, err error) {
	c := a.cloud
	if c == nil || c.download != download {
		return
	}
	c.download = nil
	switch {
	case err == nil:
		a.status = "Saved " + download.path
	case errors.Is(err, context.Canceled):
		a.status = "Download cancelled: " + download.artifact.Name
	default:
		a.status = "Download failed: " + cloudErrorMessage(err)
	}
}

func (a *App) cancelCloudDownload(*gocui.Gui, *gocui.View) error {
	c := a.cloud
	if c == nil || c.download == nil {
		a.status = "Cloud mode is read-only"
		return nil
	}
	c.download.cancel()
	a.status = "Cancelling download..."
	return nil
}

func (a *App) cloudReadOnly(*gocui.Gui, *gocui.View) error {
	a.status = "Cloud mode is read-only"
	return nil
}

// byMode routes a key to the handler for the visible mode. A nil cloud
// handler makes the key a harmless read-only notice instead of a local action.
func (a *App) byMode(local, cloud func(*gocui.Gui, *gocui.View) error) func(*gocui.Gui, *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if a.mode == modeCloud {
			if cloud == nil {
				return a.cloudReadOnly(g, v)
			}
			return cloud(g, v)
		}
		if local == nil {
			return nil
		}
		return local(g, v)
	}
}

func cloudErrorMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return ""
	case errors.Is(err, xcodecloud.ErrNotConfigured):
		return "Xcode Cloud is not configured; set " + xcodecloud.EnvIssuerID + ", " + xcodecloud.EnvKeyID + ", and " + xcodecloud.EnvPrivateKeyPath
	case errors.Is(err, xcodecloud.ErrIncompleteConfig), errors.Is(err, xcodecloud.ErrKeyUnreadable), errors.Is(err, xcodecloud.ErrKeyMalformed):
		return err.Error()
	case errors.Is(err, xcodecloud.ErrUnauthorized):
		return "App Store Connect rejected the API key; check the issuer ID, key ID, and private key"
	case errors.Is(err, xcodecloud.ErrForbidden):
		return "The App Store Connect API key lacks Xcode Cloud access; use a key with the Developer role or higher"
	case errors.Is(err, xcodecloud.ErrRateLimited):
		return "App Store Connect rate limit reached; showing the last known data"
	case errors.Is(err, xcodecloud.ErrArtifactExpired):
		return "Artifact download link expired; refresh and try again"
	case errors.Is(err, context.DeadlineExceeded):
		return "App Store Connect request timed out; showing the last known data"
	}
	return strings.ReplaceAll(err.Error(), "\n", " ")
}

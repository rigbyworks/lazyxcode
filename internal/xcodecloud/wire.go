package xcodecloud

import (
	"encoding/json"
	"time"
)

// The types below mirror the App Store Connect JSON:API documents used by this
// package. They are private so that wire details never reach the TUI.

type document struct {
	Data     json.RawMessage `json:"data"`
	Included []resource      `json:"included"`
	Links    documentLinks   `json:"links"`
}

type documentLinks struct {
	Self string `json:"self"`
	Next string `json:"next"`
}

type errorDocument struct {
	Errors []apiErrorEntry `json:"errors"`
}

type apiErrorEntry struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type resource struct {
	Type          string                  `json:"type"`
	ID            string                  `json:"id"`
	Attributes    json.RawMessage         `json:"attributes"`
	Relationships map[string]relationship `json:"relationships"`
}

type relationship struct {
	Data json.RawMessage `json:"data"`
}

type resourceRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (r relationship) ref() (resourceRef, bool) {
	if len(r.Data) == 0 || string(r.Data) == "null" || r.Data[0] != '{' {
		return resourceRef{}, false
	}
	var ref resourceRef
	if err := json.Unmarshal(r.Data, &ref); err != nil || ref.ID == "" {
		return resourceRef{}, false
	}
	return ref, true
}

func (d *document) resources() ([]resource, error) {
	if len(d.Data) == 0 || string(d.Data) == "null" {
		return nil, nil
	}
	if d.Data[0] == '[' {
		var list []resource
		err := json.Unmarshal(d.Data, &list)
		return list, err
	}
	var single resource
	if err := json.Unmarshal(d.Data, &single); err != nil {
		return nil, err
	}
	return []resource{single}, nil
}

func (d *document) includedByRef() map[resourceRef]resource {
	index := make(map[resourceRef]resource, len(d.Included))
	for _, item := range d.Included {
		index[resourceRef{Type: item.Type, ID: item.ID}] = item
	}
	return index
}

type ciProductAttributes struct {
	Name        string `json:"name"`
	ProductType string `json:"productType"`
}

type ciWorkflowAttributes struct {
	Name               string `json:"name"`
	IsEnabled          *bool  `json:"isEnabled"`
	IsLockedForEditing bool   `json:"isLockedForEditing"`
}

type ciIssueCounts struct {
	AnalyzerWarnings int `json:"analyzerWarnings"`
	Errors           int `json:"errors"`
	TestFailures     int `json:"testFailures"`
	Warnings         int `json:"warnings"`
}

type ciGitUser struct {
	DisplayName string `json:"displayName"`
}

type ciCommit struct {
	CommitSha string     `json:"commitSha"`
	Message   string     `json:"message"`
	WebURL    string     `json:"webUrl"`
	Author    *ciGitUser `json:"author"`
	Committer *ciGitUser `json:"committer"`
}

type ciBuildRunAttributes struct {
	Number             int            `json:"number"`
	CreatedDate        *time.Time     `json:"createdDate"`
	StartedDate        *time.Time     `json:"startedDate"`
	FinishedDate       *time.Time     `json:"finishedDate"`
	SourceCommit       *ciCommit      `json:"sourceCommit"`
	DestinationCommit  *ciCommit      `json:"destinationCommit"`
	IsPullRequestBuild bool           `json:"isPullRequestBuild"`
	IssueCounts        *ciIssueCounts `json:"issueCounts"`
	ExecutionProgress  string         `json:"executionProgress"`
	CompletionStatus   string         `json:"completionStatus"`
	StartReason        string         `json:"startReason"`
	CancelReason       string         `json:"cancelReason"`
}

type scmGitReferenceAttributes struct {
	CanonicalName string `json:"canonicalName"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
}

type ciBuildActionAttributes struct {
	Name              string         `json:"name"`
	ActionType        string         `json:"actionType"`
	StartedDate       *time.Time     `json:"startedDate"`
	FinishedDate      *time.Time     `json:"finishedDate"`
	IssueCounts       *ciIssueCounts `json:"issueCounts"`
	ExecutionProgress string         `json:"executionProgress"`
	CompletionStatus  string         `json:"completionStatus"`
	IsRequiredToPass  bool           `json:"isRequiredToPass"`
}

type ciFileSource struct {
	Path       string `json:"path"`
	LineNumber int    `json:"lineNumber"`
}

type ciIssueAttributes struct {
	IssueType  string        `json:"issueType"`
	Message    string        `json:"message"`
	Category   string        `json:"category"`
	FileSource *ciFileSource `json:"fileSource"`
}

type ciDestinationTestResult struct {
	UUID       string  `json:"uuid"`
	DeviceName string  `json:"deviceName"`
	OSVersion  string  `json:"osVersion"`
	Status     string  `json:"status"`
	Duration   float64 `json:"duration"`
}

type ciTestResultAttributes struct {
	ClassName              string                    `json:"className"`
	Name                   string                    `json:"name"`
	Status                 string                    `json:"status"`
	Message                string                    `json:"message"`
	FileSource             *ciFileSource             `json:"fileSource"`
	DestinationTestResults []ciDestinationTestResult `json:"destinationTestResults"`
}

type ciArtifactAttributes struct {
	FileType    string `json:"fileType"`
	FileName    string `json:"fileName"`
	FileSize    int64  `json:"fileSize"`
	DownloadURL string `json:"downloadUrl"`
}

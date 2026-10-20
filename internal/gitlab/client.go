// Package gitlab is a small GitLab REST client covering exactly the endpoints
// the operator and the runner need.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// API is the subset of GitLab used by the operator and the runner.
type API interface {
	CurrentUser(ctx context.Context) (*User, error)
	GetProject(ctx context.Context, project string) (*Project, error)
	ListMilestones(ctx context.Context, project string) ([]Milestone, error)
	GetMilestone(ctx context.Context, project string, id int64) (*Milestone, error)
	ListIssues(ctx context.Context, project string, opts ListIssuesOptions) ([]Issue, error)
	GetIssue(ctx context.Context, project string, iid int64) (*Issue, error)
	CreateIssue(ctx context.Context, project string, opts CreateIssueOptions) (*Issue, error)
	UpdateIssue(ctx context.Context, project string, iid int64, opts UpdateIssueOptions) (*Issue, error)
	ListIssueNotes(ctx context.Context, project string, iid int64) ([]Note, error)
	CreateIssueNote(ctx context.Context, project string, iid int64, body string) (*Note, error)
	UpdateIssueNote(ctx context.Context, project string, iid, noteID int64, body string) (*Note, error)
	GetMergeRequest(ctx context.Context, project string, iid int64) (*MergeRequest, error)
	FindOpenMergeRequest(ctx context.Context, project, sourceBranch string) (*MergeRequest, error)
	CreateMergeRequest(ctx context.Context, project string, opts CreateMergeRequestOptions) (*MergeRequest, error)
	UpdateMergeRequest(ctx context.Context, project string, iid int64, opts UpdateMergeRequestOptions) (*MergeRequest, error)
	MergeMergeRequest(ctx context.Context, project string, iid int64, opts MergeOptions) (*MergeRequest, error)
	RebaseMergeRequest(ctx context.Context, project string, iid int64) error
	ListMergeRequestDiffs(ctx context.Context, project string, iid int64) ([]Diff, error)
	ListIssueStateEvents(ctx context.Context, project string, iid int64) ([]StateEvent, error)
	ListMergeRequestNotes(ctx context.Context, project string, iid int64) ([]Note, error)
	CreateMergeRequestNote(ctx context.Context, project string, iid int64, body string) (*Note, error)
	ListPipelines(ctx context.Context, project string, opts ListPipelinesOptions) ([]Pipeline, error)
	ListPipelineJobs(ctx context.Context, project string, pipelineID int64, scope string) ([]Job, error)
	JobTrace(ctx context.Context, project string, jobID int64) (string, error)
	RetryPipeline(ctx context.Context, project string, pipelineID int64) (*Pipeline, error)
}

// Pipeline is a CI pipeline.
type Pipeline struct {
	ID     int64  `json:"id"`
	SHA    string `json:"sha"`
	Ref    string `json:"ref"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
}

// Job is a CI job.
type Job struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Stage  string `json:"stage"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
	// AllowFailure jobs do not fail the pipeline.
	AllowFailure bool `json:"allow_failure"`
}

// ListPipelinesOptions filters pipelines; results are newest first.
type ListPipelinesOptions struct {
	SHA string
	Ref string
	// Scope: running, pending, finished, branches, tags.
	Scope string
	// Limit caps the number of results (default: one page of 20).
	Limit int
}

// User is a GitLab user.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// Project is a GitLab project.
type Project struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	DefaultBranch     string `json:"default_branch"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	WebURL            string `json:"web_url"`
}

// Milestone is a GitLab project milestone.
type Milestone struct {
	ID          int64      `json:"id"`
	IID         int64      `json:"iid"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	WebURL      string     `json:"web_url"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

// Issue is a GitLab issue.
type Issue struct {
	ID          int64      `json:"id"`
	IID         int64      `json:"iid"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	Labels      []string   `json:"labels"`
	Milestone   *Milestone `json:"milestone"`
	Assignees   []User     `json:"assignees"`
	WebURL      string     `json:"web_url"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

// HasLabel reports whether the issue carries the label.
func (i *Issue) HasLabel(label string) bool {
	for _, l := range i.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// Note is a comment on an issue or merge request.
type Note struct {
	ID        int64      `json:"id"`
	Body      string     `json:"body"`
	System    bool       `json:"system"`
	Author    User       `json:"author"`
	CreatedAt *time.Time `json:"created_at"`
}

// MergeRequest is a GitLab merge request.
type MergeRequest struct {
	ID           int64      `json:"id"`
	IID          int64      `json:"iid"`
	Title        string     `json:"title"`
	State        string     `json:"state"`
	SourceBranch string     `json:"source_branch"`
	TargetBranch string     `json:"target_branch"`
	Milestone    *Milestone `json:"milestone"`
	WebURL       string     `json:"web_url"`
	UpdatedAt    *time.Time `json:"updated_at"`
	// SHA is the head commit of the source branch.
	SHA   string `json:"sha"`
	Draft bool   `json:"draft"`
	// DetailedMergeStatus is GitLab's view of whether the MR can be merged:
	// mergeable, need_rebase, conflict, draft_status, ci_still_running, …
	DetailedMergeStatus string `json:"detailed_merge_status"`
	MergeCommitSHA      string `json:"merge_commit_sha"`
	SquashCommitSHA     string `json:"squash_commit_sha"`
	RebaseInProgress    bool   `json:"rebase_in_progress"`
	MergeError          string `json:"merge_error"`
}

// StateEvent is a state change of an issue: closed, reopened.
type StateEvent struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
	// SourceMergeRequest is the MR whose merge closed the issue.
	SourceMergeRequest *MergeRequestRef `json:"source_merge_request"`
}

// MergeRequestRef identifies a merge request, possibly of another project.
type MergeRequestRef struct {
	IID       int64 `json:"iid"`
	ProjectID int64 `json:"project_id"`
}

// Diff is the change of one file in a merge request.
type Diff struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	Diff        string `json:"diff"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
	// Collapsed and TooLarge diffs come without their content.
	Collapsed bool `json:"collapsed"`
	TooLarge  bool `json:"too_large"`
}

// ListIssuesOptions filters issues.
type ListIssuesOptions struct {
	// Labels the issue must all carry.
	Labels []string
	// State: opened, closed or all. Defaults to opened.
	State string
	// Milestone title.
	Milestone string
}

// CreateIssueOptions creates an issue.
type CreateIssueOptions struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Labels is a comma separated list.
	Labels      string `json:"labels,omitempty"`
	MilestoneID int64  `json:"milestone_id,omitempty"`
}

// UpdateIssueOptions updates an issue. Empty fields are left untouched.
type UpdateIssueOptions struct {
	Title        string   `json:"title,omitempty"`
	Description  string   `json:"description,omitempty"`
	AddLabels    []string `json:"-"`
	RemoveLabels []string `json:"-"`
	StateEvent   string   `json:"state_event,omitempty"`
	// AssigneeIDs replaces all assignees of the issue.
	AssigneeIDs []int64 `json:"-"`
}

// CreateMergeRequestOptions creates a merge request.
type CreateMergeRequestOptions struct {
	SourceBranch       string `json:"source_branch"`
	TargetBranch       string `json:"target_branch"`
	Title              string `json:"title"`
	Description        string `json:"description,omitempty"`
	RemoveSourceBranch bool   `json:"remove_source_branch,omitempty"`
	MilestoneID        int64  `json:"milestone_id,omitempty"`
}

// UpdateMergeRequestOptions updates a merge request. Empty fields are left
// untouched.
type UpdateMergeRequestOptions struct {
	Title string `json:"title,omitempty"`
}

// MergeOptions merges a merge request.
type MergeOptions struct {
	// SHA must match the head of the source branch, so only the checked
	// commit is merged.
	SHA                      string `json:"sha,omitempty"`
	ShouldRemoveSourceBranch bool   `json:"should_remove_source_branch,omitempty"`
}

// Client implements API against the GitLab REST API v4.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

var _ API = (*Client)(nil)

// New creates a client for the GitLab instance at baseURL (e.g. https://gitlab.com).
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/") + "/api/v4",
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Error is returned for non-2xx responses.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *Error) Error() string {
	return fmt.Sprintf("gitlab: %s %s: %d %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// IsNotFound reports whether err is a GitLab 404.
func IsNotFound(err error) bool {
	if e, ok := err.(*Error); ok {
		return e.StatusCode == http.StatusNotFound
	}
	return false
}

// StatusCode returns the HTTP status of a GitLab error, 0 for other errors.
func StatusCode(err error) int {
	if e, ok := err.(*Error); ok {
		return e.StatusCode
	}
	return 0
}

func projectPath(project string) string {
	return "/projects/" + url.PathEscape(project)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) (*http.Response, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := string(data)
		if len(msg) > 512 {
			msg = msg[:512]
		}
		return resp, &Error{StatusCode: resp.StatusCode, Method: method, Path: path, Body: msg}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp, fmt.Errorf("gitlab: decoding %s %s: %w", method, path, err)
		}
	}
	return resp, nil
}

// maxPages bounds pagination so a misconfigured filter cannot stall a sync.
const maxPages = 20

// list follows GitLab's X-Next-Page pagination.
func list[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("per_page", "100")
	var all []T
	page := "1"
	for i := 0; i < maxPages && page != ""; i++ {
		query.Set("page", page)
		var items []T
		resp, err := c.do(ctx, http.MethodGet, path, query, nil, &items)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		page = resp.Header.Get("X-Next-Page")
	}
	return all, nil
}

func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	var u User
	_, err := c.do(ctx, http.MethodGet, "/user", nil, nil, &u)
	return &u, err
}

func (c *Client) GetProject(ctx context.Context, project string) (*Project, error) {
	var p Project
	_, err := c.do(ctx, http.MethodGet, projectPath(project), nil, nil, &p)
	return &p, err
}

func (c *Client) ListMilestones(ctx context.Context, project string) ([]Milestone, error) {
	return list[Milestone](ctx, c, projectPath(project)+"/milestones", url.Values{"state": {"active"}})
}

func (c *Client) GetMilestone(ctx context.Context, project string, id int64) (*Milestone, error) {
	var m Milestone
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/milestones/%d", projectPath(project), id), nil, nil, &m)
	return &m, err
}

func (c *Client) ListIssues(ctx context.Context, project string, opts ListIssuesOptions) ([]Issue, error) {
	q := url.Values{}
	state := opts.State
	if state == "" {
		state = "opened"
	}
	q.Set("state", state)
	if len(opts.Labels) > 0 {
		q.Set("labels", strings.Join(opts.Labels, ","))
	}
	if opts.Milestone != "" {
		q.Set("milestone", opts.Milestone)
	}
	return list[Issue](ctx, c, projectPath(project)+"/issues", q)
}

func (c *Client) GetIssue(ctx context.Context, project string, iid int64) (*Issue, error) {
	var i Issue
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues/%d", projectPath(project), iid), nil, nil, &i)
	return &i, err
}

func (c *Client) CreateIssue(ctx context.Context, project string, opts CreateIssueOptions) (*Issue, error) {
	var i Issue
	_, err := c.do(ctx, http.MethodPost, projectPath(project)+"/issues", nil, opts, &i)
	return &i, err
}

func (c *Client) UpdateIssue(ctx context.Context, project string, iid int64, opts UpdateIssueOptions) (*Issue, error) {
	body := map[string]any{}
	if opts.Title != "" {
		body["title"] = opts.Title
	}
	if opts.Description != "" {
		body["description"] = opts.Description
	}
	if opts.StateEvent != "" {
		body["state_event"] = opts.StateEvent
	}
	if len(opts.AddLabels) > 0 {
		body["add_labels"] = strings.Join(opts.AddLabels, ",")
	}
	if len(opts.RemoveLabels) > 0 {
		body["remove_labels"] = strings.Join(opts.RemoveLabels, ",")
	}
	if len(opts.AssigneeIDs) > 0 {
		body["assignee_ids"] = opts.AssigneeIDs
	}
	var i Issue
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/issues/%d", projectPath(project), iid), nil, body, &i)
	return &i, err
}

func (c *Client) ListIssueNotes(ctx context.Context, project string, iid int64) ([]Note, error) {
	q := url.Values{"sort": {"asc"}, "order_by": {"created_at"}}
	return list[Note](ctx, c, fmt.Sprintf("%s/issues/%d/notes", projectPath(project), iid), q)
}

func (c *Client) CreateIssueNote(ctx context.Context, project string, iid int64, body string) (*Note, error) {
	var n Note
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/notes", projectPath(project), iid), nil, map[string]string{"body": body}, &n)
	return &n, err
}

func (c *Client) UpdateIssueNote(ctx context.Context, project string, iid, noteID int64, body string) (*Note, error) {
	var n Note
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/issues/%d/notes/%d", projectPath(project), iid, noteID), nil, map[string]string{"body": body}, &n)
	return &n, err
}

func (c *Client) GetMergeRequest(ctx context.Context, project string, iid int64) (*MergeRequest, error) {
	var mr MergeRequest
	q := url.Values{"include_rebase_in_progress": {"true"}}
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/merge_requests/%d", projectPath(project), iid), q, nil, &mr)
	return &mr, err
}

func (c *Client) UpdateMergeRequest(ctx context.Context, project string, iid int64, opts UpdateMergeRequestOptions) (*MergeRequest, error) {
	var mr MergeRequest
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/merge_requests/%d", projectPath(project), iid), nil, opts, &mr)
	return &mr, err
}

func (c *Client) MergeMergeRequest(ctx context.Context, project string, iid int64, opts MergeOptions) (*MergeRequest, error) {
	var mr MergeRequest
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/merge_requests/%d/merge", projectPath(project), iid), nil, opts, &mr)
	return &mr, err
}

func (c *Client) RebaseMergeRequest(ctx context.Context, project string, iid int64) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/merge_requests/%d/rebase", projectPath(project), iid), nil, nil, nil)
	return err
}

func (c *Client) ListMergeRequestDiffs(ctx context.Context, project string, iid int64) ([]Diff, error) {
	return list[Diff](ctx, c, fmt.Sprintf("%s/merge_requests/%d/diffs", projectPath(project), iid), nil)
}

// ListIssueStateEvents lists the state changes of an issue, oldest first.
func (c *Client) ListIssueStateEvents(ctx context.Context, project string, iid int64) ([]StateEvent, error) {
	return list[StateEvent](ctx, c, fmt.Sprintf("%s/issues/%d/resource_state_events", projectPath(project), iid), nil)
}

func (c *Client) FindOpenMergeRequest(ctx context.Context, project, sourceBranch string) (*MergeRequest, error) {
	var mrs []MergeRequest
	q := url.Values{"state": {"opened"}, "source_branch": {sourceBranch}}
	if _, err := c.do(ctx, http.MethodGet, projectPath(project)+"/merge_requests", q, nil, &mrs); err != nil {
		return nil, err
	}
	if len(mrs) == 0 {
		return nil, nil
	}
	return &mrs[0], nil
}

func (c *Client) CreateMergeRequest(ctx context.Context, project string, opts CreateMergeRequestOptions) (*MergeRequest, error) {
	var mr MergeRequest
	_, err := c.do(ctx, http.MethodPost, projectPath(project)+"/merge_requests", nil, opts, &mr)
	return &mr, err
}

func (c *Client) ListMergeRequestNotes(ctx context.Context, project string, iid int64) ([]Note, error) {
	q := url.Values{"sort": {"asc"}, "order_by": {"created_at"}}
	return list[Note](ctx, c, fmt.Sprintf("%s/merge_requests/%d/notes", projectPath(project), iid), q)
}

func (c *Client) CreateMergeRequestNote(ctx context.Context, project string, iid int64, body string) (*Note, error) {
	var n Note
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/merge_requests/%d/notes", projectPath(project), iid), nil, map[string]string{"body": body}, &n)
	return &n, err
}

func (c *Client) ListPipelines(ctx context.Context, project string, opts ListPipelinesOptions) ([]Pipeline, error) {
	q := url.Values{"order_by": {"id"}, "sort": {"desc"}}
	if opts.SHA != "" {
		q.Set("sha", opts.SHA)
	}
	if opts.Ref != "" {
		q.Set("ref", opts.Ref)
	}
	if opts.Scope != "" {
		q.Set("scope", opts.Scope)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	q.Set("per_page", strconv.Itoa(limit))
	var ps []Pipeline
	_, err := c.do(ctx, http.MethodGet, projectPath(project)+"/pipelines", q, nil, &ps)
	return ps, err
}

func (c *Client) ListPipelineJobs(ctx context.Context, project string, pipelineID int64, scope string) ([]Job, error) {
	q := url.Values{}
	if scope != "" {
		q.Add("scope[]", scope)
	}
	return list[Job](ctx, c, fmt.Sprintf("%s/pipelines/%d/jobs", projectPath(project), pipelineID), q)
}

// maxTrace bounds how much of a job log is downloaded; the end of the log is
// what matters.
const maxTrace = 4 << 20

func (c *Client) JobTrace(ctx context.Context, project string, jobID int64) (string, error) {
	path := fmt.Sprintf("%s/jobs/%d/trace", projectPath(project), jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &Error{StatusCode: resp.StatusCode, Method: http.MethodGet, Path: path}
	}
	// Keep the tail of very long logs.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if len(data) > maxTrace {
		data = data[len(data)-maxTrace:]
	}
	return string(data), nil
}

func (c *Client) RetryPipeline(ctx context.Context, project string, pipelineID int64) (*Pipeline, error) {
	var p Pipeline
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/pipelines/%d/retry", projectPath(project), pipelineID), nil, nil, &p)
	return &p, err
}

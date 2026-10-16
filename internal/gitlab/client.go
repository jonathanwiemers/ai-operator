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
	GetMergeRequest(ctx context.Context, project string, iid int64) (*MergeRequest, error)
	FindOpenMergeRequest(ctx context.Context, project, sourceBranch string) (*MergeRequest, error)
	CreateMergeRequest(ctx context.Context, project string, opts CreateMergeRequestOptions) (*MergeRequest, error)
	ListMergeRequestNotes(ctx context.Context, project string, iid int64) ([]Note, error)
	CreateMergeRequestNote(ctx context.Context, project string, iid int64, body string) (*Note, error)
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
	WebURL       string     `json:"web_url"`
	UpdatedAt    *time.Time `json:"updated_at"`
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
}

// CreateMergeRequestOptions creates a merge request.
type CreateMergeRequestOptions struct {
	SourceBranch       string `json:"source_branch"`
	TargetBranch       string `json:"target_branch"`
	Title              string `json:"title"`
	Description        string `json:"description,omitempty"`
	RemoveSourceBranch bool   `json:"remove_source_branch,omitempty"`
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

func (c *Client) GetMergeRequest(ctx context.Context, project string, iid int64) (*MergeRequest, error) {
	var mr MergeRequest
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/merge_requests/%d", projectPath(project), iid), nil, nil, &mr)
	return &mr, err
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

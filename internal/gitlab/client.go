// Package gitlab is a small GitLab REST client covering exactly the endpoints
// the operator and the runner need.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
)

// API is the subset of GitLab used by the operator and the runner.
type API interface {
	CurrentUser(ctx context.Context) (*User, error)
	FindUser(ctx context.Context, username string) (*User, error)
	GetProject(ctx context.Context, project string) (*Project, error)
	ListMilestones(ctx context.Context, project string) ([]Milestone, error)
	ListClosedMilestones(ctx context.Context, project string, maxPages int) ([]Milestone, error)
	GetMilestone(ctx context.Context, project string, id int64) (*Milestone, error)
	GetMilestoneByIID(ctx context.Context, project string, iid int64) (*Milestone, error)
	UpdateMilestoneDescription(ctx context.Context, project string, id int64, description string) (*Milestone, error)
	CloseMilestone(ctx context.Context, project string, id int64) error
	CreateMilestone(ctx context.Context, project string, opts CreateMilestoneOptions) (*Milestone, error)
	ListIssues(ctx context.Context, project string, opts ListIssuesOptions) ([]Issue, error)
	GetIssue(ctx context.Context, project string, iid int64) (*Issue, error)
	CreateIssue(ctx context.Context, project string, opts CreateIssueOptions) (*Issue, error)
	UpdateIssue(ctx context.Context, project string, iid int64, opts UpdateIssueOptions) (*Issue, error)
	ListIssueNotes(ctx context.Context, project string, iid int64) ([]Note, error)
	ListIssueNotesNewest(ctx context.Context, project string, iid int64, n int) ([]Note, error)
	ListIssueDiscussions(ctx context.Context, project string, iid int64) ([]Discussion, error)
	CreateIssueNote(ctx context.Context, project string, iid int64, body string) (*Note, error)
	ReplyIssueDiscussion(ctx context.Context, project string, iid int64, discussion, body string) (*Note, error)
	UpdateIssueNote(ctx context.Context, project string, iid, noteID int64, body string) (*Note, error)
	GetMergeRequest(ctx context.Context, project string, iid int64) (*MergeRequest, error)
	FindOpenMergeRequest(ctx context.Context, project, sourceBranch string) (*MergeRequest, error)
	CreateMergeRequest(ctx context.Context, project string, opts CreateMergeRequestOptions) (*MergeRequest, error)
	UpdateMergeRequest(ctx context.Context, project string, iid int64, opts UpdateMergeRequestOptions) (*MergeRequest, error)
	MergeMergeRequest(ctx context.Context, project string, iid int64, opts MergeOptions) (*MergeRequest, error)
	RebaseMergeRequest(ctx context.Context, project string, iid int64) error
	DeleteBranch(ctx context.Context, project, branch string) error
	GetBranch(ctx context.Context, project, branch string) (*Branch, error)
	ListMergeRequestDiffs(ctx context.Context, project string, iid int64) ([]Diff, error)
	// ListMergeRequestVersions lists the versions of an MR, newest first.
	ListMergeRequestVersions(ctx context.Context, project string, iid int64) ([]MergeRequestVersion, error)
	GetMergeRequestVersion(ctx context.Context, project string, iid, versionID int64) (*MergeRequestVersionDetail, error)
	// CompareCommits compares two refs of the repository (straight=false).
	CompareCommits(ctx context.Context, project, from, to string) (*Compare, error)
	ListIssueStateEvents(ctx context.Context, project string, iid int64) ([]StateEvent, error)
	ListIssueLabelEvents(ctx context.Context, project string, iid int64) ([]LabelEvent, error)
	ListRepositoryTree(ctx context.Context, project, path, ref string, recursive bool) ([]TreeEntry, error)
	ListTags(ctx context.Context, project string, opts ListTagsOptions) ([]Tag, error)
	ListCommitRefs(ctx context.Context, project, sha, refType string) ([]CommitRef, error)
	ListIssueLinks(ctx context.Context, project string, iid int64) ([]LinkedIssue, error)
	CreateIssueLink(ctx context.Context, project string, iid, targetIID int64, linkType string) error
	DeleteIssueLink(ctx context.Context, project string, iid, linkID int64) error
	ListMergeRequestNotes(ctx context.Context, project string, iid int64) ([]Note, error)
	CreateMergeRequestNote(ctx context.Context, project string, iid int64, body string) (*Note, error)
	ListMergeRequestDiscussions(ctx context.Context, project string, iid int64) ([]Discussion, error)
	ReplyMergeRequestDiscussion(ctx context.Context, project string, iid int64, discussion, body string) (*Note, error)
	UpdateMergeRequestNote(ctx context.Context, project string, iid, noteID int64, body string) (*Note, error)
	DeleteMergeRequestNote(ctx context.Context, project string, iid, noteID int64) error
	CreateMergeRequestPipeline(ctx context.Context, project string, iid int64) error
	CreatePipeline(ctx context.Context, project, ref string, vars ...PipelineVariable) (*Pipeline, error)
	CreateRelease(ctx context.Context, project, tag, ref, description string) error
	GetPipeline(ctx context.Context, project string, id int64) (*Pipeline, error)
	ListPipelines(ctx context.Context, project string, opts ListPipelinesOptions) ([]Pipeline, error)
	ListPipelineJobs(ctx context.Context, project string, pipelineID int64, scope string) ([]Job, error)
	JobTrace(ctx context.Context, project string, jobID int64) (string, error)
	RetryPipeline(ctx context.Context, project string, pipelineID int64) (*Pipeline, error)
	ListProjectHooks(ctx context.Context, project string) ([]ProjectHook, error)
	AddProjectHook(ctx context.Context, project string, opts ProjectHookOptions) (*ProjectHook, error)
	EditProjectHook(ctx context.Context, project string, id int64, opts ProjectHookOptions) (*ProjectHook, error)
	DeleteProjectHook(ctx context.Context, project string, id int64) error
	// TestProjectHook makes GitLab send the hook a test delivery of trigger
	// (push_events, issues_events, milestone_events, ...).
	TestProjectHook(ctx context.Context, project string, id int64, trigger string) error
	// UploadFile stores a file in the project's uploads (not retried after a
	// server error); DownloadUpload reads one, failing with ErrTooLarge past
	// max bytes.
	UploadFile(ctx context.Context, project, name string, content []byte) (Upload, error)
	DownloadUpload(ctx context.Context, project, secret, filename string, max int64) ([]byte, error)
}

// Client implements API against the GitLab REST API v4.
type Client struct {
	baseURL string
	token   string
	// oauth sends the token as an OAuth bearer token (a user's token from
	// the web UI) instead of a personal/project access token.
	oauth bool
	http  *http.Client

	requests, retries atomic.Int64
	// graphql counts the GraphQL requests; they are part of requests too.
	graphql atomic.Int64
	// ops counts requests by kind and the exported method that sent them.
	opsMu sync.Mutex
	ops   map[OpKey]int64
}

// OpKey names one kind of request: Kind is rest or graphql, Op the exported
// Client method that sent it (ListIssues, DiscoverGroup, ...), "other" when
// none is on the stack.
type OpKey struct {
	Kind, Op string
}

// Stats counts the requests a client sent (REST and GraphQL), retries
// included, how many of them were retries, and how many were GraphQL.
type Stats struct {
	Requests, Retries int64
	// GraphQL is the GraphQL subset of Requests.
	GraphQL int64
	// Ops splits Requests by kind and method; nil for clients that do not
	// track it (fakes).
	Ops map[OpKey]int64
}

// Stats returns the client's request counts so far.
func (c *Client) Stats() Stats {
	c.opsMu.Lock()
	ops := maps.Clone(c.ops)
	c.opsMu.Unlock()
	return Stats{Requests: c.requests.Load(), Retries: c.retries.Load(), GraphQL: c.graphql.Load(), Ops: ops}
}

// countOp records one request under the outermost exported Client method on
// the caller's stack, so the metric says what the requests were for.
func (c *Client) countOp(graphql bool) {
	key := OpKey{Kind: "rest", Op: callerOp()}
	if graphql {
		key.Kind = "graphql"
	}
	c.opsMu.Lock()
	if c.ops == nil {
		c.ops = map[OpKey]int64{}
	}
	c.ops[key]++
	c.opsMu.Unlock()
}

const clientMethodPrefix = "/internal/gitlab.(*Client)."

// callerOp returns the name of the outermost exported (*Client) method on
// the stack, or "other".
func callerOp() string {
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	op := "other"
	for {
		f, more := frames.Next()
		if i := strings.Index(f.Function, clientMethodPrefix); i >= 0 {
			name := f.Function[i+len(clientMethodPrefix):]
			if j := strings.IndexByte(name, '.'); j >= 0 {
				name = name[:j] // closures: ListIssues.func1
			}
			if name != "" && unicode.IsUpper(rune(name[0])) {
				op = name
			}
		}
		if !more {
			return op
		}
	}
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

// NewOAuth creates a client that acts with a user's OAuth access token.
func NewOAuth(baseURL, accessToken string) *Client {
	c := New(baseURL, accessToken)
	c.oauth = true
	return c
}

func (c *Client) auth(req *http.Request) {
	if c.oauth {
		req.Header.Set("Authorization", "Bearer "+c.token)
		return
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
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

// IsTransient reports whether err is one a later attempt may not get: a rate
// limit, a GitLab server error, a timeout or a broken connection. Joined
// errors are transient if all of them are.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			if !IsTransient(e) {
				return false
			}
		}
		return true
	}
	var ge *Error
	if errors.As(err, &ge) {
		return ge.StatusCode == http.StatusTooManyRequests || ge.StatusCode >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return de.IsTimeout || de.IsTemporary
	}
	// A connection GitLab dropped; refused connections, TLS and proxy errors
	// are more likely a setting that stays wrong.
	for _, e := range []error{syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE, syscall.ETIMEDOUT, io.EOF, io.ErrUnexpectedEOF, context.DeadlineExceeded} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
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

// Retries. A rate-limited request (429) did not run, so it is retried
// whatever its method, after the wait GitLab asks for. Server errors and
// broken connections are retried for reads only: a write may have been
// applied. Waits beyond maxRetryWait, or past the context's deadline, are not
// waited out; the caller's next poll tries again.
var (
	maxAttempts  = 3
	retryBackoff = time.Second
	maxRetryWait = time.Minute
)

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) (*http.Response, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	return c.call(ctx, request{method: method, url: u, path: path, payload: payload, safe: method == http.MethodGet}, out)
}

// request is one HTTP request with its retries: method to the full URL url,
// path naming it in errors. safe marks a request that may be repeated after a
// server error or a broken connection (a read); graphql counts its attempts as
// GraphQL requests.
type request struct {
	method, url, path string
	payload           []byte
	safe, graphql     bool
	// contentType is the payload's Content-Type, application/json when empty.
	contentType string
	// maxBody, when positive, makes the request raw: the body is not decoded
	// and more than maxBody bytes of a 2xx answer fail with ErrTooLarge.
	maxBody int64
}

// ErrTooLarge is returned by DownloadUpload when the file is larger than
// the caller's limit.
var ErrTooLarge = errors.New("gitlab: download exceeds the size limit")

// call sends r, retrying as retryWait says, and decodes a 2xx body into out.
func (c *Client) call(ctx context.Context, r request, out any) (*http.Response, error) {
	method, path := r.method, r.path
	for attempt := 1; ; attempt++ {
		if r.graphql {
			c.graphql.Add(1)
		}
		c.countOp(r.graphql)
		resp, data, err := c.send(ctx, r)
		if err == nil && (resp.StatusCode < 200 || resp.StatusCode > 299) {
			msg := string(data)
			if len(msg) > 512 {
				msg = msg[:512]
			}
			err = &Error{StatusCode: resp.StatusCode, Method: method, Path: path, Body: msg}
		}
		if err != nil {
			wait, ok := retryWait(r.safe, resp, err, attempt)
			if !ok || attempt >= maxAttempts || ctx.Err() != nil {
				return resp, err
			}
			if d, has := ctx.Deadline(); has && time.Until(d) < wait {
				return resp, err
			}
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return resp, err
			case <-t.C:
			}
			c.retries.Add(1)
			continue
		}
		if r.maxBody > 0 {
			if rb, ok := out.(*[]byte); ok {
				*rb = data
			}
			return resp, nil
		}
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return resp, fmt.Errorf("gitlab: decoding %s %s: %w", method, path, err)
			}
		}
		return resp, nil
	}
}

// send makes one attempt and reads the response body.
func (c *Client) send(ctx context.Context, r request) (*http.Response, []byte, error) {
	var rdr io.Reader
	if r.payload != nil {
		rdr = bytes.NewReader(r.payload)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, r.url, rdr)
	if err != nil {
		return nil, nil, err
	}
	c.auth(req)
	if r.payload != nil {
		ct := r.contentType
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	c.requests.Add(1)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	limit := int64(16 << 20)
	if r.maxBody > 0 {
		limit = r.maxBody + 1
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return resp, nil, err
	}
	if r.maxBody > 0 && resp.StatusCode >= 200 && resp.StatusCode <= 299 && int64(len(data)) > r.maxBody {
		return resp, nil, ErrTooLarge
	}
	return resp, data, nil
}

// maxPages bounds pagination so a misconfigured filter cannot stall a sync.
const maxPages = 20

// list follows GitLab's X-Next-Page pagination.
func list[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	return listPages[T](ctx, c, path, query, maxPages)
}

// listPages is list reading at most pages pages, maxPages for 0.
func listPages[T any](ctx context.Context, c *Client, path string, query url.Values, pages int) ([]T, error) {
	if pages <= 0 || pages > maxPages {
		pages = maxPages
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("per_page", "100")
	var all []T
	page := "1"
	for i := 0; i < pages && page != ""; i++ {
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

// FindUser returns the user with the username, or nil if there is none.
func (c *Client) FindUser(ctx context.Context, username string) (*User, error) {
	var users []User
	if _, err := c.do(ctx, http.MethodGet, "/users", url.Values{"username": {username}}, nil, &users); err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, nil
	}
	return &users[0], nil
}

// UploadFile uploads content as the file name to the project's uploads. It
// is a write: a server error is not retried, as the upload may have been
// stored.
func (c *Client) UploadFile(ctx context.Context, project, name string, content []byte) (Upload, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, quoteEscaper.Replace(name)))
	h.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(h)
	if err != nil {
		return Upload{}, err
	}
	if _, err := part.Write(content); err != nil {
		return Upload{}, err
	}
	if err := mw.Close(); err != nil {
		return Upload{}, err
	}
	path := projectPath(project) + "/uploads"
	var up Upload
	_, err = c.call(ctx, request{method: http.MethodPost, url: c.baseURL + path, path: path, payload: buf.Bytes(), contentType: mw.FormDataContentType()}, &up)
	return up, err
}

var quoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "")

// DownloadUpload reads an upload of the project. It fails with ErrTooLarge
// when the file is larger than max bytes. A missing file is an *Error with
// status 404. The secret stays out of errors.
func (c *Client) DownloadUpload(ctx context.Context, project, secret, filename string, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, errors.New("gitlab: DownloadUpload needs a positive limit")
	}
	path := projectPath(project) + "/uploads/" + url.PathEscape(secret) + "/" + url.PathEscape(filename)
	var data []byte
	_, err := c.call(ctx, request{method: http.MethodGet, url: c.baseURL + path, path: projectPath(project) + "/uploads/…", safe: true, maxBody: max}, &data)
	return data, err
}

func (c *Client) GetProject(ctx context.Context, project string) (*Project, error) {
	var p Project
	_, err := c.do(ctx, http.MethodGet, projectPath(project), nil, nil, &p)
	return &p, err
}

func (c *Client) ListMilestones(ctx context.Context, project string) ([]Milestone, error) {
	return list[Milestone](ctx, c, projectPath(project)+"/milestones", url.Values{"state": {"active"}})
}

// ListClosedMilestones lists the closed milestones, reading at most
// maxPages pages of 100 (0: up to the client's limit).
func (c *Client) ListClosedMilestones(ctx context.Context, project string, maxPages int) ([]Milestone, error) {
	return listPages[Milestone](ctx, c, projectPath(project)+"/milestones", url.Values{"state": {"closed"}}, maxPages)
}

func (c *Client) GetMilestone(ctx context.Context, project string, id int64) (*Milestone, error) {
	var m Milestone
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/milestones/%d", projectPath(project), id), nil, nil, &m)
	return &m, err
}

// GetMilestoneByIID returns the milestone %iid, open or closed; a 404 Error
// when the project has none.
func (c *Client) GetMilestoneByIID(ctx context.Context, project string, iid int64) (*Milestone, error) {
	path := projectPath(project) + "/milestones"
	var ms []Milestone
	// Without state GitLab lists open and closed milestones.
	if _, err := c.do(ctx, http.MethodGet, path, url.Values{"iids[]": {strconv.FormatInt(iid, 10)}}, nil, &ms); err != nil {
		return nil, err
	}
	for _, m := range ms {
		if m.IID == iid {
			return &m, nil
		}
	}
	return nil, &Error{StatusCode: http.StatusNotFound, Method: http.MethodGet, Path: fmt.Sprintf("%s?iids[]=%d", path, iid), Body: "no such milestone"}
}

func (c *Client) UpdateMilestoneDescription(ctx context.Context, project string, id int64, description string) (*Milestone, error) {
	var m Milestone
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/milestones/%d", projectPath(project), id), nil, map[string]string{"description": description}, &m)
	return &m, err
}

func (c *Client) CloseMilestone(ctx context.Context, project string, id int64) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/milestones/%d", projectPath(project), id), nil, map[string]string{"state_event": "close"}, nil)
	return err
}

func (c *Client) CreateMilestone(ctx context.Context, project string, opts CreateMilestoneOptions) (*Milestone, error) {
	var m Milestone
	_, err := c.do(ctx, http.MethodPost, projectPath(project)+"/milestones", nil, opts, &m)
	return &m, err
}

// ErrMilestoneFilter is returned by ListIssues when Milestone and
// NoMilestone are both set.
var ErrMilestoneFilter = errors.New("gitlab: ListIssuesOptions sets both Milestone and NoMilestone")

func (c *Client) ListIssues(ctx context.Context, project string, opts ListIssuesOptions) ([]Issue, error) {
	if opts.NoMilestone && opts.Milestone != "" {
		return nil, ErrMilestoneFilter
	}
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
	if opts.NoMilestone {
		q.Set("milestone", "None")
	}
	if opts.NewestFirst {
		q.Set("order_by", "created_at")
		q.Set("sort", "desc")
	}
	if opts.UpdatedAfter != nil {
		q.Set("updated_after", opts.UpdatedAfter.UTC().Format(time.RFC3339))
	}
	return listPages[Issue](ctx, c, projectPath(project)+"/issues", q, opts.MaxPages)
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
	if opts.MilestoneID != 0 {
		body["milestone_id"] = opts.MilestoneID
	}
	var i Issue
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/issues/%d", projectPath(project), iid), nil, body, &i)
	return &i, err
}

func (c *Client) ListIssueNotes(ctx context.Context, project string, iid int64) ([]Note, error) {
	q := url.Values{"sort": {"asc"}, "order_by": {"created_at"}}
	return list[Note](ctx, c, fmt.Sprintf("%s/issues/%d/notes", projectPath(project), iid), q)
}

// ListIssueNotesNewest lists the issue's newest n notes (1 to 100), newest
// first, in one request.
func (c *Client) ListIssueNotesNewest(ctx context.Context, project string, iid int64, n int) ([]Note, error) {
	q := url.Values{"sort": {"desc"}, "order_by": {"created_at"}, "per_page": {strconv.Itoa(min(max(n, 1), 100))}}
	var notes []Note
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues/%d/notes", projectPath(project), iid), q, nil, &notes)
	return notes, err
}

func (c *Client) ListIssueDiscussions(ctx context.Context, project string, iid int64) ([]Discussion, error) {
	return list[Discussion](ctx, c, fmt.Sprintf("%s/issues/%d/discussions", projectPath(project), iid), nil)
}

func (c *Client) CreateIssueNote(ctx context.Context, project string, iid int64, body string) (*Note, error) {
	var n Note
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/notes", projectPath(project), iid), nil, map[string]string{"body": body}, &n)
	return &n, err
}

// ReplyIssueDiscussion adds a comment to an issue's thread.
func (c *Client) ReplyIssueDiscussion(ctx context.Context, project string, iid int64, discussion, body string) (*Note, error) {
	var n Note
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/discussions/%s/notes", projectPath(project), iid, url.PathEscape(discussion)), nil, map[string]string{"body": body}, &n)
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

// DeleteBranch deletes a branch of the project's repository.
func (c *Client) DeleteBranch(ctx context.Context, project, branch string) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/repository/branches/%s", projectPath(project), url.PathEscape(branch)), nil, nil, nil)
	return err
}

func (c *Client) ListMergeRequestDiffs(ctx context.Context, project string, iid int64) ([]Diff, error) {
	return list[Diff](ctx, c, fmt.Sprintf("%s/merge_requests/%d/diffs", projectPath(project), iid), nil)
}

// ListMergeRequestVersions lists the versions of a merge request, newest
// first.
func (c *Client) ListMergeRequestVersions(ctx context.Context, project string, iid int64) ([]MergeRequestVersion, error) {
	return list[MergeRequestVersion](ctx, c, fmt.Sprintf("%s/merge_requests/%d/versions", projectPath(project), iid), nil)
}

// GetMergeRequestVersion returns one version with its commits and diffs.
func (c *Client) GetMergeRequestVersion(ctx context.Context, project string, iid, versionID int64) (*MergeRequestVersionDetail, error) {
	var v MergeRequestVersionDetail
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/merge_requests/%d/versions/%d", projectPath(project), iid, versionID), nil, nil, &v)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// GetBranch returns a branch of the project's repository with its head.
func (c *Client) GetBranch(ctx context.Context, project, branch string) (*Branch, error) {
	var b Branch
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/repository/branches/%s", projectPath(project), url.PathEscape(branch)), nil, nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// CompareCommits compares two refs (not straight: from the merge base of
// from and to).
func (c *Client) CompareCommits(ctx context.Context, project, from, to string) (*Compare, error) {
	var cmp Compare
	q := url.Values{"from": {from}, "to": {to}, "straight": {"false"}}
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/repository/compare", projectPath(project)), q, nil, &cmp)
	if err != nil {
		return nil, err
	}
	return &cmp, nil
}

func (c *Client) ListIssueLinks(ctx context.Context, project string, iid int64) ([]LinkedIssue, error) {
	return list[LinkedIssue](ctx, c, fmt.Sprintf("%s/issues/%d/links", projectPath(project), iid), nil)
}

// CreateIssueLink links issue iid to targetIID of the same project; linkType
// is seen from iid (is_blocked_by: iid waits for targetIID).
func (c *Client) CreateIssueLink(ctx context.Context, project string, iid, targetIID int64, linkType string) error {
	body := map[string]any{"target_project_id": project, "target_issue_iid": targetIID, "link_type": linkType}
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/links", projectPath(project), iid), nil, body, nil)
	return err
}

func (c *Client) DeleteIssueLink(ctx context.Context, project string, iid, linkID int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/issues/%d/links/%d", projectPath(project), iid, linkID), nil, nil, nil)
	return err
}

// ListIssueStateEvents lists the state changes of an issue, oldest first.
func (c *Client) ListIssueStateEvents(ctx context.Context, project string, iid int64) ([]StateEvent, error) {
	return list[StateEvent](ctx, c, fmt.Sprintf("%s/issues/%d/resource_state_events", projectPath(project), iid), nil)
}

// ListIssueLabelEvents lists the label changes of an issue, oldest first.
func (c *Client) ListIssueLabelEvents(ctx context.Context, project string, iid int64) ([]LabelEvent, error) {
	return list[LabelEvent](ctx, c, fmt.Sprintf("%s/issues/%d/resource_label_events", projectPath(project), iid), nil)
}

// ListRepositoryTree lists the entries under path ("" for the root) at ref
// ("" for the default branch); with recursive, everything below it.
func (c *Client) ListRepositoryTree(ctx context.Context, project, path, ref string, recursive bool) ([]TreeEntry, error) {
	q := url.Values{}
	if path != "" {
		q.Set("path", path)
	}
	if ref != "" {
		q.Set("ref", ref)
	}
	if recursive {
		q.Set("recursive", "true")
	}
	return list[TreeEntry](ctx, c, projectPath(project)+"/repository/tree", q)
}

// ListTags lists the newest tags of the project's repository (by update),
// with the commits they point to.
func (c *Client) ListTags(ctx context.Context, project string, opts ListTagsOptions) ([]Tag, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{"order_by": {"updated"}, "sort": {"desc"}, "per_page": {strconv.Itoa(limit)}}
	var tags []Tag
	_, err := c.do(ctx, http.MethodGet, projectPath(project)+"/repository/tags", q, nil, &tags)
	return tags, err
}

// ListCommitRefs lists the branches or tags (refType "branch", "tag", or
// "all") that contain the commit sha.
func (c *Client) ListCommitRefs(ctx context.Context, project, sha, refType string) ([]CommitRef, error) {
	q := url.Values{}
	if refType != "" {
		q.Set("type", refType)
	}
	return list[CommitRef](ctx, c, fmt.Sprintf("%s/repository/commits/%s/refs", projectPath(project), url.PathEscape(sha)), q)
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

func (c *Client) UpdateMergeRequestNote(ctx context.Context, project string, iid, noteID int64, body string) (*Note, error) {
	var n Note
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/merge_requests/%d/notes/%d", projectPath(project), iid, noteID), nil, map[string]string{"body": body}, &n)
	return &n, err
}

// CreateMergeRequestPipeline runs a merge request pipeline for the MR's head.
func (c *Client) CreateMergeRequestPipeline(ctx context.Context, project string, iid int64) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/merge_requests/%d/pipelines", projectPath(project), iid), nil, nil, nil)
	return err
}

// CreatePipeline runs a pipeline for the head of a branch.
// With variables they are set as environment variables of the pipeline.
func (c *Client) CreatePipeline(ctx context.Context, project, ref string, vars ...PipelineVariable) (*Pipeline, error) {
	type variable struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Type  string `json:"variable_type"`
	}
	body := struct {
		Ref       string     `json:"ref"`
		Variables []variable `json:"variables,omitempty"`
	}{Ref: ref}
	for _, v := range vars {
		body.Variables = append(body.Variables, variable{Key: v.Key, Value: v.Value, Type: "env_var"})
	}
	var p Pipeline
	if _, err := c.do(ctx, http.MethodPost, projectPath(project)+"/pipeline", nil, body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateRelease creates the release tag at ref (a SHA) together with its
// GitLab release, named tag, with description as its notes. A tag push
// pipeline runs if the project's CI defines one. An existing tag is a 409 or
// a 400.
func (c *Client) CreateRelease(ctx context.Context, project, tag, ref, description string) error {
	body := struct {
		TagName     string `json:"tag_name"`
		Ref         string `json:"ref"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}{tag, ref, tag, description}
	_, err := c.do(ctx, http.MethodPost, projectPath(project)+"/releases", nil, body, nil)
	return err
}

// GetPipeline reads one pipeline.
func (c *Client) GetPipeline(ctx context.Context, project string, id int64) (*Pipeline, error) {
	var p Pipeline
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pipelines/%d", projectPath(project), id), nil, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) DeleteMergeRequestNote(ctx context.Context, project string, iid, noteID int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/merge_requests/%d/notes/%d", projectPath(project), iid, noteID), nil, nil, nil)
	return err
}

func (c *Client) CreateMergeRequestNote(ctx context.Context, project string, iid int64, body string) (*Note, error) {
	var n Note
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/merge_requests/%d/notes", projectPath(project), iid), nil, map[string]string{"body": body}, &n)
	return &n, err
}

func (c *Client) ListMergeRequestDiscussions(ctx context.Context, project string, iid int64) ([]Discussion, error) {
	return list[Discussion](ctx, c, fmt.Sprintf("%s/merge_requests/%d/discussions", projectPath(project), iid), nil)
}

// ReplyMergeRequestDiscussion adds a comment to a merge request's thread.
func (c *Client) ReplyMergeRequestDiscussion(ctx context.Context, project string, iid int64, discussion, body string) (*Note, error) {
	var n Note
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/merge_requests/%d/discussions/%s/notes", projectPath(project), iid, url.PathEscape(discussion)), nil, map[string]string{"body": body}, &n)
	return &n, err
}

func (c *Client) ListProjectHooks(ctx context.Context, project string) ([]ProjectHook, error) {
	return list[ProjectHook](ctx, c, projectPath(project)+"/hooks", nil)
}

func (c *Client) AddProjectHook(ctx context.Context, project string, opts ProjectHookOptions) (*ProjectHook, error) {
	var h ProjectHook
	_, err := c.do(ctx, http.MethodPost, projectPath(project)+"/hooks", nil, opts, &h)
	return &h, err
}

func (c *Client) EditProjectHook(ctx context.Context, project string, id int64, opts ProjectHookOptions) (*ProjectHook, error) {
	var h ProjectHook
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("%s/hooks/%d", projectPath(project), id), nil, opts, &h)
	return &h, err
}

func (c *Client) DeleteProjectHook(ctx context.Context, project string, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/hooks/%d", projectPath(project), id), nil, nil, nil)
	return err
}

func (c *Client) TestProjectHook(ctx context.Context, project string, id int64, trigger string) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/hooks/%d/test/%s", projectPath(project), id, url.PathEscape(trigger)), nil, nil, nil)
	return err
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

// maxTrace bounds how much of a job log is kept; the end of the log is what
// matters. Memory stays O(maxTrace) while reading (see readTail).
const maxTrace = 4 << 20

func (c *Client) JobTrace(ctx context.Context, project string, jobID int64) (string, error) {
	path := fmt.Sprintf("%s/jobs/%d/trace", projectPath(project), jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &Error{StatusCode: resp.StatusCode, Method: http.MethodGet, Path: path}
	}
	data, err := readTail(resp.Body, maxTrace)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readTail returns the last max bytes of r, or all of r if shorter. Peak
// memory is bounded by a small multiple of max (ring + read chunk), not by
// the size of r.
func readTail(r io.Reader, max int) ([]byte, error) {
	if max <= 0 {
		_, err := io.Copy(io.Discard, r)
		return nil, err
	}
	ring := make([]byte, max)
	chunk := make([]byte, 32<<10)
	var off int
	var filled bool
	for {
		n, err := r.Read(chunk)
		for i := 0; i < n; i++ {
			ring[off] = chunk[i]
			off++
			if off == max {
				off = 0
				filled = true
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if !filled {
		out := make([]byte, off)
		copy(out, ring[:off])
		return out, nil
	}
	out := make([]byte, max)
	copy(out, ring[off:])
	copy(out[max-off:], ring[:off])
	return out, nil
}

func (c *Client) RetryPipeline(ctx context.Context, project string, pipelineID int64) (*Pipeline, error) {
	var p Pipeline
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/pipelines/%d/retry", projectPath(project), pipelineID), nil, nil, &p)
	return &p, err
}

// draftPrefix matches the title prefixes that make GitLab treat an MR as a
// draft.
var draftPrefix = regexp.MustCompile(`(?i)^\s*(?:\[draft\]|\(draft\)|draft:)\s*`)

// ReadyTitle is title without its draft prefix: setting it marks the MR ready.
func ReadyTitle(title string) string { return draftPrefix.ReplaceAllString(title, "") }

// DraftTitle is title with a draft prefix: setting it marks the MR draft.
func DraftTitle(title string) string {
	if draftPrefix.MatchString(title) {
		return title
	}
	return "Draft: " + title
}

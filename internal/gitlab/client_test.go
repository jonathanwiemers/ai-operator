package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestListIssuesPaginatesAndFilters(t *testing.T) {
	var gotQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			t.Errorf("missing token header")
		}
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fapp/issues" {
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
		}
		gotQueries = append(gotQueries, r.URL.RawQuery)
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("X-Next-Page", "2")
			_ = json.NewEncoder(w).Encode([]Issue{{IID: 1}, {IID: 2}})
		default:
			_ = json.NewEncoder(w).Encode([]Issue{{IID: 3, Labels: []string{"agent::ready"}}})
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	issues, err := c.ListIssues(context.Background(), "group/app", ListIssuesOptions{Labels: []string{"agent::ready"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 || !issues[2].HasLabel("agent::ready") {
		t.Fatalf("unexpected issues %+v", issues)
	}
	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 pages, got %v", gotQueries)
	}
	q := gotQueries[0]
	for _, want := range []string{"labels=agent%3A%3Aready", "state=opened", "per_page=100"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q misses %q", q, want)
		}
	}
}

func TestListIssuesUpdatedAfter(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("updated_after")
		_ = json.NewEncoder(w).Encode([]Issue{})
	}))
	defer srv.Close()
	after := time.Date(2026, 10, 20, 12, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	if _, err := New(srv.URL, "tok").ListIssues(context.Background(), "group/app", ListIssuesOptions{State: "closed", UpdatedAfter: &after}); err != nil {
		t.Fatal(err)
	}
	if got != "2026-10-20T10:00:00Z" {
		t.Fatalf("updated_after = %q", got)
	}
}

func TestUpdateIssueSendsLabelDeltas(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method %s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(Issue{IID: 7})
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok").UpdateIssue(context.Background(), "g/p", 7, UpdateIssueOptions{
		AddLabels: []string{"agent::working"}, RemoveLabels: []string{"agent::ready", "agent::review"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if body["add_labels"] != "agent::working" || body["remove_labels"] != "agent::ready,agent::review" {
		t.Fatalf("unexpected body %v", body)
	}
	if _, ok := body["title"]; ok {
		t.Fatalf("empty title must not be sent: %v", body)
	}
	if _, ok := body["assignee_ids"]; ok {
		t.Fatalf("no assignee_ids must be sent unless set, they replace all assignees: %v", body)
	}
}

func TestUpdateIssueSendsAssignees(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(Issue{IID: 7, Assignees: []User{{ID: 999, Username: "ai-bot"}}})
	}))
	defer srv.Close()

	is, err := New(srv.URL, "tok").UpdateIssue(context.Background(), "g/p", 7, UpdateIssueOptions{
		AddLabels: []string{"agent::working"}, AssigneeIDs: []int64{999}, MilestoneID: 41,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids, ok := body["assignee_ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != float64(999) || body["add_labels"] != "agent::working" || body["milestone_id"] != float64(41) {
		t.Fatalf("unexpected body %v", body)
	}
	if len(is.Assignees) != 1 || is.Assignees[0].Username != "ai-bot" {
		t.Fatalf("assignees not decoded: %+v", is.Assignees)
	}
}

func TestCreateMergeRequestMilestone(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/v4/projects/g%2Fp/merge_requests" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.EscapedPath())
		}
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(MergeRequest{IID: 1, Milestone: &Milestone{ID: 11}})
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	opts := CreateMergeRequestOptions{SourceBranch: "agent/7-x", TargetBranch: "main", Title: "Draft: x"}

	if _, err := c.CreateMergeRequest(context.Background(), "g/p", opts); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["milestone_id"]; ok {
		t.Fatalf("milestone_id must not be sent without a milestone: %v", body)
	}

	opts.MilestoneID = 11
	mr, err := c.CreateMergeRequest(context.Background(), "g/p", opts)
	if err != nil {
		t.Fatal(err)
	}
	if body["milestone_id"] != float64(11) {
		t.Fatalf("unexpected body %v", body)
	}
	if mr.Milestone == nil || mr.Milestone.ID != 11 {
		t.Fatalf("milestone not decoded: %+v", mr.Milestone)
	}
}

func TestUpdateIssueNote(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method %s", r.Method)
		}
		path = r.URL.EscapedPath()
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(Note{ID: 55, Body: "new"})
	}))
	defer srv.Close()

	n, err := New(srv.URL, "tok").UpdateIssueNote(context.Background(), "g/p", 7, 55, "new")
	if err != nil || n.ID != 55 {
		t.Fatalf("note %+v err %v", n, err)
	}
	if !strings.HasSuffix(path, "/projects/g%2Fp/issues/7/notes/55") || body["body"] != "new" {
		t.Fatalf("unexpected request %s %v", path, body)
	}
}

func TestReplyIssueDiscussion(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(Note{ID: 56, Body: "hi"})
	}))
	defer srv.Close()

	n, err := New(srv.URL, "tok").ReplyIssueDiscussion(context.Background(), "g/p", 7, "abc123", "hi")
	if err != nil || n.ID != 56 {
		t.Fatalf("note %+v err %v", n, err)
	}
	if method != http.MethodPost || !strings.HasSuffix(path, "/projects/g%2Fp/issues/7/discussions/abc123/notes") || body["body"] != "hi" {
		t.Fatalf("unexpected request %s %s %v", method, path, body)
	}
}

func TestErrorsAndNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"404 Not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").GetIssue(context.Background(), "g/p", 1)
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestPipelinesJobsAndTrace(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		switch {
		case strings.HasSuffix(r.URL.Path, "/pipelines"):
			_ = json.NewEncoder(w).Encode([]Pipeline{{ID: 7, SHA: "abc", Status: "failed"}})
		case strings.HasSuffix(r.URL.Path, "/pipelines/7/jobs"):
			_ = json.NewEncoder(w).Encode([]Job{{ID: 70, Name: "test", Status: "failed"}})
		case strings.HasSuffix(r.URL.Path, "/jobs/70/trace"):
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("FAIL: TestX\n"))
		case strings.HasSuffix(r.URL.Path, "/pipelines/7/retry"):
			_ = json.NewEncoder(w).Encode(Pipeline{ID: 7, Status: "running"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	ps, err := c.ListPipelines(ctx, "g/p", ListPipelinesOptions{SHA: "abc", Limit: 1})
	if err != nil || len(ps) != 1 || ps[0].Status != "failed" {
		t.Fatalf("pipelines: %+v %v", ps, err)
	}
	jobs, err := c.ListPipelineJobs(ctx, "g/p", 7, "failed")
	if err != nil || len(jobs) != 1 || jobs[0].Name != "test" {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	trace, err := c.JobTrace(ctx, "g/p", 70)
	if err != nil || trace != "FAIL: TestX\n" {
		t.Fatalf("trace: %q %v", trace, err)
	}
	if p, err := c.RetryPipeline(ctx, "g/p", 7); err != nil || p.Status != "running" {
		t.Fatalf("retry: %+v %v", p, err)
	}
	all := strings.Join(queries, "\n")
	for _, want := range []string{"sha=abc", "per_page=1", "order_by=id", "scope%5B%5D=failed", "POST /api/v4/projects/g%2Fp/pipelines/7/retry"} {
		if !strings.Contains(all, want) {
			t.Errorf("requests miss %q:\n%s", want, all)
		}
	}
}

func TestMergeRequestActions(t *testing.T) {
	type call struct {
		method, path, query string
		body                map[string]any
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{method: r.Method, path: r.URL.EscapedPath(), query: r.URL.RawQuery}
		_ = json.NewDecoder(r.Body).Decode(&c.body)
		calls = append(calls, c)
		_ = json.NewEncoder(w).Encode(MergeRequest{IID: 3, State: "merged", SHA: "abc", DetailedMergeStatus: "mergeable", MergeCommitSHA: "m1",
			DiffRefs: &DiffRefs{BaseSHA: "b1", HeadSHA: "abc", StartSHA: "b1"}})
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	mr, err := c.GetMergeRequest(ctx, "g/p", 3)
	if err != nil || mr.SHA != "abc" || mr.DetailedMergeStatus != "mergeable" || mr.DiffRefs == nil || mr.DiffRefs.BaseSHA != "b1" {
		t.Fatalf("get: %+v %v", mr, err)
	}
	if _, err := c.UpdateMergeRequest(ctx, "g/p", 3, UpdateMergeRequestOptions{Title: "Ready"}); err != nil {
		t.Fatal(err)
	}
	if mr, err = c.MergeMergeRequest(ctx, "g/p", 3, MergeOptions{SHA: "abc", ShouldRemoveSourceBranch: true}); err != nil || mr.MergeCommitSHA != "m1" {
		t.Fatalf("merge: %+v %v", mr, err)
	}
	if err := c.RebaseMergeRequest(ctx, "g/p", 3); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteBranch(ctx, "g/p", "agent/3-x"); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{method: "GET", path: "/api/v4/projects/g%2Fp/merge_requests/3", query: "include_rebase_in_progress=true"},
		{method: "PUT", path: "/api/v4/projects/g%2Fp/merge_requests/3", body: map[string]any{"title": "Ready"}},
		{method: "PUT", path: "/api/v4/projects/g%2Fp/merge_requests/3/merge", body: map[string]any{"sha": "abc", "should_remove_source_branch": true}},
		{method: "PUT", path: "/api/v4/projects/g%2Fp/merge_requests/3/rebase"},
		{method: "DELETE", path: "/api/v4/projects/g%2Fp/repository/branches/agent%2F3-x"},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls %+v", calls)
	}
	for i := range want {
		if calls[i].method != want[i].method || calls[i].path != want[i].path || calls[i].query != want[i].query ||
			fmt.Sprint(calls[i].body) != fmt.Sprint(want[i].body) {
			t.Fatalf("call %d: got %+v, want %+v", i, calls[i], want[i])
		}
	}
	if StatusCode(&Error{StatusCode: 406}) != 406 || StatusCode(nil) != 0 {
		t.Fatal("StatusCode")
	}
}

func TestMergeRequestDiffsAndStateEvents(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		if strings.HasSuffix(r.URL.Path, "/diffs") {
			_ = json.NewEncoder(w).Encode([]Diff{{NewPath: "a.go", Diff: "+x\n", TooLarge: true}})
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"state":"closed","source_merge_request":{"iid":3,"project_id":1}}]`))
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ds, err := c.ListMergeRequestDiffs(context.Background(), "group/app", 3)
	if err != nil || len(ds) != 1 || !ds[0].TooLarge {
		t.Fatalf("diffs %+v %v", ds, err)
	}
	evs, err := c.ListIssueStateEvents(context.Background(), "group/app", 4)
	if err != nil || len(evs) != 1 || evs[0].State != "closed" || evs[0].SourceMergeRequest == nil || evs[0].SourceMergeRequest.IID != 3 {
		t.Fatalf("state events %+v %v", evs, err)
	}
	want := []string{"/api/v4/projects/group%2Fapp/merge_requests/3/diffs", "/api/v4/projects/group%2Fapp/issues/4/resource_state_events"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths %v", paths)
	}
}

// traceBody builds n bytes of recognizable content: one "%08d\n" line per
// counter value, truncated to exactly n bytes.
func traceBody(n int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%08d\n", i)
	}
	return b.Bytes()[:n]
}

func TestJobTraceTail(t *testing.T) {
	cases := []struct {
		name string
		size int
	}{
		{"1KiB", 1 << 10},
		{"exact4MiB", maxTrace},
		{"10MiB", 10 << 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := traceBody(tc.size)
			want := body
			if len(want) > maxTrace {
				want = want[len(want)-maxTrace:]
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/jobs/70/trace") {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			got, err := New(srv.URL, "tok").JobTrace(context.Background(), "g/p", 70)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("len=%d want len=%d (prefix %q vs %q)", len(got), len(want), got[:min(32, len(got))], string(want[:min(32, len(want))]))
			}
		})
	}
}

func TestJobTraceNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tok").JobTrace(context.Background(), "g/p", 70)
	var ge *Error
	if !errors.As(err, &ge) || ge.StatusCode != http.StatusForbidden {
		t.Fatalf("want *Error 403, got %v", err)
	}
}

// genTrace streams size bytes without holding them; each byte is pos%251.
type genTrace struct {
	left, pos int
}

func (g *genTrace) Read(p []byte) (int, error) {
	if g.left == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > g.left {
		n = g.left
	}
	for i := 0; i < n; i++ {
		p[i] = byte((g.pos + i) % 251)
	}
	g.pos += n
	g.left -= n
	return n, nil
}

func TestJobTraceBoundedMemory(t *testing.T) {
	const bodySize = 64 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Stream so the server side also stays bounded.
		_, _ = io.Copy(w, &genTrace{left: bodySize})
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	trace, err := c.JobTrace(context.Background(), "g/p", 70)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace) != maxTrace {
		t.Fatalf("got %d bytes, want %d", len(trace), maxTrace)
	}
	// Last byte of a 64 MiB stream of pos%251 is (bodySize-1)%251.
	if trace[len(trace)-1] != byte((bodySize-1)%251) {
		t.Fatalf("tail byte %d, want %d", trace[len(trace)-1], (bodySize-1)%251)
	}
	if trace[0] != byte((bodySize-maxTrace)%251) {
		t.Fatalf("head of tail %d, want %d", trace[0], (bodySize-maxTrace)%251)
	}
	alloc := after.TotalAlloc - before.TotalAlloc
	// Far below the body: a ReadAll implementation allocates ~bodySize.
	const budget = 20 << 20
	if alloc > budget {
		t.Fatalf("TotalAlloc grew by %d bytes for a %d-byte body (budget %d); JobTrace must not ReadAll", alloc, bodySize, budget)
	}
}

func TestIssueLinks(t *testing.T) {
	var calls []string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`[{"iid":1,"project_id":7,"state":"opened","issue_link_id":33,"link_type":"is_blocked_by"}]`))
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()
	links, err := c.ListIssueLinks(ctx, "group/app", 2)
	if err != nil || len(links) != 1 || links[0].IssueLinkID != 33 || links[0].LinkType != LinkIsBlockedBy {
		t.Fatalf("links %+v %v", links, err)
	}
	if err := c.CreateIssueLink(ctx, "group/app", 2, 1, LinkIsBlockedBy); err != nil {
		t.Fatal(err)
	}
	if body["target_project_id"] != "group/app" || body["target_issue_iid"] != float64(1) || body["link_type"] != "is_blocked_by" {
		t.Fatalf("create body %v", body)
	}
	if err := c.DeleteIssueLink(ctx, "group/app", 2, 33); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /api/v4/projects/group%2Fapp/issues/2/links", "POST /api/v4/projects/group%2Fapp/issues/2/links",
		"DELETE /api/v4/projects/group%2Fapp/issues/2/links/33"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls %v", calls)
	}
}

func TestProjectHooks(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":5,"url":"https://op.example/hooks/gitlab","issues_events":true,"note_events":true,"merge_requests_events":true,"pipeline_events":true,"push_events":false,"enable_ssl_verification":true}]`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(ProjectHook{ID: 6, URL: "https://op.example/hooks/gitlab"})
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	hooks, err := c.ListProjectHooks(ctx, "g/p")
	if err != nil {
		t.Fatal(err)
	}
	want := ProjectHook{ID: 5, URL: "https://op.example/hooks/gitlab", IssuesEvents: true, NoteEvents: true, MergeRequestsEvents: true, PipelineEvents: true, EnableSSLVerification: true}
	if method != http.MethodGet || path != "/api/v4/projects/g%2Fp/hooks" || len(hooks) != 1 || hooks[0] != want {
		t.Fatalf("list: %s %s %+v", method, path, hooks)
	}

	// Every field is sent, false ones too, so an edit leaves nothing behind.
	opts := ProjectHookOptions{URL: "https://op.example/hooks/gitlab", Token: "s3cret", IssuesEvents: true, NoteEvents: true, EnableSSLVerification: true}
	wantBody := map[string]any{"url": "https://op.example/hooks/gitlab", "token": "s3cret", "issues_events": true, "note_events": true,
		"merge_requests_events": false, "pipeline_events": false, "push_events": false, "enable_ssl_verification": true}
	h, err := c.AddProjectHook(ctx, "g/p", opts)
	if err != nil || h.ID != 6 {
		t.Fatalf("add: %+v %v", h, err)
	}
	if method != http.MethodPost || path != "/api/v4/projects/g%2Fp/hooks" || !reflect.DeepEqual(body, wantBody) {
		t.Fatalf("add: %s %s %v", method, path, body)
	}

	if h, err = c.EditProjectHook(ctx, "g/p", 6, opts); err != nil || h.ID != 6 {
		t.Fatalf("edit: %+v %v", h, err)
	}
	if method != http.MethodPut || path != "/api/v4/projects/g%2Fp/hooks/6" || !reflect.DeepEqual(body, wantBody) {
		t.Fatalf("edit: %s %s %v", method, path, body)
	}

	if err := c.DeleteProjectHook(ctx, "g/p", 6); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodDelete || path != "/api/v4/projects/g%2Fp/hooks/6" {
		t.Fatalf("delete: %s %s", method, path)
	}
}

func TestListIssueLabelEvents(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		_, _ = w.Write([]byte(`[{"id":5,"user":{"id":7,"username":"alice"},"label":{"id":2,"name":"agent-repo::group/infra"},"action":"add"},` +
			`{"id":6,"user":{"id":8,"username":"bob"},"label":null,"action":"remove"}]`))
	}))
	defer srv.Close()
	evs, err := New(srv.URL, "tok").ListIssueLabelEvents(context.Background(), "group/app", 4)
	if err != nil || len(evs) != 2 || evs[0].User.Username != "alice" || evs[0].LabelName() != "agent-repo::group/infra" || evs[0].Action != "add" ||
		evs[1].LabelName() != "" {
		t.Fatalf("label events %+v %v", evs, err)
	}
	if !slices.Equal(paths, []string{"/api/v4/projects/group%2Fapp/issues/4/resource_label_events"}) {
		t.Fatalf("paths %v", paths)
	}
}

func TestListRepositoryTree(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.EscapedPath()+"?"+r.URL.Query().Get("path")+"|"+r.URL.Query().Get("ref")+"|"+r.URL.Query().Get("recursive")+"|"+r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte(`[{"path":"clusters/a","name":"a","type":"tree"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"path":"clusters/a/x.yaml","name":"x.yaml","type":"blob"}]`))
	}))
	defer srv.Close()
	es, err := New(srv.URL, "tok").ListRepositoryTree(context.Background(), "group/infra", "clusters", "main", true)
	if err != nil || len(es) != 2 || es[1].Path != "clusters/a/x.yaml" || es[1].Type != "blob" || es[0].Type != "tree" {
		t.Fatalf("tree %+v %v", es, err)
	}
	want := []string{"/api/v4/projects/group%2Finfra/repository/tree?clusters|main|true|1", "/api/v4/projects/group%2Finfra/repository/tree?clusters|main|true|2"}
	if !slices.Equal(queries, want) {
		t.Fatalf("queries %v", queries)
	}
}

func TestListTags(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath() + "?" + r.URL.RawQuery
		_, _ = w.Write([]byte(`[{"name":"2026.11.3","commit":{"id":"m1","parent_ids":["a","b"],"created_at":"2026-11-02T10:00:00.000+01:00"}},
			{"name":"old","commit":{"id":"c1","parent_ids":["a"]}}]`))
	}))
	defer srv.Close()
	tags, err := New(srv.URL, "tok").ListTags(context.Background(), "group/app", ListTagsOptions{Limit: 5})
	if err != nil || len(tags) != 2 {
		t.Fatalf("tags %+v %v", tags, err)
	}
	c := tags[0].Commit
	if tags[0].Name != "2026.11.3" || c.ID != "m1" || !slices.Equal(c.ParentIDs, []string{"a", "b"}) || c.CreatedAt == nil ||
		!c.CreatedAt.Equal(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)) || tags[1].Commit.CreatedAt != nil {
		t.Fatalf("tags decoded wrong: %+v", tags)
	}
	if got != "/api/v4/projects/group%2Fapp/repository/tags?order_by=updated&per_page=5&sort=desc" {
		t.Fatalf("request %s", got)
	}
}

func TestListCommitRefs(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath()+"?"+r.URL.Query().Get("type")+"|"+r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte(`[{"type":"tag","name":"2026.11.4"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"type":"tag","name":"2026.11.3"}]`))
	}))
	defer srv.Close()
	refs, err := New(srv.URL, "tok").ListCommitRefs(context.Background(), "group/app", "abc123", "tag")
	if err != nil || len(refs) != 2 || refs[1] != (CommitRef{Type: "tag", Name: "2026.11.3"}) {
		t.Fatalf("refs %+v %v", refs, err)
	}
	want := []string{"/api/v4/projects/group%2Fapp/repository/commits/abc123/refs?tag|1", "/api/v4/projects/group%2Fapp/repository/commits/abc123/refs?tag|2"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths %v", paths)
	}
}

func TestCreateMilestone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/v4/projects/group%2Fapp/milestones" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.EscapedPath())
		}
		var got map[string]string
		_ = json.NewDecoder(r.Body).Decode(&got)
		want := map[string]string{"title": "Next", "description": "d", "due_date": "2026-12-01"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("body = %v, want %v", got, want)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":77,"iid":5,"title":"Next","state":"active","web_url":"https://gl/m/5"}`))
	}))
	defer srv.Close()

	m, err := New(srv.URL, "tok").CreateMilestone(context.Background(), "group/app", CreateMilestoneOptions{Title: "Next", Description: "d", DueDate: "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != 77 || m.IID != 5 || m.WebURL != "https://gl/m/5" {
		t.Fatalf("unexpected milestone %+v", m)
	}
}

// The backfill's listings stay within their requests: closed milestones and
// a milestone's issues stop after their page caps, the newest notes are one
// request.
func TestBackfillListingsCapPages(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("X-Next-Page", "99") // always more
		switch {
		case strings.HasSuffix(r.URL.Path, "/milestones"):
			_ = json.NewEncoder(w).Encode([]Milestone{{IID: 1, State: "closed", DueDate: "2026-01-31"}})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			_ = json.NewEncoder(w).Encode([]Note{{ID: 9}, {ID: 8}})
		default:
			_ = json.NewEncoder(w).Encode([]Issue{{IID: 1}})
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	ms, err := c.ListClosedMilestones(ctx, "group/app", 2)
	if err != nil || len(ms) != 2 || ms[0].DueDate != "2026-01-31" {
		t.Fatalf("ListClosedMilestones = %+v, %v", ms, err)
	}
	if len(requests) != 2 || !strings.Contains(requests[0], "state=closed") {
		t.Errorf("closed milestones: requests %v, want 2 with state=closed", requests)
	}

	requests = nil
	if is, err := c.ListIssues(ctx, "group/app", ListIssuesOptions{State: "all", Milestone: "M1", MaxPages: 3}); err != nil || len(is) != 3 {
		t.Fatalf("ListIssues = %d issues, %v", len(is), err)
	}
	if len(requests) != 3 || !strings.Contains(requests[0], "milestone=M1") || !strings.Contains(requests[0], "state=all") {
		t.Errorf("issues: requests %v, want 3 of milestone M1, state all", requests)
	}

	requests = nil
	notes, err := c.ListIssueNotesNewest(ctx, "group/app", 7, 500)
	if err != nil || len(notes) != 2 || notes[0].ID != 9 {
		t.Fatalf("ListIssueNotesNewest = %+v, %v", notes, err)
	}
	if len(requests) != 1 {
		t.Fatalf("notes: requests %v, want 1", requests)
	}
	for _, want := range []string{"/issues/7/notes", "sort=desc", "order_by=created_at", "per_page=100"} {
		if !strings.Contains(requests[0], want) {
			t.Errorf("notes request %q misses %q", requests[0], want)
		}
	}

	// Without a cap, the client's limit holds.
	requests = nil
	if _, err := c.ListIssues(ctx, "group/app", ListIssuesOptions{}); err != nil || len(requests) != maxPages {
		t.Errorf("uncapped ListIssues: %d requests, %v; want %d", len(requests), err, maxPages)
	}
}

func TestMergeRequestVersionsAndCompare(t *testing.T) {
	var reqs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions") && r.URL.Query().Get("page") == "1":
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte(`[{"id":9,"head_commit_sha":"h2","base_commit_sha":"b2","start_commit_sha":"s2","created_at":"2026-11-02T09:00:00Z","state":"collected"}]`))
		case strings.HasSuffix(r.URL.Path, "/versions"):
			_, _ = w.Write([]byte(`[{"id":8,"head_commit_sha":"h1","base_commit_sha":"b1","start_commit_sha":"s1","created_at":"2026-11-01T09:00:00Z","state":"collected"}]`))
		case strings.HasSuffix(r.URL.Path, "/versions/8"):
			_, _ = w.Write([]byte(`{"id":8,"head_commit_sha":"h1","commits":[{"id":"c1","title":"t","author_name":"A","author_email":"a@x","committer_email":"c@x"}],
				"diffs":[{"old_path":"a.go","new_path":"a.go","diff":"+x\n","too_large":true,"collapsed":true}]}`))
		default:
			_, _ = w.Write([]byte(`{"commits":[{"id":"c2","title":"u"}],"diffs":[{"new_path":"b.go","new_file":true}]}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()
	vs, err := c.ListMergeRequestVersions(ctx, "group/app", 3)
	if err != nil || len(vs) != 2 || vs[0].ID != 9 || vs[1].HeadCommitSHA != "h1" || vs[0].BaseCommitSHA != "b2" || vs[0].StartCommitSHA != "s2" ||
		vs[0].State != "collected" || vs[0].CreatedAt.IsZero() {
		t.Fatalf("versions %+v %v", vs, err)
	}
	d, err := c.GetMergeRequestVersion(ctx, "group/app", 3, 8)
	if err != nil || d.ID != 8 || len(d.Commits) != 1 || d.Commits[0].AuthorEmail != "a@x" || d.Commits[0].CommitterEmail != "c@x" ||
		d.Commits[0].AuthorName != "A" || len(d.Diffs) != 1 || !d.Diffs[0].TooLarge || !d.Diffs[0].Collapsed {
		t.Fatalf("detail %+v %v", d, err)
	}
	cmp, err := c.CompareCommits(ctx, "group/app", "base1", "base2")
	if err != nil || len(cmp.Commits) != 1 || cmp.Commits[0].Title != "u" || len(cmp.Diffs) != 1 || !cmp.Diffs[0].NewFile {
		t.Fatalf("compare %+v %v", cmp, err)
	}
	want := []string{
		"/api/v4/projects/group%2Fapp/merge_requests/3/versions?page=1&per_page=100",
		"/api/v4/projects/group%2Fapp/merge_requests/3/versions?page=2&per_page=100",
		"/api/v4/projects/group%2Fapp/merge_requests/3/versions/8?",
		"/api/v4/projects/group%2Fapp/repository/compare?from=base1&straight=false&to=base2",
	}
	if !slices.Equal(reqs, want) {
		t.Fatalf("requests %q", reqs)
	}
}

func TestReplyMergeRequestDiscussion(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(Note{ID: 57, Body: "hi"})
	}))
	defer srv.Close()

	n, err := New(srv.URL, "tok").ReplyMergeRequestDiscussion(context.Background(), "g/p", 7, "ab/c 1", "hi")
	if err != nil || n.ID != 57 {
		t.Fatalf("note %+v err %v", n, err)
	}
	if method != http.MethodPost || !strings.HasSuffix(path, "/projects/g%2Fp/merge_requests/7/discussions/ab%2Fc%201/notes") || body["body"] != "hi" {
		t.Fatalf("unexpected request %s %s %v", method, path, body)
	}
}

func TestListMergeRequestDiscussionsPaginates(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		paths = append(paths, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("X-Next-Page", "2")
			_ = json.NewEncoder(w).Encode([]Discussion{{ID: "a", Notes: []Note{{ID: 1}}}})
			return
		}
		_ = json.NewEncoder(w).Encode([]Discussion{{ID: "b", Notes: []Note{{ID: 2}}}})
	}))
	defer srv.Close()

	ds, err := New(srv.URL, "tok").ListMergeRequestDiscussions(context.Background(), "g/p", 7)
	if err != nil || len(ds) != 2 || ds[0].ID != "a" || ds[1].ID != "b" {
		t.Fatalf("discussions %+v err %v", ds, err)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "/projects/g%2Fp/merge_requests/7/discussions?") {
		t.Fatalf("unexpected requests %v", paths)
	}
}

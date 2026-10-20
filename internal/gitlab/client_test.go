package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		AddLabels: []string{"agent::working"}, AssigneeIDs: []int64{999},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids, ok := body["assignee_ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != float64(999) || body["add_labels"] != "agent::working" {
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

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

package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// gqlServer answers each GraphQL POST with answer, after checking the path,
// the token and the group variable, and records the requests.
func gqlServer(t *testing.T, answer func(n int, req gqlRequest, w http.ResponseWriter)) (*Client, *[]gqlRequest) {
	t.Helper()
	var (
		mu   sync.Mutex
		reqs []gqlRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/graphql" {
			t.Errorf("request %s %s, want POST /api/graphql", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "tok" {
			t.Errorf("PRIVATE-TOKEN = %q", got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if req.Variables["group"] != "jo.wiemers" {
			t.Errorf("group variable = %v", req.Variables["group"])
		}
		mu.Lock()
		reqs = append(reqs, req)
		n := len(reqs)
		mu.Unlock()
		answer(n, req, w)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "tok"), &reqs
}

func projectNodes(from, to int) []map[string]any {
	var nodes []map[string]any
	for i := from; i < to; i++ {
		nodes = append(nodes, map[string]any{
			"fullPath":   fmt.Sprintf("jo.wiemers/p%d", i),
			"archived":   i == 3,
			"milestones": map[string]any{"nodes": []any{}},
		})
	}
	return nodes
}

func page(nodes any, next bool, cursor string) map[string]any {
	return map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": next, "endCursor": cursor}}
}

func writeData(w http.ResponseWriter, score int, group map[string]any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
		"queryComplexity": map[string]any{"score": score, "limit": 250},
		"group":           group,
	}})
}

func TestDiscoverGroupPaginates(t *testing.T) {
	c, reqs := gqlServer(t, func(n int, req gqlRequest, w http.ResponseWriter) {
		switch n {
		case 1:
			if req.Variables["pCursor"] != nil || req.Variables["iCursor"] != nil {
				t.Errorf("first cursors = %v, %v, want null", req.Variables["pCursor"], req.Variables["iCursor"])
			}
			if req.Variables["label"] != "agent::ready" {
				t.Errorf("label = %v", req.Variables["label"])
			}
			nodes := projectNodes(0, 100)
			nodes[0]["milestones"] = map[string]any{"nodes": []any{map[string]any{
				"iid": "7", "title": "M7", "description": "/agent spec\n\nGoal", "updatedAt": "2026-11-16T10:00:00Z",
			}}}
			writeData(w, 68, map[string]any{
				"projects": page(nodes, true, "p1"),
				"issues": page([]any{
					map[string]any{"reference": "jo.wiemers/p1#12"},
					map[string]any{"reference": "jo.wiemers/devops/functions/pnpm#3"},
				}, false, "i1"),
			})
		case 2:
			if req.Variables["pCursor"] != "p1" {
				t.Errorf("second pCursor = %v", req.Variables["pCursor"])
			}
			if _, ok := req.Variables["iCursor"]; ok {
				t.Errorf("second request has iCursor")
			}
			writeData(w, 40, map[string]any{"projects": page(projectNodes(100, 112), false, "p2")})
		default:
			t.Errorf("request %d", n)
		}
	})
	got, err := c.DiscoverGroup(context.Background(), "jo.wiemers", "agent::ready")
	if err != nil {
		t.Fatal(err)
	}
	if len(*reqs) != 2 || got.Posts != 2 {
		t.Fatalf("%d requests, Posts %d, want 2", len(*reqs), got.Posts)
	}
	first, second := (*reqs)[0].Query, (*reqs)[1].Query
	for _, want := range []string{"projects(", "issues(", "queryComplexity", "reference(full: true)", "milestones(state: active, first: 20)"} {
		if !strings.Contains(first, want) {
			t.Errorf("first query lacks %q:\n%s", want, first)
		}
	}
	if strings.Contains(second, "issues(") || strings.Contains(second, "$label") || !strings.Contains(second, "projects(") {
		t.Errorf("second query should ask for projects only:\n%s", second)
	}
	if len(got.Projects) != 112 {
		t.Fatalf("%d projects, want 112", len(got.Projects))
	}
	if p := got.Projects[111]; p.FullPath != "jo.wiemers/p111" {
		t.Errorf("last project %q", p.FullPath)
	}
	if !got.Projects[3].Archived || got.Projects[4].Archived {
		t.Errorf("archived flags: p3 %v, p4 %v", got.Projects[3].Archived, got.Projects[4].Archived)
	}
	wantM := DiscoveredMilestone{IID: 7, Title: "M7", Description: "/agent spec\n\nGoal", UpdatedAt: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)}
	if ms := got.Projects[0].Milestones; len(ms) != 1 || ms[0] != wantM {
		t.Errorf("milestones = %+v, want %+v", ms, wantM)
	}
	wantI := []DiscoveredIssue{{Project: "jo.wiemers/p1", IID: 12}, {Project: "jo.wiemers/devops/functions/pnpm", IID: 3}}
	if fmt.Sprint(got.ReadyIssues) != fmt.Sprint(wantI) {
		t.Errorf("issues = %v, want %v", got.ReadyIssues, wantI)
	}
	if got.Complexity != (QueryComplexity{Score: 68, Limit: 250}) {
		t.Errorf("complexity = %+v, want the first POST's 68", got.Complexity)
	}
	if st := c.Stats(); st.GraphQL != 2 || st.Requests != 2 || st.Ops[OpKey{Kind: "graphql", Op: "DiscoverGroup"}] != 2 {
		t.Errorf("stats = %+v", st)
	}
}

func TestDiscoverGroupIssuesOutlastProjects(t *testing.T) {
	c, reqs := gqlServer(t, func(n int, req gqlRequest, w http.ResponseWriter) {
		if n == 1 {
			writeData(w, 30, map[string]any{
				"projects": page(projectNodes(0, 2), false, "p1"),
				"issues":   page([]any{map[string]any{"reference": "jo.wiemers/p0#1"}}, true, "i1"),
			})
			return
		}
		if req.Variables["iCursor"] != "i1" {
			t.Errorf("iCursor = %v", req.Variables["iCursor"])
		}
		writeData(w, 90, map[string]any{"issues": page([]any{map[string]any{"reference": "jo.wiemers/p1#2"}}, false, "i2")})
	})
	got, err := c.DiscoverGroup(context.Background(), "jo.wiemers", "agent::ready")
	if err != nil {
		t.Fatal(err)
	}
	if q := (*reqs)[1].Query; strings.Contains(q, "projects(") || strings.Contains(q, "$pCursor") {
		t.Errorf("second query should ask for issues only:\n%s", q)
	}
	if len(got.Projects) != 2 || len(got.ReadyIssues) != 2 {
		t.Errorf("%d projects, %d issues", len(got.Projects), len(got.ReadyIssues))
	}
	if got.Complexity.Score != 90 {
		t.Errorf("complexity %d, want the maximum 90", got.Complexity.Score)
	}
}

func TestDiscoverGroupWithoutLabelSkipsIssues(t *testing.T) {
	c, reqs := gqlServer(t, func(_ int, _ gqlRequest, w http.ResponseWriter) {
		writeData(w, 20, map[string]any{"projects": page(projectNodes(0, 1), false, "")})
	})
	if _, err := c.DiscoverGroup(context.Background(), "jo.wiemers", ""); err != nil {
		t.Fatal(err)
	}
	if q := (*reqs)[0].Query; strings.Contains(q, "issues(") {
		t.Errorf("query asks for issues without a label:\n%s", q)
	}
}

func TestDiscoverGroupErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		notFound         bool
	}{
		{"graphql errors", `{"data":null,"errors":[{"message":"Field 'x' doesn't exist"},{"message":"second"}]}`, "Field 'x' doesn't exist; second", false},
		{"group null", `{"data":{"queryComplexity":{"score":2,"limit":250},"group":null}}`, "group jo.wiemers not found or not visible", true},
		{"cursor stuck", `{"data":{"group":{"projects":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":""}}}}}`, "projects cursor does not advance", false},
		{"bad reference", `{"data":{"group":{"projects":{"nodes":[],"pageInfo":{}},"issues":{"nodes":[{"reference":"jo.wiemers/p"}],"pageInfo":{}}}}}`, `issue reference "jo.wiemers/p"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, reqs := gqlServer(t, func(_ int, _ gqlRequest, w http.ResponseWriter) { fmt.Fprint(w, tc.body) })
			_, err := c.DiscoverGroup(context.Background(), "jo.wiemers", "agent::ready")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if IsNotFound(err) != tc.notFound || IsTransient(err) {
				t.Errorf("IsNotFound %v, IsTransient %v", IsNotFound(err), IsTransient(err))
			}
			if len(*reqs) != 1 {
				t.Errorf("%d requests, want 1 (no retry)", len(*reqs))
			}
		})
	}
}

func TestDiscoverGroupCursorRepeated(t *testing.T) {
	c, reqs := gqlServer(t, func(_ int, _ gqlRequest, w http.ResponseWriter) {
		writeData(w, 20, map[string]any{"projects": page(projectNodes(0, 1), true, "same")})
	})
	_, err := c.DiscoverGroup(context.Background(), "jo.wiemers", "")
	if err == nil || !strings.Contains(err.Error(), "does not advance") || len(*reqs) != 2 {
		t.Fatalf("err = %v after %d requests, want a stuck cursor after 2", err, len(*reqs))
	}
}

func TestDiscoverGroupStopsAt50Posts(t *testing.T) {
	c, reqs := gqlServer(t, func(n int, _ gqlRequest, w http.ResponseWriter) {
		writeData(w, 20, map[string]any{"projects": page(projectNodes(0, 1), true, fmt.Sprint("p", n))})
	})
	_, err := c.DiscoverGroup(context.Background(), "jo.wiemers", "")
	if err == nil || !strings.Contains(err.Error(), "more than 50 queries") || len(*reqs) != 50 {
		t.Fatalf("err = %v after %d requests", err, len(*reqs))
	}
}

func TestDiscoverGroupRetries(t *testing.T) {
	ok := func(w http.ResponseWriter) {
		writeData(w, 20, map[string]any{"projects": page(projectNodes(0, 1), false, "")})
	}
	t.Run("429 Retry-After", func(t *testing.T) {
		c, reqs := gqlServer(t, func(n int, _ gqlRequest, w http.ResponseWriter) {
			if n == 1 {
				w.Header().Set("Retry-After", "1")
				http.Error(w, `{"message":"429 Too Many Requests"}`, http.StatusTooManyRequests)
				return
			}
			ok(w)
		})
		start := time.Now()
		got, err := c.DiscoverGroup(context.Background(), "jo.wiemers", "")
		if err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(start); waited < time.Second {
			t.Errorf("retried after %v, want Retry-After's 1s", waited)
		}
		st := c.Stats()
		if len(*reqs) != 2 || got.Posts != 1 || st.GraphQL != 2 || st.Requests != 2 || st.Retries != 1 {
			t.Errorf("%d requests, Posts %d, stats %+v", len(*reqs), got.Posts, st)
		}
	})
	t.Run("500 up to the REST limit", func(t *testing.T) {
		defer SetRetryBackoff(time.Millisecond)()
		c, reqs := gqlServer(t, func(_ int, _ gqlRequest, w http.ResponseWriter) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})
		_, err := c.DiscoverGroup(context.Background(), "jo.wiemers", "")
		if StatusCode(err) != http.StatusInternalServerError || !IsTransient(err) {
			t.Fatalf("err = %v", err)
		}
		if len(*reqs) != maxAttempts || c.Stats().GraphQL != int64(maxAttempts) {
			t.Errorf("%d requests, stats %+v, want %d", len(*reqs), c.Stats(), maxAttempts)
		}
	})
	t.Run("500 then success", func(t *testing.T) {
		defer SetRetryBackoff(time.Millisecond)()
		c, _ := gqlServer(t, func(n int, _ gqlRequest, w http.ResponseWriter) {
			if n == 1 {
				http.Error(w, "boom", http.StatusBadGateway)
				return
			}
			ok(w)
		})
		if _, err := c.DiscoverGroup(context.Background(), "jo.wiemers", ""); err != nil {
			t.Fatal(err)
		}
	})
}

package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// addSearchTask adds an Implement task of the shop project (testObjects).
func addSearchTask(t *testing.T, env *testEnv, name string, iid, mr int64, title string, created time.Time) {
	t.Helper()
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: name, CreationTimestamp: metav1.NewTime(created)},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement,
			Issue: &aiv1.IssueRef{IID: iid, Title: title}},
		Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{MergeRequestIID: mr}},
	}
	if err := env.reader.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
}

func searchTasks(t *testing.T, env *testEnv, q string) []string {
	t.Helper()
	var got SearchResponse
	if code := env.get(t, "/api/v1/search?q="+url.QueryEscape(q), &got); code != http.StatusOK {
		t.Fatalf("search %q: status %d", q, code)
	}
	if got.Results == nil {
		t.Fatalf("search %q: results are null", q)
	}
	names := []string{}
	for _, r := range got.Results {
		names = append(names, r.Task)
	}
	return names
}

func TestSearchMatches(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	day := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	addSearchTask(t, env, "shop-12-refresh", 12, 0, "Desk session Refresh", day)
	addSearchTask(t, env, "shop-13-palette", 13, 34, "Palette", day.Add(time.Hour))
	addSearchTask(t, env, "shop-34-other", 34, 12, "Other", day.Add(2*time.Hour))
	cases := []struct {
		q    string
		want []string
	}{
		{"#12", []string{"shop-12-refresh"}},
		{"!34", []string{"shop-13-palette"}},
		{"refresh", []string{"shop-12-refresh"}},
		{"  PAYPAL ", []string{"shop-42-add-paypal"}},
		{"#99", []string{}},
		// Not a number: a title search.
		{"#x", []string{}},
	}
	for _, tc := range cases {
		if got := searchTasks(t, env, tc.q); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("search %q = %v, want %v", tc.q, got, tc.want)
		}
	}
	var got SearchResponse
	env.get(t, "/api/v1/search?q=%2312", &got)
	want := SearchResult{Namespace: "team-a", Project: "shop", Task: "shop-12-refresh", Type: aiv1.TaskTypeImplement,
		Title: "Desk session Refresh", IssueIID: 12}
	if len(got.Results) != 1 || !got.Results[0].CreatedAt.Equal(day) {
		t.Fatalf("results = %+v", got.Results)
	}
	got.Results[0].CreatedAt = time.Time{}
	if got.Results[0] != want {
		t.Errorf("result = %+v, want %+v", got.Results[0], want)
	}
}

// Spec tasks have no issue and are not searched: the palette finds
// milestones by itself.
func TestSearchSkipsTasksWithoutIssue(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	if got := searchTasks(t, env, "checkout"); len(got) != 0 {
		t.Errorf("search checkout = %v", got)
	}
}

func TestSearchLimitNewestFirst(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	day := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	for i := range 25 {
		addSearchTask(t, env, fmt.Sprintf("shop-%d-desk", 100+i), int64(100+i), 0, "Desk polish", day.Add(time.Duration(i)*time.Minute))
	}
	got := searchTasks(t, env, "desk")
	if len(got) != maxSearchResults {
		t.Fatalf("got %d results, want %d", len(got), maxSearchResults)
	}
	for i, name := range got {
		if want := fmt.Sprintf("shop-%d-desk", 124-i); name != want {
			t.Errorf("result %d = %s, want %s", i, name, want)
		}
	}
}

func TestSearchShortQuery(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	for _, q := range []string{"", "a", " a ", "#"} {
		if code := env.get(t, "/api/v1/search?q="+url.QueryEscape(q), nil); code != http.StatusBadRequest {
			t.Errorf("search %q: status %d, want 400", q, code)
		}
	}
}

// A task of a project the user may not view is never found, not even by its
// exact issue number; without a session the endpoint answers 401.
func TestSearchHiddenProject(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"shop-devs"}, Projects: []string{"team-a/*"}}))
	if code := env.get(t, "/api/v1/search?q=%237", nil); code != http.StatusUnauthorized {
		t.Fatalf("before login: status %d", code)
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"shop-devs": map[string]any{"1": "acme"}}}
	env.login(t, "/")
	if got := searchTasks(t, env, "#7"); len(got) != 0 {
		t.Errorf("search #7 = %v, want nothing (team-b/billing is hidden)", got)
	}
	if got := searchTasks(t, env, "#42"); fmt.Sprint(got) != "[shop-42-add-paypal]" {
		t.Errorf("search #42 = %v", got)
	}
}

package ui

import (
	"net/http"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func TestStreamWithoutDatabase(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	var page StreamPage
	if code := env.get(t, "/api/v1/stream?project=team-a/shop", &page); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if page.History || len(page.Events) != 0 {
		t.Errorf("page = %+v, want no history", page)
	}
	if code := env.get(t, "/api/v1/projects/team-a/shop/history", nil); code != http.StatusNotFound {
		t.Errorf("history without a database: status %d, want 404", code)
	}
}

func TestStream(t *testing.T) {
	owner, reader := readerStore(t)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	ev := func(key, ns, project, kind string, issue int64, minutes int, data map[string]any) store.Event {
		return store.Event{Key: key, At: at.Add(time.Duration(minutes) * time.Minute), Namespace: ns, Project: project,
			Kind: kind, IssueIID: issue, Summary: key, Data: data, ExpiresAt: at.Add(time.Hour * 24)}
	}
	if _, err := owner.InsertEvents(t.Context(), []store.Event{
		ev("shop-note", "team-a", "shop", "note", 42, 0, map[string]any{"body": "secret plan for PayPal"}),
		ev("shop-run", "team-a", "shop", "run.finished", 42, 1, map[string]any{"costUSD": "0.5"}),
		ev("shop-other", "team-a", "shop", "ci", 99, 2, nil),
		ev("billing-run", "team-b", "billing", "run.finished", 7, 3, nil),
	}); err != nil {
		t.Fatal(err)
	}
	rules := []Rule{
		{Roles: []string{"shop-devs"}, Projects: []string{"team-a/shop"}},
		{Roles: []string{"leads"}, Projects: []string{"team-a/shop"}, Conversation: true},
	}
	login := func(t *testing.T, role string) *testEnv {
		env := newTestEnv(t, withResearch(oidcConfig(rules...), false))
		env.server.SetResearchStore(reader)
		env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
		env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{role: map[string]any{"1": "acme"}}}
		env.login(t, "/")
		return env
	}
	keys := func(page StreamPage) []string {
		var ks []string
		for _, e := range page.Events {
			ks = append(ks, e.Summary)
		}
		return ks
	}

	dev := login(t, "shop-devs")
	var page StreamPage
	if code := dev.get(t, "/api/v1/stream", &page); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got := keys(page); !slices.Equal(got, []string{"shop-other", "shop-run", "shop-note"}) || !page.History {
		t.Errorf("everything = %v (history %v), want the shop's events only", got, page.History)
	}
	note := page.Events[2]
	if !note.Redacted || note.Data["body"] != nil {
		t.Errorf("note without conversation = %+v, want redacted", note)
	}
	if code := dev.get(t, "/api/v1/stream?project=team-b/billing", nil); code != http.StatusNotFound {
		t.Errorf("other project: status %d, want 404", code)
	}
	page = StreamPage{}
	dev.get(t, "/api/v1/stream?project=team-a/shop&q=paypal", &page)
	if len(page.Events) != 0 {
		t.Errorf("search reached a comment body without conversation: %v", keys(page))
	}
	page = StreamPage{}
	dev.get(t, "/api/v1/stream?project=team-a/shop&milestone=3", &page)
	if got := keys(page); !slices.Equal(got, []string{"shop-run", "shop-note"}) {
		t.Errorf("milestone 3 = %v, want the events of #42", got)
	}
	page = StreamPage{}
	dev.get(t, "/api/v1/stream?project=team-a/shop&limit=2", &page)
	if page.Older == "" || len(page.Events) != 2 {
		t.Fatalf("first page = %+v", page)
	}
	older := StreamPage{}
	dev.get(t, "/api/v1/stream?project=team-a/shop&limit=2&before="+page.Older, &older)
	if got := keys(older); !slices.Equal(got, []string{"shop-note"}) || older.Older != "" {
		t.Errorf("older page = %v (older %q)", got, older.Older)
	}
	for _, bad := range []string{"kinds=nope", "before=x", "limit=0", "milestone=a"} {
		if code := dev.get(t, "/api/v1/stream?project=team-a/shop&"+bad, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", bad, code)
		}
	}
	var hist HistoryResponse
	if code := dev.get(t, "/api/v1/projects/team-a/shop/history?days=7&tz=Europe/Berlin", &hist); code != http.StatusOK {
		t.Fatalf("history: status %d", code)
	}
	if len(hist.Days) != 7 || hist.Totals.Runs != 1 || hist.Totals.CostUSD != 0.5 || hist.Totals.Events != 3 {
		t.Errorf("history = %+v", hist)
	}

	lead := login(t, "leads")
	page = StreamPage{}
	lead.get(t, "/api/v1/stream?project=team-a/shop&q=paypal", &page)
	if len(page.Events) != 1 || page.Events[0].Redacted || page.Events[0].Data["body"] != "secret plan for PayPal" {
		t.Errorf("lead's search = %+v, want the comment with its body", page.Events)
	}
}

func TestParseCursor(t *testing.T) {
	at := time.Date(2026, 11, 2, 14, 0, 0, 5, time.UTC)
	c, err := parseCursor(formatCursor(store.Event{At: at, ID: 9}))
	if err != nil || !c.At.Equal(at) || c.ID != 9 {
		t.Errorf("round trip = %+v, %v", c, err)
	}
	if c, err := parseCursor("t:2026-11-02T23:59:59+01:00"); err != nil || c.ID != 0 {
		t.Errorf("time cursor = %+v, %v", c, err)
	}
	for _, bad := range []string{"c:1", "c:x:1", "c:1:0", "t:yesterday", "z:1"} {
		if _, err := parseCursor(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestInbox(t *testing.T) {
	posted := metav1.NewTime(time.Now())
	ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "bloop"}}
	tasks := []aiv1.AgentTask{
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "q"},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "bloop", Type: aiv1.TaskTypeImplement, RunRequest: 1, Issue: &aiv1.IssueRef{IID: 143, Title: "All-in"}},
			Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{ObservedRunRequest: 1, RunCount: 1, LastResult: aiv1.RunResultNeedsInput}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "pilot"},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "bloop", Type: aiv1.TaskTypeCoordinate, Milestone: &aiv1.MilestoneRef{IID: 20}},
			Status: aiv1.AgentTaskStatus{Pilot: &aiv1.PilotStatus{Proposals: []aiv1.PilotProposal{{ID: 4, Summary: "Split #149", PostedAt: &posted}}}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "spec"},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "bloop", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 21, Title: "Poker"}},
			Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedFor: 391,
				PausedReason: "1 reviewers failed on #391", PauseKind: aiv1.PauseReviewErrors}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "running"},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "bloop", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 22, Title: "Lobby"}},
			Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "hidden"},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "secret", Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: 1}},
			Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{LastResult: aiv1.RunResultFailed}}},
	}
	items := inboxOf(map[string]*aiv1.AgentProject{"agents/bloop": ap}, tasks, time.Now())
	if len(items) != 3 || items[0].Kind != "proposal" || items[0].ProposalID != 4 || items[1].IssueIID != 143 || items[1].Reason != "needs-input" {
		t.Fatalf("inbox = %+v", items)
	}
	if p := items[2]; p.Kind != "pause" || p.MilestoneIID != 21 || p.Title != "Poker" || p.IssueIID != 391 || p.PauseKind != aiv1.PauseReviewErrors || p.Reason != "1 reviewers failed on #391" {
		t.Errorf("a paused autopilot is in the inbox, a running one is not: %+v", p)
	}
}

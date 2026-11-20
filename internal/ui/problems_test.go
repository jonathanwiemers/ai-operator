package ui

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const shopProblems = "/api/v1/projects/team-a/shop/milestones/3/problems"

// withHold holds #42 of the shop's milestone since since, while the
// autopilot runs, and adds the milestone's pilot.
func withHold(t *testing.T, env *testEnv, since time.Time) {
	t.Helper()
	ctx := context.Background()
	var st aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Spec.Autopilot = true
	st.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
		{IID: 42, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "job `lint` failed", Since: metav1.NewTime(since)}},
	}}
	if err := env.reader.Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
	pilot := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-pilot-m3"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeCoordinate, Milestone: &aiv1.MilestoneRef{ID: 30, IID: 3, Title: "Checkout"}}}
	if err := env.reader.Create(ctx, pilot); err != nil {
		t.Fatal(err)
	}
}

func loginAs(t *testing.T, env *testEnv, role string) {
	t.Helper()
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{role: map[string]any{"1": "acme"}}}
	env.login(t, "/")
}

func TestProblemsWithoutHistory(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}}))
	since := time.Now().Add(-time.Hour).Truncate(time.Second)
	withHold(t, env, since)
	loginAs(t, env, "devs")

	var list ProblemList
	if code := env.get(t, shopProblems, &list); code != http.StatusOK || list.History || len(list.Problems) != 1 {
		t.Fatalf("list: %d %+v", code, list)
	}
	if p := list.Problems[0]; p.ID != 0 || p.IssueIID != 42 || p.Title != "Add PayPal" || p.State != store.ProblemOpen ||
		p.Status != ProblemWaiting || p.Cause != "held (ciRed): job `lint` failed" || !p.OpenedAt.Equal(since) {
		t.Errorf("live problem: %+v", p)
	}
	var th ProblemThread
	if code := env.get(t, shopProblems+"/issue-42", &th); code != http.StatusOK || th.History || th.Facts == nil || th.Facts.IID != 42 ||
		th.Pilot != "shop-pilot-m3" || len(th.Exits) == 0 || len(th.Events) != 0 {
		t.Errorf("thread by issue: %d %+v", code, th)
	}
	for _, ref := range []string{"1", "issue-43", "issue-x", "x"} {
		if code := env.get(t, shopProblems+"/"+ref, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 without the database", ref, code)
		}
	}
}

func TestProblemThread(t *testing.T) {
	owner, reader, chat := chatStores(t)
	rules := []Rule{{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: true, Conversation: true}}
	env := chatEnv(t, rules, reader, chat)
	now := time.Now()
	opened := now.Add(-time.Hour).Truncate(time.Millisecond)
	withHold(t, env, opened)
	loginAs(t, env, "chatters")
	ctx := context.Background()

	ev := func(key string, at time.Time, milestone, issue int64, kind string, data map[string]any) store.Event {
		return store.Event{Key: key, At: at, Namespace: "team-a", Project: "shop", MilestoneIID: milestone, IssueIID: issue,
			Kind: kind, Summary: key, Data: data, ExpiresAt: now.Add(24 * time.Hour)}
	}
	// A problem of #42 that resolved two days ago, the open one, and events
	// before and in its window.
	old := now.Add(-48 * time.Hour)
	if _, err := owner.InsertEvents(ctx, []store.Event{
		ev("old-open", old, 3, 42, store.KindProblemOpened, map[string]any{"cause": "paused (budget): spent"}),
		ev("old-done", old.Add(time.Minute), 3, 42, store.KindProblemResolved, map[string]any{"resolvedBy": "owner"}),
		ev("before", opened.Add(-10*time.Minute), 3, 42, "run.finished", nil),
		ev("open", opened, 3, 42, store.KindProblemOpened, map[string]any{"cause": "held (ciRed): job `lint` failed"}),
		ev("hold", opened, 3, 42, "hold.set", map[string]any{"holdKind": "ciRed", "reason": "job `lint` failed"}),
		ev("other", opened.Add(time.Minute), 3, 43, "run.finished", nil),
	}); err != nil {
		t.Fatal(err)
	}
	for i, m := range []store.ChatMessage{
		{Task: "shop-pilot-m3", Body: "problem: #42 retry the lint job"},
		{Task: "shop-pilot-m3", Body: "problem: #420 not this one"},
		{Task: "shop-pilot-m3", Body: "how is it going?"},
		{Task: "shop-42-add-paypal", Body: "use the sandbox"},
	} {
		m.Namespace, m.Project, m.Author, m.ClientKey, m.ExpiresAt = "team-a", "shop", "u1", "k"+strconv.Itoa(i), now.Add(time.Hour)
		if _, _, err := chat.InsertUserMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	var list ProblemList
	if code := env.get(t, shopProblems, &list); code != http.StatusOK || !list.History || len(list.Problems) != 2 {
		t.Fatalf("list: %d %+v", code, list)
	}
	open, done := list.Problems[0], list.Problems[1]
	if open.ID == 0 || open.State != store.ProblemOpen || open.Title != "Add PayPal" || open.Status != ProblemWaiting {
		t.Errorf("open problem: %+v", open)
	}
	if done.State != store.ProblemResolved || done.ResolvedBy != "owner" || done.ResolvedAt == nil || done.Cause != "paused (budget): spent" {
		t.Errorf("resolved problem: %+v", done)
	}

	var th ProblemThread
	if code := env.get(t, shopProblems+"/"+strconv.FormatInt(open.ID, 10), &th); code != http.StatusOK || !th.History {
		t.Fatalf("thread: %d %+v", code, th)
	}
	var got []string
	for _, e := range th.Events {
		if body, _ := e.Data["body"].(string); body != "" {
			got = append(got, body)
		} else {
			got = append(got, e.Summary)
		}
	}
	for _, want := range []string{"open", "hold", "problem: #42 retry the lint job", "use the sandbox"} {
		if !slices.Contains(got, want) {
			t.Errorf("thread misses %q: %q", want, got)
		}
	}
	for _, not := range []string{"before", "other", "old-open", "problem: #420 not this one", "how is it going?"} {
		if slices.Contains(got, not) {
			t.Errorf("thread has %q: %q", not, got)
		}
	}
	if !th.Permissions.Chat || len(th.Exits) == 0 {
		t.Errorf("an open problem has the ways out and chat: %+v %+v", th.Permissions, th.Exits)
	}

	// The resolved problem's thread is its own window, with no ways out.
	th = ProblemThread{}
	if code := env.get(t, shopProblems+"/"+strconv.FormatInt(done.ID, 10), &th); code != http.StatusOK || len(th.Exits) != 0 || th.Prompt != nil {
		t.Fatalf("resolved thread: %d %+v", code, th)
	}
	if len(th.Events) != 2 {
		t.Errorf("resolved thread events: %+v", th.Events)
	}

	// issue-42 is the open problem; a bad cursor is a 400.
	th = ProblemThread{}
	if code := env.get(t, shopProblems+"/issue-42", &th); code != http.StatusOK || th.Problem.ID != open.ID {
		t.Errorf("issue-42: %d %+v", code, th.Problem)
	}
	if code := env.get(t, shopProblems+"/issue-42?before=x", nil); code != http.StatusBadRequest {
		t.Errorf("bad cursor: status %d", code)
	}
	if code := env.get(t, shopProblems+"/999999", nil); code != http.StatusNotFound {
		t.Errorf("unknown id: status %d", code)
	}
}

func TestMergeProblems(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	rows := []store.Problem{
		{ID: 3, IssueIID: 42, State: store.ProblemOpen, OpenedAt: t0.Add(2 * time.Hour)},
		{ID: 2, IssueIID: 43, State: store.ProblemOpen, Cause: "stale", OpenedAt: t0.Add(time.Hour)},
		{ID: 1, IssueIID: 42, State: store.ProblemResolved, OpenedAt: t0},
	}
	live := map[int64]ProblemView{
		42: {IssueIID: 42, Title: "Add PayPal", State: store.ProblemOpen, Status: ProblemActing, Cause: "held", OpenedAt: t0.Add(2 * time.Hour)},
		0:  {IssueIID: 0, State: store.ProblemOpen, Status: ProblemPilot, Cause: "paused", OpenedAt: t0.Add(3 * time.Hour)},
	}
	got := mergeProblems(rows, live)
	type row struct {
		id, iid int64
		state   string
		status  ProblemStatus
	}
	var short []row
	for _, v := range got {
		short = append(short, row{v.ID, v.IssueIID, v.State, v.Status})
	}
	want := []row{
		{0, 0, store.ProblemOpen, ProblemPilot},
		{3, 42, store.ProblemOpen, ProblemActing},
		{2, 43, store.ProblemResolved, ProblemResolved}, // the CRD no longer has it
		{1, 42, store.ProblemResolved, ProblemResolved},
	}
	if !slices.Equal(short, want) {
		t.Errorf("merged = %+v, want %+v", short, want)
	}
	if got[1].Title != "Add PayPal" || got[1].Cause != "held" {
		t.Errorf("an open row takes the live title and cause: %+v", got[1])
	}
}

func TestProblemStatus(t *testing.T) {
	st := func() *aiv1.AgentTask {
		return &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
			{IID: 42, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseFindings}},
		}}}}
	}
	coord := func(phase aiv1.TaskPhase, accepted ...int64) *aiv1.AgentTask {
		c := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Phase: phase, Pilot: &aiv1.PilotStatus{}}}
		for _, iid := range accepted {
			c.Status.Pilot.Accepted = append(c.Status.Pilot.Accepted, aiv1.PilotProposal{Actions: []aiv1.PilotAction{{IssueIID: iid}}})
		}
		return c
	}
	asked := st()
	asked.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptOpen, Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 42}}}
	for _, c := range []struct {
		name      string
		st, coord *aiv1.AgentTask
		want      ProblemStatus
	}{
		{"an open question waits for you, even while the pilot runs", asked, coord(aiv1.TaskPhaseRunning, 42), ProblemWaiting},
		{"an accepted action on the issue is acting", st(), coord(aiv1.TaskPhaseRunning, 42), ProblemActing},
		{"an action on another issue is not", st(), coord(aiv1.TaskPhaseRunning, 43), ProblemPilot},
		{"no pilot run waits for you", st(), coord(""), ProblemWaiting},
		{"no pilot at all waits for you", st(), nil, ProblemWaiting},
	} {
		if got := problemStatus(c.st, c.coord, 42); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestChatAbout(t *testing.T) {
	for _, c := range []struct {
		body string
		iid  int64
		want bool
	}{
		{"problem: #42", 42, true},
		{"  problem: #42 retry it", 42, true},
		{"problem: #42: retry", 42, true},
		{"problem: #420 retry", 42, false},
		{"problem: #42a", 42, false},
		{"about problem: #42", 42, false},
		{"problem: milestone go on", 0, true},
		{"problem: milestones", 0, false},
		{"problem: #42", 0, false},
	} {
		if got := chatAbout(c.body, c.iid); got != c.want {
			t.Errorf("chatAbout(%q, %d) = %v", c.body, c.iid, got)
		}
	}
}

func TestInboxProblemQuestion(t *testing.T) {
	posted := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit"}}
	tasks := []aiv1.AgentTask{{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "spec"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 5, Title: "Poker"}},
		Status: aiv1.AgentTaskStatus{
			Plan: []aiv1.PlannedIssue{{IID: 7, Title: "Blinds"}},
			Prompts: []aiv1.Prompt{
				{ID: "Q1", State: aiv1.PromptOpen, Question: "Split #7?", PostedAt: &posted, Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 7}},
				{ID: "Q2", State: aiv1.PromptOpen, Question: "main is red", PostedAt: &posted, Source: aiv1.PromptSource{Kind: aiv1.PromptMainHealth}},
			}}}}
	items := inboxOf(map[string]*aiv1.AgentProject{"agents/orbit": ap}, tasks, time.Now())
	if len(items) != 2 {
		t.Fatalf("want the problem of #7 and the main-health prompt: %+v", items)
	}
	i := slices.IndexFunc(items, func(it InboxItem) bool { return it.Kind == "problem" })
	if i < 0 || items[i].IssueIID != 7 || items[i].Title != "Blinds" || items[i].Reason != "question: Split #7?" || items[i].At == nil || !items[i].At.Equal(posted.Time) {
		t.Errorf("problem item: %+v", items)
	}
	if j := slices.IndexFunc(items, func(it InboxItem) bool { return it.Kind == "prompt" }); j < 0 || items[j].PromptID != "Q2" {
		t.Errorf("a question outside a problem stays a prompt: %+v", items)
	}
}

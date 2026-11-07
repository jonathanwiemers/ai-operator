package ui

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// fakeMilestones is a snapshot store: it answers the rows of the queried
// projects, as the store does, or err.
type fakeMilestones struct {
	rows []store.MilestoneRow
	err  error
	got  []store.MilestoneQuery
}

func (f *fakeMilestones) Milestones(_ context.Context, q store.MilestoneQuery) ([]store.MilestoneRow, error) {
	f.got = append(f.got, q)
	var out []store.MilestoneRow
	for _, r := range f.rows {
		if slices.Contains(q.Projects, r.Project) {
			out = append(out, r)
		}
	}
	return out, f.err
}

var t0 = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

// specTask is a spec task of project's milestone iid, created at t0 plus
// age hours.
func specTask(ns, project string, iid int64, age int, ap *aiv1.AutopilotStatus) *aiv1.AgentTask {
	return &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: project + "-spec-m" + string(rune('0'+iid)),
			CreationTimestamp: metav1.NewTime(t0.Add(time.Duration(age) * time.Hour))},
		Spec: aiv1.AgentTaskSpec{ProjectRef: project, Type: aiv1.TaskTypeSpec, RunRequest: 1,
			Milestone: &aiv1.MilestoneRef{ID: iid * 10, IID: iid, Title: "M" + string(rune('0'+iid))}},
		Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{ObservedRunRequest: 1, LastResult: aiv1.RunResultSucceeded, CostUSD: "1.00"},
			Autopilot: ap},
	}
}

// milestonesEnv is a Desk whose user may see team-a only. Besides
// testObjects' team-a/shop %3 (planned, no autopilot) it has in shop %4
// running, %5 paused (needs you), %6 waiting for its turn, %7 done; in
// team-b/billing %8 running.
func milestonesEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}}))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	running := func() *aiv1.AutopilotStatus {
		return &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
			{IID: 1, State: aiv1.AutopilotItemMerged}, {IID: 2, State: aiv1.AutopilotItemSkipped}, {IID: 3, State: aiv1.AutopilotItemActive}}}
	}
	m4 := specTask("team-a", "shop", 4, 4, running())
	m4.Status.Plan = []aiv1.PlannedIssue{{Key: "a", IID: 1}, {Key: "b", IID: 2}, {Key: "c", IID: 3}, {Key: "d", IID: 9}}
	m5 := specTask("team-a", "shop", 5, 5, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedReason: "CI red"})
	m6 := specTask("team-a", "shop", 6, 1, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning})
	m6.Spec.RunRequest, m6.Spec.QueuedBehind = 0, []int64{4}
	m6.Status.Run = aiv1.RunStatus{}
	m7 := specTask("team-a", "shop", 7, 0, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone,
		Items: []aiv1.AutopilotItem{{IID: 11, State: aiv1.AutopilotItemMerged}}})
	m7.Status.Pilot = &aiv1.PilotStatus{Retrospective: &aiv1.PilotRetrospective{
		FollowUps: []aiv1.PilotFollowUp{{Title: "a"}, {Title: "b"}}, Created: map[string]int64{"1": 99}}}
	m8 := specTask("team-b", "billing", 8, 0, running())
	for _, o := range []*aiv1.AgentTask{m4, m5, m6, m7, m8} {
		if err := env.reader.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	env.login(t, "/")
	return env
}

func rowIIDs(rows []MilestoneRow) []int64 {
	var out []int64
	for _, r := range rows {
		out = append(out, r.IID)
	}
	return out
}

func rowByIID(rows []MilestoneRow, iid int64) MilestoneRow {
	for _, r := range rows {
		if r.IID == iid {
			return r
		}
	}
	return MilestoneRow{}
}

func TestMilestonesWithoutDatabase(t *testing.T) {
	env := milestonesEnv(t)
	var l MilestoneList
	if code := env.get(t, "/api/v1/milestones", &l); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if l.History {
		t.Error("history without a database")
	}
	// Needs you (%5), running (%4), then waiting and planned oldest first
	// (%6 at 1h, %3 created now); team-b's %8 and the done %7 stay out.
	if got := rowIIDs(l.Milestones); !slices.Equal(got, []int64{5, 4, 6, 3}) {
		t.Fatalf("open rows = %v", got)
	}
	m4 := rowByIID(l.Milestones, 4)
	if m4.State != MilestoneStateRunning || m4.Phase != "running" || m4.Progress != (MilestoneProgress{Merged: 2, Total: 4, Open: 2}) ||
		m4.CostUSD != "1.00" || m4.CostFrom != CostFromTasks || m4.Stored || m4.NeedsYou ||
		m4.WebURL != "https://gitlab.example.com/acme/shop/-/milestones/4" || m4.Namespace != "team-a" || m4.Project != "shop" {
		t.Errorf("running row = %+v", m4)
	}
	if m5 := rowByIID(l.Milestones, 5); m5.State != MilestoneStatePaused || !m5.NeedsYou {
		t.Errorf("paused row = %+v", m5)
	}
	if m6 := rowByIID(l.Milestones, 6); m6.State != MilestoneStateWaiting {
		t.Errorf("waiting row = %+v", m6)
	}
	// testObjects' %3: its spec and implement tasks' costs.
	if m3 := rowByIID(l.Milestones, 3); m3.State != MilestoneStatePlanned || m3.Phase != "" || m3.CostUSD != "1.75" ||
		m3.Progress != (MilestoneProgress{Total: 1, Open: 1}) {
		t.Errorf("planned row = %+v", m3)
	}

	l = MilestoneList{}
	env.get(t, "/api/v1/milestones?state=done", &l)
	if len(l.Milestones) != 1 {
		t.Fatalf("done rows = %+v", l.Milestones)
	}
	if m7 := l.Milestones[0]; m7.IID != 7 || m7.State != MilestoneStateDone || m7.FollowUpsOpen != 1 || m7.Stored || m7.DoneAt != nil {
		t.Errorf("done row = %+v", m7)
	}

	l = MilestoneList{}
	env.get(t, "/api/v1/milestones?state=all&limit=2", &l)
	if got := rowIIDs(l.Milestones); !slices.Equal(got, []int64{5, 4}) {
		t.Errorf("all, limit 2 = %v", got)
	}
	l = MilestoneList{}
	env.get(t, "/api/v1/milestones?state=all", &l)
	if got := rowIIDs(l.Milestones); !slices.Equal(got, []int64{5, 4, 6, 3, 7}) {
		t.Errorf("all = %v", got)
	}

	for _, q := range []string{"state=closed", "limit=0", "limit=x", "limit=-1"} {
		if code := env.get(t, "/api/v1/milestones?"+q, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, code)
		}
	}
}

func TestMilestonesWithSnapshot(t *testing.T) {
	env := milestonesEnv(t)
	shop := store.ProjectKey{Namespace: "team-a", Name: "shop"}
	started := t0.Add(-48 * time.Hour)
	done := func(d int) time.Time { return t0.Add(-time.Duration(d) * 24 * time.Hour) }
	spent := store.SpendTotals{CostUSD: 4.5, CostKnown: true, InputTokens: 1000, OutputTokens: 200, Calls: 3}
	fake := &fakeMilestones{rows: []store.MilestoneRow{
		// Live: its spend, start and follow-ups count, the live state and
		// progress stay.
		{Project: shop, IID: 3, Title: "Old title", State: "open", Phase: "", StartedAt: started,
			Issues: 7, Merged: 7, FollowUpsOpen: 2, Spend: spent},
		{Project: shop, IID: 7, State: "open", Phase: "done", StartedAt: done(3), DoneAt: done(1)},
		// Gone: closed ones are stored rows, an open one stays out.
		{Project: shop, IID: 1, Title: "First", WebURL: "https://gitlab.example.com/acme/shop/-/milestones/1",
			State: "closed", Phase: "done", StartedAt: done(30), DoneAt: done(20), Issues: 3, Merged: 2, Open: 1, FollowUpsOpen: 1},
		{Project: shop, IID: 2, Title: "Second", State: "closed", DoneAt: done(10), Spend: spent},
		{Project: shop, IID: 20, Title: "Orphan", State: "open", Phase: "running"},
		{Project: store.ProjectKey{Namespace: "team-b", Name: "billing"}, IID: 9, State: "closed", Spend: spent},
	}}
	env.server.milestoneRows = fake

	var l MilestoneList
	if code := env.get(t, "/api/v1/milestones", &l); code != http.StatusOK || !l.History {
		t.Fatalf("status %d, history %v", code, l.History)
	}
	if len(fake.got) != 1 || !slices.Equal(fake.got[0].Projects, []store.ProjectKey{shop}) || fake.got[0].State != "" {
		t.Errorf("queried %+v", fake.got)
	}
	// %3 started 48h before %6: it moves to the front of the others.
	if got := rowIIDs(l.Milestones); !slices.Equal(got, []int64{5, 4, 3, 6}) {
		t.Fatalf("open rows = %v", got)
	}
	m3 := rowByIID(l.Milestones, 3)
	if m3.Title != "Checkout" || m3.Progress != (MilestoneProgress{Total: 1, Open: 1}) || m3.FollowUpsOpen != 2 ||
		m3.CostUSD != "4.50" || !m3.CostKnown || m3.Tokens != 1200 || m3.CostFrom != CostFromCalls || m3.Spend != nil ||
		m3.StartedAt == nil || !m3.StartedAt.Equal(started) || m3.Stored {
		t.Errorf("merged row = %+v", m3)
	}
	// A live row without a snapshot keeps the tasks' cost.
	if m4 := rowByIID(l.Milestones, 4); m4.CostFrom != CostFromTasks {
		t.Errorf("live row without a snapshot = %+v", m4)
	}

	l = MilestoneList{}
	env.get(t, "/api/v1/milestones?state=done", &l)
	// Newest done first; billing's %9 is not visible.
	if got := rowIIDs(l.Milestones); !slices.Equal(got, []int64{7, 2, 1}) {
		t.Fatalf("done rows = %v", got)
	}
	if m7 := l.Milestones[0]; m7.Stored || m7.DoneAt == nil || !m7.DoneAt.Equal(done(1)) || m7.CostKnown {
		t.Errorf("live done row = %+v", m7)
	}
	m1 := l.Milestones[2]
	if !m1.Stored || m1.State != MilestoneStateDone || m1.Title != "First" || m1.Phase != "done" ||
		m1.Progress != (MilestoneProgress{Merged: 2, Total: 3, Open: 1}) || m1.FollowUpsOpen != 1 || m1.CostKnown ||
		m1.WebURL != "https://gitlab.example.com/acme/shop/-/milestones/1" || m1.DoneAt == nil || !m1.DoneAt.Equal(done(20)) {
		t.Errorf("stored row = %+v", m1)
	}

	l = MilestoneList{}
	env.get(t, "/api/v1/milestones?state=done&limit=1", &l)
	if got := rowIIDs(l.Milestones); !slices.Equal(got, []int64{7}) {
		t.Errorf("done, limit 1 = %v", got)
	}

	// A failing store: the live rows, without history.
	fake.err = errors.New("connection refused")
	l = MilestoneList{}
	if code := env.get(t, "/api/v1/milestones?state=all", &l); code != http.StatusOK || l.History {
		t.Fatalf("failing store: status %d, history %v", code, l.History)
	}
	if got := rowIIDs(l.Milestones); !slices.Equal(got, []int64{5, 4, 6, 3, 7}) {
		t.Errorf("failing store rows = %v", got)
	}
}

func TestMilestonesLimitCapped(t *testing.T) {
	var rows []store.MilestoneRow
	shop := store.ProjectKey{Namespace: "team-a", Name: "shop"}
	for i := range maxMilestoneRows + 10 {
		rows = append(rows, store.MilestoneRow{Project: shop, IID: int64(100 + i), State: "closed", DoneAt: t0.Add(time.Duration(i) * time.Minute)})
	}
	env := milestonesEnv(t)
	env.server.milestoneRows = &fakeMilestones{rows: rows}
	var l MilestoneList
	env.get(t, "/api/v1/milestones?state=done&limit=1000", &l)
	if len(l.Milestones) != maxMilestoneRows {
		t.Errorf("%d rows, want %d", len(l.Milestones), maxMilestoneRows)
	}
	l = MilestoneList{}
	env.get(t, "/api/v1/milestones?state=done", &l)
	if len(l.Milestones) != defaultMilestoneRows || l.Milestones[0].IID != int64(100+maxMilestoneRows+9) {
		t.Errorf("default: %d rows, first %d", len(l.Milestones), l.Milestones[0].IID)
	}
}

func TestMilestonesProjectFilter(t *testing.T) {
	env := milestonesEnv(t)
	blog := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "blog"},
		Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: "https://gitlab.example.com", Project: "acme/blog"}}}
	for _, o := range []client.Object{blog, specTask("team-a", "blog", 2, 0, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning})} {
		if err := env.reader.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	shop, blogKey := store.ProjectKey{Namespace: "team-a", Name: "shop"}, store.ProjectKey{Namespace: "team-a", Name: "blog"}
	fake := &fakeMilestones{rows: []store.MilestoneRow{
		{Project: shop, IID: 1, State: "closed", DoneAt: t0},
		{Project: blogKey, IID: 1, State: "closed", DoneAt: t0},
	}}
	env.server.milestoneRows = fake

	var l MilestoneList
	if code := env.get(t, "/api/v1/milestones?state=all&project=team-a/blog", &l); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, m := range l.Milestones {
		if m.Namespace != "team-a" || m.Project != "blog" {
			t.Errorf("row of another project: %+v", m)
		}
	}
	if got := rowIIDs(l.Milestones); !slices.Equal(got, []int64{2, 1}) {
		t.Errorf("blog rows = %v", got)
	}
	if q := fake.got[len(fake.got)-1]; !slices.Equal(q.Projects, []store.ProjectKey{blogKey}) {
		t.Errorf("snapshot queried for %v", q.Projects)
	}

	// Without the filter both projects are in.
	l = MilestoneList{}
	env.get(t, "/api/v1/milestones?state=all", &l)
	projects := map[string]bool{}
	for _, m := range l.Milestones {
		projects[m.Project] = true
	}
	if !projects["shop"] || !projects["blog"] {
		t.Errorf("unfiltered projects = %v", projects)
	}

	// A project the user may not see, an unknown one or a malformed ref:
	// 404, as for any project.
	for _, ref := range []string{"team-b/billing", "team-a/nope", "shop"} {
		if code := env.get(t, "/api/v1/milestones?project="+ref, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d", ref, code)
		}
	}
}

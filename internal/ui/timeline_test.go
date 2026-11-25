package ui

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// TimelineEvents answers the events of q's projects (and milestone), as
// the store's scope does.
func (f *fakeCosts) TimelineEvents(_ context.Context, q store.TimelineQuery, _ time.Time) ([]store.Event, bool, error) {
	f.timelineQ = append(f.timelineQ, q)
	var out []store.Event
	for _, e := range f.timeline {
		if slices.Contains(q.Projects, store.ProjectKey{Namespace: e.Namespace, Name: e.Project}) &&
			(q.Milestone == 0 || e.MilestoneIID == q.Milestone) {
			out = append(out, e)
		}
	}
	return out, false, nil
}

// TimelineReviews answers the reviews of issues.
func (f *fakeCosts) TimelineReviews(_ context.Context, issues []store.IssueKey, _ time.Time) ([]store.ReviewSpan, error) {
	f.reviewsQ = append(f.reviewsQ, issues)
	var out []store.ReviewSpan
	for _, r := range f.reviews {
		if slices.Contains(issues, store.IssueKey{Namespace: r.Namespace, Project: r.Project, IssueIID: r.IssueIID}) {
			out = append(out, r)
		}
	}
	return out, f.reviewsErr
}

var tl0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

func tlEvent(ns, project string, ms, issue int64, task, kind string, min int) store.Event {
	return store.Event{Namespace: ns, Project: project, MilestoneIID: ms, IssueIID: issue, Task: task, Kind: kind,
		At: tl0.Add(time.Duration(min) * time.Minute)}
}

// timelineEnv is costsEnv with team-a/shop %3's plan of #11 and #12 in
// wave 1 and #13 after both, a second visible project team-a/cart and the
// events of a run of the milestone, of cart #5 and of hidden billing #7.
func timelineEnv(t *testing.T) (*testEnv, *fakeCosts) {
	env, fake := costsEnv(t, false, "devs")
	ctx := context.Background()
	var st aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Status.Plan = []aiv1.PlannedIssue{{Key: "a", IID: 11, Title: "Parser"}, {Key: "b", IID: 12, Title: "Lexer"},
		{Key: "c", IID: 13, Title: "Printer", DependsOn: []int64{11, 12}}}
	if err := env.reader.Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
	for _, o := range []*aiv1.AgentProject{{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "cart"}}} {
		if err := env.reader.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	impl := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "cart-5-fix"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "cart", Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: 5, Title: "Fix cart"}}}
	if err := env.reader.Create(ctx, impl); err != nil {
		t.Fatal(err)
	}
	fake.timeline = []store.Event{
		tlEvent("team-a", "shop", 3, 13, "t13", "run.started", 70),
		tlEvent("team-a", "shop", 3, 11, "t11", "run.started", 0),
		tlEvent("team-a", "shop", 3, 12, "t12", "run.started", 5),
		tlEvent("team-a", "shop", 3, 11, "t11", "run.finished", 30),
		tlEvent("team-a", "shop", 3, 12, "t12", "run.finished", 40),
		tlEvent("team-a", "shop", 3, 11, "t11", "push", 30),
		tlEvent("team-a", "shop", 3, 11, "t11", "ci", 40),
		tlEvent("team-a", "shop", 3, 11, "t11", "merged", 50),
		tlEvent("team-a", "shop", 3, 12, "t12", "ci.started", 40),
		tlEvent("team-a", "shop", 3, 12, "t12", "ci", 55),
		tlEvent("team-a", "shop", 3, 12, "t12", "merged", 60),
		tlEvent("team-a", "shop", 3, 13, "t13", "run.finished", 90),
		tlEvent("team-a", "shop", 3, 13, "t13", "merged", 100),
		tlEvent("team-a", "cart", 0, 5, "cart-5-fix", "run.started", 20),
		tlEvent("team-b", "billing", 0, 7, "billing-7-invoice", "run.started", 20),
	}
	// #11's review at the end of its run; hidden billing #7 is never asked.
	fake.reviews = []store.ReviewSpan{
		{Namespace: "team-a", Project: "shop", IssueIID: 11, At: tl0.Add(25 * time.Minute), DurationMS: 5 * 60000},
		{Namespace: "team-b", Project: "billing", IssueIID: 7, At: tl0.Add(25 * time.Minute), DurationMS: 60000},
	}
	return env, fake
}

// TestTimelineMilestone: #11 and #12 ran side by side in wave 1, #13 after
// them in wave 2; each ran, waited and merged.
func TestTimelineMilestone(t *testing.T) {
	env, fake := timelineEnv(t)
	var v TimelineView
	if code := env.get(t, "/api/v1/timeline?project=team-a/shop&milestone=3", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if q := fake.timelineQ[0]; q.Milestone != 3 || !slices.Equal(q.Projects, shopKey) || !q.From.IsZero() {
		t.Errorf("query = %+v", q)
	}
	if !v.History || !v.From.Equal(tl0) || !v.To.Equal(v.Now) || len(v.Rows) != 3 {
		t.Fatalf("view = %+v", v)
	}
	sameJSON(t, v.Rows, `[
		{"namespace": "team-a", "project": "shop", "milestoneIID": 3, "milestoneTitle": "Checkout", "issueIID": 11, "issueTitle": "Parser",
			"wave": 1, "segments": [
				{"kind": "running", "start": "2026-11-20T09:00:00Z", "end": "2026-11-20T09:25:00Z"},
				{"kind": "review", "start": "2026-11-20T09:25:00Z", "end": "2026-11-20T09:30:00Z"},
				{"kind": "ci", "start": "2026-11-20T09:30:00Z", "end": "2026-11-20T09:40:00Z"},
				{"kind": "waiting", "start": "2026-11-20T09:40:00Z", "end": "2026-11-20T09:50:00Z"}],
			"mergedAt": "2026-11-20T09:50:00Z", "deskURL": "/projects/team-a/shop/milestones/3/issues/11"},
		{"namespace": "team-a", "project": "shop", "milestoneIID": 3, "milestoneTitle": "Checkout", "issueIID": 12, "issueTitle": "Lexer",
			"wave": 1, "segments": [
				{"kind": "running", "start": "2026-11-20T09:05:00Z", "end": "2026-11-20T09:40:00Z"},
				{"kind": "ci", "start": "2026-11-20T09:40:00Z", "end": "2026-11-20T09:55:00Z"},
				{"kind": "waiting", "start": "2026-11-20T09:55:00Z", "end": "2026-11-20T10:00:00Z"}],
			"mergedAt": "2026-11-20T10:00:00Z", "deskURL": "/projects/team-a/shop/milestones/3/issues/12"},
		{"namespace": "team-a", "project": "shop", "milestoneIID": 3, "milestoneTitle": "Checkout", "issueIID": 13, "issueTitle": "Printer",
			"wave": 2, "segments": [
				{"kind": "running", "start": "2026-11-20T10:10:00Z", "end": "2026-11-20T10:30:00Z"},
				{"kind": "waiting", "start": "2026-11-20T10:30:00Z", "end": "2026-11-20T10:40:00Z"}],
			"mergedAt": "2026-11-20T10:40:00Z", "deskURL": "/projects/team-a/shop/milestones/3/issues/13"}
	]`)

	// The reviews are read for the issues of the events only.
	if len(fake.reviewsQ) != 1 || len(fake.reviewsQ[0]) != 3 || slices.ContainsFunc(fake.reviewsQ[0], func(k store.IssueKey) bool {
		return k.Namespace != "team-a" || k.Project != "shop"
	}) {
		t.Errorf("reviews asked for %+v", fake.reviewsQ)
	}
	fake.reviewsErr = fmt.Errorf("db down")
	if code := env.get(t, "/api/v1/timeline?project=team-a/shop&milestone=3", nil); code != http.StatusInternalServerError {
		t.Errorf("reviews failing: status %d", code)
	}
	fake.reviewsErr = nil

	// A milestone of a project the viewer may not see is not found.
	if code := env.get(t, "/api/v1/timeline?project=team-b/billing&milestone=8", nil); code != http.StatusNotFound {
		t.Errorf("hidden project: status %d", code)
	}
}

// TestTimelineWindow: only the visible projects, sorted by project; an
// issue without a milestone links its task and takes its title.
func TestTimelineWindow(t *testing.T) {
	env, fake := timelineEnv(t)
	var v TimelineView
	if code := env.get(t, "/api/v1/timeline?from=2026-11-20T09:00:00Z&to=2026-11-20T10:00:00Z", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	q := fake.timelineQ[0]
	slices.SortFunc(q.Projects, compareKeys)
	if !q.From.Equal(tl0) || !q.To.Equal(tl0.Add(time.Hour)) || q.Milestone != 0 ||
		!slices.Equal(q.Projects, []store.ProjectKey{{Namespace: "team-a", Name: "cart"}, {Namespace: "team-a", Name: "shop"}}) {
		t.Errorf("query = %+v", q)
	}
	var got []string
	for _, r := range v.Rows {
		got = append(got, fmt.Sprintf("%s#%d", r.Project, r.IssueIID))
	}
	// #13 started after the window: not there.
	if !slices.Equal(got, []string{"cart#5", "shop#11", "shop#12"}) || !v.From.Equal(tl0) || !v.To.Equal(tl0.Add(time.Hour)) {
		t.Fatalf("rows %v, window %s %s", got, v.From, v.To)
	}
	if r := v.Rows[0]; r.IssueTitle != "Fix cart" || r.Wave != 0 || r.DeskURL != "/projects/team-a/cart/tasks/cart-5-fix" ||
		len(r.Segments) != 1 || r.Segments[0].End != nil && !r.Segments[0].End.Equal(tl0.Add(time.Hour)) {
		t.Errorf("cart row = %+v", r)
	}
	// #11's review and CI, and #12's CI cut at the window's end.
	for i, want := range []string{"running 0-25 review 25-30 ci 30-40 waiting 40-50", "running 5-40 ci 40-55 waiting 55-60"} {
		var parts []string
		for _, sg := range v.Rows[i+1].Segments {
			end := "now"
			if sg.End != nil {
				end = fmt.Sprint(int(sg.End.Sub(tl0).Minutes()))
			}
			parts = append(parts, fmt.Sprintf("%s %d-%s", sg.Kind, int(sg.Start.Sub(tl0).Minutes()), end))
		}
		if got := strings.Join(parts, " "); got != want {
			t.Errorf("%s#%d = %q, want %q", v.Rows[i+1].Project, v.Rows[i+1].IssueIID, got, want)
		}
	}

	if code := env.get(t, "/api/v1/timeline?from=2026-11-20T09:00:00Z&to=2026-11-20T10:00:00Z&project=team-a/cart", &v); code != http.StatusOK ||
		len(v.Rows) != 1 || v.Rows[0].Project != "cart" {
		t.Errorf("project filter: status %d, %d rows", code, len(v.Rows))
	}
	for _, path := range []string{"/api/v1/timeline?from=2026-11-20T09:00:00Z&to=2026-11-20T10:00:00Z&project=team-b/billing",
		"/api/v1/timeline?from=2026-11-20T09:00:00Z&to=2026-11-20T10:00:00Z&project=team-a/nope"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d", path, code)
		}
	}
}

func TestTimelineParams(t *testing.T) {
	env, fake := timelineEnv(t)
	for _, path := range []string{
		"/api/v1/timeline",
		"/api/v1/timeline?from=2026-11-20T09:00:00Z",
		"/api/v1/timeline?to=2026-11-20T09:00:00Z",
		"/api/v1/timeline?from=2026-11-20T10:00:00Z&to=2026-11-20T09:00:00Z",
		"/api/v1/timeline?from=2026-11-20T09:00:00Z&to=2026-11-20T09:00:00Z",
		"/api/v1/timeline?from=2026-11-12T09:00:00Z&to=2026-11-20T09:00:00Z",
		"/api/v1/timeline?from=yesterday&to=2026-11-20T09:00:00Z",
		"/api/v1/timeline?milestone=3",
		"/api/v1/timeline?project=team-a/shop&milestone=x",
		"/api/v1/timeline?project=team-a/shop&milestone=3&from=2026-11-20T09:00:00Z",
	} {
		if code := env.get(t, path, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", path, code)
		}
	}
	// Exactly 7 days is allowed.
	if code := env.get(t, "/api/v1/timeline?from=2026-11-13T09:00:00Z&to=2026-11-20T09:00:00Z", nil); code != http.StatusOK {
		t.Errorf("7 days: status %d", code)
	}
	if len(fake.timelineQ) != 1 {
		t.Errorf("queries %d, want 1", len(fake.timelineQ))
	}
}

// TestTimelineNoDatabase: without the history feature, no history and no
// rows; configured but not connected, 503.
func TestTimelineNoDatabase(t *testing.T) {
	off := milestonesEnv(t)
	off.server.costReads = &fakeCosts{}
	var v TimelineView
	if code := off.get(t, "/api/v1/timeline?project=team-a/shop&milestone=4", &v); code != http.StatusOK || v.History || v.Rows == nil || len(v.Rows) != 0 {
		t.Errorf("research off: status %d, view %+v", code, v)
	}
	none, _ := costsEnv(t, true, "devs")
	if code := none.get(t, "/api/v1/timeline?project=team-a/shop&milestone=3", nil); code != http.StatusServiceUnavailable {
		t.Errorf("not connected: status %d", code)
	}
}

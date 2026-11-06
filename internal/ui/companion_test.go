package ui

import (
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// The chip of every landing state; blocked and closed keep the chip of the
// step they stopped at and carry a reason.
func TestCompanionChip(t *testing.T) {
	ts := metav1.NewTime(time.Now())
	cases := []struct {
		name       string
		c          *aiv1.CompanionStatus
		chip       CompanionChip
		withReason bool
	}{
		{"before the home merge", nil, CompanionChipDraft, false},
		{"waitingRelease", &aiv1.CompanionStatus{State: aiv1.LandingWaitingRelease}, CompanionChipWaitingRelease, false},
		{"rebasing", &aiv1.CompanionStatus{State: aiv1.LandingRebasing}, CompanionChipWaitingRelease, false},
		{"waitingCI", &aiv1.CompanionStatus{State: aiv1.LandingWaitingCI}, CompanionChipWaitingRelease, false},
		{"ready", &aiv1.CompanionStatus{State: aiv1.LandingReady, ReadyAt: &ts}, CompanionChipReady, false},
		{"merging", &aiv1.CompanionStatus{State: aiv1.LandingMerging, ReadyAt: &ts, MergeStartedAt: &ts}, CompanionChipMerging, false},
		{"merged", &aiv1.CompanionStatus{State: aiv1.LandingMerged, ReadyAt: &ts}, CompanionChipMerged, false},
		{"blocked before ready", &aiv1.CompanionStatus{State: aiv1.LandingBlocked, Reason: "rebase conflict"}, CompanionChipWaitingRelease, true},
		{"blocked when ready", &aiv1.CompanionStatus{State: aiv1.LandingBlocked, ReadyAt: &ts}, CompanionChipReady, true},
		{"blocked after the merge started", &aiv1.CompanionStatus{State: aiv1.LandingBlocked, ReadyAt: &ts, MergeStartedAt: &ts,
			Reason: "default branch red"}, CompanionChipMerging, true},
		{"closed before ready", &aiv1.CompanionStatus{State: aiv1.LandingClosed}, CompanionChipWaitingRelease, true},
		{"closed when ready", &aiv1.CompanionStatus{State: aiv1.LandingClosed, ReadyAt: &ts}, CompanionChipReady, true},
	}
	for _, c := range cases {
		if got := companionChip(c.c); got != c.chip {
			t.Errorf("%s: chip %q, want %q", c.name, got, c.chip)
		}
		if c.c == nil {
			continue
		}
		v := companionEntryView(c.c)
		if v.Chip != c.chip || (v.Reason != "") != (c.withReason || c.c.Reason != "") {
			t.Errorf("%s: view %+v", c.name, v)
		}
	}
}

func companionTask(name string, iid int64, run *aiv1.CompanionRun) aiv1.AgentTask {
	t := aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: name},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: iid},
			Milestone: &aiv1.MilestoneRef{IID: 3, Title: "Checkout"}}}
	t.Status.Run.Companion = run
	if run != nil {
		t.Spec.Companion = &aiv1.CompanionSpec{Project: run.Project, AutoMerge: true}
	}
	return t
}

// The task view takes the landing entry of the same merge request, else
// the run's draft.
func TestTaskCompanion(t *testing.T) {
	ready := metav1.NewTime(time.Now().Truncate(time.Second))
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	p.Status.Companions = []aiv1.CompanionStatus{
		{HomeIssueIID: 1, MilestoneIID: 3, Project: "acme/infra", MRIID: 7, State: aiv1.LandingClosed, Reason: "closed by hand"},
		{HomeIssueIID: 1, MilestoneIID: 3, Project: "Acme/Infra", MRIID: 8, MRURL: "https://gl/acme/infra/-/merge_requests/8",
			State: aiv1.LandingReady, Release: "v2026.11.3", ReadyAt: &ready},
	}
	landing := companionTask("shop-1", 1, &aiv1.CompanionRun{Project: "acme/infra", MRIID: 8})
	v := taskView(&landing, p, time.Now()).Companion
	if v == nil || v.MRIID != 8 || v.Chip != CompanionChipReady || v.State != aiv1.LandingReady || v.Release != "v2026.11.3" ||
		v.ReadyAt == nil || v.MRURL == "" || v.HomeIssueIID != 1 {
		t.Errorf("landing companion = %+v", v)
	}

	draft := companionTask("shop-2", 2, &aiv1.CompanionRun{Project: "acme/infra", MRIID: 9, MRURL: "https://gl/acme/infra/-/merge_requests/9"})
	v = taskView(&draft, p, time.Now()).Companion
	if v == nil || v.Chip != CompanionChipDraft || v.State != "" || v.MRIID != 9 || v.MRURL == "" || !v.AutoMerge {
		t.Errorf("draft companion = %+v", v)
	}

	none := companionTask("shop-4", 4, nil)
	if v := taskView(&none, p, time.Now()).Companion; v != nil {
		t.Errorf("a task without a companion: %+v", v)
	}

	// The run's companion was cleared: the issue's newest entry.
	gone := companionTask("shop-1", 1, nil)
	if v := taskView(&gone, p, time.Now()).Companion; v == nil || v.MRIID != 8 {
		t.Errorf("without the run's companion: %+v", v)
	}
}

// The milestone lists its tasks' companions and the entries of tasks that
// are gone, by home issue; other milestones' entries stay out.
func TestMilestoneCompanions(t *testing.T) {
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	p.Status.Companions = []aiv1.CompanionStatus{
		{HomeIssueIID: 5, MilestoneIID: 3, Project: "acme/infra", MRIID: 11, State: aiv1.LandingMerged},
		{HomeIssueIID: 1, MilestoneIID: 3, Project: "acme/infra", MRIID: 8, State: aiv1.LandingWaitingRelease},
		{HomeIssueIID: 9, MilestoneIID: 4, Project: "acme/infra", MRIID: 12, State: aiv1.LandingReady},
	}
	spec := aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop-spec"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 3, Title: "Checkout"}}}
	tasks := []aiv1.AgentTask{
		spec,
		companionTask("shop-2", 2, &aiv1.CompanionRun{Project: "acme/infra", MRIID: 9}),
		companionTask("shop-1", 1, &aiv1.CompanionRun{Project: "acme/infra", MRIID: 8}),
		companionTask("shop-4", 4, nil),
	}
	v := milestoneView(p, tasks, &tasks[0], nil, time.Now())
	var got []string
	for _, c := range v.Companions {
		got = append(got, companionRef(c.Project, c.MRIID)+" "+string(c.Chip))
	}
	want := []string{"acme/infra!8 waiting for release", "acme/infra!9 draft", "acme/infra!11 merged"}
	if !slices.Equal(got, want) {
		t.Errorf("companions = %v, want %v", got, want)
	}
	for _, task := range v.Tasks {
		if (task.Companion != nil) != (task.Name != "shop-4") {
			t.Errorf("task %s companion = %+v", task.Name, task.Companion)
		}
	}
	if v := milestoneView(&aiv1.AgentProject{}, tasks[:1], &tasks[0], nil, time.Now()); v.Companions == nil || len(v.Companions) != 0 {
		t.Errorf("without companions the list is empty, not null: %#v", v.Companions)
	}
}

// companionReady is an Inbox item only for the owner path: ready, not
// merged by autopilot, the owners told. It leaves once merged or closed.
func TestInboxCompanionReady(t *testing.T) {
	notified := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	ap.Status.Companions = []aiv1.CompanionStatus{
		{HomeIssueIID: 1, MilestoneIID: 3, Project: "acme/infra", MRIID: 8, MRURL: "https://gl/acme/infra/-/merge_requests/8",
			State: aiv1.LandingReady, Release: "v2026.11.3", NotifiedAt: &notified},
		{HomeIssueIID: 2, MilestoneIID: 3, Project: "acme/infra", MRIID: 9, State: aiv1.LandingReady, AutoMerge: true},
		{HomeIssueIID: 3, MilestoneIID: 3, Project: "acme/infra", MRIID: 10, State: aiv1.LandingMerged, NotifiedAt: &notified},
		{HomeIssueIID: 4, MilestoneIID: 3, Project: "acme/infra", MRIID: 11, State: aiv1.LandingClosed, NotifiedAt: &notified},
		{HomeIssueIID: 5, MilestoneIID: 3, Project: "acme/infra", MRIID: 12, State: aiv1.LandingReady},
		{HomeIssueIID: 6, MilestoneIID: 3, Project: "acme/infra", MRIID: 13, State: aiv1.LandingMerging, NotifiedAt: &notified},
	}
	tasks := []aiv1.AgentTask{companionTask("shop-1", 1, &aiv1.CompanionRun{Project: "acme/infra", MRIID: 8})}
	tasks[0].Status.Run.LastResult = aiv1.RunResultSucceeded
	items := inboxOf(map[string]*aiv1.AgentProject{"agents/shop": ap}, tasks, time.Now())
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one companionReady", items)
	}
	it := items[0]
	if it.Kind != "companionReady" || it.Title != "acme/infra!8" || it.URL != "https://gl/acme/infra/-/merge_requests/8" ||
		it.Reason != "ready to merge: release v2026.11.3 runs" || it.IssueIID != 1 || it.MilestoneIID != 3 ||
		it.Task != "shop-1" || it.Namespace != "agents" || it.Project != "shop" || it.At == nil || !it.At.Equal(notified.Time) {
		t.Errorf("item = %+v", it)
	}
}

// The companion pause shows its ways out, with the live state of the
// automatic ones.
func TestExitViewsCompanionPause(t *testing.T) {
	ap := pausedOn(aiv1.PauseCompanion, aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemActive, MergeRequestIID: 5})
	views := exitViews(ap, nil, &aiv1.AgentProject{}, MilestonePermissions{Chat: true})
	var ids []string
	for _, v := range views {
		ids = append(ids, v.ID)
		if v.Auto && v.Status == "" {
			t.Errorf("%s: an automatic exit without a status", v.ID)
		}
		if v.ID == "resume" && !v.Allowed {
			t.Errorf("resume is a button with chat: %+v", v)
		}
	}
	for _, want := range []string{"companionMergeable", "companionByHand", "companionDefaultBranchGreen", "fixCompanion", "resume", "skip"} {
		if !slices.Contains(ids, want) {
			t.Errorf("the companion pause misses exit %q: %v", want, ids)
		}
	}
}

// Issue rows of Spend carry the companion project of their issue.
func TestSpendCompanionMarker(t *testing.T) {
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	p.Status.Companions = []aiv1.CompanionStatus{{HomeIssueIID: 5, Project: "acme/infra", MRIID: 11, State: aiv1.LandingMerged}}
	optedIn := companionTask("shop-3", 3, nil)
	optedIn.Spec.Companion = &aiv1.CompanionSpec{Project: "acme/docs"}
	tasks := []aiv1.AgentTask{companionTask("shop-2", 2, &aiv1.CompanionRun{Project: "acme/infra", MRIID: 9}), optedIn, companionTask("shop-4", 4, nil)}
	v := spendView(store.Spend{Children: []store.SpendChild{
		{Level: store.SpendIssue, Key: "2"}, {Level: store.SpendIssue, Key: "3"}, {Level: store.SpendIssue, Key: "4"},
		{Level: store.SpendIssue, Key: "5"}, {Level: store.SpendIssue, Key: ""}, {Level: store.SpendRun, Key: "shop-2/1"},
	}})
	markCompanions(&v, issueCompanions(p, tasks))
	var got []string
	for _, c := range v.Children {
		got = append(got, c.Key+"="+c.Companion)
	}
	want := []string{"2=acme/infra", "3=acme/docs", "4=", "5=acme/infra", "=", "shop-2/1="}
	if !slices.Equal(got, want) {
		t.Errorf("markers = %v, want %v", got, want)
	}
}

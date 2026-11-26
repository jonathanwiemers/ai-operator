package controller

import (
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

const queueTaskName = "shop-loose-issues"

// newLooseEnv is a project with the loose issues queue on, after its first
// poll.
func newLooseEnv(t *testing.T, mutate func(*aiv1.LooseIssuesSpec)) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	e.patchProject(func(p *aiv1.AgentProject) {
		p.Spec.Notify.Users = []string{"bob"}
		p.Spec.LooseIssues = aiv1.LooseIssuesSpec{Enabled: true, OwnerSubjects: []string{"alice@example.com"}}
		if mutate != nil {
			mutate(&p.Spec.LooseIssues)
		}
	})
	e.reconcile()
	return e
}

func (e *projectEnv) queue() *aiv1.AgentTask {
	e.t.Helper()
	qt, ok := e.tasks()[queueTaskName]
	if !ok {
		e.t.Fatal("no Queue task")
	}
	return &qt
}

func (e *projectEnv) setLoose(enabled bool) {
	e.t.Helper()
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.LooseIssues.Enabled = enabled })
}

func TestLooseQueueTaskAndTrackingIssue(t *testing.T) {
	e := newLooseEnv(t, nil)
	qt := e.queue()
	if qt.Spec.Type != aiv1.TaskTypeQueue || qt.Spec.Milestone != nil || qt.Spec.Issue != nil || qt.Spec.RunRequest != 0 ||
		!slices.Equal(qt.Spec.Owners, []string{"bob"}) || !slices.Equal(qt.Spec.OwnerSubjects, []string{"alice@example.com"}) {
		t.Fatalf("Queue task: %+v", qt.Spec)
	}
	ap := qt.Status.Autopilot
	if ap == nil || ap.Phase != aiv1.AutopilotRunning || ap.MaxParallel != 1 || ap.TrackingIssueIID == 0 {
		t.Fatalf("queue status: %+v", ap)
	}
	is := e.gl.Issues[ap.TrackingIssueIID]
	if is.Title != "🤖 Loose issues" || is.Milestone != nil || !slices.Equal(is.Labels, []string{"ai-generated"}) ||
		!strings.Contains(is.Description, "_No issues in the queue yet._") || !strings.Contains(is.Description, "Owners: `bob`") {
		t.Fatalf("tracking issue: %+v", is)
	}

	// The queue's own owners replace notify.users; maxParallel follows.
	e.patchProject(func(p *aiv1.AgentProject) {
		p.Spec.LooseIssues.Owners, p.Spec.LooseIssues.MaxParallel = []string{"carol"}, 3
	})
	e.reconcile()
	qt = e.queue()
	if !slices.Equal(qt.Spec.Owners, []string{"carol"}) || qt.Status.Autopilot.MaxParallel != 3 || qt.Status.Autopilot.TrackingIssueIID != ap.TrackingIssueIID {
		t.Fatalf("owners and maxParallel follow the spec: %+v %+v", qt.Spec, qt.Status.Autopilot)
	}
	if d := e.gl.Issues[ap.TrackingIssueIID].Description; !strings.Contains(d, "Owners: `carol`") || !strings.Contains(d, "up to 3 at a time") {
		t.Fatalf("the tracking issue follows: %s", d)
	}
}

func TestLooseOwnersFallBackToTheDefaultOwners(t *testing.T) {
	e := newProjectEnv(t)
	e.r.DefaultOwners = []string{"ops"}
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.LooseIssues.Enabled = true })
	e.reconcile()
	if qt := e.queue(); !slices.Equal(qt.Spec.Owners, []string{"ops"}) || len(qt.Spec.OwnerSubjects) != 0 {
		t.Fatalf("owners fall back, owner subjects do not: %+v", qt.Spec)
	}
}

func TestLooseNoQueueTaskWhileDisabled(t *testing.T) {
	e := newProjectEnv(t)
	e.reconcile()
	for name, task := range e.tasks() {
		if task.Spec.Type == aiv1.TaskTypeQueue {
			t.Fatalf("no Queue task while looseIssues is off: %s", name)
		}
	}
	if len(e.gl.Issues) != 0 {
		t.Fatalf("no tracking issue while looseIssues is off: %+v", e.gl.Issues)
	}
}

func TestLooseSwitchOffAndOn(t *testing.T) {
	e := newLooseEnv(t, nil)
	tracking := e.queue().Status.Autopilot.TrackingIssueIID
	e.gl.AddIssue(6, "Active one", "agent::working")
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Items = []aiv1.AutopilotItem{
			{IID: 4, State: aiv1.AutopilotItemMerged, MergeRequestIID: 40},
			{IID: 5, State: aiv1.AutopilotItemPending},
			{IID: 6, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "CI is red"}},
			{IID: 7, State: aiv1.AutopilotItemPending},
		}
	})
	e.setLoose(false)
	e.reconcile()
	qt := e.queue()
	ap := qt.Status.Autopilot
	if ap.Phase != aiv1.AutopilotOff || e.gl.Issues[tracking].State != "closed" {
		t.Fatalf("switched off, the queue is off and its tracking issue closed: %+v %s", ap, e.gl.Issues[tracking].State)
	}
	notes := e.gl.IssueNotes[tracking]
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "Dropped from the queue: #5, #7.") ||
		!strings.Contains(notes[0].Body, "a human merges them: #6.") {
		t.Fatalf("the note names the dropped and the manual issues: %+v", notes)
	}
	want := map[int64]string{4: "", 5: "the queue was switched off", 6: "the queue was switched off; continues in the manual flow", 7: "the queue was switched off"}
	for _, it := range ap.Items {
		if it.LeftReason != want[it.IID] || it.IID != 4 && (it.State != aiv1.AutopilotItemLeft || it.LeftAt == nil || it.Hold != nil) {
			t.Fatalf("item #%d: %+v", it.IID, it)
		}
	}
	if !e.gl.Issues[6].HasLabel("agent::working") {
		t.Fatal("an active issue keeps its labels: it continues in the manual flow")
	}

	// Off stays off without a second note.
	e.reconcile()
	if n := len(e.gl.IssueNotes[tracking]); n != 1 || e.queue().Status.Autopilot.Phase != aiv1.AutopilotOff {
		t.Fatalf("a second poll while off does nothing: %d notes", n)
	}

	// On again: the same tracking issue is reopened.
	e.setLoose(true)
	e.reconcile()
	ap = e.queue().Status.Autopilot
	if ap.Phase != aiv1.AutopilotRunning || ap.TrackingIssueIID != tracking || e.gl.Issues[tracking].State != "opened" ||
		!strings.Contains(e.gl.Issues[tracking].Description, "▶️ running") {
		t.Fatalf("switched on again, the tracking issue is reopened: %+v %+v", ap, e.gl.Issues[tracking])
	}
}

func TestLooseQueueTaskWithoutTrackingIssueIsDeletedWhenOff(t *testing.T) {
	e := newLooseEnv(t, nil)
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) { s.Autopilot = nil })
	e.setLoose(false)
	e.reconcile()
	if _, ok := e.tasks()[queueTaskName]; ok {
		t.Fatal("a Queue task without a tracking issue is deleted once the queue is off")
	}
}

func TestLooseRecreatesADeletedTrackingIssue(t *testing.T) {
	e := newLooseEnv(t, nil)
	old := e.queue().Status.Autopilot.TrackingIssueIID
	delete(e.gl.Issues, old)
	e.reconcile()
	if now := e.queue().Status.Autopilot.TrackingIssueIID; now == old || e.gl.Issues[now] == nil || e.gl.Issues[now].Title != "🤖 Loose issues" {
		t.Fatalf("a new tracking issue should be created: old #%d, now #%d", old, now)
	}
}

func TestLooseRenderTracking(t *testing.T) {
	at := func(m int) *metav1.Time { return &metav1.Time{Time: time.Date(2026, 11, 1, 12, m, 0, 0, time.UTC)} }
	a := &autopilot{loose: true, spec: testProject().Spec.WithDefaults(),
		st: &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeQueue, Owners: []string{"bob", "carol"}}},
		ap: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, MaxParallel: 2, Items: []aiv1.AutopilotItem{
			{IID: 10, State: aiv1.AutopilotItemMerged, MergeRequestIID: 100, MergedAt: at(1)},
			{IID: 11, State: aiv1.AutopilotItemActive, MergeRequestIID: 110},
			{IID: 12, State: aiv1.AutopilotItemLeft, LeftReason: "closed by @mallory", LeftAt: at(2)},
			{IID: 13, State: aiv1.AutopilotItemPending},
			{IID: 14, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "the pipeline failed"}},
			{IID: 15, State: aiv1.AutopilotItemPending},
			{IID: 16, State: aiv1.AutopilotItemMerged, MergeRequestIID: 160, MergedAt: at(3)},
			{IID: 17, State: aiv1.AutopilotItemActive},
		}},
		issues: map[int64]gitlab.Issue{11: {IID: 11, Labels: []string{"agent::review"}}},
		tasks:  map[int64]*aiv1.AgentTask{17: {Status: aiv1.AgentTaskStatus{Phase: aiv1.TaskPhaseRunning}}},
	}
	got := a.renderTracking()
	want := "**Loose issues queue**: ▶️ running.\n\n" +
		"The operator works through the open issues without a milestone that an owner takes, up to 2 at a time, first taken, first started. " +
		"It merges each merge request when its pipeline is green and its review is clean. A problem holds only its own issue.\n\n" +
		"Owners: `bob`, `carol`. Take an issue with `/agent take #<issue>` as a comment here (owners).\n" +
		"\n### Pending\n\n1. #13\n2. #15\n" +
		"\n### Active\n\n- #11 !110: agent::review\n- #17: running\n" +
		"\n### Held\n\n- #14: the pipeline failed (`/agent resume #14`)\n" +
		"\n### Merged\n\n- [x] #16: merged !160\n- [x] #10: merged !100\n" +
		"\n### Left\n\n- #12: " + notify.Defuse("closed by @mallory") + "\n" +
		"\n<!-- ai-operator:autopilot -->\n"
	if got != want {
		t.Fatalf("tracking issue body:\n%s\nwant:\n%s", got, want)
	}
	if again := a.renderTracking(); again != got {
		t.Fatal("the body must be deterministic")
	}
}

func TestLoosePruneFinished(t *testing.T) {
	var items []aiv1.AutopilotItem
	for i := range 60 {
		at := &metav1.Time{Time: time.Date(2026, 11, 1, 0, i, 0, 0, time.UTC)}
		if i%2 == 0 {
			items = append(items, aiv1.AutopilotItem{IID: int64(i + 1), State: aiv1.AutopilotItemMerged, MergedAt: at})
		} else {
			items = append(items, aiv1.AutopilotItem{IID: int64(i + 1), State: aiv1.AutopilotItemLeft, LeftAt: at})
		}
	}
	items = append([]aiv1.AutopilotItem{{IID: 100, State: aiv1.AutopilotItemPending}}, items...)
	items = append(items, aiv1.AutopilotItem{IID: 101, State: aiv1.AutopilotItemActive})
	got := pruneFinished(items, maxLooseFinished)
	if len(got) != 52 || got[0].IID != 100 || got[1].IID != 11 || got[50].IID != 60 || got[51].IID != 101 {
		t.Fatalf("the newest 50 finished items and every open one stay, in order: %d items, %+v", len(got), got[:2])
	}
	a := &autopilot{loose: true, spec: testProject().Spec.WithDefaults(), st: &aiv1.AgentTask{}, ap: &aiv1.AutopilotStatus{Items: items}}
	// Unpruned: the body still shows the newest 50 finished, 25 merged and 25 left.
	if body := a.renderTracking(); strings.Count(body, "- [x] #") != 25 || strings.Count(body, "\n- #") != 25+1 ||
		strings.Contains(body, "- [x] #9:") || !strings.Contains(body, "- [x] #59:") || strings.Contains(body, "- #10:") || !strings.Contains(body, "- #60:") {
		t.Fatalf("the body shows at most 50 finished items:\n%s", body)
	}
}

package history

import (
	"reflect"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// plannedSpec is the spec task of milestone %20 "Poker" with plan.
func plannedSpec(rv string, plan ...aiv1.PlannedIssue) *aiv1.AgentTask {
	return task(rv, func(t *aiv1.AgentTask) {
		t.Spec.Type, t.Spec.Issue, t.Spec.Milestone.Title = aiv1.TaskTypeSpec, nil, "Poker"
		t.Status.Plan = plan
	})
}

func eventsOf(es []store.Event, kind string) []store.Event {
	return slices.DeleteFunc(es, func(e store.Event) bool { return e.Kind != kind })
}

// TestDifferMilestoneStarted: the first plan of a spec task is one
// milestone.started; a re-plan is none, a plan emptied and filled again a
// new one, and a task that is no spec task gets none.
func TestDifferMilestoneStarted(t *testing.T) {
	plan := []aiv1.PlannedIssue{{IID: 7, Title: "Blinds"}, {IID: 8, Title: "Chips"}}
	d := NewDiffer()
	got := eventsOf(d.Update(plannedSpec("1"), plannedSpec("2", plan...), now), KindMilestoneStarted)
	if len(got) != 1 {
		t.Fatalf("first plan: %v", kinds(got))
	}
	e := got[0]
	if e.Key != "task/u1/milestone/started/2" || e.Summary != "Milestone planned: 2 issues" || e.MilestoneIID != 20 || e.IssueIID != 0 {
		t.Errorf("event: %+v", e)
	}
	if want := map[string]any{"issues": 2, "title": "Poker"}; !reflect.DeepEqual(e.Data, want) {
		t.Errorf("data %v, want %v", e.Data, want)
	}

	if got := eventsOf(d.Update(plannedSpec("2", plan...), plannedSpec("3", plan[:1]...), now), KindMilestoneStarted); len(got) != 0 {
		t.Errorf("a re-plan: %v", kinds(got))
	}
	if got := eventsOf(d.Update(plannedSpec("3"), plannedSpec("4", plan...), now), KindMilestoneStarted); len(got) != 1 || got[0].Key != "task/u1/milestone/started/4" {
		t.Errorf("a plan filled again: %v", kinds(got))
	}
	impl := func(rv string, plan ...aiv1.PlannedIssue) *aiv1.AgentTask {
		return task(rv, func(t *aiv1.AgentTask) { t.Status.Plan = plan })
	}
	if got := eventsOf(d.Update(impl("1"), impl("2", plan...), now), KindMilestoneStarted); len(got) != 0 {
		t.Errorf("a task that is no spec task: %v", kinds(got))
	}
}

// TestDifferEventsCarryMilestoneTitle: autopilot and release events carry
// the milestone's title in data.title.
func TestDifferEventsCarryMilestoneTitle(t *testing.T) {
	ended := metav1.NewTime(now)
	rec := aiv1.ReleaseRecord{ID: 1, By: aiv1.ReleaseByPlanner, State: aiv1.ReleaseReleased, Version: "2026.11.4", EndedAt: &ended}
	old, cur := plannedSpec("1"), plannedSpec("2")
	cur.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, TrackingIssueIID: 9, Releases: []aiv1.ReleaseRecord{rec}}
	es := NewDiffer().Update(old, cur, now)
	for _, kind := range []string{KindAutopilot, KindReleaseDecided, KindReleaseEnded} {
		got := eventsOf(slices.Clone(es), kind)
		if len(got) != 1 || got[0].Data["title"] != "Poker" {
			t.Errorf("%s: %+v", kind, got)
		}
	}
	if a := eventsOf(slices.Clone(es), KindAutopilot); len(a) == 1 && (a[0].Summary != "Autopilot done" || a[0].Data["phase"] != "Done") {
		t.Errorf("autopilot event changed: %+v", a[0])
	}
}

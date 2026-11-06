package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestQueueLess(t *testing.T) {
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	task := func(name string, lane QueueLane, milestone int64, age time.Duration) *AgentTask {
		t := &AgentTask{ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: metav1.NewTime(base.Add(age))}}
		t.Spec.QueueLane = lane
		if milestone != 0 {
			t.Spec.Milestone = &MilestoneRef{IID: milestone}
		}
		return t
	}
	review := func(name string, milestone int64, reviewedFor string) *AgentTask {
		t := task(name, "", milestone, 0)
		t.Spec.Source.ReviewFor, t.Status.Run.PushedSHA, t.Status.Run.ReviewedFor = "sha", "sha", reviewedFor
		return t
	}
	tests := []struct {
		name string
		a, b *AgentTask
	}{
		{"urgent before no milestone", task("a", QueueLaneUrgent, 9, time.Hour), task("b", "", 0, 0)},
		{"urgent before the oldest milestone", task("a", QueueLaneUrgent, 9, time.Hour), task("b", "", 1, 0)},
		{"urgent before a review-only run", task("a", QueueLaneUrgent, 9, time.Hour), review("b", 1, "")},
		{"review-only run before no milestone", review("a", 9, ""), task("b", "", 0, 0)},
		{"review-only run before an older milestone", review("a", 9, ""), task("b", "", 1, 0)},
		{"a review already done ranks normally", task("a", "", 1, 0), review("b", 9, "sha")},
		{"no milestone before a milestone", task("a", "", 0, time.Hour), task("b", "", 1, 0)},
		{"older milestone first", task("a", "", 6, time.Hour), task("b", "", 9, 0)},
		{"same milestone, older task first", task("a", "", 6, 0), task("b", "", 6, time.Minute)},
		{"both without milestone, older task first", task("a", "", 0, 0), task("b", "", 0, time.Minute)},
		{"both urgent, older task first", task("a", QueueLaneUrgent, 0, 0), task("b", QueueLaneUrgent, 6, time.Minute)},
		{"same timestamp, name decides", task("a", "", 6, 0), task("b", "", 6, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !QueueLess(tt.a, tt.b) {
				t.Errorf("%s should go ahead of %s", tt.a.Name, tt.b.Name)
			}
			if QueueLess(tt.b, tt.a) {
				t.Errorf("%s should not go ahead of %s", tt.b.Name, tt.a.Name)
			}
		})
	}
	if x := task("a", "", 6, 0); QueueLess(x, x) {
		t.Error("a task does not go ahead of itself")
	}
}

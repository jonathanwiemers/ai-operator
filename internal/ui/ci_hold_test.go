package ui

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// recoveredTask is the implement task of issue iid whose pipeline of the
// pushed head turned green at recovered (nil: no recovery).
func recoveredTask(iid int64, ms *aiv1.MilestoneRef, recovered *metav1.Time, cause aiv1.NeedsInputCause) aiv1.AgentTask {
	return aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop-impl"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, Milestone: ms, Issue: &aiv1.IssueRef{IID: iid}},
		Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha1", NeedsInputCause: cause},
			CI: &aiv1.CIStatus{SHA: "sha1", State: aiv1.CIStateSuccess, RecoveredAt: recovered}}}
}

func TestCIGreenSinceHold(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *metav1.Time { return &metav1.Time{Time: now.Add(d)} }
	hold := &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "job `build` failed", Since: *at(-time.Hour)}
	recovered := recoveredTask(2, nil, at(-time.Minute), "")
	newHead := recoveredTask(2, nil, at(-time.Minute), "")
	newHead.Status.Run.PushedSHA = "sha2"
	failed := recoveredTask(2, nil, at(-time.Minute), "")
	failed.Status.CI.State = aiv1.CIStateFailed
	for _, tc := range []struct {
		name string
		t    aiv1.AgentTask
		hold *aiv1.ItemHold
		want bool
	}{
		{"held with a recovery", recovered, hold, true},
		{"still in needs-input for red CI", recoveredTask(2, nil, at(-time.Minute), aiv1.NeedsInputCIRed), nil, true},
		{"the hold ended", recovered, nil, false},
		{"recovered before the hold", recoveredTask(2, nil, at(-2*time.Hour), ""), hold, false},
		{"green before the hold, no recovery", recoveredTask(2, nil, nil, ""), hold, false},
		{"a new head", newHead, hold, false},
		{"red again", failed, hold, false},
	} {
		if got := ciGreenSinceHold(&tc.t, tc.hold); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if ciGreenSinceHold(nil, hold) {
		t.Error("no task, no flag")
	}
}

func TestCIGreenSinceHoldViews(t *testing.T) {
	now := time.Now()
	since := metav1.NewTime(now.Add(-time.Hour))
	hold := &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "job `build` failed", Since: since}
	st := lanesSpec(aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemActive},
		aiv1.AutopilotItem{IID: 2, State: aiv1.AutopilotItemActive, Hold: hold})
	impl := recoveredTask(2, st.Spec.Milestone, &metav1.Time{Time: now.Add(-time.Minute)}, "")
	tasks := []aiv1.AgentTask{st, impl}

	v := milestoneView(&aiv1.AgentProject{}, tasks, &st, nil, now)
	if items := v.Autopilot.Items; len(items) != 2 || items[0].CIGreenSinceHold || !items[1].CIGreenSinceHold {
		t.Errorf("queue items = %+v", items)
	}
	if h := taskHold(tasks, &impl); h != hold {
		t.Errorf("the task's hold = %+v, want the milestone item's", h)
	}

	queue := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeQueue},
		Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Items: []aiv1.AutopilotItem{{IID: 2, State: aiv1.AutopilotItemActive, Hold: hold}}}}}
	loose := recoveredTask(2, nil, &metav1.Time{Time: now.Add(-time.Minute)}, "")
	looseTasks := []aiv1.AgentTask{queue, loose}
	if items := looseItems(&aiv1.AgentProject{}, looseTasks, queue.Status.Autopilot.Items, nil); len(items) != 1 || !items[0].CIGreenSinceHold {
		t.Errorf("loose items = %+v", items)
	}
	if h := taskHold(looseTasks, &loose); h != hold {
		t.Errorf("the loose task's hold = %+v, want the Queue item's", h)
	}

	// The hold ended on the autopilot pass: no hold, no flag.
	queue.Status.Autopilot.Items[0].Hold = nil
	if h := taskHold(looseTasks, &loose); h != nil || ciGreenSinceHold(&loose, h) {
		t.Errorf("after the hold, hold = %+v", h)
	}
	if h := taskHold(tasks, &st); h != nil {
		t.Errorf("a spec task has no hold: %+v", h)
	}
}

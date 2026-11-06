package ui

import (
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func waitingSpec() aiv1.AgentTask {
	return aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop-spec-m9"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 9, Title: "Later"},
			QueuedBehind: []int64{8, 7}},
		Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, TrackingIssueIID: 70}}}
}

// A milestone waiting for its turn shows the older milestones, sorted; once
// its planning is asked for (by its turn or an owner's start), it does not.
func TestMilestoneViewQueuedBehind(t *testing.T) {
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	st := waitingSpec()
	v := milestoneView(p, []aiv1.AgentTask{st}, &st, nil, time.Now())
	if v.Autopilot == nil || !slices.Equal(v.Autopilot.QueuedBehind, []int64{7, 8}) {
		t.Fatalf("autopilot = %+v", v.Autopilot)
	}
	for name, change := range map[string]func(*aiv1.AgentTask){
		"planning": func(t *aiv1.AgentTask) { t.Spec.RunRequest = 1 },
		"started":  func(t *aiv1.AgentTask) { t.Status.Autopilot.StartRequestedBy = "@alice" },
		"planned":  func(t *aiv1.AgentTask) { t.Status.Plan = []aiv1.PlannedIssue{{IID: 1, Title: "One"}} },
	} {
		st := waitingSpec()
		change(&st)
		if v := milestoneView(p, []aiv1.AgentTask{st}, &st, nil, time.Now()); v.Autopilot.QueuedBehind != nil {
			t.Errorf("%s: queuedBehind = %v", name, v.Autopilot.QueuedBehind)
		}
	}
}

// Waiting for its turn asks nothing of anyone: no Inbox item.
func TestInboxWaitingForTurn(t *testing.T) {
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	if items := inboxOf(map[string]*aiv1.AgentProject{"agents/shop": p}, []aiv1.AgentTask{waitingSpec()}, time.Now()); len(items) != 0 {
		t.Fatalf("inbox = %+v", items)
	}
}

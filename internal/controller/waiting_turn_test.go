package controller

import (
	"strings"
	"testing"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const waitingLine = "⏳ waiting for its turn: %7, %8 go first. Comment `/agent start` to plan it now (owners)."

func TestRenderTrackingStates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(st *aiv1.AgentTask, ap *aiv1.AutopilotStatus)
		want  string
	}{
		{"waiting, ahead sorted", func(st *aiv1.AgentTask, _ *aiv1.AutopilotStatus) {
			st.Spec.QueuedBehind = []int64{8, 7}
		}, waitingLine},
		{"waiting, the turn has come", func(*aiv1.AgentTask, *aiv1.AutopilotStatus) {}, "⏳ waiting for its turn, planning starts on the next poll."},
		{"start asked for", func(st *aiv1.AgentTask, ap *aiv1.AutopilotStatus) {
			st.Spec.QueuedBehind, ap.StartRequestedBy = []int64{7, 8}, "@alice"
		}, "📝 planning"},
		{"planning", func(st *aiv1.AgentTask, _ *aiv1.AutopilotStatus) { st.Spec.RunRequest = 1 }, "📝 planning"},
		{"planning failed", func(st *aiv1.AgentTask, _ *aiv1.AutopilotStatus) {
			st.Spec.RunRequest, st.Status.Run.ObservedRunRequest = 1, 1
		}, "⚠️ not planned"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 9, Title: "M9"}}}
			ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
			tc.setup(st, ap)
			a := &autopilot{st: st, ap: ap, log: logr.Discard(), spec: aiv1.AgentProjectSpec{Workflow: aiv1.WorkflowSpec{CommandPrefix: "/agent"}}}
			got := a.renderTracking()
			if !strings.Contains(got, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, got)
			}
			if tc.want != waitingLine && strings.Contains(got, "waiting for its turn:") {
				t.Fatalf("only a waiting milestone names the ones ahead:\n%s", got)
			}
		})
	}
}

// A waiting milestone's tracking issue names the older milestones and the
// way out; once its turn comes, it says planning.
func TestTrackingIssueOfAWaitingMilestone(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Milestones = []gitlab.Milestone{
		turnMilestone(7, "/agent autopilot\nFirst."),
		turnMilestone(8, "/agent autopilot\nSecond."),
		turnMilestone(9, "/agent autopilot\nThird."),
	}
	e.reconcile()
	e.reconcile()
	e.wantWaiting(9, 7, 8)
	tracking := e.trackingOf(9)
	if tracking == 0 {
		t.Fatalf("the waiting milestone has its tracking issue: %+v", e.specFor(9).Status.Autopilot)
	}
	body := e.gl.Issues[tracking].Description
	if !strings.Contains(body, waitingLine) || strings.Contains(body, "not planned") {
		t.Fatalf("the tracking issue says it waits:\n%s", body)
	}

	e.setPhase(7, aiv1.AutopilotDone)
	e.setPhase(8, aiv1.AutopilotDone)
	e.reconcile()
	e.reconcile()
	e.wantPlanning(9, "/agent autopilot\nThird.")
	body = e.gl.Issues[tracking].Description
	if !strings.Contains(body, "📝 planning") || strings.Contains(body, "waiting for its turn") {
		t.Fatalf("the tracking issue says planning once the turn came:\n%s", body)
	}
}

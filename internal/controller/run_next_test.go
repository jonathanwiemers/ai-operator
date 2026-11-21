package controller

import (
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// setOrder stores the project's milestone run order.
func (e *projectEnv) setOrder(iids ...int64) {
	e.t.Helper()
	p := e.project()
	orig := p.DeepCopy()
	p.Status.MilestoneOrder = iids
	if err := e.c.Status().Patch(e.ctx, &p, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *projectEnv) wantOrder(want ...int64) {
	e.t.Helper()
	if got := e.project().Status.MilestoneOrder; !slices.Equal(got, want) {
		e.t.Fatalf("status.milestoneOrder = %v, want %v", got, want)
	}
}

// setMark sets the deprecated run-next mark of milestone iid.
func (e *projectEnv) setMark(iid int64, by string) {
	e.t.Helper()
	e.patchStatus(e.specFor(iid).Name, func(s *aiv1.AgentTaskStatus) {
		if s.Autopilot == nil {
			s.Autopilot = &aiv1.AutopilotStatus{}
		}
		now := metav1.Now()
		s.Autopilot.StartRequestedBy, s.Autopilot.StartRequestedAt = by, &now
	})
}

// Right after the upgrade, the run-next marks seed the stored order once, so
// the queue stays as it was; after that new milestones join at the bottom
// and done, closed and off-autopilot ones leave the list.
func TestMilestoneOrderIsSeededOnceAndPruned(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Milestones = []gitlab.Milestone{turnMilestone(5, "/agent autopilot\nM5."), turnMilestone(6, "/agent autopilot\nM6."), turnMilestone(7, "/agent autopilot\nM7.")}
	e.reconcile()
	e.wantOrder(5, 6, 7)

	// The state before the upgrade: no stored order, %6 marked.
	e.setMark(6, "@alice")
	e.setOrder()
	e.reconcile()
	e.wantOrder(6, 5, 7)
	e.wantWaiting(7, 6, 5)

	// The marks are read once: a stored order is kept.
	e.setMark(7, "@alice")
	e.reconcile()
	e.wantOrder(6, 5, 7)

	// New milestones join at the bottom.
	e.gl.Milestones = append(e.gl.Milestones, turnMilestone(8, "/agent autopilot\nM8."), turnMilestone(4, "/agent autopilot\nM4."))
	e.reconcile()
	e.wantOrder(6, 5, 7, 4, 8)

	// Done, closed and off-autopilot milestones leave the list.
	e.setPhase(6, aiv1.AutopilotDone)
	e.gl.Milestones = slices.DeleteFunc(e.gl.Milestones, func(m gitlab.Milestone) bool { return m.IID == 8 })
	for i := range e.gl.Milestones {
		if e.gl.Milestones[i].IID == 4 {
			e.gl.Milestones[i].Description = "/agent spec\nM4."
		}
	}
	e.reconcile()
	e.wantOrder(5, 7)
}

// A milestone moved ahead of a running one plans while the running one is
// open; the running one lets its active issue finish and holds the next one.
// Moved back, the first one goes on.
func TestMovedMilestoneRunsBeforeTheRunningOne(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile() // #1 of %3 is active
	e.gl.Milestones = append(e.gl.Milestones, turnMilestone(5, "/agent autopilot\nYounger."))
	e.reconcile()
	e.wantWaiting(5, 3)

	e.setOrder(5, 3)
	e.reconcile()
	if st := e.specFor(5); st.Spec.RunRequest != 1 || st.Spec.QueuedBehind != nil {
		t.Fatalf("the moved %%5 plans with %%3 open: %+v", st.Spec)
	}
	if ap := e.specFor(3).Status.Autopilot; ap.Items[0].State != aiv1.AutopilotItemActive {
		t.Fatalf("%%3 keeps #1 active: %+v", ap)
	}
	e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "mergeable"
	e.reconcile()
	e.reconcile()
	ap := e.specFor(3).Status.Autopilot
	if !slices.Equal(e.gl.Merged, []int64{1}) || ap.Items[1].State != aiv1.AutopilotItemPending || e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatalf("#1 finishes, #2 waits behind %%5: merged %v, %+v, %v", e.gl.Merged, ap, e.gl.Labels(2))
	}
	if d := e.gl.Issues[ap.TrackingIssueIID].Description; !strings.Contains(d, "waiting for its turn: %5 go first") {
		t.Fatalf("the tracking issue must say which milestone goes first: %s", d)
	}

	e.setOrder(3, 5)
	e.reconcile()
	if ap := e.specFor(3).Status.Autopilot; ap.Items[1].State != aiv1.AutopilotItemActive {
		t.Fatalf("%%3 is first again and starts #2: %+v", ap)
	}
}

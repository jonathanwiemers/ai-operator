package controller

import (
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// setMark sets (by != "") or clears the run-next mark of milestone iid.
func (e *projectEnv) setMark(iid int64, by string) {
	e.t.Helper()
	e.patchStatus(e.specFor(iid).Name, func(s *aiv1.AgentTaskStatus) {
		if s.Autopilot == nil {
			s.Autopilot = &aiv1.AutopilotStatus{}
		}
		s.Autopilot.StartRequestedBy, s.Autopilot.StartRequestedAt = "", nil
		if by != "" {
			now := metav1.Now()
			s.Autopilot.StartRequestedBy, s.Autopilot.StartRequestedAt = by, &now
		}
	})
}

// A marked milestone runs before an older unmarked one: it starts its next
// issue while the older one is open, and the older one waits to plan behind
// it.
func TestMarkedMilestoneRunsBeforeAnOlderOne(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile() // #1 of %3 starts
	e.setMark(3, "@alice")
	e.gl.Milestones = append(e.gl.Milestones, turnMilestone(2, "/agent autopilot\nOlder."))
	e.reconcile()
	e.reconcile()
	e.wantWaiting(2, 3)

	e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "mergeable"
	e.reconcile()
	e.reconcile()
	if ap := e.specFor(3).Status.Autopilot; !slices.Equal(e.gl.Merged, []int64{1}) || ap.Items[1].State != aiv1.AutopilotItemActive {
		t.Fatalf("the marked %%3 merges #1 and starts #2 with %%2 open: merged %v, %+v", e.gl.Merged, ap)
	}
	e.wantWaiting(2, 3)
}

// A milestone passed by a marked younger one lets its active issue finish and
// holds the next one; with the mark cleared, age order is back.
func TestPassedMilestoneFinishesItsActiveIssue(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile() // #1 of %3 is active
	e.gl.Milestones = append(e.gl.Milestones, turnMilestone(5, "/agent autopilot\nYounger."))
	e.reconcile()
	e.wantWaiting(5, 3)

	e.setMark(5, "@alice")
	e.reconcile()
	if st := e.specFor(5); st.Spec.RunRequest != 1 || st.Spec.QueuedBehind != nil {
		t.Fatalf("the marked %%5 plans with %%3 open: %+v", st.Spec)
	}
	if ap := e.specFor(3).Status.Autopilot; ap.Items[0].State != aiv1.AutopilotItemActive {
		t.Fatalf("the passed %%3 keeps #1 active: %+v", ap)
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

	e.setMark(5, "")
	e.reconcile()
	if ap := e.specFor(3).Status.Autopilot; ap.Items[1].State != aiv1.AutopilotItemActive {
		t.Fatalf("mark cleared: %%3 is first again and starts #2: %+v", ap)
	}
}

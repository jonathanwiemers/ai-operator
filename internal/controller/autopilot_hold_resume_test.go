package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// heldRed is a milestone at limit 2 with #1 held for its red CI on sha-1.
func heldRed(t *testing.T) *projectEnv {
	t.Helper()
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.redRun(1, "sha-1")
	e.reconcile()
	e.wantHeld("CI red holds #1", 1, aiv1.PauseCIRed)
	return e
}

// wantUnheld fails unless iid is no longer held and the tracking issue said
// line once.
func (e *projectEnv) wantUnheld(what string, iid int64, line string) {
	e.t.Helper()
	for _, it := range e.autopilot().Items {
		if it.IID == iid && it.Hold != nil {
			e.t.Fatalf("%s: #%d still held: %+v", what, iid, it.Hold)
		}
	}
	if c := trackingComments(e); strings.Count(c, line) != 1 || strings.Contains(c, "Autopilot continues") {
		e.t.Fatalf("%s: want %q once, and no milestone resume:\n%s", what, line, c)
	}
}

func TestAutopilotHoldEndsWhenALaterRunIsGreenAndTheItemLands(t *testing.T) {
	e := heldRed(t)
	e.finishRun(1, "sha-1b", nil)
	e.reconcile()
	e.wantUnheld("a green later run ends the hold", 1,
		"▶️ #1 goes on: #1 is reviewed and green again (ended by a later run of #1, which reviewed a head someone else pushed).")
	for range 3 {
		e.reconcile()
	}
	if got := e.states(); got[1] != merged || e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("#1 lands once its hold ended: %v %+v", got, e.autopilot())
	}
}

func TestAutopilotHoldEndsWhenMergedByHand(t *testing.T) {
	e := heldRed(t)
	mr := e.tasks()[e.taskOf(1)].Status.Run.MergeRequestIID
	e.gl.MRs[mr].State = "merged"
	e.reconcile()
	e.wantUnheld("merged by hand", 1, "▶️ #1 goes on: !1 was merged (ended by hand: the merge request was merged).")
	if got := e.states(); got[1] != merged {
		t.Fatalf("#1 is merged: %v", got)
	}
}

func TestAutopilotHoldEndsWhenClosedByHand(t *testing.T) {
	e := heldRed(t)
	e.gl.Issues[1].State = "closed"
	e.reconcile()
	e.wantUnheld("closed by hand", 1, "▶️ #1 goes on: #1 was closed (ended by hand: the issue was closed).")
	if got := e.states(); got[1] == active {
		t.Fatalf("the closed #1 does not run any more: %v", got)
	}
}

func TestAutopilotHoldBaseFailingIsHealedOnceMainIsGreen(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, SHA: "main-x", IssueIID: 99, FailedJobs: []string{"test"}})
	name := e.finishRun(1, "sha-api", nil)
	mr := e.gl.MRs[1]
	mr.Title, mr.Draft, mr.DetailedMergeStatus = "issue", false, "mergeable"
	mr.DiffRefs = &gitlab.DiffRefs{BaseSHA: "main-x", StartSHA: "main-x", HeadSHA: "sha-api"}
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateBaseFailing, FailedJobs: []string{"test"}}
	})
	e.reconcile()
	e.reconcile()
	e.wantHeld("BaseFailing holds #1", 1, aiv1.PauseCIRed)

	e.mainGreenAt("main-y")
	e.reconcile()
	e.wantUnheld("the rebase heals the hold", 1,
		"▶️ #1 goes on: `main` is green at main-y: rebasing !1, whose CI failed on jobs that also failed on `main` (ended by the operator: it rebased the merge request).")
	if it := e.autopilot().Items[0]; !slices.Equal(e.gl.Rebased, []int64{1}) || it.HealedOn != "main-y" {
		t.Fatalf("!1 rebased once, healed on main-y: rebased %v, %+v", e.gl.Rebased, it)
	}
	e.greenRebased(name, 1)
	for range 3 {
		e.reconcile()
	}
	if !slices.Contains(e.gl.Merged, int64(1)) || !slices.Equal(e.gl.Rebased, []int64{1}) {
		t.Fatalf("the rebased !1 lands: merged %v, rebased %v, %+v", e.gl.Merged, e.gl.Rebased, e.autopilot())
	}
}

func TestAutopilotHoldLaterRunFailingForAnotherReasonUpdatesTheHold(t *testing.T) {
	e := heldRed(t)
	e.finishRun(1, "sha-1b", func(r *aiv1.RunStatus) { r.BlockingFindings = 1 })
	e.reconcile()
	h := e.wantHeld("held anew", 1, aiv1.PauseFindings)
	e.reconcile()
	if c := trackingComments(e); strings.Count(c, "⏸️ #1 is held: ") != 2 || strings.Contains(c, "▶️") || !strings.Contains(c, "⏸️ #1 is held: "+h.Reason) {
		t.Fatalf("the new reason is said once, the hold stays:\n%s", c)
	}
}

func TestAutopilotHoldEndingLiftsStuck(t *testing.T) {
	e := newParallelEnv(t, 2, true) // #4 waits for #1 and #2
	e.reconcile()
	e.reconcile()
	e.redRun(1, "sha-1")
	e.reconcile()
	name := e.finishRun(2, "sha-2", nil)
	e.gl.MRs[e.tasks()[name].Status.Run.MergeRequestIID].DetailedMergeStatus = "not_approved"
	e.gl.MRs[e.tasks()[name].Status.Run.MergeRequestIID].Draft = false
	e.reconcile()
	if ap := e.autopilot(); ap.PauseKind != aiv1.PauseStuck {
		t.Fatalf("every running issue held: stuck: %+v", ap)
	}
	e.finishRun(1, "sha-1b", nil)
	e.reconcile()
	ap := e.autopilot()
	c := trackingComments(e)
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[0].Hold != nil || !strings.Contains(c, "▶️ #1 goes on: #1 is reviewed and green again") ||
		!strings.Contains(c, "▶️ Autopilot continues: #1 goes on (ended by the operator: the cause went away).") {
		t.Fatalf("the end of #1's hold lifts stuck: %+v\n%s", ap, c)
	}
}

// A mergeRequest hold for a head someone pushed ends only once a run took
// that head over.
func TestAutopilotHoldMovedHeadEndsAfterATakeover(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-1", nil)
	mr := e.tasks()[name].Status.Run.MergeRequestIID
	e.gl.MRs[mr].SHA = "sha-push"
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Items[0].Hold = &aiv1.ItemHold{Kind: aiv1.PauseMergeRequest, Reason: "!1 has a head nobody reviewed", ResumeWhenReady: true, RunHead: "sha-1"}
	})
	e.reconcile()
	e.reconcile()
	e.wantHeld("no run took the pushed head over", 1, aiv1.PauseMergeRequest)
	e.finishRun(1, "sha-push", nil)
	e.reconcile()
	e.wantUnheld("a run took the head over", 1, "▶️ #1 goes on: #1 is reviewed and green again")
}

// A command's run of a held issue is recorded on its hold, not the
// milestone's pause, and ends the hold as a takeover.
func TestLaterRunViaForAHeldItem(t *testing.T) {
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedFor: 2, PauseRunBy: aiv1.PauseRunByAutopilot, PauseRunHead: "sha-2",
		Items: []aiv1.AutopilotItem{{IID: 1, State: active, Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, RunHead: "sha-1"}}, {IID: 2, State: active}}}
	if laterRunVia(ap, 1, "sha-1") != aiv1.ResumedViaCauseGone {
		t.Fatalf("nobody asked during the hold, same head: cause gone")
	}
	if !notePauseRun(ap, 1, "@alice", "sha-1") || notePauseRun(ap, 1, "@alice", "sha-1") {
		t.Fatal("the run is recorded once")
	}
	if h := ap.Items[0].Hold; h.RunBy != "@alice" || h.RunHead != "sha-1" || ap.PauseRunBy != aiv1.PauseRunByAutopilot || ap.PauseRunHead != "sha-2" {
		t.Fatalf("recorded on the hold, the milestone's pause untouched: %+v, %+v", h, ap)
	}
	if got := laterRunVia(ap, 1, "sha-1c"); got != aiv1.ResumedViaTakeover {
		t.Fatalf("laterRunVia of the held #1 = %q, want takeover", got)
	}
	if got := laterRunVia(ap, 2, "sha-2"); got != aiv1.ResumedViaReReview {
		t.Fatalf("laterRunVia of the paused #2 = %q, want reReview", got)
	}
	// While the milestone runs, a held issue's run is still recorded.
	ap.Phase = aiv1.AutopilotRunning
	if !notePauseRun(ap, 1, pilotSelf, "sha-1c") || ap.Items[0].Hold.RunBy != pilotSelf {
		t.Fatalf("a held issue's run is recorded while running: %+v", ap.Items[0].Hold)
	}
}

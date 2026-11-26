package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// looseItem is the queue's item of iid; it fails if there is none.
func (e *projectEnv) looseItem(iid int64) aiv1.AutopilotItem {
	e.t.Helper()
	for _, it := range e.queue().Status.Autopilot.Items {
		if it.IID == iid {
			return it
		}
	}
	e.t.Fatalf("no queue item #%d: %v", iid, e.looseItems())
	return aiv1.AutopilotItem{}
}

// mergeLoose finishes iid's run green and polls until the queue merged it.
func (e *projectEnv) mergeLoose(iid int64) {
	e.t.Helper()
	e.finishRun(iid, fmt.Sprintf("sha-%d", iid), nil)
	for range 4 {
		if e.looseItem(iid).State == merged {
			return
		}
		e.reconcile()
	}
	e.t.Fatalf("#%d not merged: %v", iid, e.looseItems())
}

// newLooseRunEnv is a queue at maxParallel max (0: the default, 1) that took
// #350, #421 and #46, none of them in work, after the poll that took them.
func newLooseRunEnv(t *testing.T, max int32) (*projectEnv, int64) {
	t.Helper()
	e, tracking := newLooseCommandEnv(t)
	if max > 0 {
		e.patchProject(func(p *aiv1.AgentProject) { p.Spec.LooseIssues.MaxParallel = max })
	}
	for _, iid := range []int64{350, 421, 46} {
		e.gl.AddIssue(iid, fmt.Sprintf("Loose %d", iid))
	}
	e.sayLoose(tracking, bob, "/agent take #350 #421 #46")
	return e, tracking
}

func TestLooseRunStartsInOrderAndMergesOnGreen(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.wantItems("the first taken starts, one at a time", "350:Active", "421:Pending", "46:Pending")
	if !e.gl.Issues[350].HasLabel("agent::ready") || len(e.gl.Issues[421].Labels) != 0 {
		t.Fatalf("#350 handed over, #421 not: %v %v", e.gl.Labels(350), e.gl.Labels(421))
	}
	e.reconcile() // picked up
	e.wantItems("still one at a time", "350:Active", "421:Pending", "46:Pending")

	e.mergeLoose(350)
	mr := e.looseItem(350).MergeRequestIID
	if !slices.Contains(e.gl.Merged, mr) || e.gl.Issues[350].State != "closed" {
		t.Fatalf("!%d merged and #350 closed: merged %v, state %s", mr, e.gl.Merged, e.gl.Issues[350].State)
	}
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, fmt.Sprintf("- [x] #350: merged !%d\n", mr)) {
		t.Fatalf("the tracking issue shows the merge with its MR:\n%s", d)
	}
	if ap := e.queue().Status.Autopilot; len(ap.Releases) != 0 || ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("no releases, the queue runs on: %+v", ap)
	}
	e.reconcile()
	e.wantItems("the next taken starts", "350:Merged", "421:Active", "46:Pending")
}

func TestLooseRunHoldsRedCIAndTheOtherMerges(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 2)
	e.wantItems("two of three run", "350:Active", "421:Active", "46:Pending")
	e.reconcile()

	e.redRun(350, "sha-350")
	e.reconcile()
	it := e.looseItem(350)
	if it.Hold == nil || it.Hold.Kind != aiv1.PauseCIRed {
		t.Fatalf("red CI holds #350: %+v", it)
	}
	ap := e.queue().Status.Autopilot
	if ap.Phase != aiv1.AutopilotRunning || ap.PausedReason != "" {
		t.Fatalf("the queue runs on: %+v", ap)
	}
	e.wantItems("the held item keeps its slot", "350:Active", "421:Active", "46:Pending")
	if c := strings.Join(botComments(e, tracking), "\n"); strings.Count(c, "⏸️ #350 is held: ") != 1 || strings.Contains(c, "Autopilot paused") {
		t.Fatalf("one hold comment, no pause:\n%s", c)
	}
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "### Held\n\n- #350: CI of !") {
		t.Fatalf("the tracking issue shows the hold with its reason:\n%s", d)
	}

	e.mergeLoose(421)
	e.reconcile()
	e.wantItems("#421 merges, #46 starts, #350 stays held", "350:Active", "421:Merged", "46:Active")
	if mr := e.looseItem(350).MergeRequestIID; slices.Contains(e.gl.Merged, mr) {
		t.Fatalf("the held #350 is not merged: %v", e.gl.Merged)
	}
	if task := e.tasks()[e.taskOf(350)]; task.Spec.Source.AutopilotPause != e.looseItem(350).Hold.Reason {
		t.Fatalf("the implement task gets the hold as its pause: %q", task.Spec.Source.AutopilotPause)
	}
}

func TestLooseRunHoldsAtMaxParallelOne(t *testing.T) {
	e, _ := newLooseRunEnv(t, 0)
	e.reconcile()
	e.redRun(350, "sha-350")
	e.reconcile()
	e.reconcile()
	if it := e.looseItem(350); it.Hold == nil || it.Hold.Kind != aiv1.PauseCIRed {
		t.Fatalf("red CI holds #350 also at a limit of 1: %+v", it)
	}
	if ap := e.queue().Status.Autopilot; ap.Phase != aiv1.AutopilotRunning || ap.PauseKind != "" {
		t.Fatalf("no queue pause, also not stuck: %+v", ap)
	}
	e.wantItems("the held item keeps the only slot", "350:Active", "421:Pending", "46:Pending")
}

func TestLooseRunImplementTaskOwnersAndTitlePattern(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.setTitlePattern("^Release")
	e.reconcile()
	task := e.tasks()[e.taskOf(350)]
	if !task.Spec.Autopilot || !slices.Equal(task.Spec.Owners, []string{"bob"}) || !slices.Equal(task.Spec.OwnerSubjects, []string{"alice@example.com"}) {
		t.Fatalf("the queue's owners and autopilot, kept by the title pattern: %+v", task.Spec)
	}

	// Dropped while active: the manual flow, with the project's owners.
	e.say(tracking, bob, "/agent drop #350")
	e.gl.Issues[350].Milestone = &gitlab.Milestone{ID: 7, IID: 7, Title: "Release 1"} // the pattern keeps it
	e.reconcile()
	task = e.tasks()[e.taskOf(350)]
	if task.Spec.Autopilot || len(task.Spec.OwnerSubjects) != 0 || task.Spec.Source.AutopilotPause != "" {
		t.Fatalf("a dropped issue is manual again: %+v", task.Spec)
	}
}

func TestLooseRunTitlePatternSkipsDroppedIssues(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.setTitlePattern("^Release")
	e.reconcile()
	e.taskOf(350)
	e.say(tracking, bob, "/agent drop #350")
	e.reconcile()
	for _, task := range e.tasks() {
		if task.Spec.Issue != nil && task.Spec.Issue.IID == 350 {
			t.Fatal("out of the queue, the title pattern skips it again")
		}
	}
}

func TestLooseRunLeavesByCloseAndByMilestone(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.gl.Issues[350].State = "closed"
	e.gl.Issues[46].Milestone = &gitlab.Milestone{ID: 8, IID: 8, Title: "Q4 @all"}
	e.reconcile()
	e.wantItems("both leave, the next starts", "350:Left", "421:Active", "46:Left")
	title := inertLine("Q4 @all", 100)
	if it := e.looseItem(46); it.LeftReason != fmt.Sprintf("moved into milestone %q", title) || it.LeftAt == nil {
		t.Fatalf("moved into a milestone: %+v", it)
	}
	e.wantNotes(tracking, "↩️ #350 left the queue: closed.", 1)
	e.wantNotes(tracking, fmt.Sprintf("↩️ #46 left the queue: moved into milestone %q.", title), 1)

	// Merged by hand while active: recorded as merged, not as left.
	e.reconcile()
	e.finishRun(421, "sha-421", nil)
	mr := e.tasks()[e.taskOf(421)].Status.Run.MergeRequestIID
	e.gl.MRs[mr].State = "merged"
	e.gl.Issues[421].State = "closed"
	e.gl.ClosedBy(421, mr)
	e.reconcile()
	if it := e.looseItem(421); it.State != merged || it.MergeRequestIID != mr {
		t.Fatalf("merged by hand: %+v", it)
	}
}

func TestLooseRunBesideAMilestone(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.gl.Users = []gitlab.User{bob}
	e.patchProject(func(p *aiv1.AgentProject) {
		p.Spec.Notify.Users = []string{"bob"}
		p.Spec.LooseIssues = aiv1.LooseIssuesSpec{Enabled: true}
	})
	e.gl.AddIssue(350, "Loose")
	e.reconcile()
	e.say(e.queue().Status.Autopilot.TrackingIssueIID, bob, "/agent take #350")
	e.reconcile()
	e.wantStates("the milestone runs as before", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: pending})
	e.wantItems("the queue runs beside it", "350:Active")

	e.mergeLoose(350)
	e.mergeItem(1)
	e.reconcile()
	e.wantStates("the milestone is not affected", map[int64]aiv1.AutopilotItemState{1: merged, 2: active, 4: active})
	if ap := e.autopilot(); slices.ContainsFunc(ap.Items, func(it aiv1.AutopilotItem) bool { return it.IID == 350 }) || strings.Contains(trackingComments(e), "#350") {
		t.Fatalf("the milestone knows nothing of #350: %+v", ap.Items)
	}
	if task := e.tasks()[e.taskOf(2)]; !task.Spec.Autopilot || task.Spec.Milestone == nil {
		t.Fatalf("a milestone issue stays the milestone's: %+v", task.Spec)
	}
}

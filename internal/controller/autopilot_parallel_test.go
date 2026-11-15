package controller

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// newParallelEnv is an autopilot milestone at limit max, planned into #1
// (API), #2 (UI) and #4 (Docs, after both when dependant, else a root too);
// #3 is the tracking issue.
func newParallelEnv(t *testing.T, max int32, dependant bool) *projectEnv {
	t.Helper()
	e := newUnplannedAutopilotEnv(t)
	e.releaseOff()
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Autopilot = &aiv1.AutopilotSpec{MaxParallel: max} }) // 0: the default
	docs := e.gl.AddIssue(4, "Docs", "ai-generated", "agent::draft")
	m := e.gl.Milestones[0]
	docs.Milestone = &m
	docs.Description = "Write it.\n<!-- ai-operator:key=docs -->"
	var deps []int64
	if dependant {
		deps = []int64{1, 2}
	}
	st := e.tasks()[specTaskName]
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = st.Spec.RunRequest // planned
		s.Plan = []aiv1.PlannedIssue{{Key: "api", IID: 1, Title: "API"}, {Key: "ui", IID: 2, Title: "UI"}, {Key: "docs", IID: 4, Title: "Docs", DependsOn: deps}}
	})
	return e
}

// states are the queue's item states by issue.
func (e *projectEnv) states() map[int64]aiv1.AutopilotItemState {
	e.t.Helper()
	out := map[int64]aiv1.AutopilotItemState{}
	for _, it := range e.autopilot().Items {
		out[it.IID] = it.State
	}
	return out
}

// mergeItem finishes iid's run green and polls until autopilot merged it.
func (e *projectEnv) mergeItem(iid int64) {
	e.t.Helper()
	e.finishRun(iid, fmt.Sprintf("sha-%d", iid), nil)
	for range 4 {
		if e.states()[iid] == aiv1.AutopilotItemMerged {
			return
		}
		e.reconcile()
	}
	if e.states()[iid] != aiv1.AutopilotItemMerged {
		e.t.Fatalf("#%d not merged: %+v", iid, e.autopilot())
	}
}

// wantStates fails unless the queue is in want and running (not paused).
func (e *projectEnv) wantStates(what string, want map[int64]aiv1.AutopilotItemState) {
	e.t.Helper()
	ap := e.autopilot()
	if got := e.states(); !maps.Equal(got, want) || ap.Phase == aiv1.AutopilotPaused {
		e.t.Fatalf("%s: want %v, got %v (phase %s %s: %s)", what, want, got, ap.Phase, ap.PauseKind, ap.PausedReason)
	}
}

const (
	pending = aiv1.AutopilotItemPending
	active  = aiv1.AutopilotItemActive
	merged  = aiv1.AutopilotItemMerged
)

func TestAutopilotParallelRootsStartTogetherAndADependantWaitsForBoth(t *testing.T) {
	e := newParallelEnv(t, 0, true) // the default limit, 3
	e.reconcile()
	e.wantStates("two roots start together", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: pending})
	if !e.gl.Issues[1].HasLabel("agent::ready") || !e.gl.Issues[2].HasLabel("agent::ready") || !e.gl.Issues[4].HasLabel("agent::draft") {
		t.Fatalf("#1 and #2 handed over, #4 not: %v %v %v", e.gl.Labels(1), e.gl.Labels(2), e.gl.Labels(4))
	}
	e.reconcile() // picked up

	e.mergeItem(2)
	e.reconcile()
	e.wantStates("the dependant waits for #1, not stuck", map[int64]aiv1.AutopilotItemState{1: active, 2: merged, 4: pending})

	e.mergeItem(1)
	e.reconcile()
	e.wantStates("the dependant starts after both", map[int64]aiv1.AutopilotItemState{1: merged, 2: merged, 4: active})
	if !e.gl.Issues[4].HasLabel("agent::ready") {
		t.Fatalf("#4 handed over: %v", e.gl.Labels(4))
	}
	e.reconcile()
	e.mergeItem(4)
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotDone {
		t.Fatalf("done once all are merged: %+v", ap)
	}
}

func TestAutopilotParallelRespectsTheLimit(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.wantStates("two of three roots run", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: pending})
	e.finishRun(1, "sha-1", nil)
	e.reconcile() // #1 lands: marked ready, merged on the next poll
	e.wantStates("the third starts once a slot is free (the landing item does not count)", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})
	e.reconcile()
	e.wantStates("#1 merged", map[int64]aiv1.AutopilotItemState{1: merged, 2: active, 4: active})
}

func TestAutopilotParallelLimitOneKeepsTheOrder(t *testing.T) {
	e := newParallelEnv(t, 1, false)
	var started []int64
	for range 3 {
		e.reconcile()
		var running []int64
		for _, it := range e.autopilot().Items {
			if it.State == active {
				running = append(running, it.IID)
			}
		}
		if len(running) != 1 || e.autopilot().Phase != aiv1.AutopilotRunning {
			t.Fatalf("one issue at a time at limit 1: %+v", e.autopilot())
		}
		started = append(started, running[0])
		e.reconcile()
		e.mergeItem(running[0])
	}
	if !slices.Equal(started, []int64{1, 2, 4}) {
		t.Fatalf("issues must start one after another in queue order: %v", started)
	}
}

func TestAutopilotParallelMainRedHoldsTheSecondActiveItem(t *testing.T) {
	e := newParallelEnv(t, 0, false)
	e.reconcile()
	e.reconcile()
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 99, FailedJobs: []string{"test"}})
	e.finishRun(2, "sha-ui", nil)
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseMainRed || ap.PausedFor != 2 || len(e.gl.Merged) != 0 {
		t.Fatalf("a red main must hold the second active item's merge: merged %v, %+v", e.gl.Merged, ap)
	}
	e.reconcile()
	if len(e.gl.Merged) != 0 {
		t.Fatalf("nothing merges while main is red: %v", e.gl.Merged)
	}

	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen})
	for range 3 {
		e.reconcile()
	}
	if got := e.states(); got[2] != merged || got[1] != active || e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("green again: #2 merges, #1 still runs: %v %+v", got, e.autopilot())
	}
}

// Above a limit of 1, a BaseFailing ciRed holds the item instead of pausing
// the milestone, and the other issues go on. The heal of a held item is
// TestAutopilotHoldBaseFailingIsHealedOnceMainIsGreen; at limit 1 it pauses
// and heals as before (TestAutopilotRebasesABaseFailingMROnceMainIsGreen).
func TestAutopilotParallelBaseFailingHoldsTheSecondActiveItem(t *testing.T) {
	e := newParallelEnv(t, 0, false)
	e.reconcile()
	e.reconcile()
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, SHA: "main-x", IssueIID: 99, FailedJobs: []string{"test"}})
	name := e.finishRun(2, "sha-ui", nil)
	mr := e.gl.MRs[1]
	mr.Title, mr.Draft, mr.DetailedMergeStatus = "issue", false, "mergeable"
	mr.DiffRefs = &gitlab.DiffRefs{BaseSHA: "main-x", StartSHA: "main-x", HeadSHA: "sha-ui"}
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.CI = &aiv1.CIStatus{SHA: "sha-ui", State: aiv1.CIStateBaseFailing, FailedJobs: []string{"test"}}
	})
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if h := ap.Items[1].Hold; ap.Items[1].IID != 2 || h == nil || h.Kind != aiv1.PauseCIRed || !h.ResumeWhenReady || h.RunHead != "sha-ui" {
		t.Fatalf("a BaseFailing MR of #2 must hold it ciRed: %+v", ap.Items[1])
	}
	e.wantStates("the milestone goes on", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})
}

// A held item is not driven and frees its slot: with limit 2, #1 held and #2
// running, the pending root #4 starts, and #1's finished run is left alone.
func TestAutopilotParallelHeldItemIsNotDrivenAndFreesItsSlot(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.wantStates("two of three roots run", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: pending})
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		// Not resumeWhenReady: the green run below would end the hold.
		s.Autopilot.Items[0].Hold = &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "CI of !1 is red"}
	})
	e.finishRun(1, "sha-1", nil)
	e.reconcile()
	e.wantStates("the pending root starts in the held item's slot", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})
	e.reconcile()
	ap := e.autopilot()
	it := ap.Items[0]
	if it.IID != 1 || it.Hold == nil || it.Hold.Kind != aiv1.PauseCIRed || it.ReadyAt != nil || it.MarkedReady || ap.Landing == 1 {
		t.Fatalf("the held #1 must not be driven: %+v (landing %d)", it, ap.Landing)
	}
	if mr := e.gl.MRs[1]; !mr.Draft || len(e.gl.Merged) != 0 {
		t.Fatalf("no GitLab calls for the held item's MR: draft %t, merged %v", mr.Draft, e.gl.Merged)
	}
}

// A held item neither blocks the landing lane nor waits in it: #1 held while
// landing (and ready first), #2 lands and merges.
func TestAutopilotParallelHeldLandingItemFreesTheLane(t *testing.T) {
	e := newParallelEnv(t, 0, false)
	e.reconcile()
	e.reconcile()
	e.wantStates("three roots run", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		ready := metav1.NewTime(time.Now().Add(-time.Hour))
		s.Autopilot.Items[0].Hold = &aiv1.ItemHold{Kind: aiv1.PauseMergeRequest, Reason: "!9 cannot be merged", Since: ready}
		s.Autopilot.Items[0].ReadyAt, s.Autopilot.Landing = &ready, 1
	})
	e.mergeItem(2)
	if got := e.states(); got[1] != active || e.autopilot().Items[0].Hold == nil {
		t.Fatalf("#1 stays held: %v %+v", got, e.autopilot().Items[0])
	}
}

package controller

import (
	"fmt"
	"maps"
	"slices"
	"testing"

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
	if max > 0 {
		e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Autopilot = &aiv1.AutopilotSpec{MaxParallel: max} })
	}
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
	e.mergeItem(1)
	e.wantStates("no start in the poll of the merge", map[int64]aiv1.AutopilotItemState{1: merged, 2: active, 4: pending})
	e.reconcile()
	e.wantStates("the third starts once a slot is free", map[int64]aiv1.AutopilotItemState{1: merged, 2: active, 4: active})
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

func TestAutopilotParallelHealsTheSecondActiveItem(t *testing.T) {
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
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseCIRed || ap.PausedFor != 2 {
		t.Fatalf("a BaseFailing MR of #2 must pause ciRed: %+v", ap)
	}

	e.mainGreenAt("main-y")
	e.reconcile()
	ap := e.autopilot()
	if !slices.Equal(e.gl.Rebased, []int64{1}) || ap.Phase != aiv1.AutopilotRunning || ap.Items[1].IID != 2 || ap.Items[1].HealedOn != "main-y" {
		t.Fatalf("main green must rebase #2's !1 and lift the pause: rebased %v, %+v", e.gl.Rebased, ap)
	}
}

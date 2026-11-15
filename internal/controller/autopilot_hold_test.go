package controller

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// redRun finishes iid's run with a red pipeline on sha.
func (e *projectEnv) redRun(iid int64, sha string) string {
	e.t.Helper()
	name := e.finishRun(iid, sha, nil)
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.CI = &aiv1.CIStatus{SHA: sha, State: aiv1.CIStateFailed, FailedJobs: []string{"test"}}
	})
	return name
}

// wantHeld fails unless iid is held for kind while the milestone runs.
func (e *projectEnv) wantHeld(what string, iid int64, kind aiv1.PauseKind) *aiv1.ItemHold {
	e.t.Helper()
	ap := e.autopilot()
	for _, it := range ap.Items {
		if it.IID != iid {
			continue
		}
		if it.Hold == nil || it.Hold.Kind != kind || ap.Phase != aiv1.AutopilotRunning || ap.PausedReason != "" || ap.PauseKind != "" || ap.Landing == iid {
			e.t.Fatalf("%s: want #%d held %s and the milestone running: %+v", what, iid, kind, ap)
		}
		return it.Hold
	}
	e.t.Fatalf("%s: no item #%d: %+v", what, iid, ap)
	return nil
}

func TestAutopilotHoldCIRedHoldsTheItemWhileTheOtherMerges(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.wantStates("two of three run", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: pending})
	e.redRun(1, "sha-1")
	e.reconcile()
	h := e.wantHeld("CI red holds #1", 1, aiv1.PauseCIRed)
	if !h.ResumeWhenReady || h.RunHead != "sha-1" || h.Since.IsZero() {
		t.Fatalf("a later run can end the hold, from the pushed head: %+v", h)
	}
	e.wantStates("the held item's slot starts #4", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})

	e.mergeItem(2)
	e.reconcile()
	e.wantStates("#2 merges, #1 stays held", map[int64]aiv1.AutopilotItemState{1: active, 2: merged, 4: active})
	e.wantHeld("still held", 1, aiv1.PauseCIRed)
	if mr1 := e.tasks()[e.taskOf(1)].Status.Run.MergeRequestIID; slices.Contains(e.gl.Merged, mr1) {
		t.Fatalf("the held #1 is not merged: %v", e.gl.Merged)
	}
	c := trackingComments(e)
	if strings.Count(c, "⏸️ #1 is held: ") != 1 || !strings.Contains(c, ". The other issues go on.\n\nWays out:\n- ") || strings.Contains(c, "Autopilot paused") {
		t.Fatalf("one hold comment with the ways out, no pause:\n%s", c)
	}
}

func TestAutopilotHoldFindingsAndMergeRequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		run   func(*aiv1.RunStatus)
		mr    func(*gitlab.MergeRequest)
		kind  aiv1.PauseKind
		byRun bool
	}{
		{name: "findings", run: func(r *aiv1.RunStatus) { r.BlockingFindings = 1 }, kind: aiv1.PauseFindings, byRun: true},
		{name: "not approved", mr: func(m *gitlab.MergeRequest) { m.DetailedMergeStatus, m.Draft = "not_approved", false }, kind: aiv1.PauseMergeRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newParallelEnv(t, 2, false)
			e.reconcile()
			e.reconcile()
			name := e.finishRun(1, "sha-1", tc.run)
			if tc.mr != nil {
				tc.mr(e.gl.MRs[e.tasks()[name].Status.Run.MergeRequestIID])
			}
			e.reconcile()
			if h := e.wantHeld(tc.name, 1, tc.kind); h.ResumeWhenReady != tc.byRun {
				t.Fatalf("resumeWhenReady %t, want %t: %+v", h.ResumeWhenReady, tc.byRun, h)
			}
			e.wantStates("the others go on", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})
			e.reconcile()
			if n := strings.Count(trackingComments(e), "⏸️ #1 is held: "); n != 1 || len(e.gl.Merged) != 0 {
				t.Fatalf("one hold comment over polls, nothing merged: %d, %v", n, e.gl.Merged)
			}
		})
	}
}

func TestAutopilotHoldOwnerPauseStopsAll(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.redRun(1, "sha-1")
	e.reconcile()
	e.wantHeld("#1 held", 1, aiv1.PauseCIRed)
	e.wantStates("#4 starts in #1's slot", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})

	e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
	e.reconcile()
	e.readyBehind(2, "need_rebase")
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner || ap.Landing != 0 || len(e.gl.Rebased) != 0 || len(e.gl.Merged) != 0 {
		t.Fatalf("an owner's pause stops landing too: rebased %v, merged %v, %+v", e.gl.Rebased, e.gl.Merged, ap)
	}
}

func TestAutopilotHoldOwnerPauseStopsStarts(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
	e.redRun(1, "sha-1")
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner || e.states()[4] != pending || !e.gl.Issues[4].HasLabel("agent::draft") {
		t.Fatalf("nothing starts while the owner paused: %v, %+v", e.states(), ap)
	}
}

func TestAutopilotHoldEveryRunningItemHeldIsStuck(t *testing.T) {
	e := newParallelEnv(t, 2, true) // #4 waits for #1 and #2
	e.reconcile()
	e.reconcile()
	e.redRun(1, "sha-1")
	e.reconcile()
	e.wantHeld("#1 held", 1, aiv1.PauseCIRed)
	name := e.finishRun(2, "sha-2", nil)
	e.gl.MRs[e.tasks()[name].Status.Run.MergeRequestIID].DetailedMergeStatus = "not_approved"
	e.gl.MRs[e.tasks()[name].Status.Run.MergeRequestIID].Draft = false
	e.reconcile()
	ap := e.autopilot()
	want := "nothing can move: every running issue is held (#1: ciRed, #2: mergeRequest); #4 waits for #1"
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseStuck || ap.PausedFor != 0 || ap.PausedReason != want {
		t.Fatalf("all held, nothing startable: stuck\nwant %q\n got %q (%+v)", want, ap.PausedReason, ap)
	}
	e.reconcile()
	e.reconcile()
	c := trackingComments(e)
	if strings.Count(c, "Autopilot paused: nothing can move") != 1 || strings.Contains(c, "Autopilot continues") {
		t.Fatalf("stuck stays, announced once:\n%s", c)
	}
}

// The same reason twice gives one comment; a new reason holds anew and keeps
// the clock of the same kind and the run head.
func TestAutopilotHoldSameReasonOnce(t *testing.T) {
	gl := gitlabfake.New()
	gl.AddIssue(7, "Tracking")
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, TrackingIssueIID: 7, Landing: 1,
		Items: []aiv1.AutopilotItem{{IID: 1, State: active}, {IID: 2, State: active}}}
	task := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha-1"}}}
	a := &autopilot{r: &AgentProjectReconciler{Now: func() time.Time { return now }}, gl: gl, spec: aiv1.AgentProjectSpec{}.WithDefaults(),
		st: &aiv1.AgentTask{}, ap: ap, tasks: map[int64]*aiv1.AgentTask{1: task}, log: logr.Discard()}
	ctx := context.Background()
	for range 2 {
		if err := a.pause(ctx, aiv1.PauseCIRed, 1, "CI of !1 is red"); err != nil {
			t.Fatal(err)
		}
	}
	h := ap.Items[0].Hold
	if n := len(gl.IssueNotes[7]); n != 1 || h == nil || h.RunHead != "sha-1" || !h.Since.Time.Equal(now) || ap.Landing != 0 || ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("one comment, held from the pushed head, out of the lane: %d notes, %+v, %+v", n, h, ap)
	}
	now = now.Add(time.Hour)
	task.Status.Run.PushedSHA = "sha-1b"
	h.RunBy = "alice"
	if err := a.pause(ctx, aiv1.PauseCIRed, 1, "CI of !1 is red on test"); err != nil {
		t.Fatal(err)
	}
	if h := ap.Items[0].Hold; len(gl.IssueNotes[7]) != 2 || h.Reason != "CI of !1 is red on test" || !h.Since.Time.Equal(now.Add(-time.Hour)) || h.RunHead != "sha-1" || h.RunBy != "alice" {
		t.Fatalf("a new reason comments again and keeps the clock and the run: %+v", h)
	}
	if err := a.pause(ctx, aiv1.PauseFindings, 1, "findings"); err != nil {
		t.Fatal(err)
	}
	if h := ap.Items[0].Hold; !h.Since.Time.Equal(now) || h.Kind != aiv1.PauseFindings {
		t.Fatalf("a new kind restarts the clock: %+v", h)
	}
	// Milestone kinds and the main-red fix still pause the milestone.
	if err := a.pause(ctx, aiv1.PauseConfig, 2, "bad config"); err != nil {
		t.Fatal(err)
	}
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseConfig || ap.Items[1].Hold != nil {
		t.Fatalf("a milestone kind pauses the milestone: %+v", ap)
	}
}

func TestAutopilotHoldNotForTheMainRedFixNorAtLimitOne(t *testing.T) {
	for _, tc := range []struct {
		name     string
		max      int32
		fix      bool
		wantHold bool
	}{
		{name: "limit 2", max: 2, wantHold: true},
		{name: "limit 1", max: 1},
		{name: "main-red fix", max: 2, fix: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gl := gitlabfake.New()
			gl.AddIssue(7, "Tracking")
			ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, TrackingIssueIID: 7, Items: []aiv1.AutopilotItem{{IID: 1, State: active}}}
			if tc.fix {
				ap.MainRedFix = &aiv1.AutopilotItem{IID: 1, State: active}
			}
			spec := aiv1.AgentProjectSpec{Autopilot: &aiv1.AutopilotSpec{MaxParallel: tc.max}}.WithDefaults()
			a := &autopilot{r: &AgentProjectReconciler{}, gl: gl, spec: spec, st: &aiv1.AgentTask{}, ap: ap, log: logr.Discard()}
			if err := a.pause(context.Background(), aiv1.PauseCIRed, 1, "CI of !1 is red"); err != nil {
				t.Fatal(err)
			}
			held := a.item(1).Hold != nil
			if held != tc.wantHold || (ap.Phase == aiv1.AutopilotPaused) == tc.wantHold {
				t.Fatalf("held %t (want %t): %+v", held, tc.wantHold, ap)
			}
			note := gl.IssueNotes[7][0].Body
			if tc.wantHold != strings.Contains(note, "⏸️ #1 is held: CI of !1 is red. The other issues go on.") {
				t.Fatalf("comment: %s", note)
			}
		})
	}
}

// A skip drops the hold: unskipped and started again, the item is driven (and
// held anew for its red CI), not a held zombie.
func TestAutopilotHoldSkipDropsTheHold(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.redRun(1, "sha-1")
	e.reconcile()
	e.wantHeld("#1 held", 1, aiv1.PauseCIRed)
	tracking := e.autopilot().TrackingIssueIID
	owner := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddNote(tracking, owner, "/agent skip #1")
	e.reconcile()
	if it := e.autopilot().Items[0]; it.State != aiv1.AutopilotItemSkipped || it.Hold != nil {
		t.Fatalf("a skipped item is not held: %+v", it)
	}
	e.gl.AddNote(tracking, owner, "/agent unskip #1")
	e.reconcile()
	e.mergeItem(2) // frees a slot for #1
	for range 3 {
		e.reconcile()
	}
	if n := strings.Count(trackingComments(e), "⏸️ #1 is held: "); n != 2 || e.states()[1] != active {
		t.Fatalf("#1 starts again and is driven: %d hold comments, %v", n, e.states())
	}
}

// An active main-red fix moves: every queue item held is no stuck then.
func TestAutopilotHoldNoStuckWhileTheMainRedFixRuns(t *testing.T) {
	for _, fix := range []bool{false, true} {
		gl := gitlabfake.New()
		gl.AddIssue(7, "Tracking")
		ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, TrackingIssueIID: 7,
			Items: []aiv1.AutopilotItem{{IID: 1, State: active, Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "red"}}}}
		if fix {
			ap.MainRedFix = &aiv1.AutopilotItem{IID: 9, State: active}
		}
		a := &autopilot{r: &AgentProjectReconciler{}, gl: gl, spec: aiv1.AgentProjectSpec{}.WithDefaults(), st: &aiv1.AgentTask{}, ap: ap, log: logr.Discard()}
		if err := a.stuckOnHolds(context.Background()); err != nil {
			t.Fatal(err)
		}
		if stuck := ap.Phase == aiv1.AutopilotPaused && ap.PauseKind == aiv1.PauseStuck; stuck == fix {
			t.Fatalf("fix %t: stuck %t: %+v", fix, stuck, ap)
		}
	}
}

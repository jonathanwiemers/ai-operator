package controller

import (
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// laneEnv is a frozen autopilot milestone (#1 blocked at the gate, main red)
// whose main-red issue #99 was handed to the agent.
func laneEnv(t *testing.T, autoFix bool) *projectEnv {
	e := newAutopilotEnv(t)
	e.setMainHealth(aiv1.MainHealthSpec{AutoFix: autoFix})
	e.setNotify(aiv1.NotifySpec{Users: []string{"alice"}})
	e.gl.AddIssue(99, "🚑 `main` is red: `test`", "ai-generated", "ci::main-red", "agent::ready")
	e.reconcile()
	e.reconcile()
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 99, FailedJobs: []string{"test"}})
	e.finishRun(1, "sha-api", nil)
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PausedReason != frozenReason {
		t.Fatalf("expected the freeze: %+v", ap)
	}
	return e
}

// laneTask returns the implement task of an issue, if there is one.
func (e *projectEnv) laneTask(iid int64) *aiv1.AgentTask {
	e.t.Helper()
	for _, task := range e.tasks() {
		if task.Spec.Issue != nil && task.Spec.Issue.IID == iid {
			return &task
		}
	}
	return nil
}

func (e *projectEnv) mergeRequestOf(iid int64) int64 {
	e.t.Helper()
	task := e.laneTask(iid)
	if task == nil {
		e.t.Fatalf("no task for #%d", iid)
	}
	return task.Status.Run.MergeRequestIID
}

func TestAutopilotMergesTheMainRedFixDuringTheFreeze(t *testing.T) {
	e := laneEnv(t, true)
	if task := e.laneTask(99); task == nil || !task.Spec.Autopilot {
		t.Fatalf("the lane's issue runs on autopilot (no questions, CI fixes reviewed): %+v", task)
	}
	e.finishRun(99, "sha-fix", nil)
	e.reconcile() // marked ready
	e.reconcile()
	fix := e.mergeRequestOf(99)
	if !slices.Equal(e.gl.Merged, []int64{fix}) {
		t.Fatalf("during the freeze only the fix !%d is merged, merged %v", fix, e.gl.Merged)
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.MainRedFix == nil || ap.MainRedFix.State != aiv1.AutopilotItemMerged ||
		e.tasks()["shop-1-api"].Status.Blocked == nil || e.gl.Issues[99].State != "closed" {
		t.Fatalf("the queue stays frozen until main is green: %+v", ap)
	}
	e.reconcile()
	if len(e.gl.Merged) != 1 {
		t.Fatalf("one fix per red episode, merged %v", e.gl.Merged)
	}

	// The merge's pipeline is green: the freeze lifts, the queue goes on.
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen})
	e.reconcile()
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{fix, e.mergeRequestOf(1)}) || e.autopilot().MainRedFix != nil {
		t.Fatalf("green after the fix must lift the freeze and merge #1, merged %v, %+v", e.gl.Merged, e.autopilot())
	}
}

func TestAutopilotLeavesTheMainRedIssueWithoutAutoFix(t *testing.T) {
	e := laneEnv(t, false)
	e.finishRun(99, "sha-fix", nil)
	e.reconcile()
	e.reconcile()
	if len(e.gl.Merged) != 0 || e.autopilot().MainRedFix != nil || e.laneTask(99).Spec.Autopilot {
		t.Fatalf("without autoFix a human merges the fix, merged %v", e.gl.Merged)
	}
}

func TestAutopilotLaneHoldsWhileAHumanPaused(t *testing.T) {
	e := laneEnv(t, true)
	e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
	e.reconcile()
	e.finishRun(99, "sha-fix", nil)
	e.reconcile()
	e.reconcile()
	if len(e.gl.Merged) != 0 {
		t.Fatalf("a human pause holds the lane too, merged %v", e.gl.Merged)
	}
}

func TestAutopilotPausesWhenTheMainRedFixIsRed(t *testing.T) {
	e := laneEnv(t, true)
	e.finishRun(99, "sha-fix", nil)
	e.gl.SetLabels(99, "ai-generated", "agent::needs-input") // CI red after the fix rounds
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	c := trackingComments(e)
	if ap.Phase != aiv1.AutopilotPaused || !strings.HasPrefix(ap.PausedReason, "the fix of the red default branch (#99) cannot be merged: #99 needs input") ||
		!strings.Contains(c, "cc @alice") || len(e.gl.Merged) != 0 {
		t.Fatalf("a red fix must pause with a ping: merged %v, %+v\n%s", e.gl.Merged, ap, c)
	}
	if strings.Count(c, "Autopilot paused") != 2 || e.tasks()["shop-1-api"].Status.Blocked == nil {
		t.Fatalf("one pause comment for the freeze, one for the fix, and the freeze holds:\n%s", c)
	}

	// Green by hand: the main-red issue is closed, autopilot continues.
	e.gl.Issues[99].State = "closed"
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen})
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || e.tasks()["shop-1-api"].Status.Blocked != nil {
		t.Fatalf("green must end the pause over the fix and lift the freeze: %+v\n%s", ap, trackingComments(e))
	}
}

func TestAutopilotLaneWaitsWhilePausedForTheFix(t *testing.T) {
	e := laneEnv(t, true)
	e.finishRun(99, "sha-fix", nil)
	fix := e.mergeRequestOf(99)
	e.gl.MRs[fix].Draft, e.gl.MRs[fix].DetailedMergeStatus = false, "not_approved"
	e.reconcile()
	if ap := e.autopilot(); ap.PausedFor != 99 || !strings.Contains(ap.PausedReason, "needs an approval") {
		t.Fatalf("the fix's MR needs an approval: %+v", ap)
	}
	// Approved: still paused until "resume" (as for any MR pause), then merged.
	e.gl.MRs[fix].DetailedMergeStatus = "mergeable"
	e.reconcile()
	if len(e.gl.Merged) != 0 {
		t.Fatalf("a pause over the fix holds its merge, merged %v", e.gl.Merged)
	}
	e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{fix}) || e.autopilot().Phase != aiv1.AutopilotPaused || e.autopilot().PausedFor != 1 {
		t.Fatalf("after resume the fix is merged and the freeze holds again: merged %v, %+v", e.gl.Merged, e.autopilot())
	}
}

func TestAutopilotResumeUnblocksTheFix(t *testing.T) {
	e := laneEnv(t, true)
	e.finishRun(99, "sha-fix", nil)
	task := e.laneTask(99)
	if err := e.r.block(e.ctx, aiv1.WorkflowSpec{}.WithDefaults(), *e.gl.Issues[99], task, aiv1.BlockInfra, "reviewers down"); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
	e.reconcile()
	if b := e.laneTask(99).Status.Blocked; b != nil {
		t.Fatalf("resume lifts the infra block of the fix too: %+v", b)
	}
}

func TestStaleMainHealthTakesTheFixOffAutopilot(t *testing.T) {
	e := laneEnv(t, true)
	stale := metav1.NewTime(e.r.Now().Add(-4 * aiv1.DefaultMainHealthInterval))
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 99, CheckedAt: &stale})
	e.reconcile()
	if e.laneTask(99).Spec.Autopilot {
		t.Fatal("a stale main health drives no lane, so its issue runs in the normal flow")
	}
}

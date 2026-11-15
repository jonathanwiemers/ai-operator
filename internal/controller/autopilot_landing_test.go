package controller

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// newLaneEnv is newParallelEnv with its three roots (#1, #2, #4) running.
func newLaneEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newParallelEnv(t, 0, false)
	e.reconcile()
	e.reconcile() // picked up
	e.wantStates("three roots run", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})
	return e
}

// readyBehind finishes iid's run green with an MR that needs a rebase (main
// moved), a minute after the last one; it returns the task and the MR.
func (e *projectEnv) readyBehind(iid int64, status string) (string, int64) {
	e.t.Helper()
	e.gl.Clock = e.gl.Clock.Add(time.Minute)
	name := e.finishRun(iid, fmt.Sprintf("sha-%d", iid), nil)
	mr := e.tasks()[name].Status.Run.MergeRequestIID
	e.gl.MRs[mr].Draft, e.gl.MRs[mr].DetailedMergeStatus = false, status
	return name, mr
}

// greenRebased plays the CI loop for the rebased head of task name.
func (e *projectEnv) greenRebased(name string, iid int64) {
	e.t.Helper()
	sha := e.tasks()[name].Status.Run.PushedSHA
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.CI = &aiv1.CIStatus{SHA: sha, State: aiv1.CIStateSuccess} })
	e.gl.SetLabels(iid, "ai-generated", "agent::review")
}

func (e *projectEnv) wantLanding(what string, iid int64, rebased ...int64) {
	e.t.Helper()
	if ap := e.autopilot(); ap.Landing != iid || !slices.Equal(e.gl.Rebased, rebased) || ap.Phase != aiv1.AutopilotRunning {
		e.t.Fatalf("%s: want #%d landing and rebases of %v, got #%d and %v (merged %v): %+v", what, iid, rebased, ap.Landing, e.gl.Rebased, e.gl.Merged, ap)
	}
}

func TestAutopilotLandingIsFIFOByReadyTimeAndOnlyTheLandingItemRebases(t *testing.T) {
	e := newLaneEnv(t)
	// The last queue item is ready first, then #2, then #1.
	n4, mr4 := e.readyBehind(4, "need_rebase")
	e.reconcile()
	e.wantLanding("the first ready item lands", 4, mr4)
	n2, mr2 := e.readyBehind(2, "need_rebase")
	e.reconcile() // #4's rebased head goes to CI; #2 waits
	n1, mr1 := e.readyBehind(1, "need_rebase")
	e.reconcile()
	e.wantLanding("ready items wait for the lane", 4, mr4)
	for _, it := range e.autopilot().Items {
		if it.ReadyAt == nil {
			t.Fatalf("#%d is ready: %+v", it.IID, it)
		}
	}
	if e.tasks()[n4].Status.Run.PushedSHA != "sha-4-rebased" || len(e.gl.Merged) != 0 {
		t.Fatalf("the landing item waits for CI on its rebased head: %+v, merged %v", e.tasks()[n4].Status.Run, e.gl.Merged)
	}

	e.greenRebased(n4, 4)
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{mr4}) || e.autopilot().Landing != 0 || e.autopilot().Items[2].ReadyAt != nil {
		t.Fatalf("#4 merges and frees the lane: merged %v, %+v", e.gl.Merged, e.autopilot())
	}
	e.reconcile()
	e.wantLanding("#2 was ready before #1", 2, mr4, mr2)
	e.reconcile()
	e.greenRebased(n2, 2)
	e.reconcile()
	e.reconcile()
	e.wantLanding("then #1", 1, mr4, mr2, mr1)
	e.reconcile()
	e.greenRebased(n1, 1)
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{mr4, mr2, mr1}) || e.autopilot().Landing != 0 {
		t.Fatalf("all land one after another: merged %v, %+v", e.gl.Merged, e.autopilot())
	}
}

func TestAutopilotLandingItemDoesNotCountAgainstTheLimit(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.wantStates("two of three run", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: pending})
	_, mr := e.readyBehind(1, "need_rebase")
	e.reconcile()
	e.wantLanding("#1 lands", 1, mr)
	e.wantStates("a free slot starts #4 while #1 lands", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})
}

func TestAutopilotLandingMergeBaseFreesTheLane(t *testing.T) {
	e := newLaneEnv(t)
	n1, _ := e.readyBehind(1, "conflict")
	_, mr2 := e.readyBehind(2, "need_rebase")
	e.reconcile()
	if !mergeBaseRequested(e, n1, "sha-1") {
		t.Fatalf("#1's conflict goes to the agent: %+v", e.tasks()[n1].Spec)
	}
	e.wantLanding("the next ready item lands at once", 2, mr2)
	if it := e.autopilot().Items[0]; it.IID != 1 || it.ReadyAt != nil {
		t.Fatalf("#1 is not ready any more: %+v", it)
	}
}

func TestAutopilotLandingRedAfterTheRebaseHoldsAndFreesTheLane(t *testing.T) {
	e := newLaneEnv(t)
	n1, mr1 := e.readyBehind(1, "need_rebase")
	_, mr2 := e.readyBehind(2, "need_rebase")
	e.reconcile()
	e.wantLanding("#1 lands", 1, mr1)
	e.reconcile() // the rebased head goes to CI
	e.patchStatus(n1, func(s *aiv1.AgentTaskStatus) {
		s.CI = &aiv1.CIStatus{SHA: "sha-1-rebased", State: aiv1.CIStateFailed, FailedJobs: []string{"test"}}
	})
	e.gl.SetLabels(1, "ai-generated", "agent::review")
	// The hold comment fails: no hold yet, so #1 keeps the lane and #2
	// neither lands nor rebases.
	tracking := e.autopilot().TrackingIssueIID
	e.gl.NoteErrors = map[int64]error{tracking: &gitlab.Error{StatusCode: 500, Method: "POST", Path: "notes"}}
	_, _ = e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}})
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.Landing != 1 || ap.Items[0].Hold != nil || !slices.Equal(e.gl.Rebased, []int64{mr1}) {
		t.Fatalf("an unsaid hold keeps the lane: rebased %v, %+v", e.gl.Rebased, ap)
	}
	e.gl.NoteErrors = nil
	e.reconcile()
	ap := e.autopilot()
	if h := ap.Items[0].Hold; h == nil || h.Kind != aiv1.PauseCIRed || !h.ResumeWhenReady || ap.Items[0].ReadyAt != nil {
		t.Fatalf("a red rebased head holds #1 and takes it out of the lane: %+v", ap.Items[0])
	}
	// The next ready item lands in the same poll.
	e.wantLanding("#2 lands", 2, mr1, mr2)
	if ap.PausedReason != "" {
		t.Fatalf("the milestone is not paused: %+v", ap)
	}
}

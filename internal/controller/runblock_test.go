package controller

import (
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// newRunBlockEnv returns a project env whose issues got a run each: they are
// in working, and their tasks have a pending run request.
func newRunBlockEnv(t *testing.T, iids ...int64) *projectEnv {
	e := newProjectEnv(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	e.r.Now = func() time.Time { return now }
	for _, iid := range iids {
		e.gl.AddIssue(iid, "Add PayPal button", "agent::ready")
	}
	e.reconcile()
	return e
}

// taskOf returns the name of the implement task of issue iid.
func (e *projectEnv) taskOf(iid int64) string {
	e.t.Helper()
	for name, task := range e.tasks() {
		if task.Spec.Issue != nil && task.Spec.Issue.IID == iid {
			return name
		}
	}
	e.t.Fatalf("no implement task for #%d", iid)
	return ""
}

func (e *projectEnv) labelsOf(iid int64) []string {
	l := e.gl.Labels(iid)
	slices.Sort(l)
	return l
}

// deferred plays the runner deferring the issue's pending run at at.
func (e *projectEnv) deferred(iid int64, at time.Time) {
	e.t.Helper()
	e.patchStatus(e.taskOf(iid), func(s *aiv1.AgentTaskStatus) {
		s.Run.ActiveRunRequest = s.Run.ObservedRunRequest
		s.Run.DeferredUntil = &metav1.Time{Time: at.Add(time.Hour)}
		s.Run.LimitedEngine, s.Run.BlockReason = aiv1.EngineClaude, aiv1.BlockQuota
		s.Run.LastResult, s.Run.LastActivityTime = aiv1.RunResultDeferred, &metav1.Time{Time: at}
	})
}

// started plays the runner starting the issue's pending run.
func (e *projectEnv) started(iid int64) {
	e.t.Helper()
	request := e.tasks()[e.taskOf(iid)].Spec.RunRequest
	e.patchStatus(e.taskOf(iid), func(s *aiv1.AgentTaskStatus) { s.Run.ActiveRunRequest = request })
}

// ended plays the runner finishing the issue's run at at, with block as its
// report.
func (e *projectEnv) ended(iid int64, at time.Time, result aiv1.RunResult, block aiv1.BlockReason) {
	e.t.Helper()
	request := e.tasks()[e.taskOf(iid)].Spec.RunRequest
	e.patchStatus(e.taskOf(iid), func(s *aiv1.AgentTaskStatus) {
		s.Run.ActiveRunRequest, s.Run.ObservedRunRequest = request, request
		s.Run.DeferredUntil, s.Run.LimitedEngine, s.Run.Deferrals = nil, "", 0
		s.Run.LastResult, s.Run.BlockReason, s.Run.LastActivityTime = result, block, &metav1.Time{Time: at}
	})
}

func TestUsageLimitBlocksUntilTheRunStartsAgain(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	e.deferred(42, e.r.Now())
	e.reconcile()

	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent-blocked::quota", "agent::blocked"}) {
		t.Fatalf("a deferred run blocks the issue (quota): labels = %v", got)
	}
	if b := e.tasks()[e.taskOf(42)].Status.Blocked; b == nil || b.Reason != aiv1.BlockQuota || b.RestoreLabel != "agent::working" {
		t.Fatalf("blocked status = %+v", b)
	}
	e.reconcile() // still deferred: stays blocked
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent-blocked::quota", "agent::blocked"}) {
		t.Fatalf("a run still deferred stays blocked: labels = %v", got)
	}

	e.started(42)
	e.reconcile()
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent::working"}) {
		t.Fatalf("the run started again: working comes back, labels = %v", got)
	}
	if b := e.tasks()[e.taskOf(42)].Status.Blocked; b != nil {
		t.Fatalf("the block must be lifted: %+v", b)
	}

	// Deferred again by the next limit: blocked again.
	e.deferred(42, e.r.Now().Add(time.Hour))
	e.reconcile()
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent-blocked::quota", "agent::blocked"}) {
		t.Fatalf("a new deferral blocks again: labels = %v", got)
	}
}

func TestUsageLimitGiveUpStaysBlocked(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	e.deferred(42, e.r.Now())
	e.reconcile()
	e.ended(42, e.r.Now().Add(time.Hour), aiv1.RunResultFailed, aiv1.BlockQuota)
	e.reconcile()
	e.reconcile()

	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent-blocked::quota", "agent::blocked"}) {
		t.Fatalf("giving up keeps the quota block: labels = %v", got)
	}

	// Also when the poll saw the last attempt running.
	e = newRunBlockEnv(t, 42)
	e.deferred(42, e.r.Now())
	e.reconcile()
	e.started(42)
	e.reconcile()
	e.ended(42, e.r.Now().Add(time.Hour), aiv1.RunResultFailed, aiv1.BlockQuota)
	e.reconcile()
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent-blocked::quota", "agent::blocked"}) {
		t.Fatalf("giving up blocks with quota: labels = %v", got)
	}
}

func TestProjectWideLimitBlocksEveryDeferredTask(t *testing.T) {
	e := newRunBlockEnv(t, 42, 43)
	e.deferred(42, e.r.Now())
	e.deferred(43, e.r.Now())
	e.reconcile()

	for _, iid := range []int64{42, 43} {
		if got := e.labelsOf(iid); !slices.Equal(got, []string{"agent-blocked::quota", "agent::blocked"}) {
			t.Fatalf("#%d: labels = %v", iid, got)
		}
	}
}

func TestARunBlockLiftedByHandStaysLifted(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	e.deferred(42, e.r.Now())
	e.reconcile()

	e.gl.SetLabels(42, "agent::working")
	e.reconcile()
	e.reconcile()
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent::working"}) {
		t.Fatalf("the same report must not block again: labels = %v", got)
	}
	// A human lifted the block, and the next deferral comes before the
	// poll dropped it: it blocks again.
	e.deferred(42, e.r.Now().Add(time.Hour))
	e.reconcile()
	e.gl.SetLabels(42, "agent::working")
	e.deferred(42, e.r.Now().Add(2*time.Hour))
	e.reconcile()
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent-blocked::quota", "agent::blocked"}) {
		t.Fatalf("a new report blocks again: labels = %v", got)
	}
}

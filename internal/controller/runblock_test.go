package controller

import (
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
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

func TestANewRunLiftsABlockWhoseLabelMoveFailed(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	e.started(42)
	e.ended(42, e.r.Now(), aiv1.RunResultSucceeded, "")
	e.gl.SetLabels(42, "agent::review")
	task := e.tasks()[e.taskOf(42)]
	if err := e.r.block(e.ctx, aiv1.WorkflowSpec{}.WithDefaults(), *e.gl.Issues[42], &task, aiv1.BlockInfra, "test"); err != nil {
		t.Fatal(err)
	}
	e.reconcile()

	// A run request whose label move failed: the issue still shows the block.
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: ns, Name: e.taskOf(42)}, &task); err != nil {
		t.Fatal(err)
	}
	task.Spec.RunRequest++
	if err := e.c.Update(e.ctx, &task); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent::working"}) || e.tasks()[e.taskOf(42)].Status.Blocked != nil {
		t.Fatalf("a pending run lifts the block and shows working: labels = %v", got)
	}
}

// failedInfra plays a run of #42 that failed because its pod was lost.
func failedInfra(e *projectEnv) {
	e.t.Helper()
	e.started(42)
	e.ended(42, e.r.Now(), aiv1.RunResultFailed, aiv1.BlockInfra)
	e.reconcile()
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent-blocked::infra", "agent::blocked"}) {
		e.t.Fatalf("an infra failure blocks the issue: labels = %v", got)
	}
}

func (e *projectEnv) advance(d time.Duration) {
	now := e.r.Now().Add(d)
	e.r.Now = func() time.Time { return now }
}

func TestInfraFailureIsRetriedOnce(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	failedInfra(e)
	request := e.tasks()[e.taskOf(42)].Spec.RunRequest

	e.advance(29 * time.Minute)
	e.reconcile()
	if task := e.tasks()[e.taskOf(42)]; task.Spec.RunRequest != request || task.Status.Blocked == nil {
		t.Fatalf("the retry waits for lifecycle.infraRetryAfter (30m): %+v", task.Spec)
	}

	e.advance(2 * time.Minute)
	e.reconcile()
	task := e.tasks()[e.taskOf(42)]
	if task.Spec.RunRequest != request+1 || task.Spec.Source.InfraRetry != request+1 || task.Status.Blocked != nil {
		t.Fatalf("one new run after infraRetryAfter, block lifted: %+v %+v", task.Spec, task.Status.Blocked)
	}
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent::working"}) {
		t.Fatalf("the retry shows working: labels = %v", got)
	}
	e.reconcile()
	if task := e.tasks()[e.taskOf(42)]; task.Spec.RunRequest != request+1 || task.Status.Blocked != nil {
		t.Fatalf("one retry, and the pending run stays unblocked: %+v %+v", task.Spec, task.Status.Blocked)
	}

	// The retry fails the same way: blocked, and it waits for a human.
	failedInfra(e)
	e.advance(time.Hour)
	e.reconcile()
	e.reconcile()
	if task := e.tasks()[e.taskOf(42)]; task.Spec.RunRequest != request+1 || task.Status.Blocked == nil {
		t.Fatalf("no second retry: %+v %+v", task.Spec, task.Status.Blocked)
	}

	// A human's run request gets its own retry.
	e.gl.AddNote(42, gitlab.User{ID: 1, Username: "jonathan"}, "/agent retry")
	e.reconcile()
	failedInfra(e)
	request = e.tasks()[e.taskOf(42)].Spec.RunRequest
	e.advance(31 * time.Minute)
	e.reconcile()
	if task := e.tasks()[e.taskOf(42)]; task.Spec.RunRequest != request+1 {
		t.Fatalf("a new run request is retried once again: %+v", task.Spec)
	}
}

func TestRealFailureDoesNotBlock(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	e.started(42)
	e.ended(42, e.r.Now(), aiv1.RunResultFailed, "")
	e.gl.SetLabels(42, "agent::needs-input") // the runner's move
	e.reconcile()
	e.advance(time.Hour)
	e.reconcile()

	task := e.tasks()[e.taskOf(42)]
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent::needs-input"}) || task.Status.Blocked != nil || task.RunPending() {
		t.Fatalf("a real failure needs input, no block and no retry: labels = %v, %+v", got, task.Status.Blocked)
	}
}

func TestHumanCommandLiftsAnInfraBlock(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	failedInfra(e)
	request := e.tasks()[e.taskOf(42)].Spec.RunRequest

	e.gl.AddNote(42, gitlab.User{ID: 1, Username: "jonathan"}, "/agent the runner is fixed, try again")
	e.reconcile()
	e.reconcile()
	task := e.tasks()[e.taskOf(42)]
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent::working"}) || task.Status.Blocked != nil || task.Spec.RunRequest != request+1 {
		t.Fatalf("/agent lifts the block and runs: labels = %v, %+v, request %d", got, task.Status.Blocked, task.Spec.RunRequest)
	}
}

func TestAutopilotResumeKeepsARunsInfraBlock(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.taskOf(1)
	task := e.tasks()[name]
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.Run.ActiveRunRequest, s.Run.ObservedRunRequest = task.Spec.RunRequest, task.Spec.RunRequest
		s.Run.LastResult, s.Run.BlockReason, s.Run.LastActivityTime = aiv1.RunResultFailed, aiv1.BlockInfra, &metav1.Time{Time: e.gl.Clock}
	})
	e.reconcile()
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddNote(e.autopilot().TrackingIssueIID, human, "/agent pause")
	e.reconcile()
	e.gl.AddNote(e.autopilot().TrackingIssueIID, human, "/agent resume")
	e.reconcile()

	if got := e.gl.Labels(1); !slices.Contains(got, "agent-blocked::infra") || e.tasks()[name].Status.Blocked == nil {
		t.Fatalf("only a new run lifts a block its failed run asked for: labels = %v", got)
	}
	e.gl.Clock = e.gl.Clock.Add(31 * time.Minute)
	e.reconcile()
	if task := e.tasks()[name]; !task.RunPending() || task.Status.Blocked != nil {
		t.Fatalf("the automatic retry still comes: %+v %+v", task.Spec, task.Status.Blocked)
	}
}

func TestAnInfraRetryWhoseLabelMoveFailedStillLifts(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	failedInfra(e)

	// The retry was requested, but moving the labels failed.
	var task aiv1.AgentTask
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: ns, Name: e.taskOf(42)}, &task); err != nil {
		t.Fatal(err)
	}
	task.Spec.RunRequest++
	task.Spec.Source.InfraRetry = task.Spec.RunRequest
	if err := e.c.Update(e.ctx, &task); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Hour)
	e.reconcile()
	got := e.tasks()[e.taskOf(42)]
	if labels := e.labelsOf(42); !slices.Equal(labels, []string{"agent::working"}) || got.Status.Blocked != nil || got.Spec.RunRequest != task.Spec.RunRequest {
		t.Fatalf("the pending retry lifts the block, and no second one: labels = %v, %+v", labels, got.Spec)
	}
}

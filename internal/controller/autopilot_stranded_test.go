package controller

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// strandRun plays a run of #1 that failed while GitLab was rate-limited: the
// runner recorded the failure, but its label move was lost, so the issue is
// still on agent::working. A GitLab failure asks for an infra block, as the
// runner does.
func (e *projectEnv) strandRun(kind aiv1.FailureKind) string {
	e.t.Helper()
	name := e.taskOf(1)
	task := e.tasks()[name]
	ended := metav1.NewTime(e.gl.Clock)
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest, s.Run.ActiveRunRequest = task.Spec.RunRequest, task.Spec.RunRequest
		s.Run.LastResult, s.Run.Summary, s.Run.FailureKind = aiv1.RunResultFailed, "listing issue notes: 429 Rate limit exceeded", kind
		s.Run.NeedsInputCause, s.Run.LastActivityTime = aiv1.NeedsInputRunFailed, &ended
		s.Run.BlockReason = ""
		if kind == aiv1.FailureGitLab {
			s.Run.BlockReason = aiv1.BlockInfra
		}
	})
	e.gl.SetLabels(1, "ai-generated", "agent::working")
	return name
}

// A run that failed on GitLab is blocked and retried once by syncRunBlock,
// also on autopilot with its issue left on agent::working; no pause is posted.
func TestAutopilotRetriesAGitLabFailureOnWorkingOnce(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.strandRun(aiv1.FailureGitLab)
	before := e.tasks()[name].Spec.RunRequest

	e.reconcile()
	if task := e.tasks()[name]; task.Status.Blocked == nil || task.Status.Blocked.Reason != aiv1.BlockInfra || task.Spec.RunRequest != before {
		t.Fatalf("a GitLab failure is blocked (infra) first, with no run yet: %+v, run request %d", task.Status.Blocked, task.Spec.RunRequest)
	}
	e.gl.Clock = e.gl.Clock.Add(aiv1.DefaultInfraRetryAfter - time.Minute)
	e.reconcile()
	if got := e.tasks()[name].Spec.RunRequest; got != before {
		t.Fatalf("before lifecycle.infraRetryAfter it waits: run request %d", got)
	}
	e.gl.Clock = e.gl.Clock.Add(time.Minute)
	e.reconcile()
	task := e.tasks()[name]
	if task.Spec.RunRequest != before+1 || task.Spec.Source.InfraRetry != task.Spec.RunRequest {
		t.Fatalf("after lifecycle.infraRetryAfter one run is requested: %d → %d, infraRetry %d", before, task.Spec.RunRequest, task.Spec.Source.InfraRetry)
	}
	e.reconcile()
	if got := e.tasks()[name].Spec.RunRequest; got != before+1 {
		t.Fatalf("a second poll raises no other run: run request %d", got)
	}
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotPaused || strings.Contains(trackingComments(e), "Autopilot paused") {
		t.Fatalf("the automatic retry posts no pause: %+v", ap)
	}

	// The retried run fails on GitLab again: the retry is used, no other run.
	e.strandRun(aiv1.FailureGitLab)
	e.gl.Clock = e.gl.Clock.Add(2 * aiv1.DefaultInfraRetryAfter)
	e.reconcile()
	e.reconcile()
	if got := e.tasks()[name].Spec.RunRequest; got != before+1 {
		t.Fatalf("after the used retry no other run is requested: run request %d", got)
	}
}

func TestAutopilotPausesAStrandedFailureAndMovesTheLabel(t *testing.T) {
	defer gitlab.SetRetryBackoff(time.Millisecond)()
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.strandRun("")
	before := e.tasks()[name].Spec.RunRequest
	e.gl.UpdateErrors = map[int64]error{1: errLimited()}
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseRunFailed || ap.PausedFor != 1 ||
		!strings.Contains(ap.PausedReason, "the run failed: listing issue notes: 429 Rate limit exceeded") {
		t.Fatalf("a stranded failure pauses runFailed with its summary: %+v", ap)
	}
	if got := e.tasks()[name].Spec.RunRequest; got != before {
		t.Fatalf("another cause raises no run: run request %d", got)
	}
	// The label move failed on this poll; the next poll moves it.
	delete(e.gl.UpdateErrors, 1)
	e.reconcile()
	labels := e.gl.Issues[1].Labels
	if !slices.Contains(labels, "agent::needs-input") || slices.Contains(labels, "agent::working") {
		t.Fatalf("the stranded issue moves to needs-input: %v", labels)
	}
	if ap := e.autopilot(); ap.PauseKind != aiv1.PauseRunFailed || strings.Count(trackingComments(e), "Autopilot paused") != 1 {
		t.Fatalf("the pause stays, announced once: %+v\n%s", ap, trackingComments(e))
	}
}

func TestAutopilotWaitsForARunningTaskOnWorking(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.strandRun("")
	// A new run is in progress: the last run's failure is history.
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.Run.ActiveRunRequest = s.Run.ObservedRunRequest + 1 })
	e.reconcile()
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotPaused {
		t.Fatalf("a running task on working waits: %+v", ap)
	}
}

func errLimited() error {
	return &gitlab.Error{StatusCode: http.StatusTooManyRequests, Method: "PUT", Path: "/projects/g%2Fp/issues/1"}
}

func TestStrandedRunSkipsAFailureAwaitingItsBlock(t *testing.T) {
	task := &aiv1.AgentTask{}
	task.Status.Run = aiv1.RunStatus{LastResult: aiv1.RunResultFailed, NeedsInputCause: aiv1.NeedsInputRunFailed}
	if !strandedRun(task) {
		t.Fatal("an idle failed run without a block is stranded")
	}
	// The runner asked for an infra block; syncRunBlock has not applied it yet.
	task.Status.Run.BlockReason = aiv1.BlockInfra
	if strandedRun(task) {
		t.Fatal("a failure awaiting its block is not stranded")
	}
	a := &autopilot{spec: aiv1.AgentProjectSpec{}.WithDefaults()}
	is := gitlab.Issue{IID: 1, Labels: []string{"agent::working"}}
	if g := a.gateCheck(&aiv1.AutopilotItem{IID: 1}, is, task); g.stranded || g.state != gateWaiting {
		t.Fatalf("the gate waits for the block: %+v", g)
	}
}

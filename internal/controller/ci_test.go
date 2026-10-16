package controller

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const ciTask = "shop-42-add-paypal-button"

// newCIEnv returns a project env with an issue in "working" whose task has
// just pushed sha.
func newCIEnv(t *testing.T, sha string) *projectEnv {
	e := newProjectEnv(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	e.r.Now = func() time.Time { return now }
	e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	e.reconcile() // creates the task, no run requested
	e.agentPushed(sha, 0, 0)
	return e
}

// agentPushed simulates the runner finishing a run that pushed sha.
func (e *projectEnv) agentPushed(sha string, round int32, fixedPipeline int64) {
	e.t.Helper()
	var task aiv1.AgentTask
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: ciTask}, &task); err != nil {
		e.t.Fatal(err)
	}
	orig := task.DeepCopy()
	run := &task.Status.Run
	run.ObservedRunRequest = task.Spec.RunRequest
	run.ActiveRunRequest = task.Spec.RunRequest
	run.PushedSHA = sha
	run.PushedAt = &metav1.Time{Time: e.r.Now()}
	run.CIRound = round
	if fixedPipeline != 0 {
		run.CIFixedPipelineID = fixedPipeline
	}
	if err := e.c.Status().Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *projectEnv) ciTask() aiv1.AgentTask {
	e.t.Helper()
	return e.tasks()[ciTask]
}

func (e *projectEnv) ciState() aiv1.CIState {
	e.t.Helper()
	if ci := e.ciTask().Status.CI; ci != nil {
		return ci.State
	}
	return ""
}

func (e *projectEnv) notes() string {
	var b strings.Builder
	for _, n := range e.gl.IssueNotes[42] {
		b.WriteString(n.Body + "\n")
	}
	return b.String()
}

func failedTest() gitlab.Job { return gitlab.Job{Name: "test", Stage: "test", Status: "failed"} }

func TestCIGreenMovesToReview(t *testing.T) {
	e := newCIEnv(t, "sha1")
	pid := e.gl.AddPipeline("sha1", "agent/42", "running")
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStateRunning {
		t.Fatalf("state %s", got)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("issue must stay in working while CI runs, has %v", labels)
	}
	e.gl.SetPipelineStatus(pid, "success")
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStateSuccess {
		t.Fatalf("state %s", got)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("green pipeline should move the issue to review, has %v", labels)
	}
	if e.notes() != "" {
		t.Fatalf("a green pipeline needs no comment, got %q", e.notes())
	}
	// Final states are not touched again.
	e.gl.SetLabels(42, "agent::working")
	e.reconcile()
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("a finished CI loop must not move the issue again, has %v", labels)
	}
}

func TestCINoPipeline(t *testing.T) {
	e := newCIEnv(t, "sha1")
	start := e.r.Now()
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStatePending {
		t.Fatalf("state %s", got)
	}
	e.r.Now = func() time.Time { return start.Add(11 * time.Minute) }
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStateNone {
		t.Fatalf("state %s", got)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("no CI: review after the timeout, has %v", labels)
	}
}

func TestCIRetryThenFixRoundsThenGiveUp(t *testing.T) {
	e := newCIEnv(t, "sha1")
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest(), gitlab.Job{Name: "lint", Status: "failed", AllowFailure: true})

	// 1. Red: the failed jobs are retried once, no agent run yet.
	e.reconcile()
	if !slices.Equal(e.gl.Retried, []int64{pid}) || e.ciState() != aiv1.CIStateRunning || e.ciTask().Spec.RunRequest != 0 {
		t.Fatalf("expected a retry first: retried=%v state=%s", e.gl.Retried, e.ciState())
	}

	// 2. Still red: the agent is asked to fix it.
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile()
	task := e.ciTask()
	if task.Spec.RunRequest != 1 || task.Spec.Source.CIFixPipelineID != pid || e.ciState() != aiv1.CIStateFixing {
		t.Fatalf("expected a CI fix run request: %+v state=%s", task.Spec, e.ciState())
	}
	if ci := task.Status.CI; !slices.Equal(ci.FailedJobs, []string{"test"}) {
		t.Fatalf("allowed-to-fail jobs are not counted: %v", ci.FailedJobs)
	}
	// While the fix run is pending nothing else happens.
	e.reconcile()
	if e.ciTask().Spec.RunRequest != 1 {
		t.Fatal("no second request while the first is pending")
	}

	// 3. Fix round 1 pushed sha2, still red after retry -> round 2.
	e.agentPushed("sha2", 1, pid)
	pid2 := e.gl.AddPipeline("sha2", "agent/42", "failed", failedTest())
	e.reconcile() // retry
	e.gl.SetPipelineStatus(pid2, "failed")
	e.reconcile()
	if task := e.ciTask(); task.Spec.RunRequest != 2 || task.Spec.Source.CIFixPipelineID != pid2 {
		t.Fatalf("expected the second fix round: %+v", task.Spec)
	}

	// 4. Round 2 pushed sha3, still red -> rounds used up: needs-input.
	e.agentPushed("sha3", 2, pid2)
	pid3 := e.gl.AddPipeline("sha3", "agent/42", "failed", failedTest())
	e.reconcile()
	e.gl.SetPipelineStatus(pid3, "failed")
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStateFailed {
		t.Fatalf("state %s", got)
	}
	if e.ciTask().Spec.RunRequest != 2 {
		t.Fatal("no third fix round")
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::needs-input"}) {
		t.Fatalf("expected needs-input, has %v", labels)
	}
	if n := e.notes(); !strings.Contains(n, "still red after 2 fix attempts") || !strings.Contains(n, "`test`") {
		t.Fatalf("unexpected note %q", n)
	}
}

func TestCIFixWithoutNewCommitGivesUp(t *testing.T) {
	e := newCIEnv(t, "sha1")
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest())
	e.reconcile() // retry
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile() // fix requested
	if e.ciState() != aiv1.CIStateFixing {
		t.Fatalf("state %s", e.ciState())
	}
	// The fix run finishes without pushing anything new.
	e.agentPushed("sha1", 0, pid)
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStateFailed {
		t.Fatalf("state %s", got)
	}
	if !strings.Contains(e.notes(), "could not fix") {
		t.Fatalf("unexpected note %q", e.notes())
	}
}

func TestCIBaseFailing(t *testing.T) {
	e := newCIEnv(t, "sha1")
	e.gl.AddPipeline("base", "main", "failed", failedTest())
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest())
	e.reconcile() // retry
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStateBaseFailing {
		t.Fatalf("state %s", got)
	}
	if e.ciTask().Spec.RunRequest != 0 {
		t.Fatal("the agent must not be asked to fix a failure that is on the default branch too")
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("expected review, has %v", labels)
	}
	if !strings.Contains(e.notes(), "also fail on the default branch") {
		t.Fatalf("unexpected note %q", e.notes())
	}
}

func TestCICanceled(t *testing.T) {
	e := newCIEnv(t, "sha1")
	e.gl.AddPipeline("sha1", "agent/42", "canceled")
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStateCanceled {
		t.Fatalf("state %s", got)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) || !strings.Contains(e.notes(), "canceled") {
		t.Fatalf("expected review with a note, labels %v notes %q", labels, e.notes())
	}
}

func TestCIRetryDisabled(t *testing.T) {
	e := newCIEnv(t, "sha1")
	var project aiv1.AgentProject
	_ = e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project)
	off := false
	project.Spec.CI.RetryFailedJobs = &off
	if err := e.c.Update(e.ctx, &project); err != nil {
		t.Fatal(err)
	}
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest())
	e.reconcile()
	if len(e.gl.Retried) != 0 || e.ciTask().Spec.Source.CIFixPipelineID != pid {
		t.Fatalf("without retries the agent is asked right away: retried=%v", e.gl.Retried)
	}
}

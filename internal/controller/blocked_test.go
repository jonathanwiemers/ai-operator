package controller

import (
	"slices"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// newBlockedEnv returns a project env with issue #42 in review and its task.
func newBlockedEnv(t *testing.T) (*projectEnv, aiv1.AgentProjectSpec) {
	e := newProjectEnv(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	e.r.Now = func() time.Time { return now }
	e.gl.AddIssue(42, "Add PayPal button", "agent::review")
	e.reconcile()
	return e, testProject().Spec.WithDefaults()
}

func (e *projectEnv) block(reason aiv1.BlockReason) {
	e.t.Helper()
	task := e.ciTask()
	if err := e.r.block(e.ctx, aiv1.WorkflowSpec{}.WithDefaults(), *e.gl.Issues[42], &task, reason, "test"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *projectEnv) labels() []string {
	l := slices.Clone(e.gl.Issues[42].Labels)
	slices.Sort(l)
	return l
}

func TestBlockReplacesTheLabelAndUnblockRestoresIt(t *testing.T) {
	e, spec := newBlockedEnv(t)
	e.block(aiv1.BlockInfra)
	e.reconcile()

	if got := e.labels(); !slices.Equal(got, []string{"agent-blocked::infra", "agent::blocked"}) {
		t.Fatalf("labels = %v", got)
	}
	b := e.ciTask().Status.Blocked
	if b == nil || !b.Applied || b.RestoreLabel != "agent::review" || b.Reason != aiv1.BlockInfra {
		t.Fatalf("blocked status = %+v", b)
	}
	e.reconcile() // stays blocked, the task is kept
	if b := e.ciTask().Status.Blocked; b == nil || !b.Applied {
		t.Fatalf("a blocked issue must stay blocked: %+v", b)
	}

	task := e.ciTask()
	if err := e.r.unblock(e.ctx, e.gl, spec, *e.gl.Issues[42], &task); err != nil {
		t.Fatal(err)
	}
	if got := e.labels(); !slices.Equal(got, []string{"agent::review"}) {
		t.Fatalf("labels after unblock = %v", got)
	}
	if e.ciTask().Status.Blocked != nil {
		t.Fatal("unblock must clear the status")
	}
}

func TestHumanLiftsABlock(t *testing.T) {
	e, _ := newBlockedEnv(t)
	e.block(aiv1.BlockInfra)
	e.reconcile()
	before := e.ciTask().Spec.RunRequest

	e.gl.SetLabels(42, "agent::ready", "agent-blocked::infra")
	e.reconcile() // the ready issue gets a run
	e.reconcile() // and the block follows the missing label

	if got := e.labels(); !slices.Equal(got, []string{"agent::working"}) {
		t.Fatalf("labels = %v", got)
	}
	task := e.ciTask()
	if task.Status.Blocked != nil || task.Spec.RunRequest != before+1 {
		t.Fatalf("block should be lifted and one run requested: %+v, runRequest %d → %d", task.Status.Blocked, before, task.Spec.RunRequest)
	}
}

func TestANewBlockReasonReplacesTheOld(t *testing.T) {
	e, _ := newBlockedEnv(t)
	e.block(aiv1.BlockInfra)
	e.reconcile()
	e.block(aiv1.BlockMainRed)
	e.reconcile()

	if got := e.labels(); !slices.Equal(got, []string{"agent-blocked::main-red", "agent::blocked"}) {
		t.Fatalf("labels = %v", got)
	}
	if b := e.ciTask().Status.Blocked; b.RestoreLabel != "agent::review" {
		t.Fatalf("the label to restore must survive a new reason: %+v", b)
	}
}

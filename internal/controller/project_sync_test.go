package controller

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// issuePhases builds the poll a sync builds before its per-issue phases:
// tasks, milestones and the labelled issues.
func (e *projectEnv) issuePhases() issuePhases {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	poll, err := e.r.newProjectPoll(e.ctx, e.gl, &p, p.Spec.WithDefaults(), e.r.now())
	if err != nil {
		e.t.Fatal(err)
	}
	if err := poll.syncMilestones(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	if err := poll.listIssues(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	return issuePhases{poll}
}

func TestImplementPhaseCreatesTheTask(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::ready")
	ph := e.issuePhases()

	task, _, err := ph.implement(e.ctx, ph.issues[42])
	if err != nil {
		t.Fatal(err)
	}
	const name = "shop-42-add-paypal-button"
	if task.Name != name || !ph.keep[name] || ph.implTasks[42] != task {
		t.Fatalf("the task must be kept and recorded: %s keep=%v implTasks=%v", task.Name, ph.keep, ph.implTasks)
	}
	if _, ok := ph.homeMRs[name]; !ok {
		t.Fatalf("the home merge request (none yet) must be recorded: %v", ph.homeMRs)
	}
	if _, ok := e.tasks()[name]; !ok {
		t.Fatalf("expected the task to be created, got %v", keys(e.tasks()))
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("the issue should be moved to working, has %v", labels)
	}
}

func TestImplementPhaseKeepsTheTaskOfAFailingIssue(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	e.reconcile()
	const name = "shop-42-add-paypal-button"
	e.gl.ListNoteErrors = map[int64]error{42: &gitlab.Error{StatusCode: 404, Method: "GET", Path: "notes"}}
	e.gl.SetLabels(42, "agent::working") // the notes are read again
	ph := e.issuePhases()

	if _, _, err := ph.implement(e.ctx, ph.issues[42]); err == nil {
		t.Fatal("listing the notes fails: the phase must fail")
	}
	if !ph.keep[name] || ph.implTasks[42] == nil || ph.implTasks[42].Name != name {
		t.Fatalf("the existing task must be kept: keep=%v implTasks=%v", ph.keep, ph.implTasks)
	}
	if _, ok := ph.homeMRs[name]; ok {
		t.Fatal("a failed issue has no home merge request")
	}
}

func TestRunBlockPhaseBlocksADeferredRun(t *testing.T) {
	e := newRunBlockEnv(t, 42)
	e.deferred(42, e.r.Now())
	ph := e.issuePhases()

	if err := ph.runBlock(e.ctx, ph.issues[42], ph.byIssue[42]); err != nil {
		t.Fatal(err)
	}
	// The block is recorded; the blocked phase puts its labels on.
	task := e.tasks()[e.taskOf(42)]
	if b := task.Status.Blocked; b == nil || b.Reason != aiv1.BlockQuota || b.Applied || b.RestoreLabel != "agent::working" {
		t.Fatalf("a deferred run blocks the issue (quota): blocked status = %+v", b)
	}
	if task.Status.RunBlockedAt == nil {
		t.Fatal("the run's report must be marked as blocked once")
	}
	if got := e.labelsOf(42); !slices.Equal(got, []string{"agent::working"}) {
		t.Fatalf("the labels wait for the blocked phase: labels = %v", got)
	}
}

func TestBlockedPhaseAppliesTheBlock(t *testing.T) {
	e, _ := newBlockedEnv(t)
	e.block(aiv1.BlockInfra)
	ph := e.issuePhases()

	if err := ph.blocked(e.ctx, ph.issues[42], ph.byIssue[42]); err != nil {
		t.Fatal(err)
	}
	if got := e.labels(); !slices.Equal(got, []string{"agent-blocked::infra", "agent::blocked"}) {
		t.Fatalf("labels = %v", got)
	}
	if b := e.ciTask().Status.Blocked; b == nil || !b.Applied || b.RestoreLabel != "agent::review" {
		t.Fatalf("blocked status = %+v", b)
	}
}

func TestCIPhaseRecordsARedPipeline(t *testing.T) {
	e := newCIEnv(t, "sha1")
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest())
	mr := e.ciMR("sha1")
	mr.HeadPipeline = &gitlab.Pipeline{ID: pid, SHA: "sha1", Status: "failed"}
	ph := e.issuePhases()

	if err := ph.ci(e.ctx, ph.issues[42], ph.byIssue[42], mr); err != nil {
		t.Fatal(err)
	}
	ci := e.ciTask().Status.CI
	if ci == nil || ci.PipelineID != pid || ci.State != aiv1.CIStateRunning || !slices.Equal(e.gl.Retried, []int64{pid}) {
		t.Fatalf("the red pipeline must be recorded and retried: %+v retried=%v", ci, e.gl.Retried)
	}
}

func TestCIPhaseSkipsABlockedTask(t *testing.T) {
	e := newCIEnv(t, "sha1")
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest())
	mr := e.ciMR("sha1")
	mr.HeadPipeline = &gitlab.Pipeline{ID: pid, SHA: "sha1", Status: "failed"}
	e.block(aiv1.BlockMainRed)
	ph := e.issuePhases()

	if err := ph.ci(e.ctx, ph.issues[42], ph.byIssue[42], mr); err != nil {
		t.Fatal(err)
	}
	if ci := e.ciTask().Status.CI; ci != nil || len(e.gl.Retried) != 0 {
		t.Fatalf("a blocked task's CI is not followed: %+v retried=%v", ci, e.gl.Retried)
	}
}

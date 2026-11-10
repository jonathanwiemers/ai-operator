package controller

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// projectPoll builds the poll a sync builds before its phases: tasks, milestones
// and the labelled issues.
func (e *projectEnv) projectPoll() *projectPoll {
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
	return poll
}

// issuePhases is the poll before its per-issue phases.
func (e *projectEnv) issuePhases() issuePhases {
	e.t.Helper()
	return issuePhases{e.projectPoll()}
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

func TestMainHealthPhaseRecordsARedDefaultBranch(t *testing.T) {
	e, _ := newMainEnv(t)
	e.gl.RetryError = &gitlab.Error{StatusCode: 403, Method: "POST", Path: "retry"}
	e.gl.AddPipeline("sha1", "main", "failed", coverageJob())
	poll := e.projectPoll()

	mainHealthPhase{poll}.run(e.ctx)
	if len(poll.errs) != 0 {
		t.Fatal(poll.errs)
	}
	if mh := poll.project.Status.MainHealth; mh == nil || mh.State != aiv1.MainHealthRed || !slices.Equal(mh.FailedJobs, []string{"test:coverage"}) {
		t.Fatalf("a red default branch must be recorded in the project status: %+v", mh)
	}
}

func TestAutopilotPhaseAdvancesTheQueue(t *testing.T) {
	e := newAutopilotEnv(t)
	poll := e.projectPoll()
	issuePhases{poll}.run(e.ctx)

	autopilotPhase{poll}.run(e.ctx)
	if len(poll.errs) != 0 {
		t.Fatal(poll.errs)
	}
	ap := e.autopilot()
	if ap.TrackingIssueIID == 0 || len(ap.Items) == 0 {
		t.Fatalf("the autopilot must fill its queue and open its tracking issue: %+v", ap)
	}
	if !slices.Equal(e.gl.Labels(1), []string{"ai-generated", "agent::ready"}) || !e.gl.Issues[2].HasLabel("agent::draft") {
		t.Fatalf("#1 goes first: #1 %v, #2 %v", e.gl.Labels(1), e.gl.Labels(2))
	}
}

func TestAutopilotPhaseCollectsAMilestonesError(t *testing.T) {
	e := newAutopilotEnv(t)
	// A second autopilot milestone.
	m := gitlab.Milestone{ID: 12, IID: 4, Title: "Search", State: "active", Description: "/agent autopilot\nSearch the shop."}
	e.gl.Milestones = append(e.gl.Milestones, m)
	is := e.gl.AddIssue(5, "Index", "ai-generated", "agent::draft")
	is.Milestone = &m
	is.Description = "Index it.\n<!-- ai-operator:key=index -->"
	poll := e.projectPoll()
	issuePhases{poll}.run(e.ctx)
	if len(poll.autopilots) != 2 {
		t.Fatalf("expected two autopilot milestones, got %v", poll.autopilots)
	}
	// The autopilot that runs first fails to list its milestone's issues.
	gl := &failFirstAutopilot{API: e.gl}
	poll.gl = gl

	autopilotPhase{poll}.run(e.ctx)
	if len(gl.listed) != 2 || gl.listed[1] == gl.failed {
		t.Fatalf("the autopilot after the failing one must still run, listed %v", gl.listed)
	}
	if len(poll.errs) != 1 || !strings.Contains(poll.errs[0].Error(), `autopilot of milestone "`+gl.failed+`"`) {
		t.Fatalf("the failing milestone's error must be collected: %v", poll.errs)
	}
}

// failFirstAutopilot fails the milestone issue list of the first autopilot
// that reads it.
type failFirstAutopilot struct {
	gitlab.API
	failed string
	listed []string
}

func (f *failFirstAutopilot) ListIssues(ctx context.Context, project string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	if opts.Milestone != "" && opts.State == "all" { // the autopilot's
		f.listed = append(f.listed, opts.Milestone)
		if f.failed == "" || f.failed == opts.Milestone {
			f.failed = opts.Milestone
			return nil, errors.New("refused")
		}
	}
	return f.API.ListIssues(ctx, project, opts)
}

func TestPilotPhaseClosesResolvedPromptsFirst(t *testing.T) {
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 1))
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 9)) // gone from GitLab
	poll := e.projectPoll()
	issuePhases{poll}.run(e.ctx)

	pilotPhase{poll}.run(e.ctx)
	if len(poll.errs) != 0 {
		t.Fatal(poll.errs)
	}
	// The spec task the pilots were handed has the prompt closed already.
	if got := promptStates(poll.byMilestone[3]); got != "Q1=open Q2=superseded" {
		t.Fatalf("the pilots must see the resolved prompt closed: %s", got)
	}
	if got := promptStates(e.task(specTaskName)); got != "Q1=open Q2=superseded" {
		t.Fatalf("the closed prompt must be stored: %s", got)
	}
}

func TestCleanupPhaseDeletesOnlyTasksWhoseIssueIsGone(t *testing.T) {
	e := newAutopilotEnv(t)
	e.gl.AddIssue(7, "Docs", "agent::working")
	e.gl.AddIssue(8, "Logs", "agent::working")
	e.reconcile()
	const docs, logs = "shop-7-docs", "shop-8-logs"
	tracking := e.autopilot().TrackingIssueIID
	if _, ok := e.tasks()[logs]; !ok || tracking == 0 {
		t.Fatalf("expected tasks for #7 and #8 and a tracking issue, got %v, tracking %d", keys(e.tasks()), tracking)
	}
	// #7 is closed, listing #8's notes fails and the milestone is no
	// longer planned.
	e.gl.Issues[7].State = "closed"
	e.gl.ListNoteErrors = map[int64]error{8: &gitlab.Error{StatusCode: 404, Method: "GET", Path: "notes"}}
	e.gl.SetLabels(8, "agent::working") // the notes are read again
	e.gl.Milestones[0].Description = "Build a new checkout."
	poll := e.projectPoll()
	issuePhases{poll}.run(e.ctx)
	if len(poll.errs) != 1 {
		t.Fatalf("expected #8's error, got %v", poll.errs)
	}

	if err := (cleanupPhase{poll}).run(e.ctx); err != nil {
		t.Fatal(err)
	}
	if len(poll.errs) != 1 {
		t.Fatalf("the cleanup adds no error: %v", poll.errs)
	}
	tasks := e.tasks()
	if _, ok := tasks[docs]; ok {
		t.Fatalf("#7's task must be deleted, got %v", keys(tasks))
	}
	if _, ok := tasks[logs]; !ok {
		t.Fatalf("the failing #8 keeps its task, got %v", keys(tasks))
	}
	if _, ok := tasks[specTaskName]; ok {
		t.Fatal("the unplanned milestone's spec task must be deleted")
	}
	notes := e.gl.IssueNotes[tracking]
	if e.gl.Issues[tracking].State != "closed" || len(notes) == 0 || !strings.Contains(notes[len(notes)-1].Body, "Autopilot is off") {
		t.Fatalf("the autopilot must be switched off before its task goes: %+v", e.gl.Issues[tracking])
	}
	if poll.project.Status.ActiveTasks != int32(len(poll.keep)) {
		t.Fatalf("ActiveTasks = %d, keep %v", poll.project.Status.ActiveTasks, poll.keep)
	}
}

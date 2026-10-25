package controller

import (
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// redMain makes main red after its retry and returns the main-red issue.
func redMain(e *projectEnv, advance func(time.Duration)) *gitlab.Issue {
	e.t.Helper()
	pid := e.gl.AddPipeline("sha1", "main", "failed", coverageJob())
	e.reconcile()
	e.gl.SetPipelineStatus(pid, "failed")
	e.check(advance)
	red := e.issuesWith("ci::main-red")
	if len(red) != 1 {
		e.t.Fatalf("expected one main-red issue, got %d", len(red))
	}
	return red[0]
}

// taskOf returns the implement task of an issue, if there is one.
func (e *projectEnv) taskOf(iid int64) *aiv1.AgentTask {
	e.t.Helper()
	for _, task := range e.tasks() {
		if task.Spec.Issue != nil && task.Spec.Issue.IID == iid {
			return &task
		}
	}
	return nil
}

func (e *projectEnv) setTitlePattern(pattern string) {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	p.Spec.Milestones.TitlePattern = pattern
	if err := e.c.Update(e.ctx, &p); err != nil {
		e.t.Fatal(err)
	}
}

func TestMainHealthAutoFixOff(t *testing.T) {
	e, advance := newMainEnv(t)
	is := redMain(e, advance)
	e.check(advance)
	if is.HasLabel("agent::ready") || e.taskOf(is.IID) != nil || e.mainHealth().AutoFixIssueIID != 0 {
		t.Fatalf("without autoFix the main-red issue stays with humans: labels %v", is.Labels)
	}
}

func TestMainHealthAutoFix(t *testing.T) {
	for name, pattern := range map[string]string{"all issues": "", "milestone filter": "^Sprint "} {
		t.Run(name, func(t *testing.T) {
			e, advance := newMainEnv(t)
			e.setTitlePattern(pattern)
			e.setMainHealth(aiv1.MainHealthSpec{AutoFix: true})
			is := redMain(e, advance)
			if !is.HasLabel("agent::ready") || e.mainHealth().AutoFixIssueIID != is.IID {
				t.Fatalf("autoFix must hand the main-red issue to the agent: labels %v, status %+v", is.Labels, e.mainHealth())
			}
			for _, want := range []string{"Make `main` green again", "do not delete it", marker.UntrustedData} {
				if !strings.Contains(is.Description, want) {
					t.Errorf("description lacks %q:\n%s", want, is.Description)
				}
			}
			if task, logs := strings.Index(is.Description, "Make `main` green"), strings.Index(is.Description, marker.UntrustedData); task > logs {
				t.Fatalf("the task must come before the job logs:\n%s", is.Description)
			}
			e.reconcile()
			task := e.taskOf(is.IID)
			if task == nil || task.Spec.RunRequest != 1 || !is.HasLabel("agent::working") {
				t.Fatalf("the main-red issue must get a task and a run, also without a milestone: %+v, labels %v", task, is.Labels)
			}

			// The agent asked a question; other jobs fail: no second hand-over.
			e.gl.SetLabels(is.IID, "ai-generated", "ci::main-red", "agent::needs-input")
			e.setMainHealth(aiv1.MainHealthSpec{AutoFix: true, RetryFailedJobs: new(false)})
			e.gl.AddPipeline("sha2", "main", "failed", gitlab.Job{Name: "build", Status: "failed"})
			e.check(advance)
			if is.HasLabel("agent::ready") || len(e.issuesWith("ci::main-red")) != 1 {
				t.Fatalf("one hand-over per red episode: labels %v", is.Labels)
			}
		})
	}
}

func TestMainHealthAutoFixSwitchedOnWhileRed(t *testing.T) {
	e, advance := newMainEnv(t)
	is := redMain(e, advance)
	e.setMainHealth(aiv1.MainHealthSpec{AutoFix: true})
	e.check(advance)
	if !is.HasLabel("agent::ready") {
		t.Fatalf("switching autoFix on while red must hand the issue over: %v", is.Labels)
	}
}

func TestMainHealthAutoFixSkipsAnIssueWithUnmarkedLogs(t *testing.T) {
	e, advance := newMainEnv(t)
	is := redMain(e, advance)
	is.Description = strings.Replace(is.Description, marker.UntrustedData, "", 1) // opened by an older operator
	e.setMainHealth(aiv1.MainHealthSpec{AutoFix: true})
	e.check(advance)
	if is.HasLabel("agent::ready") {
		t.Fatalf("logs not marked as untrusted data must not become the agent's task: %v", is.Labels)
	}
}

func TestMainHealthGreenClosesTheAutoFixIssue(t *testing.T) {
	e, advance := newMainEnv(t)
	e.setMainHealth(aiv1.MainHealthSpec{AutoFix: true})
	is := redMain(e, advance)
	e.reconcile()
	if e.taskOf(is.IID) == nil {
		t.Fatal("no task for the main-red issue")
	}
	e.gl.AddPipeline("sha2", "main", "success")
	e.check(advance)
	notes := e.gl.IssueNotes[is.IID]
	if is.State != "closed" || !strings.Contains(notes[len(notes)-1].Body, "left for a human to merge or close") {
		t.Fatalf("green on its own must close the issue and leave the MR to a human: %s, %+v", is.State, notes)
	}
	if st := e.mainHealth(); st.AutoFixIssueIID != 0 || st.IssueIID != 0 {
		t.Fatalf("the red episode is over: %+v", st)
	}
	e.reconcile()
	if e.taskOf(is.IID) != nil {
		t.Fatal("the closed issue's task must go")
	}
}

func TestCIMainRedIssueFixesItsJobs(t *testing.T) {
	e := newCIEnv(t, "sha1")
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 42, FailedJobs: []string{"test"}})
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest())
	e.reconcile() // retry
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile()
	if got := e.ciState(); got != aiv1.CIStateFixing || e.ciTask().Spec.RunRequest != 1 {
		t.Fatalf("the main-red issue's change must fix the jobs that fail on main: state %s", got)
	}
}

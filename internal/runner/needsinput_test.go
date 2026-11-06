package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// causeAtLabel records the task's needs-input cause and URL as they are
// when the needs-input label is added.
type causeAtLabel struct {
	*gitlabfake.Fake
	k8s  client.Client
	task string
	seen []string
}

func (c *causeAtLabel) UpdateIssue(ctx context.Context, project string, iid int64, opts gitlab.UpdateIssueOptions) (*gitlab.Issue, error) {
	if slices.Contains(opts.AddLabels, "agent::needs-input") {
		var task aiv1.AgentTask
		if err := c.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: c.task}, &task); err != nil {
			return nil, err
		}
		c.seen = append(c.seen, string(task.Status.Run.NeedsInputCause)+" "+task.Status.Run.NeedsInputURL)
	}
	return c.Fake.UpdateIssue(ctx, project, iid, opts)
}

func (e *implementEnv) runStatus(t *testing.T) aiv1.RunStatus {
	t.Helper()
	var task aiv1.AgentTask
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task); err != nil {
		t.Fatal(err)
	}
	return task.Status.Run
}

const issueURL = "https://gitlab.example/group/app/-/issues/42"

func TestRunFailureRecordsItsCauseBeforeTheLabel(t *testing.T) {
	e := newImplementEnv(t)
	e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(s *aiv1.AgentTaskSpec) { s.Issue.WebURL = issueURL })
	gl := &causeAtLabel{Fake: e.gl, k8s: e.k8s, task: e.task.Name}
	r := newTestRunner(t, e.tmp, e.k8s, gl, &describingEngine{runErr: errors.New("engine exploded")}, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	notes := e.gl.IssueNotes[42]
	want := fmt.Sprintf("%s#note_%d", issueURL, notes[len(notes)-1].ID)
	if !slices.Equal(gl.seen, []string{"runFailed " + want}) {
		t.Fatalf("the label comes after the cause %q, saw %q", want, gl.seen)
	}
	if run := e.runStatus(t); run.NeedsInputCause != aiv1.NeedsInputRunFailed || run.NeedsInputURL != want {
		t.Fatalf("the final patch keeps the cause: %q %q", run.NeedsInputCause, run.NeedsInputURL)
	}
}

func TestBlockedFailureRecordsNoCause(t *testing.T) {
	e := newImplementEnv(t)
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{}, e.task.Name)
	out := &Outcome{}
	r.run = newRunLog(r.Log, e.task)
	task := e.task.DeepCopy()
	r.reportFailure(context.Background(), e.gl, e.k8sProjectSpec(t), task, out, errors.New("pod lost"), aiv1.BlockInfra)
	if out.NeedsInputCause != "" || e.runStatus(t).NeedsInputCause != "" {
		t.Fatalf("a blocked issue does not need input: %+v", out)
	}
}

func (e *implementEnv) k8sProjectSpec(t *testing.T) aiv1.AgentProjectSpec {
	t.Helper()
	var p aiv1.AgentProject
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "shop"}, &p); err != nil {
		t.Fatal(err)
	}
	return p.Spec.WithDefaults()
}

func TestSuccessfulRunClearsTheCause(t *testing.T) {
	e := newImplementEnv(t)
	var task aiv1.AgentTask
	ctx := context.Background()
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	task.Status.Run.NeedsInputCause, task.Status.Run.NeedsInputURL = aiv1.NeedsInputRunFailed, issueURL+"#note_1"
	if err := e.k8s.Status().Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{}, e.task.Name)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if run := e.runStatus(t); run.LastResult != aiv1.RunResultSucceeded || run.NeedsInputCause != "" || run.NeedsInputURL != "" {
		t.Fatalf("a run without needs-input clears the cause: %s %q %q", run.LastResult, run.NeedsInputCause, run.NeedsInputURL)
	}
}

func TestAgentQuestionRecordsItsCauseBeforeTheLabel(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	run := phaseRun(t, e, &scriptedEngine{})
	run.spec.CI.Disabled = true
	phases(t, run, append(upToPrompt(run), run.implement, run.concludeMerge, run.reviewLoop, run.push, run.postDecisionsAndHandoff)...)
	gl := &causeAtLabel{Fake: e.gl, k8s: e.k8s, task: e.task.Name}
	run.gl = gl
	run.answer.Status = "needs_input"
	if err := run.report(ctx); err != nil {
		t.Fatal(err)
	}
	notes := e.gl.MRNotes[run.mr.IID]
	want := fmt.Sprintf("%s#note_%d", run.mr.WebURL, notes[len(notes)-1].ID)
	if !slices.Equal(gl.seen, []string{"agentQuestion " + want}) {
		t.Fatalf("the label comes after the cause %q, saw %q", want, gl.seen)
	}
	if out := run.outcome(ctx); out.Result != aiv1.RunResultNeedsInput || out.NeedsInputCause != aiv1.NeedsInputAgentQuestion || out.NeedsInputURL != want {
		t.Fatalf("the outcome keeps the cause for the final patch: %+v", out)
	}
}

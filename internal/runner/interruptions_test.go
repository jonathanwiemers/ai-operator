package runner

import (
	"context"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// interruptTask records that an attempt of the task's run request started
// and never finished, as a lost pod leaves it.
func interruptTask(t *testing.T, e *implementEnv, interruptions int32) {
	t.Helper()
	ctx := context.Background()
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	orig := task.DeepCopy()
	task.Status.Run.ActiveRunRequest = task.Spec.RunRequest
	task.Status.Run.Interruptions = interruptions
	if err := e.k8s.Status().Patch(ctx, &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
}

func allNotes(e *implementEnv) string {
	var b strings.Builder
	for _, n := range e.gl.IssueNotes[42] {
		b.WriteString(n.Body)
	}
	for _, notes := range e.gl.MRNotes {
		for _, n := range notes {
			b.WriteString(n.Body)
		}
	}
	return b.String()
}

// statusProbe records the task's run status when the agent is first called.
type statusProbe struct {
	Engine
	k8s  client.Client
	task string
	seen *aiv1.RunStatus
}

func (p *statusProbe) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if p.seen == nil {
		var task aiv1.AgentTask
		_ = p.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: p.task}, &task)
		p.seen = task.Status.Run.DeepCopy()
	}
	return p.Engine.Run(ctx, inv)
}

func TestInterruptedRunSaysItStartedAgain(t *testing.T) {
	e := newImplementEnv(t)
	interruptTask(t, e, 1)
	probe := &statusProbe{Engine: &scriptedEngine{}, k8s: e.k8s, task: e.task.Name}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, probe, e.task.Name)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	// Recorded while the run goes on, so a lost pod counts towards the limit.
	if p := probe.seen; p == nil || p.Interruptions != 2 || p.ActiveRunRequest <= p.ObservedRunRequest {
		t.Fatalf("the restart must be counted at the start of the run: %+v", p)
	}
	if notes := allNotes(e); !strings.Contains(notes, "previous attempt was interrupted") || !strings.Contains(notes, "Restart 2 of at most 3") {
		t.Fatalf("the report should say the run started again: %q", notes)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if run := task.Status.Run; run.Interruptions != 0 || run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("a finished run resets the count: %+v", run)
	}
}

func TestUninterruptedRunHasNoRestartNotice(t *testing.T) {
	e := newImplementEnv(t)
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &scriptedEngine{}, e.task.Name)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if notes := allNotes(e); strings.Contains(notes, "interrupted") {
		t.Fatalf("no restart notice expected: %q", notes)
	}
}

func TestRunGivesUpAfterTooManyInterruptions(t *testing.T) {
	e := newImplementEnv(t)
	ctx := context.Background()
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	task.Spec.Owners = []string{"alice"}
	if err := e.k8s.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	interruptTask(t, e, aiv1.MaxInterruptions)
	// The lost attempt's live progress comment is still on its MR.
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	orig := task.DeepCopy()
	task.Status.Run.MergeRequestIID = 7
	if err := e.k8s.Status().Patch(ctx, &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.gl.MRNotes[7] = []gitlab.Note{{ID: 70, Body: progressMarker + "\n🤖 " + liveHeadline, Author: gitlab.User{ID: e.gl.Bot.ID}}}
	engine := &scriptedEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if ran, err := r.Step(ctx); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if len(engine.calls) != 0 {
		t.Fatalf("the agent must not run again: %d calls", len(engine.calls))
	}
	note := failureNote(e)
	if !strings.Contains(note, "interrupted 4 times") || !strings.Contains(note, "@alice") {
		t.Fatalf("expected the failure with the owner mention: %q", note)
	}
	if labels := e.gl.Labels(42); !slices.Contains(labels, "agent::needs-input") || slices.Contains(labels, "agent::working") {
		t.Fatalf("the issue waits for input: %v", labels)
	}
	if len(e.gl.MRNotes[7]) != 0 {
		t.Fatalf("the stale progress comment must go: %+v", e.gl.MRNotes[7])
	}
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if run := task.Status.Run; task.RunPending() || run.LastResult != aiv1.RunResultFailed || run.Interruptions != 0 {
		t.Fatalf("the run ends failed: %+v", run)
	}
}

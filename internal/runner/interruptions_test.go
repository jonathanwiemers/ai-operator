package runner

import (
	"context"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
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

func TestInterruptedRunSaysItStartedAgain(t *testing.T) {
	e := newImplementEnv(t)
	interruptTask(t, e, 0)
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &scriptedEngine{}, e.task.Name)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if notes := allNotes(e); !strings.Contains(notes, "previous attempt was interrupted") || !strings.Contains(notes, "Restart 1 of at most 3") {
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
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if run := task.Status.Run; task.RunPending() || run.LastResult != aiv1.RunResultFailed || run.Interruptions != 0 {
		t.Fatalf("the run ends failed: %+v", run)
	}
}

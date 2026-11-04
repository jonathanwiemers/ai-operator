package runner

import (
	"context"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Bloop #331 on an autopilot milestone: the pilot manages, yet the
// assessment is a prompt like any other, with the trigger and the evidence
// for the operator's audit; the runner posts nothing and pings nobody. The
// operator runs the recommended option or puts the prompt to the owners.
func TestPilotManagedAssessmentIsAPromptToo(t *testing.T) {
	e := newAssessEnv(t, "")
	task := e.task(t)
	task.Spec.Autopilot = true
	if err := e.k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	e.step(t)
	p := e.proposed(t)
	if p.Trigger != 1 || !strings.HasPrefix(p.Why, "`env`: `rust:lint` fails") || !p.Quiet || p.State == "" || len(p.Options) != 2 {
		t.Fatalf("the prompt names its trigger and why: %+v", p)
	}
	if a := botNotes(e.gl, 5); len(a) != 0 {
		t.Fatalf("the runner posts nothing: %q", a)
	}
}

func TestPilotRunKeepsTheCIRoundsAHumanRunStartsOver(t *testing.T) {
	task := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{RunRequest: 4}, Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{CIRound: 2}}}
	if got := ciRound(task, true); got != 3 {
		t.Fatalf("a CI fix run is one round more: %d", got)
	}
	task.Spec.Source.PilotRun = 4
	if got := ciRound(task, false); got != 2 {
		t.Fatalf("a run the pilot started keeps the rounds: %d", got)
	}
	// A human's "/agent …" after the pilot's request is a new request.
	task.Spec.RunRequest = 5
	if got := ciRound(task, false); got != 0 {
		t.Fatalf("a human's run starts over: %d", got)
	}
	// A run without any request (an agent label) is nobody's pilot run.
	task.Spec.RunRequest, task.Spec.Source.PilotRun = 0, 0
	if got := ciRound(task, false); got != 0 {
		t.Fatalf("a run of no request starts over: %d", got)
	}
}

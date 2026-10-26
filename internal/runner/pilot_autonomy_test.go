package runner

import (
	"context"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Bloop #331 on an autopilot milestone: the pilot manages, so the
// assessment records its proposal with the trigger and the evidence for
// the operator's audit, and pings nobody; the operator acts or pings.
func TestPilotManagedAssessmentRecordsWhyAndPingsNobody(t *testing.T) {
	e := newAssessEnv(t, "")
	task := e.task(t)
	task.Spec.Autopilot = true
	if err := e.k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	e.engine.assessment.Proposal = proposalOf(t, `{"summary": "install the toolchain", "actions": [{"tool": "instruct", "issue": 1,
		"kind": "install-toolchains", "note": "cargo fmt --check failed; no Rust toolchain"}]}`)
	e.step(t)
	ps := e.task(t).Status.Pilot
	if ps == nil || len(ps.Proposed) != 1 || ps.Proposed[0].Trigger != 1 || !strings.HasPrefix(ps.Proposed[0].Why, "`env`: `rust:lint` fails") {
		t.Fatalf("the proposal names its trigger and why: %+v", ps)
	}
	a := botNotes(e.gl, 5)
	if len(a) != 1 || strings.Contains(a[0], "@alice") || !strings.Contains(a[0], "within its interventions") {
		t.Fatalf("a managed assessment with a proposal pings nobody: %q", a)
	}
}

func TestPilotManagedAssessmentWithoutAProposalPings(t *testing.T) {
	e := newAssessEnv(t, "")
	task := e.task(t)
	task.Spec.Autopilot = true
	if err := e.k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	e.step(t)
	if a := botNotes(e.gl, 5); len(a) != 1 || !strings.HasSuffix(a[0], "cc @alice") {
		t.Fatalf("nothing to act on: the owners hear of it: %q", a)
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

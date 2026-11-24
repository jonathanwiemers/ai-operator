package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// pilotStateWith is the pilot's state text with #1's task changed by mutate.
func pilotStateWith(t *testing.T, mutate func(*aiv1.AgentTask)) string {
	t.Helper()
	e := newPilotEnv(t, "")
	ctx := context.Background()
	var impl aiv1.AgentTask
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop-1-api"}, &impl); err != nil {
		t.Fatal(err)
	}
	mutate(&impl)
	status := impl.Status // Update writes the spec and reads the old status back
	if err := e.k8s.Update(ctx, &impl); err != nil {
		t.Fatal(err)
	}
	impl.Status = status
	if err := e.k8s.Status().Update(ctx, &impl); err != nil {
		t.Fatal(err)
	}
	var project aiv1.AgentProject
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop"}, &project); err != nil {
		t.Fatal(err)
	}
	pilot := e.task(t)
	state, _, _, err := e.r.pilotState(ctx, e.gl, &pilot, &project, project.Spec)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestPilotStateHasTheRunningImplementer(t *testing.T) {
	since := metav1.NewTime(time.Date(2026, 11, 24, 14, 1, 0, 0, time.UTC))
	state := pilotStateWith(t, func(impl *aiv1.AgentTask) {
		impl.Spec.Source.OwnerMessages = []aiv1.OwnerMessage{
			{ID: "tell/55", Owner: "alice", Text: "use the existing retry helper", At: since},
			{ID: "chat/3", Owner: "alice", Text: "and keep the tests", At: since},
		}
		run := &impl.Status.Run
		run.ActiveRunRequest, run.ObservedRunRequest, run.RunCount = 4, 3, 2
		run.CurrentStep, run.CurrentStepSince = "9 run implementer", &since
		run.DeliveredMessages = []aiv1.DeliveredMessage{{ID: "chat/3", How: aiv1.DeliveryStream, At: since}}
		run.Calls = []aiv1.AgentCall{
			{Run: 2, Role: "implement", Engine: "claude"},
			{Run: 3, Role: "implement", Engine: "cursor"},
			{Run: 3, Role: "review", Engine: "claude"},
		}
	})
	for _, want := range []string{`"running": {`, `"step": "9 run implementer"`, `"since": "2026-11-24T14:01:00Z"`, `"engine": "cursor"`,
		`"waitingOwnerMessages": [`, `"text": "use the existing retry helper"`, `"owner": "alice"`} {
		if !strings.Contains(state, want) {
			t.Fatalf("the state lacks %s:\n%s", want, state)
		}
	}
	if strings.Contains(state, "and keep the tests") {
		t.Fatalf("a delivered message does not wait:\n%s", state)
	}
	if !strings.Contains(pilotSystemPrompt, "on a running issue\n  (issues[].running) they reach its implementer at once, mid-run") {
		t.Error("the system prompt says where an owner's words go")
	}
}

func TestPilotStateHasNoRunBetweenRuns(t *testing.T) {
	state := pilotStateWith(t, func(impl *aiv1.AgentTask) {
		impl.Status.Run.ActiveRunRequest, impl.Status.Run.ObservedRunRequest = 3, 3
	})
	if strings.Contains(state, `"running"`) {
		t.Fatalf("no run, no running entry:\n%s", state)
	}
}

func TestRunningOfFallsBackToTheIssuesEngine(t *testing.T) {
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	role, _, _ := spec.IssueRole(nil)
	task := &aiv1.AgentTask{}
	task.Status.Run.ActiveRunRequest = 1
	task.Status.Run.Calls = []aiv1.AgentCall{{Run: 0, Role: "implement", Engine: "other"}}
	if r := runningOf(task, spec, nil); r == nil || r.Engine != role.Engine || r.Waiting != nil {
		t.Fatalf("a run without an implementer call yet works on the issue's engine %q: %+v", role.Engine, r)
	}
}

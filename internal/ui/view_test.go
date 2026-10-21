package ui

import (
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Minute:                 "1m",
		90 * time.Second:            "1m30s",
		2 * time.Hour:               "2h",
		2*time.Hour + 5*time.Minute: "2h5m",
		30 * time.Second:            "30s",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestStageOf(t *testing.T) {
	task := func(typ aiv1.TaskType, requested, observed int64, phase aiv1.TaskPhase, result aiv1.RunResult, ci *aiv1.CIStatus) *aiv1.AgentTask {
		return &aiv1.AgentTask{
			Spec: aiv1.AgentTaskSpec{Type: typ, RunRequest: requested},
			Status: aiv1.AgentTaskStatus{
				Phase: phase,
				Run:   aiv1.RunStatus{ObservedRunRequest: observed, LastResult: result},
				CI:    ci,
			},
		}
	}
	impl, spec := aiv1.TaskTypeImplement, aiv1.TaskTypeSpec
	cases := []struct {
		name string
		t    *aiv1.AgentTask
		want Stage
	}{
		{"new", task(impl, 0, 0, aiv1.TaskPhasePending, "", nil), StagePending},
		{"requested", task(impl, 1, 0, aiv1.TaskPhaseRunning, "", nil), StageWorking},
		{"follow-up requested", task(impl, 2, 1, aiv1.TaskPhaseIdle, aiv1.RunResultSucceeded, nil), StageWorking},
		{"queued", task(impl, 1, 0, aiv1.TaskPhaseQueued, "", nil), StageWaiting},
		{"usage limit", task(impl, 1, 0, aiv1.TaskPhaseWaiting, aiv1.RunResultDeferred, nil), StageWaiting},
		{"blocked", task(impl, 3, 1, aiv1.TaskPhaseBlocked, aiv1.RunResultFailed, nil), StageBlocked},
		{"questions", task(impl, 1, 1, aiv1.TaskPhaseIdle, aiv1.RunResultNeedsInput, nil), StageNeedsInput},
		{"failed", task(impl, 1, 1, aiv1.TaskPhaseIdle, aiv1.RunResultFailed, nil), StageFailed},
		{"pipeline running", task(impl, 1, 1, aiv1.TaskPhaseIdle, aiv1.RunResultSucceeded, &aiv1.CIStatus{State: aiv1.CIStateRunning}), StageCI},
		{"pipeline red for good", task(impl, 1, 1, aiv1.TaskPhaseIdle, aiv1.RunResultSucceeded, &aiv1.CIStatus{State: aiv1.CIStateFailed}), StageNeedsInput},
		{"green", task(impl, 1, 1, aiv1.TaskPhaseHibernated, aiv1.RunResultSucceeded, &aiv1.CIStatus{State: aiv1.CIStateSuccess}), StageReview},
		{"red on main too", task(impl, 1, 1, aiv1.TaskPhaseIdle, aiv1.RunResultSucceeded, &aiv1.CIStatus{State: aiv1.CIStateBaseFailing}), StageReview},
		{"planned", task(spec, 1, 1, aiv1.TaskPhaseIdle, aiv1.RunResultSucceeded, nil), StagePlanned},
	}
	for _, tc := range cases {
		if got := StageOf(tc.t, true); got != tc.want {
			t.Errorf("%s: stage %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestStageOfPush(t *testing.T) {
	pushed := func(ci *aiv1.CIStatus) *aiv1.AgentTask {
		return &aiv1.AgentTask{
			Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, RunRequest: 2},
			Status: aiv1.AgentTaskStatus{
				Phase: aiv1.TaskPhaseIdle,
				Run:   aiv1.RunStatus{ObservedRunRequest: 2, LastResult: aiv1.RunResultSucceeded, PushedSHA: "new"},
				CI:    ci,
			},
		}
	}
	cases := []struct {
		name      string
		t         *aiv1.AgentTask
		ciEnabled bool
		want      Stage
	}{
		{"no pipeline followed yet", pushed(nil), true, StageCI},
		{"previous commit's pipeline failed", pushed(&aiv1.CIStatus{SHA: "old", State: aiv1.CIStateFailed}), true, StageCI},
		{"pipeline of the push is green", pushed(&aiv1.CIStatus{SHA: "new", State: aiv1.CIStateSuccess}), true, StageReview},
		{"CI disabled", pushed(nil), false, StageReview},
	}
	for _, tc := range cases {
		if got := StageOf(tc.t, tc.ciEnabled); got != tc.want {
			t.Errorf("%s: stage %q, want %q", tc.name, got, tc.want)
		}
	}

	suspended := pushed(nil)
	suspended.Spec.Suspend = true
	suspended.Spec.RunRequest = 3
	suspended.Status.Phase = aiv1.TaskPhaseHibernated
	if got := StageOf(suspended, true); got != StageWaiting {
		t.Errorf("suspended task with a requested run: stage %q, want %q", got, StageWaiting)
	}
}

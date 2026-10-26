package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// specRetryEnv is a planned milestone whose first run is pending, with a
// clock the test moves.
func specRetryEnv(t *testing.T, description string) (*projectEnv, *time.Time) {
	t.Helper()
	e := newProjectEnv(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	e.r.Now = func() time.Time { return now }
	e.gl.Milestones = []gitlab.Milestone{{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: description}}
	e.reconcile()
	return e, &now
}

// failSpecRun ends the pending spec run as the runner does after a failure.
func (e *projectEnv) failSpecRun(now time.Time, block aiv1.BlockReason) {
	e.t.Helper()
	request := e.tasks()[specTaskName].Spec.RunRequest
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest, s.Run.ActiveRunRequest = request, request
		s.Run.LastResult, s.Run.BlockReason = aiv1.RunResultFailed, block
		s.Run.LastActivityTime = &metav1.Time{Time: now}
	})
}

func (e *projectEnv) specRunRequest() int64 {
	e.t.Helper()
	return e.tasks()[specTaskName].Spec.RunRequest
}

func TestSpecRunFailedForAnOutsideReasonIsRetriedOnce(t *testing.T) {
	for _, tc := range []struct{ name, description string }{
		{"spec", "/agent spec\nBuild a new checkout."},
		{"autopilot", "/agent autopilot\nBuild a new checkout."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, now := specRetryEnv(t, tc.description)
			e.failSpecRun(*now, aiv1.BlockInfra)

			*now = now.Add(aiv1.DefaultInfraRetryAfter - time.Minute)
			e.reconcile()
			if got := e.specRunRequest(); got != 1 {
				t.Fatalf("no retry before lifecycle.infraRetryAfter: run request %d", got)
			}
			*now = now.Add(time.Minute)
			e.reconcile()
			e.reconcile()
			task := e.tasks()[specTaskName]
			if task.Spec.RunRequest != 2 || task.Spec.Source.InfraRetry != 2 {
				t.Fatalf("expected exactly one retry: %+v", task.Spec)
			}

			// The retry fails as well: no further run.
			e.failSpecRun(*now, aiv1.BlockInfra)
			*now = now.Add(24 * time.Hour)
			e.reconcile()
			e.reconcile()
			if got := e.specRunRequest(); got != 2 {
				t.Fatalf("the retry's own failure is not retried: run request %d", got)
			}

			// A spec change plans again; its run gets a retry of its own.
			e.gl.Milestones[0].Description += "\nAlso support PayPal."
			e.reconcile()
			e.failSpecRun(*now, aiv1.BlockInfra)
			*now = now.Add(aiv1.DefaultInfraRetryAfter)
			e.reconcile()
			if got := e.specRunRequest(); got != 4 {
				t.Fatalf("a new run request gets its own retry: run request %d", got)
			}
		})
	}
}

func TestSpecRunFailedOnTheWorkIsNotRetried(t *testing.T) {
	e, now := specRetryEnv(t, "/agent spec\nBuild a new checkout.")
	e.failSpecRun(*now, "")
	*now = now.Add(24 * time.Hour)
	e.reconcile()
	if got := e.specRunRequest(); got != 1 {
		t.Fatalf("a failure that is not an outside one waits for a spec change: run request %d", got)
	}
}

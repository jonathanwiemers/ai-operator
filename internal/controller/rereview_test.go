package controller

import (
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// reviewersDown is #1 on autopilot in review, with its reviewers failed on
// sha-api, transiently or not.
func reviewersDown(t *testing.T, transient bool) (*projectEnv, string) {
	t.Helper()
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", func(r *aiv1.RunStatus) {
		r.ReviewErrors, r.ReviewErrorsTransient = 2, transient
		r.LastActivityTime = &metav1.Time{Time: e.gl.Clock}
	})
	return e, name
}

// reReviewed requests the re-review after review.retryAfter (default 15m).
func reReviewed(t *testing.T, e *projectEnv, name string) {
	t.Helper()
	e.reconcile()
	if task := e.tasks()[name]; task.Spec.Source.ReviewFor != "" || task.RunPending() || e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("the re-review waits for review.retryAfter, without a pause: %+v %+v", task.Spec, e.autopilot())
	}
	e.gl.Clock = e.gl.Clock.Add(16 * time.Minute)
	e.reconcile()
	task := e.tasks()[name]
	if task.Spec.Source.ReviewFor != "sha-api" || !task.RunPending() || !slices.Contains(e.gl.Labels(1), "agent::working") {
		t.Fatalf("expected a re-review run for sha-api: %+v %v", task.Spec, e.gl.Labels(1))
	}
}

func TestAutopilotReReviewsTransientReviewerFailuresOnce(t *testing.T) {
	e, name := reviewersDown(t, true)
	reReviewed(t, e, name)
	request := e.tasks()[name].Spec.RunRequest
	e.reconcile()
	e.reconcile()
	if got := e.tasks()[name].Spec.RunRequest; got != request || e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("exactly one re-review, no pause: run request %d, was %d, %+v", got, request, e.autopilot())
	}
}

// failedAgain plays the re-review run failing transiently again.
func failedAgain(t *testing.T, e *projectEnv, name string) {
	t.Helper()
	task := e.tasks()[name]
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest, s.Run.ReviewedFor = task.Spec.RunRequest, "sha-api"
		s.Run.LastActivityTime = &metav1.Time{Time: e.gl.Clock}
	})
	e.gl.SetLabels(1, "ai-generated", "agent::review")
	e.reconcile()
	e.reconcile()
}

func TestAutopilotSecondTransientFailurePausesAndBlocks(t *testing.T) {
	e, name := reviewersDown(t, true)
	reReviewed(t, e, name)
	request := e.tasks()[name].Spec.RunRequest
	failedAgain(t, e, name)

	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PausedFor != 1 || !strings.Contains(ap.PausedReason, "also when reviewed again") {
		t.Fatalf("a second transient failure on the head pauses: %+v", ap)
	}
	task := e.tasks()[name]
	if b := task.Status.Blocked; b == nil || b.Reason != aiv1.BlockInfra {
		t.Fatalf("the pause blocks #1 with reason infra: %+v", task.Status.Blocked)
	}
	if labels := e.gl.Labels(1); !slices.Contains(labels, "agent::blocked") || !slices.Contains(labels, "agent-blocked::infra") {
		t.Fatalf("the board shows #1 blocked: %v", labels)
	}
	if task.Spec.RunRequest != request || len(e.gl.Merged) != 0 {
		t.Fatalf("no further re-review and no merge: run request %d, was %d, merged %v", task.Spec.RunRequest, request, e.gl.Merged)
	}
}

func TestAutopilotEndOfAnInfraPauseUnblocks(t *testing.T) {
	for _, how := range []string{"merged by hand", "resume"} {
		t.Run(how, func(t *testing.T) {
			e, name := reviewersDown(t, true)
			reReviewed(t, e, name)
			failedAgain(t, e, name)
			if e.tasks()[name].Status.Blocked == nil {
				t.Fatal("expected #1 blocked")
			}
			if how == "resume" {
				e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
			} else {
				e.gl.MRs[e.tasks()[name].Status.Run.MergeRequestIID].State = "merged"
			}
			e.reconcile()
			labels := e.gl.Labels(1)
			if e.tasks()[name].Status.Blocked != nil || slices.Contains(labels, "agent::blocked") || slices.Contains(labels, "agent-blocked::infra") {
				t.Fatalf("the end of the pause unblocks #1: %+v %v", e.tasks()[name].Status.Blocked, labels)
			}
		})
	}
}

func TestAutopilotNonTransientReviewerFailurePausesAtOnce(t *testing.T) {
	e, name := reviewersDown(t, false)
	e.reconcile()
	ap := e.autopilot()
	task := e.tasks()[name]
	if ap.Phase != aiv1.AutopilotPaused || !strings.Contains(ap.PausedReason, "2 reviewers failed on #1") || strings.Contains(ap.PausedReason, "again") {
		t.Fatalf("a real reviewer failure pauses as before: %+v", ap)
	}
	if task.Spec.Source.ReviewFor != "" || task.RunPending() || task.Status.Blocked != nil {
		t.Fatalf("no re-review and no block: %+v %+v", task.Spec, task.Status.Blocked)
	}
}

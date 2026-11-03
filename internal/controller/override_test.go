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

// ownedReviewersDown is reviewersDown (paused, reviewers failed for good on
// sha-api) on a milestone owned by alice, Desk subject ada@x.
func ownedReviewersDown(t *testing.T) (*projectEnv, string) {
	t.Helper()
	e, name := reviewersDown(t, false)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones[0].Description = "/agent owners @alice\n/agent owner-subjects ada@x\n" + e.gl.Milestones[0].Description
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseReviewErrors {
		t.Fatalf("expected a pause over the failed reviewers: %+v", ap)
	}
	return e, name
}

func TestOwnerMergesWithoutAFullReview(t *testing.T) {
	e, _ := ownedReviewersDown(t)
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 sha-api")
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("the owner's override should merge the head: merged %v, %+v", e.gl.Merged, e.autopilot())
	}
	ov := e.autopilot().MergeOverride
	if ov == nil || ov.IssueIID != 1 || ov.SHA != "sha-api" || ov.By != "@alice" {
		t.Fatalf("the override is recorded: %+v", ov)
	}
	if !strings.Contains(trackingComments(e), "lets autopilot merge !1 at sha-api without a full review") {
		t.Fatalf("the override is said on the tracking issue:\n%s", trackingComments(e))
	}
}

func TestMergeOverrideIsRefused(t *testing.T) {
	t.Run("not an owner", func(t *testing.T) {
		e, _ := ownedReviewersDown(t)
		e.gl.AddNote(e.autopilot().TrackingIssueIID, mallory, "/agent merge #1 sha-api")
		e.reconcile()
		if ap := e.autopilot(); ap.MergeOverride != nil || ap.Phase != aiv1.AutopilotPaused || len(e.gl.Merged) != 0 {
			t.Fatalf("only owners may merge without a full review: %+v", ap)
		}
		if !strings.Contains(trackingComments(e), notOwnerMerge) {
			t.Fatalf("the refusal is answered:\n%s", trackingComments(e))
		}
	})
	t.Run("another head", func(t *testing.T) {
		e, _ := ownedReviewersDown(t)
		e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 0123abc")
		e.reconcile()
		if ap := e.autopilot(); ap.MergeOverride != nil || len(e.gl.Merged) != 0 {
			t.Fatalf("an override for another head changes nothing: %+v", ap)
		}
		if !strings.Contains(trackingComments(e), "The head of !1 is sha-api, not 0123abc") {
			t.Fatalf("the refusal names the head:\n%s", trackingComments(e))
		}
	})
	t.Run("without the head", func(t *testing.T) {
		e, _ := ownedReviewersDown(t)
		e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1")
		e.reconcile()
		if ap := e.autopilot(); ap.MergeOverride != nil || len(e.gl.Merged) != 0 {
			t.Fatalf("an override names the head the owner looked at: %+v", ap)
		}
		if !strings.Contains(trackingComments(e), "`/agent merge #1 sha-api` for the current head of !1") {
			t.Fatalf("the answer gives the command for the current head:\n%s", trackingComments(e))
		}
	})
	t.Run("from the Desk, not an owner subject", func(t *testing.T) {
		e, _ := ownedReviewersDown(t)
		st := e.tasks()[specTaskName]
		st.Spec.Source.ChatMessages = []aiv1.ChatMessage{{ID: 1, Kind: "command", Text: `{"cmd":"merge","issue":1,"sha":"5a"}`, Author: "eve@x"},
			{ID: 2, Kind: "command", Text: `{"cmd":"merge","issue":1,"sha":"0123abc"}`, Author: "eve@x"}}
		if err := e.c.Update(e.ctx, &st); err != nil {
			t.Fatal(err)
		}
		e.reconcile()
		got := e.tasks()[specTaskName].Spec.Source.ChatAnswers
		if len(got) != 2 || !strings.Contains(got[0].Text, "sha of its head") || got[1].Text != notOwnerMerge || e.autopilot().MergeOverride != nil {
			t.Fatalf("a bad sha and a non-owner are refused: %+v", got)
		}
	})
}

func TestMergeOverrideFromTheDesk(t *testing.T) {
	e, _ := ownedReviewersDown(t)
	st := e.tasks()[specTaskName]
	st.Spec.Source.ChatMessages = []aiv1.ChatMessage{{ID: 1, Kind: "command", Text: `{"cmd":"merge","issue":1,"sha":"5a0e000"}`, Author: "ada@x", AuthorName: "Ada"}}
	e.finishRun(1, "5a0e0001", func(r *aiv1.RunStatus) { r.ReviewErrors = 2 }) // pushed again, as !2
	if err := e.c.Update(e.ctx, &st); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	e.reconcile()
	if ov := e.autopilot().MergeOverride; ov == nil || ov.SHA != "5a0e0001" || ov.By != "Ada via the Desk" || !slices.Equal(e.gl.Merged, []int64{2}) {
		t.Fatalf("an owner subject's override merges the head it names: %+v, merged %v", ov, e.gl.Merged)
	}
}

func TestMergeOverrideKeepsTheRestOfTheGate(t *testing.T) {
	t.Run("a new head voids it", func(t *testing.T) {
		e, name := ownedReviewersDown(t)
		e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 sha-api")
		e.reconcile() // the override, and the pause ends
		e.finishRun(1, "sha-new", func(r *aiv1.RunStatus) { r.ReviewErrors = 1 })
		e.reconcile()
		if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseReviewErrors || len(e.gl.Merged) != 0 {
			t.Fatalf("a new push needs its own review or override: %+v, merged %v, task %s", ap, e.gl.Merged, name)
		}
	})
	t.Run("a later review voids it", func(t *testing.T) {
		e, name := ownedReviewersDown(t)
		e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 sha-api")
		e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 sha-api")
		e.reconcile() // the override, once; the pause ends
		if n := strings.Count(trackingComments(e), "without a full review."); n != 1 {
			t.Fatalf("the same override again changes nothing, got %d comments", n)
		}
		// A re-review of the same head finishes after it, with findings.
		e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
			s.Run.ReviewErrors, s.Run.BlockingFindings = 0, 1
			s.Run.LastActivityTime = &metav1.Time{Time: e.gl.Clock.Add(time.Minute)}
		})
		e.reconcile()
		if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseFindings || len(e.gl.Merged) != 0 {
			t.Fatalf("findings of a later review are not overridden: %+v, merged %v", ap, e.gl.Merged)
		}
	})
	t.Run("red CI", func(t *testing.T) {
		e, name := ownedReviewersDown(t)
		e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
			s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateFailed, FailedJobs: []string{"test"}}
		})
		e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 sha-api")
		e.reconcile()
		e.reconcile()
		if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseCIRed || len(e.gl.Merged) != 0 {
			t.Fatalf("the override does not skip CI: %+v, merged %v", ap, e.gl.Merged)
		}
	})
}

func TestMergeOverrideSurvivesARebase(t *testing.T) {
	e, name := ownedReviewersDown(t)
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 sha-api")
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "need_rebase"
	e.reconcile() // the override; the pause ends
	e.reconcile() // rebase requested
	e.reconcile() // the rebased head goes to the CI loop
	if ov := e.autopilot().MergeOverride; ov == nil || ov.SHA != "sha-api-rebased" {
		t.Fatalf("a rebase that keeps the change carries the override: %+v, rebased %v", ov, e.gl.Rebased)
	}
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.CI = &aiv1.CIStatus{SHA: "sha-api-rebased", State: aiv1.CIStateSuccess}
	})
	e.gl.SetLabels(1, "ai-generated", "agent::review")
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("the rebased head merges under the override: merged %v, %+v", e.gl.Merged, e.autopilot())
	}
}

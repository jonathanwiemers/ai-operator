package controller

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// baseFailingEnv replays Bloop milestone 24: main is red at main-x (#99),
// #1's ready MR !1 is based on main-x and red on the job that fails on main
// (BaseFailing), so autopilot paused ciRed and waits for a green MR pipeline
// that nothing starts.
func baseFailingEnv(t *testing.T) (*projectEnv, string) {
	t.Helper()
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	return e, e.pauseBaseFailing()
}

// pauseBaseFailing pauses e's autopilot ciRed over #1's BaseFailing MR !1
// (baseFailingEnv) and returns #1's task.
func (e *projectEnv) pauseBaseFailing() string {
	t := e.t
	t.Helper()
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, SHA: "main-x", IssueIID: 99, FailedJobs: []string{"test"}})
	name := e.finishRun(1, "sha-api", nil)
	mr := e.gl.MRs[1]
	mr.Title, mr.Draft, mr.DetailedMergeStatus = "issue", false, "mergeable"
	mr.DiffRefs = &gitlab.DiffRefs{BaseSHA: "main-x", StartSHA: "main-x", HeadSHA: "sha-api"}
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateBaseFailing, FailedJobs: []string{"test"}}
	})
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseCIRed || ap.PausedFor != 1 || !ap.ResumeWhenReady {
		t.Fatalf("a BaseFailing MR must pause ciRed: %+v", ap)
	}
	return name
}

// mainGreenAt reports the default branch green at sha.
func (e *projectEnv) mainGreenAt(sha string) {
	e.t.Helper()
	e.gl.MainSHA = sha
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen, SHA: sha})
}

func TestAutopilotRebasesABaseFailingMROnceMainIsGreen(t *testing.T) {
	e, name := baseFailingEnv(t)
	e.mainGreenAt("main-y")
	e.reconcile()
	const line = "▶️ Autopilot continues: `main` is green at main-y: rebasing !1, whose CI failed on jobs that also failed on `main`."
	if c := trackingComments(e); !slices.Equal(e.gl.Rebased, []int64{1}) || !strings.Contains(c, line) {
		t.Fatalf("main green on a new commit must rebase !1 once and say so: rebased %v\n%s", e.gl.Rebased, c)
	}
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[0].HealedOn != "main-y" {
		t.Fatalf("the heal must lift the pause and remember main-y: %+v", ap)
	}
	// drive handed the rebased head to CI in the same poll.
	if run := e.tasks()[name].Status; run.Run.PushedSHA != "sha-api-rebased" || run.Run.ReviewedSHA != "sha-api-rebased" || run.CI != nil ||
		!e.gl.Issues[1].HasLabel("agent::working") {
		t.Fatalf("the rebased head must go to CI: %+v %v", run, e.gl.Labels(1))
	}

	// The rebased head's pipeline is green: the CI loop moves #1 to review
	// and autopilot merges it, without an owner.
	e.gl.AddPipeline("sha-api-rebased", "agent/sha-api", "success")
	for range 3 {
		e.reconcile()
	}
	if !slices.Equal(e.gl.Merged, []int64{1}) || !slices.Equal(e.gl.Rebased, []int64{1}) {
		t.Fatalf("the rebased MR must be merged after one rebase: merged %v, rebased %v, %+v", e.gl.Merged, e.gl.Rebased, e.autopilot())
	}
	if c := trackingComments(e); strings.Count(c, "rebasing !1") != 1 || strings.Count(c, "Autopilot paused") != 1 {
		t.Fatalf("one pause and one heal line expected:\n%s", c)
	}
}

func TestAutopilotHealsOncePerGreenCommit(t *testing.T) {
	e, _ := baseFailingEnv(t)
	e.gl.MRs[1].RebaseInProgress = true // GitLab is still rebasing
	e.mainGreenAt("main-y")
	e.reconcile()
	if ap := e.autopilot(); !slices.Equal(e.gl.Rebased, []int64{1}) || ap.Items[0].RebasedFrom != "sha-api" || ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("expected one rebase in flight: rebased %v, %+v", e.gl.Rebased, ap)
	}
	// Another green commit while the rebase runs: no second rebase.
	e.mainGreenAt("main-z")
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); !slices.Equal(e.gl.Rebased, []int64{1}) || ap.Phase != aiv1.AutopilotRunning || strings.Count(trackingComments(e), "rebasing !1") != 1 {
		t.Fatalf("a rebase in flight must not be requested again: rebased %v, %+v\n%s", e.gl.Rebased, ap, trackingComments(e))
	}
	e.gl.MRs[1].RebaseInProgress = false
	e.gl.AddPipeline("sha-api-rebased", "agent/sha-api", "success")
	for range 4 {
		e.reconcile()
	}
	if !slices.Equal(e.gl.Merged, []int64{1}) || !slices.Equal(e.gl.Rebased, []int64{1}) {
		t.Fatalf("expected !1 merged after one rebase: merged %v, rebased %v, %+v", e.gl.Merged, e.gl.Rebased, e.autopilot())
	}
}

func TestAutopilotHealRebaseConflictGoesToTheAgent(t *testing.T) {
	e, name := baseFailingEnv(t)
	e.gl.RebaseConflict = true
	e.mainGreenAt("main-y")
	e.reconcile()
	if ap := e.autopilot(); !mergeBaseRequested(e, name, "sha-api") || ap.Phase != aiv1.AutopilotRunning || ap.Items[0].RebasedFrom != "" || ap.Items[0].HealedOn != "main-y" {
		t.Fatalf("a conflicting heal rebase must go to the agent: %+v, %+v", e.tasks()[name].Spec.Source, ap)
	}
	e.reconcile()
	if !slices.Equal(e.gl.Rebased, []int64{1}) || strings.Count(trackingComments(e), "rebasing !1") != 1 {
		t.Fatalf("the heal must not repeat: rebased %v\n%s", e.gl.Rebased, trackingComments(e))
	}
}

func TestAutopilotHealRebaseAfterARebaseGoesToTheAgent(t *testing.T) {
	e, name := baseFailingEnv(t)
	e.patchAutopilot(func(ap *aiv1.AutopilotStatus) { ap.Items[0].Rebases = 1 })
	e.mainGreenAt("main-y")
	e.reconcile()
	if !mergeBaseRequested(e, name, "sha-api") || len(e.gl.Rebased) != 0 || e.autopilot().Phase != aiv1.AutopilotRunning {
		t.Fatalf("an MR rebased before must merge the default branch instead: rebased %v, %+v", e.gl.Rebased, e.autopilot())
	}
}

func TestAutopilotDoesNotHeal(t *testing.T) {
	for name, tc := range map[string]func(e *projectEnv, task string){
		"main red": func(e *projectEnv, _ string) {
			e.gl.MainSHA = "main-y"
			e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, SHA: "main-y", IssueIID: 99, FailedJobs: []string{"test"}})
		},
		// Stale main health is not fresh (freshMainHealth); in the fake the
		// watcher re-stamps it before autopilot runs, so this is the case of a
		// health that is not watched.
		"main health not watched": func(e *projectEnv, _ string) {
			e.mainGreenAt("main-y")
			e.setMainHealth(aiv1.MainHealthSpec{Disabled: true})
		},
		"main green without a commit": func(e *projectEnv, _ string) {
			e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen})
		},
		"same base": func(e *projectEnv, _ string) {
			e.mainGreenAt("main-x")
		},
		"healed on it already": func(e *projectEnv, _ string) {
			e.patchAutopilot(func(ap *aiv1.AutopilotStatus) { ap.Items[0].HealedOn = "main-y" })
			e.mainGreenAt("main-y")
		},
		"red on its own": func(e *projectEnv, task string) {
			e.patchStatus(task, func(s *aiv1.AgentTaskStatus) { s.CI.State = aiv1.CIStateFailed })
			e.mainGreenAt("main-y")
		},
		"paused by an owner": func(e *projectEnv, _ string) {
			e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
			e.mainGreenAt("main-y")
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, task := baseFailingEnv(t)
			tc(e, task)
			e.reconcile()
			e.reconcile()
			if len(e.gl.Rebased) != 0 || strings.Contains(trackingComments(e), "rebasing") || e.autopilot().Phase != aiv1.AutopilotPaused {
				t.Fatalf("no heal expected: rebased %v, %+v\n%s", e.gl.Rebased, e.autopilot(), trackingComments(e))
			}
		})
	}
}

func (e *projectEnv) patchAutopilot(mutate func(*aiv1.AutopilotStatus)) {
	e.t.Helper()
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { mutate(s.Autopilot) })
}

// A rebase that fails before GitLab was asked says nothing and is tried again;
// a tracking comment that fails after it neither repeats the rebase nor keeps
// the pause. Either way the tracking issue gets the line once.
func TestAutopilotHealSaysItOnce(t *testing.T) {
	e, _ := baseFailingEnv(t)
	e.gl.DiffErrors = map[int64]error{1: errors.New("gitlab is down")}
	e.mainGreenAt("main-y")
	e.reconcileFailing()
	e.reconcileFailing()
	if ap := e.autopilot(); len(e.gl.Rebased) != 0 || strings.Contains(trackingComments(e), "rebasing") || ap.Items[0].HealedOn != "" || ap.Phase != aiv1.AutopilotPaused {
		t.Fatalf("a failed rebase must say nothing and stay to try again: rebased %v, %+v\n%s", e.gl.Rebased, ap, trackingComments(e))
	}
	e.gl.DiffErrors = nil
	tracking := e.autopilot().TrackingIssueIID
	e.gl.MRs[1].RebaseInProgress = true
	e.gl.NoteErrors = map[int64]error{tracking: errors.New("gitlab is down")}
	e.reconcileFailing()
	if ap := e.autopilot(); !slices.Equal(e.gl.Rebased, []int64{1}) || ap.Items[0].HealedOn != "main-y" || ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("a requested rebase must be recorded and lift the pause, though the comment failed: rebased %v, %+v", e.gl.Rebased, ap)
	}
	e.gl.NoteErrors = nil
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Rebased, []int64{1}) || strings.Contains(trackingComments(e), "rebasing") {
		t.Fatalf("no second rebase or line after a failed comment: rebased %v\n%s", e.gl.Rebased, trackingComments(e))
	}
}

// reconcileFailing polls once where the sync is expected to fail.
func (e *projectEnv) reconcileFailing() {
	_, _ = e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}})
}

// An owner's "/agent rebase #1" on the tracking issue rebases the active MR
// and lifts its ciRed pause, so the rebased head goes to CI; other targets
// and a rebase in flight are answered with the reason.
func TestAutopilotRebaseCommand(t *testing.T) {
	e, name := baseFailingEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	jonathan := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddNote(tracking, jonathan, "/agent rebase #2")
	e.reconcile()
	if c := trackingComments(e); len(e.gl.Rebased) != 0 || !strings.Contains(c, "`rebase` needs the active issue with a merge request, e.g. `/agent rebase #12`: #2 is not the active issue of the queue.") {
		t.Fatalf("a pending issue is refused with the reason: rebased %v\n%s", e.gl.Rebased, c)
	}
	e.gl.MRs[1].RebaseInProgress = true
	e.gl.AddNote(tracking, jonathan, "/agent rebase #1")
	e.reconcile()
	if c := trackingComments(e); !slices.Equal(e.gl.Rebased, []int64{1}) || !strings.Contains(c, "▶️ Autopilot continues: @jonathan asked to rebase !1 onto `main`.") {
		t.Fatalf("the active MR is rebased and the pause ends: rebased %v\n%s", e.gl.Rebased, c)
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.Items[0].RebasedFrom != "sha-api" {
		t.Fatalf("the rebase is in flight, autopilot runs: %+v", ap)
	}
	e.gl.AddNote(tracking, jonathan, "/agent mergebase #1")
	e.reconcile()
	if c := trackingComments(e); mergeBaseRequested(e, name, "sha-api") || !strings.Contains(c, "`mergeBase` needs the active issue with a merge request, e.g. `/agent mergeBase #12`: a rebase of !1 is in progress.") {
		t.Fatalf("a rebase in flight refuses mergeBase: %+v\n%s", e.tasks()[name].Spec.Source, c)
	}
	// GitLab is done: the rebased head goes to CI.
	e.gl.MRs[1].RebaseInProgress = false
	e.reconcile()
	if run := e.tasks()[name].Status; run.Run.PushedSHA != "sha-api-rebased" || run.CI != nil || !e.gl.Issues[1].HasLabel("agent::working") {
		t.Fatalf("the rebased head must go to CI: %+v %v", run, e.gl.Labels(1))
	}
}

// "/agent mergeBase #1" hands the active MR to the agent, which merges the
// default branch, and lifts the pause; once per head.
func TestAutopilotMergeBaseCommand(t *testing.T) {
	e, name := baseFailingEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, gitlab.User{ID: 1, Username: "jonathan"}, "/agent mergeBase #1")
	e.reconcile()
	if c := trackingComments(e); !mergeBaseRequested(e, name, "sha-api") || len(e.gl.Rebased) != 0 ||
		!strings.Contains(c, "▶️ Autopilot continues: @jonathan asked the agent to merge `main` into !1.") {
		t.Fatalf("the agent merges the default branch: %+v, rebased %v\n%s", e.tasks()[name].Spec.Source, e.gl.Rebased, c)
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("the pause ends: %+v", ap)
	}
}

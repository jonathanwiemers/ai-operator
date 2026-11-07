package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// withCompanion makes group/infra a companion target of the project and
// routes the env's GitLab by project: group/app stays e.gl, group/infra is
// the returned fake.
func withCompanion(e *projectEnv) *gitlabfake.Fake {
	e.t.Helper()
	withInfraTarget(e, false)
	projects := gitlabfake.NewProjects(e.gl)
	infra := projects.Add("group/infra")
	e.r.NewGitLab = func(string, string) gitlab.API { return projects }
	return infra
}

// companionPushed records a push of the companion head sha by the runner,
// with its merge request !mrIID (0: none), reviewed at sha or not.
func (e *projectEnv) companionPushed(name, sha string, mrIID int64, reviewed bool) {
	e.t.Helper()
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		c := &aiv1.CompanionRun{Project: "group/infra", MRIID: mrIID, Branch: "agent/app-42-add-paypal-button", PushedSHA: sha}
		if reviewed {
			c.ReviewedSHA = sha
		}
		s.Run.Companion = c
	})
}

func (e *projectEnv) companionCIState() aiv1.CIState {
	e.t.Helper()
	if ci := e.ciTask().Status.CI; ci != nil && ci.Companion != nil {
		return ci.Companion.State
	}
	return ""
}

// The loop waits for both trees, and a green companion with a green home
// moves the issue to review.
func TestCICompanionGreenWithHome(t *testing.T) {
	e := newCIEnv(t, "sha1")
	infra := withCompanion(e)
	e.companionPushed(ciTask, "isha1", 0, true)
	e.gl.AddPipeline("sha1", "agent/42", "success")
	ipid := infra.AddPipeline("isha1", "agent/app-42-add-paypal-button", "running")
	e.reconcile()
	if e.ciState() != aiv1.CIStateSuccess || e.companionCIState() != aiv1.CIStateRunning {
		t.Fatalf("home green, companion running: %+v", e.ciTask().Status.CI)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("the issue waits in working for the companion's pipeline, has %v", labels)
	}
	infra.SetPipelineStatus(ipid, "success")
	e.reconcile()
	if e.companionCIState() != aiv1.CIStateSuccess || e.ciTask().Status.CI.Companion.PipelineID != ipid {
		t.Fatalf("the companion's pipeline is recorded green: %+v", e.ciTask().Status.CI.Companion)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("both green: review, has %v", labels)
	}
}

// A red companion asks for a fix run of the companion tree; a red home is
// fixed first; both share the fix rounds.
func TestCICompanionRedSharesTheFixRounds(t *testing.T) {
	e := newCIEnv(t, "sha1")
	infra := withCompanion(e)
	e.companionPushed(ciTask, "isha1", 0, true)
	e.gl.AddPipeline("sha1", "agent/42", "success")
	ipid := infra.AddPipeline("isha1", "agent/app-42-add-paypal-button", "failed", failedTest())

	// Red: retried once in the companion project, then a fix run for it.
	e.reconcile()
	if !slices.Equal(infra.Retried, []int64{ipid}) || len(e.gl.Retried) != 0 || e.ciTask().Spec.RunRequest != 0 {
		t.Fatalf("the companion's failed jobs are retried first: infra %v, home %v", infra.Retried, e.gl.Retried)
	}
	infra.SetPipelineStatus(ipid, "failed")
	e.reconcile()
	task := e.ciTask()
	if src := task.Spec.Source; task.Spec.RunRequest != 1 || src.CIFixPipelineID != ipid || src.CIFixProject != "group/infra" {
		t.Fatalf("expected a fix run of the companion's pipeline: %+v", src)
	}
	if e.ciState() != aiv1.CIStateSuccess || e.companionCIState() != aiv1.CIStateFixing {
		t.Fatalf("home stays green, the companion is being fixed: %+v", task.Status.CI)
	}

	// Round 1 pushed both trees; both are red: home is fixed first.
	e.agentPushed("sha2", 1, ipid)
	e.companionPushed(ciTask, "isha2", 0, true)
	hpid := e.gl.AddPipeline("sha2", "agent/42", "failed", failedTest())
	ipid2 := infra.AddPipeline("isha2", "agent/app-42-add-paypal-button", "failed", failedTest())
	e.reconcile() // both retried
	e.gl.SetPipelineStatus(hpid, "failed")
	infra.SetPipelineStatus(ipid2, "failed")
	e.reconcile()
	task = e.ciTask()
	if src := task.Spec.Source; task.Spec.RunRequest != 2 || src.CIFixPipelineID != hpid || src.CIFixProject != "" {
		t.Fatalf("home is fixed first: %+v", src)
	}

	// Round 2 fixed home; the companion is still red and no round is left.
	e.agentPushed("sha3", 2, hpid)
	e.gl.AddPipeline("sha3", "agent/42", "success")
	e.reconcile()
	if task := e.ciTask(); task.Spec.RunRequest != 2 {
		t.Fatalf("the rounds are shared, so no third fix run: %+v", task.Spec.Source)
	}
	if e.companionCIState() != aiv1.CIStateFailed || e.ciState() != aiv1.CIStateSuccess {
		t.Fatalf("the companion ends failed: %+v", e.ciTask().Status.CI)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::needs-input"}) {
		t.Fatalf("expected needs-input, has %v", labels)
	}
	if n := e.notes(); !strings.Contains(n, "CI of the companion repository group/infra is still red after 2 fix attempts") {
		t.Fatalf("the note names the companion:\n%s", n)
	}
}

// A companion fix run that pushed nothing new gives up.
func TestCICompanionFixWithoutNewCommitGivesUp(t *testing.T) {
	e := newCIEnv(t, "sha1")
	infra := withCompanion(e)
	e.companionPushed(ciTask, "isha1", 0, true)
	e.gl.AddPipeline("sha1", "agent/42", "success")
	ipid := infra.AddPipeline("isha1", "agent/app-42-add-paypal-button", "failed", failedTest())
	e.reconcile() // retry
	infra.SetPipelineStatus(ipid, "failed")
	e.reconcile() // fix requested
	e.agentPushed("sha1", 1, ipid)
	e.reconcile()
	if e.companionCIState() != aiv1.CIStateFailed || !strings.Contains(e.notes(), "could not fix the failing pipeline of the companion repository group/infra") {
		t.Fatalf("expected the companion to fail: %s, %q", e.companionCIState(), e.notes())
	}
}

// A canceled companion pipeline goes to review with a note; a retry in
// review is followed.
func TestCICompanionCanceledThenRetried(t *testing.T) {
	e := newCIEnv(t, "sha1")
	infra := withCompanion(e)
	cmr, _ := infra.CreateMergeRequest(e.ctx, "group/infra", gitlab.CreateMergeRequestOptions{SourceBranch: "agent/app-42-add-paypal-button", Title: "Draft: x"})
	infra.MRs[cmr.IID].SHA = "isha1"
	e.companionPushed(ciTask, "isha1", cmr.IID, true)
	e.gl.AddPipeline("sha1", "agent/42", "success")
	ipid := infra.AddPipeline("isha1", "agent/app-42-add-paypal-button", "canceled")
	infra.MRs[cmr.IID].HeadPipeline = &gitlab.Pipeline{ID: ipid}
	e.reconcile()
	if e.companionCIState() != aiv1.CIStateCanceled || !strings.Contains(e.notes(), "of the companion repository group/infra was canceled") {
		t.Fatalf("expected a canceled companion with a note: %s %q", e.companionCIState(), e.notes())
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("canceled: review, has %v", labels)
	}
	// Retried by hand: the new head pipeline is followed, and green recorded.
	ipid2 := infra.AddPipeline("isha1", "agent/app-42-add-paypal-button", "success")
	infra.MRs[cmr.IID].HeadPipeline = &gitlab.Pipeline{ID: ipid2}
	e.reconcile()
	if cc := e.ciTask().Status.CI.Companion; cc.State != aiv1.CIStateSuccess || cc.PipelineID != ipid2 {
		t.Fatalf("the retried companion pipeline is recorded: %+v", cc)
	}
}

// finishCompanionRun is finishRun with a companion: its draft merge request
// in infra at csha, reviewed there, and its pipeline in state ci.
func (e *projectEnv) finishCompanionRun(infra *gitlabfake.Fake, iid int64, sha, csha string, ci aiv1.CIState, mutate func(*aiv1.RunStatus)) (string, *gitlab.MergeRequest) {
	e.t.Helper()
	cmr, err := infra.CreateMergeRequest(e.ctx, "group/infra", gitlab.CreateMergeRequestOptions{
		SourceBranch: "agent/app-1-api", TargetBranch: "main", Title: "Draft: API (companion of group/app#1)"})
	if err != nil {
		e.t.Fatal(err)
	}
	infra.MRs[cmr.IID].SHA = csha
	name := e.finishRun(iid, sha, func(r *aiv1.RunStatus) {
		r.Companion = &aiv1.CompanionRun{Project: "group/infra", MRIID: cmr.IID, Branch: "agent/app-1-api", PushedSHA: csha, ReviewedSHA: csha}
		if mutate != nil {
			mutate(r)
		}
	})
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.CI.Companion = &aiv1.CompanionCI{SHA: csha, State: ci}
	})
	return name, infra.MRs[cmr.IID]
}

func newCompanionGateEnv(t *testing.T) (*projectEnv, *gitlabfake.Fake) {
	t.Helper()
	e := newAutopilotEnv(t)
	infra := withCompanion(e)
	e.reconcile()
	e.reconcile()
	return e, infra
}

func expectPause(t *testing.T, e *projectEnv, kind aiv1.PauseKind, reason string) {
	t.Helper()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != kind || !strings.Contains(ap.PausedReason, reason) || len(e.gl.Merged) != 0 {
		t.Fatalf("expected a %s pause with %q and no merge: %+v, merged %v", kind, reason, ap, e.gl.Merged)
	}
}

// A green, reviewed companion that stays a draft lets the home merge go on;
// the companion itself is not merged.
func TestCompanionGateDraftAndGreenMerges(t *testing.T) {
	e, infra := newCompanionGateEnv(t)
	_, cmr := e.finishCompanionRun(infra, 1, "sha-api", "sha-infra", aiv1.CIStateSuccess, nil)
	e.reconcile() // marked ready
	e.reconcile() // merged
	if ap := e.autopilot(); !slices.Equal(e.gl.Merged, []int64{1}) || ap.Phase == aiv1.AutopilotPaused {
		t.Fatalf("the home MR merges: merged %v, %+v", e.gl.Merged, ap)
	}
	if len(infra.Merged) != 0 || !cmr.Draft || len(infra.Rebased) != 0 {
		t.Fatalf("the companion stays a draft and is not merged: merged %v, %+v", infra.Merged, cmr)
	}
}

func TestCompanionGateHoldsTheHomeMerge(t *testing.T) {
	cases := []struct {
		name   string
		ci     aiv1.CIState
		run    func(*aiv1.RunStatus)
		mr     func(*gitlab.MergeRequest)
		labels []string
		kind   aiv1.PauseKind
		reason string
	}{
		{name: "unreviewed", ci: aiv1.CIStateSuccess, run: func(r *aiv1.RunStatus) { r.Companion.ReviewedSHA = "sha-infra-0" },
			kind: aiv1.PauseNotReviewed, reason: "the companion head of #1 (group/infra, sha-infr) was not reviewed"},
		{name: "red after the fix rounds", ci: aiv1.CIStateFailed, labels: []string{"ai-generated", "agent::needs-input"},
			kind: aiv1.PauseCIRed, reason: "CI of the companion group/infra!1 is still red"},
		{name: "red in review", ci: aiv1.CIStateFailed, kind: aiv1.PauseCIRed, reason: "CI of the companion group/infra!1 is red"},
		{name: "canceled", ci: aiv1.CIStateCanceled, kind: aiv1.PauseCICanceled, reason: "the pipeline of the companion group/infra!1 was canceled"},
		{name: "closed", ci: aiv1.CIStateSuccess, mr: func(m *gitlab.MergeRequest) { m.State = "closed" },
			kind: aiv1.PauseCompanion, reason: "the companion merge request group/infra!1 of #1 was closed without a merge"},
		{name: "approval", ci: aiv1.CIStateSuccess, mr: func(m *gitlab.MergeRequest) { m.DetailedMergeStatus = "not_approved" },
			kind: aiv1.PauseCompanion, reason: "group/infra!1 needs an approval"},
		{name: "someone pushed", ci: aiv1.CIStateSuccess, mr: func(m *gitlab.MergeRequest) { m.SHA = "sha-other" },
			kind: aiv1.PauseCompanion, reason: "someone pushed to its branch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, infra := newCompanionGateEnv(t)
			_, cmr := e.finishCompanionRun(infra, 1, "sha-api", "sha-infra", c.ci, c.run)
			if c.mr != nil {
				c.mr(cmr)
			}
			if c.labels != nil {
				e.gl.SetLabels(1, c.labels...)
			}
			e.reconcile()
			e.reconcile()
			expectPause(t, e, c.kind, c.reason)
			comments := trackingComments(e)
			for _, x := range aiv1.PauseExits(c.kind) {
				if !strings.Contains(comments, ": "+x.Does+".\n") {
					t.Errorf("the pause comment lacks the exit %s:\n%s", x.ID, comments)
				}
			}
		})
	}
}

// A conflicting companion is rebased once; still conflicting, autopilot
// pauses with kind companion, and continues by itself once it is fixed.
func TestCompanionGateConflictRebasesOnceThenPauses(t *testing.T) {
	e, infra := newCompanionGateEnv(t)
	name, cmr := e.finishCompanionRun(infra, 1, "sha-api", "sha-infra", aiv1.CIStateSuccess, nil)
	cmr.DetailedMergeStatus = "conflict"
	infra.RebaseConflict = true
	e.reconcile()
	if !slices.Equal(infra.Rebased, []int64{cmr.IID}) {
		t.Fatalf("the companion is rebased once: %v", infra.Rebased)
	}
	if rb := e.tasks()[name].Spec.Source.CompanionRebase; rb == nil || rb.From != "sha-infra" || rb.Diff == "" {
		t.Fatalf("the rebase is recorded for the companion head: %+v", rb)
	}
	e.reconcile()
	expectPause(t, e, aiv1.PauseCompanion, "a rebase did not resolve them")
	e.reconcile()
	if len(infra.Rebased) != 1 {
		t.Fatalf("no second rebase: %v", infra.Rebased)
	}

	// Resolved by hand: autopilot goes on by itself and merges.
	cmr.DetailedMergeStatus = "draft_status"
	e.reconcile()
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotPaused {
		t.Fatalf("a mergeable companion ends the pause: %+v", ap)
	}
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("the home MR merges once the companion can follow: %v, %+v", e.gl.Merged, e.autopilot())
	}
}

// A companion that needs a rebase is rebased; the rebased head keeps its
// review (same change) and is checked by CI before the home merge.
func TestCompanionGateRebaseKeepsTheReview(t *testing.T) {
	e, infra := newCompanionGateEnv(t)
	name, cmr := e.finishCompanionRun(infra, 1, "sha-api", "sha-infra", aiv1.CIStateSuccess, nil)
	cmr.DetailedMergeStatus = "need_rebase"
	e.reconcile() // rebased
	cmr.DetailedMergeStatus = "draft_status"
	e.reconcile() // the new head is taken over
	c := e.tasks()[name].Status.Run.Companion
	if c.PushedSHA != "sha-infra-rebased" || c.ReviewedSHA != "sha-infra-rebased" {
		t.Fatalf("the rebased companion head keeps its review: %+v", c)
	}
	if len(e.gl.Merged) != 0 {
		t.Fatal("no home merge before CI checked the rebased companion")
	}
	e.reconcile() // back to working for CI
	if !e.gl.Issues[1].HasLabel("agent::working") {
		t.Fatalf("the CI loop follows the new companion head: %v", e.gl.Labels(1))
	}
	infra.AddPipeline("sha-infra-rebased", "agent/app-1-api", "success")
	e.reconcile() // CI green: review
	e.reconcile() // ready
	e.reconcile() // merged
	if !slices.Equal(e.gl.Merged, []int64{1}) || len(infra.Rebased) != 1 {
		t.Fatalf("the home MR merges after the companion's new pipeline: merged %v, rebased %v, %+v", e.gl.Merged, infra.Rebased, e.autopilot())
	}
}

// An owner's merge override skips the review checks of both trees, but not
// the companion's CI.
func TestCompanionGateMergeOverrideKeepsCompanionCI(t *testing.T) {
	e, infra := newCompanionGateEnv(t)
	e.gl.Users = []gitlab.User{alice}
	e.gl.Milestones[0].Description = "/agent owners @alice\n" + e.gl.Milestones[0].Description
	name, _ := e.finishCompanionRun(infra, 1, "sha-api", "sha-infra", aiv1.CIStateFailed, func(r *aiv1.RunStatus) {
		r.ReviewedSHA, r.Companion.ReviewedSHA = "older", "older"
	})
	e.reconcile()
	expectPause(t, e, aiv1.PauseNotReviewed, "was not reviewed")
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent merge #1 sha-api")
	e.reconcile()
	e.reconcile()
	expectPause(t, e, aiv1.PauseCIRed, "CI of the companion group/infra!1 is red")

	// Green: the pause ends by itself and the head merges unreviewed.
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.CI.Companion.State = aiv1.CIStateSuccess })
	for range 3 {
		e.reconcile()
	}
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("the override merges once the companion is green: %v, %+v", e.gl.Merged, e.autopilot())
	}
}

// The home merge waits for the review of the companion head, whatever
// conventionalCommits says (the merge call then also builds the squash
// message): nothing is merged, no merge options are sent, and the merge goes
// through once the companion head is reviewed.
func TestAutopilotCompanionReviewGate(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		name string
		opt  *bool
		want string
	}{
		{"on", &on, "feat(ui): add x\n\nRefs #1"},
		{"off", &off, ""},
		{"unset", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, infra := newCompanionGateEnv(t)
			e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Workflow.ConventionalCommits = c.opt })
			name, cmr := e.finishCompanionRun(infra, 1, "sha-api", "sha-infra-2", aiv1.CIStateSuccess, func(r *aiv1.RunStatus) {
				r.Companion.ReviewedSHA = "sha-infra-1"
			})
			cmr.DetailedMergeStatus = "draft_status"
			e.gl.MRs[1].Title = "Draft: feat(ui): add x"
			for range 4 {
				e.reconcile()
			}
			if _, ok := e.gl.MergeOpts[1]; ok {
				t.Fatalf("merge was called for an unreviewed companion: %+v", e.gl.MergeOpts[1])
			}
			expectPause(t, e, aiv1.PauseNotReviewed, "the companion head of #1 (group/infra, sha-infr) was not reviewed")
			if len(infra.Merged) != 0 {
				t.Fatalf("the companion was merged: %v", infra.Merged)
			}

			e.patchStatus(name, func(s *aiv1.AgentTaskStatus) { s.Run.Companion.ReviewedSHA = "sha-infra-2" })
			for range 4 {
				e.reconcile()
			}
			if !slices.Equal(e.gl.Merged, []int64{1}) {
				t.Fatalf("the home MR merges once the companion is reviewed: merged %v, %+v", e.gl.Merged, e.autopilot())
			}
			if got := e.gl.MergeOpts[1].SquashCommitMessage; got != c.want {
				t.Fatalf("squash message = %q, want %q", got, c.want)
			}
		})
	}
}

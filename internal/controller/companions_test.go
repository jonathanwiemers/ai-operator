package controller

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// landingEnv is a companion entry of group/app#1 (merged in !1 as m1, in
// milestone %3 "Checkout v2", ID 11) for its draft merge request in
// group/infra at c1. group/infra is a valid target, without autoMerge, and
// the milestone is on autopilot with the line "/agent repo group/infra
// merge"; the entry has no autoMerge (autoMerge sets all three switches).
type landingEnv struct {
	t           *testing.T
	ctx         context.Context
	r           *AgentProjectReconciler
	gl          *gitlabfake.Projects
	home, infra *gitlabfake.Fake
	cmr         *gitlab.MergeRequest
	spec        aiv1.AgentProjectSpec
	e           aiv1.CompanionStatus
	// refusals counts the merge/push guard's refusals.
	refusals int
}

const landingMilestone = "/agent autopilot\n/agent repo group/infra merge\nBuild a new checkout."

func newLandingEnv(t *testing.T) *landingEnv {
	t.Helper()
	ctx := context.Background()
	home := gitlabfake.New()
	gl := gitlabfake.NewProjects(home)
	infra := gl.Add("group/infra")
	spec := testProject().Spec
	spec.CrossRepo = &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{{Project: "group/infra", Paths: []string{"clusters/app/"}}}}
	home.Milestones = []gitlab.Milestone{{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: landingMilestone}}
	l := &landingEnv{t: t, ctx: ctx, gl: gl, home: home, infra: infra, spec: spec.WithDefaults()}
	l.r = &AgentProjectReconciler{Now: func() time.Time { return home.Clock }, CrossRepoAllowed: []string{"group/infra"}}
	mr, err := infra.CreateMergeRequest(ctx, "group/infra", gitlab.CreateMergeRequestOptions{
		SourceBranch: "agent/app-1-api", TargetBranch: "main", Title: "Draft: API (companion of group/app#1)"})
	if err != nil {
		t.Fatal(err)
	}
	l.cmr = infra.MRs[mr.IID]
	l.cmr.SHA = "c1"
	diff, err := diffHash(ctx, gl, "group/infra", mr.IID)
	if err != nil || diff == "" {
		t.Fatalf("hashing the companion's change: %q %v", diff, err)
	}
	now := metav1.NewTime(home.Clock)
	l.e = aiv1.CompanionStatus{HomeIssueIID: 1, MilestoneIID: 3, MilestoneID: 11, HomeMRIID: 1, HomeMergeSHA: "m1",
		Project: "group/infra", MRIID: mr.IID, HeadSHA: "c1", HeadSince: &now, Diff: diff, State: aiv1.LandingWaitingRelease}
	return l
}

// api is the env's GitLab behind the merge/push guard of the current spec,
// like the operator's client; its refusals are counted.
func (l *landingEnv) api() gitlab.API {
	logger := funcr.New(func(_, args string) {
		if strings.Contains(args, "guard refused") {
			l.refusals++
		}
	}, funcr.Options{})
	return gitlab.Guard(l.gl, gitlab.NewPolicy(l.spec, l.r.CrossRepoAllowed), logger)
}

func (l *landingEnv) poll() {
	l.t.Helper()
	if err := l.r.landCompanion(l.ctx, l.api(), l.spec, &l.e, []string{"alice"}); err != nil {
		l.t.Fatal(err)
	}
}

// autoMerge sets all three of autopilot's switches: the entry's autoMerge,
// and the target's (the milestone has the other two).
func (l *landingEnv) autoMerge() {
	l.e.AutoMerge = true
	l.spec.CrossRepo.Targets[0].AutoMerge = true
}

// milestone sets the milestone's description.
func (l *landingEnv) milestone(description string) { l.home.Milestones[0].Description = description }

// notes are the comments on the companion merge request.
func (l *landingEnv) notes() []gitlab.Note { return l.infra.MRNotes[l.cmr.IID] }

// tag tags the home project with name at a commit with parents parents,
// age old, containing the merge m1 or not.
func (l *landingEnv) tag(name string, parents int, age time.Duration, containsMerge bool) {
	created := l.home.Clock.Add(-age)
	ids := []string{"p1", "p2", "p3"}[:parents]
	var contains []string
	if containsMerge {
		contains = []string{"m1"}
	}
	l.home.AddTag(name, gitlab.TagCommit{ID: "t-" + name, ParentIDs: ids, CreatedAt: &created}, contains...)
}

func (l *landingEnv) self(version string) {
	l.r.SelfProject, l.r.Version = "group/app", version
}

func (l *landingEnv) green(sha string) { l.infra.AddPipeline(sha, "agent/app-1-api", "success") }

type landingStep struct {
	do     func(*landingEnv)
	want   aiv1.LandingState
	reason string
}

func TestCompanionLanding(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name  string
		setup func(*landingEnv)
		steps []landingStep
		// Afterwards: the release that made it live, the rebases asked for,
		// and the home project's tag lookups (ListCommitRefs).
		release string
		rebases int
		refs    int
	}{
		{name: "self project, version below the first tag", setup: func(l *landingEnv) {
			l.self("2026.11.2")
			l.tag("2026.11.3", 2, day, true)
			l.green("c1")
		}, steps: []landingStep{{want: aiv1.LandingWaitingRelease, reason: "the operator runs 2026.11.2; the merge m1 is first in 2026.11.3"}}, refs: 1},
		{name: "self project, version equal to the first tag", setup: func(l *landingEnv) {
			l.self("2026.11.3")
			l.tag("2026.11.3", 2, day, true)
			l.tag("2026.11.10", 2, 0, true) // newer, and lexically smaller
			l.green("c1")
		}, steps: []landingStep{{want: aiv1.LandingReady}}, release: "2026.11.3", refs: 1},
		{name: "self project, version updated to the release", setup: func(l *landingEnv) {
			l.self("2026.9.12")
			l.green("c1")
		}, steps: []landingStep{
			{want: aiv1.LandingWaitingRelease, reason: "no release contains the merge m1 yet"},
			{do: func(l *landingEnv) { l.tag("2026.10.1", 2, 0, true) }, want: aiv1.LandingWaitingRelease, reason: "the operator runs 2026.9.12"},
			{do: func(l *landingEnv) { l.r.Version = "2026.10.1" }, want: aiv1.LandingReady},
		}, release: "2026.10.1", refs: 3},
		{name: "self project, version dev", setup: func(l *landingEnv) {
			l.self("dev")
			l.tag("2026.11.3", 2, day, true)
			l.green("c1")
		}, steps: []landingStep{{want: aiv1.LandingWaitingRelease, reason: `the running operator's version "dev" is no release (YEAR.MONTH.N)`}}},
		{name: "tagging project without a containing tag", setup: func(l *landingEnv) {
			l.tag("v1.2.0", 2, 3*day, false)
			l.green("c1")
		}, steps: []landingStep{
			{want: aiv1.LandingWaitingRelease, reason: "group/app tags its default branch, and no tag contains the merge m1 yet"},
			{do: func(l *landingEnv) { l.tag("v1.3.0", 2, 0, true) }, want: aiv1.LandingReady},
		}, release: "v1.3.0", refs: 2},
		{name: "tags older than 30 days: merged is enough", setup: func(l *landingEnv) {
			l.tag("v1.2.0", 2, 31*day, false)
			l.green("c1")
		}, steps: []landingStep{{want: aiv1.LandingReady}}},
		{name: "tags on single-parent commits: merged is enough", setup: func(l *landingEnv) {
			l.tag("v1.2.0", 1, day, false)
			l.green("c1")
		}, steps: []landingStep{{want: aiv1.LandingReady}}},
		{name: "a project without tags: merged is enough", setup: func(l *landingEnv) { l.green("c1") },
			steps: []landingStep{{want: aiv1.LandingReady}}},
		{name: "docs-only is live at merge", setup: func(l *landingEnv) {
			l.self("dev")
			l.e.DocsOnly = true
			l.green("c1")
		}, steps: []landingStep{{want: aiv1.LandingReady}}},
		{name: "need_rebase: one rebase, then CI on the rebased head", setup: func(l *landingEnv) {
			l.green("c1")
			l.cmr.DetailedMergeStatus = "need_rebase"
		}, steps: []landingStep{
			{want: aiv1.LandingRebasing, reason: "GitLab rebases group/infra!1"},
			{want: aiv1.LandingWaitingCI, reason: "waiting for a pipeline on the head c1-rebas"},
			{do: func(l *landingEnv) { l.green("c1-rebased") }, want: aiv1.LandingReady},
		}, rebases: 1},
		{name: "need_rebase after the one rebase blocks", setup: func(l *landingEnv) {
			l.cmr.DetailedMergeStatus = "need_rebase"
		}, steps: []landingStep{
			{want: aiv1.LandingRebasing},
			{do: func(l *landingEnv) { l.cmr.DetailedMergeStatus = "need_rebase" }, want: aiv1.LandingBlocked, reason: "still needs a rebase after one"},
			{want: aiv1.LandingBlocked, reason: "still needs a rebase after one"},
		}, rebases: 1},
		{name: "a conflict the rebase does not resolve blocks", setup: func(l *landingEnv) {
			l.cmr.DetailedMergeStatus = "conflict"
			l.infra.RebaseConflict = true
		}, steps: []landingStep{
			{want: aiv1.LandingRebasing},
			{want: aiv1.LandingBlocked, reason: "has merge conflicts with its default branch, and the one rebase did not resolve them"},
			// Resolved by hand on the branch, same change: it goes on.
			{do: func(l *landingEnv) {
				l.cmr.SHA, l.cmr.DetailedMergeStatus = "c2", "draft_status"
				l.green("c2")
			}, want: aiv1.LandingReady},
		}, rebases: 1},
		{name: "a failed rebase blocks", setup: func(l *landingEnv) {
			l.cmr.DetailedMergeStatus = "need_rebase"
		}, steps: []landingStep{
			{want: aiv1.LandingRebasing},
			{do: func(l *landingEnv) {
				l.cmr.SHA, l.cmr.MergeError, l.cmr.DetailedMergeStatus = "c1", "Rebase failed: conflict", "need_rebase"
			}, want: aiv1.LandingBlocked,
				reason: "the rebase of the companion merge request group/infra!1 failed: Rebase failed"},
		}, rebases: 1},
		{name: "someone pushed another change", setup: func(l *landingEnv) {
			l.cmr.SHA = "c9"
			l.infra.MRDiffs = map[int64][]gitlab.Diff{1: {{OldPath: "x.yaml", NewPath: "x.yaml", Diff: "+other"}}}
			l.green("c9")
		}, steps: []landingStep{{want: aiv1.LandingBlocked, reason: "does not carry the change it had at the home merge"}}},
		{name: "a red pipeline blocks until it is green", setup: func(l *landingEnv) {
			l.infra.AddPipeline("c1", "agent/app-1-api", "failed")
		}, steps: []landingStep{
			{want: aiv1.LandingBlocked, reason: "is failed on its head c1"},
			{do: func(l *landingEnv) { l.green("c1") }, want: aiv1.LandingReady},
		}},
		{name: "a running pipeline waits", setup: func(l *landingEnv) {
			l.infra.AddPipeline("c1", "agent/app-1-api", "running")
		}, steps: []landingStep{{want: aiv1.LandingWaitingCI, reason: "is running"}}},
		{name: "no pipeline within the timeout is no CI", steps: []landingStep{
			{want: aiv1.LandingWaitingCI, reason: "waiting for a pipeline"},
			{do: func(l *landingEnv) { l.home.Clock = l.home.Clock.Add(11 * time.Minute) }, want: aiv1.LandingReady},
		}},
		{name: "an approval blocks", setup: func(l *landingEnv) {
			l.green("c1")
			l.cmr.DetailedMergeStatus = "not_approved"
		}, steps: []landingStep{{want: aiv1.LandingBlocked, reason: "needs an approval"}}},
		{name: "merged by hand while waiting for the release", setup: func(l *landingEnv) {
			l.self("dev")
		}, steps: []landingStep{
			{want: aiv1.LandingWaitingRelease},
			{do: func(l *landingEnv) { l.cmr.State, l.cmr.MergeCommitSHA = "merged", "cm1" }, want: aiv1.LandingMerged},
		}, refs: 0},
		{name: "closed by hand once ready", setup: func(l *landingEnv) { l.green("c1") }, steps: []landingStep{
			{want: aiv1.LandingReady},
			// Ready is the end state here: a later conflict does not move it.
			{do: func(l *landingEnv) { l.cmr.DetailedMergeStatus = "conflict" }, want: aiv1.LandingReady},
			{do: func(l *landingEnv) { l.cmr.State = "closed" }, want: aiv1.LandingClosed, reason: "closed without a merge"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newLandingEnv(t)
			if c.setup != nil {
				c.setup(l)
			}
			for i, s := range c.steps {
				if s.do != nil {
					s.do(l)
				}
				l.poll()
				if l.e.State != s.want || !strings.Contains(l.e.Reason, s.reason) {
					t.Fatalf("poll %d: want %s with %q, got %s with %q", i+1, s.want, s.reason, l.e.State, l.e.Reason)
				}
			}
			if l.e.Release != c.release {
				t.Errorf("release %q, want %q", l.e.Release, c.release)
			}
			if len(l.infra.Rebased) != c.rebases {
				t.Errorf("rebases %v, want %d", l.infra.Rebased, c.rebases)
			}
			if len(l.home.RefLists) != c.refs {
				t.Errorf("tag lookups %v, want %d", l.home.RefLists, c.refs)
			}
			if len(l.infra.Merged) != 0 || len(l.home.Merged) != 0 || l.refusals != 0 {
				t.Errorf("nothing is merged, and the guard is not hit: %v %v, %d refusals", l.infra.Merged, l.home.Merged, l.refusals)
			}
			switch l.e.State {
			case aiv1.LandingReady:
				// Without autopilot's switches: marked ready, the owners asked.
				if l.e.ReadyAt == nil || l.e.NotifiedAt == nil || l.cmr.Draft || len(l.notes()) != 1 {
					t.Errorf("ready without readyAt, or the owners not asked: %+v, draft %v, notes %+v", l.e, l.cmr.Draft, l.notes())
				}
			case aiv1.LandingMerged, aiv1.LandingClosed:
				if l.e.EndedAt == nil {
					t.Error("ended without endedAt")
				}
				if l.e.State == aiv1.LandingMerged && l.e.MergeSHA != "cm1" {
					t.Errorf("merge SHA %q", l.e.MergeSHA)
				}
			}
		})
	}
}

// Who merges a ready companion: autopilot with all three switches (read
// again when it is about to merge), else the owners, asked once and
// reminded once. The guard is never hit.
func TestCompanionWhoMerges(t *testing.T) {
	day := 24 * time.Hour
	merged := func(l *landingEnv) { l.cmr.State, l.cmr.MergeCommitSHA = "merged", "cm1" }
	mainPipeline := func(status string) func(*landingEnv) {
		return func(l *landingEnv) { l.infra.AddPipeline("merge-c1", "main", status) }
	}
	headPipeline := func(status string) func(*landingEnv) {
		return func(l *landingEnv) { l.infra.AddPipeline("c1", "agent/app-1-api", status) }
	}
	later := func(d time.Duration) func(*landingEnv) {
		return func(l *landingEnv) { l.home.Clock = l.home.Clock.Add(d) }
	}
	line := func(description string) func(*landingEnv) { return func(l *landingEnv) { l.milestone(description) } }
	cases := []struct {
		name  string
		setup func(*landingEnv)
		// noCI: the head has no pipeline (else a green one).
		noCI  bool
		steps []landingStep
		// Afterwards: merged or not, the comments on the companion merge
		// request (by substring, each mentions the owners), the merge
		// request pipelines started, the merge request still a draft, the
		// guard's refusals.
		merged    bool
		notes     []string
		pipelines int
		draft     bool
		refusals  int
	}{
		{name: "all switches: merged once the default branch is green", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging, reason: "autopilot merges group/infra!1"}, // marked ready
			{want: aiv1.LandingMerging, reason: "autopilot merged group/infra!1; waiting for the pipeline of merge-c1 on main"},
			{do: mainPipeline("running"), want: aiv1.LandingMerging, reason: "the pipeline of merge-c1 on main is running"},
			{do: mainPipeline("success"), want: aiv1.LandingMerged},
		}, merged: true},
		{name: "all switches: a red default branch blocks until its pipeline is green", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{want: aiv1.LandingMerging},
			{do: mainPipeline("failed"), want: aiv1.LandingBlocked,
				reason: "autopilot merged group/infra!1, and the pipeline https://gitlab.example/group/app/-/pipelines/"},
			{want: aiv1.LandingBlocked, reason: "of the merge merge-c1 on group/infra's default branch main is failed"},
			{do: mainPipeline("success"), want: aiv1.LandingMerged}, // retried
		}, merged: true},
		{name: "all switches: a canceled default-branch pipeline blocks", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{do: mainPipeline("canceled"), want: aiv1.LandingBlocked, reason: "is canceled"},
		}, merged: true},
		{name: "all switches, an entry without the milestone's ID: found by IID", setup: func(l *landingEnv) {
			l.autoMerge()
			l.e.MilestoneID = 0
		}, steps: []landingStep{
			{want: aiv1.LandingMerging, reason: "autopilot merges"},
			{do: mainPipeline("success"), want: aiv1.LandingMerged},
		}, merged: true},
		{name: "all switches: no default-branch pipeline is no CI", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{want: aiv1.LandingMerging, reason: "waiting for the pipeline of merge-c1"},
			{do: later(11 * time.Minute), want: aiv1.LandingMerged},
		}, merged: true},
		{name: "merge missing on the line: the owners", setup: func(l *landingEnv) {
			l.autoMerge()
			l.milestone("/agent autopilot\n/agent repo group/infra\nBuild.")
		}, steps: []landingStep{
			{want: aiv1.LandingReady, reason: "the milestone \"Checkout v2\" has no line `/agent repo group/infra merge` any more"},
			{want: aiv1.LandingReady},
		}, notes: []string{"can land now: its home change group/app!1 is merged, and this merge request is green and mergeable"}},
		{name: "not on autopilot: the owners", setup: func(l *landingEnv) {
			l.autoMerge()
			l.milestone("/agent spec\n/agent repo group/infra merge\nBuild.")
		}, steps: []landingStep{{want: aiv1.LandingReady, reason: "is not on autopilot any more"}},
			notes: []string{"Autopilot does not merge it (the milestone \"Checkout v2\" is not on autopilot any more)"}},
		{name: "target without autoMerge: the owners", setup: func(l *landingEnv) {
			l.autoMerge()
			l.spec.CrossRepo.Targets[0].AutoMerge = false
		}, steps: []landingStep{{want: aiv1.LandingReady, reason: "the companion target group/infra does not have `autoMerge: true`"}},
			notes: []string{"so please merge it"}},
		{name: "target no longer allowed by the cluster: the owners", setup: func(l *landingEnv) {
			l.autoMerge()
			l.r.CrossRepoAllowed = []string{"group/other"}
		}, steps: []landingStep{{want: aiv1.LandingReady, reason: "group/infra is no valid companion target of this project any more"}},
			// Not a target, so the guard refuses marking it ready too.
			notes: []string{"It could not be marked ready (refused by the merge/push guard"}, draft: true, refusals: 1},
		{name: "the entry without autoMerge: the owners, with the release", setup: func(l *landingEnv) {
			l.spec.CrossRepo.Targets[0].AutoMerge = true
			l.tag("v1.3.0", 2, 0, true)
		}, steps: []landingStep{{want: aiv1.LandingReady, reason: "its issue was not set to have it merged by autopilot"}},
			notes: []string{"its home change group/app!1 is live in release v1.3.0"}},
		{name: "merge removed from the line before the merge: the owners", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{do: headPipeline("running"), want: aiv1.LandingMerging, reason: "is running"},
			{do: line("/agent autopilot\n/agent repo group/infra\nBuild."), want: aiv1.LandingReady, reason: "has no line"},
			// Asked once, the owners merge it, also with the line back.
			{do: func(l *landingEnv) { l.milestone(landingMilestone); headPipeline("success")(l) }, want: aiv1.LandingReady},
		}, notes: []string{"so please merge it"}},
		{name: "marked draft again while merging: the owners", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{do: headPipeline("running"), want: aiv1.LandingMerging, reason: "is running"},
			{do: func(l *landingEnv) { l.cmr.Title, l.cmr.Draft = "Draft: "+l.cmr.Title, true }, want: aiv1.LandingReady, reason: "someone marked it draft again"},
		}, notes: []string{"Autopilot does not merge it (someone marked it draft again)"}, draft: true},
		{name: "no pipeline on the head: one is started", noCI: true, setup: func(l *landingEnv) {
			l.autoMerge()
			l.e.HeadSince.Time = l.home.Clock.Add(-11 * time.Minute) // no CI so far
		}, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{want: aiv1.LandingMerging, reason: "waiting for the pipeline autopilot started on the head c1"},
			{want: aiv1.LandingMerging, reason: "waiting for the pipeline autopilot started"},
			{do: func(l *landingEnv) { l.green("c1") }, want: aiv1.LandingMerging, reason: "waiting for the pipeline of merge-c1"},
			{do: mainPipeline("success"), want: aiv1.LandingMerged},
		}, merged: true, pipelines: 1},
		{name: "the started pipeline never comes: merged without CI", noCI: true, setup: func(l *landingEnv) {
			l.autoMerge()
			l.e.HeadSince.Time = l.home.Clock.Add(-11 * time.Minute)
		}, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{want: aiv1.LandingMerging, reason: "waiting for the pipeline autopilot started"},
			{do: later(11 * time.Minute), want: aiv1.LandingMerging, reason: "waiting for the pipeline of merge-c1"},
		}, merged: true, pipelines: 1},
		{name: "no merge request pipelines: waits for the timeout before merging without CI", noCI: true, setup: func(l *landingEnv) {
			l.autoMerge()
			l.e.HeadSince.Time = l.home.Clock.Add(-11 * time.Minute)
			l.infra.NoMRPipelines = true
		}, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{want: aiv1.LandingMerging, reason: "GitLab starts no merge request pipeline for group/infra!1; waiting for a pipeline on its head c1"},
			{do: later(5 * time.Minute), want: aiv1.LandingMerging, reason: "waiting for the pipeline autopilot started"},
			{do: later(6 * time.Minute), want: aiv1.LandingMerging, reason: "waiting for the pipeline of merge-c1"},
		}, merged: true},
		{name: "no pipeline permission: the owners, never merged without CI", noCI: true, setup: func(l *landingEnv) {
			l.autoMerge()
			l.e.HeadSince.Time = l.home.Clock.Add(-11 * time.Minute)
			l.infra.MRPipelineError = &gitlab.Error{StatusCode: 403, Method: "POST", Path: "pipelines", Body: "403 Forbidden"}
		}, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{want: aiv1.LandingReady, reason: "it may not start a pipeline for its head (HTTP 403)"},
			{do: later(time.Hour), want: aiv1.LandingReady},
		}, notes: []string{"Autopilot does not merge it (it may not start a pipeline for its head (HTTP 403))"}},
		{name: "a red pipeline before the merge blocks until green", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{do: headPipeline("failed"), want: aiv1.LandingBlocked, reason: "is failed on its head c1"},
			{want: aiv1.LandingBlocked, reason: "is failed on its head c1"},
			{do: headPipeline("success"), want: aiv1.LandingMerging, reason: "autopilot merged"},
		}, merged: true},
		{name: "one comment, one reminder after 7 days, no third", steps: []landingStep{
			{want: aiv1.LandingReady},
			{do: later(7*day - time.Minute), want: aiv1.LandingReady},
			{do: later(time.Minute), want: aiv1.LandingReady},
			{do: later(30 * day), want: aiv1.LandingReady},
		}, notes: []string{"can land now", "Reminder: this companion of group/app#1 has been ready to land since"}},
		{name: "merged by hand while ready", steps: []landingStep{
			{want: aiv1.LandingReady},
			{do: merged, want: aiv1.LandingMerged},
		}, notes: []string{"can land now"}},
		{name: "merged by hand while merging", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{do: merged, want: aiv1.LandingMerged},
		}},
		{name: "closed by hand while merging", setup: (*landingEnv).autoMerge, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{do: func(l *landingEnv) { l.cmr.State = "closed" }, want: aiv1.LandingClosed},
		}},
		{name: "a merge GitLab refuses: the ready checks once, then blocked", setup: func(l *landingEnv) {
			l.autoMerge()
			l.infra.MergeErrors = map[int64]error{1: &gitlab.Error{StatusCode: 409, Method: "PUT", Path: "merge", Body: "SHA does not match HEAD of source branch"}}
		}, steps: []landingStep{
			{want: aiv1.LandingMerging},
			{want: aiv1.LandingWaitingCI, reason: "GitLab refused to merge group/infra!1 (gitlab: PUT merge: 409 SHA does not match"},
			{want: aiv1.LandingBlocked, reason: "GitLab refused to merge group/infra!1 again"},
			// The checks pass, but it stays blocked until a resume.
			{want: aiv1.LandingBlocked, reason: "again"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newLandingEnv(t)
			if !c.noCI {
				l.green("c1")
			}
			if c.setup != nil {
				c.setup(l)
			}
			for i, s := range c.steps {
				if s.do != nil {
					s.do(l)
				}
				l.poll()
				if l.e.State != s.want || !strings.Contains(l.e.Reason, s.reason) {
					t.Fatalf("poll %d: want %s with %q, got %s with %q", i+1, s.want, s.reason, l.e.State, l.e.Reason)
				}
			}
			if c.merged != (len(l.infra.Merged) == 1) || len(l.infra.Merged) > 1 {
				t.Errorf("merged %v, want merged %v", l.infra.Merged, c.merged)
			}
			if c.merged {
				if opts := l.infra.MergeOpts[1]; opts.SHA != "c1" || l.e.MergeSHA != "merge-c1" || l.e.MergeStartedAt == nil {
					t.Errorf("merged with the checked head: %+v, entry %+v", opts, l.e)
				}
			}
			if l.e.NotifiedAt != nil && l.e.AutoMerge {
				t.Errorf("the owners' path clears autoMerge: %+v", l.e)
			}
			notes := l.notes()
			if len(notes) != len(c.notes) {
				t.Fatalf("%d comments, want %d: %+v", len(notes), len(c.notes), notes)
			}
			for i, want := range c.notes {
				if !strings.Contains(notes[i].Body, want) || !strings.HasSuffix(notes[i].Body, "cc @alice") {
					t.Errorf("comment %d without %q or the owners: %s", i+1, want, notes[i].Body)
				}
			}
			if l.cmr.Draft != c.draft {
				t.Errorf("draft %v, want %v", l.cmr.Draft, c.draft)
			}
			if len(l.infra.MRPipelines) != c.pipelines {
				t.Errorf("merge request pipelines %v, want %d", l.infra.MRPipelines, c.pipelines)
			}
			if l.refusals != c.refusals {
				t.Errorf("guard refusals %d, want %d", l.refusals, c.refusals)
			}
			if l.e.State.Ended() && l.e.EndedAt == nil {
				t.Error("ended without endedAt")
			}
		})
	}
}

// A switch that does not hold can never merge: forced past mergeSwitches,
// the guard refuses the merge in a target without autoMerge, and the entry
// is blocked.
func TestCompanionMergeGuard(t *testing.T) {
	l := newLandingEnv(t)
	l.e.AutoMerge = true // the target has no autoMerge
	l.e.State = aiv1.LandingMerging
	if err := l.r.mergeNow(l.ctx, l.api(), l.spec, &l.e); err != nil {
		t.Fatal(err)
	}
	if len(l.infra.Merged) != 0 || l.refusals != 1 || l.e.State != aiv1.LandingBlocked ||
		!strings.Contains(l.e.Reason, "the merge/push guard refused to merge group/infra!1: a companion target without autoMerge") {
		t.Fatalf("the guard refuses: merged %v, %d refusals, %+v", l.infra.Merged, l.refusals, l.e)
	}
	l.poll()
	if l.e.State != aiv1.LandingBlocked || len(l.infra.Merged) != 0 {
		t.Fatalf("it stays blocked: %+v", l.e)
	}
}

func TestReleaseOrdering(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"2026.9.1", "2026.10.1", -1},
		{"2026.11.10", "2026.11.9", 1},
		{"2026.11.3", "2026.11.3", 0},
		{"2027.1.1", "2026.12.40", 1},
	} {
		a, okA := parseRelease(c.a)
		b, okB := parseRelease(c.b)
		if !okA || !okB || compareReleases(a, b) != c.want {
			t.Errorf("%s vs %s: %d, want %d", c.a, c.b, compareReleases(a, b), c.want)
		}
	}
	for _, bad := range []string{"dev", "", "v2026.11.3", "2026.11", "2026.11.3.1", "2026.-1.3", "2026.11.x"} {
		if _, ok := parseRelease(bad); ok {
			t.Errorf("%q parsed as a release", bad)
		}
	}
	first, ok := firstRelease([]gitlab.CommitRef{{Type: "tag", Name: "2026.11.10"}, {Type: "tag", Name: "latest"}, {Type: "tag", Name: "2026.11.9"}, {Type: "branch", Name: "2026.1.1"}})
	if !ok || releaseName(first) != "2026.11.9" {
		t.Errorf("first release %v %v", first, ok)
	}
}

func TestDocsOnly(t *testing.T) {
	d := func(old, new string) gitlab.Diff { return gitlab.Diff{OldPath: old, NewPath: new} }
	for _, c := range []struct {
		diffs []gitlab.Diff
		want  bool
	}{
		{[]gitlab.Diff{d("docs/usage.md", "docs/usage.md"), d("README.md", "README.md"), d("docs/img/a.png", "docs/img/a.png")}, true},
		{[]gitlab.Diff{d("charts/x/README.md", "charts/x/README.md")}, true},
		{[]gitlab.Diff{d("docs/usage.md", "docs/usage.md"), d("cmd/main.go", "cmd/main.go")}, false},
		{[]gitlab.Diff{d("cmd/notes.go", "docs/notes.md")}, false},
		{nil, false},
	} {
		if got := docsOnly(c.diffs); got != c.want {
			t.Errorf("%+v: %v, want %v", c.diffs, got, c.want)
		}
	}
}

// companionTask is the implement task of #1 with its home merge request !1
// and the companion group/infra!1 pushed at c1.
func companionTask(autoMerge bool) aiv1.AgentTask {
	t := aiv1.AgentTask{}
	t.Name = "shop-1-api"
	t.Spec.Type = aiv1.TaskTypeImplement
	t.Spec.Issue = &aiv1.IssueRef{IID: 1, Title: "API"}
	t.Spec.Milestone = &aiv1.MilestoneRef{ID: 11, IID: 3, Title: "Checkout v2"}
	t.Spec.Owners = []string{"alice"}
	t.Spec.Companion = &aiv1.CompanionSpec{Project: "group/infra", Branch: "agent/app-1-api", AutoMerge: autoMerge}
	t.Status.Run.MergeRequestIID = 1
	t.Status.Run.Companion = &aiv1.CompanionRun{Project: "group/infra", MRIID: 1, MRURL: "https://gitlab.example/group/infra/-/merge_requests/1",
		PushedSHA: "c1", ReviewedSHA: "c1"}
	return t
}

// The entry is created once the home merge request is merged, also by hand,
// with the task's fields, once per companion merge request; outside
// autopilot a blocked companion is told once, with the owners; an ended
// entry is removed after a week.
func TestSyncCompanions(t *testing.T) {
	l := newLandingEnv(t)
	project := testProject()
	spec := l.spec
	home, err := l.home.CreateMergeRequest(l.ctx, "group/app", gitlab.CreateMergeRequestOptions{SourceBranch: "agent/1-api", Title: "API"})
	if err != nil {
		t.Fatal(err)
	}
	tasks := []aiv1.AgentTask{companionTask(true)}
	sync := func(homeMRs map[string]*gitlab.MergeRequest, autopilots map[int64]*aiv1.AgentTask) map[string]bool {
		t.Helper()
		keep := map[string]bool{}
		if err := l.r.syncCompanions(l.ctx, l.api(), project, spec, tasks, homeMRs, autopilots, nil, keep); err != nil {
			t.Fatal(err)
		}
		return keep
	}

	// Open: nothing yet (also not with the issue sync's stale copy).
	sync(nil, nil)
	if len(project.Status.Companions) != 0 {
		t.Fatalf("no entry before the home merge: %+v", project.Status.Companions)
	}
	// Merged by hand: recorded, also without the issue sync's copy (a task
	// whose issue closed).
	l.home.MRs[home.IID].State, l.home.MRs[home.IID].MergeCommitSHA = "merged", "m1"
	sync(map[string]*gitlab.MergeRequest{"other": nil}, nil)
	if n := len(project.Status.Companions); n != 1 {
		t.Fatalf("one entry after the home merge, got %d", n)
	}
	e := project.Status.Companions[0]
	if e.HomeIssueIID != 1 || e.MilestoneIID != 3 || e.MilestoneTitle != "Checkout v2" || e.HomeMRIID != 1 || e.HomeMergeSHA != "m1" ||
		e.Project != "group/infra" || e.MRIID != 1 || e.HeadSHA != "c1" || e.Diff == "" || !e.AutoMerge || !slices.Equal(e.Owners, []string{"alice"}) ||
		e.DocsOnly || e.State != aiv1.LandingWaitingCI {
		t.Fatalf("the entry: %+v", e)
	}
	sync(nil, nil)
	if n := len(project.Status.Companions); n != 1 {
		t.Fatalf("idempotent per companion merge request, got %d", n)
	}

	// Blocked outside autopilot: one comment with the owners, not again.
	l.infra.AddPipeline("c1", "agent/app-1-api", "failed")
	sync(nil, nil)
	sync(nil, nil)
	notes := l.infra.MRNotes[1]
	if project.Status.Companions[0].State != aiv1.LandingBlocked || len(notes) != 1 ||
		!strings.Contains(notes[0].Body, "This companion of group/app#1 (merged in !1) cannot land") || !strings.Contains(notes[0].Body, "cc @alice") {
		t.Fatalf("one comment on the blocked companion: %+v, %+v", project.Status.Companions[0], notes)
	}
	// On an autopilot milestone the tracking issue says it instead.
	project.Status.Companions[0].BlockedNotice = ""
	st := &aiv1.AgentTask{}
	st.Spec.Autopilot = true
	sync(nil, map[int64]*aiv1.AgentTask{3: st})
	if len(l.infra.MRNotes[1]) != 1 {
		t.Fatalf("no comment on an autopilot milestone: %+v", l.infra.MRNotes[1])
	}

	// Merged by hand: recorded, and removed a week later.
	l.cmr.State, l.cmr.MergeCommitSHA = "merged", "cm1"
	sync(nil, nil)
	if e := project.Status.Companions[0]; e.State != aiv1.LandingMerged || e.MergeSHA != "cm1" || e.EndedAt == nil {
		t.Fatalf("merged by hand: %+v", e)
	}
	l.home.Clock = l.home.Clock.Add(aiv1.CompanionRetention - time.Minute)
	sync(nil, nil)
	if len(project.Status.Companions) != 1 {
		t.Fatal("kept for a week")
	}
	l.home.Clock = l.home.Clock.Add(time.Minute)
	sync(nil, nil)
	if len(project.Status.Companions) != 0 {
		t.Fatalf("removed after a week: %+v", project.Status.Companions)
	}
}

// A task whose home merge cannot be checked is kept, so it is not deleted
// before its companion is recorded.
func TestSyncCompanionsKeepsUncheckedTasks(t *testing.T) {
	l := newLandingEnv(t)
	project := testProject()
	home, _ := l.home.CreateMergeRequest(l.ctx, "group/app", gitlab.CreateMergeRequestOptions{SourceBranch: "agent/1-api", Title: "API"})
	l.home.MRs[home.IID].State = "merged"
	l.home.DiffErrors = map[int64]error{home.IID: &gitlab.Error{StatusCode: 500, Method: "GET", Path: "diffs"}}
	keep := map[string]bool{}
	err := l.r.syncCompanions(l.ctx, l.api(), project, l.spec, []aiv1.AgentTask{companionTask(false)}, nil, nil, nil, keep)
	if err == nil || !keep["shop-1-api"] || len(project.Status.Companions) != 0 {
		t.Fatalf("the task is kept and the error reported: %v, keep %v, %+v", err, keep, project.Status.Companions)
	}
}

// After the autopilot's home merge the companion is recorded and lands by
// rule; the home task may be deleted, the entry stays.
func TestCompanionLandsAfterAutopilotMerge(t *testing.T) {
	e, infra := newCompanionGateEnv(t)
	e.finishCompanionRun(infra, 1, "sha-api", "sha-infra", aiv1.CIStateSuccess, nil)
	e.reconcile() // marked ready
	e.reconcile() // merged
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("the home MR merges: %v", e.gl.Merged)
	}
	infra.AddPipeline("sha-infra", "agent/app-1-api", "success")
	e.reconcile()
	cs := e.project().Status.Companions
	if len(cs) != 1 {
		t.Fatalf("one companion entry after the home merge: %+v", cs)
	}
	c := cs[0]
	if c.State != aiv1.LandingReady || c.HomeIssueIID != 1 || c.MilestoneIID != 3 || c.HomeMergeSHA != "merge-sha-api" || c.HeadSHA != "sha-infra" ||
		c.ReadyAt == nil || c.AutoMerge || c.Release != "" {
		t.Fatalf("a project without tags: ready after the merge: %+v", c)
	}
	for range 2 {
		e.reconcile()
	}
	if e.implTaskOf(1) != nil {
		t.Fatal("the home task of the closed issue is deleted")
	}
	if cs := e.project().Status.Companions; len(cs) != 1 || cs[0].State != aiv1.LandingReady || len(infra.Merged) != 0 {
		t.Fatalf("the entry stays ready and nothing merges it here: %+v, merged %v", cs, infra.Merged)
	}
}

// A landing conflict pauses the milestone with kind companion; it lifts by
// itself once the companion can land, and resume allows one more rebase.
func TestCompanionLandingPausesTheMilestone(t *testing.T) {
	e, infra := newCompanionGateEnv(t)
	_, cmr := e.finishCompanionRun(infra, 1, "sha-api", "sha-infra", aiv1.CIStateSuccess, nil)
	e.reconcile()
	e.reconcile() // home merged
	infra.AddPipeline("sha-infra", "agent/app-1-api", "success")
	cmr.DetailedMergeStatus = "conflict"
	infra.RebaseConflict = true
	e.reconcile() // recorded, rebased
	if !slices.Equal(infra.Rebased, []int64{cmr.IID}) {
		t.Fatalf("one rebase: %v", infra.Rebased)
	}
	e.reconcile() // blocked: paused
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseCompanion || ap.PausedFor != 0 ||
		!strings.Contains(ap.PausedReason, "the companion group/infra!1 of #1 cannot land: the companion merge request group/infra!1 has merge conflicts") {
		t.Fatalf("a landing conflict pauses with kind companion: %+v", ap)
	}
	comments := trackingComments(e)
	if strings.Count(comments, "Autopilot paused") != 1 {
		t.Fatalf("one pause comment:\n%s", comments)
	}
	e.reconcile()
	if strings.Count(trackingComments(e), "Autopilot paused") != 1 || len(infra.Rebased) != 1 {
		t.Fatalf("the pause is said once, and no second rebase: %v", infra.Rebased)
	}

	// resume: checked again, with one more rebase; still conflicting, it
	// pauses again.
	e.gl.AddNote(ap.TrackingIssueIID, alice, "/agent resume")
	e.reconcile()
	if cs := e.project().Status.Companions; cs[0].State != aiv1.LandingWaitingCI || cs[0].RebasedSHA != "" {
		t.Fatalf("resume re-checks the entry: %+v", cs[0])
	}
	e.reconcile()
	if len(infra.Rebased) != 2 {
		t.Fatalf("one more rebase after resume: %v", infra.Rebased)
	}
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseCompanion {
		t.Fatalf("paused again: %+v", ap)
	}

	// Resolved by hand: the pause lifts by itself.
	cmr.DetailedMergeStatus = "draft_status"
	e.reconcile()
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotPaused {
		t.Fatalf("the pause lifts once the companion can land: %+v", ap)
	}
	if cs := e.project().Status.Companions; cs[0].State != aiv1.LandingReady {
		t.Fatalf("ready: %+v", cs[0])
	}
	if !strings.Contains(trackingComments(e), "Autopilot continues: the companions of the milestone can land again") {
		t.Fatalf("the lift is said:\n%s", trackingComments(e))
	}
}

// With all three switches, autopilot merges the companion after the home
// merge with the checked head, and a red default branch afterwards pauses
// the milestone with kind companion, mentioning the owners once.
func TestCompanionAutopilotMergeAndRedDefaultBranch(t *testing.T) {
	e, infra := newCompanionGateEnv(t)
	e.r.DefaultOwners = []string{"alice"}
	withInfraTarget(e, true)
	e.gl.Milestones[0].Description = "/agent autopilot\n/agent repo group/infra merge\nBuild a new checkout."
	_, cmr := e.finishCompanionRun(infra, 1, "sha-api", "sha-infra", aiv1.CIStateSuccess, nil)
	e.gl.SetLabels(1, "ai-generated", "agent::review", "agent-repo::group/infra")
	e.reconcile() // marked ready
	if c := e.implTaskOf(1).Spec.Companion; c == nil || !c.AutoMerge {
		t.Fatalf("the issue's companion may be merged by autopilot: %+v", c)
	}
	e.reconcile() // merged
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("the home MR merges: %v", e.gl.Merged)
	}
	infra.AddPipeline("sha-infra", "agent/app-1-api", "success")
	e.reconcile() // recorded, ready: marked ready
	if cs := e.project().Status.Companions; len(cs) != 1 || cs[0].State != aiv1.LandingMerging || !cs[0].AutoMerge || cs[0].MilestoneID != 11 || cmr.Draft {
		t.Fatalf("autopilot marks the companion ready to merge it: %+v, draft %v", cs, cmr.Draft)
	}
	e.reconcile() // merged
	if !slices.Equal(infra.Merged, []int64{cmr.IID}) || infra.MergeOpts[cmr.IID].SHA != "sha-infra" {
		t.Fatalf("autopilot merges the checked head: %v %+v", infra.Merged, infra.MergeOpts)
	}
	infra.AddPipeline("merge-sha-infra", "main", "failed")
	e.reconcile() // blocked
	e.reconcile() // paused
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseCompanion ||
		!strings.Contains(ap.PausedReason, "autopilot merged group/infra!1, and the pipeline") {
		t.Fatalf("a red default branch after the merge pauses with kind companion: %+v", ap)
	}
	comments := trackingComments(e)
	if strings.Count(comments, "Autopilot paused: the companion group/infra!1 of #1 cannot land") != 1 || !strings.Contains(comments, "cc @alice") {
		t.Fatalf("one pause comment, mentioning the owners:\n%s", comments)
	}
	if len(infra.MRNotes[cmr.IID]) != 0 {
		t.Fatalf("on autopilot the companion merge request gets no comment: %+v", infra.MRNotes[cmr.IID])
	}

	// resume: the owner goes on, it is recorded as merged and the pause ends.
	e.gl.AddNote(ap.TrackingIssueIID, alice, "/agent resume")
	e.reconcile()
	e.reconcile()
	if cs := e.project().Status.Companions; cs[0].State != aiv1.LandingMerged || cs[0].EndedAt == nil {
		t.Fatalf("resume records it as merged: %+v", cs[0])
	}
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotPaused {
		t.Fatalf("the pause ends: %+v", ap)
	}
}

// A rate limit (or a GitLab error) when starting the head's pipeline is not
// "no CI": the poll fails, it is tried again, and nothing is merged.
func TestCompanionMergePipelineRateLimited(t *testing.T) {
	l := newLandingEnv(t)
	l.autoMerge()
	l.e.HeadSince.Time = l.home.Clock.Add(-11 * time.Minute)
	l.infra.MRPipelineError = &gitlab.Error{StatusCode: 429, Method: "POST", Path: "pipelines", Body: "Retry later"}
	l.poll() // marked ready
	for range 2 {
		l.home.Clock = l.home.Clock.Add(time.Hour)
		if err := l.r.landCompanion(l.ctx, l.api(), l.spec, &l.e, []string{"alice"}); err == nil || !strings.Contains(err.Error(), "starting a pipeline for group/infra!1") {
			t.Fatalf("the rate limit is an error: %v", err)
		}
	}
	if l.e.State != aiv1.LandingMerging || l.e.PipelineStartedAt != nil || len(l.infra.Merged) != 0 {
		t.Fatalf("still merging, no pipeline on record, nothing merged: %+v, merged %v", l.e, l.infra.Merged)
	}
	l.infra.MRPipelineError = nil
	l.poll()
	if len(l.infra.MRPipelines) != 1 || l.e.PipelineStartedAt == nil || len(l.infra.Merged) != 0 {
		t.Fatalf("started once GitLab allows it, not merged yet: %v %+v", l.infra.MRPipelines, l.e)
	}
}

// A companion of the milestone that waits for a release adds one companion
// record, unless one covers its home merge; a docs-only home merge, another
// milestone's companion and one past waitingRelease add none.
func TestCompanionReleases(t *testing.T) {
	rec := func(id, after int64, by aiv1.ReleaseBy, state aiv1.ReleaseState, sha string) aiv1.ReleaseRecord {
		return aiv1.ReleaseRecord{ID: id, By: by, AfterIssue: after, Why: "planned", SHA: sha, State: state}
	}
	waiting := aiv1.CompanionStatus{HomeIssueIID: 1, MilestoneIID: 3, HomeMRIID: 11, HomeMergeSHA: "m1",
		Project: "group/infra", MRIID: 8, State: aiv1.LandingWaitingRelease}
	for _, tc := range []struct {
		name string
		recs []aiv1.ReleaseRecord
		edit func(*aiv1.CompanionStatus)
		add  bool
	}{
		{name: "waiting for a release", add: true},
		{name: "a planner record after the issue", recs: []aiv1.ReleaseRecord{rec(1, 1, aiv1.ReleaseByPlanner, aiv1.ReleasePlanned, "")}},
		{name: "a waiting owner record after the issue", recs: []aiv1.ReleaseRecord{rec(1, 1, aiv1.ReleaseByOwner, aiv1.ReleaseWaiting, "m1")}},
		{name: "a released record after the issue", recs: []aiv1.ReleaseRecord{rec(1, 1, aiv1.ReleaseByPlanner, aiv1.ReleaseReleased, "m1")}},
		{name: "a failed planner record after the issue", recs: []aiv1.ReleaseRecord{rec(1, 1, aiv1.ReleaseByPlanner, aiv1.ReleaseFailed, "m1")}, add: true},
		{name: "a failed companion record", recs: []aiv1.ReleaseRecord{rec(1, 1, aiv1.ReleaseByCompanion, aiv1.ReleaseFailed, "m1")}},
		{name: "a later record that contains the merge", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByPilot, aiv1.ReleaseWaiting, "m2")}},
		{name: "a running record whose release contains the merge", recs: []aiv1.ReleaseRecord{
			{ID: 1, By: aiv1.ReleaseByOwner, SHA: "x0", ReleasedSHA: "h1", State: aiv1.ReleaseRunning}}},
		{name: "a dropped record that contains the merge", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByPilot, aiv1.ReleaseDropped, "m2")}, add: true},
		{name: "a record that does not contain the merge", recs: []aiv1.ReleaseRecord{rec(1, 2, aiv1.ReleaseByPilot, aiv1.ReleaseWaiting, "x9")}, add: true},
		{name: "docs only", edit: func(c *aiv1.CompanionStatus) { c.DocsOnly = true }},
		{name: "another milestone", edit: func(c *aiv1.CompanionStatus) { c.MilestoneIID = 4 }},
		{name: "live already", edit: func(c *aiv1.CompanionStatus) { c.State = aiv1.LandingWaitingCI }},
		{name: "no merge commit", edit: func(c *aiv1.CompanionStatus) { c.HomeMergeSHA = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newReleaseEnv(t)
			e.contains("m2", "m1") // h1 contains m1 from newReleaseEnv
			e.recs = slices.Clone(tc.recs)
			a := e.releaseAutopilot()
			c := waiting
			if tc.edit != nil {
				tc.edit(&c)
			}
			a.project = &aiv1.AgentProject{Status: aiv1.AgentProjectStatus{Companions: []aiv1.CompanionStatus{c}}}
			for range 2 { // a second poll adds nothing
				if err := a.companionReleases(e.ctx); err != nil {
					t.Fatal(err)
				}
			}
			added := a.ap.Releases[len(tc.recs):]
			if !tc.add {
				if len(added) != 0 {
					t.Fatalf("want no record, got %+v", added)
				}
				return
			}
			want := aiv1.ReleaseRecord{ID: int64(len(tc.recs)) + 1, By: aiv1.ReleaseByCompanion, AfterIssue: 1, SHA: "m1",
				State: aiv1.ReleaseWaiting, Why: "group/infra!8 waits for #1 to be live", DecidedAt: metav1.NewTime(e.now)}
			if len(added) != 1 || !reflect.DeepEqual(added[0], want) {
				t.Fatalf("want one record %+v, got %+v", want, added)
			}
		})
	}
}

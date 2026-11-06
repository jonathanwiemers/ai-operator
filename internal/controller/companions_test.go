package controller

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// landingEnv is a companion entry of group/app#1 (merged in !1 as m1) for
// its draft merge request in group/infra at c1.
type landingEnv struct {
	t           *testing.T
	ctx         context.Context
	r           *AgentProjectReconciler
	gl          *gitlabfake.Projects
	home, infra *gitlabfake.Fake
	cmr         *gitlab.MergeRequest
	spec        aiv1.AgentProjectSpec
	e           aiv1.CompanionStatus
}

func newLandingEnv(t *testing.T) *landingEnv {
	t.Helper()
	ctx := context.Background()
	home := gitlabfake.New()
	gl := gitlabfake.NewProjects(home)
	infra := gl.Add("group/infra")
	l := &landingEnv{t: t, ctx: ctx, gl: gl, home: home, infra: infra, spec: testProject().Spec.WithDefaults()}
	l.r = &AgentProjectReconciler{Now: func() time.Time { return home.Clock }}
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
	l.e = aiv1.CompanionStatus{HomeIssueIID: 1, MilestoneIID: 3, HomeMRIID: 1, HomeMergeSHA: "m1",
		Project: "group/infra", MRIID: mr.IID, HeadSHA: "c1", HeadSince: &now, Diff: diff, State: aiv1.LandingWaitingRelease}
	return l
}

func (l *landingEnv) poll() {
	l.t.Helper()
	if err := l.r.landCompanion(l.ctx, l.gl, l.spec, &l.e); err != nil {
		l.t.Fatal(err)
	}
}

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
			if len(l.infra.Merged) != 0 || len(l.home.Merged) != 0 {
				t.Errorf("nothing is merged: %v %v", l.infra.Merged, l.home.Merged)
			}
			switch l.e.State {
			case aiv1.LandingReady:
				if l.e.ReadyAt == nil {
					t.Error("ready without readyAt")
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
		if err := l.r.syncCompanions(l.ctx, l.gl, project, spec, tasks, homeMRs, autopilots, keep); err != nil {
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
	err := l.r.syncCompanions(l.ctx, l.gl, project, l.spec, []aiv1.AgentTask{companionTask(false)}, nil, nil, keep)
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

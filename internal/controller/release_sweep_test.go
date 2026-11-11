package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// sweepEnv is a project outside milestones that tags its default branch:
// 2026.11.1 at t1, two days ago. main's head h1 is a code commit that
// landed unreleased ago, with a green pipeline.
type sweepEnv struct {
	t       *testing.T
	ctx     context.Context
	gl      *gitlabfake.Fake
	now     time.Time
	r       *AgentProjectReconciler
	project *aiv1.AgentProject
	spec    aiv1.AgentProjectSpec
	tasks   []*aiv1.AgentTask
}

func newSweepEnv(t *testing.T, unreleased time.Duration) *sweepEnv {
	t.Helper()
	gl := gitlabfake.New()
	gl.Project.WebURL = "https://gitlab.example/group/app"
	e := &sweepEnv{t: t, ctx: context.Background(), gl: gl, now: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), project: testProject()}
	e.r = &AgentProjectReconciler{Now: func() time.Time { return e.now }}
	e.spec = e.project.Spec.WithDefaults()
	created := e.now.Add(-48 * time.Hour)
	gl.AddTag("2026.11.1", gitlab.TagCommit{ID: "t1", ParentIDs: []string{"p1", "p2"}, CreatedAt: &created})
	e.compare("2026.11.1", []gitlab.Commit{e.commit("h1", "t1", unreleased)})
	gl.BranchHeads = map[string]string{"main": "h1"}
	gl.AddPipeline("h1", "main", "success")
	e.project.Status.MainHealth = &aiv1.MainHealthStatus{State: aiv1.MainHealthGreen, SHA: "h1"}
	return e
}

// commit is a commit landed ago, with its first parent.
func (e *sweepEnv) commit(id, parent string, ago time.Duration) gitlab.Commit {
	at := e.now.Add(-ago)
	return gitlab.Commit{ID: id, ParentIDs: []string{parent}, CommittedDate: &at}
}

// compare sets the change from tag to main: commits (oldest first), and a
// code change unless diffs are given.
func (e *sweepEnv) compare(tag string, commits []gitlab.Commit, diffs ...gitlab.Diff) {
	if len(diffs) == 0 {
		diffs = []gitlab.Diff{{OldPath: "go.mod", NewPath: "go.mod"}}
	}
	if e.gl.Compares == nil {
		e.gl.Compares = map[string]*gitlab.Compare{}
	}
	e.gl.Compares[tag+"..main"] = &gitlab.Compare{Commits: commits, Diffs: diffs}
}

func (e *sweepEnv) sync() {
	e.t.Helper()
	if err := e.r.syncProjectReleases(e.ctx, e.gl, e.project, e.spec, e.tasks); err != nil {
		e.t.Fatal(err)
	}
}

func (e *sweepEnv) states() []aiv1.ReleaseState {
	var out []aiv1.ReleaseState
	for _, rec := range e.project.Status.Releases {
		out = append(out, rec.State)
	}
	return out
}

func (e *sweepEnv) wantStates(want ...aiv1.ReleaseState) {
	e.t.Helper()
	got := e.states()
	if len(got) != len(want) {
		e.t.Fatalf("states %v, want %v (%+v)", got, want, e.project.Status.Releases)
	}
	for i := range got {
		if got[i] != want[i] {
			e.t.Fatalf("states %v, want %v (%+v)", got, want, e.project.Status.Releases)
		}
	}
}

func (e *sweepEnv) autopilot(phase aiv1.AutopilotPhase) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 3}}}
	st.Status.Autopilot = &aiv1.AutopilotStatus{Phase: phase}
	e.tasks = append(e.tasks, st)
}

// A code commit landed 25 h ago with main green: the sweep releases it.
func TestSweepReleasesOldUnreleasedCommits(t *testing.T) {
	e := newSweepEnv(t, 25*time.Hour)
	e.sync()
	e.wantStates(aiv1.ReleaseRunning)
	rec := e.project.Status.Releases[0]
	want := "1 commit since 2026.11.1 is unreleased, the oldest since 2026-11-09 11:00 UTC (more than 24h)"
	if rec.By != aiv1.ReleaseBySweep || rec.SHA != "h1" || rec.Why != want {
		t.Fatalf("record %+v, want a sweep record of h1 because %q", rec, want)
	}
	if len(e.gl.Releases) != 1 || e.gl.Releases[0].Ref != "h1" || e.gl.Releases[0].Tag != "2026.11.2" {
		t.Fatalf("releases %+v", e.gl.Releases)
	}

	// While it runs, no second record.
	e.now = e.now.Add(time.Hour)
	e.sync()
	e.wantStates(aiv1.ReleaseRunning)

	e.gl.SetPipelineStatus(e.gl.Releases[0].PipelineID, "success")
	e.gl.Compares["2026.11.2..main"] = &gitlab.Compare{}
	e.sync()
	e.wantStates(aiv1.ReleaseReleased)
	if v := e.project.Status.Releases[0].Version; v != "2026.11.2" {
		t.Fatalf("version %q", v)
	}
	// Nothing unreleased since: nothing more.
	e.now = e.now.Add(48 * time.Hour)
	e.sync()
	e.wantStates(aiv1.ReleaseReleased)
	if len(e.gl.Releases) != 1 {
		t.Fatalf("releases %+v", e.gl.Releases)
	}
}

func TestSweepAddsNothing(t *testing.T) {
	cases := map[string]func(*sweepEnv){
		"23 h old": func(e *sweepEnv) { e.compare("2026.11.1", []gitlab.Commit{e.commit("h1", "t1", 23*time.Hour)}) },
		"docs only": func(e *sweepEnv) {
			e.compare("2026.11.1", []gitlab.Commit{e.commit("h1", "t1", 25*time.Hour)}, gitlab.Diff{OldPath: "docs/usage.md", NewPath: "docs/usage.md"})
		},
		"empty diff":          func(e *sweepEnv) { e.gl.Compares["2026.11.1..main"].Diffs = nil },
		"a running autopilot": func(e *sweepEnv) { e.autopilot(aiv1.AutopilotRunning) },
		"a paused autopilot":  func(e *sweepEnv) { e.autopilot(aiv1.AutopilotPaused) },
		"red main": func(e *sweepEnv) {
			e.project.Status.MainHealth = &aiv1.MainHealthStatus{State: aiv1.MainHealthRed, SHA: "h1"}
		},
		"main green on an older head": func(e *sweepEnv) {
			e.project.Status.MainHealth = &aiv1.MainHealthStatus{State: aiv1.MainHealthGreen, SHA: "h0"}
		},
		"no main health yet": func(e *sweepEnv) { e.project.Status.MainHealth = nil },
		"no tags":            func(e *sweepEnv) { e.gl.Tags = nil },
		"mode Off":           func(e *sweepEnv) { e.spec.Workflow.Release.Mode = aiv1.ReleaseOff },
		// A Renovate branch made three days ago, merged two hours ago: it is
		// unreleased since its merge.
		"a branch merged recently": func(e *sweepEnv) {
			e.compare("2026.11.1", []gitlab.Commit{e.commit("b1", "t1", 72*time.Hour), {ID: "h1", ParentIDs: []string{"t1", "b1"},
				CommittedDate: ptrTime(e.now.Add(-2 * time.Hour))}})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newSweepEnv(t, 25*time.Hour)
			setup(e)
			e.sync()
			e.wantStates()
			if len(e.gl.Releases) != 0 {
				t.Fatalf("releases %+v", e.gl.Releases)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// A sweep release that failed is not tried again for the same head; a new
// merge moves the head and the sweep decides again.
func TestSweepAfterAFailedRelease(t *testing.T) {
	e := newSweepEnv(t, 25*time.Hour)
	e.project.Status.Releases = []aiv1.ReleaseRecord{{ID: 1, By: aiv1.ReleaseBySweep, SHA: "h1", State: aiv1.ReleaseFailed,
		Attempts: 2, DecidedAt: metav1.NewTime(e.now.Add(-2 * time.Hour))}}
	e.now = e.now.Add(time.Hour)
	e.sync()
	e.wantStates(aiv1.ReleaseFailed)
	if len(e.gl.Releases) != 0 {
		t.Fatalf("releases %+v", e.gl.Releases)
	}

	e.compare("2026.11.1", []gitlab.Commit{e.commit("h1", "t1", 26*time.Hour), e.commit("h2", "h1", time.Hour)})
	e.gl.BranchHeads = map[string]string{"main": "h2"}
	e.gl.AddPipeline("h2", "main", "success")
	e.project.Status.MainHealth = &aiv1.MainHealthStatus{State: aiv1.MainHealthGreen, SHA: "h2"}
	e.sync()
	e.wantStates(aiv1.ReleaseFailed, aiv1.ReleaseRunning)
	if sha := e.project.Status.Releases[1].SHA; sha != "h2" {
		t.Fatalf("sha %q", sha)
	}
}

// With the main health watcher off, the releaser's green-head check decides:
// a red newest pipeline keeps the sweep's record waiting.
func TestSweepWithoutMainHealth(t *testing.T) {
	e := newSweepEnv(t, 25*time.Hour)
	e.spec.MainHealth.Disabled = true
	e.project.Status.MainHealth = nil
	e.gl.AddPipeline("h1", "main", "failed")
	e.sync()
	e.wantStates(aiv1.ReleaseWaiting)
	if r := e.project.Status.Releases[0].Reason; r != "the newest pipeline of main (h1) is failed" {
		t.Fatalf("reason %q", r)
	}
	if len(e.gl.Releases) != 0 {
		t.Fatalf("releases %+v", e.gl.Releases)
	}
}

// A branch merged 30 h ago counts from its merge, not from its own commits.
func TestSweepCountsFromTheOldestMerge(t *testing.T) {
	e := newSweepEnv(t, 0)
	e.compare("2026.11.1", []gitlab.Commit{e.commit("b1", "t1", 72*time.Hour), e.commit("m1", "t1", 30*time.Hour),
		e.commit("h1", "m1", time.Hour)})
	e.sync()
	e.wantStates(aiv1.ReleaseRunning)
	if want := "3 commits since 2026.11.1 are unreleased, the oldest since 2026-11-09 06:00 UTC (more than 24h)"; e.project.Status.Releases[0].Why != want {
		t.Fatalf("why %q, want %q", e.project.Status.Releases[0].Why, want)
	}
}

// While an autopilot runs, a Running record is followed to its end and a
// Waiting one waits.
func TestProjectReleasesWhileAnAutopilotRuns(t *testing.T) {
	e := newSweepEnv(t, 25*time.Hour)
	running := aiv1.ReleaseRecord{ID: 1, By: aiv1.ReleaseBySweep, SHA: "h1", State: aiv1.ReleaseRunning, Attempts: 1,
		PipelineID: e.gl.AddPipeline("h1", "main", "success"), DecidedAt: metav1.NewTime(e.now)}
	waiting := aiv1.ReleaseRecord{ID: 2, By: aiv1.ReleaseByCompanion, SHA: "m9", State: aiv1.ReleaseWaiting, DecidedAt: metav1.NewTime(e.now)}
	e.project.Status.Releases = []aiv1.ReleaseRecord{running, waiting}
	created := e.now
	e.gl.AddTag("2026.11.2", gitlab.TagCommit{ID: "h1", ParentIDs: []string{"t1", "c1"}, CreatedAt: &created})
	e.autopilot(aiv1.AutopilotRunning)
	e.sync()
	e.wantStates(aiv1.ReleaseReleased, aiv1.ReleaseWaiting)
	if e.project.Status.Releases[1].Reason != "" {
		t.Fatalf("the waiting record moved: %+v", e.project.Status.Releases[1])
	}
}

// A companion waiting for a release outside a milestone adds a record at
// once, also before the sweep would; never a second one for its merge.
func TestCompanionReleaseOutsideMilestones(t *testing.T) {
	e := newSweepEnv(t, time.Hour)
	e.project.Status.Companions = []aiv1.CompanionStatus{
		{Project: "group/b", MRIID: 9, HomeIssueIID: 4, HomeMergeSHA: "m1", State: aiv1.LandingWaitingRelease},
		{Project: "group/c", MRIID: 5, HomeIssueIID: 6, HomeMergeSHA: "m2", State: aiv1.LandingWaitingRelease, DocsOnly: true},
	}
	e.gl.Compares["h1..m1"] = &gitlab.Compare{}
	e.sync()
	e.wantStates(aiv1.ReleaseRunning)
	rec := e.project.Status.Releases[0]
	if rec.By != aiv1.ReleaseByCompanion || rec.SHA != "m1" || rec.AfterIssue != 4 || rec.Why != "group/b!9 waits for #4 to be live" {
		t.Fatalf("record %+v", rec)
	}

	// Its release failed after the retry: not tried again.
	e.project.Status.Releases[0].State = aiv1.ReleaseFailed
	e.sync()
	e.wantStates(aiv1.ReleaseFailed)
}

// The project's list keeps the newest 20.
func TestProjectReleasesCapped(t *testing.T) {
	e := newSweepEnv(t, 25*time.Hour)
	for i := range 25 {
		e.project.Status.Releases = append(e.project.Status.Releases, aiv1.ReleaseRecord{ID: int64(i + 1), By: aiv1.ReleaseBySweep,
			State: aiv1.ReleaseAlreadyReleased, DecidedAt: metav1.NewTime(e.now.Add(-time.Duration(30-i) * time.Hour))})
	}
	e.sync()
	recs := e.project.Status.Releases
	if len(recs) != aiv1.MaxProjectReleases || recs[0].ID != 7 || recs[len(recs)-1].ID != 26 || recs[len(recs)-1].State != aiv1.ReleaseRunning {
		t.Fatalf("%d records, %+v", len(recs), recs)
	}
}

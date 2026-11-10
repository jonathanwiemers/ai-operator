package controller

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// releaseEnv is a project that tags its default branch (2026.11.1, a day
// ago), whose milestone merged #1 (!11) and #2 (!12) as m1 and m2. main's
// head h1 has a green pipeline and contains both merges.
type releaseEnv struct {
	t    *testing.T
	ctx  context.Context
	gl   *gitlabfake.Fake
	now  time.Time
	x    *releaser
	recs []aiv1.ReleaseRecord
}

func newReleaseEnv(t *testing.T) *releaseEnv {
	t.Helper()
	gl := gitlabfake.New()
	gl.Project.WebURL = "https://gitlab.example/group/app"
	e := &releaseEnv{t: t, ctx: context.Background(), gl: gl, now: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	r := &AgentProjectReconciler{Now: func() time.Time { return e.now }}
	e.x = &releaser{r: r, gl: gl, spec: testProject().Spec.WithDefaults(),
		project:   func(ctx context.Context) (*gitlab.Project, error) { return gl.GetProject(ctx, "group/app") },
		items:     []releaseItem{{IID: 1, MR: 11, Title: "API"}, {IID: 2, MR: 12, Title: "UI"}},
		milestone: aiv1.MilestoneRef{ID: 11, IID: 3, Title: "Checkout v2"}, log: logr.Discard()}
	e.tag("2026.11.1", "t1")
	e.green("h1")
	e.contains("h1", "m1", "m2")
	return e
}

// tag tags commit id as name, a merge commit an hour ago that contains
// shas.
func (e *releaseEnv) tag(name, id string, shas ...string) {
	created := e.now.Add(-time.Hour)
	e.gl.AddTag(name, gitlab.TagCommit{ID: id, ParentIDs: []string{"p1", "p2"}, CreatedAt: &created}, shas...)
}

// green makes sha main's head with a green pipeline.
func (e *releaseEnv) green(sha string) {
	e.gl.BranchHeads = map[string]string{"main": sha}
	e.gl.AddPipeline(sha, "main", "success")
}

// contains records that head contains each of shas.
func (e *releaseEnv) contains(head string, shas ...string) {
	if e.gl.Compares == nil {
		e.gl.Compares = map[string]*gitlab.Compare{}
	}
	for _, sha := range shas {
		e.gl.Compares[head+".."+sha] = &gitlab.Compare{}
	}
}

func (e *releaseEnv) waiting(id int64, sha string) aiv1.ReleaseRecord {
	return aiv1.ReleaseRecord{ID: id, By: aiv1.ReleaseByPlanner, Why: "users see the API", AfterIssue: id, SHA: sha,
		State: aiv1.ReleaseWaiting, DecidedAt: metav1.NewTime(e.now.Add(-time.Hour))}
}

func (e *releaseEnv) step() {
	e.t.Helper()
	if err := e.x.step(e.ctx, e.recs); err != nil {
		e.t.Fatal(err)
	}
}

// pipeline is the ID of the n-th release pipeline created (from 1).
func (e *releaseEnv) pipeline(n int) int64 {
	e.t.Helper()
	if len(e.gl.CreatedPipelines) < n {
		e.t.Fatalf("want release pipeline %d, created %+v", n, e.gl.CreatedPipelines)
	}
	return e.gl.CreatedPipelines[n-1].ID
}

type releasePoll struct {
	do func(*releaseEnv)
	// want is the state of each record after the poll; reason a part of
	// the last record's reason.
	want   []aiv1.ReleaseState
	reason string
}

func TestReleaseStep(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*releaseEnv)
		polls []releasePoll
		// pipelines is how many release pipelines were created; version
		// the version of the last record afterwards.
		pipelines int
		version   string
	}{
		{name: "a tag of a project that releases on push: already released", setup: func(e *releaseEnv) {
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
			e.tag("2026.11.2", "t2", "m1")
		}, polls: []releasePoll{{want: []aiv1.ReleaseState{aiv1.ReleaseAlreadyReleased}, reason: "the tag 2026.11.2 already contains m1"}},
			version: "2026.11.2"},
		{name: "contained in our own release: released", setup: func(e *releaseEnv) {
			ours := e.waiting(1, "m1")
			ours.State, ours.Version, ours.PipelineID = aiv1.ReleaseReleased, "2026.11.2", 5
			e.recs = []aiv1.ReleaseRecord{ours, e.waiting(2, "m2")}
			e.tag("2026.11.2", "t2", "m1", "m2")
		}, polls: []releasePoll{{want: []aiv1.ReleaseState{aiv1.ReleaseReleased, aiv1.ReleaseReleased}, reason: "contained in 2026.11.2"}},
			version: "2026.11.2"},
		{name: "a project that does not tag its default branch", setup: func(e *releaseEnv) {
			e.gl.Tags = nil
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
		}, polls: []releasePoll{{want: []aiv1.ReleaseState{aiv1.ReleaseNotApplicable}, reason: "group/app does not tag its default branch"}}},
		{name: "mode Off", setup: func(e *releaseEnv) {
			e.x.spec.Workflow.Release.Mode = aiv1.ReleaseOff
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
		}, polls: []releasePoll{{want: []aiv1.ReleaseState{aiv1.ReleaseNotApplicable}, reason: "release.mode is Off"}}},
		{name: "a red head waits", setup: func(e *releaseEnv) {
			e.gl.AddPipeline("h2", "main", "failed")
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
		}, polls: []releasePoll{
			{want: []aiv1.ReleaseState{aiv1.ReleaseWaiting}, reason: "the newest pipeline of main (h2) is failed"},
			{do: func(e *releaseEnv) { e.green("h3"); e.contains("h3", "m1") }, want: []aiv1.ReleaseState{aiv1.ReleaseRunning}},
		}, pipelines: 1},
		{name: "a head without the commit waits", setup: func(e *releaseEnv) {
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m3")}
			e.gl.Compares["h1..m3"] = &gitlab.Compare{Commits: []gitlab.Commit{{ID: "m3"}}}
		}, polls: []releasePoll{{want: []aiv1.ReleaseState{aiv1.ReleaseWaiting}, reason: "the newest green pipeline of main (h1) does not contain m3 yet"}}},
		{name: "a head equal to the commit", setup: func(e *releaseEnv) {
			e.green("m3")
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m3")}
		}, polls: []releasePoll{{want: []aiv1.ReleaseState{aiv1.ReleaseRunning}}}, pipelines: 1},
		{name: "within minInterval of the last start", setup: func(e *releaseEnv) {
			last := e.waiting(1, "m0")
			last.State, last.Version, last.StartedAt = aiv1.ReleaseReleased, "2026.11.1", &metav1.Time{Time: e.now.Add(-5 * time.Minute)}
			e.recs = []aiv1.ReleaseRecord{last, e.waiting(2, "m1")}
		}, polls: []releasePoll{
			{want: []aiv1.ReleaseState{aiv1.ReleaseReleased, aiv1.ReleaseWaiting}, reason: "at most one release per 15m0s: waits until 2026-11-10 12:10 UTC"},
			{do: func(e *releaseEnv) { e.now = e.now.Add(10 * time.Minute) }, want: []aiv1.ReleaseState{aiv1.ReleaseReleased, aiv1.ReleaseRunning}},
		}, pipelines: 1},
		{name: "the final release does not wait for minInterval", setup: func(e *releaseEnv) {
			last := e.waiting(1, "m0")
			last.State, last.Version, last.StartedAt = aiv1.ReleaseReleased, "2026.11.1", &metav1.Time{Time: e.now.Add(-5 * time.Minute)}
			final := e.waiting(2, "m1")
			final.By, final.Final = aiv1.ReleaseByFinal, true
			e.recs = []aiv1.ReleaseRecord{last, final}
		}, polls: []releasePoll{{want: []aiv1.ReleaseState{aiv1.ReleaseReleased, aiv1.ReleaseRunning}}}, pipelines: 1},
		{name: "success with a tag", setup: func(e *releaseEnv) {
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
		}, polls: []releasePoll{
			{want: []aiv1.ReleaseState{aiv1.ReleaseRunning}},
			{do: func(e *releaseEnv) { e.gl.SetPipelineStatus(e.pipeline(1), "running") }, want: []aiv1.ReleaseState{aiv1.ReleaseRunning}},
			{do: func(e *releaseEnv) {
				e.gl.SetPipelineStatus(e.pipeline(1), "success")
				e.tag("2026.11.2", "h1", "m1", "m2")
			}, want: []aiv1.ReleaseState{aiv1.ReleaseReleased}},
		}, pipelines: 1, version: "2026.11.2"},
		{name: "success without a tag is a failed attempt", setup: func(e *releaseEnv) {
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
		}, polls: []releasePoll{
			{want: []aiv1.ReleaseState{aiv1.ReleaseRunning}},
			{do: func(e *releaseEnv) { e.gl.SetPipelineStatus(e.pipeline(1), "success") }, want: []aiv1.ReleaseState{aiv1.ReleaseWaiting},
				reason: "attempt 1 failed: the pipeline passed but no tag contains m1: does its CI know AGENT_RELEASE?; the retry waits: at most one release per 15m0s"},
		}, pipelines: 1},
		{name: "failed, retried, Failed", setup: func(e *releaseEnv) {
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
		}, polls: []releasePoll{
			{want: []aiv1.ReleaseState{aiv1.ReleaseRunning}},
			{do: func(e *releaseEnv) { e.gl.SetPipelineStatus(e.pipeline(1), "failed") }, want: []aiv1.ReleaseState{aiv1.ReleaseWaiting},
				reason: "attempt 1 failed: the release pipeline https://gitlab.example/group/app/-/pipelines/"},
			{do: func(e *releaseEnv) { e.now = e.now.Add(15 * time.Minute) }, want: []aiv1.ReleaseState{aiv1.ReleaseRunning}},
			{do: func(e *releaseEnv) { e.gl.SetPipelineStatus(e.pipeline(2), "canceled") }, want: []aiv1.ReleaseState{aiv1.ReleaseFailed}, reason: "is canceled"},
			{do: func(e *releaseEnv) { e.now = e.now.Add(time.Hour) }, want: []aiv1.ReleaseState{aiv1.ReleaseFailed}},
		}, pipelines: 2},
		{name: "two waiting records, one pipeline", setup: func(e *releaseEnv) {
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1"), e.waiting(2, "m2")}
		}, polls: []releasePoll{
			{want: []aiv1.ReleaseState{aiv1.ReleaseRunning, aiv1.ReleaseWaiting}, reason: "after the running release"},
			{do: func(e *releaseEnv) {
				e.gl.SetPipelineStatus(e.pipeline(1), "success")
				e.tag("2026.11.2", "h1", "m1", "m2")
			}, want: []aiv1.ReleaseState{aiv1.ReleaseReleased, aiv1.ReleaseReleased}, reason: "contained in 2026.11.2"},
		}, pipelines: 1, version: "2026.11.2"},
		{name: "a running record goes first", setup: func(e *releaseEnv) {
			started := metav1.NewTime(e.now.Add(-time.Hour))
			running := e.waiting(2, "m2")
			running.State, running.Attempts, running.StartedAt = aiv1.ReleaseRunning, 1, &started
			running.PipelineID = e.gl.AddPipeline("h1", "main", "success")
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1"), running}
			e.tag("2026.11.2", "h1", "m1", "m2")
			// #1 is Released (not Waiting after a running release): the
			// running record ended first.
		}, polls: []releasePoll{{want: []aiv1.ReleaseState{aiv1.ReleaseReleased, aiv1.ReleaseReleased}}}, version: "2026.11.2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newReleaseEnv(t)
			tc.setup(e)
			for i, p := range tc.polls {
				if p.do != nil {
					p.do(e)
				}
				e.step()
				var got []aiv1.ReleaseState
				for _, rec := range e.recs {
					got = append(got, rec.State)
				}
				if !slices.Equal(got, p.want) {
					t.Fatalf("poll %d: want %v, got %v: %+v", i+1, p.want, got, e.recs)
				}
				if p.reason == "" {
					continue
				}
				if rec := e.recs[len(e.recs)-1]; !strings.Contains(rec.Reason, p.reason) {
					t.Fatalf("poll %d: want a reason with %q, got %q", i+1, p.reason, rec.Reason)
				}
			}
			if n := len(e.gl.CreatedPipelines); n != tc.pipelines {
				t.Fatalf("want %d release pipelines, got %d: %+v", tc.pipelines, n, e.gl.CreatedPipelines)
			}
			last := e.recs[len(e.recs)-1]
			if last.Version != tc.version {
				t.Fatalf("want version %q, got %+v", tc.version, last)
			}
			if tc.version != "" && last.ReleaseURL != "https://gitlab.example/group/app/-/releases/"+tc.version {
				t.Fatalf("want the release's URL, got %q", last.ReleaseURL)
			}
			if last.State.Ended() != (last.EndedAt != nil) {
				t.Fatalf("an ended record has EndedAt, others not: %+v", last)
			}
		})
	}
}

// A started release records the pipeline, the head it runs on and what it
// contains, with AGENT_RELEASE and the defused notes as variables; its
// release lists only what no earlier record released.
func TestReleaseTrigger(t *testing.T) {
	e := newReleaseEnv(t)
	earlier := e.waiting(1, "m0")
	earlier.State, earlier.Version, earlier.Issues, earlier.MergeRequests = aiv1.ReleaseReleased, "2026.11.1", []int64{1}, []int64{11}
	rec := e.waiting(2, "m2")
	rec.Why = "cc @alice\n/close the API is live"
	e.recs = []aiv1.ReleaseRecord{earlier, rec}
	e.step()

	got := e.recs[1]
	if got.State != aiv1.ReleaseRunning || got.Attempts != 1 || got.StartedAt == nil || !got.StartedAt.Equal(&metav1.Time{Time: e.now}) {
		t.Fatalf("want a running first attempt started now, got %+v", got)
	}
	if got.PipelineID != e.pipeline(1) || got.PipelineURL == "" || got.ReleasedSHA != "h1" {
		t.Fatalf("want the pipeline and the head h1, got %+v", got)
	}
	if !slices.Equal(got.Issues, []int64{2}) || !slices.Equal(got.MergeRequests, []int64{12}) {
		t.Fatalf("want #2 and !12 (#1 was released before), got %v %v", got.Issues, got.MergeRequests)
	}
	created := e.gl.CreatedPipelines[0]
	notes := "## Why\n\nThe planner released after #2. Reason given by the planner: " + notify.Defuse("cc @alice") + " /close the API is live.\n" +
		"\n## Contains\n\n- #2 UI (!12)\n" +
		"\nMilestone: [Checkout v2](https://gitlab.example/group/app/-/milestones/3)\n"
	want := []gitlab.PipelineVariable{{Key: "AGENT_RELEASE", Value: "true"}, {Key: "AGENT_RELEASE_NOTES", Value: notes}}
	if created.Ref != "main" || !slices.Equal(created.Variables, want) {
		t.Fatalf("want a pipeline on main with %+v, got %+v", want, created)
	}
}

// releaseAutopilot is the milestone of e with its tracking issue #100 and
// e.recs as its releases.
func (e *releaseEnv) releaseAutopilot() *autopilot {
	ms := e.x.milestone
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &ms}}
	st.Status.Plan = []aiv1.PlannedIssue{{Key: "api", IID: 1, Title: "API"}, {Key: "ui", IID: 2, Title: "UI"}}
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, TrackingIssueIID: 100, Releases: e.recs,
		Items: []aiv1.AutopilotItem{{IID: 1, State: aiv1.AutopilotItemMerged, MergeRequestIID: 11, MergedSHA: "m1"},
			{IID: 2, State: aiv1.AutopilotItemMerged, MergeRequestIID: 12, MergedSHA: "m2"}}}
	return &autopilot{r: e.x.r, gl: e.gl, spec: e.x.spec, st: st, ap: ap, log: logr.Discard()}
}

// notes are the bodies of the comments on the tracking issue.
func (e *releaseEnv) notes() []string {
	var bodies []string
	for _, n := range e.gl.IssueNotes[100] {
		bodies = append(bodies, n.Body)
	}
	return bodies
}

// A release that runs gets one comment when it starts and one when it ends,
// across polls and its retried attempt; a non-final one that failed says
// the queue goes on.
func TestReleaseComments(t *testing.T) {
	start := "🤖 🚀 Release started (planner): users see the API. Contains #1, #2. Pipeline https://gitlab.example/group/app/-/pipelines/"
	for _, tc := range []struct {
		name string
		end  func(e *releaseEnv)
		want string
	}{
		{name: "released", end: func(e *releaseEnv) {
			e.gl.SetPipelineStatus(e.pipeline(2), "success")
			e.tag("2026.11.2", "h1", "m1", "m2")
		}, want: "🤖 📦 Released 2026.11.2: https://gitlab.example/group/app/-/releases/2026.11.2"},
		{name: "failed", end: func(e *releaseEnv) { e.gl.SetPipelineStatus(e.pipeline(2), "failed") },
			want: "🤖 ❌ The release failed twice: the release pipeline https://gitlab.example/group/app/-/pipelines/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newReleaseEnv(t)
			e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
			a := e.releaseAutopilot()
			polls := []func(){
				func() {},
				func() {},
				func() { e.gl.SetPipelineStatus(e.pipeline(1), "failed") },
				func() { e.now = e.now.Add(15 * time.Minute) }, // the retry: the same run
				func() { tc.end(e) },
				func() { e.now = e.now.Add(time.Hour) },
			}
			for i, do := range polls {
				do()
				if err := a.releases(e.ctx); err != nil {
					t.Fatalf("poll %d: %v", i+1, err)
				}
			}
			got := e.notes()
			if len(got) != 2 || !strings.HasPrefix(got[0], start) || !strings.HasPrefix(got[1], tc.want) {
				t.Fatalf("want a start comment %q and an end comment %q, got %q", start, tc.want, got)
			}
			if tc.name == "failed" && !strings.HasSuffix(got[1], "The queue goes on.") {
				t.Fatalf("a failed non-final release goes on: %q", got[1])
			}
			if rec := a.ap.Releases[0]; rec.Announced != rec.State {
				t.Fatalf("want the end announced, got %+v", rec)
			}
		})
	}
}

// A comment that fails is posted on the next poll, once.
func TestReleaseCommentRetried(t *testing.T) {
	e := newReleaseEnv(t)
	e.recs = []aiv1.ReleaseRecord{e.waiting(1, "m1")}
	a := e.releaseAutopilot()
	e.gl.NoteErrors = map[int64]error{100: errors.New("gitlab is down")}
	if err := a.releases(e.ctx); err == nil {
		t.Fatal("want the failing comment's error")
	}
	if rec := a.ap.Releases[0]; rec.State != aiv1.ReleaseRunning || rec.Announced != "" || len(e.notes()) != 0 {
		t.Fatalf("want a running release not announced yet, got %+v %q", rec, e.notes())
	}
	e.gl.NoteErrors = nil
	for range 2 {
		if err := a.releases(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.notes()); n != 1 || len(e.gl.CreatedPipelines) != 1 {
		t.Fatalf("want one start comment and one pipeline, got %q %+v", e.notes(), e.gl.CreatedPipelines)
	}
}

// The final record's end is commented also without a run; other records
// that never ran, and dropped ones, are not.
func TestReleaseAnnouncement(t *testing.T) {
	final := func(state aiv1.ReleaseState, reason string) aiv1.ReleaseRecord {
		return aiv1.ReleaseRecord{By: aiv1.ReleaseByFinal, Final: true, Why: finalWhy, State: state, Reason: reason,
			Version: "2026.11.2", ReleaseURL: "https://gitlab.example/group/app/-/releases/2026.11.2"}
	}
	planner := func(state, announced aiv1.ReleaseState) aiv1.ReleaseRecord {
		return aiv1.ReleaseRecord{By: aiv1.ReleaseByPlanner, State: state, Announced: announced, Reason: "contained in 2026.11.2", Version: "2026.11.2"}
	}
	for _, tc := range []struct {
		name string
		rec  aiv1.ReleaseRecord
		want string
	}{
		{name: "final already released", rec: final(aiv1.ReleaseAlreadyReleased, "the tag 2026.11.2 already contains m1"),
			want: "📦 Already released in 2026.11.2 (the tag 2026.11.2 already contains m1): https://gitlab.example/group/app/-/releases/2026.11.2"},
		{name: "final not needed", rec: final(aiv1.ReleaseNotNeeded, "nothing release-relevant since 2026.11.1"),
			want: "📦 No release needed: nothing release-relevant since 2026.11.1."},
		{name: "final not applicable", rec: final(aiv1.ReleaseNotApplicable, "group/app does not tag its default branch"),
			want: "📦 No release: group/app does not tag its default branch."},
		{name: "final failed", rec: final(aiv1.ReleaseFailed, "the release pipeline https://gitlab.example/p/2 is failed"),
			want: "❌ The final release failed twice: the release pipeline https://gitlab.example/p/2 is failed. The milestone is not done without it."},
		{name: "final released without a run of its own", rec: final(aiv1.ReleaseReleased, "contained in 2026.11.2"),
			want: "📦 Released 2026.11.2: https://gitlab.example/group/app/-/releases/2026.11.2"},
		{name: "final dropped", rec: final(aiv1.ReleaseDropped, "ended without a release by @alice")},
		{name: "final announced", rec: func() aiv1.ReleaseRecord {
			r := final(aiv1.ReleaseNotNeeded, "no change since 2026.11.1")
			r.Announced = r.State
			return r
		}()},
		{name: "final retried after it failed", rec: func() aiv1.ReleaseRecord {
			r := final(aiv1.ReleaseRunning, "")
			r.Announced, r.Issues = aiv1.ReleaseFailed, []int64{2}
			return r
		}(), want: "🚀 Release started (final): the milestone's last merge. Contains #2."},
		{name: "contained in another release, never ran", rec: planner(aiv1.ReleaseReleased, "")},
		{name: "contained in another release after a failed attempt", rec: planner(aiv1.ReleaseReleased, aiv1.ReleaseRunning),
			want: "📦 Released 2026.11.2."},
		{name: "waiting for its retry", rec: planner(aiv1.ReleaseWaiting, aiv1.ReleaseRunning)},
		{name: "an owner's release", rec: aiv1.ReleaseRecord{By: aiv1.ReleaseByOwner, Actor: "@alice", Why: "[x](http://evil) <img>", State: aiv1.ReleaseRunning},
			want: "🚀 Release started (" + notify.Defuse("@alice") + "): \\[x\\]\\(http:\u200b//evil\\) \\<img\\>. Contains no new issue of the milestone."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseAnnouncement(tc.rec); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// The autopilot runs its release records on every poll, also while paused,
// and an already released one does not pause it.
func TestAutopilotRunsReleasesWhilePaused(t *testing.T) {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.gl.AddNote(e.autopilot().TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotPaused {
		t.Fatalf("want paused, got %+v", e.autopilot())
	}
	created := time.Now().Add(-time.Hour)
	e.gl.AddTag("2026.11.2", gitlab.TagCommit{ID: "t2", ParentIDs: []string{"p1", "p2"}, CreatedAt: &created}, "m1")
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Releases = []aiv1.ReleaseRecord{{ID: 1, By: aiv1.ReleaseByOwner, SHA: "m1", State: aiv1.ReleaseWaiting, DecidedAt: metav1.Now()}}
	})
	e.reconcile()
	ap := e.autopilot()
	if got := ap.Releases[0]; got.State != aiv1.ReleaseAlreadyReleased || got.Version != "2026.11.2" {
		t.Fatalf("want already released in 2026.11.2, got %+v", got)
	}
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner || len(e.gl.CreatedPipelines) != 0 {
		t.Fatalf("want still the owner's pause and no pipeline, got %+v %+v", ap, e.gl.CreatedPipelines)
	}
}

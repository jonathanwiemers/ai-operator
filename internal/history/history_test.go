package history

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

var now = time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)

func task(rv string, mut func(*aiv1.AgentTask)) *aiv1.AgentTask {
	t := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "bloop-148", Namespace: "agents", UID: "u1", ResourceVersion: rv},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "bloop", RunRequest: 3,
			Issue: &aiv1.IssueRef{IID: 148}, Milestone: &aiv1.MilestoneRef{IID: 20}},
	}
	if mut != nil {
		mut(t)
	}
	return t
}

func kinds(es []store.Event) []string {
	var ks []string
	for _, e := range es {
		ks = append(ks, e.Kind+" "+e.Key)
	}
	return ks
}

func TestDifferTransitions(t *testing.T) {
	cases := []struct {
		name     string
		old, cur func(*aiv1.AgentTask)
		want     []string
	}{
		{"nothing changed", nil, nil, nil},
		{"run starts", nil, func(t *aiv1.AgentTask) { t.Status.Phase = aiv1.TaskPhaseRunning },
			[]string{"run.started task/u1/run/3/start"}},
		{"push", nil, func(t *aiv1.AgentTask) { t.Status.Run.PushedSHA = "abcdef0123" },
			[]string{"push task/u1/push/abcdef0123"}},
		{"ci red", nil, func(t *aiv1.AgentTask) {
			t.Status.CI = &aiv1.CIStatus{State: aiv1.CIStateFailed, PipelineID: 8841, FailedJobs: []string{"test:race"}}
		}, []string{"ci task/u1/ci/8841/Failed"}},
		{"ci running is no event", nil, func(t *aiv1.AgentTask) {
			t.Status.CI = &aiv1.CIStatus{State: aiv1.CIStateRunning, PipelineID: 1}
		}, nil},
		{"review", nil, func(t *aiv1.AgentTask) {
			t.Status.Run.ReviewedSHA, t.Status.Run.BlockingFindings = "beef", 1
		}, []string{"review task/u1/review/beef"}},
		{"question", nil, func(t *aiv1.AgentTask) {
			t.Status.Run.RunCount, t.Status.Run.LastResult = 1, aiv1.RunResultNeedsInput
		}, []string{"run.finished task/u1/run/1", "needs task/u1/needs/run/1"}},
		{"merged", func(t *aiv1.AgentTask) {
			t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning,
				Items: []aiv1.AutopilotItem{{IID: 142, State: aiv1.AutopilotItemActive}}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning,
				Items: []aiv1.AutopilotItem{{IID: 142, State: aiv1.AutopilotItemMerged, MergeRequestIID: 209}}}
		}, []string{"merged task/u1/merged/142"}},
		{"autopilot pauses", func(t *aiv1.AgentTask) {
			t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
		}, func(t *aiv1.AgentTask) {
			t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedReason: "CI"}
		}, []string{"autopilot task/u1/autopilot/Paused/2"}},
		{"proposal posted, then accepted", func(t *aiv1.AgentTask) {
			t.Status.Pilot = &aiv1.PilotStatus{Proposals: []aiv1.PilotProposal{{ID: 3}}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Pilot = &aiv1.PilotStatus{Proposals: []aiv1.PilotProposal{{ID: 4}},
				Accepted: []aiv1.PilotProposal{{ID: 3, AcceptedBy: "jw"}}}
		}, []string{"proposal task/u1/proposal/4/posted", "proposal task/u1/proposal/3/accepted"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := task("1", c.old)
			cur := task("2", c.cur)
			if c.cur == nil && c.old == nil {
				cur = task("1", nil)
			}
			got := kinds(NewDiffer().Update(old, cur, now))
			if !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestDifferRunDurationAndCost(t *testing.T) {
	d := NewDiffer()
	idle := task("1", func(t *aiv1.AgentTask) { t.Status.Run.CostUSD = "1.50" })
	running := task("2", func(t *aiv1.AgentTask) {
		t.Status.Phase, t.Status.Run.CostUSD = aiv1.TaskPhaseRunning, "1.50"
	})
	done := task("3", func(t *aiv1.AgentTask) {
		t.Status.Phase, t.Status.Run.CostUSD = aiv1.TaskPhaseIdle, "2.75"
		t.Status.Run.RunCount, t.Status.Run.LastResult = 1, aiv1.RunResultSucceeded
	})
	d.Update(idle, running, now)
	es := d.Update(running, done, now.Add(90*time.Second))
	if len(es) != 1 {
		t.Fatalf("events = %v", kinds(es))
	}
	e := es[0]
	if e.Data["durationSec"] != int64(90) || e.Data["costUSD"] != "1.2500" || e.IssueIID != 148 || e.MilestoneIID != 20 {
		t.Errorf("run.finished = %+v", e)
	}
	if e.Project != "bloop" || e.Namespace != "agents" || e.Task != "bloop-148" {
		t.Errorf("event names = %+v", e)
	}
}

func TestDifferDelete(t *testing.T) {
	e := NewDiffer().Delete(task("1", nil), now)
	if e.Kind != KindClosed || e.Key != "task/u1/closed" || e.Summary != "#148 closed" {
		t.Errorf("delete = %+v", e)
	}
}

type fakeGitLab struct {
	gitlab.API
	notes []gitlab.Note
}

func (f fakeGitLab) ListIssueNotes(context.Context, string, int64) ([]gitlab.Note, error) {
	return f.notes, nil
}

func TestWrapGitLabRecordsNotesOnce(t *testing.T) {
	r := NewRecorder(nil, time.Hour, clock.RealClock{}, testLogger(t))
	if _, wrapped := (*Recorder)(nil).WrapGitLab(fakeGitLab{}, Project{}).(*notesAPI); wrapped {
		t.Errorf("nil recorder wrapped the client")
	}
	r.started.Store(true)
	at := now
	api := r.WrapGitLab(fakeGitLab{notes: []gitlab.Note{
		{ID: 1, Body: "/pilot hold merges", Author: gitlab.User{Username: "jw"}, CreatedAt: &at},
		{ID: 2, Body: "changed the label", System: true},
		{ID: 3, Body: "Done.", Author: gitlab.User{Username: "ai-bot"}, CreatedAt: &at},
	}}, Project{Namespace: "agents", Name: "bloop", WebURL: "https://gitlab.com/jo/bloop/", Bot: "ai-bot"})
	for range 2 {
		if _, err := api.ListIssueNotes(t.Context(), "jo/bloop", 138); err != nil {
			t.Fatal(err)
		}
	}
	close(r.queue)
	var got []store.Event
	for e := range r.queue {
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("recorded %d events, want 2: %v", len(got), kinds(got))
	}
	if e := got[0]; e.Key != "note/1" || e.IssueIID != 138 || e.Actor != "jw" ||
		e.Data["url"] != "https://gitlab.com/jo/bloop/-/issues/138#note_1" || e.Data["bot"] != nil || !e.ExpiresAt.Equal(at.Add(time.Hour)) {
		t.Errorf("note 1 = %+v", e)
	}
	if got[1].Data["bot"] != true {
		t.Errorf("bot note = %+v", got[1])
	}
}

func TestRecordDropsWhenNotStartedOrFull(t *testing.T) {
	r := NewRecorder(nil, time.Hour, clock.RealClock{}, testLogger(t))
	r.Record(store.Event{Key: "a"})
	if len(r.queue) != 0 {
		t.Fatal("recorded before start")
	}
	r.started.Store(true)
	for range queueSize + 5 {
		r.Record(store.Event{Key: "a"})
	}
	if r.dropped.Load() != 5 {
		t.Errorf("dropped = %d, want 5", r.dropped.Load())
	}
}

func testLogger(t *testing.T) logr.Logger { return testr.New(t) }

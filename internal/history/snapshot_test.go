package history

import (
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// specTask is the spec task of milestone 20 with a plan of 148 and 149.
func specTask(rv string, mut func(*aiv1.AgentTask)) *aiv1.AgentTask {
	return task(rv, func(t *aiv1.AgentTask) {
		t.Name, t.UID, t.Spec.Type, t.Spec.Issue = "bloop-m20", "spec", aiv1.TaskTypeSpec, nil
		t.Spec.Milestone = &aiv1.MilestoneRef{ID: 9020, IID: 20, Title: "Desk"}
		t.Spec.Owners, t.Spec.OwnerSubjects = []string{"jo"}, []string{"sub-jo"}
		t.Status.Plan = []aiv1.PlannedIssue{{Key: "a", IID: 148, Title: "Schema"}, {Key: "b", IID: 149, Title: "Recorder"}}
		if mut != nil {
			mut(t)
		}
	})
}

func withAutopilot(phase aiv1.AutopilotPhase, items ...aiv1.AutopilotItem) func(*aiv1.AgentTask) {
	return func(t *aiv1.AgentTask) {
		t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: phase, TrackingIssueIID: 300, Items: items}
	}
}

func TestSnapshot(t *testing.T) {
	bloop := store.ProjectKey{Namespace: "agents", Name: "bloop"}
	base := func(mut func(*store.MilestoneSnapshot)) store.MilestoneSnapshot {
		m := store.MilestoneSnapshot{Project: bloop, IID: 20, MilestoneID: 9020, Title: "Desk", State: "open",
			StartedAt: now, Owners: []string{"jo"}, OwnerSubjects: []string{"sub-jo"},
			Issues: []store.IssueSnapshot{{IID: 148, Title: "Schema", State: "planned"}, {IID: 149, Title: "Recorder", State: "planned"}}}
		if mut != nil {
			mut(&m)
		}
		return m
	}
	posted := metav1.NewTime(now.Add(-time.Hour))
	cases := []struct {
		name string
		task *aiv1.AgentTask
		web  string
		want store.MilestoneSnapshot
	}{
		{name: "no autopilot", task: specTask("1", nil), want: base(nil)},
		{name: "web URL", task: specTask("1", nil), web: "https://gitlab.example/jo/bloop/",
			want: base(func(m *store.MilestoneSnapshot) { m.WebURL = "https://gitlab.example/jo/bloop/-/milestones/20" })},
		{name: "autopilot with items", task: specTask("1", withAutopilot(aiv1.AutopilotRunning,
			aiv1.AutopilotItem{IID: 150, State: aiv1.AutopilotItemPending, FlakyFix: true},
			aiv1.AutopilotItem{IID: 148, State: aiv1.AutopilotItemMerged, MergeRequestIID: 900, MergeRequestURL: "https://gitlab.example/mr/900"},
		)), want: base(func(m *store.MilestoneSnapshot) {
			m.Phase, m.TrackingIID = "running", 300
			m.Issues = []store.IssueSnapshot{
				{IID: 148, Title: "Schema", State: "merged", MergeRequestIID: 900, MergeRequestURL: "https://gitlab.example/mr/900"},
				{IID: 149, Title: "Recorder", State: "planned"},
				{IID: 150, State: "pending"},
			}
		})},
		{name: "done with a retrospective and created follow-ups", task: specTask("1", func(t *aiv1.AgentTask) {
			withAutopilot(aiv1.AutopilotDone)(t)
			t.Status.Pilot = &aiv1.PilotStatus{Retrospective: &aiv1.PilotRetrospective{NoteID: 77, PostedAt: &posted,
				FollowUps: []aiv1.PilotFollowUp{{Title: "Backfill", Body: "later"}, {Title: "Docs"}},
				Created:   map[string]int64{"2": 410}}}
		}), want: base(func(m *store.MilestoneSnapshot) {
			m.Phase, m.TrackingIID, m.DoneAt = "done", 300, now
			m.RetroNoteID, m.RetroPostedAt = 77, posted.Time
			m.FollowUps = []store.FollowUpSnapshot{
				{Number: 1, Title: "Backfill", Body: "later", State: store.FollowUpOpen},
				{Number: 2, Title: "Docs", State: store.FollowUpIssue, TargetIID: 410},
			}
		})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Snapshot(c.task, c.web, now)
			if !ok {
				t.Fatal("no snapshot of a spec task")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Snapshot =\n%+v\nwant\n%+v", got, c.want)
			}
		})
	}
	if _, ok := Snapshot(task("1", nil), "", now); ok {
		t.Error("an implement task has a snapshot")
	}
	if _, ok := Snapshot(specTask("1", func(t *aiv1.AgentTask) { t.Spec.Milestone = nil }), "", now); ok {
		t.Error("a spec task without a milestone has a snapshot")
	}
}

// The queue keeps the newest snapshot per milestone, a close after it, and
// drops new milestones when full, counting them.
func TestSnapshotQueueCoalescesAndDrops(t *testing.T) {
	// Without the research database there is no recorder.
	(*Recorder)(nil).snapshotTask(specTask("1", nil), now)
	(*Recorder)(nil).closeTask(specTask("1", nil), now)
	r := NewRecorder(nil, time.Hour, clocktesting.NewFakeClock(now), testLogger(t))
	r.snapshotTask(specTask("1", nil), now)
	if len(r.snaps) != 0 {
		t.Fatal("queued before start")
	}
	r.started.Store(true)
	r.snapshotTask(specTask("1", nil), now)
	r.snapshotTask(specTask("2", func(t *aiv1.AgentTask) { t.Spec.Milestone.Title = "Desk 2" }), now)
	r.snapshotTask(task("3", nil), now)
	k := snapKey{store.ProjectKey{Namespace: "agents", Name: "bloop"}, 20}
	if len(r.snaps) != 1 || r.snaps[k].save.Title != "Desk 2" || !r.snaps[k].closed.IsZero() {
		t.Fatalf("queue = %+v, want the newest snapshot of milestone 20 only", r.snaps)
	}
	r.closeTask(specTask("2", nil), now.Add(time.Minute))
	if op := r.snaps[k]; op.save == nil || !op.closed.Equal(now.Add(time.Minute)) {
		t.Errorf("after the delete: %+v, want the snapshot and the close", op)
	}
	r.snapshotTask(specTask("4", nil), now)
	if !r.snaps[k].closed.IsZero() {
		t.Error("a new snapshot kept the close")
	}
	for i := range queueSize + 5 {
		r.RecordSnapshot(store.MilestoneSnapshot{Project: k.p, IID: int64(100 + i)})
	}
	// Milestone 20 holds a place: 6 of the new ones find none.
	if len(r.snaps) != queueSize || r.droppedSnaps.Load() != 6 {
		t.Errorf("queued %d, dropped %d; want %d and 6", len(r.snaps), r.droppedSnaps.Load(), queueSize)
	}
	r.RecordClosed(k.p, 20, time.Time{})
	if !r.snaps[k].closed.Equal(now) || r.droppedSnaps.Load() != 6 {
		t.Error("a full queue dropped the close of a queued milestone")
	}
}

// Add, update twice, delete: the rows follow, the repeated update changes
// nothing, the delete closes the milestone and keeps every row.
func TestRecorderWritesSnapshots(t *testing.T) {
	s := storetest.New(t)
	clk := clocktesting.NewFakeClock(now)
	r := NewRecorder(readyDB{s}, time.Hour, clk, testLogger(t))
	r.started.Store(true)
	ctx := t.Context()
	milestone := func() []string {
		t.Helper()
		rows := storetest.Rows(t, `SELECT state, phase, coalesce(done_at::text, ''), updated_at::text, xmin::text
			FROM desk_milestones WHERE namespace = 'agents' AND project = 'bloop' AND iid = 20`)
		if len(rows) != 1 {
			t.Fatalf("%d milestone rows, want 1", len(rows))
		}
		return rows[0]
	}
	issues := func() []string {
		t.Helper()
		var out []string
		for _, row := range storetest.Rows(t, `SELECT iid::text || ' ' || state || ' ' || coalesce(mr_iid::text, '-')
			FROM desk_milestone_issues WHERE milestone_iid = 20 ORDER BY position`) {
			out = append(out, row[0])
		}
		return out
	}

	r.snapshotTask(specTask("1", nil), clk.Now()) // the add
	r.writeSnapshots(ctx)
	if m := milestone(); m[0] != "open" || m[1] != "" || m[2] != "" {
		t.Errorf("after the add: %v", m)
	}
	if got := issues(); !slices.Equal(got, []string{"148 planned -", "149 planned -"}) {
		t.Errorf("issues after the add = %v", got)
	}

	running := specTask("2", withAutopilot(aiv1.AutopilotRunning,
		aiv1.AutopilotItem{IID: 148, State: aiv1.AutopilotItemMerged, MergeRequestIID: 900},
		aiv1.AutopilotItem{IID: 149, State: aiv1.AutopilotItemActive}))
	clk.Step(time.Minute)
	r.snapshotTask(running, clk.Now())
	r.writeSnapshots(ctx)
	after := milestone()
	if after[1] != "running" {
		t.Errorf("after the update: %v", after)
	}
	if got := issues(); !slices.Equal(got, []string{"148 merged 900", "149 active -"}) {
		t.Errorf("issues after the update = %v", got)
	}

	// The same update again, e.g. a status write that changed other fields.
	clk.Step(time.Minute)
	again := running.DeepCopy()
	again.ResourceVersion, again.Status.Message = "3", "polled"
	r.snapshotTask(again, clk.Now())
	r.writeSnapshots(ctx)
	if m := milestone(); !slices.Equal(m, after) {
		t.Errorf("the repeated update changed the milestone: %v, was %v", m, after)
	}

	clk.Step(time.Minute)
	r.closeTask(again, clk.Now())
	r.writeSnapshots(ctx)
	if m := milestone(); m[0] != "closed" || m[1] != "running" || m[2] == "" {
		t.Errorf("after the delete: %v, want closed with a done time", m)
	}
	if got := issues(); !slices.Equal(got, []string{"148 merged 900", "149 active -"}) {
		t.Errorf("issues after the delete = %v", got)
	}
}

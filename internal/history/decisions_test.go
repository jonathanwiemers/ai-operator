package history

import (
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func issueDecisions(issue int64, ids ...string) store.IssueDecisions {
	d := store.IssueDecisions{Project: store.ProjectKey{Namespace: "agents", Name: "orbit"}, MilestoneIID: 20, IssueIID: issue}
	for _, id := range ids {
		d.Decisions = append(d.Decisions, store.DecisionRow{ID: id, Kind: store.DecisionPlanner, Title: "Title " + id,
			Choice: "a", IssueTitle: "Schema", IssueState: "opened", PostedAt: now})
	}
	return d
}

// Without a database there is no recorder; before it started it queues
// nothing.
func TestRecordIssueDecisionsNoop(t *testing.T) {
	(*Recorder)(nil).RecordIssueDecisions(issueDecisions(148, "D148-P1"))
	r := NewRecorder(nil, time.Hour, clocktesting.NewFakeClock(now), testLogger(t))
	r.RecordIssueDecisions(issueDecisions(148, "D148-P1"))
	if len(r.decs) != 0 {
		t.Errorf("queued before start: %+v", r.decs)
	}
}

// The queue keeps the newest decisions per issue and drops new issues when
// full, counting them.
func TestDecisionQueueCoalescesAndDrops(t *testing.T) {
	r := NewRecorder(nil, time.Hour, clocktesting.NewFakeClock(now), testLogger(t))
	r.started.Store(true)
	r.RecordIssueDecisions(issueDecisions(148, "D148-P1"))
	r.RecordIssueDecisions(issueDecisions(148, "D148-P1", "D148-P2"))
	k := decKey{store.ProjectKey{Namespace: "agents", Name: "orbit"}, 148}
	if len(r.decs) != 1 || len(r.decs[k].Decisions) != 2 {
		t.Fatalf("queue = %+v, want the newest decisions of #148 only", r.decs)
	}
	for i := range queueSize + 3 {
		r.RecordIssueDecisions(issueDecisions(int64(1000+i), "D1-1"))
	}
	// #148 holds a place: 4 of the new ones find none.
	if len(r.decs) != queueSize || r.droppedDecs.Load() != 4 {
		t.Errorf("queued %d, dropped %d; want %d and 4", len(r.decs), r.droppedDecs.Load(), queueSize)
	}
	r.RecordIssueDecisions(issueDecisions(148))
	if len(r.decs[k].Decisions) != 0 || r.droppedDecs.Load() != 4 {
		t.Error("a full queue dropped the update of a queued issue")
	}
}

// The queued decisions are written with the snapshots' expiry; a later read
// of the issue replaces them.
func TestRecorderWritesDecisions(t *testing.T) {
	s := storetest.New(t)
	clk := clocktesting.NewFakeClock(now)
	r := NewRecorder(readyDB{s}, time.Hour, clk, testLogger(t))
	r.started.Store(true)
	ctx := t.Context()
	stored := func() [][]string {
		t.Helper()
		return storetest.Rows(t, `SELECT decision_id, issue_iid::text, (expires_at = $1)::text
			FROM desk_decisions WHERE namespace = 'agents' AND project = 'orbit' ORDER BY decision_id`, now.Add(time.Hour))
	}

	r.RecordIssueDecisions(issueDecisions(148, "D148-P1", "D148-P2"))
	r.writeDecisions(ctx)
	if got := stored(); len(got) != 2 || got[0][0] != "D148-P1" || got[1][0] != "D148-P2" || got[0][2] != "true" {
		t.Errorf("after the first write: %v", got)
	}
	if len(r.decs) != 0 {
		t.Errorf("the queue kept %d issues", len(r.decs))
	}

	// A re-plan renumbered the planner's decisions.
	r.RecordIssueDecisions(issueDecisions(148, "D148-P3"))
	r.writeDecisions(ctx)
	if got := stored(); len(got) != 1 || got[0][0] != "D148-P3" {
		t.Errorf("after the re-plan: %v, want D148-P3 only", got)
	}
}

// Each decision ID gets one decision.recorded event, at its PostedAt; a
// re-save writes none again, a new decision one more.
func TestRecorderWritesDecisionEvents(t *testing.T) {
	s := storetest.New(t)
	r := NewRecorder(readyDB{s}, time.Hour, clocktesting.NewFakeClock(now), testLogger(t))
	r.started.Store(true)
	ctx := t.Context()
	events := func() [][]string {
		t.Helper()
		return storetest.Rows(t, `SELECT key, issue_iid::text, (at = $1)::text, summary, data->>'choice', data->>'kind'
			FROM history_events WHERE kind = 'decision.recorded' ORDER BY key`, now)
	}

	for range 2 {
		r.RecordIssueDecisions(issueDecisions(148, "D148-P1", "D148-P2"))
		r.writeDecisions(ctx)
	}
	got := events()
	if len(got) != 2 || got[0][0] != "decision/agents/orbit/D148-P1" || got[0][1] != "148" || got[0][2] != "true" ||
		got[0][3] != "D148-P1 Title D148-P1" || got[0][4] != "a" || got[0][5] != "planner" {
		t.Fatalf("after two saves: %v", got)
	}

	r.RecordIssueDecisions(issueDecisions(148, "D148-P1", "D148-P2", "D148-P3"))
	r.writeDecisions(ctx)
	if got := events(); len(got) != 3 {
		t.Errorf("after a new decision: %v, want 3 events", got)
	}
}

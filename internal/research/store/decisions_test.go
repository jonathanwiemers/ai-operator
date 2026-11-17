package store_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

var posted = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// decision returns a decision with text derived from id.
func decision(id, kind string) store.DecisionRow {
	return store.DecisionRow{
		ID: id, Kind: kind, Title: "Title of " + id, Question: "Question of " + id,
		Choice: "Choice of " + id, Why: "Why of " + id, Alternatives: []string{"Alt A of " + id, "Alt B of " + id},
		RunID: "run-" + id, URL: "https://gitlab.example/notes/" + id,
		IssueTitle: "Issue", IssueState: "opened", PostedAt: posted,
	}
}

func saveDecisions(t *testing.T, s *store.Store, milestone, issue int64, exp time.Time, ds []store.DecisionRow, rs ...store.RevisitRequestRow) {
	t.Helper()
	err := s.SaveIssueDecisions(t.Context(), store.IssueDecisions{
		Project: orbit, MilestoneIID: milestone, IssueIID: issue, Decisions: ds, Revisits: rs,
	}, exp)
	if err != nil {
		t.Fatalf("SaveIssueDecisions(%d) = %v", issue, err)
	}
}

func queryDecisions(t *testing.T, s *store.Store, q store.DecisionQuery) []store.DecisionResult {
	t.Helper()
	q.Project = orbit
	got, err := s.Decisions(t.Context(), q)
	if err != nil {
		t.Fatalf("Decisions(%+v) = %v", q, err)
	}
	return got
}

func ids(rs []store.DecisionResult) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func wantIDs(t *testing.T, what string, got []store.DecisionResult, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if g := ids(got); !slices.Equal(g, want) {
		t.Errorf("%s: IDs %v, want %v", what, g, want)
	}
}

func future() time.Time { return time.Now().Add(24 * time.Hour) }

func TestSaveIssueDecisionsInsert(t *testing.T) {
	s := storetest.New(t)
	d := decision("D42-P1", store.DecisionPlanner)
	d.Supersedes = "D40-2"
	d.Superseded = true
	at := posted.Add(time.Hour)
	saveDecisions(t, s, 21, 42, future(), []store.DecisionRow{d}, store.RevisitRequestRow{
		DecisionID: "D42-P1", Source: store.RevisitComment, RequestedBy: "jo", Instruction: "use EUR",
		URL: "https://gitlab.example/notes/7", At: at,
	})

	got := queryDecisions(t, s, store.DecisionQuery{Milestone: 21})
	if len(got) != 1 {
		t.Fatalf("got %d decisions, want 1", len(got))
	}
	r := got[0]
	if !r.PostedAt.Equal(posted) {
		t.Errorf("PostedAt = %v, want %v", r.PostedAt, posted)
	}
	r.PostedAt = posted
	want := d
	want.MilestoneIID, want.IssueIID, want.Seq = 21, 42, 1
	if !reflect.DeepEqual(r.DecisionRow, want) {
		t.Errorf("row = %+v\nwant %+v", r.DecisionRow, want)
	}
	if r.SupersededBy != "" {
		t.Errorf("SupersededBy = %q, want none", r.SupersededBy)
	}
	if len(r.Revisits) != 1 {
		t.Fatalf("revisits = %+v, want 1", r.Revisits)
	}
	rv := r.Revisits[0]
	if rv.MilestoneIID != 21 || rv.SourceIssueIID != 42 || rv.Source != store.RevisitComment || rv.RequestedBy != "jo" ||
		rv.Instruction != "use EUR" || rv.URL != "https://gitlab.example/notes/7" || rv.FollowUpIID != 0 ||
		rv.FollowUpState != "" || !rv.At.Equal(at) || rv.Applied {
		t.Errorf("revisit = %+v", rv)
	}
}

func TestSaveIssueDecisionsReplace(t *testing.T) {
	s := storetest.New(t)
	fromComment := store.RevisitRequestRow{DecisionID: "D42-P2", Source: store.RevisitComment, RequestedBy: "jo", At: posted}
	saveDecisions(t, s, 21, 42, future(), []store.DecisionRow{
		decision("D42-P1", store.DecisionPlanner), decision("D42-P2", store.DecisionPlanner),
		decision("D42-P3", store.DecisionPlanner),
	}, fromComment)
	// A follow-up's request is read from the follow-up, and stays.
	saveDecisions(t, s, 21, 57, future(), nil, store.RevisitRequestRow{
		DecisionID: "D42-P1", Source: store.RevisitFollowUp, RequestedBy: "ann", FollowUpIID: 57,
		FollowUpState: "opened", At: posted,
	})

	// A re-plan renumbered the planner's decisions: P3 is gone, and the
	// comment's request was edited.
	p1 := decision("D42-P1", store.DecisionPlanner)
	p1.Title = "Renumbered"
	edited := fromComment
	edited.Instruction = "edited"
	saveDecisions(t, s, 21, 42, future(), []store.DecisionRow{p1, decision("D42-P2", store.DecisionPlanner)}, edited)

	got := queryDecisions(t, s, store.DecisionQuery{})
	wantIDs(t, "after replace", got, "D42-P1", "D42-P2")
	if got[0].Title != "Renumbered" {
		t.Errorf("D42-P1 title = %q, want the second save's", got[0].Title)
	}
	if len(got[0].Revisits) != 1 || got[0].Revisits[0].SourceIssueIID != 57 || got[0].Revisits[0].FollowUpIID != 57 {
		t.Errorf("D42-P1 revisits = %+v, want the follow-up's", got[0].Revisits)
	}
	if len(got[1].Revisits) != 1 || got[1].Revisits[0].Instruction != "edited" {
		t.Errorf("D42-P2 revisits = %+v, want only the edited request", got[1].Revisits)
	}

	// Saving an issue with nothing removes its rows; a decision ID that moved
	// to another milestone is updated, not refused.
	saveDecisions(t, s, 22, 42, future(), []store.DecisionRow{decision("D42-P1", store.DecisionPlanner)})
	got = queryDecisions(t, s, store.DecisionQuery{})
	wantIDs(t, "after a move", got, "D42-P1", "D42-P2")
	if got[0].MilestoneIID != 22 {
		t.Errorf("D42-P1 milestone = %d, want 22", got[0].MilestoneIID)
	}
	saveDecisions(t, s, 21, 42, future(), nil)
	wantIDs(t, "after an empty save", queryDecisions(t, s, store.DecisionQuery{}), "D42-P1")
}

func TestDecisionsExpiry(t *testing.T) {
	s := storetest.New(t)
	now := time.Now()
	past := now.Add(-time.Hour)
	saveDecisions(t, s, 21, 41, past, []store.DecisionRow{decision("D41-1", store.DecisionImplementer)},
		store.RevisitRequestRow{DecisionID: "D42-1", Source: store.RevisitComment, At: posted})
	saveDecisions(t, s, 21, 42, future(), []store.DecisionRow{decision("D42-1", store.DecisionImplementer)})

	got := queryDecisions(t, s, store.DecisionQuery{})
	wantIDs(t, "before retention", got, "D42-1")
	if len(got[0].Revisits) != 0 {
		t.Errorf("expired revisit returned: %+v", got[0].Revisits)
	}
	n, err := s.DeleteExpiredDecisions(t.Context(), now)
	if err != nil || n != 2 {
		t.Fatalf("DeleteExpiredDecisions = %d, %v; want 2 (a decision and a revisit)", n, err)
	}
	if c := count(t, s.Pool(), "desk_decisions"); c != 1 {
		t.Errorf("desk_decisions has %d rows, want 1", c)
	}
	if c := count(t, s.Pool(), "desk_decision_revisits"); c != 0 {
		t.Errorf("desk_decision_revisits has %d rows, want 0", c)
	}
	if n, err := s.DeleteExpiredDecisions(t.Context(), now); err != nil || n != 0 {
		t.Errorf("second DeleteExpiredDecisions = %d, %v; want 0", n, err)
	}

	// More than a batch.
	var many []store.DecisionRow
	for i := range 1001 {
		many = append(many, decision(fmt.Sprintf("D43-%d", i+1), store.DecisionImplementer))
	}
	saveDecisions(t, s, 21, 43, past, many)
	if n, err := s.DeleteExpiredDecisions(t.Context(), now); err != nil || n != 1001 {
		t.Errorf("DeleteExpiredDecisions = %d, %v; want 1001", n, err)
	}
}

func TestDecisionsState(t *testing.T) {
	s := storetest.New(t)
	marked := decision("D42-2", store.DecisionImplementer)
	marked.Superseded = true // marker only, successor not stored
	replaced := decision("D42-3", store.DecisionImplementer)
	successor := decision("D42-4", store.DecisionImplementer)
	successor.Supersedes = "D42-3"
	saveDecisions(t, s, 21, 42, future(), []store.DecisionRow{
		decision("D42-1", store.DecisionImplementer), marked, replaced, successor,
	})
	for _, c := range []struct {
		state string
		want  []string
	}{
		{"", []string{"D42-1", "D42-2", "D42-3", "D42-4"}},
		{store.DecisionStateAll, []string{"D42-1", "D42-2", "D42-3", "D42-4"}},
		{"bogus", []string{"D42-1", "D42-2", "D42-3", "D42-4"}},
		{store.DecisionStateActive, []string{"D42-1", "D42-4"}},
		{store.DecisionStateSuperseded, []string{"D42-2", "D42-3"}},
	} {
		got := queryDecisions(t, s, store.DecisionQuery{State: c.state})
		wantIDs(t, "state "+c.state, got, c.want...)
		for _, r := range got {
			if want := r.ID == "D42-2" || r.ID == "D42-3"; r.Superseded != want {
				t.Errorf("state %q: %s Superseded = %v, want %v", c.state, r.ID, r.Superseded, want)
			}
		}
	}
}

func TestDecisionsSearch(t *testing.T) {
	s := storetest.New(t)
	blank := func(id string) store.DecisionRow {
		return store.DecisionRow{ID: id, Kind: store.DecisionImplementer, Title: "plain", PostedAt: posted}
	}
	title, question, choice, why, alt := blank("D42-1"), blank("D42-2"), blank("D42-3"), blank("D42-4"), blank("D42-5")
	title.Title = "Store the Currency"
	question.Question = "Which currency?"
	choice.Choice = "CURRENCY codes"
	why.Why = "currency matters"
	alt.Alternatives = []string{"none", "a currency table"}
	wild := blank("D42-6")
	wild.Why = "100% sure, snake_case"
	saveDecisions(t, s, 21, 42, future(), []store.DecisionRow{title, question, choice, why, alt, wild, blank("D42-7")})

	for _, c := range []struct {
		q    string
		want []string
	}{
		{"currency", []string{"D42-1", "D42-2", "D42-3", "D42-4", "D42-5"}},
		{"  Currency  ", []string{"D42-1", "D42-2", "D42-3", "D42-4", "D42-5"}},
		{"d42-3", []string{"D42-3"}},
		{"D42-7", []string{"D42-7"}},
		{"%", []string{"D42-6"}},
		{"_", []string{"D42-6"}},
		{"e_c", []string{"D42-6"}},
		{"nowhere", nil},
		{"", []string{"D42-1", "D42-2", "D42-3", "D42-4", "D42-5", "D42-6", "D42-7"}},
	} {
		wantIDs(t, "q "+c.q, queryDecisions(t, s, store.DecisionQuery{Q: c.q}), c.want...)
	}
}

// A chain D42-3 → D57-1 → D60-1: each reads with its successor, the first
// two superseded, and a request counts as applied once its successor exists.
func TestDecisionsChain(t *testing.T) {
	s := storetest.New(t)
	saveDecisions(t, s, 21, 42, future(), []store.DecisionRow{decision("D42-3", store.DecisionImplementer)},
		store.RevisitRequestRow{DecisionID: "D42-3", Source: store.RevisitComment, RequestedBy: "jo", At: posted})
	d57 := decision("D57-1", store.DecisionImplementer)
	d57.Supersedes = "D42-3"
	saveDecisions(t, s, 21, 57, future(), []store.DecisionRow{d57},
		store.RevisitRequestRow{DecisionID: "D42-3", Source: store.RevisitFollowUp, FollowUpIID: 57,
			FollowUpState: "closed", At: posted.Add(time.Minute)},
		store.RevisitRequestRow{DecisionID: "D57-1", Source: store.RevisitComment, RequestedBy: "ann",
			At: posted.Add(time.Hour)})
	d60 := decision("D60-1", store.DecisionImplementer)
	d60.Supersedes = "D57-1"
	saveDecisions(t, s, 21, 60, future(), []store.DecisionRow{d60},
		store.RevisitRequestRow{DecisionID: "D57-1", Source: store.RevisitFollowUp, FollowUpIID: 60,
			FollowUpState: "opened", At: posted.Add(2 * time.Hour)},
		store.RevisitRequestRow{DecisionID: "D60-1", Source: store.RevisitComment, At: posted.Add(3 * time.Hour)})

	got := queryDecisions(t, s, store.DecisionQuery{Milestone: 21})
	wantIDs(t, "chain", got, "D42-3", "D57-1", "D60-1")
	type step struct {
		by         string
		superseded bool
		sources    []string
		applied    []bool
	}
	want := map[string]step{
		"D42-3": {"D57-1", true, []string{store.RevisitComment, store.RevisitFollowUp}, []bool{true, true}},
		"D57-1": {"D60-1", true, []string{store.RevisitComment, store.RevisitFollowUp}, []bool{true, true}},
		"D60-1": {"", false, []string{store.RevisitComment}, []bool{false}},
	}
	for _, r := range got {
		w := want[r.ID]
		if r.SupersededBy != w.by || r.Superseded != w.superseded {
			t.Errorf("%s: SupersededBy %q, Superseded %v; want %q, %v", r.ID, r.SupersededBy, r.Superseded, w.by, w.superseded)
		}
		var sources []string
		var applied []bool
		for _, rv := range r.Revisits {
			sources = append(sources, rv.Source)
			applied = append(applied, rv.Applied)
		}
		if !slices.Equal(sources, w.sources) || !slices.Equal(applied, w.applied) {
			t.Errorf("%s: revisits %v applied %v, want %v %v (oldest first)", r.ID, sources, applied, w.sources, w.applied)
		}
	}
}

// Decisions come by issue in plan order (a follow-up the plan does not list
// last), the planner's first, each by number.
func TestDecisionsPlanOrder(t *testing.T) {
	s := storetest.New(t)
	m := snapshot(posted)
	m.Issues = []store.IssueSnapshot{{IID: 303, Title: "First"}, {IID: 301, Title: "Second"}, {IID: 302, Title: "Third"}}
	save(t, s, m, future(), true)
	saveDecisions(t, s, 21, 310, future(), []store.DecisionRow{decision("D310-1", store.DecisionImplementer)})
	saveDecisions(t, s, 21, 309, future(), []store.DecisionRow{decision("D309-1", store.DecisionImplementer)})
	saveDecisions(t, s, 21, 301, future(), []store.DecisionRow{
		decision("D301-10", store.DecisionImplementer), decision("D301-2", store.DecisionImplementer),
		decision("D301-P10", store.DecisionPlanner), decision("D301-P2", store.DecisionPlanner),
	})
	saveDecisions(t, s, 21, 302, future(), []store.DecisionRow{decision("D302-P1", store.DecisionPlanner)})
	saveDecisions(t, s, 21, 303, future(), []store.DecisionRow{decision("D303-1", store.DecisionImplementer)})

	wantIDs(t, "plan order", queryDecisions(t, s, store.DecisionQuery{Milestone: 21}),
		"D303-1", "D301-P2", "D301-P10", "D301-2", "D301-10", "D302-P1", "D309-1", "D310-1")
}

func TestDecisionsAcrossMilestones(t *testing.T) {
	s := storetest.New(t)
	saveDecisions(t, s, 21, 42, future(), []store.DecisionRow{decision("D42-1", store.DecisionImplementer)})
	saveDecisions(t, s, 22, 50, future(), []store.DecisionRow{decision("D50-1", store.DecisionImplementer)})
	saveDecisions(t, s, 21, 41, future(), []store.DecisionRow{decision("D41-1", store.DecisionImplementer)})
	if err := s.SaveIssueDecisions(t.Context(), store.IssueDecisions{
		Project: store.ProjectKey{Namespace: "agents", Name: "other"}, MilestoneIID: 23, IssueIID: 70,
		Decisions: []store.DecisionRow{decision("D70-1", store.DecisionImplementer)},
	}, future()); err != nil {
		t.Fatal(err)
	}

	wantIDs(t, "all milestones", queryDecisions(t, s, store.DecisionQuery{}), "D50-1", "D41-1", "D42-1")
	wantIDs(t, "milestone 21", queryDecisions(t, s, store.DecisionQuery{Milestone: 21}), "D41-1", "D42-1")
	wantIDs(t, "milestone 22", queryDecisions(t, s, store.DecisionQuery{Milestone: 22}), "D50-1")
}

func TestDecisionsLimit(t *testing.T) {
	s := storetest.New(t)
	var many []store.DecisionRow
	for i := range 205 {
		many = append(many, decision(fmt.Sprintf("D42-%d", i+1), store.DecisionImplementer))
	}
	saveDecisions(t, s, 21, 42, future(), many)
	for _, c := range []struct{ limit, want int }{{0, 200}, {-1, 200}, {500, 200}, {5, 5}} {
		got := queryDecisions(t, s, store.DecisionQuery{Limit: c.limit})
		if len(got) != c.want {
			t.Errorf("Limit %d: %d decisions, want %d", c.limit, len(got), c.want)
		}
		if len(got) > 0 && (got[0].ID != "D42-1" || got[len(got)-1].ID != fmt.Sprintf("D42-%d", c.want)) {
			t.Errorf("Limit %d: from %s to %s, want the lowest numbers", c.limit, got[0].ID, got[len(got)-1].ID)
		}
	}
}

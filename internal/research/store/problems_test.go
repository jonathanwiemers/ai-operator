package store_test

import (
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestProblemsOpenResolveReopen(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	orbit := store.ProjectKey{Namespace: "agents", Name: "orbit"}
	ev := func(key, kind string, at time.Duration, issue int64, data map[string]any) store.Event {
		return store.Event{Key: key, Kind: kind, At: base.Add(at), Namespace: "agents", Project: "orbit",
			MilestoneIID: 7, IssueIID: issue, Data: data, ExpiresAt: base.Add(48 * time.Hour)}
	}
	write := func(events ...store.Event) {
		t.Helper()
		if _, err := s.InsertEvents(ctx, events); err != nil {
			t.Fatal(err)
		}
	}
	list := func() []store.Problem {
		t.Helper()
		ps, err := s.Problems(ctx, orbit, 7)
		if err != nil {
			t.Fatal(err)
		}
		return ps
	}

	write(ev("o1", store.KindProblemOpened, 0, 42, map[string]any{"cause": "CI red"}),
		ev("o1-again", store.KindProblemOpened, time.Minute, 42, nil),
		ev("m1", store.KindProblemOpened, 0, 0, map[string]any{"cause": "budget"}))
	ps := list()
	if len(ps) != 2 {
		t.Fatalf("problems = %+v, want 2 (the issue and the milestone)", ps)
	}
	issue := ps[0]
	if ps[0].IssueIID != 42 {
		issue = ps[1]
	}
	if issue.State != "open" || issue.Cause != "CI red" {
		t.Errorf("issue problem = %+v, want open with its first cause kept", issue)
	}
	if open, err := s.OpenProblems(ctx, []store.ProjectKey{orbit}); err != nil || len(open) != 2 {
		t.Errorf("OpenProblems = %v, %v; want 2", open, err)
	}

	// Resolved, then stuck again within a day: the same row reopens.
	write(ev("r1", store.KindProblemResolved, time.Hour, 42, map[string]any{"resolvedBy": "owner"}))
	got, err := s.GetProblem(ctx, orbit, 7, issue.ID)
	if err != nil || got.State != "resolved" || got.ResolvedBy != "owner" || got.ResolvedAt == nil {
		t.Fatalf("after resolve = %+v, %v", got, err)
	}
	// A replayed resolve with the same key changes nothing, nor does it
	// resolve a reopened problem later.
	write(ev("o2", store.KindProblemOpened, 3*time.Hour, 42, map[string]any{"cause": "review failed"}))
	write(ev("r1", store.KindProblemResolved, 4*time.Hour, 42, nil))
	got, _ = s.GetProblem(ctx, orbit, 7, issue.ID)
	if got.State != "open" || got.ReopenedAt == nil || got.Cause != "review failed" || got.ResolvedBy != "" {
		t.Errorf("reopened = %+v, want the same row open with the new cause", got)
	}

	// Resolved and stuck again after more than a day: a new row.
	write(ev("r2", store.KindProblemResolved, 5*time.Hour, 42, map[string]any{"resolvedBy": "pilot"}),
		ev("o3", store.KindProblemOpened, 5*time.Hour+store.ReopenWithin+time.Minute, 42, nil))
	ps = list()
	if len(ps) != 3 || ps[0].IssueIID != 42 || ps[0].State != "open" || ps[0].ID == issue.ID {
		t.Errorf("problems = %+v, want a new open row for #42 first", ps)
	}

	if _, err := s.GetProblem(ctx, orbit, 8, issue.ID); err != store.ErrNoProblem {
		t.Errorf("GetProblem of another milestone = %v, want ErrNoProblem", err)
	}
}

package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestMilestones(t *testing.T) {
	owner := storetest.New(t)
	// The UI lists milestones as the reader.
	s := asReader(t, owner)
	ctx := t.Context()
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	exp := time.Now().Add(24 * time.Hour)
	other := store.ProjectKey{Namespace: "agents", Name: "other"}

	ms := []store.MilestoneSnapshot{
		{Project: bloop, IID: 20, Title: "Desk", State: "open", Phase: "implementing", StartedAt: base, TrackingIID: 300,
			Issues: []store.IssueSnapshot{
				{IID: 148, Title: "Schema", State: "merged", MergeRequestIID: 900},
				{IID: 149, Title: "Dropped", State: "skipped"},
				{IID: 150, Title: "Queries", State: "active"},
			},
			FollowUps: []store.FollowUpSnapshot{
				{Number: 1, Title: "Backfill", State: store.FollowUpOpen},
				{Number: 2, Title: "Docs", State: store.FollowUpOpen},
				{Number: 3, Title: "Seed", State: store.FollowUpIssue, TargetIID: 410},
			}},
		// Started later, no issues and no calls.
		{Project: bloop, IID: 21, Title: "Pilot", State: "open", Phase: "waiting", StartedAt: base.Add(time.Hour)},
		{Project: bloop, IID: 10, Title: "Old", State: "closed", Phase: "done", StartedAt: base, DoneAt: base.Add(2 * time.Hour)},
		{Project: bloop, IID: 11, Title: "Newer", State: "closed", Phase: "done", StartedAt: base, DoneAt: base.Add(3 * time.Hour)},
		// Another project with the same IID and costly calls.
		{Project: other, IID: 20, Title: "Elsewhere", State: "open", StartedAt: base.Add(-time.Hour),
			Issues:    []store.IssueSnapshot{{IID: 148, State: "merged"}},
			FollowUps: []store.FollowUpSnapshot{{Number: 1, State: store.FollowUpOpen}}},
	}
	for _, m := range ms {
		if _, err := owner.SaveMilestone(ctx, m, exp); err != nil {
			t.Fatal(err)
		}
	}

	usd := func(v float64) *float64 { return &v }
	call := func(key string, p store.ProjectKey, milestone, issue int64, cost *float64, in, out int64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: base.Add(time.Hour), Namespace: p.Namespace, Project: p.Name, MilestoneIID: milestone,
			IssueIID: issue, Task: "t-" + key, Run: 1, Seq: 1, Kind: "implement", Role: "implement", Engine: "claude",
			Model: "claude-opus-5-5", InputTokens: in, OutputTokens: out, CostUSD: cost, CostSource: source, ExpiresAt: exp}
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{
		call("c1", bloop, 20, 148, usd(1.5), 10, 20),
		call("c2", bloop, 20, 150, nil, 5, 5),
		call("c3", other, 20, 148, usd(9), 1000, 1000),
		// No milestone: counts for none.
		call("c4", bloop, 0, 0, usd(4), 1, 1),
	}); err != nil {
		t.Fatal(err)
	}
	// Milestone 10's run from before agent_calls: its cost counts, without
	// tokens.
	if _, err := owner.InsertEvents(ctx, []store.Event{{Key: "e1", At: base, Namespace: "agents", Project: "bloop",
		MilestoneIID: 10, IssueIID: 140, Task: "bloop-140", Kind: "run.finished",
		Data: map[string]any{"costUSD": "2.0000", "runCount": 1}, ExpiresAt: exp}}); err != nil {
		t.Fatal(err)
	}

	list := func(q store.MilestoneQuery) []store.MilestoneRow {
		t.Helper()
		rows, err := s.Milestones(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	iids := func(rows []store.MilestoneRow) []int64 {
		var out []int64
		for _, r := range rows {
			out = append(out, r.IID)
		}
		return out
	}

	all := list(store.MilestoneQuery{Projects: []store.ProjectKey{bloop}})
	// Open first by start, then closed newest done first; other never.
	if got, want := iids(all), []int64{20, 21, 11, 10}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for _, r := range all {
		if r.Project != bloop {
			t.Errorf("row of %v listed", r.Project)
		}
	}

	m20 := all[0]
	if m20.Title != "Desk" || m20.Phase != "implementing" || m20.TrackingIID != 300 || !m20.StartedAt.Equal(base) || !m20.DoneAt.IsZero() {
		t.Errorf("milestone 20 = %+v", m20)
	}
	if m20.Issues != 3 || m20.Merged != 2 || m20.Open != 1 || m20.FollowUpsOpen != 2 {
		t.Errorf("milestone 20 progress = %d issues, %d merged, %d open, %d follow-ups open",
			m20.Issues, m20.Merged, m20.Open, m20.FollowUpsOpen)
	}
	if sp := m20.Spend; !sp.CostUSD.Known || sp.CostUSD.USD != 1.5 || sp.Calls != 2 || sp.UnknownCalls != 1 || sp.Tokens() != 40 {
		t.Errorf("milestone 20 spend = %+v, want only bloop's calls", sp)
	}

	m21 := all[1]
	if m21.Issues != 0 || m21.Open != 0 || m21.FollowUpsOpen != 0 {
		t.Errorf("milestone 21 progress = %+v", m21)
	}
	if sp := m21.Spend; sp.CostUSD.Known || sp.CostUSD.USD != 0 || sp.Calls != 0 || sp.Tokens() != 0 {
		t.Errorf("milestone 21 without calls: spend = %+v, want unknown", sp)
	}

	m10 := all[3]
	if !m10.DoneAt.Equal(base.Add(2*time.Hour)) || m10.State != "closed" {
		t.Errorf("milestone 10 = %+v", m10)
	}
	if sp := m10.Spend; !sp.CostUSD.Known || sp.CostUSD.USD != 2 || sp.LegacyCostUSD.USD != 2 || sp.Calls != 0 || sp.Tokens() != 0 {
		t.Errorf("milestone 10 spend = %+v, want the legacy run's cost", sp)
	}

	if got := iids(list(store.MilestoneQuery{Projects: []store.ProjectKey{bloop}, State: "closed"})); !slices.Equal(got, []int64{11, 10}) {
		t.Errorf("closed = %v", got)
	}
	if got := iids(list(store.MilestoneQuery{Projects: []store.ProjectKey{bloop}, State: "open", Limit: 1})); !slices.Equal(got, []int64{20}) {
		t.Errorf("open, limit 1 = %v", got)
	}

	both := list(store.MilestoneQuery{Projects: []store.ProjectKey{bloop, other}, State: "open"})
	if len(both) != 3 || both[0].Project != other || both[0].Spend.CostUSD.USD != 9 || both[0].Merged != 1 || both[0].FollowUpsOpen != 1 {
		t.Errorf("both projects, open = %+v", both)
	}

	if rows := list(store.MilestoneQuery{}); len(rows) != 0 {
		t.Errorf("no projects = %v, want none", rows)
	}
	if _, err := s.Milestones(ctx, store.MilestoneQuery{Projects: []store.ProjectKey{bloop}, State: "merged"}); err == nil {
		t.Error("unknown state: no error")
	}
}

// queryCounter counts the queries of a pool.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// counting returns a store on owner's schema whose queries c counts.
func counting(t *testing.T, owner *store.Store, c *queryCounter) *store.Store {
	t.Helper()
	var schema string
	if err := owner.Pool().QueryRow(t.Context(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	pc, err := pgxpool.ParseConfig(os.Getenv("AI_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	pc.ConnConfig.Tracer = c
	s, err := store.OpenPool(t.Context(), pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMilestoneDetail(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	base := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	now := base.Add(5 * time.Hour)
	exp := time.Now().Add(24 * time.Hour)
	posted := base.Add(4 * time.Hour)

	m := store.MilestoneSnapshot{Project: bloop, IID: 30, Title: "Detail", State: "open", Phase: "implementing",
		StartedAt: base, TrackingIID: 301, Owners: []string{"jo"}, OwnerSubjects: []string{"sub-jo"},
		RetroNoteID: 77, RetroText: "It went **well**.", RetroPostedAt: posted,
		Issues: []store.IssueSnapshot{
			{IID: 160, Title: "Merged", State: "merged", MergeRequestIID: 901, MergeRequestURL: "https://gitlab.example/mr/901"},
			{IID: 161, Title: "Running", State: "active"},
			{IID: 162, Title: "Never run", State: "pending"},
			{IID: 163, Title: "Paused", State: "active"},
		},
		FollowUps: []store.FollowUpSnapshot{
			{Number: 2, Title: "Docs", Body: "Write them", State: store.FollowUpOpen},
			{Number: 1, Title: "Seed", State: store.FollowUpIssue, TargetIID: 410},
		},
		Releases: []store.ReleaseSnapshot{
			{ID: 2, By: "final", State: "Planned", DecidedAt: base.Add(4 * time.Hour)},
			{ID: 1, By: "planner", Why: "Users see it", AfterIID: 160, SHA: "abc", State: "Released", Issues: []int64{160},
				MergeRequests: []int64{901}, Version: "2026.11.3", DecidedAt: base, StartedAt: base.Add(3 * time.Hour),
				EndedAt: base.Add(4 * time.Hour)},
		}}
	if _, err := owner.SaveMilestone(ctx, m, exp); err != nil {
		t.Fatal(err)
	}
	// Another milestone of the same project, without issues.
	if _, err := owner.SaveMilestone(ctx, store.MilestoneSnapshot{Project: bloop, IID: 31, State: "open"}, exp); err != nil {
		t.Fatal(err)
	}

	ev := func(key string, at time.Time, milestone, issue int64, kind string, data map[string]any, expires time.Time) store.Event {
		return store.Event{Key: key, At: at, Namespace: bloop.Namespace, Project: bloop.Name, MilestoneIID: milestone,
			IssueIID: issue, Task: "bloop-" + key, Kind: kind, Data: data, ExpiresAt: expires}
	}
	pause := func(task, kind string, at, start time.Time, pk string, item int64) store.Event {
		d := map[string]any{"kind": pk}
		if item != 0 {
			d["item"] = item
		}
		if kind == "autopilot.resumed" {
			d["resolvedBy"], d["pausedSec"] = "owner", int64(at.Sub(start).Seconds())
		}
		return ev(fmt.Sprintf("task/%s/%s/%d/%s", task, kind, start.Unix(), pk), at, 30, 301, kind, d, exp)
	}
	events := []store.Event{
		// 160 ran twice and merged: wall time from the first run to the merge.
		ev("r160a", base, 30, 160, "run.started", nil, exp),
		ev("r160b", base.Add(time.Hour), 30, 160, "run.started", nil, exp),
		ev("m160", base.Add(3*time.Hour), 30, 160, "merged", nil, exp),
		// 161 runs: wall time up to now. Its expired and other milestone's
		// earlier runs do not count.
		ev("r161", base.Add(2*time.Hour), 30, 161, "run.started", nil, exp),
		ev("r161old", base.Add(-time.Hour), 30, 161, "run.started", nil, now.Add(-time.Minute)),
		ev("r161m31", base.Add(-2*time.Hour), 31, 161, "run.started", nil, exp),
		// 163 paused twice, the second still open.
		ev("r163", base.Add(30*time.Minute), 30, 163, "run.started", nil, exp),
		pause("u3", "autopilot.paused", base.Add(time.Hour), base.Add(time.Hour), "stuck", 163),
		pause("u3", "autopilot.resumed", base.Add(70*time.Minute), base.Add(time.Hour), "stuck", 163),
		pause("u3", "autopilot.paused", base.Add(2*time.Hour), base.Add(2*time.Hour), "ciRed", 163),
		// A pause of the milestone, no item: counts for no issue.
		pause("u9", "autopilot.paused", base.Add(20*time.Minute), base.Add(20*time.Minute), "budget", 0),
		pause("u9", "autopilot.resumed", base.Add(25*time.Minute), base.Add(20*time.Minute), "budget", 0),
	}
	if _, err := owner.InsertEvents(ctx, events); err != nil {
		t.Fatal(err)
	}

	usd := func(v float64) *float64 { return &v }
	call := func(key string, milestone, issue int64, cost *float64, ms int64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: base.Add(time.Hour), Namespace: bloop.Namespace, Project: bloop.Name,
			MilestoneIID: milestone, IssueIID: issue, Task: "t-" + key, Run: 1, Seq: 1, Kind: "implement", Role: "implement",
			Engine: "claude", Model: "claude-opus-5-5", InputTokens: 10, OutputTokens: 5, CostUSD: cost, CostSource: source,
			DurationMS: ms, ExpiresAt: exp}
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{
		call("c160a", 30, 160, usd(1.25), 1000),
		call("c160b", 30, 160, nil, 2500),
		call("c161", 30, 161, usd(2), 4000),
		// The milestone's own call (pilot) and another milestone's: no issue's.
		call("c30", 30, 0, usd(3), 100),
		call("c31", 31, 160, usd(7), 9000),
	}); err != nil {
		t.Fatal(err)
	}

	d, err := s.Milestone(ctx, bloop, 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Detail" || d.Phase != "implementing" || d.TrackingIID != 301 || d.MilestoneRow.Issues != 4 || d.Merged != 1 ||
		d.Open != 3 || d.FollowUpsOpen != 1 {
		t.Errorf("milestone row = %+v", d.MilestoneRow)
	}
	if sp := d.Spend; sp.CostUSD.USD != 6.25 || sp.Calls != 4 || sp.DurationMS != 7600 {
		t.Errorf("milestone spend = %+v, want its four calls", sp)
	}
	if d.RetroText != "It went **well**." || !d.RetroPostedAt.Equal(posted) || d.RetroNoteID != 77 ||
		!slices.Equal(d.Owners, []string{"jo"}) || !slices.Equal(d.OwnerSubjects, []string{"sub-jo"}) {
		t.Errorf("retrospective and owners = %q %v %d %v %v", d.RetroText, d.RetroPostedAt, d.RetroNoteID, d.Owners, d.OwnerSubjects)
	}
	wantFollowUps := []store.FollowUpRow{
		{Number: 1, Title: "Seed", State: store.FollowUpIssue, TargetIID: 410},
		{Number: 2, Title: "Docs", Body: "Write them", State: store.FollowUpOpen},
	}
	if !slices.Equal(d.FollowUps, wantFollowUps) {
		t.Errorf("follow-ups = %+v", d.FollowUps)
	}
	if len(d.Releases) != 2 || !equalRelease(d.Releases[0], m.Releases[1]) || !equalRelease(d.Releases[1], m.Releases[0]) {
		t.Errorf("releases = %+v, want IDs 1, 2 as saved", d.Releases)
	}

	var got []int64
	for _, is := range d.Issues {
		got = append(got, is.IID)
	}
	if !slices.Equal(got, []int64{160, 161, 162, 163}) {
		t.Fatalf("issues = %v, want them in order", got)
	}
	i160, i161, i162, i163 := d.Issues[0], d.Issues[1], d.Issues[2], d.Issues[3]
	if i160.Title != "Merged" || i160.State != "merged" || i160.MRIID != 901 || i160.MRURL != "https://gitlab.example/mr/901" ||
		i160.Position != 0 || i163.Position != 3 {
		t.Errorf("issue 160 = %+v", i160)
	}
	if i160.FirstRun == nil || !i160.FirstRun.Equal(base) || i160.MergedAt == nil || !i160.MergedAt.Equal(base.Add(3*time.Hour)) ||
		i160.WallSec != 3*3600 {
		t.Errorf("issue 160 wall time = %v to %v, %ds; want 3h to the merge", i160.FirstRun, i160.MergedAt, i160.WallSec)
	}
	if i160.AgentMS != 3500 || !i160.Spend.CostUSD.Known || i160.Spend.CostUSD.USD != 1.25 || i160.Spend.UnknownCalls != 1 ||
		i160.Spend.Tokens() != 30 {
		t.Errorf("issue 160 agent time and spend = %d, %+v", i160.AgentMS, i160.Spend)
	}
	if i161.FirstRun == nil || !i161.FirstRun.Equal(base.Add(2*time.Hour)) || i161.MergedAt != nil || i161.WallSec != 3*3600 {
		t.Errorf("issue 161 wall time = %v to %v, %ds; want 3h to now", i161.FirstRun, i161.MergedAt, i161.WallSec)
	}
	if i161.AgentMS != 4000 || i161.Spend.CostUSD.USD != 2 || i161.PausedSec != 0 {
		t.Errorf("issue 161 = %+v", i161)
	}
	if i162.FirstRun != nil || i162.MergedAt != nil || i162.WallSec != 0 || i162.AgentMS != 0 {
		t.Errorf("issue 162 never ran: %+v", i162)
	}
	if sp := i162.Spend; sp.CostUSD.Known || sp.Calls != 0 || sp.Tokens() != 0 {
		t.Errorf("issue 162 without calls: spend = %+v, want unknown", sp)
	}
	// 10 minutes, then 3 hours open up to now.
	if i163.PausedSec != 600+3*3600 {
		t.Errorf("issue 163 paused %ds, want both pauses", i163.PausedSec)
	}

	one, err := s.IssueFigures(ctx, bloop, 30, 160, now)
	if err != nil {
		t.Fatal(err)
	}
	if one.IID != 160 || one.WallSec != i160.WallSec || one.AgentMS != i160.AgentMS || one.Spend != i160.Spend {
		t.Errorf("IssueFigures(160) = %+v, want %+v", one, i160)
	}
	if one, err = s.IssueFigures(ctx, bloop, 30, 163, now); err != nil || one.PausedSec != i163.PausedSec {
		t.Errorf("IssueFigures(163) = %+v, %v", one, err)
	}

	empty, err := s.Milestone(ctx, bloop, 31, now)
	if err != nil || empty.IID != 31 || len(empty.Issues) != 0 || len(empty.FollowUps) != 0 || len(empty.Releases) != 0 || empty.Spend.CostUSD.USD != 7 {
		t.Errorf("milestone 31 = %+v, %v", empty, err)
	}

	for _, missing := range []func() error{
		func() error { _, err := s.Milestone(ctx, bloop, 99, now); return err },
		func() error {
			_, err := s.Milestone(ctx, store.ProjectKey{Namespace: "agents", Name: "other"}, 30, now)
			return err
		},
		func() error { _, err := s.IssueFigures(ctx, bloop, 30, 999, now); return err },
		func() error { _, err := s.IssueFigures(ctx, bloop, 99, 160, now); return err },
	} {
		if err := missing(); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("missing: err = %v, want not found", err)
		}
	}

	// The number of queries does not grow with the issues.
	var c queryCounter
	cs := counting(t, owner, &c)
	queries := func() int64 {
		t.Helper()
		c.n.Store(0)
		if _, err := cs.Milestone(ctx, bloop, 30, now); err != nil {
			t.Fatal(err)
		}
		return c.n.Load()
	}
	few := queries()
	for i := range 20 {
		m.Issues = append(m.Issues, store.IssueSnapshot{IID: int64(200 + i), State: "pending"})
	}
	if _, err := owner.SaveMilestone(ctx, m, exp); err != nil {
		t.Fatal(err)
	}
	if many := queries(); many != few || few != 6 {
		t.Errorf("queries: %d for 4 issues, %d for 24; want 6 each", few, many)
	}
}

// assertUsesIndex fails unless the plan of query names index. Tiny test
// tables always seq-scan, so sequential scans are off inside the transaction.
func assertUsesIndex(t *testing.T, pool *pgxpool.Pool, query string, args []any, index string) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(plan, "\n"); !strings.Contains(text, index) {
		t.Errorf("plan does not use index %s:\n%s", index, text)
	}
}

package store_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// backfilled is a closed milestone as the backfill reads it from GitLab.
func backfilled(iid int64) store.MilestoneSnapshot {
	done := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	return store.MilestoneSnapshot{
		Project: bloop, IID: iid, MilestoneID: 6000 + iid, Title: "Old", State: "closed", DoneAt: done,
		RetroNoteID: 77, RetroText: "## Retrospective", RetroPostedAt: done,
		Issues: []store.IssueSnapshot{
			{IID: 11, Title: "One", State: "merged", MergeRequestIID: 50, MergeRequestURL: "https://gitlab.example/mr/50"},
			{IID: 12, Title: "Two", State: "closed"},
		},
		FollowUps: []store.FollowUpSnapshot{{Number: 1, Title: "Later", State: store.FollowUpOpen}},
	}
}

func pending(t *testing.T, s *store.Store, candidates []int64, want ...int64) {
	t.Helper()
	got, err := s.BackfillPending(t.Context(), bloop, candidates)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("BackfillPending(%v) = %v, %v; want %v", candidates, got, err, want)
	}
}

func saveBackfilled(t *testing.T, s *store.Store, m store.MilestoneSnapshot, exp time.Time, want bool) {
	t.Helper()
	inserted, err := s.SaveBackfilled(t.Context(), m, exp)
	if err != nil || inserted != want {
		t.Fatalf("SaveBackfilled(%d) = %v, %v; want %v", m.IID, inserted, err, want)
	}
}

func failBackfill(t *testing.T, s *store.Store, iid int64, cause error, want int) {
	t.Helper()
	n, err := s.BackfillFailed(t.Context(), bloop, iid, cause)
	if err != nil || n != want {
		t.Fatalf("BackfillFailed(%d) = %d, %v; want %d", iid, n, err, want)
	}
}

type backfillRow struct {
	status   string
	attempts int
	err      string
	at       time.Time
}

func backfillOf(t *testing.T, s *store.Store, iid int64) backfillRow {
	t.Helper()
	var r backfillRow
	if err := s.Pool().QueryRow(t.Context(), `SELECT status, attempts, error, at FROM desk_backfill
		WHERE namespace = 'agents' AND project = 'bloop' AND milestone_iid = $1`, iid).Scan(&r.status, &r.attempts, &r.err, &r.at); err != nil {
		t.Fatalf("backfill row of %d: %v", iid, err)
	}
	return r
}

// BackfillPending leaves out what is backfilled, stored with issues by the
// recorder or failed three times, and keeps the candidates' order.
func TestBackfillPending(t *testing.T) {
	s := storetest.New(t)
	exp := time.Now().Add(time.Hour)
	pending(t, s, nil)
	saveBackfilled(t, s, backfilled(5), exp, true)
	withIssues := snapshot(time.Time{})
	withIssues.IID = 6
	save(t, s, withIssues, exp, true)
	planning := snapshot(time.Time{})
	planning.IID, planning.Issues = 7, nil
	save(t, s, planning, exp, true)
	for want := range store.BackfillAttempts {
		failBackfill(t, s, 8, errors.New("boom"), want+1)
	}
	failBackfill(t, s, 9, errors.New("boom"), 1)
	// Another project's rows count for none of bloop's.
	other := backfilled(10)
	other.Project = store.ProjectKey{Namespace: "agents", Name: "other"}
	saveBackfilled(t, s, other, exp, true)

	pending(t, s, []int64{10, 9, 8, 7, 6, 5, 9, 11}, 10, 9, 7, 11)
}

func TestSaveBackfilled(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exp := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	m := backfilled(4)
	saveBackfilled(t, s, m, exp, true)

	var title, state, retro string
	var done *time.Time
	var expires time.Time
	if err := s.Pool().QueryRow(ctx, `SELECT title, state, done_at, retro_text, expires_at FROM desk_milestones
		WHERE namespace = 'agents' AND project = 'bloop' AND iid = 4`).Scan(&title, &state, &done, &retro, &expires); err != nil {
		t.Fatal(err)
	}
	if title != "Old" || state != "closed" || done == nil || !done.Equal(m.DoneAt) || retro != m.RetroText || !expires.Equal(exp) {
		t.Errorf("milestone = %q %q %v %q %v", title, state, done, retro, expires)
	}
	got, err := s.Milestone(ctx, bloop, 4, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Issues) != 2 || got.Issues[0].IID != 11 || got.Issues[0].MRIID != 50 || got.Issues[1].State != "closed" ||
		len(got.FollowUps) != 1 || got.FollowUps[0].Title != "Later" {
		t.Errorf("Milestone = %+v", got)
	}
	// The backfill invents no spend: a milestone without calls has none.
	if got.Spend.CostUSD.Known || got.Spend.Calls != 0 || got.Spend.Tokens() != 0 {
		t.Errorf("backfilled milestone has spend %+v; want none", got.Spend)
	}
	first := backfillOf(t, s, 4)
	if first.status != "done" || first.attempts != 0 || first.err != "" {
		t.Errorf("backfill = %+v, want done", first)
	}

	// A repeat changes nothing, not even the done time.
	before := versions(t, s)
	m.Title, m.Issues = "Changed", m.Issues[:1]
	saveBackfilled(t, s, m, exp.Add(time.Hour), false)
	if c := changedRows(before, versions(t, s)); len(c) != 0 {
		t.Errorf("a repeated backfill changed %v", c)
	}
	if again := backfillOf(t, s, 4); again != first {
		t.Errorf("a repeated backfill changed its record: %+v, was %+v", again, first)
	}
}

// The recorder's rows take precedence: the backfill changes none of them,
// adds no issues to a milestone the recorder stored without, and marks the
// milestone done all the same.
func TestSaveBackfilledKeepsRecorderRows(t *testing.T) {
	s := storetest.New(t)
	exp := time.Now().Add(time.Hour)
	recorded := snapshot(time.Now())
	planning := snapshot(time.Time{})
	planning.IID, planning.Issues, planning.FollowUps = 22, nil, nil
	save(t, s, recorded, exp, true)
	save(t, s, planning, exp, true)
	before := versions(t, s)

	for _, iid := range []int64{recorded.IID, planning.IID} {
		m := backfilled(iid)
		m.Issues = append(m.Issues, store.IssueSnapshot{IID: 301, Title: "Overwritten", State: "closed"})
		saveBackfilled(t, s, m, exp.Add(time.Hour), false)
		if r := backfillOf(t, s, iid); r.status != "done" {
			t.Errorf("milestone %d: backfill %+v, want done", iid, r)
		}
	}
	if c := changedRows(before, versions(t, s)); len(c) != 0 {
		t.Errorf("the backfill changed the recorder's rows %v", c)
	}
	pending(t, s, []int64{recorded.IID, planning.IID})
}

func TestBackfillFailed(t *testing.T) {
	s := storetest.New(t)
	exp := time.Now().Add(time.Hour)
	failBackfill(t, s, 3, errors.New("gitlab: 502"), 1)
	failBackfill(t, s, 3, nil, 2)
	pending(t, s, []int64{3}, 3)
	long := strings.Repeat("é", 600) + "\x00"
	failBackfill(t, s, 3, errors.New(long), 3)
	r := backfillOf(t, s, 3)
	if r.status != "failed" || r.attempts != 3 || len(r.err) != 1000 || !strings.HasPrefix(long, r.err) {
		t.Errorf("backfill = %s %d, %d bytes of error; want failed 3, 1000 bytes", r.status, r.attempts, len(r.err))
	}
	// Three failures: skipped.
	pending(t, s, []int64{3})

	// A success after a failure is done, with the failures kept and the
	// error cleared; a later failure leaves it done.
	failBackfill(t, s, 4, errors.New("timeout"), 1)
	saveBackfilled(t, s, backfilled(4), exp, true)
	if r := backfillOf(t, s, 4); r.status != "done" || r.attempts != 1 || r.err != "" {
		t.Errorf("backfill after success = %+v, want done, 1 attempt, no error", r)
	}
	failBackfill(t, s, 4, errors.New("late"), 0)
	if r := backfillOf(t, s, 4); r.status != "done" || r.err != "" {
		t.Errorf("backfill after a late failure = %+v, want done", r)
	}
}

// The backfill restarts where it stopped: what was saved or failed three
// times is not pending, the rest is, also through a new connection.
func TestBackfillRestart(t *testing.T) {
	s := storetest.New(t)
	exp := time.Now().Add(time.Hour)
	all := []int64{1, 2, 3, 4}
	saveBackfilled(t, s, backfilled(1), exp, true)
	failBackfill(t, s, 2, errors.New("boom"), 1)
	// Stopped here; a new operator connection picks up.
	s.Pool().Reset()
	pending(t, s, all, 2, 3, 4)
	saveBackfilled(t, s, backfilled(2), exp, true)
	saveBackfilled(t, s, backfilled(3), exp, true)
	pending(t, s, all, 4)
}

func TestProjectListed(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	at, complete, err := s.ProjectListed(ctx, bloop)
	if err != nil || !at.IsZero() || complete {
		t.Fatalf("ProjectListed before = %v, %v, %v; want never", at, complete, err)
	}
	listed := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	if err := s.SetProjectListed(ctx, bloop, listed, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectListed(ctx, bloop, listed.Add(time.Minute), true); err != nil {
		t.Fatal(err)
	}
	if at, complete, err = s.ProjectListed(ctx, bloop); err != nil || !at.Equal(listed.Add(time.Minute)) || !complete {
		t.Errorf("ProjectListed = %v, %v, %v; want %v, complete", at, complete, err, listed.Add(time.Minute))
	}
	if at, _, err = s.ProjectListed(ctx, store.ProjectKey{Namespace: "agents", Name: "other"}); err != nil || !at.IsZero() {
		t.Errorf("another project = %v, %v; want never", at, err)
	}
}

// A backfilled milestone's record leaves with the milestone; failed records
// have no milestone and stay.
func TestDeleteExpiredSnapshotBackfill(t *testing.T) {
	s := storetest.New(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	saveBackfilled(t, s, backfilled(1), now.Add(-time.Hour), true)
	saveBackfilled(t, s, backfilled(2), now.Add(time.Hour), true)
	failBackfill(t, s, 3, errors.New("boom"), 1)

	if n, err := s.DeleteExpiredSnapshot(t.Context(), now); err != nil || n != 1 {
		t.Fatalf("DeleteExpiredSnapshot = %d, %v; want 1", n, err)
	}
	if n := count(t, s.Pool(), "desk_backfill"); n != 2 {
		t.Errorf("%d backfill rows left, want 2", n)
	}
	backfillOf(t, s, 2)
	backfillOf(t, s, 3)
	pending(t, s, []int64{1, 2, 3}, 1, 3)
}

// MergedIssues takes each issue's newest unexpired merge of the project,
// and leaves out issues without one.
func TestMergedIssues(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	now := time.Now()
	exp := now.Add(time.Hour)
	merged := func(key, project string, issue, mr int64, at, expires time.Time) store.Event {
		return store.Event{Key: key, At: at, Namespace: "agents", Project: project, IssueIID: issue, MergeRequestIID: mr,
			Kind: "merged", Data: map[string]any{"mergeRequestURL": "https://gitlab.example/mr/" + key}, ExpiresAt: expires}
	}
	if _, err := s.InsertEvents(ctx, []store.Event{
		merged("m1", "bloop", 11, 50, now.Add(-2*time.Hour), exp),
		merged("m2", "bloop", 11, 51, now.Add(-time.Hour), exp), // merged again: the newest counts
		merged("m3", "bloop", 12, 52, now.Add(-time.Hour), now.Add(-time.Minute)),
		merged("m4", "other", 13, 53, now.Add(-time.Hour), exp),
		{Key: "r1", At: now, Namespace: "agents", Project: "bloop", IssueIID: 13, MergeRequestIID: 54, Kind: "run.started", ExpiresAt: exp},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.MergedIssues(ctx, bloop, []int64{11, 12, 13, 14}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]store.MergedMR{11: {IID: 51, URL: "https://gitlab.example/mr/m2"}}
	if len(got) != len(want) || got[11] != want[11] {
		t.Errorf("MergedIssues = %+v, want %+v", got, want)
	}
	if got, err := s.MergedIssues(ctx, bloop, nil, now); err != nil || len(got) != 0 {
		t.Errorf("MergedIssues(nil) = %v, %v; want none", got, err)
	}
}

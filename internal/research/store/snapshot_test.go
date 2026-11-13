package store_test

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

var orbit = store.ProjectKey{Namespace: "agents", Name: "orbit"}

func snapshot(started time.Time) store.MilestoneSnapshot {
	return store.MilestoneSnapshot{
		Project: orbit, IID: 21, MilestoneID: 6123456789, Title: "Desk", WebURL: "https://gitlab.example/m/21",
		State: "open", Phase: "implementing", StartedAt: started, TrackingIID: 300,
		Owners: []string{"jo"}, OwnerSubjects: []string{"sub-jo"},
		Issues: []store.IssueSnapshot{
			{IID: 301, Title: "Schema", State: "merged", MergeRequestIID: 900, MergeRequestURL: "https://gitlab.example/mr/900"},
			{IID: 302, Title: "Recorder", State: "running"},
			{IID: 303, Title: "Queries", State: "queued"},
		},
		FollowUps: []store.FollowUpSnapshot{
			{Number: 1, Title: "Backfill", Body: "later", State: store.FollowUpOpen},
		},
	}
}

// versions returns the xmin of every snapshot row, keyed by table and key:
// a row written again gets a new xmin.
func versions(t *testing.T, s *store.Store) map[string]string {
	t.Helper()
	m := map[string]string{}
	rows, _ := s.Pool().Query(t.Context(), `
		SELECT 'm/' || iid, xmin::text FROM desk_milestones
		UNION ALL SELECT 'i/' || milestone_iid || '/' || iid, xmin::text FROM desk_milestone_issues
		UNION ALL SELECT 'f/' || milestone_iid || '/' || number, xmin::text FROM desk_follow_ups
		UNION ALL SELECT 'r/' || milestone_iid || '/' || release_id, xmin::text FROM desk_milestone_releases`)
	var k, v string
	if _, err := pgx.ForEachRow(rows, []any{&k, &v}, func() error { m[k] = v; return nil }); err != nil {
		t.Fatal(err)
	}
	return m
}

// changedRows returns the keys whose xmin differs between before and after,
// or that exist in only one of them.
func changedRows(before, after map[string]string) []string {
	var keys []string
	for k := range maps.Keys(before) {
		if after[k] != before[k] {
			keys = append(keys, k)
		}
	}
	for k := range maps.Keys(after) {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

func save(t *testing.T, s *store.Store, m store.MilestoneSnapshot, exp time.Time, want bool) {
	t.Helper()
	changed, err := s.SaveMilestone(t.Context(), m, exp)
	if err != nil || changed != want {
		t.Fatalf("SaveMilestone = %v, %v; want %v", changed, err, want)
	}
}

func TestSaveMilestoneIdempotent(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	started := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	exp := started.Add(24 * time.Hour)
	m := snapshot(started)
	save(t, s, m, exp, true)

	var title, state, phase, url string
	var milestoneID int64
	var tracking *int64
	var owners, subjects []string
	var gotStarted, done *time.Time
	var updated, expires time.Time
	if err := s.Pool().QueryRow(ctx, `SELECT milestone_id, title, web_url, state, phase, started_at, done_at,
			tracking_iid, owners, owner_subjects, updated_at, expires_at FROM desk_milestones
		WHERE namespace = 'agents' AND project = 'orbit' AND iid = 21`).Scan(&milestoneID, &title, &url, &state, &phase,
		&gotStarted, &done, &tracking, &owners, &subjects, &updated, &expires); err != nil {
		t.Fatal(err)
	}
	if milestoneID != 6123456789 || title != "Desk" || url != m.WebURL || state != "open" || phase != "implementing" ||
		gotStarted == nil || !gotStarted.Equal(started) || done != nil || tracking == nil || *tracking != 300 ||
		!slices.Equal(owners, []string{"jo"}) || !slices.Equal(subjects, []string{"sub-jo"}) || !expires.Equal(exp) {
		t.Errorf("milestone = %d %q %q %q %q %v %v %v %v %v %v", milestoneID, title, url, state, phase, gotStarted, done,
			tracking, owners, subjects, expires)
	}
	type issue struct {
		iid      int64
		title    string
		state    string
		mr       *int64
		url      string
		position int
	}
	rows, _ := s.Pool().Query(ctx, `SELECT iid, title, state, mr_iid, mr_url, position FROM desk_milestone_issues
		WHERE milestone_iid = 21 ORDER BY position`)
	issues, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (issue, error) {
		var i issue
		err := r.Scan(&i.iid, &i.title, &i.state, &i.mr, &i.url, &i.position)
		return i, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 || issues[0].iid != 301 || issues[0].mr == nil || *issues[0].mr != 900 ||
		issues[1].iid != 302 || issues[1].mr != nil || issues[1].position != 1 || issues[2].iid != 303 {
		t.Errorf("issues = %+v", issues)
	}

	// The same snapshot, later and with a later expiry, writes nothing.
	before := versions(t, s)
	save(t, s, m, exp.Add(time.Hour), false)
	if c := changedRows(before, versions(t, s)); len(c) != 0 {
		t.Errorf("repeated save changed %v", c)
	}
	var updated2, expires2 time.Time
	if err := s.Pool().QueryRow(ctx, "SELECT updated_at, expires_at FROM desk_milestones WHERE iid = 21").
		Scan(&updated2, &expires2); err != nil {
		t.Fatal(err)
	}
	if !updated2.Equal(updated) || !expires2.Equal(exp) {
		t.Errorf("updated_at %v, expires_at %v after a repeated save; want %v, %v", updated2, expires2, updated, exp)
	}

	// Without a start time, or with another one, the first start stays.
	m.StartedAt = time.Time{}
	save(t, s, m, exp, false)
	m.StartedAt = started.Add(time.Hour)
	save(t, s, m, exp, false)
}

func TestSaveMilestonePartialUpdate(t *testing.T) {
	s := storetest.New(t)
	started := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	exp := started.Add(24 * time.Hour)
	m := snapshot(started)
	save(t, s, m, exp, true)

	before := versions(t, s)
	m.Issues[1].State = "merged"
	save(t, s, m, exp.Add(time.Hour), true)
	if c := changedRows(before, versions(t, s)); !slices.Equal(c, []string{"i/21/302"}) {
		t.Errorf("changed rows %v, want only i/21/302", c)
	}
	var state string
	var expires time.Time
	if err := s.Pool().QueryRow(t.Context(), "SELECT state, expires_at FROM desk_milestone_issues WHERE iid = 302").
		Scan(&state, &expires); err != nil {
		t.Fatal(err)
	}
	if state != "merged" || !expires.Equal(exp.Add(time.Hour)) {
		t.Errorf("issue 302 = %q, expires %v; want merged, %v", state, expires, exp.Add(time.Hour))
	}

	before = versions(t, s)
	m.FollowUps[0].State, m.FollowUps[0].TargetIID = store.FollowUpIssue, 400
	save(t, s, m, exp, true)
	if c := changedRows(before, versions(t, s)); !slices.Equal(c, []string{"f/21/1"}) {
		t.Errorf("changed rows %v, want only f/21/1", c)
	}

	before = versions(t, s)
	m.Phase = "reviewing"
	save(t, s, m, exp, true)
	if c := changedRows(before, versions(t, s)); !slices.Equal(c, []string{"m/21"}) {
		t.Errorf("changed rows %v, want only m/21", c)
	}

	bad := m
	bad.FollowUps = []store.FollowUpSnapshot{{Number: 2, State: "taken"}}
	if _, err := s.SaveMilestone(t.Context(), bad, exp); err == nil {
		t.Error("SaveMilestone with follow-up state taken succeeded, want a check violation")
	}
}

func TestSaveMilestoneReplan(t *testing.T) {
	s := storetest.New(t)
	exp := time.Now().Add(time.Hour)
	m := snapshot(time.Time{})
	m.FollowUps = append(m.FollowUps, store.FollowUpSnapshot{Number: 2, Title: "Docs", State: store.FollowUpOpen})
	save(t, s, m, exp, true)

	// A re-plan drops 302; the follow-ups are gone from the status.
	m.Issues = []store.IssueSnapshot{m.Issues[0], m.Issues[2]}
	m.FollowUps = nil
	save(t, s, m, exp, true)
	rows, _ := s.Pool().Query(t.Context(), "SELECT iid FROM desk_milestone_issues ORDER BY position")
	iids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || !slices.Equal(iids, []int64{301, 303}) {
		t.Errorf("issues after re-plan = %v, %v; want 301, 303", iids, err)
	}
	if n := count(t, s.Pool(), "desk_follow_ups"); n != 2 {
		t.Errorf("%d follow-ups left, want 2 (never deleted)", n)
	}
	// Saving the re-planned list again changes nothing.
	save(t, s, m, exp, false)

	// Without issues, none are left.
	m.Issues = nil
	save(t, s, m, exp, true)
	if n := count(t, s.Pool(), "desk_milestone_issues"); n != 0 {
		t.Errorf("%d issues left, want 0", n)
	}
}

func TestSaveMilestoneRetrospective(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exp := time.Now().Add(time.Hour)
	posted := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	m := snapshot(time.Time{})
	m.RetroNoteID = 4176159413 // more than an int4
	save(t, s, m, exp, true)
	m.RetroText, m.RetroPostedAt = "## Retrospective\n\nWent well.", posted
	save(t, s, m, exp, true)

	// A stored retrospective stays, and so does a later one's note.
	m.RetroNoteID, m.RetroText, m.RetroPostedAt = 4176159999, "other", posted.Add(time.Hour)
	save(t, s, m, exp, false)
	m.RetroNoteID, m.RetroText, m.RetroPostedAt = 0, "", time.Time{}
	save(t, s, m, exp, false)

	var note int64
	var text string
	var at time.Time
	if err := s.Pool().QueryRow(ctx, "SELECT retro_note_id, retro_text, retro_posted_at FROM desk_milestones").
		Scan(&note, &text, &at); err != nil {
		t.Fatal(err)
	}
	if note != 4176159413 || text != "## Retrospective\n\nWent well." || !at.Equal(posted) {
		t.Errorf("retrospective = %d %q %v", note, text, at)
	}
}

func TestCloseMilestone(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exp := time.Now().Truncate(time.Second).Add(time.Hour)
	done := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	save(t, s, snapshot(time.Time{}), exp, true)

	if changed, err := s.CloseMilestone(ctx, orbit, 21, done, exp.Add(time.Hour)); err != nil || !changed {
		t.Fatalf("CloseMilestone = %v, %v; want true", changed, err)
	}
	before := versions(t, s)
	if changed, err := s.CloseMilestone(ctx, orbit, 21, done.Add(time.Hour), exp); err != nil || changed {
		t.Fatalf("CloseMilestone again = %v, %v; want false", changed, err)
	}
	if c := changedRows(before, versions(t, s)); len(c) != 0 {
		t.Errorf("closing again changed %v", c)
	}
	if changed, err := s.CloseMilestone(ctx, orbit, 99, done, exp); err != nil || changed {
		t.Errorf("CloseMilestone of an unknown milestone = %v, %v; want false", changed, err)
	}
	var state string
	var doneAt, expires time.Time
	if err := s.Pool().QueryRow(ctx, "SELECT state, done_at, expires_at FROM desk_milestones WHERE iid = 21").
		Scan(&state, &doneAt, &expires); err != nil {
		t.Fatal(err)
	}
	if state != "closed" || !doneAt.Equal(done) || !expires.Equal(exp.Add(time.Hour)) {
		t.Errorf("closed milestone = %q %v %v", state, doneAt, expires)
	}
	// A later snapshot of the closed milestone keeps its done time.
	m := snapshot(time.Time{})
	m.State, m.DoneAt = "closed", done.Add(2*time.Hour)
	save(t, s, m, exp, false)
}

func TestDeleteExpiredSnapshot(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	old, keep := snapshot(time.Time{}), snapshot(time.Time{})
	old.IID, keep.IID = 10, 11
	old.Releases = []store.ReleaseSnapshot{{ID: 1, By: "final", State: "Released", DecidedAt: now.Add(-2 * time.Hour)}}
	save(t, s, old, now.Add(-time.Hour), true)
	// A milestone whose rows expired but whose release changed later.
	released := snapshot(time.Time{})
	released.IID = 13
	released.Releases = []store.ReleaseSnapshot{{ID: 1, By: "final", State: "Running", DecidedAt: now.Add(-2 * time.Hour)}}
	save(t, s, released, now.Add(-time.Hour), true)
	released.Releases[0].State = "Released"
	save(t, s, released, now.Add(time.Hour), true)
	save(t, s, keep, now.Add(time.Hour), true)
	// A milestone whose row expired but one of whose issues changed later.
	recent := snapshot(time.Time{})
	recent.IID = 12
	save(t, s, recent, now.Add(-time.Hour), true)
	recent.Issues[0].State = "closed"
	save(t, s, recent, now.Add(time.Hour), true)
	// More than a batch of expired milestones without issues.
	exec(t, s.Pool(), `INSERT INTO desk_milestones (namespace, project, iid, state, updated_at, expires_at)
		SELECT 'agents', 'other', i, 'closed', now(), $1 FROM generate_series(1, 1500) i`, now.Add(-time.Minute))

	n, err := s.DeleteExpiredSnapshot(ctx, now)
	if err != nil || n != 1501 {
		t.Fatalf("DeleteExpiredSnapshot = %d, %v; want 1501", n, err)
	}
	rows, _ := s.Pool().Query(ctx, "SELECT DISTINCT milestone_iid FROM desk_milestone_issues ORDER BY 1")
	left, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || !slices.Equal(left, []int64{11, 12, 13}) {
		t.Errorf("issues left of milestones %v, %v; want 11, 12, 13", left, err)
	}
	if n := count(t, s.Pool(), "desk_milestones"); n != 3 {
		t.Errorf("%d milestones left, want 3", n)
	}
	if n := count(t, s.Pool(), "desk_follow_ups"); n != 3 {
		t.Errorf("%d follow-ups left, want 3", n)
	}
	rows, _ = s.Pool().Query(ctx, "SELECT milestone_iid FROM desk_milestone_releases ORDER BY 1")
	left, err = pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || !slices.Equal(left, []int64{13}) {
		t.Errorf("releases left of milestones %v, %v; want 13", left, err)
	}
}

// TakeFollowUps marks only open follow-ups: a repeat changes nothing, and
// one opened as an issue, dismissed or taken by another milestone stays.
func TestTakeFollowUps(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	started := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	exp := started.Add(24 * time.Hour)
	if _, err := s.FollowUpsOf(ctx, orbit, 21); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("FollowUpsOf without a snapshot = %v, want ErrNotFound", err)
	}
	m := snapshot(started)
	m.FollowUps = []store.FollowUpSnapshot{
		{Number: 1, Title: "Backfill", State: store.FollowUpOpen},
		{Number: 2, Title: "Docs", State: store.FollowUpIssue, TargetIID: 410},
		{Number: 3, Title: "Flaky", State: store.FollowUpDismissed},
		{Number: 4, Title: "Cleanup", State: store.FollowUpMilestone, TargetIID: 24},
		{Number: 5, Title: "Later", State: store.FollowUpOpen},
	}
	save(t, s, m, exp, true)
	take := func(numbers []int, want bool) {
		t.Helper()
		changed, err := s.TakeFollowUps(ctx, orbit, 21, numbers, 25, exp)
		if err != nil || changed != want {
			t.Fatalf("TakeFollowUps(%v) = %v, %v; want %v", numbers, changed, err, want)
		}
	}
	take([]int{1, 2, 3, 4, 9}, true)
	before := versions(t, s)
	take([]int{1, 2, 3, 4, 9}, false)
	if c := changedRows(before, versions(t, s)); len(c) > 0 {
		t.Fatalf("a repeat wrote %v", c)
	}
	got, err := s.FollowUpsOf(ctx, orbit, 21)
	if err != nil || got.TrackingIID != 300 {
		t.Fatalf("FollowUpsOf = %+v, %v", got, err)
	}
	want := []store.FollowUpRow{
		{Number: 1, Title: "Backfill", State: store.FollowUpMilestone, TargetIID: 25},
		{Number: 2, Title: "Docs", State: store.FollowUpIssue, TargetIID: 410},
		{Number: 3, Title: "Flaky", State: store.FollowUpDismissed},
		{Number: 4, Title: "Cleanup", State: store.FollowUpMilestone, TargetIID: 24},
		{Number: 5, Title: "Later", State: store.FollowUpOpen},
	}
	if !slices.Equal(got.FollowUps, want) {
		t.Fatalf("follow-ups =\n%+v\nwant\n%+v", got.FollowUps, want)
	}
}

// equalRelease compares release snapshots, times by instant.
func equalRelease(a, b store.ReleaseSnapshot) bool {
	return a.ID == b.ID && a.By == b.By && a.Actor == b.Actor && a.Why == b.Why && a.AfterIID == b.AfterIID &&
		a.SHA == b.SHA && a.ReleasedSHA == b.ReleasedSHA && a.State == b.State && a.Reason == b.Reason &&
		slices.Equal(a.Issues, b.Issues) && slices.Equal(a.MergeRequests, b.MergeRequests) &&
		a.Version == b.Version && a.PipelineURL == b.PipelineURL && a.ReleaseURL == b.ReleaseURL &&
		a.DecidedAt.Equal(b.DecidedAt) && a.StartedAt.Equal(b.StartedAt) && a.EndedAt.Equal(b.EndedAt)
}

// Releases are written only when they differ and never deleted: a release
// missing from a later snapshot (trimmed from status) is kept.
func TestSaveMilestoneReleases(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	decided := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	exp := decided.Add(24 * time.Hour)
	m := snapshot(decided)
	m.Releases = []store.ReleaseSnapshot{
		{ID: 1, By: "planner", Why: "Users see the Desk", AfterIID: 301, SHA: "a1", ReleasedSHA: "b1", State: "Released",
			Issues: []int64{301}, MergeRequests: []int64{900}, Version: "2026.11.3",
			PipelineURL: "https://gitlab.example/p/1", ReleaseURL: "https://gitlab.example/r/1",
			DecidedAt: decided, StartedAt: decided.Add(time.Hour), EndedAt: decided.Add(2 * time.Hour)},
		{ID: 2, By: "owner", Actor: "@jo", State: "Planned", DecidedAt: decided.Add(3 * time.Hour)},
	}
	save(t, s, m, exp, true)

	var after *int64
	var started, ended *time.Time
	if err := s.Pool().QueryRow(ctx, `SELECT after_iid, started_at, ended_at FROM desk_milestone_releases
		WHERE milestone_iid = 21 AND release_id = 2`).Scan(&after, &started, &ended); err != nil {
		t.Fatal(err)
	}
	if after != nil || started != nil || ended != nil {
		t.Errorf("release 2 after %v, started %v, ended %v; want NULLs", after, started, ended)
	}

	// The same snapshot writes nothing.
	before := versions(t, s)
	save(t, s, m, exp.Add(time.Hour), false)
	if c := changedRows(before, versions(t, s)); len(c) != 0 {
		t.Errorf("repeated save changed %v", c)
	}

	// A changed state and version write that release only.
	m.Releases[1].State, m.Releases[1].Version = "Released", "2026.11.4"
	save(t, s, m, exp.Add(time.Hour), true)
	if c := changedRows(before, versions(t, s)); !slices.Equal(c, []string{"r/21/2"}) {
		t.Errorf("changed rows %v, want only r/21/2", c)
	}
	var state, version string
	var expires time.Time
	if err := s.Pool().QueryRow(ctx, `SELECT state, version, expires_at FROM desk_milestone_releases
		WHERE release_id = 2`).Scan(&state, &version, &expires); err != nil {
		t.Fatal(err)
	}
	if state != "Released" || version != "2026.11.4" || !expires.Equal(exp.Add(time.Hour)) {
		t.Errorf("release 2 = %q %q, expires %v", state, version, expires)
	}

	// Status trimmed release 1: the snapshot keeps it, unchanged.
	before = versions(t, s)
	want := m.Releases
	m.Releases = m.Releases[1:]
	save(t, s, m, exp, false)
	if c := changedRows(before, versions(t, s)); len(c) != 0 {
		t.Errorf("a shorter list changed %v", c)
	}
	d, err := s.Milestone(ctx, orbit, 21, decided)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Releases) != 2 || !equalRelease(d.Releases[0], want[0]) || !equalRelease(d.Releases[1], want[1]) {
		t.Errorf("releases = %+v\nwant %+v", d.Releases, want)
	}
}

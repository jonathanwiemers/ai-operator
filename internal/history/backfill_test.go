package history

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// countingGitLab counts the requests the client would make: one per page
// of 100, at most the call's page cap.
type countingGitLab struct {
	gitlab.API
	mu       sync.Mutex
	requests int
	calls    []string
}

func pages(n, maxPages int) int {
	p := max(1, (n+99)/100)
	if maxPages > 0 {
		p = min(p, maxPages)
	}
	return p
}

func (c *countingGitLab) count(call string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests += n
	c.calls = append(c.calls, call)
}

// take returns the requests counted since the last take.
func (c *countingGitLab) take() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.requests
	c.requests, c.calls = 0, nil
	return n
}

func (c *countingGitLab) ListMilestones(ctx context.Context, project string) ([]gitlab.Milestone, error) {
	ms, err := c.API.ListMilestones(ctx, project)
	c.count("ListMilestones", pages(len(ms), 0))
	return ms, err
}

func (c *countingGitLab) ListClosedMilestones(ctx context.Context, project string, maxPages int) ([]gitlab.Milestone, error) {
	ms, err := c.API.ListClosedMilestones(ctx, project, maxPages)
	c.count("ListClosedMilestones", pages(len(ms), maxPages))
	return ms, err
}

func (c *countingGitLab) ListIssues(ctx context.Context, project string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	is, err := c.API.ListIssues(ctx, project, opts)
	c.count("ListIssues", pages(len(is), opts.MaxPages))
	return is, err
}

func (c *countingGitLab) ListIssueNotes(ctx context.Context, project string, iid int64) ([]gitlab.Note, error) {
	ns, err := c.API.ListIssueNotes(ctx, project, iid)
	c.count("ListIssueNotes", pages(len(ns), 0))
	return ns, err
}

func (c *countingGitLab) ListIssueNotesNewest(ctx context.Context, project string, iid int64, n int) ([]gitlab.Note, error) {
	ns, err := c.API.ListIssueNotesNewest(ctx, project, iid, n)
	c.count("ListIssueNotesNewest", 1)
	return ns, err
}

func (c *countingGitLab) GetIssue(ctx context.Context, project string, iid int64) (*gitlab.Issue, error) {
	c.count("GetIssue", 1)
	return c.API.GetIssue(ctx, project, iid)
}

func (c *countingGitLab) GetMilestone(ctx context.Context, project string, id int64) (*gitlab.Milestone, error) {
	c.count("GetMilestone", 1)
	return c.API.GetMilestone(ctx, project, id)
}

func (c *countingGitLab) GetMergeRequest(ctx context.Context, project string, iid int64) (*gitlab.MergeRequest, error) {
	c.count("GetMergeRequest", 1)
	return c.API.GetMergeRequest(ctx, project, iid)
}

// memBackfill is the backfill's store in memory, with the semantics of the
// research store's backfill functions.
type memBackfill struct {
	mu       sync.Mutex
	saved    map[store.ProjectKey]map[int64]store.MilestoneSnapshot
	recorded map[int64]bool // stored with issues by the recorder
	attempts map[int64]int
	listedAt time.Time
	complete bool
	merged   map[int64]store.MergedMR
	calls    int
	failWith error // every call fails
}

func newMemBackfill() *memBackfill {
	return &memBackfill{saved: map[store.ProjectKey]map[int64]store.MilestoneSnapshot{}, recorded: map[int64]bool{},
		attempts: map[int64]int{}, merged: map[int64]store.MergedMR{}}
}

func (m *memBackfill) call() error {
	m.calls++
	return m.failWith
}

func (m *memBackfill) BackfillPending(_ context.Context, p store.ProjectKey, candidates []int64) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.call(); err != nil {
		return nil, err
	}
	var out []int64
	for _, iid := range candidates {
		_, done := m.saved[p][iid]
		if !done && !m.recorded[iid] && m.attempts[iid] < store.BackfillAttempts && !slices.Contains(out, iid) {
			out = append(out, iid)
		}
	}
	return out, nil
}

func (m *memBackfill) SaveBackfilled(_ context.Context, s store.MilestoneSnapshot, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.call(); err != nil {
		return false, err
	}
	if m.saved[s.Project] == nil {
		m.saved[s.Project] = map[int64]store.MilestoneSnapshot{}
	}
	if _, ok := m.saved[s.Project][s.IID]; ok {
		return false, nil
	}
	m.saved[s.Project][s.IID] = s
	return true, nil
}

func (m *memBackfill) BackfillFailed(_ context.Context, _ store.ProjectKey, iid int64, _ error) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[iid]++
	return m.attempts[iid], m.call()
}

func (m *memBackfill) ProjectListed(context.Context, store.ProjectKey) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listedAt, m.complete, m.call()
}

func (m *memBackfill) SetProjectListed(_ context.Context, _ store.ProjectKey, at time.Time, complete bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listedAt, m.complete = at, complete
	return m.call()
}

func (m *memBackfill) MergedIssues(_ context.Context, _ store.ProjectKey, issues []int64, _ time.Time) (map[int64]store.MergedMR, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int64]store.MergedMR{}
	for _, iid := range issues {
		if mr, ok := m.merged[iid]; ok {
			out[iid] = mr
		}
	}
	return out, m.call()
}

var bloopKey = store.ProjectKey{Namespace: "agents", Name: "bloop"}

func bloopTarget() BackfillTarget {
	return BackfillTarget{Namespace: "agents", Name: "bloop", GitLab: "group/app", BotID: 999,
		Selected: func(m gitlab.Milestone) bool { return !strings.HasPrefix(m.Title, "Manual") }}
}

// oldGitLab holds closed milestones 1 to n (each with two issues, 1 with a
// tracking issue and a retrospective), an active one and a closed one the
// operator does not plan.
func oldGitLab(n int64) *gitlabfake.Fake {
	f := gitlabfake.New()
	updated := time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)
	for iid := n; iid >= 1; iid-- {
		f.Milestones = append(f.Milestones, gitlab.Milestone{ID: 500 + iid, IID: iid, Title: "M" + string(rune('0'+iid)),
			State: "closed", WebURL: "https://gitlab.example/group/app/-/milestones/" + string(rune('0'+iid)), UpdatedAt: &updated})
	}
	f.Milestones = append(f.Milestones,
		gitlab.Milestone{ID: 600, IID: 60, Title: "Open", State: "active"},
		gitlab.Milestone{ID: 601, IID: 61, Title: "Manual cleanup", State: "closed"})
	for _, m := range f.Milestones {
		mc := m
		base := m.IID * 10
		f.Issues[base+1] = &gitlab.Issue{IID: base + 1, Title: "First of " + m.Title, State: "closed", Milestone: &mc}
		f.Issues[base+2] = &gitlab.Issue{IID: base + 2, Title: "Second of " + m.Title, State: "opened", Milestone: &mc}
	}
	m1 := f.Milestones[n-1]
	created := time.Date(2025, 4, 1, 9, 0, 0, 0, time.UTC)
	f.Issues[19] = &gitlab.Issue{IID: 19, Title: "Autopilot: M1", State: "closed", Milestone: &m1, Author: f.Bot,
		Description: "Tracking\n\n" + marker.TrackingIssue, CreatedAt: &created}
	posted := time.Date(2025, 4, 30, 9, 0, 0, 0, time.UTC)
	f.IssueNotes[19] = []gitlab.Note{
		{ID: 70, Author: f.Bot, Body: "🧭 **Retrospective**\n\nAn old one.\n\n" + marker.Retrospective, CreatedAt: &posted},
		{ID: 71, Author: f.Bot, Body: "🧭 **Retrospective**\n\nMerged #11 <img src=x onerror=alert(1)>.\n\n" + marker.Retrospective, CreatedAt: &posted},
		{ID: 72, Author: gitlab.User{ID: 5, Username: "mallory"}, Body: "Fake " + marker.Retrospective, CreatedAt: &posted},
		{ID: 73, Author: f.Bot, Body: "Autopilot is done.", CreatedAt: &posted},
	}
	return f
}

type backfillRig struct {
	b   *Backfill
	st  *memBackfill
	gl  *countingGitLab
	clk *clocktesting.FakeClock
}

func newBackfillRig(t *testing.T, f gitlab.API) *backfillRig {
	st := newMemBackfill()
	clk := clocktesting.NewFakeClock(now)
	return &backfillRig{b: newBackfill(func() BackfillStore { return st }, time.Hour, clk, testLogger(t)), st: st,
		gl: &countingGitLab{API: f}, clk: clk}
}

// step runs a step and returns its requests, failing past four.
func (r *backfillRig) step(t *testing.T) int {
	t.Helper()
	r.b.Step(t.Context(), r.gl, bloopTarget())
	n := r.gl.take()
	if n > 4 {
		t.Fatalf("a step made %d requests, want at most 4", n)
	}
	r.clk.Step(backfillSpacing)
	return n
}

func (r *backfillRig) saved() []int64 {
	var iids []int64
	for iid := range r.st.saved[bloopKey] {
		iids = append(iids, iid)
	}
	slices.Sort(iids)
	return iids
}

// The first step lists the closed milestones the operator plans; each
// further step saves one, oldest first; once all are in, steps make no
// request until the next day's listing.
func TestBackfillListsOnceThenOneMilestonePerStep(t *testing.T) {
	r := newBackfillRig(t, oldGitLab(3))
	if n := r.step(t); n != 1 || len(r.saved()) != 0 {
		t.Fatalf("listing step: %d requests, saved %v; want 1, none", n, r.saved())
	}
	if !r.st.listedAt.Equal(now) || r.st.complete {
		t.Errorf("listing recorded at %v, complete %v; want %v, not complete", r.st.listedAt, r.st.complete, now)
	}
	for i, want := range [][]int64{{1}, {1, 2}, {1, 2, 3}} {
		r.step(t)
		if got := r.saved(); !slices.Equal(got, want) {
			t.Fatalf("after step %d saved %v, want %v", i+1, got, want)
		}
	}
	for i := range 3 {
		if n := r.step(t); n != 0 {
			t.Errorf("step %d with nothing pending made %d requests", i, n)
		}
	}
	if !r.st.complete {
		t.Error("the project is not recorded complete")
	}
	// A day later the listing runs again, then nothing is pending.
	r.clk.Step(backfillRelist)
	if n := r.step(t); n != 1 {
		t.Errorf("relisting made %d requests, want 1", n)
	}
	if n := r.step(t); n != 0 {
		t.Errorf("after the relisting a step made %d requests, want none", n)
	}
}

// A backfilled milestone has its GitLab title, issues and states, merges
// from the history, the bot's newest retrospective without HTML, no
// follow-ups and no spend.
func TestBackfillMilestoneContent(t *testing.T) {
	r := newBackfillRig(t, oldGitLab(1))
	r.st.merged[11] = store.MergedMR{IID: 50, URL: "https://gitlab.example/group/app/-/merge_requests/50"}
	r.step(t)
	if n := r.step(t); n != 2 {
		t.Errorf("milestone step made %d requests, want 2 (issues, notes)", n)
	}
	m, ok := r.st.saved[bloopKey][1]
	if !ok {
		t.Fatal("milestone 1 not saved")
	}
	updated := time.Date(2025, 5, 1, 9, 0, 0, 0, time.UTC)
	if m.Title != "M1" || m.State != "closed" || m.MilestoneID != 501 || !m.DoneAt.Equal(updated) || m.TrackingIID != 19 ||
		!m.StartedAt.Equal(time.Date(2025, 4, 1, 9, 0, 0, 0, time.UTC)) || m.Phase != "" {
		t.Errorf("milestone = %+v", m)
	}
	want := []store.IssueSnapshot{
		{IID: 11, Title: "First of M1", State: "merged", MergeRequestIID: 50, MergeRequestURL: "https://gitlab.example/group/app/-/merge_requests/50"},
		{IID: 12, Title: "Second of M1", State: "pending"},
	}
	if !slices.Equal(m.Issues, want) {
		t.Errorf("issues = %+v, want %+v", m.Issues, want)
	}
	if m.RetroNoteID != 71 || m.RetroText != "🧭 **Retrospective**\n\nMerged #11 ." || m.RetroPostedAt.IsZero() {
		t.Errorf("retrospective = %d %q %v", m.RetroNoteID, m.RetroText, m.RetroPostedAt)
	}
	if len(m.FollowUps) != 0 || m.Owners != nil {
		t.Errorf("follow-ups %v, owners %v; want none", m.FollowUps, m.Owners)
	}
}

// Without a tracking issue of the bot there is no retrospective and no
// notes request; with one but without its retrospective the text is empty.
func TestBackfillRetrospectiveAbsent(t *testing.T) {
	f := oldGitLab(2)
	f.Issues[19].Author = gitlab.User{ID: 5, Username: "mallory"} // not the bot's: no tracking issue
	r := newBackfillRig(t, f)
	r.step(t)
	if n := r.step(t); n != 1 {
		t.Errorf("milestone without a tracking issue made %d requests, want 1", n)
	}
	m := r.st.saved[bloopKey][1]
	if m.TrackingIID != 0 || m.RetroText != "" || len(m.Issues) != 3 {
		t.Errorf("milestone 1 = tracking %d, retro %q, %d issues; want none, none, 3", m.TrackingIID, m.RetroText, len(m.Issues))
	}

	f = oldGitLab(1)
	f.IssueNotes[19] = f.IssueNotes[19][2:]
	r = newBackfillRig(t, f)
	r.step(t)
	r.step(t)
	if m := r.st.saved[bloopKey][1]; m.TrackingIID != 19 || m.RetroNoteID != 0 || m.RetroText != "" {
		t.Errorf("milestone 1 = tracking %d, retro %d %q; want 19 without a retrospective", m.TrackingIID, m.RetroNoteID, m.RetroText)
	}
}

// A restarted operator lists once more and goes on with what is pending;
// a project found complete needs no request.
func TestBackfillRestartResumes(t *testing.T) {
	f := oldGitLab(3)
	r := newBackfillRig(t, f)
	r.step(t)
	r.step(t)
	// Restart: same store, new backfill.
	b := newBackfill(func() BackfillStore { return r.st }, time.Hour, r.clk, testLogger(t))
	r.b = b
	if n := r.step(t); n != 1 {
		t.Errorf("first step after the restart made %d requests, want the listing", n)
	}
	r.step(t)
	r.step(t)
	if got := r.saved(); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Errorf("saved %v after the restart, want 1 2 3", got)
	}
	r.step(t) // complete
	r.b = newBackfill(func() BackfillStore { return r.st }, time.Hour, r.clk, testLogger(t))
	for range 2 {
		if n := r.step(t); n != 0 {
			t.Errorf("a step of a complete project after a restart made %d requests", n)
		}
	}
}

// A failing milestone counts an attempt per step and is skipped after
// three; GitLab giving out (a 5xx) counts none.
func TestBackfillFailures(t *testing.T) {
	f := oldGitLab(2)
	r := newBackfillRig(t, f)
	r.step(t)
	f.ListNoteErrors = map[int64]error{19: &gitlab.Error{StatusCode: 502, Method: "GET", Path: "notes"}}
	r.step(t)
	if r.st.attempts[1] != 0 || len(r.saved()) != 0 {
		t.Fatalf("a transient failure counted %d attempts, saved %v", r.st.attempts[1], r.saved())
	}
	f.ListNoteErrors[19] = &gitlab.Error{StatusCode: 403, Method: "GET", Path: "notes"}
	for want := 1; want <= 3; want++ {
		r.step(t)
		if r.st.attempts[1] != want {
			t.Fatalf("attempts = %d, want %d", r.st.attempts[1], want)
		}
	}
	// Skipped: milestone 2 goes on, then nothing is pending.
	r.step(t)
	if got := r.saved(); !slices.Equal(got, []int64{2}) {
		t.Errorf("saved %v, want 2", got)
	}
	if n := r.step(t); n != 0 || !r.st.complete {
		t.Errorf("after the skip: %d requests, complete %v; want none, complete", n, r.st.complete)
	}
}

// A milestone step stays at four requests with hundreds of issues, a
// failed listing is retried an hour later, and steps of any project are
// backfillSpacing apart.
func TestBackfillRequestLimits(t *testing.T) {
	f := oldGitLab(1)
	m1 := f.Milestones[0]
	for iid := int64(1000); iid < 1350; iid++ {
		f.Issues[iid] = &gitlab.Issue{IID: iid, Title: "Many", State: "closed", Milestone: &m1}
	}
	r := newBackfillRig(t, f)
	r.step(t)
	if n := r.step(t); n != 4 {
		t.Errorf("a milestone of 353 issues took %d requests, want 4 (three pages, the notes)", n)
	}

	// Two projects within backfillSpacing: the second waits.
	f = oldGitLab(1)
	r = newBackfillRig(t, f)
	r.b.Step(t.Context(), r.gl, bloopTarget())
	other := bloopTarget()
	other.Name = "other"
	r.b.Step(t.Context(), r.gl, other)
	if n := r.gl.take(); n != 1 {
		t.Errorf("two projects' steps at once made %d requests, want 1", n)
	}
	r.clk.Step(backfillSpacing - time.Second)
	r.b.Step(t.Context(), r.gl, other)
	if n := r.gl.take(); n != 0 {
		t.Errorf("a step %v after the last made %d requests, want none", backfillSpacing-time.Second, n)
	}
	r.clk.Step(time.Second)
	r.b.Step(t.Context(), r.gl, other)
	if n := r.gl.take(); n != 1 {
		t.Errorf("a step %v after the last made %d requests, want the listing", backfillSpacing, n)
	}

	// A failed listing waits an hour.
	f = oldGitLab(1)
	f.ListMilestonesError = errors.New("forbidden")
	r = newBackfillRig(t, f)
	r.step(t)
	f.ListMilestonesError = nil
	for range 3 {
		if n := r.step(t); n != 0 {
			t.Errorf("a step after a failed listing made %d requests, want none", n)
		}
	}
	r.clk.Step(backfillListRetry)
	if n := r.step(t); n != 1 {
		t.Errorf("an hour after the failed listing: %d requests, want 1", n)
	}
}

// Without a backfill, or before the database is ready, a step does
// nothing; a store that fails makes no GitLab request.
func TestBackfillWithoutStore(t *testing.T) {
	gl := &countingGitLab{API: oldGitLab(1)}
	(*Backfill)(nil).Step(t.Context(), gl, bloopTarget())
	newBackfill(func() BackfillStore { return nil }, time.Hour, clocktesting.NewFakeClock(now), testLogger(t)).Step(t.Context(), gl, bloopTarget())
	r := newBackfillRig(t, oldGitLab(1))
	r.st.failWith = errors.New("database down")
	r.step(t)
	if n := gl.take() + r.gl.take(); n != 0 {
		t.Errorf("%d requests without a store", n)
	}
}

// With the research store: the backfill writes the snapshot the Desk
// reads, without spend, and a restarted operator goes on where it stopped.
func TestBackfillWithStore(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	clk := clocktesting.NewFakeClock(time.Now())
	gl := &countingGitLab{API: oldGitLab(2)}
	b := newBackfill(func() BackfillStore { return s }, time.Hour, clk, testLogger(t))
	step := func() {
		b.Step(ctx, gl, bloopTarget())
		clk.Step(backfillSpacing)
	}
	step() // listing
	step() // milestone 1
	m, err := s.Milestone(ctx, bloopKey, 1, clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "M1" || m.State != "closed" || len(m.Issues) != 2 || m.TrackingIID != 19 ||
		!strings.Contains(m.RetroText, "Merged #11") || strings.Contains(m.RetroText, "<img") {
		t.Errorf("stored milestone = %+v", m)
	}
	if m.Spend.CostUSD.Known || m.Spend.Calls != 0 {
		t.Errorf("backfilled milestone has spend %+v, want none", m.Spend)
	}
	// Restart.
	b = newBackfill(func() BackfillStore { return s }, time.Hour, clk, testLogger(t))
	step() // listing again
	step() // milestone 2
	if _, err := s.Milestone(ctx, bloopKey, 2, clk.Now()); err != nil {
		t.Fatalf("milestone 2 after the restart: %v", err)
	}
	step() // nothing pending: complete
	if at, complete, err := s.ProjectListed(ctx, bloopKey); err != nil || !complete || at.IsZero() {
		t.Errorf("ProjectListed = %v, %v, %v; want complete", at, complete, err)
	}
	gl.take()
	b = newBackfill(func() BackfillStore { return s }, time.Hour, clk, testLogger(t))
	step()
	if n := gl.take(); n != 0 {
		t.Errorf("a complete project made %d requests after a restart", n)
	}
}

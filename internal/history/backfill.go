package history

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/utils/clock"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// The backfill fills the Desk's snapshot once from GitLab for the closed
// milestones the recorder never saw: one step per project poll, either the
// listing of the project's closed milestones or one milestone. A step makes
// at most backfillRequests GitLab requests, and steps are backfillSpacing
// apart across all projects, so the backfill stays far below GitLab's 100
// requests a minute per user however often webhooks wake the polls.
const (
	backfillSpacing = 15 * time.Second
	// backfillRelist is how often the closed milestones are listed again,
	// for those closed while the operator did not run: the recorder covers
	// the others.
	backfillRelist = 24 * time.Hour
	// backfillListRetry is when a failed listing is tried again.
	backfillListRetry = time.Hour
	// A listing step reads up to backfillListPages pages of 100 closed
	// milestones; a milestone step up to backfillIssuePages pages of 100
	// issues and one page of the tracking issue's newest notes.
	backfillListPages  = 4
	backfillIssuePages = 3
	backfillNotes      = 100
)

// BackfillStore is what the backfill needs of the research store.
type BackfillStore interface {
	BackfillPending(ctx context.Context, p store.ProjectKey, candidates []int64) ([]int64, error)
	SaveBackfilled(ctx context.Context, m store.MilestoneSnapshot, expiresAt time.Time) (bool, error)
	BackfillFailed(ctx context.Context, p store.ProjectKey, iid int64, cause error) (int, error)
	ProjectListed(ctx context.Context, p store.ProjectKey) (time.Time, bool, error)
	SetProjectListed(ctx context.Context, p store.ProjectKey, at time.Time, complete bool) error
	MergedIssues(ctx context.Context, p store.ProjectKey, issues []int64, now time.Time) (map[int64]store.MergedMR, error)
}

// BackfillTarget is the project a step works for.
type BackfillTarget struct {
	Namespace, Name string
	// GitLab is the GitLab project's path.
	GitLab string
	// BotID is the bot's user ID: only its tracking issue and
	// retrospective count. 0 trusts none.
	BotID int64
	// Selected reports whether the operator would plan the milestone,
	// ignoring its state; nil selects every closed milestone.
	Selected func(gitlab.Milestone) bool
}

// Backfill runs the backfill's steps. It keeps the listed milestones in
// memory; the store keeps what is done, so a restarted operator lists once
// more and goes on where it stopped.
type Backfill struct {
	store     func() BackfillStore
	retention time.Duration
	clock     clock.Clock
	log       logr.Logger

	// mu is held for a whole step: a poll of another project skips
	// meanwhile.
	mu sync.Mutex
	// last is when the last step with GitLab requests started.
	last     time.Time
	projects map[store.ProjectKey]*backfillProject
}

// backfillProject is a project's listing: the selected closed milestones,
// oldest (lowest IID) first.
type backfillProject struct {
	listedAt   time.Time
	next       time.Time
	complete   bool
	iids       []int64
	milestones map[int64]gitlab.Milestone
}

// NewBackfill returns the backfill writing to db's store, with rows that
// expire after retention. Before db is ready, steps do nothing.
func NewBackfill(db Database, retention time.Duration, clk clock.Clock, log logr.Logger) *Backfill {
	return newBackfill(func() BackfillStore {
		if s := db.Store(); s != nil {
			return s
		}
		return nil
	}, retention, clk, log)
}

func newBackfill(st func() BackfillStore, retention time.Duration, clk clock.Clock, log logr.Logger) *Backfill {
	return &Backfill{store: st, retention: retention, clock: clk, log: log, projects: map[store.ProjectKey]*backfillProject{}}
}

// Step runs one step of t's backfill with t's GitLab client gl: the listing
// when due, else the oldest pending milestone. It makes no request when the
// last step was less than backfillSpacing ago, another project's step runs,
// or nothing is pending. It never fails: errors are logged, and a failed
// milestone is recorded, so it is skipped after store.BackfillAttempts.
func (b *Backfill) Step(ctx context.Context, gl gitlab.API, t BackfillTarget) {
	if b == nil || !b.mu.TryLock() {
		return
	}
	defer b.mu.Unlock()
	st := b.store()
	if st == nil {
		return
	}
	key := store.ProjectKey{Namespace: t.Namespace, Name: t.Name}
	log := b.log.WithValues("namespace", t.Namespace, "project", t.Name)
	now := b.clock.Now()
	p := b.projects[key]
	if p == nil {
		// After a restart: a project found complete a short while ago
		// needs no listing yet.
		at, complete, err := st.ProjectListed(ctx, key)
		if err != nil {
			log.Error(err, "backfill: reading the project's listing")
			return
		}
		p = &backfillProject{}
		if complete {
			p.listedAt, p.next, p.complete = at, at.Add(backfillRelist), true
		}
		b.projects[key] = p
	}
	if !now.Before(p.next) {
		if b.spaced(now) {
			b.list(ctx, gl, st, t, key, p, now, log)
		}
		return
	}
	if p.complete || p.listedAt.IsZero() {
		return
	}
	pending, err := st.BackfillPending(ctx, key, p.iids)
	if err != nil {
		log.Error(err, "backfill: reading the pending milestones")
		return
	}
	if len(pending) == 0 {
		p.complete = true
		if err := st.SetProjectListed(ctx, key, p.listedAt, true); err != nil {
			log.Error(err, "backfill: recording the project complete")
		}
		log.Info("backfill: every closed milestone is in the snapshot", "milestones", len(p.iids))
		return
	}
	if !b.spaced(now) {
		return
	}
	m := p.milestones[pending[0]]
	err = b.fill(ctx, gl, st, t, key, m, now)
	switch {
	case err == nil:
		log.Info("backfill: milestone saved", "milestone", m.IID, "pending", len(pending)-1)
	case ctx.Err() != nil || gitlab.IsTransient(err):
		// GitLab or the poll gave out: no attempt of the milestone's.
		log.Info("backfill: milestone failed, trying again", "milestone", m.IID, "error", err.Error())
	default:
		n, ferr := st.BackfillFailed(ctx, key, m.IID, err)
		log.Error(errors.Join(err, ferr), "backfill: milestone failed", "milestone", m.IID, "attempts", n)
	}
}

// spaced reports whether a step with requests may run at now, and if so
// notes it.
func (b *Backfill) spaced(now time.Time) bool {
	if !b.last.IsZero() && now.Sub(b.last) < backfillSpacing {
		return false
	}
	b.last = now
	return true
}

// list lists t's closed milestones into p: those Selected, without their
// descriptions.
func (b *Backfill) list(ctx context.Context, gl gitlab.API, st BackfillStore, t BackfillTarget, key store.ProjectKey,
	p *backfillProject, now time.Time, log logr.Logger) {
	all, err := gl.ListClosedMilestones(ctx, t.GitLab, backfillListPages)
	if err != nil {
		p.next = now.Add(backfillListRetry)
		log.Info("backfill: listing the closed milestones failed, trying again later", "in", backfillListRetry, "error", err.Error())
		return
	}
	p.listedAt, p.next, p.iids, p.milestones = now, now.Add(backfillRelist), nil, map[int64]gitlab.Milestone{}
	for _, m := range all {
		if m.IID == 0 || m.State != "closed" || (t.Selected != nil && !t.Selected(m)) {
			continue
		}
		if _, dup := p.milestones[m.IID]; !dup {
			m.Description = ""
			p.milestones[m.IID] = m
			p.iids = append(p.iids, m.IID)
		}
	}
	slices.Sort(p.iids)
	pending, err := st.BackfillPending(ctx, key, p.iids)
	if err != nil {
		log.Error(err, "backfill: reading the pending milestones")
		return
	}
	p.complete = len(pending) == 0
	if err := st.SetProjectListed(ctx, key, now, p.complete); err != nil {
		log.Error(err, "backfill: recording the listing")
	}
	log.Info("backfill: closed milestones listed", "milestones", len(p.iids), "pending", len(pending))
}

// fill reads milestone m from GitLab and saves it: its issues, their
// merges from the history, and the bot's retrospective on its tracking
// issue.
func (b *Backfill) fill(ctx context.Context, gl gitlab.API, st BackfillStore, t BackfillTarget, key store.ProjectKey,
	m gitlab.Milestone, now time.Time) error {
	issues, err := gl.ListIssues(ctx, t.GitLab, gitlab.ListIssuesOptions{State: "all", Milestone: m.Title, MaxPages: backfillIssuePages})
	if err != nil {
		return fmt.Errorf("listing the issues of milestone %d: %w", m.IID, err)
	}
	snap := store.MilestoneSnapshot{Project: key, IID: m.IID, MilestoneID: m.ID, Title: m.Title, WebURL: m.WebURL,
		State: "closed", DoneAt: milestoneDone(m), Issues: []store.IssueSnapshot{}}
	slices.SortFunc(issues, func(a, b gitlab.Issue) int { return cmp.Compare(a.IID, b.IID) })
	var iids []int64
	for _, is := range issues {
		// The filter is by title: a milestone of the same title elsewhere
		// (a group's) is not this one.
		if is.Milestone != nil && is.Milestone.ID != 0 && m.ID != 0 && is.Milestone.ID != m.ID {
			continue
		}
		if t.BotID != 0 && is.Author.ID == t.BotID && marker.IsTrackingIssue(is.Description) {
			if snap.TrackingIID == 0 {
				snap.TrackingIID = is.IID
				if is.CreatedAt != nil {
					snap.StartedAt = *is.CreatedAt
				}
			}
			continue
		}
		state := "pending"
		if is.State == "closed" {
			state = "closed"
		}
		snap.Issues = append(snap.Issues, store.IssueSnapshot{IID: is.IID, Title: is.Title, State: state})
		iids = append(iids, is.IID)
	}
	if snap.TrackingIID != 0 {
		notes, err := gl.ListIssueNotesNewest(ctx, t.GitLab, snap.TrackingIID, backfillNotes)
		if err != nil {
			return fmt.Errorf("listing the notes of tracking issue #%d: %w", snap.TrackingIID, err)
		}
		// Newest first: the newest retrospective.
		for _, n := range notes {
			if !n.System && n.Author.ID == t.BotID && strings.Contains(n.Body, marker.Retrospective) {
				snap.RetroNoteID, snap.RetroText = n.ID, textutil.UntrustedMarkdown(n.Body, aiv1.MaxRetroText)
				if n.CreatedAt != nil {
					snap.RetroPostedAt = *n.CreatedAt
				}
				break
			}
		}
	}
	merged, err := st.MergedIssues(ctx, key, iids, now)
	if err != nil {
		return fmt.Errorf("reading the merges of milestone %d: %w", m.IID, err)
	}
	for i, is := range snap.Issues {
		if mr, ok := merged[is.IID]; ok {
			snap.Issues[i].State, snap.Issues[i].MergeRequestIID, snap.Issues[i].MergeRequestURL = "merged", mr.IID, mr.URL
		}
	}
	_, err = st.SaveBackfilled(ctx, snap, now.Add(b.retention))
	return err
}

// milestoneDone is when a closed milestone was done, as far as GitLab
// tells: its last update (the close, mostly), else its due date, else
// unknown (zero).
func milestoneDone(m gitlab.Milestone) time.Time {
	if m.UpdatedAt != nil {
		return *m.UpdatedAt
	}
	if d, err := time.Parse(time.DateOnly, m.DueDate); err == nil {
		return d
	}
	return time.Time{}
}

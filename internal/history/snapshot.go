package history

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// IssuePlanned is the state of a planned issue autopilot has not queued.
const IssuePlanned = "planned"

// Snapshot is the Desk's snapshot of the milestone of the spec task t at
// now; false for other tasks. web is the GitLab project's web URL, empty
// when unknown (the UI builds the URL then). Issues are the plan's, in plan
// order, then the autopilot's items the plan lacks; states are the items'
// in lower case, else planned. StartedAt is now (the store keeps the first),
// DoneAt now once autopilot is done.
func Snapshot(t *aiv1.AgentTask, web string, now time.Time) (store.MilestoneSnapshot, bool) {
	ms := t.Spec.Milestone
	if t.Spec.Type != aiv1.TaskTypeSpec || ms == nil || ms.IID == 0 {
		return store.MilestoneSnapshot{}, false
	}
	m := store.MilestoneSnapshot{Project: store.ProjectKey{Namespace: t.Namespace, Name: t.Spec.ProjectRef},
		IID: ms.IID, MilestoneID: ms.ID, Title: ms.Title, State: "open", StartedAt: now,
		Owners: t.Spec.Owners, OwnerSubjects: t.Spec.OwnerSubjects, Issues: []store.IssueSnapshot{}}
	if web != "" {
		m.WebURL = fmt.Sprintf("%s/-/milestones/%d", strings.TrimRight(web, "/"), ms.IID)
	}
	items := map[int64]aiv1.AutopilotItem{}
	if ap := t.Status.Autopilot; ap != nil {
		m.Phase = strings.ToLower(string(ap.Phase))
		// trackingIssueIID of internal/ui without a Coordinate task.
		m.TrackingIID = ap.TrackingIssueIID
		if ap.Phase == aiv1.AutopilotDone {
			m.DoneAt = now
		}
		for _, it := range ap.Items {
			items[it.IID] = it
		}
	}
	seen := map[int64]bool{}
	add := func(iid int64, title string) {
		if iid == 0 || seen[iid] {
			return
		}
		seen[iid] = true
		is := store.IssueSnapshot{IID: iid, Title: title, State: IssuePlanned}
		if it, ok := items[iid]; ok {
			is.State = strings.ToLower(string(it.State))
			is.MergeRequestIID, is.MergeRequestURL = it.MergeRequestIID, it.MergeRequestURL
		}
		m.Issues = append(m.Issues, is)
	}
	for _, pi := range t.Status.Plan {
		add(pi.IID, pi.Title)
	}
	if ap := t.Status.Autopilot; ap != nil {
		for _, it := range ap.Items {
			add(it.IID, "")
		}
	}
	if ps := t.Status.Pilot; ps != nil && ps.Retrospective != nil {
		r := ps.Retrospective
		m.RetroNoteID, m.RetroText = r.NoteID, r.Text
		if r.PostedAt != nil {
			m.RetroPostedAt = r.PostedAt.Time
		}
		for i, f := range r.FollowUps {
			n := i + 1
			fu := store.FollowUpSnapshot{Number: n, Title: f.Title, Body: f.Body, State: store.FollowUpOpen}
			if iid := r.Created[strconv.Itoa(n)]; iid != 0 {
				fu.State, fu.TargetIID = store.FollowUpIssue, iid
			} else if iid := r.Taken[strconv.Itoa(n)]; iid != 0 {
				fu.State, fu.TargetIID = store.FollowUpMilestone, iid
			}
			m.FollowUps = append(m.FollowUps, fu)
		}
	}
	if f := t.Status.FollowUpOf; f != nil && len(f.Numbers) > 0 {
		m.TakesFrom, m.Takes = f.MilestoneIID, f.Numbers
	}
	return m, true
}

// snapKey is a milestone in the snapshot queue.
type snapKey struct {
	p   store.ProjectKey
	iid int64
}

// snapOp is what the queue writes for a milestone: its newest snapshot,
// then, when closed is set, the close.
type snapOp struct {
	save   *store.MilestoneSnapshot
	closed time.Time
}

// RecordSnapshot queues m, replacing a queued snapshot of the milestone. It
// never blocks: with the queue full of other milestones it is dropped.
func (r *Recorder) RecordSnapshot(m store.MilestoneSnapshot) {
	if r == nil || !r.started.Load() {
		return
	}
	r.queueSnap(snapKey{m.Project, m.IID}, func(op *snapOp) { op.save, op.closed = &m, time.Time{} })
}

// RecordClosed queues the close of the milestone at at (now when zero),
// after its queued snapshot if any. It never blocks, like RecordSnapshot.
func (r *Recorder) RecordClosed(p store.ProjectKey, iid int64, at time.Time) {
	if r == nil || !r.started.Load() {
		return
	}
	if at.IsZero() {
		at = r.clock.Now()
	}
	r.queueSnap(snapKey{p, iid}, func(op *snapOp) { op.closed = at })
}

func (r *Recorder) queueSnap(k snapKey, set func(*snapOp)) {
	r.snapMu.Lock()
	defer r.snapMu.Unlock()
	op, ok := r.snaps[k]
	if !ok && len(r.snaps) >= queueSize {
		r.droppedSnaps.Add(1)
		return
	}
	set(&op)
	r.snaps[k] = op
}

// writeSnapshots writes the queued snapshots and closes and empties the
// queue.
func (r *Recorder) writeSnapshots(ctx context.Context) {
	r.snapMu.Lock()
	ops := r.snaps
	if len(ops) > 0 {
		r.snaps = map[snapKey]snapOp{}
	}
	r.snapMu.Unlock()
	expires := r.clock.Now().Add(r.retention)
	var lost int
	var last error
	for k, op := range ops {
		var err error
		if op.save != nil {
			_, err = r.db.Store().SaveMilestone(ctx, *op.save, expires)
		}
		if s := op.save; err == nil && s != nil && len(s.Takes) > 0 {
			_, err = r.db.Store().TakeFollowUps(ctx, k.p, s.TakesFrom, s.Takes, k.iid, expires)
		}
		if err == nil && !op.closed.IsZero() {
			_, err = r.db.Store().CloseMilestone(ctx, k.p, k.iid, op.closed, expires)
		}
		if err != nil {
			lost, last = lost+1, err
		}
	}
	if lost > 0 && ctx.Err() == nil {
		r.log.Error(last, "milestone snapshots lost", "milestones", lost)
	}
	if n := r.droppedSnaps.Swap(0); n > 0 {
		r.log.Info("snapshot queue full, milestone snapshots dropped", "snapshots", n)
	}
}

// queueExisting queues the snapshots of the spec tasks existingSnaps
// returns: the informer's adds came before the recorder started.
func (r *Recorder) queueExisting(ctx context.Context) {
	if r.existingSnaps == nil {
		return
	}
	snaps, err := r.existingSnaps(ctx)
	if err != nil {
		r.log.Error(err, "listing the milestones of existing spec tasks")
		return
	}
	for _, m := range snaps {
		r.RecordSnapshot(m)
	}
}

// snapshotTask queues the snapshot of t at now if it is a spec task. The
// web URL is left to the UI: the recorder knows no projects.
func (r *Recorder) snapshotTask(t *aiv1.AgentTask, now time.Time) {
	if m, ok := Snapshot(t, "", now); ok {
		r.RecordSnapshot(m)
	}
}

// closeTask queues the close of the milestone of t at now if it is a spec
// task: its rows stay.
func (r *Recorder) closeTask(t *aiv1.AgentTask, now time.Time) {
	if m, ok := Snapshot(t, "", now); ok {
		r.RecordClosed(m.Project, m.IID, now)
	}
}

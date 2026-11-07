package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// FollowUpSource reads the Desk's snapshot of a milestone's follow-ups
// (desk_follow_ups), ErrNotFound for a milestone it has none of.
type FollowUpSource interface {
	FollowUpsOf(ctx context.Context, p store.ProjectKey, iid int64) (store.MilestoneFollowUps, error)
}

// followUpKey is follow-up number of the milestone iid.
type followUpKey struct {
	iid    int64
	number int
}

// syncFollowUpOf settles the follow-ups that the milestones' lines
// "/agent followup-of <iid> <numbers>" take over, lowest milestone first,
// so of two that name one in the same poll the lower takes it. Once taken,
// a follow-up stays with its milestone. A malformed line (descriptions are
// the milestones' by ID) is noticed once.
func (r *AgentProjectReconciler) syncFollowUpOf(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	specTasks []*aiv1.AgentTask, pilots map[int64]*aiv1.AgentTask, descriptions map[int64]string) error {
	open := map[int64]*aiv1.AgentTask{}
	// What the milestones took so far, of open and closed milestones alike:
	// the snapshot of a closed one may not have it yet.
	claims := map[followUpKey]int64{}
	var takers []*aiv1.AgentTask
	for _, st := range specTasks {
		iid := st.Spec.Milestone.IID
		open[iid] = st
		if f := st.Status.FollowUpOf; f != nil {
			for _, n := range f.Numbers {
				k := followUpKey{f.MilestoneIID, n}
				if c, ok := claims[k]; !ok || iid < c {
					claims[k] = iid
				}
			}
		}
		if st.Spec.FollowUpOf != nil {
			takers = append(takers, st)
		}
	}
	var errs []error
	for _, st := range specTasks {
		if st.Spec.FollowUpOf != nil {
			continue
		}
		if _, err := rules.FollowUpOf(descriptions[st.Spec.Milestone.ID], spec.Workflow.CommandPrefix); err != nil {
			if err := r.malformedFollowUpOf(ctx, gl, spec, st, pilots, err); err != nil {
				errs = append(errs, fmt.Errorf("followup-of line of milestone %q: %w", st.Spec.Milestone.Title, err))
			}
		}
	}
	slices.SortFunc(takers, func(a, b *aiv1.AgentTask) int { return cmp.Compare(a.Spec.Milestone.IID, b.Spec.Milestone.IID) })
	for _, st := range takers {
		if err := r.takeFollowUps(ctx, gl, project, spec, st, open, pilots, claims); err != nil {
			errs = append(errs, fmt.Errorf("followup-of line of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
	}
	return errors.Join(errs...)
}

// takeFollowUps checks the followup-of line of the spec task st against its
// source milestone: the spec task of an open one, the snapshot of another.
// A follow-up is taken when the retrospective has it and it was neither
// opened, dismissed nor taken by another milestone; an open source records
// it in its Taken, the recorder a closed one's. What the line names
// otherwise is st's problem, posted once on its tracking issue (else the
// source's).
func (r *AgentProjectReconciler) takeFollowUps(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st *aiv1.AgentTask, open, pilots map[int64]*aiv1.AgentTask, claims map[followUpKey]int64) error {
	line, self := st.Spec.FollowUpOf, st.Spec.Milestone.IID
	src := line.MilestoneIID
	source := open[src]
	// What the line took before stays taken, even once the source closed
	// and is unknown without the database.
	var accepted, pending []int
	for _, n := range line.Numbers {
		if prev := st.Status.FollowUpOf; prev != nil && prev.MilestoneIID == src && slices.Contains(prev.Numbers, n) {
			accepted = append(accepted, n)
			claims[followUpKey{src, n}] = self
		} else {
			pending = append(pending, n)
		}
	}
	// The source's follow-ups by number, nil while unknown.
	var rows map[int]store.FollowUpRow
	var srcTracking int64
	switch {
	case src == self:
	case source != nil:
		srcTracking = milestoneTracking(source, pilots[src])
		if ps := source.Status.Pilot; ps != nil && ps.Retrospective != nil {
			rows = liveFollowUps(ps.Retrospective)
		}
	case r.FollowUps != nil && len(pending) > 0:
		stored, err := r.FollowUps.FollowUpsOf(ctx, store.ProjectKey{Namespace: project.Namespace, Name: project.Name}, src)
		if errors.Is(err, store.ErrNotFound) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the follow-ups of %%%d: %w", src, err)
		}
		srcTracking, rows = stored.TrackingIID, map[int]store.FollowUpRow{}
		for _, f := range stored.FollowUps {
			rows[f.Number] = f
		}
	}

	var missing []int
	var problems []string
	switch {
	case src == self:
		problems = append(problems, "a milestone takes over no follow-ups of its own")
	case rows == nil && len(pending) > 0:
		problems = append(problems, fmt.Sprintf("%%%d has no retrospective the operator knows of", src))
	}
	for _, n := range pending {
		if rows == nil {
			break
		}
		f, ok := rows[n]
		c, claimed := claims[followUpKey{src, n}]
		switch {
		case !ok:
			missing = append(missing, n)
		case claimed && c != self:
			problems = append(problems, fmt.Sprintf("follow-up %d is taken by %%%d", n, c))
		case f.State == store.FollowUpMilestone && f.TargetIID != self:
			problems = append(problems, fmt.Sprintf("follow-up %d is taken by %%%d", n, f.TargetIID))
		case f.State == store.FollowUpIssue:
			problems = append(problems, fmt.Sprintf("follow-up %d was opened as #%d", n, f.TargetIID))
		case f.State == store.FollowUpDismissed:
			problems = append(problems, fmt.Sprintf("follow-up %d was dismissed", n))
		default:
			accepted = append(accepted, n)
			claims[followUpKey{src, n}] = self
		}
	}
	slices.Sort(accepted)
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("the retrospective of %%%d has no %s", src, followUpList(missing)))
	}

	if source != nil && len(accepted) > 0 {
		var take []string
		for _, n := range accepted {
			if key := strconv.Itoa(n); source.Status.Pilot.Retrospective.Taken[key] == 0 {
				take = append(take, key)
			}
		}
		if len(take) > 0 {
			if err := r.patchPilot(ctx, source, func(ps *aiv1.PilotStatus) {
				if ps.Retrospective.Taken == nil {
					ps.Retrospective.Taken = map[string]int64{}
				}
				for _, key := range take {
					ps.Retrospective.Taken[key] = self
				}
			}); err != nil {
				return err
			}
		}
	}

	next := &aiv1.FollowUpOfStatus{MilestoneIID: src, Numbers: accepted, Problem: strings.Join(problems, "; ")}
	what := "nothing"
	if len(accepted) > 0 {
		what = fmt.Sprintf("%s of %%%d", followUpList(accepted), src)
	}
	body := fmt.Sprintf("📌 The milestone %%%d took over %s with its line `%s`. Ignored: %s.",
		self, what, rules.FollowUpOfLine(spec.Workflow.CommandPrefix, src, line.Numbers), next.Problem)
	return r.recordFollowUpOf(ctx, gl, spec, st, next, cmp.Or(milestoneTracking(st, pilots[self]), srcTracking), body)
}

// malformedFollowUpOf records the parse error of st's malformed followup-of
// line as its problem; what an earlier line took stays taken.
func (r *AgentProjectReconciler) malformedFollowUpOf(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec,
	st *aiv1.AgentTask, pilots map[int64]*aiv1.AgentTask, parseErr error) error {
	next := &aiv1.FollowUpOfStatus{Problem: parseErr.Error()}
	if prev := st.Status.FollowUpOf; prev != nil {
		next.MilestoneIID, next.Numbers = prev.MilestoneIID, prev.Numbers
	}
	body := fmt.Sprintf("📌 The line `%s followup-of` of the milestone %%%d is ignored: %s.", spec.Workflow.CommandPrefix, st.Spec.Milestone.IID, next.Problem)
	return r.recordFollowUpOf(ctx, gl, spec, st, next, milestoneTracking(st, pilots[st.Spec.Milestone.IID]), body)
}

// recordFollowUpOf sets st's status.followUpOf to next, after posting body
// on the tracking issue when next has a problem not noticed before. Without
// a tracking issue the notice waits.
func (r *AgentProjectReconciler) recordFollowUpOf(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec,
	st *aiv1.AgentTask, next *aiv1.FollowUpOfStatus, tracking int64, body string) error {
	if prev := st.Status.FollowUpOf; prev != nil && prev.MilestoneIID == next.MilestoneIID && prev.Problem == next.Problem {
		next.Noticed = prev.Noticed
	}
	var noticeErr error
	if next.Problem != "" && !next.Noticed && tracking != 0 {
		_, noticeErr = gl.CreateIssueNote(ctx, spec.GitLab.Project, tracking, body)
		if noticeErr == nil || gitlab.IsNotFound(noticeErr) {
			next.Noticed, noticeErr = true, nil
		} else {
			noticeErr = fmt.Errorf("posting the notice on #%d: %w", tracking, noticeErr)
		}
	}
	if !equality.Semantic.DeepEqual(st.Status.FollowUpOf, next) {
		orig := st.DeepCopy()
		st.Status.FollowUpOf = next
		if err := r.writeFailed(ctx, st, "followUpOf", r.Status().Patch(ctx, st, client.MergeFrom(orig))); err != nil {
			return errors.Join(noticeErr, err)
		}
	}
	return noticeErr
}

// liveFollowUps are the follow-ups of an open milestone's retrospective by
// number, with their state.
func liveFollowUps(retro *aiv1.PilotRetrospective) map[int]store.FollowUpRow {
	out := map[int]store.FollowUpRow{}
	for i, f := range retro.FollowUps {
		n := i + 1
		row := store.FollowUpRow{Number: n, Title: f.Title, State: store.FollowUpOpen}
		if iid := retro.Created[strconv.Itoa(n)]; iid != 0 {
			row.State, row.TargetIID = store.FollowUpIssue, iid
		} else if iid := retro.Taken[strconv.Itoa(n)]; iid != 0 {
			row.State, row.TargetIID = store.FollowUpMilestone, iid
		} else if retro.DismissedAt != nil {
			row.State = store.FollowUpDismissed
		}
		out[n] = row
	}
	return out
}

// milestoneTracking is the milestone's tracking issue: the pilot's or the
// autopilot's, also once autopilot is done; 0 without one.
func milestoneTracking(st, coord *aiv1.AgentTask) int64 {
	if tracking := trackingIssue(st, coord); tracking != 0 {
		return tracking
	}
	if ap := st.Status.Autopilot; ap != nil {
		return ap.TrackingIssueIID
	}
	return 0
}

// followUpList is "follow-up 3" or "follow-ups 1, 3".
func followUpList(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	if len(ns) == 1 {
		return "follow-up " + parts[0]
	}
	return "follow-ups " + strings.Join(parts, ", ")
}

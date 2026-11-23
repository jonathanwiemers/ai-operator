package controller

import (
	"context"
	"fmt"
	"slices"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// blocker is an open issue a queued issue is linked as "blocked by" to.
type blocker struct {
	ref string
	// queued: pending or active in this queue, so autopilot gets there.
	queued bool
}

// blockedByLinks returns the open issues iid is linked as "blocked by" to.
// Such links only exist on GitLab Premium. The plan's own dependencies are
// linked too and met once their issue is merged and closed; links the
// operator made for dependencies the plan dropped since do not count.
func (a *autopilot) blockedByLinks(ctx context.Context, iid int64) ([]blocker, error) {
	links, err := a.gl.ListIssueLinks(ctx, a.spec.GitLab.Project, iid)
	if gitlab.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing the links of #%d: %w", iid, err)
	}
	proj, err := a.glProject(ctx)
	if err != nil {
		return nil, err
	}
	var planned aiv1.PlannedIssue
	for _, p := range a.st.Status.Plan {
		if p.IID == iid {
			planned = p
		}
	}
	var out []blocker
	for _, l := range links {
		if l.LinkType != gitlab.LinkIsBlockedBy || l.State != "opened" {
			continue
		}
		if l.ProjectID != proj.ID {
			ref := l.References.Full
			if ref == "" {
				ref = fmt.Sprintf("#%d of project %d", l.IID, l.ProjectID)
			}
			out = append(out, blocker{ref: ref})
			continue
		}
		if slices.Contains(planned.Linked, l.IID) && !slices.Contains(planned.DependsOn, l.IID) {
			continue // the plan dropped this dependency; its link is stale
		}
		dep := a.item(l.IID)
		queued := dep != nil && (dep.State == aiv1.AutopilotItemPending || dep.State == aiv1.AutopilotItemActive)
		out = append(out, blocker{ref: fmt.Sprintf("#%d", l.IID), queued: queued})
	}
	return out, nil
}

// doneByHand reports whether a closed issue was finished outside autopilot:
// its MR was merged, a merged MR of the project closed it, or it was closed as
// done. It returns the merged MR, if any. (GitLab's closed_by only answers for
// open issues, so the issue's state events are used.)
func (a *autopilot) doneByHand(ctx context.Context, iid int64) (*gitlab.MergeRequest, bool, error) {
	is, ok := a.issues[iid]
	if !ok || is.State == "opened" {
		return nil, false, nil
	}
	if it := a.item(iid); it != nil && it.MergeRequestIID != 0 {
		mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
		if err != nil && !gitlab.IsNotFound(err) {
			return nil, false, fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
		}
		if err == nil && mr.State == "merged" {
			return mr, true, nil
		}
	}
	// The issue's last state change says whether a merge request closed it;
	// merely mentioning the issue does not count.
	events, err := a.gl.ListIssueStateEvents(ctx, a.spec.GitLab.Project, iid)
	if err != nil {
		return nil, false, fmt.Errorf("listing the state events of #%d: %w", iid, err)
	}
	if n := len(events); n > 0 && events[n-1].State == "closed" && events[n-1].SourceMergeRequest != nil {
		src := events[n-1].SourceMergeRequest
		proj, err := a.gl.GetProject(ctx, a.spec.GitLab.Project)
		if err != nil {
			return nil, false, fmt.Errorf("getting project: %w", err)
		}
		if src.ProjectID == proj.ID {
			mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, src.IID)
			if err != nil && !gitlab.IsNotFound(err) {
				return nil, false, fmt.Errorf("getting merge request !%d: %w", src.IID, err)
			}
			if err == nil && mr.State == "merged" {
				return mr, true, nil
			}
		}
	}
	return nil, is.HasLabel(a.spec.Workflow.DoneLabel), nil
}

// With an empty queue it does nothing: a milestone whose plan is empty or
// was dropped by a re-plan merged nothing and is not done (a real milestone).
// It keeps running until a release contains the milestone's last merge
// (finalRelease), and after that while companions of the milestone have not
// landed; it says once per change which ones it waits for
// (CloseWaitsForCompanions).
func (a *autopilot) done(ctx context.Context) error {
	if len(a.ap.Items) == 0 {
		a.log.Info("autopilot is not done: its queue is empty")
		return nil
	}
	if ok, err := a.finalRelease(ctx); err != nil || !ok {
		return err
	}
	open, err := a.landingCompanions(ctx)
	if err != nil {
		return err
	}
	if len(open) > 0 {
		if err := a.reportCompanionWaits(ctx, open, "⏳ Every issue of the queue is through. Autopilot is done once the companions %s have landed (merged or closed)."); err != nil {
			return err
		}
		a.ap.Phase = aiv1.AutopilotRunning
		return nil
	}
	a.ap.CloseWaitsForCompanions = nil
	merged := 0
	for _, it := range a.ap.Items {
		if it.State == aiv1.AutopilotItemMerged {
			merged++
		}
	}
	a.log.Info("autopilot milestone done", "merged", merged)
	// Close first and comment after, so a failing close does not repeat
	// the comment.
	prev := a.ap.Phase
	a.ap.Phase = aiv1.AutopilotDone // for the description
	if err := a.closeTracking(ctx); err != nil {
		a.ap.Phase = prev // tried again on the next poll
		return err
	}
	return a.comment(ctx, fmt.Sprintf("✅ Autopilot is done: %d of %d issues merged. The decisions are listed in the description.", merged, len(a.ap.Items))+
		a.mention(aiv1.NotifyAutopilotDone))
}

// endMilestone ends the milestone at an owner's answer (by): the pending
// issues are skipped, so none is picked any more, and autopilot is done
// after the active issue. During a pause for a budget the active issue
// cannot go on without spend: it is skipped too, the pause ends and
// autopilot is done now. With an empty queue it only answers that there is
// nothing to end.
func (a *autopilot) endMilestone(ctx context.Context, by string) error {
	if len(a.ap.Items) == 0 {
		// Nothing to skip and nothing to be done with (done): the state stays.
		a.log.Info("autopilot milestone not ended: nothing is planned", "by", by)
		return a.comment(ctx, fmt.Sprintf("⏹️ Nothing to end (%s): nothing is planned in this milestone, so autopilot is not done. "+
			"To stop it, close the milestone in GitLab or remove `%s autopilot` from its description.", by, a.spec.Workflow.CommandPrefix))
	}
	budget := a.budgetPaused()
	var skipped []int64
	var active *aiv1.AutopilotItem
	for i := range a.ap.Items {
		switch it := &a.ap.Items[i]; {
		case it.State == aiv1.AutopilotItemPending, it.State == aiv1.AutopilotItemActive && budget:
			it.State, it.RebasedFrom, it.Hold = aiv1.AutopilotItemSkipped, "", nil
			skipped = append(skipped, it.IID)
		case it.State == aiv1.AutopilotItemActive:
			active = it
		}
	}
	a.log.Info("autopilot milestone ends here", "by", by, "skipped", skipped)
	what := "nothing is left to skip"
	if len(skipped) > 0 {
		what = issueList(skipped) + " skipped"
	}
	if active != nil {
		return a.comment(ctx, fmt.Sprintf("⏹️ The milestone ends here (%s): %s. Autopilot picks no further issue and is done after #%d.", by, what, active.IID))
	}
	if err := a.comment(ctx, fmt.Sprintf("⏹️ The milestone ends here (%s): %s.", by, what)); err != nil {
		return err
	}
	if a.ap.Phase == aiv1.AutopilotDone {
		return nil
	}
	prev := *a.ap
	a.ap.PausedReason, a.ap.PauseKind, a.ap.PausedFor, a.ap.ResumeWhenReady = "", "", 0, false
	if prev.Phase == aiv1.AutopilotPaused {
		a.ap.ResumedBy, a.ap.ResumedVia = resolverOf(by), aiv1.ResumedViaCommand // the pause ends with the milestone
	}
	releases := slices.Clone(a.ap.Releases)
	a.dropFinalRelease(by) // a failed final release: ends without one
	if err := a.done(ctx); err != nil {
		a.ap.PausedReason, a.ap.PauseKind, a.ap.PausedFor, a.ap.ResumeWhenReady = prev.PausedReason, prev.PauseKind, prev.PausedFor, prev.ResumeWhenReady
		a.ap.ResumedBy, a.ap.ResumedVia = prev.ResumedBy, prev.ResumedVia
		a.ap.Releases = releases
		return err
	}
	return nil
}

// closeMilestone closes the milestone of a done autopilot once the
// retrospective and its follow-ups are settled, no issue in it is open and
// all its companions have landed.
// A closed milestone is no longer planned: the next poll deletes its tasks,
// so the comment after the close is not repeated.
func (a *autopilot) closeMilestone(ctx context.Context) error {
	var retro *aiv1.PilotRetrospective
	if ps := a.st.Status.Pilot; ps != nil {
		retro = ps.Retrospective
	}
	switch {
	case a.st.RunPending():
		return nil // a re-plan may add issues and continue the queue
	case retro == nil && a.pilotTask != nil:
		return nil // the pilot is still writing it
	case retro != nil && !retro.FollowUpsSettled(a.r.now()):
		return nil
	}
	var open []int64
	for iid, is := range a.issues {
		if is.State == "opened" && iid != a.ap.TrackingIssueIID {
			open = append(open, iid)
		}
	}
	slices.Sort(open)
	if len(open) > 0 && !slices.Equal(open, a.ap.CloseWaitsFor) {
		verb := "are"
		if len(open) == 1 {
			verb = "is"
		}
		if err := a.comment(ctx, fmt.Sprintf("📌 The milestone stays open while %s %s open. Autopilot closes it once they are closed.",
			issueList(open), verb)); err != nil {
			return err
		}
		a.ap.CloseWaitsFor = open
	}
	// A companion recorded after done (its issue finished by hand): the
	// milestone waits for it to land too.
	companions, err := a.landingCompanions(ctx)
	if err != nil {
		return err
	}
	if len(companions) > 0 {
		if err := a.reportCompanionWaits(ctx, companions, "📌 The milestone stays open while the companions %s have not landed. Autopilot closes it once they are merged or closed."); err != nil {
			return err
		}
	}
	a.ap.CloseWaitsForCompanions = companions
	if len(open) > 0 || len(companions) > 0 {
		return nil
	}
	a.log.Info("closing the done autopilot milestone")
	if err := a.gl.CloseMilestone(ctx, a.spec.GitLab.Project, a.st.Spec.Milestone.ID); err != nil && !gitlab.IsNotFound(err) {
		return fmt.Errorf("closing the milestone: %w", err)
	}
	a.ap.CloseWaitsFor, a.ap.CloseWaitsForCompanions = nil, nil
	return a.comment(ctx, "🏁 The milestone is closed.")
}

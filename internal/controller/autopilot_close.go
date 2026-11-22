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

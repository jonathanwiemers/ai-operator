package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// validAction checks an action against the milestone (spec task st): a
// known tool, an issue of the milestone with a task, a kind of
// instruction, an issue title the pilot has not opened yet, an autopilot
// queue the command applies to.
func validAction(a aiv1.PilotAction, st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, opened []string) error {
	ap := st.Status.Autopilot
	running := queueRunning(st)
	switch {
	case !slices.Contains(pilotTools, a.Tool) && !slices.Contains(ownerTools, a.Tool):
		return fmt.Errorf("`%s` is not a tool of the pilot; merging, closing and pushing are not available", inertLine(a.Tool, 40))
	case a.Tool == "raiseBudget":
		return validRaise(a, st)
	case a.Tool == "setRule" || a.Tool == "forgetRule":
		return validRule(a)
	case a.Tool == "revisit" && marker.DecisionIssue(a.Decision) == 0:
		return fmt.Errorf("`%s` is not a decision", inertLine(a.Decision, 40))
	case a.Tool == "revisit":
		return nil
	case a.Tool == "editSpec" && strings.TrimSpace(a.Spec) == "":
		return errors.New("a spec edit needs the new text")
	case a.Tool == "editSpec" && len(a.Spec) > maxSpec:
		return fmt.Errorf("the new spec is over %d KiB", maxSpec>>10)
	case a.Tool == "editSpec":
		return nil // the milestone itself is checked in GitLab (specEdit)
	case a.Tool == "fixFlaky" && a.IssueIID <= 0:
		return errors.New("fixFlaky needs the flaky issue")
	case a.Tool == "fixFlaky":
		return nil // the issue itself is checked in GitLab (checkFlaky)
	case a.Tool == "retryPipeline" && a.PipelineID <= 0:
		return errors.New("retryPipeline needs the pipeline")
	case a.Tool == "retryPipeline":
		return nil // checked against main health (checkRetryPipeline)
	case slices.Contains(queueTools, a.Tool) && !running:
		return errors.New("this milestone has no autopilot queue")
	case a.Tool == "pause" && ap.Phase == aiv1.AutopilotPaused, a.Tool == "resume" && ap.Phase != aiv1.AutopilotPaused:
		return fmt.Errorf("autopilot is %s", strings.ToLower(string(ap.Phase)))
	case a.Tool == "pause" || a.Tool == "resume" || a.Tool == "endMilestone":
		return nil
	case a.Tool == "skip" && !slices.ContainsFunc(ap.Items, func(it aiv1.AutopilotItem) bool { return it.IID == a.IssueIID && it.State != aiv1.AutopilotItemMerged }):
		return fmt.Errorf("#%d is no planned issue that is not merged yet", a.IssueIID)
	case a.Tool == "skip":
		return nil
	case a.Tool == "rebase" || a.Tool == "mergeBase":
		return activeMR(ap, a.IssueIID)
	case a.Tool == "unskip" || a.Tool == "startAnyway" || a.Tool == "markDone":
		return validWayOut(a, st)
	}
	if a.Tool == "openIssue" {
		switch {
		case strings.TrimSpace(a.Title) == "":
			return errors.New("an issue needs a title")
		case slices.Contains(opened, titleKey(a.Title)):
			return errors.New("the pilot opened an issue with this title before")
		}
		return nil
	}
	t := tasks[a.IssueIID]
	switch {
	case t == nil:
		return fmt.Errorf("#%d is not an issue of this milestone with a task", a.IssueIID)
	case a.Tool == "reReview" && t.Status.Run.PushedSHA == "":
		return fmt.Errorf("#%d has no pushed head to review", a.IssueIID)
	case a.Tool == "reReview" && t.Status.Run.FullyReviewed():
		return fmt.Errorf("the head of #%d is already reviewed, without findings or reviewer errors", a.IssueIID)
	case a.Tool == "instruct" && a.Text == "" && !slices.Contains(aiv1.PilotInstructKinds, a.Kind):
		return fmt.Errorf("`%s` is not a kind of instruction", inertLine(a.Kind, 40))
	case a.Tool == "restartRun" && !meta.IsStatusConditionTrue(t.Status.Conditions, aiv1.ConditionStalled):
		return errors.New("the run is moving again")
	case a.Tool == "restartRun" && t.Status.Run.Interruptions >= aiv1.MaxInterruptions:
		// One more interruption fails the run: that is the owners' call.
		return fmt.Errorf("the run of #%d was interrupted %d times, another restart fails it; an owner can still `restart` it", a.IssueIID, t.Status.Run.Interruptions)
	}
	return nil
}

// activeMR refuses rebase and mergeBase on anything but the active issue of
// the queue (ap) with a merge request; not on the main-red fix. The tracking
// issue's command checks the MR itself when it runs (mrCommand).
func activeMR(ap *aiv1.AutopilotStatus, iid int64) error {
	i := slices.IndexFunc(ap.Items, func(it aiv1.AutopilotItem) bool { return it.IID == iid })
	switch {
	case i < 0 || ap.Items[i].State != aiv1.AutopilotItemActive:
		return fmt.Errorf("#%d is not the active issue of the queue", iid)
	case ap.Items[i].MergeRequestIID == 0:
		return fmt.Errorf("#%d has no merge request yet", iid)
	}
	return nil
}

// validWayOut checks unskip, startAnyway and markDone against the queue of
// st: unskip a skipped item, startAnyway a pending item with a skipped
// dependency, markDone a skipped or pending item (never the active or a
// merged one: that would pass the merge gate by). Whether the issue is
// still open is checked in GitLab (checkOpenIssue, autopilot.command).
func validWayOut(a aiv1.PilotAction, st *aiv1.AgentTask) error {
	if !queueRunning(st) {
		return errors.New("this milestone has no autopilot queue")
	}
	items := st.Status.Autopilot.Items
	i := slices.IndexFunc(items, func(it aiv1.AutopilotItem) bool { return it.IID == a.IssueIID })
	if i < 0 {
		return fmt.Errorf("#%d is not an issue of the queue", a.IssueIID)
	}
	state := items[i].State
	switch a.Tool {
	case "unskip":
		if state != aiv1.AutopilotItemSkipped {
			return fmt.Errorf("#%d is not skipped", a.IssueIID)
		}
	case "markDone":
		if state != aiv1.AutopilotItemSkipped && state != aiv1.AutopilotItemPending {
			return fmt.Errorf("#%d is %s; only a skipped or pending issue can be marked done", a.IssueIID, strings.ToLower(string(state)))
		}
	case "startAnyway":
		if state != aiv1.AutopilotItemPending {
			return fmt.Errorf("#%d is not pending", a.IssueIID)
		}
		var deps []int64
		for _, p := range st.Status.Plan {
			if p.IID == a.IssueIID {
				deps = p.DependsOn
			}
		}
		if !slices.ContainsFunc(items, func(it aiv1.AutopilotItem) bool {
			return it.State == aiv1.AutopilotItemSkipped && slices.Contains(deps, it.IID)
		}) {
			return fmt.Errorf("#%d depends on no skipped issue", a.IssueIID)
		}
	}
	return nil
}

// checkOpenIssue refuses an issue that is closed, gone or no longer in the
// milestone of st.
func checkOpenIssue(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, iid int64) error {
	is, err := gl.GetIssue(ctx, spec.GitLab.Project, iid)
	switch {
	case gitlab.IsNotFound(err):
		return fmt.Errorf("#%d does not exist", iid)
	case err != nil:
		return fmt.Errorf("getting issue #%d: %w", iid, err)
	case is.State != "opened":
		return fmt.Errorf("#%d is closed", iid)
	case is.Milestone == nil || st.Spec.Milestone == nil || is.Milestone.ID != st.Spec.Milestone.ID:
		return fmt.Errorf("#%d is not in this milestone", iid)
	}
	return nil
}

// checkRetryPipeline refuses to retry pipeline id unless it is the red
// pipeline of the default branch that main health (mh) knows: the newest
// one that finished, red after the watcher's own retry or without one.
func checkRetryPipeline(mh *aiv1.MainHealthStatus, id int64) error {
	if mh == nil || mh.State != aiv1.MainHealthRed || mh.PipelineID != id {
		return fmt.Errorf("pipeline %d is not the red pipeline of the default branch", id)
	}
	return nil
}

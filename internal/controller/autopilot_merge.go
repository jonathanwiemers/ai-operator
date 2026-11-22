package controller

import (
	"cmp"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/difflines"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// mergeBase asks the agent to merge the default branch into the MR and
// resolve the conflicts (spec.source.mergeBaseFor), once per head. The run
// is reviewed and checked by CI like any push, then the gate runs again. If
// that is switched off or was tried for this head already, it pauses.
func (a *autopilot) mergeBase(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask, why string) error {
	wf := a.spec.Workflow
	switch {
	case !*wf.ResolveConflicts:
		return a.pauseMR(ctx, it, why+"; resolve them on the branch (or merge the default branch into it), then `resume`")
	case task.Spec.Source.MergeBaseFor == mr.SHA:
		return a.pauseMR(ctx, it, why+", and merging the default branch did not resolve it; resolve the conflicts on the branch, then `resume`")
	}
	a.log.Info("autopilot asks the agent to merge the default branch", "mr", mr.IID, "sha", mr.SHA, "reason", why)
	if err := a.r.requestMergeBase(ctx, a.gl, a.spec, task, it.IID, mr.SHA); err != nil {
		return err
	}
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	// The merge is a new head, reviewed and checked anew: the item frees the
	// lane and is ready again after that.
	a.unready(it)
	return nil
}

// rebase asks GitLab to rebase the MR, once per item. The reviewed change is
// hashed first, so the rebased head can be checked against it. If a rebase
// cannot help, the agent merges the default branch instead.
func (a *autopilot) rebase(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask) error {
	diff, err := diffHash(ctx, a.gl, a.spec.GitLab.Project, mr.IID)
	if err != nil {
		return err
	}
	if diff == "" {
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("!%d needs a rebase, but its change is too large to check that a rebase keeps it", mr.IID))
	}
	if err := a.gl.RebaseMergeRequest(ctx, a.spec.GitLab.Project, mr.IID); err != nil {
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("GitLab could not rebase !%d (%v)", mr.IID, err))
	}
	a.log.Info("autopilot rebases the merge request", "mr", mr.IID, "from", mr.SHA)
	it.Rebases++
	it.RebasedFrom, it.RebasedDiff = mr.SHA, diff
	it.RebasedAt = &metav1.Time{Time: a.r.now()}
	return nil
}

// rebaseGrace is how long GitLab may take to show a rebased head after the
// rebase itself finished.
const rebaseGrace = 5 * time.Minute

// rebased hands the rebased head to the CI loop like a new push, once GitLab
// is done and the change is still the one that was reviewed.
func (a *autopilot) rebased(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask) error {
	unrebased := func(reason string) error {
		if err := a.pauseHead(ctx, it, reason); err != nil {
			return err
		}
		it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
		return nil
	}
	switch {
	case mr.RebaseInProgress:
		return nil
	case mr.MergeError != "" && mr.SHA == it.RebasedFrom:
		// The branch did not move: the agent merges the default branch.
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("the rebase of !%d failed (%s)", mr.IID, mr.MergeError))
	case mr.MergeError != "":
		return unrebased(fmt.Sprintf("the rebase of !%d failed: %s", mr.IID, mr.MergeError))
	case mr.SHA == it.RebasedFrom && mr.DetailedMergeStatus == "mergeable":
		// Nothing to rebase after all; the gate runs again.
		it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
		return nil
	case mr.SHA == it.RebasedFrom && mr.DetailedMergeStatus == "conflict":
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("the rebase of !%d ran into conflicts", mr.IID))
	case mr.SHA == it.RebasedFrom && it.RebasedAt != nil && a.r.now().Sub(it.RebasedAt.Time) < rebaseGrace:
		return nil // GitLab refreshes the MR after the rebase
	case mr.SHA == it.RebasedFrom:
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("the rebase of !%d did not go through (GitLab reports %q)", mr.IID, mr.DetailedMergeStatus))
	}
	// The review only holds if the rebase moved the change without changing
	// it; a commit pushed meanwhile, or a rebase that had to adapt lines,
	// changes the diff.
	diff, err := diffHash(ctx, a.gl, a.spec.GitLab.Project, mr.IID)
	if err != nil {
		return err
	}
	if diff == "" || diff != it.RebasedDiff {
		// The interdiff tells a mechanical change (the reviewers check the
		// new head again, no pause) from a push or a real one (a pause).
		var check rebaseCheck
		if diff != "" {
			if check, err = a.checkRebase(ctx, it, mr); err != nil {
				return err
			}
		}
		reReview := check.mechanical && it.AutoReReviews < maxAutoReReviews
		if check.mechanical && !reReview {
			check.why += fmt.Sprintf(", but #%d was re-reviewed after %d rebases without a pause already (at most %d)", it.IID, it.AutoReReviews, maxAutoReReviews)
		}
		note := rebaseNote(mr.IID, a.baseName(ctx), check, reReview)
		if note != "" {
			if _, err := a.gl.CreateMergeRequestNote(ctx, a.spec.GitLab.Project, mr.IID, note); err != nil {
				return fmt.Errorf("posting the interdiff on !%d: %w", mr.IID, err)
			}
		}
		var companion string
		if c := task.Status.Run.Companion; c != nil {
			companion = c.PushedSHA
		}
		if reReview {
			return a.reReviewRebased(ctx, it, mr, task, check, companion)
		}
		why := ""
		switch {
		case note != "":
			why = fmt.Sprintf(" It pauses because %s; the interdiff (reviewed → rebased) is on !%d.", check.why, mr.IID)
		case check.why != "":
			why = fmt.Sprintf(" It pauses because %s.", check.why)
		}
		if err := unrebased(fmt.Sprintf("after the rebase, the change of !%d is not the one that was reviewed (someone pushed, or the rebase changed lines).%s "+
			"The reviewers take the new head over and autopilot goes on once it is reviewed and green; "+
			"or comment `%s …` on #%d so the agent takes it over, or merge by hand", mr.IID, why, a.spec.Workflow.CommandPrefix, it.IID)); err != nil {
			return err
		}
		if err := a.r.askReview(ctx, task, aiv1.ReviewHeads(mr.SHA, companion)); err != nil {
			return err
		}
		if err := a.r.requestRun(ctx, a.gl, a.spec, task, it.IID); err != nil {
			return err
		}
		// A review-only run of the rebased head: passing on it is a re-review.
		a.askedRun(it, mr.SHA)
		return nil
	}
	a.log.Info("autopilot rebase done, waiting for CI", "mr", mr.IID, "sha", mr.SHA)
	orig := task.DeepCopy()
	if task.Status.Run.ReviewedSHA == it.RebasedFrom {
		task.Status.Run.ReviewedSHA = mr.SHA // not for a head an override let through
	}
	task.Status.Run.PushedSHA = mr.SHA
	task.Status.Run.PushedAt = &metav1.Time{Time: a.r.now()}
	task.Status.CI = homeCIReset(task.Status.CI, mr.SHA)
	if err := a.r.Status().Patch(ctx, task, client.MergeFrom(orig)); err != nil {
		return err
	}
	if a.overridden(it, it.RebasedFrom, orig.Status.Run) {
		a.ap.MergeOverride.SHA = mr.SHA // the same change, like its review
	}
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	if a.spec.CI.Disabled {
		return nil // merged on the next poll
	}
	// Back to working, so the CI loop follows the new pipeline. If this
	// fails, the gate hands the unchecked head back on the next poll.
	return a.r.backToWorking(ctx, a.gl, a.spec, it.IID)
}

// diffHash hashes the change of an MR of project (home, or the companion's):
// the files and their added and removed lines, without positions, so a
// rebase that only moves the change keeps the
// hash. It is empty if GitLab does not return the whole change.
func diffHash(ctx context.Context, gl gitlab.API, project string, iid int64) (string, error) {
	diffs, err := gl.ListMergeRequestDiffs(ctx, project, iid)
	if err != nil {
		return "", fmt.Errorf("listing the diffs of !%d: %w", iid, err)
	}
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].NewPath != diffs[j].NewPath {
			return diffs[i].NewPath < diffs[j].NewPath
		}
		return diffs[i].OldPath < diffs[j].OldPath
	})
	var b strings.Builder
	for _, d := range diffs {
		if d.TooLarge || d.Collapsed {
			return "", nil
		}
		fmt.Fprintf(&b, "%s\x00%s\x00%t%t%t\n", d.OldPath, d.NewPath, d.NewFile, d.RenamedFile, d.DeletedFile)
		for _, line := range strings.Split(d.Diff, "\n") {
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "Binary") {
				b.WriteString(line + "\n")
			}
		}
	}
	return hashString(b.String()), nil
}

// merged records a merged item and closes its issue if GitLab did not. mr is
// nil for an issue closed as done without a known MR.
func (a *autopilot) merged(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest) error {
	glProject := a.spec.GitLab.Project
	wf := a.spec.Workflow
	is, err := a.gl.GetIssue(ctx, glProject, it.IID)
	if err != nil {
		return fmt.Errorf("getting issue #%d: %w", it.IID, err)
	}
	notes, err := a.gl.ListIssueNotes(ctx, glProject, it.IID)
	if err != nil {
		return fmt.Errorf("listing notes of issue #%d: %w", it.IID, err)
	}
	if is.State == "opened" {
		// "Closes #N" only closes the issue on the default branch.
		if _, err := a.gl.UpdateIssue(ctx, glProject, it.IID, gitlab.UpdateIssueOptions{
			StateEvent: "close", AddLabels: []string{wf.DoneLabel},
			RemoveLabels: []string{wf.ReviewLabel, wf.WorkingLabel},
		}); err != nil {
			return fmt.Errorf("closing issue #%d: %w", it.IID, err)
		}
	}
	if orig := a.item(marker.DecisionIssue(it.RevisitOf)); orig != nil && it.RevisitOf != "" {
		// The follow-up superseded a decision of an earlier issue.
		ois, err := a.gl.GetIssue(ctx, glProject, orig.IID)
		if err != nil {
			return fmt.Errorf("getting issue #%d: %w", orig.IID, err)
		}
		onotes, err := a.gl.ListIssueNotes(ctx, glProject, orig.IID)
		if err != nil {
			return fmt.Errorf("listing notes of issue #%d: %w", orig.IID, err)
		}
		orig.Decisions = collectDecisions(ois, onotes, a.project.Status.BotUserID)
	}
	it.State, it.Hold = aiv1.AutopilotItemMerged, nil
	it.MergedAt = &metav1.Time{Time: a.r.now()}
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	a.unready(it)
	it.Decisions = collectDecisions(is, notes, a.project.Status.BotUserID)
	if mr != nil {
		it.MergedSHA = cmp.Or(mr.MergeCommitSHA, mr.SquashCommitSHA, mr.SHA)
		it.MergeRequestIID, it.MergeRequestURL = mr.IID, cmp.Or(mr.WebURL, it.MergeRequestURL)
		a.countMergedLines(ctx, it, mr.IID)
	}
	a.log.Info("autopilot merged", "issue", it.IID, "mr", it.MergeRequestIID, "sha", it.MergedSHA)
	a.releaseDue(it)
	return nil
}

// countMergedLines records the lines the merged MR iid merged, once: an item
// that has them makes no call. It is a measurement: a failed call only logs
// and leaves MergedLines unset.
func (a *autopilot) countMergedLines(ctx context.Context, it *aiv1.AutopilotItem, iid int64) {
	if it.MergedLines != nil {
		return
	}
	diffs, err := a.gl.ListMergeRequestDiffs(ctx, a.spec.GitLab.Project, iid)
	if err != nil {
		a.log.Info("could not count the merged lines", "issue", it.IID, "mr", iid, "error", err.Error())
		return
	}
	lines := difflines.Count(diffs)
	it.MergedLines = &lines
}

// collectDecisions lists the newest aiv1.MaxAutopilotItemDecisions decisions
// on an issue: the planner's in the description and the implementer's in the
// bot's comments, oldest first. The full history is in history_events.
func collectDecisions(is *gitlab.Issue, notes []gitlab.Note, botID int64) []aiv1.DecisionRef {
	var out []aiv1.DecisionRef
	for _, d := range marker.Decisions(is.Description) {
		out = append(out, aiv1.DecisionRef{ID: d.ID, Title: d.Title, URL: is.WebURL, Superseded: d.Superseded})
	}
	for _, n := range notes {
		if botID == 0 || n.Author.ID != botID || n.System {
			continue
		}
		for _, d := range marker.Decisions(n.Body) {
			out = append(out, aiv1.DecisionRef{ID: d.ID, Title: d.Title, URL: fmt.Sprintf("%s#note_%d", is.WebURL, n.ID), Superseded: d.Superseded})
		}
	}
	// Keep the newest (last) aiv1.MaxAutopilotItemDecisions.
	return out[max(0, len(out)-aiv1.MaxAutopilotItemDecisions):]
}

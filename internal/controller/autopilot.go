package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// syncAutopilot drives an autopilot milestone, one step per poll:
//
//	no active issue         -> move the next issue whose dependencies are merged to ready
//	active issue in review  -> merge gate: pipeline green, review clean, MR mergeable -> merge
//	MR needs a rebase       -> ask GitLab to rebase once, then treat the new head as a push
//	merged                  -> record it, close the issue if needed, next issue
//	needs a human           -> pause with a comment on the tracking issue
//
// The operator only calls the GitLab API here; no model decides whether to
// merge. State lives in the spec task's status.autopilot, the tracking issue
// shows it.
func (r *AgentProjectReconciler) syncAutopilot(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask) error {
	if st.Spec.Milestone == nil {
		return nil
	}
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
	if st.Status.Autopilot != nil {
		ap = st.Status.Autopilot.DeepCopy()
	}
	a := &autopilot{r: r, gl: gl, project: project, spec: spec, st: st, ap: ap, tasks: tasks,
		log: log.FromContext(ctx).WithValues("milestone", st.Spec.Milestone.Title)}

	if !st.Spec.Autopilot {
		if st.Status.Autopilot == nil || ap.Phase == aiv1.AutopilotOff {
			return nil
		}
		// The marker was removed: stop, and say so on the tracking issue.
		a.log.Info("autopilot switched off")
		ap.Phase = aiv1.AutopilotOff
		if err := a.comment(ctx, "⏹️ Autopilot is off: the marker was removed from the milestone. Nothing is merged any more; open issues continue in the normal flow."); err != nil {
			return err
		}
		if err := a.closeTracking(ctx); err != nil {
			return err
		}
		return a.save(ctx)
	}
	if len(st.Status.Plan) == 0 {
		return nil // not planned yet
	}
	if ap.Phase == aiv1.AutopilotOff {
		ap.Phase = aiv1.AutopilotRunning
	}

	issues, err := gl.ListIssues(ctx, spec.GitLab.Project, gitlab.ListIssuesOptions{Milestone: st.Spec.Milestone.Title, State: "all"})
	if err != nil {
		return fmt.Errorf("listing issues of milestone %q: %w", st.Spec.Milestone.Title, err)
	}
	a.issues = map[int64]gitlab.Issue{}
	for _, is := range issues {
		a.issues[is.IID] = is
	}
	order, cycle := queueOrder(st.Status.Plan)
	ap.Items = mergeQueue(ap.Items, order)
	if err := a.ensureTracking(ctx); err != nil {
		return err
	}
	if err := a.commands(ctx); err != nil {
		return err
	}
	switch {
	case ap.Phase != aiv1.AutopilotRunning && ap.Phase != aiv1.AutopilotDone:
	case len(cycle) > 0:
		if err := a.pause(ctx, "the plan has a dependency cycle between "+issueList(cycle)); err != nil {
			return err
		}
	default:
		if err := a.step(ctx); err != nil {
			return err
		}
	}
	if err := a.updateTracking(ctx); err != nil {
		return err
	}
	return a.save(ctx)
}

// autopilot is one poll of one autopilot milestone.
type autopilot struct {
	r       *AgentProjectReconciler
	gl      gitlab.API
	project *aiv1.AgentProject
	spec    aiv1.AgentProjectSpec
	st      *aiv1.AgentTask
	ap      *aiv1.AutopilotStatus
	tasks   map[int64]*aiv1.AgentTask
	issues  map[int64]gitlab.Issue
	log     logr.Logger
}

// queueOrder orders the plan topologically: dependencies first, ties in plan
// order. It returns the issues of a dependency cycle, if there is one.
func queueOrder(plan []aiv1.PlannedIssue) ([]int64, []int64) {
	index := map[int64]int{}
	for i, p := range plan {
		index[p.IID] = i
	}
	waiting := make([]int, len(plan))
	dependants := map[int64][]int64{}
	for _, p := range plan {
		for _, d := range p.DependsOn {
			if _, ok := index[d]; ok {
				waiting[index[p.IID]]++
				dependants[d] = append(dependants[d], p.IID)
			}
		}
	}
	var order []int64
	done := make([]bool, len(plan))
	for len(order) < len(plan) {
		next := -1
		for i := range plan {
			if !done[i] && waiting[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			var cycle []int64
			for i, p := range plan {
				if !done[i] {
					cycle = append(cycle, p.IID)
				}
			}
			return order, cycle
		}
		done[next] = true
		order = append(order, plan[next].IID)
		for _, dep := range dependants[plan[next].IID] {
			waiting[index[dep]]--
		}
	}
	return order, nil
}

// mergeQueue keeps the state of known items and appends new ones in queue
// order. Items the plan dropped stay once they were started, so the history
// is kept; pending ones are dropped with the plan.
func mergeQueue(items []aiv1.AutopilotItem, order []int64) []aiv1.AutopilotItem {
	known := map[int64]aiv1.AutopilotItem{}
	for _, it := range items {
		known[it.IID] = it
	}
	out := make([]aiv1.AutopilotItem, 0, len(order))
	seen := map[int64]bool{}
	for _, iid := range order {
		it, ok := known[iid]
		if !ok {
			it = aiv1.AutopilotItem{IID: iid, State: aiv1.AutopilotItemPending}
		}
		out = append(out, it)
		seen[iid] = true
	}
	for _, it := range items {
		if !seen[it.IID] && it.State != aiv1.AutopilotItemPending {
			out = append(out, it)
		}
	}
	return out
}

func (a *autopilot) item(iid int64) *aiv1.AutopilotItem {
	for i := range a.ap.Items {
		if a.ap.Items[i].IID == iid {
			return &a.ap.Items[i]
		}
	}
	return nil
}

func (a *autopilot) deps(iid int64) []int64 {
	for _, p := range a.st.Status.Plan {
		if p.IID == iid {
			return p.DependsOn
		}
	}
	return nil
}

// step does at most one thing for the queue.
func (a *autopilot) step(ctx context.Context) error {
	for i := range a.ap.Items {
		if a.ap.Items[i].State == aiv1.AutopilotItemActive {
			return a.drive(ctx, &a.ap.Items[i])
		}
	}
	return a.pick(ctx)
}

// pick activates the next pending issue whose dependencies are merged.
func (a *autopilot) pick(ctx context.Context) error {
	pending := 0
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		if it.State != aiv1.AutopilotItemPending {
			continue
		}
		pending++
		blockedBy := int64(0)
		for _, d := range a.deps(it.IID) {
			if dep := a.item(d); dep != nil && dep.State != aiv1.AutopilotItemMerged {
				blockedBy = d
				break
			}
		}
		if blockedBy != 0 {
			continue
		}
		wf := a.spec.Workflow
		is, ok := a.issues[it.IID]
		if !ok || is.State != "opened" {
			it.State = aiv1.AutopilotItemSkipped
			return a.comment(ctx, fmt.Sprintf("⏭️ #%d was closed or deleted before autopilot got to it, so it is skipped.", it.IID))
		}
		it.State = aiv1.AutopilotItemActive
		a.ap.Phase = aiv1.AutopilotRunning // a re-plan can add issues after "done"
		a.log.Info("autopilot starts the next issue", "issue", it.IID)
		if hasAny(is, wf.ActiveLabels()...) {
			return nil // a human already handed it over
		}
		if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, it.IID, gitlab.UpdateIssueOptions{
			AddLabels: []string{wf.ReadyLabel}, RemoveLabels: []string{wf.DraftLabel},
		}); err != nil {
			return fmt.Errorf("moving issue #%d to %q: %w", it.IID, wf.ReadyLabel, err)
		}
		return nil
	}
	if pending > 0 {
		// Everything left waits for an issue that was skipped.
		var waiting []int64
		for _, it := range a.ap.Items {
			if it.State == aiv1.AutopilotItemPending {
				waiting = append(waiting, it.IID)
			}
		}
		return a.pause(ctx, issueList(waiting)+" depend on skipped issues; finish those by hand or `skip` them too")
	}
	if a.ap.Phase != aiv1.AutopilotDone {
		a.ap.Phase = aiv1.AutopilotDone
		merged := 0
		for _, it := range a.ap.Items {
			if it.State == aiv1.AutopilotItemMerged {
				merged++
			}
		}
		a.log.Info("autopilot milestone done", "merged", merged)
		if err := a.comment(ctx, fmt.Sprintf("✅ Autopilot is done: %d of %d issues merged. The decisions are listed in the description.", merged, len(a.ap.Items))); err != nil {
			return err
		}
		return a.closeTracking(ctx)
	}
	return nil
}

// drive moves the active issue towards its merge.
func (a *autopilot) drive(ctx context.Context, it *aiv1.AutopilotItem) error {
	wf := a.spec.Workflow
	glProject := a.spec.GitLab.Project
	is, ok := a.issues[it.IID]
	task := a.tasks[it.IID]
	if task != nil && task.Status.Run.MergeRequestIID != 0 {
		it.MergeRequestIID, it.MergeRequestURL = task.Status.Run.MergeRequestIID, task.Status.Run.MergeRequestURL
	}
	if !ok || is.State != "opened" {
		// Closed: merged by hand, or given up.
		if it.MergeRequestIID != 0 {
			mr, err := a.gl.GetMergeRequest(ctx, glProject, it.MergeRequestIID)
			if err != nil && !gitlab.IsNotFound(err) {
				return fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
			}
			if err == nil && mr.State == "merged" {
				return a.merged(ctx, it, mr)
			}
		}
		it.State = aiv1.AutopilotItemSkipped
		return a.comment(ctx, fmt.Sprintf("⏭️ #%d was closed without a merge, so it is skipped.", it.IID))
	}
	switch {
	case is.HasLabel(wf.NeedsInputLabel):
		return a.pause(ctx, fmt.Sprintf("#%d needs input (see its last comments): CI is still red, the run failed or the agent asked a question", it.IID))
	case !is.HasLabel(wf.ReviewLabel) || task == nil || task.RunPending():
		return nil // the agent or CI is still working on it
	}

	// Merge gate.
	run := task.Status.Run
	switch {
	case run.LastResult != aiv1.RunResultSucceeded:
		return a.pause(ctx, fmt.Sprintf("the last run of #%d ended with %q", it.IID, run.LastResult))
	case run.BlockingFindings > 0:
		return a.pause(ctx, fmt.Sprintf("%d blocking review findings remain on !%d", run.BlockingFindings, it.MergeRequestIID))
	case run.ReviewErrors > 0:
		return a.pause(ctx, fmt.Sprintf("%d reviewers failed on #%d, so the change is not fully reviewed", run.ReviewErrors, it.IID))
	case it.MergeRequestIID == 0 || run.PushedSHA == "":
		return a.pause(ctx, fmt.Sprintf("#%d is in review without a pushed merge request", it.IID))
	case len(a.spec.Agents.Review.Reviewers) > 0 && run.ReviewedSHA != run.PushedSHA:
		return a.pause(ctx, fmt.Sprintf("the head of !%d (%s) was not reviewed (the last review saw %s)", it.MergeRequestIID, short(run.PushedSHA), short(run.ReviewedSHA)))
	}
	if !a.spec.CI.Disabled {
		ci := task.Status.CI
		switch {
		case ci == nil || ci.SHA != run.PushedSHA:
			return nil // the CI loop has not finished this commit yet
		case ci.State == aiv1.CIStateCanceled:
			return a.pause(ctx, fmt.Sprintf("the pipeline of !%d was canceled", it.MergeRequestIID))
		case ci.State == aiv1.CIStateBaseFailing:
			return a.pause(ctx, fmt.Sprintf("CI of !%d is red with jobs that also fail on the default branch (%s)", it.MergeRequestIID, jobList(ci.FailedJobs)))
		case ci.State != aiv1.CIStateSuccess && ci.State != aiv1.CIStateNone:
			return nil
		}
	}

	mr, err := a.gl.GetMergeRequest(ctx, glProject, it.MergeRequestIID)
	if err != nil {
		return fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
	}
	switch {
	case mr.State == "merged":
		return a.merged(ctx, it, mr)
	case mr.State != "opened":
		return a.pause(ctx, fmt.Sprintf("!%d was %s without a merge", mr.IID, mr.State))
	case it.RebasedFrom != "":
		return a.rebased(ctx, it, mr, task)
	case mr.SHA != run.PushedSHA:
		return a.pause(ctx, fmt.Sprintf("the head of !%d (%s) is not the commit the agent pushed and CI checked (%s); someone pushed to the branch", mr.IID, short(mr.SHA), short(run.PushedSHA)))
	}
	switch mr.DetailedMergeStatus {
	case "mergeable":
	case "draft_status":
		title := strings.TrimSpace(strings.TrimPrefix(mr.Title, "Draft:"))
		if _, err := a.gl.UpdateMergeRequest(ctx, glProject, mr.IID, gitlab.UpdateMergeRequestOptions{Title: title}); err != nil {
			return a.pause(ctx, fmt.Sprintf("could not mark !%d ready: %v", mr.IID, err))
		}
		a.log.Info("autopilot marked the merge request ready", "mr", mr.IID)
		return nil // merged on the next poll, once GitLab rechecked it
	case "need_rebase":
		return a.rebase(ctx, it, mr)
	case "checking", "unchecked", "preparing", "approvals_syncing", "ci_still_running", "ci_must_pass":
		return nil
	case "conflict":
		return a.pause(ctx, fmt.Sprintf("!%d has merge conflicts with the default branch; resolve them on the branch, then `resume`", mr.IID))
	case "not_approved":
		return a.pause(ctx, fmt.Sprintf("!%d needs an approval before it can be merged", mr.IID))
	case "discussions_not_resolved":
		return a.pause(ctx, fmt.Sprintf("!%d has unresolved discussions", mr.IID))
	default:
		return a.pause(ctx, fmt.Sprintf("GitLab reports !%d as %q", mr.IID, mr.DetailedMergeStatus))
	}

	a.log.Info("autopilot merges", "mr", mr.IID, "sha", mr.SHA)
	res, err := a.gl.MergeMergeRequest(ctx, glProject, mr.IID, gitlab.MergeOptions{SHA: run.PushedSHA, ShouldRemoveSourceBranch: true})
	switch code := gitlab.StatusCode(err); {
	case err == nil:
		if res.State != "merged" {
			return nil // GitLab merges asynchronously; seen on the next poll
		}
		return a.merged(ctx, it, res)
	case code == 401 || code == 403:
		return a.pause(ctx, fmt.Sprintf("the bot may not merge !%d into the default branch (HTTP %d); allow Developers to merge, or give the bot the Maintainer role", mr.IID, code))
	case code == 406:
		return a.rebase(ctx, it, mr)
	case code >= 400 && code < 500:
		return a.pause(ctx, fmt.Sprintf("GitLab refused to merge !%d: %v", mr.IID, err))
	default:
		return fmt.Errorf("merging !%d: %w", mr.IID, err)
	}
}

// rebase asks GitLab to rebase the MR, once per item.
func (a *autopilot) rebase(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest) error {
	if it.Rebases > 0 {
		return a.pause(ctx, fmt.Sprintf("!%d still cannot be merged after a rebase; rebase or merge the default branch by hand, then `resume`", mr.IID))
	}
	if err := a.gl.RebaseMergeRequest(ctx, a.spec.GitLab.Project, mr.IID); err != nil {
		return a.pause(ctx, fmt.Sprintf("GitLab could not rebase !%d: %v", mr.IID, err))
	}
	a.log.Info("autopilot rebases the merge request", "mr", mr.IID, "from", mr.SHA)
	it.Rebases++
	it.RebasedFrom = mr.SHA
	return nil
}

// rebased hands the rebased head to the CI loop like a new push, once GitLab
// is done.
func (a *autopilot) rebased(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask) error {
	switch {
	case mr.RebaseInProgress || mr.SHA == it.RebasedFrom:
		if mr.DetailedMergeStatus == "conflict" {
			it.RebasedFrom = ""
			return a.pause(ctx, fmt.Sprintf("the rebase of !%d ran into conflicts; resolve them on the branch, then `resume`", mr.IID))
		}
		return nil
	case mr.MergeError != "":
		it.RebasedFrom = ""
		return a.pause(ctx, fmt.Sprintf("the rebase of !%d failed: %s", mr.IID, mr.MergeError))
	}
	a.log.Info("autopilot rebase done, waiting for CI", "mr", mr.IID, "sha", mr.SHA)
	it.RebasedFrom = ""
	orig := task.DeepCopy()
	// A rebase by GitLab moves the change onto the new default branch
	// without changing it: the review still holds, CI has to run again.
	if task.Status.Run.ReviewedSHA == task.Status.Run.PushedSHA {
		task.Status.Run.ReviewedSHA = mr.SHA
	}
	task.Status.Run.PushedSHA = mr.SHA
	task.Status.Run.PushedAt = &metav1.Time{Time: a.r.now()}
	task.Status.CI = nil
	if err := a.r.Status().Patch(ctx, task, client.MergeFrom(orig)); err != nil {
		return err
	}
	if a.spec.CI.Disabled {
		return nil // merged on the next poll
	}
	wf := a.spec.Workflow
	// Back to working, so the CI loop follows the new pipeline.
	if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, it.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d back to %q: %w", it.IID, wf.WorkingLabel, err)
	}
	return nil
}

// merged records a merged item and closes its issue if GitLab did not.
func (a *autopilot) merged(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest) error {
	it.State = aiv1.AutopilotItemMerged
	it.RebasedFrom = ""
	it.MergedSHA = firstNonEmpty(mr.MergeCommitSHA, mr.SquashCommitSHA, mr.SHA)
	it.MergeRequestIID, it.MergeRequestURL = mr.IID, firstNonEmpty(mr.WebURL, it.MergeRequestURL)
	a.log.Info("autopilot merged", "issue", it.IID, "mr", mr.IID, "sha", it.MergedSHA)
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
	it.Decisions = collectDecisions(is, notes, a.project.Status.BotUserID)
	if is.State == "opened" {
		// "Closes #N" only closes the issue on the default branch.
		if _, err := a.gl.UpdateIssue(ctx, glProject, it.IID, gitlab.UpdateIssueOptions{
			StateEvent: "close", AddLabels: []string{wf.DoneLabel},
			RemoveLabels: []string{wf.ReviewLabel, wf.WorkingLabel},
		}); err != nil {
			return fmt.Errorf("closing issue #%d: %w", it.IID, err)
		}
	}
	return nil
}

// collectDecisions lists the decisions on an issue: the planner's in the
// description and the implementer's in the bot's comments.
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
	return out
}

// pause stops the queue and says why, once.
func (a *autopilot) pause(ctx context.Context, reason string) error {
	if a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedReason == reason {
		return nil
	}
	a.log.Info("autopilot paused", "reason", reason)
	a.ap.Phase = aiv1.AutopilotPaused
	a.ap.PausedReason = reason
	return a.comment(ctx, fmt.Sprintf("⏸️ Autopilot paused: %s.\n\nFix it, then comment `%s resume`.", reason, a.spec.Workflow.CommandPrefix))
}

// commands handles "/agent pause|resume|skip #N" on the tracking issue.
func (a *autopilot) commands(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	notes, err := a.gl.ListIssueNotes(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID)
	if err != nil {
		return fmt.Errorf("listing notes of the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	prefix := a.spec.Workflow.CommandPrefix
	for _, n := range notes {
		if n.ID <= a.ap.LastCommandNoteID || !IsCommand(n, a.project.Status.BotUserID, prefix) {
			continue
		}
		a.ap.LastCommandNoteID = n.ID
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(n.Body), prefix))
		if len(fields) == 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSuffix(fields[0], ":")) {
		case "pause":
			a.ap.Phase, a.ap.PausedReason = aiv1.AutopilotPaused, "paused by @"+n.Author.Username
		case "resume":
			if a.ap.Phase == aiv1.AutopilotPaused {
				a.ap.Phase, a.ap.PausedReason = aiv1.AutopilotRunning, ""
			}
		case "skip":
			iid := int64(0)
			if len(fields) > 1 {
				iid, _ = strconv.ParseInt(strings.TrimPrefix(fields[1], "#"), 10, 64)
			}
			it := a.item(iid)
			if it == nil || it.State == aiv1.AutopilotItemMerged {
				if err := a.comment(ctx, fmt.Sprintf("`skip` needs a planned issue that is not merged yet, e.g. `%s skip #12`.", prefix)); err != nil {
					return err
				}
				continue
			}
			it.State, it.RebasedFrom = aiv1.AutopilotItemSkipped, ""
		}
	}
	return nil
}

// ensureTracking creates the tracking issue on the first poll.
func (a *autopilot) ensureTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID != 0 {
		if is, ok := a.issues[a.ap.TrackingIssueIID]; ok && is.State != "opened" && a.ap.Phase != aiv1.AutopilotDone {
			// Re-planned or switched back on after it was closed.
			if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{StateEvent: "reopen"}); err != nil {
				return fmt.Errorf("reopening the tracking issue #%d: %w", is.IID, err)
			}
		}
		return nil
	}
	is, err := a.gl.CreateIssue(ctx, a.spec.GitLab.Project, gitlab.CreateIssueOptions{
		Title:       "🤖 Autopilot: " + a.st.Spec.Milestone.Title,
		Description: a.renderTracking(),
		Labels:      a.spec.Workflow.GeneratedLabel,
		MilestoneID: a.st.Spec.Milestone.ID,
	})
	if err != nil {
		return fmt.Errorf("creating the tracking issue: %w", err)
	}
	a.log.Info("autopilot tracking issue created", "issue", is.IID)
	a.ap.TrackingIssueIID = is.IID
	a.ap.TrackingHash = hashString(a.renderTracking())
	return nil
}

func (a *autopilot) updateTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	body := a.renderTracking()
	if hash := hashString(body); hash != a.ap.TrackingHash {
		if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, gitlab.UpdateIssueOptions{Description: body}); err != nil {
			return fmt.Errorf("updating the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
		}
		a.ap.TrackingHash = hash
	}
	return nil
}

func (a *autopilot) closeTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	if err := a.updateTracking(ctx); err != nil {
		return err
	}
	if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, gitlab.UpdateIssueOptions{StateEvent: "close"}); err != nil {
		return fmt.Errorf("closing the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	return nil
}

func (a *autopilot) comment(ctx context.Context, body string) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	if _, err := a.gl.CreateIssueNote(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, "🤖 "+body); err != nil {
		return fmt.Errorf("commenting on the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	return nil
}

func (a *autopilot) save(ctx context.Context) error {
	if equality.Semantic.DeepEqual(a.st.Status.Autopilot, a.ap) {
		return nil
	}
	orig := a.st.DeepCopy()
	a.st.Status.Autopilot = a.ap
	return a.r.Status().Patch(ctx, a.st, client.MergeFrom(orig))
}

// renderTracking renders the tracking issue's description.
func (a *autopilot) renderTracking() string {
	var b strings.Builder
	prefix := a.spec.Workflow.CommandPrefix
	fmt.Fprintf(&b, "**Autopilot** for milestone **%s**: ", a.st.Spec.Milestone.Title)
	switch a.ap.Phase {
	case aiv1.AutopilotPaused:
		fmt.Fprintf(&b, "⏸️ paused, %s. Comment `%s resume` to continue.\n", a.ap.PausedReason, prefix)
	case aiv1.AutopilotDone:
		b.WriteString("✅ done.\n")
	case aiv1.AutopilotOff:
		b.WriteString("⏹️ off (the marker was removed from the milestone).\n")
	default:
		b.WriteString("▶️ running.\n")
	}
	b.WriteString("\nThe operator works through the planned issues one at a time, in dependency order. It merges each merge request " +
		"when its pipeline is green and its review is clean, then starts the next issue. The agents take their defaults instead of " +
		"asking; every decision is listed below and can be changed with `" + prefix + " revisit <id> <what you want instead>` on its issue.\n\n")
	fmt.Fprintf(&b, "Commands, as comments here: `%s pause`, `%s resume`, `%s skip #<issue>`.\n\n### Issues\n\n", prefix, prefix, prefix)
	for _, it := range a.ap.Items {
		title := a.issues[it.IID].Title
		mr := ""
		if it.MergeRequestIID != 0 {
			mr = fmt.Sprintf(" !%d", it.MergeRequestIID)
		}
		switch it.State {
		case aiv1.AutopilotItemMerged:
			fmt.Fprintf(&b, "- [x] #%d %s: merged%s (`%s`)\n", it.IID, title, mr, short(it.MergedSHA))
		case aiv1.AutopilotItemSkipped:
			fmt.Fprintf(&b, "- [ ] ~~#%d %s~~: skipped\n", it.IID, title)
		case aiv1.AutopilotItemActive:
			fmt.Fprintf(&b, "- [ ] #%d %s: **in progress**%s (%s)\n", it.IID, title, mr, a.labelOf(it.IID))
		default:
			waits := ""
			if deps := a.deps(it.IID); len(deps) > 0 {
				waits = ", after " + issueList(deps)
			}
			fmt.Fprintf(&b, "- [ ] #%d %s: pending%s\n", it.IID, title, waits)
		}
	}
	var decisions []string
	for _, it := range a.ap.Items {
		for _, d := range it.Decisions {
			line := fmt.Sprintf("[%s](%s) %s (#%d)", d.ID, d.URL, d.Title, it.IID)
			if d.Superseded {
				line = "~~" + line + "~~ superseded"
			}
			decisions = append(decisions, "- "+line)
		}
	}
	b.WriteString("\n### Decisions\n\n")
	if len(decisions) == 0 {
		b.WriteString("_Listed here once an issue is merged; until then they are on the issue._\n")
	} else {
		b.WriteString(strings.Join(decisions, "\n") + "\n")
	}
	b.WriteString("\n" + marker.TrackingIssue + "\n")
	return b.String()
}

func (a *autopilot) labelOf(iid int64) string {
	wf := a.spec.Workflow
	is := a.issues[iid]
	for _, l := range []string{wf.ReadyLabel, wf.WorkingLabel, wf.NeedsInputLabel, wf.ReviewLabel} {
		if is.HasLabel(l) {
			return l
		}
	}
	return "queued"
}

func hasAny(is gitlab.Issue, labels ...string) bool {
	for _, l := range labels {
		if is.HasLabel(l) {
			return true
		}
	}
	return false
}

func issueList(iids []int64) string {
	parts := make([]string, len(iids))
	for i, iid := range iids {
		parts[i] = fmt.Sprintf("#%d", iid)
	}
	return strings.Join(parts, ", ")
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

package controller

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
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
func (r *AgentProjectReconciler) syncAutopilot(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, lane bool) error {
	switch {
	case st.Spec.Milestone == nil:
		return nil
	case !st.Spec.Autopilot && (st.Status.Autopilot == nil || st.Status.Autopilot.Phase == aiv1.AutopilotOff):
		return nil // never on, or already off
	case st.Spec.Autopilot && len(st.Status.Plan) == 0:
		return nil // not planned yet
	}
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
	if st.Status.Autopilot != nil {
		ap = st.Status.Autopilot.DeepCopy()
	}
	a := &autopilot{r: r, gl: gl, project: project, spec: spec, st: st, ap: ap, tasks: tasks, lane: lane,
		log: log.FromContext(ctx).WithValues("milestone", st.Spec.Milestone.Title)}
	err := a.sync(ctx)
	// Save what was done even if a later step failed: its GitLab side
	// effects (a created tracking issue, a requested rebase) have happened.
	if serr := a.save(ctx); err == nil {
		err = serr
	}
	return err
}

func (a *autopilot) sync(ctx context.Context) error {
	if !a.st.Spec.Autopilot {
		return a.off(ctx)
	}
	if a.ap.Phase == aiv1.AutopilotOff {
		a.ap.Phase = aiv1.AutopilotRunning
	}
	issues, err := a.gl.ListIssues(ctx, a.spec.GitLab.Project, gitlab.ListIssuesOptions{Milestone: a.st.Spec.Milestone.Title, State: "all"})
	if err != nil {
		return fmt.Errorf("listing issues of milestone %q: %w", a.st.Spec.Milestone.Title, err)
	}
	a.issues = map[int64]gitlab.Issue{}
	for _, is := range issues {
		a.issues[is.IID] = is
	}
	if err := a.loadMainRedFix(ctx); err != nil {
		return err
	}
	order, cycle := queueOrder(a.st.Status.Plan)
	a.ap.Items = mergeQueue(a.ap.Items, order)
	if err := a.ensureTracking(ctx); err != nil {
		return err
	}
	if err := a.commands(ctx); err != nil {
		return err
	}
	if err := a.autoResume(ctx); err != nil {
		return err
	}
	if err := a.freezeOnMainRed(ctx); err != nil {
		return err
	}
	if err := a.mainRedLane(ctx); err != nil {
		return err
	}
	switch {
	case a.ap.Phase != aiv1.AutopilotRunning && a.ap.Phase != aiv1.AutopilotDone:
	case len(cycle) > 0:
		if err := a.pause(ctx, "the plan has a dependency cycle between "+issueList(cycle)); err != nil {
			return err
		}
	default:
		if err := a.step(ctx); err != nil {
			return err
		}
	}
	return a.updateTracking(ctx)
}

// off stops autopilot after its marker was removed or the milestone is no
// longer planned, and says so on the tracking issue.
func (a *autopilot) off(ctx context.Context) error {
	a.log.Info("autopilot switched off")
	if a.ap.Phase != aiv1.AutopilotDone {
		if err := a.comment(ctx, "⏹️ Autopilot is off: the marker was removed from the milestone, or the milestone is no longer planned. "+
			"Nothing is merged any more; open issues continue in the normal flow."); err != nil {
			return err
		}
	}
	prev := a.ap.Phase
	a.ap.Phase = aiv1.AutopilotOff // for the description
	if err := a.closeTracking(ctx); err != nil {
		a.ap.Phase = prev // tried again on the next poll
		return err
	}
	return nil
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
	// proj is read once per poll, when needed.
	proj *gitlab.Project
	// lane: this milestone drives the main-red issue, mainRed while red.
	lane    bool
	mainRed int64
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
// is kept; pending ones are dropped with the plan. Revisit follow-ups are not
// in the plan and always stay.
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
		if !seen[it.IID] && (it.State != aiv1.AutopilotItemPending || it.RevisitOf != "") {
			out = append(out, it)
		}
	}
	return out
}

func (a *autopilot) item(iid int64) *aiv1.AutopilotItem {
	if fix := a.ap.MainRedFix; fix != nil && fix.IID == iid {
		return fix
	}
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
	if a.st.RunPending() {
		return nil // the milestone is being planned again; pick from the new plan
	}
	return a.pick(ctx)
}

// pick activates the next pending issue whose dependencies are merged: the
// plan's, and open issues it is linked as "blocked by" to in GitLab
// (Premium), so a link a human adds holds an issue back too.
func (a *autopilot) pick(ctx context.Context) error {
	pending := 0
	waits := map[int64]string{} // why a pending issue cannot start
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		if it.State != aiv1.AutopilotItemPending {
			continue
		}
		pending++
		blocked := false
		for _, d := range a.deps(it.IID) {
			dep := a.item(d)
			if dep == nil || dep.State == aiv1.AutopilotItemMerged {
				continue
			}
			if dep.State == aiv1.AutopilotItemSkipped {
				// Skipped, then finished by hand: the dependency is met.
				mr, done, err := a.doneByHand(ctx, d)
				if err != nil {
					return err
				}
				if done {
					return a.merged(ctx, dep, mr)
				}
				waits[it.IID] = fmt.Sprintf("#%d waits for the skipped #%d", it.IID, d)
			}
			blocked = true
			break
		}
		if blocked {
			continue
		}
		wf := a.spec.Workflow
		is, ok := a.issues[it.IID]
		if !ok || is.State != "opened" {
			// Done before autopilot got to it (merged in the normal flow, or
			// by hand), or given up.
			mr, done, err := a.doneByHand(ctx, it.IID)
			if err != nil {
				return err
			}
			if done {
				return a.merged(ctx, it, mr)
			}
			if err := a.comment(ctx, fmt.Sprintf("⏭️ #%d was closed or deleted before autopilot got to it, so it is skipped.", it.IID)); err != nil {
				return err
			}
			it.State = aiv1.AutopilotItemSkipped
			return nil
		}
		blockers, err := a.blockedByLinks(ctx, it.IID)
		if err != nil {
			return err
		}
		if len(blockers) > 0 {
			var outside []string
			for _, b := range blockers {
				if b.queued {
					continue // the queue gets there
				}
				outside = append(outside, b.ref)
			}
			if len(outside) > 0 {
				waits[it.IID] = fmt.Sprintf("#%d is blocked by %s (issue link)", it.IID, strings.Join(outside, ", "))
			}
			continue
		}
		a.log.Info("autopilot starts the next issue", "issue", it.IID)
		if !hasAny(is, wf.ActiveLabels()...) { // else a human already handed it over
			if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, it.IID, gitlab.UpdateIssueOptions{
				AddLabels: []string{wf.ReadyLabel}, RemoveLabels: []string{wf.DraftLabel, wf.DoneLabel},
			}); err != nil {
				return fmt.Errorf("moving issue #%d to %q: %w", it.IID, wf.ReadyLabel, err)
			}
		}
		it.State = aiv1.AutopilotItemActive
		a.ap.Phase = aiv1.AutopilotRunning // a re-plan can add issues after "done"
		return nil
	}
	if pending > 0 {
		// Everything left waits for something autopilot does not do: a
		// skipped issue, an issue outside the queue, or a cycle of links.
		var reasons []string
		for _, it := range a.ap.Items {
			if it.State != aiv1.AutopilotItemPending {
				continue
			}
			why, ok := waits[it.IID]
			if !ok {
				why = fmt.Sprintf("#%d waits for another pending issue (a cycle of issue links?)", it.IID)
			}
			reasons = append(reasons, why)
		}
		return a.pause(ctx, fmt.Sprintf("nothing can start: %s; merge the issues they wait for by hand (or close them with `%s`), "+
			"remove the links, or `skip` the waiting ones, then `resume`", strings.Join(reasons, ", "), a.spec.Workflow.DoneLabel))
	}
	if a.ap.Phase != aiv1.AutopilotDone {
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
	return nil
}

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

// drive moves the active issue towards its merge.
func (a *autopilot) drive(ctx context.Context, it *aiv1.AutopilotItem) error {
	wf := a.spec.Workflow
	glProject := a.spec.GitLab.Project
	is, ok := a.issues[it.IID]
	task := a.tasks[it.IID]
	if task != nil && task.Status.Run.MergeRequestIID != 0 {
		it.MergeRequestIID, it.MergeRequestURL = task.Status.Run.MergeRequestIID, task.Status.Run.MergeRequestURL
	}
	if task != nil && it.RebasedFrom != "" && task.Spec.Source.MergeBaseFor == it.RebasedFrom {
		// The agent was asked to merge the default branch instead of the
		// rebase (it conflicted); its push is reviewed and checked anew.
		it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	}
	if !ok || is.State != "opened" {
		// Closed: merged by hand, or given up.
		mr, done, err := a.doneByHand(ctx, it.IID)
		if err != nil {
			return err
		}
		if done {
			return a.merged(ctx, it, mr)
		}
		if err := a.comment(ctx, fmt.Sprintf("⏭️ #%d was closed without a merge, so it is skipped.", it.IID)); err != nil {
			return err
		}
		it.State, it.RebasedFrom = aiv1.AutopilotItemSkipped, ""
		return nil
	}
	switch g := a.gateCheck(it, is, task); g.state {
	case gateWaiting:
		return nil // the agent or CI is still working on it
	case gateCIUnfinished:
		// In review, but the CI loop, which only follows issues in
		// working, never finished this commit: hand it back. It always
		// ends in review or needs-input, so this does not repeat.
		a.log.Info("autopilot hands the issue back to the CI loop", "issue", it.IID, "sha", task.Status.Run.PushedSHA)
		if _, err := a.gl.UpdateIssue(ctx, glProject, it.IID, gitlab.UpdateIssueOptions{
			AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel},
		}); err != nil {
			return fmt.Errorf("moving issue #%d back to %q: %w", it.IID, wf.WorkingLabel, err)
		}
		return nil
	case gateReReview:
		return a.reReview(ctx, it, task)
	case gateFailed:
		return a.failGate(ctx, it, is, task, g)
	}
	run := task.Status.Run

	mr, err := a.gl.GetMergeRequest(ctx, glProject, it.MergeRequestIID)
	if err != nil {
		return fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
	}
	base, err := a.base(ctx)
	if err != nil {
		return err
	}
	switch {
	case mr.State == "merged":
		return a.merged(ctx, it, mr)
	case mr.State != "opened":
		return a.pauseMR(ctx, it, fmt.Sprintf("!%d was %s without a merge", mr.IID, mr.State))
	case mr.TargetBranch != "" && mr.TargetBranch != base:
		return a.pauseMR(ctx, it, fmt.Sprintf("!%d targets `%s`, not the default branch `%s`", mr.IID, mr.TargetBranch, base))
	case it.RebasedFrom != "":
		return a.rebased(ctx, it, mr, task)
	case mr.SHA != run.PushedSHA:
		return a.pauseMR(ctx, it, fmt.Sprintf("the head of !%d (%s) is not the commit the agent pushed and CI checked (%s); someone pushed to the branch. "+
			"Comment `%s …` on #%d so the agent takes it over, or merge by hand", mr.IID, short(mr.SHA), short(run.PushedSHA), wf.CommandPrefix, it.IID))
	case !a.spec.CI.Disabled && task.Status.CI != nil && task.Status.CI.State == aiv1.CIStateNone && !it.MarkedReady:
		// No pipeline ran, and projects may skip pipelines for drafts (a run
		// can also mark the MR ready without a push, which starts none): mark
		// it ready and run CI before anything is merged.
		return a.readyForCI(ctx, it, mr, task)
	}
	switch mr.DetailedMergeStatus {
	case "mergeable":
	case "draft_status":
		if it.MarkedReady {
			return a.pauseMR(ctx, it, fmt.Sprintf("!%d was marked draft again; mark it ready, or merge by hand", mr.IID))
		}
		res, err := a.gl.UpdateMergeRequest(ctx, glProject, mr.IID, gitlab.UpdateMergeRequestOptions{Title: gitlab.ReadyTitle(mr.Title)})
		switch {
		case err != nil:
			return a.pauseMR(ctx, it, fmt.Sprintf("could not mark !%d ready: %v", mr.IID, err))
		case res.Draft:
			return a.pauseMR(ctx, it, fmt.Sprintf("could not mark !%d ready (title %q); mark it ready by hand", mr.IID, res.Title))
		}
		it.MarkedReady = true
		a.log.Info("autopilot marked the merge request ready", "mr", mr.IID)
		return nil // merged on the next poll, once GitLab rechecked it
	case "need_rebase":
		return a.rebase(ctx, it, mr, task)
	case "checking", "unchecked", "preparing", "approvals_syncing", "ci_still_running", "ci_must_pass":
		return nil
	case "conflict":
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("!%d has merge conflicts with the default branch", mr.IID))
	case "not_approved":
		return a.pauseMR(ctx, it, fmt.Sprintf("!%d needs an approval before it can be merged", mr.IID))
	case "discussions_not_resolved":
		return a.pauseMR(ctx, it, fmt.Sprintf("!%d has unresolved discussions", mr.IID))
	default:
		return a.pauseMR(ctx, it, fmt.Sprintf("GitLab reports !%d as %q", mr.IID, mr.DetailedMergeStatus))
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
		return a.pauseMR(ctx, it, fmt.Sprintf("the bot may not merge !%d into the default branch (HTTP %d); allow Developers to merge, or give the bot the Maintainer role", mr.IID, code))
	case code == 406:
		return a.rebase(ctx, it, mr, task)
	case code >= 400 && code < 500:
		return a.pauseMR(ctx, it, fmt.Sprintf("GitLab refused to merge !%d: %v", mr.IID, err))
	default:
		return fmt.Errorf("merging !%d: %w", mr.IID, err)
	}
}

// readyForCI marks a draft MR ready, starts a merge request pipeline and hands
// the issue back to the CI loop, which follows that pipeline. If there is
// still none, the gate goes on as before (the MR is not a draft any more).
func (a *autopilot) readyForCI(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask) error {
	glProject := a.spec.GitLab.Project
	wf := a.spec.Workflow
	if mr.Draft || mr.DetailedMergeStatus == "draft_status" {
		res, err := a.gl.UpdateMergeRequest(ctx, glProject, mr.IID, gitlab.UpdateMergeRequestOptions{Title: gitlab.ReadyTitle(mr.Title)})
		switch {
		case err != nil:
			return a.pauseMR(ctx, it, fmt.Sprintf("could not mark !%d ready: %v", mr.IID, err))
		case res.Draft:
			return a.pauseMR(ctx, it, fmt.Sprintf("could not mark !%d ready (title %q); mark it ready by hand", mr.IID, res.Title))
		}
		a.log.Info("autopilot marked the merge request ready to run CI", "mr", mr.IID)
	}
	// Also for an MR that was ready already: a draft after this is a human
	// holding it.
	it.MarkedReady = true
	if err := a.gl.CreateMergeRequestPipeline(ctx, glProject, mr.IID); err != nil {
		if code := gitlab.StatusCode(err); code >= 400 && code < 500 {
			// No merge request pipelines in this project (or no CI at all):
			// nothing to wait for; the gate goes on with the ready MR.
			a.log.Info("no merge request pipeline possible, merging without CI", "mr", mr.IID, "error", err.Error())
			return nil
		}
		return fmt.Errorf("starting a pipeline for !%d: %w", mr.IID, err)
	}
	orig := task.DeepCopy()
	task.Status.CI = nil
	task.Status.Run.PushedAt = &metav1.Time{Time: a.r.now()}
	if err := a.r.Status().Patch(ctx, task, client.MergeFrom(orig)); err != nil {
		return err
	}
	if _, err := a.gl.UpdateIssue(ctx, glProject, it.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d back to %q: %w", it.IID, wf.WorkingLabel, err)
	}
	return nil
}

// glProject returns the GitLab project, read once per poll.
func (a *autopilot) glProject(ctx context.Context) (*gitlab.Project, error) {
	if a.proj == nil {
		proj, err := a.gl.GetProject(ctx, a.spec.GitLab.Project)
		if err != nil {
			return nil, fmt.Errorf("getting project: %w", err)
		}
		a.proj = proj
	}
	return a.proj, nil
}

// base returns the project's default branch.
func (a *autopilot) base(ctx context.Context) (string, error) {
	proj, err := a.glProject(ctx)
	if err != nil {
		return "", err
	}
	return proj.DefaultBranch, nil
}

// pauseActive pauses because of the active issue, unless its MR was merged
// meanwhile (by hand, while the issue is still open).
func (a *autopilot) pauseActive(ctx context.Context, it *aiv1.AutopilotItem, reason string) error {
	if it.MergeRequestIID != 0 && a.ap.Phase != aiv1.AutopilotPaused {
		mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
		if err != nil && !gitlab.IsNotFound(err) {
			return fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
		}
		if err == nil && mr.State == "merged" {
			return a.merged(ctx, it, mr)
		}
	}
	if err := a.pause(ctx, a.about(it, reason)); err != nil {
		return err
	}
	a.ap.PausedFor = it.IID
	return nil
}

// pauseMR pauses because of the active issue's MR (approvals, a push, its
// state): autopilot continues by itself only if it is merged by hand.
func (a *autopilot) pauseMR(ctx context.Context, it *aiv1.AutopilotItem, reason string) error {
	if err := a.pause(ctx, a.about(it, reason)); err != nil {
		return err
	}
	a.ap.PausedFor = it.IID
	return nil
}

// pauseRun pauses because of the active issue's run, review or CI, which a
// later run can fix: then autopilot continues by itself (autoResume).
func (a *autopilot) pauseRun(ctx context.Context, it *aiv1.AutopilotItem, reason string) error {
	if err := a.pauseActive(ctx, it, reason); err != nil {
		return err
	}
	if a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedFor == it.IID {
		a.ap.ResumeWhenReady = true
	}
	return nil
}

// autoResume ends a pause about the active issue that no longer holds: it was
// merged or closed by hand, or (for a pause over its run, review or CI) a
// later run made it pass the gate. If a later run fixed the reason but the
// issue fails the gate for another one, the pause is announced again with
// that reason. A pause of any other kind waits for "resume".
func (a *autopilot) autoResume(ctx context.Context) error {
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor == 0 {
		return nil
	}
	it := a.item(a.ap.PausedFor)
	if it == nil || it.State != aiv1.AutopilotItemActive {
		a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
		return nil
	}
	task := a.tasks[it.IID]
	if task != nil && task.Status.Run.MergeRequestIID != 0 {
		// A run during the pause may have opened another MR.
		it.MergeRequestIID, it.MergeRequestURL = task.Status.Run.MergeRequestIID, task.Status.Run.MergeRequestURL
	}
	resume := func(why string) error {
		if err := a.unblockActive(ctx); err != nil {
			return err
		}
		a.log.Info("autopilot continues by itself", "issue", it.IID, "why", why)
		if err := a.comment(ctx, fmt.Sprintf("▶️ Autopilot continues: %s.", why)); err != nil {
			return err
		}
		a.ap.Phase, a.ap.PausedReason, a.ap.PausedFor, a.ap.ResumeWhenReady = aiv1.AutopilotRunning, "", 0, false
		return nil
	}
	is, ok := a.issues[it.IID]
	if !ok || is.State != "opened" {
		mr, done, err := a.doneByHand(ctx, it.IID)
		if err != nil {
			return err
		}
		if done && mr != nil {
			return resume(fmt.Sprintf("!%d was merged", mr.IID))
		}
		if done {
			return resume(fmt.Sprintf("#%d was closed as done", it.IID))
		}
		return resume(fmt.Sprintf("#%d was closed", it.IID))
	}
	if it.MergeRequestIID != 0 {
		mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
		switch {
		case err != nil && !gitlab.IsNotFound(err):
			// Tried again on the next poll; the tracking issue still updates.
			a.log.Info("could not check the paused issue's merge request", "mr", it.MergeRequestIID, "error", err.Error())
			return nil
		case err == nil && mr.State == "merged":
			if err := resume(fmt.Sprintf("!%d was merged", mr.IID)); err != nil {
				return err
			}
			return a.merged(ctx, it, mr)
		}
	}
	if !a.ap.ResumeWhenReady {
		return nil
	}
	switch g := a.gateCheck(it, is, task); {
	case g.state == gatePassed:
		return resume(fmt.Sprintf("#%d is reviewed and green again", it.IID))
	case g.state == gateReReview:
		return a.reReview(ctx, it, task)
	case g.state == gateFailed && a.about(it, g.reason) != a.ap.PausedReason:
		// A later run fixed the old reason, but something else holds the
		// issue now: say so (and mention the owners), instead of keeping
		// the stale reason.
		a.log.Info("autopilot stays paused for another reason", "issue", it.IID, "was", a.ap.PausedReason, "now", g.reason)
		return a.failGate(ctx, it, is, task, g)
	}
	return nil
}

// gateState is where the active issue stands at the merge gate's run,
// review and CI checks (the MR itself is checked when the gate runs).
type gateState int

const (
	// gateWaiting: the agent or CI is still working on it.
	gateWaiting gateState = iota
	// gateCIUnfinished: in review, but the CI loop never finished the
	// pushed commit.
	gateCIUnfinished
	// gateFailed: it cannot be merged; reason says why.
	gateFailed
	// gateReReview: its reviewers failed transiently; reviewed again
	// after review.retryAfter.
	gateReReview
	// gatePassed: reviewed and green.
	gatePassed
)

type gateResult struct {
	state  gateState
	reason string
	// byRun: a later run can fix it (pauseRun), else a human must
	// (pauseActive).
	byRun bool
	// block: the pause also blocks the issue, for no human's fault.
	block aiv1.BlockReason
}

// gateCheck checks the active issue's run, review and CI against the merge
// gate. drive pauses on a failure; while paused, autoResume continues on a
// pass and announces a failure for another reason.
func (a *autopilot) gateCheck(it *aiv1.AutopilotItem, is gitlab.Issue, task *aiv1.AgentTask) gateResult {
	wf := a.spec.Workflow
	failed := func(byRun bool, format string, args ...any) gateResult {
		return gateResult{state: gateFailed, reason: fmt.Sprintf(format, args...), byRun: byRun}
	}
	switch {
	case is.HasLabel(wf.NeedsInputLabel):
		return failed(true, "#%d needs input (see its last comments): CI is still red, the run failed or the agent asked a question", it.IID)
	case !hasAny(is, wf.ActiveLabels()...):
		return failed(false, "#%d is open but has no workflow label any more; move it back to `%s` or close it", it.IID, wf.ReviewLabel)
	case !is.HasLabel(wf.ReviewLabel) || task == nil || task.RunPending() || task.Status.Blocked != nil:
		return gateResult{state: gateWaiting} // blocked: waits for an outside reason
	}
	run := task.Status.Run
	switch {
	case run.LastResult != aiv1.RunResultSucceeded:
		return failed(true, "the last run of #%d ended with %q", it.IID, run.LastResult)
	case run.BlockingFindings > 0:
		return failed(true, "%d blocking review findings remain on !%d", run.BlockingFindings, it.MergeRequestIID)
	case run.ReviewErrors > 0:
		return a.reviewErrors(it, task)
	case it.MergeRequestIID == 0 || run.PushedSHA == "":
		return failed(false, "#%d is in review without a pushed merge request", it.IID)
	case len(a.spec.Agents.Review.Reviewers) == 0:
		return failed(false, "no reviewers are configured (`agents.review.reviewers`), and autopilot only merges reviewed changes")
	case run.ReviewedSHA != run.PushedSHA:
		return failed(true, "the head of !%d (%s) was not reviewed (the last review saw %s)", it.MergeRequestIID, short(run.PushedSHA), short(run.ReviewedSHA))
	}
	if a.spec.CI.Disabled {
		return gateResult{state: gatePassed}
	}
	switch ci := task.Status.CI; {
	case ci == nil || ci.SHA != run.PushedSHA || !ci.State.Final():
		return gateResult{state: gateCIUnfinished}
	case ci.State == aiv1.CIStateCanceled:
		return failed(true, "the pipeline of !%d was canceled", it.MergeRequestIID)
	case ci.State == aiv1.CIStateBaseFailing:
		return failed(true, "CI of !%d is red with jobs that also fail on the default branch (%s)", it.MergeRequestIID, jobList(ci.FailedJobs))
	case ci.State == aiv1.CIStateFailed:
		return failed(true, "CI of !%d is red (%s)", it.MergeRequestIID, jobList(ci.FailedJobs))
	}
	return gateResult{state: gatePassed}
}

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
	return nil
}

// rebase asks GitLab to rebase the MR, once per item. The reviewed change is
// hashed first, so the rebased head can be checked against it. If a rebase
// cannot help, the agent merges the default branch instead.
func (a *autopilot) rebase(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask) error {
	if it.Rebases > 0 {
		return a.mergeBase(ctx, it, mr, task, fmt.Sprintf("!%d still cannot be merged after a rebase", mr.IID))
	}
	diff, err := a.diffHash(ctx, mr.IID)
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
		if err := a.pauseMR(ctx, it, reason); err != nil {
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
	diff, err := a.diffHash(ctx, mr.IID)
	if err != nil {
		return err
	}
	if diff == "" || diff != it.RebasedDiff {
		return unrebased(fmt.Sprintf("after the rebase, the change of !%d is not the one that was reviewed (someone pushed, or the rebase changed lines). "+
			"Comment `%s …` on #%d so the agent takes it over, or merge by hand", mr.IID, a.spec.Workflow.CommandPrefix, it.IID))
	}
	a.log.Info("autopilot rebase done, waiting for CI", "mr", mr.IID, "sha", mr.SHA)
	orig := task.DeepCopy()
	task.Status.Run.ReviewedSHA = mr.SHA
	task.Status.Run.PushedSHA = mr.SHA
	task.Status.Run.PushedAt = &metav1.Time{Time: a.r.now()}
	task.Status.CI = nil
	if err := a.r.Status().Patch(ctx, task, client.MergeFrom(orig)); err != nil {
		return err
	}
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	if a.spec.CI.Disabled {
		return nil // merged on the next poll
	}
	// Back to working, so the CI loop follows the new pipeline. If this
	// fails, the gate hands the unchecked head back on the next poll.
	wf := a.spec.Workflow
	if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, it.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d back to %q: %w", it.IID, wf.WorkingLabel, err)
	}
	return nil
}

// diffHash hashes the change of an MR: the files and their added and removed
// lines, without positions, so a rebase that only moves the change keeps the
// hash. It is empty if GitLab does not return the whole change.
func (a *autopilot) diffHash(ctx context.Context, iid int64) (string, error) {
	diffs, err := a.gl.ListMergeRequestDiffs(ctx, a.spec.GitLab.Project, iid)
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
	it.State = aiv1.AutopilotItemMerged
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	it.Decisions = collectDecisions(is, notes, a.project.Status.BotUserID)
	if mr != nil {
		it.MergedSHA = firstNonEmpty(mr.MergeCommitSHA, mr.SquashCommitSHA, mr.SHA)
		it.MergeRequestIID, it.MergeRequestURL = mr.IID, firstNonEmpty(mr.WebURL, it.MergeRequestURL)
	}
	a.log.Info("autopilot merged", "issue", it.IID, "mr", it.MergeRequestIID, "sha", it.MergedSHA)
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
	// Comment first: if it fails, the pause is tried again on the next poll.
	if err := a.comment(ctx, fmt.Sprintf("⏸️ Autopilot paused: %s.\n\nFix it, then comment `%s resume`. A pause over the active issue also ends by itself once a later run fixed it or the MR is merged by hand.", reason, a.spec.Workflow.CommandPrefix)+
		a.mention(aiv1.NotifyAutopilotPaused)); err != nil {
		return err
	}
	a.ap.Phase = aiv1.AutopilotPaused
	a.ap.PausedReason = reason
	a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
	return nil
}

// commands handles "/agent pause|resume|skip #N" on the tracking issue and
// answers any other command once.
func (a *autopilot) commands(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	notes, err := a.gl.ListIssueNotes(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID)
	if a.trackingGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing notes of the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	a.pilot(ctx, notes)
	prefix := a.spec.Workflow.CommandPrefix
	for _, n := range notes {
		if n.ID <= a.ap.LastCommandNoteID || !IsCommand(n, a.project.Status.BotUserID, prefix) {
			continue
		}
		rest := strings.TrimPrefix(strings.TrimSpace(n.Body), prefix)
		// Padded, so a bare command and a bare "skip" have their fields.
		fields := append(strings.Fields(strings.TrimLeft(rest, ": \t")), "", "")
		word := strings.ToLower(strings.TrimSuffix(fields[0], ":"))
		if word == "revisit" {
			// syncRevisits handles (and answers) the well-formed ones.
			if m := marker.Revisit.FindStringSubmatch(n.Body); m != nil && m[1] == prefix {
				a.ap.LastCommandNoteID = n.ID
				continue
			}
			word = ""
		}
		// Tried again on the next poll when it fails.
		if err := a.command(ctx, word, fields[1], "@"+n.Author.Username); err != nil {
			return err
		}
		a.ap.LastCommandNoteID = n.ID
	}
	// The same commands from proposals an owner accepted; revisits are
	// syncRevisits'.
	if ps := a.st.Status.Pilot; ps != nil {
		for _, c := range ps.Commands {
			if c.Seq <= a.ap.LastPilotCommand {
				continue
			}
			if c.Action.Tool != "revisit" {
				if err := a.command(ctx, c.Action.Tool, fmt.Sprintf("#%d", c.Action.IssueIID), "@"+c.Owner+" via the pilot"); err != nil {
					return err
				}
			}
			a.ap.LastPilotCommand = c.Seq
		}
	}
	return nil
}

// command applies one command of the tracking issue: pause, resume or
// skip arg, by someone; it answers anything else.
func (a *autopilot) command(ctx context.Context, word, arg, by string) error {
	switch word {
	case "pause":
		if a.ap.Phase != aiv1.AutopilotDone {
			a.ap.Phase, a.ap.PausedReason = aiv1.AutopilotPaused, "paused by "+by
			a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
		}
	case "resume":
		if a.ap.Phase == aiv1.AutopilotPaused {
			if err := a.unblockActive(ctx); err != nil {
				return err
			}
			a.ap.Phase, a.ap.PausedReason = aiv1.AutopilotRunning, ""
			a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
		}
	case "skip":
		iid, _ := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
		it := a.item(iid)
		if it == nil || it.State == aiv1.AutopilotItemMerged {
			return a.comment(ctx, fmt.Sprintf("`skip` needs a planned issue that is not merged yet, e.g. `%s skip #12`.", a.spec.Workflow.CommandPrefix))
		}
		it.State, it.RebasedFrom = aiv1.AutopilotItemSkipped, ""
	default:
		return a.comment(ctx, a.unknownCommand())
	}
	return nil
}

// unknownCommand answers a command the tracking issue does not understand,
// without quoting it: instructions for the agent go on the active issue.
func (a *autopilot) unknownCommand() string {
	prefix := a.spec.Workflow.CommandPrefix
	where := "the issue it should work on"
	for _, it := range a.ap.Items {
		if it.State == aiv1.AutopilotItemActive {
			where = fmt.Sprintf("#%d, the active issue", it.IID)
		}
	}
	return fmt.Sprintf("This tracking issue only understands `%[1]s pause`, `%[1]s resume`, `%[1]s skip #N` and `%[1]s revisit <id> …`. "+
		"Did you mean `%[3]s …`? The milestone's pilot answers here. To instruct the agent, comment `%[1]s …` on %[2]s.", prefix, where, a.spec.Workflow.PilotPrefix)
}

// ensureTracking creates the tracking issue on the first poll.
func (a *autopilot) ensureTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID != 0 {
		is, ok := a.issues[a.ap.TrackingIssueIID]
		if !ok {
			// Not in the milestone any more: moved, or deleted.
			got, err := a.gl.GetIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID)
			if a.trackingGone(err) {
				return a.ensureTracking(ctx)
			}
			if err != nil {
				return fmt.Errorf("getting the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
			}
			is = *got
		}
		if is.State != "opened" && a.ap.Phase != aiv1.AutopilotDone {
			// Re-planned or switched back on after it was closed.
			_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{StateEvent: "reopen"})
			if a.trackingGone(err) {
				return a.ensureTracking(ctx)
			}
			if err != nil {
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
		_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, gitlab.UpdateIssueOptions{Description: body})
		if a.trackingGone(err) {
			return nil
		}
		if err != nil {
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
	_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, gitlab.UpdateIssueOptions{StateEvent: "close"})
	if a.trackingGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("closing the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	return nil
}

// mention returns the line that mentions the milestone's owners, if the
// event mentions them.
func (a *autopilot) mention(e aiv1.NotifyEvent) string {
	if !a.spec.Notify.Wants(e) {
		return ""
	}
	return notify.Mention(a.st.Spec.Owners)
}

func (a *autopilot) comment(ctx context.Context, body string) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	_, err := a.gl.CreateIssueNote(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, "🤖 "+body)
	if a.trackingGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("commenting on the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	return nil
}

// trackingGone reports whether err says the tracking issue no longer exists.
// It is then forgotten, and a new one is created on the next poll.
func (a *autopilot) trackingGone(err error) bool {
	if a.ap.TrackingIssueIID == 0 || !gitlab.IsNotFound(err) {
		return false
	}
	a.log.Info("autopilot tracking issue is gone, a new one is created", "issue", a.ap.TrackingIssueIID)
	a.ap.TrackingIssueIID, a.ap.TrackingHash = 0, ""
	return true
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
	b.WriteString(rulesSection(a.st))
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

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// syncCI drives the CI feedback loop of an implement task. After the agent
// pushed, the issue stays in "working" while the operator follows the
// pipeline of the pushed commit:
//
//	green / skipped / manual        -> review
//	red, not retried yet            -> retry the failed jobs once (flaky jobs)
//	red, same jobs red on base      -> review, with a note (not this change's fault)
//	red, fix rounds left            -> request a CI fix run with the failed pipeline
//	red, no rounds left             -> needs-input, with the failing jobs
//	canceled                        -> review, with a note
//	no pipeline after a timeout     -> review (no CI for this commit)
//
// With a pushed companion (status.run.companion) the loop follows the
// companion's pipeline of its pushed head too, by the same rules except the
// default-branch check, and ends once both trees are done. Both share the
// fix rounds (status.run.ciRound); a red home pipeline is fixed first, then
// the companion's (spec.source.ciFixProject names the tree).
//
// Pipelines can start after the loop is done: in projects that skip
// pipelines for drafts, one started after the MR was marked ready; and people
// run and retry them. So for an issue in review the MR's head pipeline is
// watched too, see syncReviewCI.
func (r *AgentProjectReconciler) syncCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, mh *aiv1.MainHealthStatus, is gitlab.Issue, task *aiv1.AgentTask, mr *gitlab.MergeRequest) error {
	run := task.Status.Run
	if spec.CI.Disabled || run.PushedSHA == "" || task.RunPending() {
		return nil
	}
	// The MR's head pipeline, if the MR's head is the pushed commit.
	var head *gitlab.Pipeline
	if mr != nil && mr.State == "opened" && mr.SHA == run.PushedSHA {
		head = mr.HeadPipeline
	}
	if is.HasLabel(spec.Workflow.ReviewLabel) {
		return r.syncReviewCI(ctx, gl, spec, is, task, head)
	}
	if !is.HasLabel(spec.Workflow.WorkingLabel) {
		return nil
	}
	ci := r.currentCI(task)
	if ci.Done() {
		return r.patchCI(ctx, task, ci) // drops the status of a companion that is gone
	}
	// The states before this poll: finishCI keeps them while it pings.
	open := ci.DeepCopy()
	cc := ci.Companion

	// A fix run was requested and is done, but it pushed nothing new.
	if ci.State == aiv1.CIStateFixing {
		ci.State = aiv1.CIStateFailed
		return r.finishCI(ctx, gl, spec, is, task, mr, open, ci,
			fmt.Sprintf("🤖 The agent could not fix the failing pipeline (%s): failing jobs %s. Comment `%s …` with guidance or fix it by hand.",
				ci.PipelineURL, jobList(ci.FailedJobs), spec.Workflow.CommandPrefix))
	}
	if cc != nil && cc.State == aiv1.CIStateFixing {
		cc.State = aiv1.CIStateFailed
		return r.finishCI(ctx, gl, spec, is, task, mr, open, ci,
			fmt.Sprintf("🤖 The agent could not fix the failing pipeline of the companion repository %s (%s): failing jobs %s. Comment `%s …` with guidance or fix it by hand.",
				run.Companion.Project, cc.PipelineURL, jobList(cc.FailedJobs), spec.Workflow.CommandPrefix))
	}

	homeRed, homeNote, err := r.followHomeCI(ctx, gl, spec, mh, is, ci, head, run)
	if err != nil {
		return err
	}
	compRed, compNote, err := r.followCompanionCI(ctx, gl, spec, run.Companion, cc)
	if err != nil {
		return err
	}
	homeFinal, compFinal := ci.State.Final(), cc == nil || cc.State.Final()
	// A final state with a note stays open until the other tree is done too,
	// so the note goes out once, with the loop's end.
	keepOpen := func(state *aiv1.CIState, note *string) {
		if *note != "" {
			*state, *note = aiv1.CIStateRunning, ""
		}
	}
	switch {
	case homeRed:
		return r.ciRed(ctx, gl, spec, is, task, mr, open, ci, "")
	case !homeFinal:
		if cc != nil {
			keepOpen(&cc.State, &compNote)
		}
		return r.patchCI(ctx, task, ci)
	case compRed:
		// Home first: its pipeline is done, now the companion's is fixed.
		keepOpen(&ci.State, &homeNote)
		return r.ciRed(ctx, gl, spec, is, task, mr, open, ci, run.Companion.Project)
	case !compFinal:
		keepOpen(&ci.State, &homeNote)
		return r.patchCI(ctx, task, ci)
	}
	var notes []string
	for _, n := range []string{homeNote, compNote} {
		if n != "" {
			notes = append(notes, n)
		}
	}
	return r.finishCI(ctx, gl, spec, is, task, mr, open, ci, strings.Join(notes, "\n\n"))
}

// currentCI is the task's CI status for its pushed heads: the recorded one
// for the pushed home head, else a new one, with the companion's for its
// pushed head (companionCI). A new head in one tree reopens a Fixing or
// Failed state of the other: its pipeline is checked again, with the fix
// rounds the new push left.
func (r *AgentProjectReconciler) currentCI(task *aiv1.AgentTask) *aiv1.CIStatus {
	run := task.Status.Run
	ci := &aiv1.CIStatus{SHA: run.PushedSHA, State: aiv1.CIStatePending}
	homeNew := true
	if cur := task.Status.CI; cur != nil && cur.SHA == run.PushedSHA {
		ci, homeNew = cur.DeepCopy(), false
	}
	var compNew bool
	ci.Companion, compNew = r.companionCI(task)
	reopen := func(s *aiv1.CIState) {
		if *s == aiv1.CIStateFixing || *s == aiv1.CIStateFailed {
			*s = aiv1.CIStateRunning
		}
	}
	if compNew && !homeNew {
		reopen(&ci.State)
	}
	if homeNew && !compNew && ci.Companion != nil {
		reopen(&ci.Companion.State)
	}
	return ci
}

// companionCI is the CI status of the companion's pushed head: the
// recorded one, or a new one (fresh); nil without a pushed companion head.
func (r *AgentProjectReconciler) companionCI(task *aiv1.AgentTask) (cc *aiv1.CompanionCI, fresh bool) {
	c := task.Status.Run.Companion
	if c == nil || c.PushedSHA == "" {
		return nil, false
	}
	if cur := task.Status.CI; cur != nil && cur.Companion != nil && cur.Companion.SHA == c.PushedSHA {
		return cur.Companion.DeepCopy(), false
	}
	return &aiv1.CompanionCI{SHA: c.PushedSHA, State: aiv1.CIStatePending, Since: &metav1.Time{Time: r.now()}}, true
}

// followHomeCI follows the home pipeline of ci.SHA one step, unless its
// state is final: ci gets the new state. red reports a red pipeline the
// agent is to fix (its state stays Running); note goes with a final state.
func (r *AgentProjectReconciler) followHomeCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, mh *aiv1.MainHealthStatus, is gitlab.Issue,
	ci *aiv1.CIStatus, head *gitlab.Pipeline, run aiv1.RunStatus) (red bool, note string, err error) {
	if ci.State.Final() {
		return false, "", nil
	}
	glProject := spec.GitLab.Project
	// The MR's head pipeline is the one GitLab shows and gates merging on; it
	// may also be a merged-results pipeline, whose SHA is another one. Only
	// without it the newest pipeline of the commit counts.
	var pipelines []gitlab.Pipeline
	if head != nil {
		pipelines = []gitlab.Pipeline{*head}
	} else if pipelines, err = gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{SHA: ci.SHA, Limit: 1}); err != nil {
		return false, "", fmt.Errorf("listing pipelines of %s: %w", ci.SHA, err)
	}
	// A skipped pipeline is a push with ci.skip (the runner pushes steps of
	// a draft that way): it checked nothing, so it is no pipeline.
	pipelines = slices.DeleteFunc(pipelines, func(p gitlab.Pipeline) bool { return p.Status == "skipped" })
	if len(pipelines) == 0 {
		pushedAt := run.PushedAt
		if pushedAt == nil {
			pushedAt = run.LastActivityTime
		}
		if pushedAt != nil && r.now().Sub(pushedAt.Time) >= spec.CI.NoPipelineTimeout.Duration {
			ci.State = aiv1.CIStateNone
		}
		return false, "", nil
	}
	p := pipelines[0]
	ci.PipelineID, ci.PipelineURL = p.ID, p.WebURL
	jobs, state, err := r.redPipeline(ctx, gl, spec, glProject, p, &ci.Retried)
	if err != nil {
		return false, "", err
	}
	if p.Status == "failed" {
		ci.FailedJobs = jobs
	}
	if state != "" {
		ci.State = state
		if state == aiv1.CIStateCanceled {
			note = fmt.Sprintf("🤖 The pipeline %s was canceled, so CI could not confirm this change. Moving the issue to review.", p.WebURL)
		}
		return false, note, nil
	}
	if failing, mainRed, err := r.baseFailing(ctx, gl, spec, mh, ci.FailedJobs); err != nil {
		return false, "", err
	} else if failing && (mh == nil || mh.IssueIID != is.IID) { // the main-red issue's change must fix them
		see := ""
		if mainRed != 0 {
			see = fmt.Sprintf(" (#%d)", mainRed)
		}
		ci.State = aiv1.CIStateBaseFailing
		return false, fmt.Sprintf("🤖 CI is red (%s), but the failing jobs %s also fail on the default branch%s, so this change is not the cause. Moving the issue to review.",
			p.WebURL, jobList(ci.FailedJobs), see), nil
	}
	ci.State = aiv1.CIStateRunning
	return true, "", nil
}

// followCompanionCI follows the companion's pipeline of cc.SHA one step,
// like followHomeCI without the default-branch check. The companion merge
// request's head pipeline counts if its head is cc.SHA, else the newest
// pipeline of the commit.
func (r *AgentProjectReconciler) followCompanionCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, c *aiv1.CompanionRun, cc *aiv1.CompanionCI) (red bool, note string, err error) {
	if cc == nil || cc.State.Final() {
		return false, "", nil
	}
	if cc.Since == nil {
		cc.Since = &metav1.Time{Time: r.now()}
	}
	var pipelines []gitlab.Pipeline
	if c.MRIID != 0 {
		cmr, err := gl.GetMergeRequest(ctx, c.Project, c.MRIID)
		if err != nil && !gitlab.IsNotFound(err) {
			return false, "", fmt.Errorf("getting the companion merge request %s!%d: %w", c.Project, c.MRIID, err)
		}
		if err == nil && cmr.State == "opened" && cmr.SHA == cc.SHA && cmr.HeadPipeline != nil {
			pipelines = []gitlab.Pipeline{*cmr.HeadPipeline}
		}
	}
	if pipelines == nil {
		if pipelines, err = gl.ListPipelines(ctx, c.Project, gitlab.ListPipelinesOptions{SHA: cc.SHA, Limit: 1}); err != nil {
			return false, "", fmt.Errorf("listing pipelines of %s in %s: %w", cc.SHA, c.Project, err)
		}
	}
	pipelines = slices.DeleteFunc(pipelines, func(p gitlab.Pipeline) bool { return p.Status == "skipped" })
	if len(pipelines) == 0 {
		if r.now().Sub(cc.Since.Time) >= spec.CI.NoPipelineTimeout.Duration {
			cc.State = aiv1.CIStateNone
		}
		return false, "", nil
	}
	p := pipelines[0]
	cc.PipelineID, cc.PipelineURL = p.ID, p.WebURL
	jobs, state, err := r.redPipeline(ctx, gl, spec, c.Project, p, &cc.Retried)
	if err != nil {
		return false, "", err
	}
	if p.Status == "failed" {
		cc.FailedJobs = jobs
	}
	if state != "" {
		cc.State = state
		if state == aiv1.CIStateCanceled {
			note = fmt.Sprintf("🤖 The pipeline %s of the companion repository %s was canceled, so CI could not confirm its change.", p.WebURL, c.Project)
		}
		return false, note, nil
	}
	cc.State = aiv1.CIStateRunning
	return true, "", nil
}

// redPipeline handles the parts of a pipeline both trees share. A state
// says where the pipeline stands unless it is red for good: Success
// (green, manual), Canceled, or Running (still running, or its failed jobs
// were retried just now, once per head as *retried records). With no
// state, the pipeline is red; jobs are its failed jobs that fail it.
func (r *AgentProjectReconciler) redPipeline(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, project string, p gitlab.Pipeline, retried *bool) (jobs []string, state aiv1.CIState, err error) {
	switch p.Status {
	case "success", "manual":
		return nil, aiv1.CIStateSuccess, nil
	case "canceled":
		return nil, aiv1.CIStateCanceled, nil
	case "failed":
	default:
		return nil, aiv1.CIStateRunning, nil
	}
	failed, err := gl.ListPipelineJobs(ctx, project, p.ID, "failed")
	if err != nil {
		return nil, "", fmt.Errorf("listing failed jobs of pipeline %d: %w", p.ID, err)
	}
	for _, j := range failed {
		if !j.AllowFailure {
			jobs = append(jobs, j.Name)
		}
	}
	if *spec.CI.RetryFailedJobs && !*retried {
		log.FromContext(ctx).Info("pipeline failed, retrying failed jobs once", "project", project, "pipeline", p.ID, "jobs", jobs)
		if _, err := gl.RetryPipeline(ctx, project, p.ID); err != nil {
			return nil, "", fmt.Errorf("retrying pipeline %d: %w", p.ID, err)
		}
		*retried = true
		return jobs, aiv1.CIStateRunning, nil
	}
	return jobs, "", nil
}

// ciRed asks the agent to fix a red pipeline, of the home project (project
// "") or the companion's, if fix rounds are left; else the loop ends
// failed.
func (r *AgentProjectReconciler) ciRed(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask, mr *gitlab.MergeRequest,
	open, ci *aiv1.CIStatus, project string) error {
	run := task.Status.Run
	state, pipelineID, url, jobs, of := &ci.State, ci.PipelineID, ci.PipelineURL, ci.FailedJobs, ""
	if project != "" {
		cc := ci.Companion
		state, pipelineID, url, jobs, of = &cc.State, cc.PipelineID, cc.PipelineURL, cc.FailedJobs, " of the companion repository "+project
	}
	if run.CIRound >= *spec.CI.MaxFixRounds {
		*state = aiv1.CIStateFailed
		return r.finishCI(ctx, gl, spec, is, task, mr, open, ci,
			fmt.Sprintf("🤖 CI%s is still red after %d fix attempts (%s): failing jobs %s. Comment `%s …` with guidance or fix it by hand.",
				of, run.CIRound, url, jobList(jobs), spec.Workflow.CommandPrefix))
	}
	log.FromContext(ctx).Info("pipeline failed, asking the agent to fix it", "task", task.Name, "project", cmpProject(project, spec.GitLab.Project),
		"pipeline", pipelineID, "round", run.CIRound+1, "jobs", jobs)
	task.Spec.RunRequest++
	task.Spec.Source.CIFixPipelineID = pipelineID
	task.Spec.Source.CIFixProject = project
	r.setChatContext(ctx, spec, task)
	if err := r.Update(ctx, task); err != nil {
		return err
	}
	*state = aiv1.CIStateFixing
	return r.patchCI(ctx, task, ci)
}

func cmpProject(project, home string) string {
	if project == "" {
		return home
	}
	return project
}

// syncReviewCI watches the CI of an issue in review. A loop that is not done
// (a label move that failed, a CI fix run that pushed nothing and so ended in
// review, a companion head the loop has not followed) goes back to working,
// where it finishes. A new head pipeline of the pushed commit, or a canceled
// or base-failing one that was retried, is followed: green is recorded as
// success, canceled only recorded; running or red resets the loop and sends
// the issue back to working, so the pipeline is retried and the agent asked
// to fix it. A canceled companion pipeline, or none, is watched the same way.
func (r *AgentProjectReconciler) syncReviewCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask, head *gitlab.Pipeline) error {
	if task.Status.CI == nil || task.Status.CI.SHA != task.Status.Run.PushedSHA {
		return nil
	}
	cur := task.Status.CI.DeepCopy()
	cur.Companion, _ = r.companionCI(task)
	if !cur.Done() {
		return r.backToWorking(ctx, gl, spec, is)
	}
	if head == nil || head.Status == "skipped" || !pipelineChanged(cur.PipelineID, cur.State, head) {
		return r.syncReviewCompanionCI(ctx, gl, spec, is, task, cur)
	}
	ci := cur.DeepCopy()
	ci.PipelineID, ci.PipelineURL = head.ID, head.WebURL
	switch head.Status {
	case "success", "manual":
		ci.State, ci.FailedJobs = aiv1.CIStateSuccess, nil
		return r.patchCI(ctx, task, ci)
	case "canceled":
		if ci.State != aiv1.CIStateSuccess {
			ci.State = aiv1.CIStateCanceled
		}
		return r.patchCI(ctx, task, ci)
	}
	log.FromContext(ctx).Info("new pipeline for the pushed commit, following it", "task", task.Name, "pipeline", head.ID, "status", head.Status)
	// The status first: if the label move fails, the next poll retries it.
	if err := r.patchCI(ctx, task, &aiv1.CIStatus{SHA: cur.SHA, State: aiv1.CIStatePending, PipelineID: head.ID, PipelineURL: head.WebURL,
		PingedFor: cur.PingedFor, Companion: cur.Companion}); err != nil {
		return err
	}
	return r.backToWorking(ctx, gl, spec, is)
}

// syncReviewCompanionCI follows a companion pipeline of an issue in review
// whose loop ended canceled or without one: a new head pipeline of the
// companion's pushed head (a retry, one started by hand) is recorded when
// green and otherwise sends the issue back to working.
func (r *AgentProjectReconciler) syncReviewCompanionCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask, cur *aiv1.CIStatus) error {
	c, cc := task.Status.Run.Companion, cur.Companion
	if cc == nil || c.MRIID == 0 || (cc.State != aiv1.CIStateCanceled && cc.State != aiv1.CIStateNone) {
		return nil
	}
	cmr, err := gl.GetMergeRequest(ctx, c.Project, c.MRIID)
	switch {
	case gitlab.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("getting the companion merge request %s!%d: %w", c.Project, c.MRIID, err)
	}
	p := cmr.HeadPipeline
	if cmr.State != "opened" || cmr.SHA != cc.SHA || p == nil || p.Status == "skipped" || !pipelineChanged(cc.PipelineID, cc.State, p) {
		return nil
	}
	ci := cur.DeepCopy()
	ci.Companion.PipelineID, ci.Companion.PipelineURL = p.ID, p.WebURL
	switch p.Status {
	case "success", "manual":
		ci.Companion.State, ci.Companion.FailedJobs = aiv1.CIStateSuccess, nil
		return r.patchCI(ctx, task, ci)
	case "canceled":
		ci.Companion.State = aiv1.CIStateCanceled
		return r.patchCI(ctx, task, ci)
	}
	log.FromContext(ctx).Info("new pipeline for the companion's pushed commit, following it", "task", task.Name, "project", c.Project, "pipeline", p.ID, "status", p.Status)
	ci.Companion = &aiv1.CompanionCI{SHA: cc.SHA, State: aiv1.CIStatePending, PipelineID: p.ID, PipelineURL: p.WebURL, Since: &metav1.Time{Time: r.now()}}
	if err := r.patchCI(ctx, task, ci); err != nil {
		return err
	}
	return r.backToWorking(ctx, gl, spec, is)
}

// pipelineChanged reports whether the head pipeline is not the one the CI
// loop ended with (pipeline id, in state): another pipeline, or the same
// one retried since it was canceled or failed with the default branch.
func pipelineChanged(id int64, state aiv1.CIState, head *gitlab.Pipeline) bool {
	switch {
	case head.ID != id:
		return true
	case state == aiv1.CIStateCanceled:
		return head.Status != "canceled"
	case state == aiv1.CIStateBaseFailing:
		return head.Status != "failed"
	}
	return false
}

func (r *AgentProjectReconciler) backToWorking(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue) error {
	wf := spec.Workflow
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d to %q: %w", is.IID, wf.WorkingLabel, err)
	}
	return nil
}

// baseFailing reports whether every failing job also fails on the default
// branch, and the issue that reports it red. Main health checked within one
// interval answers; without it, the latest finished pipeline is listed.
func (r *AgentProjectReconciler) baseFailing(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, mh *aiv1.MainHealthStatus, jobs []string) (bool, int64, error) {
	if len(jobs) == 0 {
		return false, 0, nil
	}
	if mh = r.freshMainHealth(spec, mh, 1); mh != nil {
		red := mh.State == aiv1.MainHealthRed || mh.State == aiv1.MainHealthRetrying
		// Main health keeps the job names as jobName cleans them.
		return red && !slices.ContainsFunc(jobs, func(j string) bool { return !slices.Contains(mh.FailedJobs, jobName(j)) }), mh.IssueIID, nil
	}
	glProject := spec.GitLab.Project
	proj, err := gl.GetProject(ctx, glProject)
	if err != nil {
		return false, 0, fmt.Errorf("getting project: %w", err)
	}
	ps, err := gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{Ref: proj.DefaultBranch, Scope: "finished", Limit: 1})
	if err != nil || len(ps) == 0 || ps[0].Status != "failed" {
		return false, 0, err
	}
	baseJobs, err := gl.ListPipelineJobs(ctx, glProject, ps[0].ID, "failed")
	if err != nil {
		return false, 0, err
	}
	onBase := map[string]bool{}
	for _, j := range baseJobs {
		onBase[j.Name] = true
	}
	for _, j := range jobs {
		if !onBase[j] {
			return false, 0, nil
		}
	}
	return true, 0, nil
}

// finishCI ends the loop for the pushed heads, with the states ci holds
// now (open: before this poll): success, no CI, canceled and base failures
// go to review; Failed (of either tree) goes to needs-input. mr is the
// task's merge request, if known: while its head is a checkpoint, nobody is
// asked to review.
func (r *AgentProjectReconciler) finishCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask, mr *gitlab.MergeRequest, open, ci *aiv1.CIStatus, note string) error {
	wf := spec.Workflow
	failed := ci.State == aiv1.CIStateFailed || ci.Companion != nil && ci.Companion.State == aiv1.CIStateFailed
	next := wf.ReviewLabel
	if failed {
		next = wf.NeedsInputLabel
	}
	logger := log.FromContext(ctx)
	kv := []any{"task", task.Name, "state", ci.State, "label", next}
	if ci.Companion != nil {
		kv = append(kv, "companionState", ci.Companion.State)
	}
	logger.Info("CI loop finished", kv...)
	// Autopilot merges what reaches review; nobody needs to look. Nor at a
	// checkpoint: the run it continues in asks once it pushed.
	checkpoint := mr != nil && task.Status.Run.CheckpointHead(mr.SHA)
	review := next == wf.ReviewLabel && !task.Spec.Autopilot && !checkpoint
	var event aiv1.NotifyEvent
	switch {
	case task.Spec.Autopilot:
		// Autopilot pauses over it and mentions the owners there.
	case failed:
		event = aiv1.NotifyCIFailed
	case review:
		event = aiv1.NotifyReview
	}
	mention := ""
	if event != "" && ci.PingedFor != event && spec.Notify.Wants(event) {
		mention = notify.Mention(task.Spec.Owners)
	}
	// Recorded before the comments, with the states still open: a failure
	// after them repeats the loop's end on the next poll, but not the ping.
	if mention != "" {
		ci.PingedFor = event
		pinged := ci.DeepCopy()
		pinged.State = open.State
		if pinged.Companion != nil && open.Companion != nil {
			pinged.Companion.State = open.Companion.State
		}
		if err := r.patchCI(ctx, task, pinged); err != nil {
			return err
		}
	}
	if note != "" {
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, is.IID, note+mention); err != nil {
			return fmt.Errorf("commenting on issue #%d: %w", is.IID, err)
		}
	}
	// The cause goes before the label (ciRed links to status.ci); a loop that
	// ends in review clears an older one.
	var cause aiv1.NeedsInputCause
	if failed {
		cause = aiv1.NeedsInputCIRed
	}
	if run := task.Status.Run; run.NeedsInputCause != cause || run.NeedsInputURL != "" {
		orig := task.DeepCopy()
		task.Status.Run.NeedsInputCause, task.Status.Run.NeedsInputURL = cause, ""
		if err := client.IgnoreNotFound(r.Status().Patch(ctx, task, client.MergeFrom(orig))); err != nil {
			return fmt.Errorf("recording the needs-input cause: %w", err)
		}
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{next}, RemoveLabels: []string{wf.WorkingLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d to %q: %w", is.IID, next, err)
	}
	if review {
		// The issue is in review already: a failure here is logged, not
		// tried again, so nobody is pinged twice.
		mrIID := task.Status.Run.MergeRequestIID
		if *spec.Notify.RequestReview {
			if _, err := notify.RequestReview(ctx, gl, spec.GitLab.Project, mrIID, task.Spec.Owners); err != nil {
				logger.Info("could not request reviews", "mr", mrIID, "error", err.Error())
			}
		}
		if note == "" && mention != "" && mrIID != 0 {
			body := "🤖 Ready for review: CI passed."
			if ci.State == aiv1.CIStateNone {
				body = "🤖 Ready for review: no pipeline ran for this commit."
			}
			if _, err := gl.CreateMergeRequestNote(ctx, spec.GitLab.Project, mrIID, body+mention); err != nil {
				logger.Info("could not ask for a review", "mr", mrIID, "error", err.Error())
			}
		}
	}
	return r.patchCI(ctx, task, ci)
}

func (r *AgentProjectReconciler) patchCI(ctx context.Context, task *aiv1.AgentTask, ci *aiv1.CIStatus) error {
	if equality.Semantic.DeepEqual(task.Status.CI, ci) {
		return nil // nothing changed since the last poll
	}
	orig := task.DeepCopy()
	task.Status.CI = ci
	return client.IgnoreNotFound(r.Status().Patch(ctx, task, client.MergeFrom(orig)))
}

func jobList(jobs []string) string {
	if len(jobs) == 0 {
		return "(none reported)"
	}
	quoted := make([]string, len(jobs))
	for i, j := range jobs {
		quoted[i] = "`" + j + "`"
	}
	return strings.Join(quoted, ", ")
}

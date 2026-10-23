package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
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
// Pipelines can start after the loop is done: in projects that skip
// pipelines for drafts, one started after the MR was marked ready; and people
// run and retry them. So for an issue in review the MR's head pipeline is
// watched too, see syncReviewCI.
func (r *AgentProjectReconciler) syncCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask, mr *gitlab.MergeRequest) error {
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
	ci := &aiv1.CIStatus{SHA: run.PushedSHA, State: aiv1.CIStatePending}
	if task.Status.CI != nil && task.Status.CI.SHA == run.PushedSHA {
		ci = task.Status.CI.DeepCopy()
	}
	if ci.State.Final() {
		return nil
	}
	glProject := spec.GitLab.Project
	logger := log.FromContext(ctx).WithValues("task", task.Name, "sha", ci.SHA)

	// A fix run was requested and is done, but it pushed nothing new.
	if ci.State == aiv1.CIStateFixing {
		return r.finishCI(ctx, gl, spec, is, task, ci, aiv1.CIStateFailed,
			fmt.Sprintf("🤖 The agent could not fix the failing pipeline (%s): failing jobs %s. Comment `%s …` with guidance or fix it by hand.",
				ci.PipelineURL, jobList(ci.FailedJobs), spec.Workflow.CommandPrefix))
	}

	// The MR's head pipeline is the one GitLab shows and gates merging on; it
	// may also be a merged-results pipeline, whose SHA is another one. Only
	// without it the newest pipeline of the commit counts.
	var pipelines []gitlab.Pipeline
	if head != nil {
		pipelines = []gitlab.Pipeline{*head}
	} else {
		var err error
		if pipelines, err = gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{SHA: ci.SHA, Limit: 1}); err != nil {
			return fmt.Errorf("listing pipelines of %s: %w", ci.SHA, err)
		}
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
			return r.finishCI(ctx, gl, spec, is, task, ci, aiv1.CIStateNone, "")
		}
		return r.patchCI(ctx, task, ci)
	}
	p := pipelines[0]
	ci.PipelineID, ci.PipelineURL = p.ID, p.WebURL

	switch p.Status {
	case "success", "manual":
		return r.finishCI(ctx, gl, spec, is, task, ci, aiv1.CIStateSuccess, "")
	case "canceled":
		return r.finishCI(ctx, gl, spec, is, task, ci, aiv1.CIStateCanceled,
			fmt.Sprintf("🤖 The pipeline %s was canceled, so CI could not confirm this change. Moving the issue to review.", p.WebURL))
	case "failed":
	default:
		ci.State = aiv1.CIStateRunning
		return r.patchCI(ctx, task, ci)
	}

	failed, err := gl.ListPipelineJobs(ctx, glProject, p.ID, "failed")
	if err != nil {
		return fmt.Errorf("listing failed jobs of pipeline %d: %w", p.ID, err)
	}
	ci.FailedJobs = nil
	for _, j := range failed {
		if !j.AllowFailure {
			ci.FailedJobs = append(ci.FailedJobs, j.Name)
		}
	}

	if *spec.CI.RetryFailedJobs && !ci.Retried {
		logger.Info("pipeline failed, retrying failed jobs once", "pipeline", p.ID, "jobs", ci.FailedJobs)
		if _, err := gl.RetryPipeline(ctx, glProject, p.ID); err != nil {
			return fmt.Errorf("retrying pipeline %d: %w", p.ID, err)
		}
		ci.Retried = true
		ci.State = aiv1.CIStateRunning
		return r.patchCI(ctx, task, ci)
	}

	if failing, err := r.baseFailing(ctx, gl, glProject, ci.FailedJobs); err != nil {
		return err
	} else if failing {
		return r.finishCI(ctx, gl, spec, is, task, ci, aiv1.CIStateBaseFailing,
			fmt.Sprintf("🤖 CI is red (%s), but the failing jobs %s also fail on the default branch, so this change is not the cause. Moving the issue to review.",
				p.WebURL, jobList(ci.FailedJobs)))
	}

	if run.CIRound >= *spec.CI.MaxFixRounds {
		return r.finishCI(ctx, gl, spec, is, task, ci, aiv1.CIStateFailed,
			fmt.Sprintf("🤖 CI is still red after %d fix attempts (%s): failing jobs %s. Comment `%s …` with guidance or fix it by hand.",
				run.CIRound, p.WebURL, jobList(ci.FailedJobs), spec.Workflow.CommandPrefix))
	}

	logger.Info("pipeline failed, asking the agent to fix it", "pipeline", p.ID, "round", run.CIRound+1, "jobs", ci.FailedJobs)
	task.Spec.RunRequest++
	task.Spec.Source.CIFixPipelineID = p.ID
	if err := r.Update(ctx, task); err != nil {
		return err
	}
	ci.State = aiv1.CIStateFixing
	return r.patchCI(ctx, task, ci)
}

// syncReviewCI watches the CI of an issue in review. A loop that is not done
// (a label move that failed, a CI fix run that pushed nothing and so ended in
// review) goes back to working, where it finishes. A new head pipeline of the
// pushed commit, or a canceled or base-failing one that was retried, is
// followed: green is recorded as success, canceled only recorded; running or
// red resets the loop and sends the issue back to working, so the pipeline is
// retried and the agent asked to fix it.
func (r *AgentProjectReconciler) syncReviewCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask, head *gitlab.Pipeline) error {
	cur := task.Status.CI
	if cur == nil || cur.SHA != task.Status.Run.PushedSHA {
		return nil
	}
	if !cur.State.Final() {
		return r.backToWorking(ctx, gl, spec, is)
	}
	if head == nil || head.Status == "skipped" || !pipelineChanged(cur, head) {
		return nil
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
	if err := r.patchCI(ctx, task, &aiv1.CIStatus{SHA: cur.SHA, State: aiv1.CIStatePending, PipelineID: head.ID, PipelineURL: head.WebURL, PingedFor: cur.PingedFor}); err != nil {
		return err
	}
	return r.backToWorking(ctx, gl, spec, is)
}

// pipelineChanged reports whether the head pipeline is not the one the CI
// loop ended with: another pipeline, or the same one retried since it was
// canceled or failed with the default branch.
func pipelineChanged(cur *aiv1.CIStatus, head *gitlab.Pipeline) bool {
	switch {
	case head.ID != cur.PipelineID:
		return true
	case cur.State == aiv1.CIStateCanceled:
		return head.Status != "canceled"
	case cur.State == aiv1.CIStateBaseFailing:
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

// baseFailing reports whether every failing job also fails in the latest
// finished pipeline of the default branch.
func (r *AgentProjectReconciler) baseFailing(ctx context.Context, gl gitlab.API, glProject string, jobs []string) (bool, error) {
	if len(jobs) == 0 {
		return false, nil
	}
	proj, err := gl.GetProject(ctx, glProject)
	if err != nil {
		return false, fmt.Errorf("getting project: %w", err)
	}
	ps, err := gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{Ref: proj.DefaultBranch, Scope: "finished", Limit: 1})
	if err != nil || len(ps) == 0 || ps[0].Status != "failed" {
		return false, err
	}
	baseJobs, err := gl.ListPipelineJobs(ctx, glProject, ps[0].ID, "failed")
	if err != nil {
		return false, err
	}
	onBase := map[string]bool{}
	for _, j := range baseJobs {
		onBase[j.Name] = true
	}
	for _, j := range jobs {
		if !onBase[j] {
			return false, nil
		}
	}
	return true, nil
}

// finishCI ends the loop for the pushed commit: success, no CI, canceled and
// base failures go to review; Failed goes to needs-input.
func (r *AgentProjectReconciler) finishCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask, ci *aiv1.CIStatus, state aiv1.CIState, note string) error {
	wf := spec.Workflow
	prev := ci.State
	ci.State = state
	next := wf.ReviewLabel
	if state == aiv1.CIStateFailed {
		next = wf.NeedsInputLabel
	}
	logger := log.FromContext(ctx)
	logger.Info("CI loop finished", "task", task.Name, "state", state, "label", next)
	// Autopilot merges what reaches review; nobody needs to look.
	review := next == wf.ReviewLabel && !task.Spec.Autopilot
	var event aiv1.NotifyEvent
	switch {
	case task.Spec.Autopilot:
		// Autopilot pauses over it and mentions the owners there.
	case state == aiv1.CIStateFailed:
		event = aiv1.NotifyCIFailed
	case review:
		event = aiv1.NotifyReview
	}
	mention := ""
	if event != "" && ci.PingedFor != event && spec.Notify.Wants(event) {
		mention = notify.Mention(task.Spec.Owners)
	}
	// Recorded before the comments, with the state still open: a failure
	// after them repeats the loop's end on the next poll, but not the ping.
	if mention != "" {
		ci.PingedFor = event
		pinged := ci.DeepCopy()
		pinged.State = prev
		if err := r.patchCI(ctx, task, pinged); err != nil {
			return err
		}
	}
	if note != "" {
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, is.IID, note+mention); err != nil {
			return fmt.Errorf("commenting on issue #%d: %w", is.IID, err)
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
			if state == aiv1.CIStateNone {
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

package controller

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
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
func (r *AgentProjectReconciler) syncCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask) error {
	run := task.Status.Run
	if spec.CI.Disabled || run.PushedSHA == "" || task.RunPending() || !is.HasLabel(spec.Workflow.WorkingLabel) {
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

	pipelines, err := gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{SHA: ci.SHA, Limit: 1})
	if err != nil {
		return fmt.Errorf("listing pipelines of %s: %w", ci.SHA, err)
	}
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
	case "success", "skipped", "manual":
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
	ci.State = state
	next := wf.ReviewLabel
	if state == aiv1.CIStateFailed {
		next = wf.NeedsInputLabel
	}
	log.FromContext(ctx).Info("CI loop finished", "task", task.Name, "state", state, "label", next)
	if note != "" {
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, is.IID, note); err != nil {
			return fmt.Errorf("commenting on issue #%d: %w", is.IID, err)
		}
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{next}, RemoveLabels: []string{wf.WorkingLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d to %q: %w", is.IID, next, err)
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

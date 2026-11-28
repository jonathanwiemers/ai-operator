package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// syncHeldCI follows the pipelines of an issue held in needs-input because
// CI stayed red (needsInputCause: ciRed). People retry failed jobs, or start
// a new pipeline for the same commit; once every red tree's head pipeline of
// the pushed heads is green the hold lifts: the issue moves to review as if
// CI had been green the first time, and autopilot's gate passes. head is the
// MR's head pipeline, nil unless the MR's head is the pushed commit: a
// commit by someone else keeps the hold. Until then status.ci follows the
// pipelines it records.
func (r *AgentProjectReconciler) syncHeldCI(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask,
	mr *gitlab.MergeRequest, head *gitlab.Pipeline) error {
	run := task.Status.Run
	cur := task.Status.CI
	if cur == nil || cur.SHA != run.PushedSHA || !ciRedHold(run.NeedsInputCause, cur) {
		return nil
	}
	open, ci := cur.DeepCopy(), cur.DeepCopy()
	homeRed := heldTreeRed(ci.State)
	if homeRed && head != nil && head.Status != "skipped" {
		if err := r.followHeldPipeline(ctx, gl, spec.GitLab.Project, *head, &ci.State, &ci.PipelineID, &ci.PipelineURL, &ci.FailedJobs); err != nil {
			return err
		}
	}
	cc, c := ci.Companion, run.Companion
	if cc != nil && heldTreeRed(cc.State) && c != nil && c.MRIID != 0 && c.PushedSHA == cc.SHA {
		cmr, err := gl.GetMergeRequest(ctx, c.Project, c.MRIID)
		if err != nil && !gitlab.IsNotFound(err) {
			return fmt.Errorf("getting the companion merge request %s!%d: %w", c.Project, c.MRIID, err)
		}
		if err == nil && cmr.State == "opened" && cmr.SHA == cc.SHA && cmr.HeadPipeline != nil && cmr.HeadPipeline.Status != "skipped" {
			if err := r.followHeldPipeline(ctx, gl, c.Project, *cmr.HeadPipeline, &cc.State, &cc.PipelineID, &cc.PipelineURL, &cc.FailedJobs); err != nil {
				return err
			}
		}
	}
	if heldTreeRed(ci.State) || cc != nil && heldTreeRed(cc.State) {
		return r.patchCI(ctx, task, ci) // still held; no write without news
	}
	url := ci.PipelineURL
	if !homeRed {
		url = cc.PipelineURL // only the companion was red
	}
	log.FromContext(ctx).Info("CI of a held issue is green again, lifting the hold", "task", task.Name, "pipeline", url)
	ci.RecoveredAt = &metav1.Time{Time: r.now()}
	return r.finishCI(ctx, gl, spec, is, task, mr, open, ci,
		fmt.Sprintf("✅ CI is green again (pipeline %s), so #%d moves to review.", url, is.IID))
}

// ciRedHold reports whether a needs-input issue is held for red CI. Besides
// the recorded cause, a cleared cause with a failed tree counts: finishCI
// clears the cause before it moves the label, so a lift whose label move
// failed is finished on the next poll.
func ciRedHold(cause aiv1.NeedsInputCause, ci *aiv1.CIStatus) bool {
	switch cause {
	case aiv1.NeedsInputCIRed:
		return true
	case "":
		return ci.State == aiv1.CIStateFailed || ci.Companion != nil && ci.Companion.State == aiv1.CIStateFailed
	}
	return false
}

// heldTreeRed reports whether a tree of a held issue is not green yet: failed,
// or retried and not done or canceled since. Success and the final states
// that went to review before the hold (None, BaseFailing) do not hold it.
func heldTreeRed(s aiv1.CIState) bool {
	switch s {
	case aiv1.CIStateFailed, aiv1.CIStateRunning, aiv1.CIStatePending, aiv1.CIStateFixing, aiv1.CIStateCanceled:
		return true
	}
	return false
}

// followHeldPipeline records the state of a held tree's head pipeline p, by
// the rules of syncCI (manual counts as green) but without retries or fix
// runs. The failed jobs are listed again only when p turns failed or is
// another pipeline; while it runs they are kept, so the hold's reason stays.
func (r *AgentProjectReconciler) followHeldPipeline(ctx context.Context, gl gitlab.API, project string, p gitlab.Pipeline,
	state *aiv1.CIState, id *int64, url *string, failedJobs *[]string) error {
	changed := *id != p.ID
	*id, *url = p.ID, p.WebURL
	switch p.Status {
	case "success", "manual":
		*state, *failedJobs = aiv1.CIStateSuccess, nil
	case "canceled":
		*state = aiv1.CIStateCanceled
	case "failed":
		if *state != aiv1.CIStateFailed || changed {
			failed, err := gl.ListPipelineJobs(ctx, project, p.ID, "failed")
			if err != nil {
				return fmt.Errorf("listing failed jobs of pipeline %d: %w", p.ID, err)
			}
			var jobs []string
			for _, j := range failed {
				if !j.AllowFailure {
					jobs = append(jobs, j.Name)
				}
			}
			*failedJobs = jobs
		}
		*state = aiv1.CIStateFailed
	default:
		*state = aiv1.CIStateRunning
	}
	return nil
}

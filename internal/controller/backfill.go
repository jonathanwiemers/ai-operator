package controller

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// backfillTimeout bounds a backfill step, at most four GitLab requests.
const backfillTimeout = time.Minute

// backfill runs one step of the project's backfill of the Desk's snapshot
// (history.Backfill) after the poll, with the project's GitLab client, and
// records the milestones' requirement counts. Only the closed milestones the
// poll would have planned count. It never fails the reconcile.
func (r *AgentProjectReconciler) backfill(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) {
	if r.Backfill == nil && r.Requirements == nil {
		return
	}
	titleRE, err := titlePattern(spec)
	if err != nil {
		return // the sync reports it
	}
	ctx, cancel := context.WithTimeout(ctx, backfillTimeout)
	defer cancel()
	gl, err := r.gitlabClient(ctx, project, spec)
	if err != nil {
		log.FromContext(ctx).V(1).Info("backfill skipped", "error", err.Error())
		return
	}
	if r.Requirements != nil {
		r.requirements.record(ctx, r.Requirements, gl, store.ProjectKey{Namespace: project.Namespace, Name: project.Name}, spec.GitLab.Project)
	}
	if r.Backfill == nil {
		return
	}
	r.Backfill.Step(ctx, gl, history.BackfillTarget{
		Namespace: project.Namespace, Name: project.Name, GitLab: spec.GitLab.Project, BotID: project.Status.BotUserID,
		Selected: func(m gitlab.Milestone) bool {
			m.State = "" // closed, but planned while open
			selected, _ := milestoneSelection(spec, titleRE, m)
			return selected
		},
	})
}

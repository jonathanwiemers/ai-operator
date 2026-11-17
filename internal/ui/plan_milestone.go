package ui

import (
	"cmp"
	"context"
	"net/http"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// createPlanMilestone creates the plan's milestone {title, description, spec,
// autopilot} with the owner's GitLab token: the owner's text, then the chosen
// markers and the owners lines. The plan claims the create first (phase
// Created under optimistic locking), so one request wins; a GitLab failure
// restores the phase.
func (s *Server) createPlanMilestone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Spec        bool   `json:"spec"`
		Autopilot   bool   `json:"autopilot"`
	}
	pl, ok := s.ownPlan(w, r, 8*maxFollowUpDescription, &req) // JSON escapes a byte in up to 6
	if !ok || !milestoneText(w, &req.Title, &req.Description) {
		return
	}
	if pl.Status.Phase == aiv1.AgentPlanCreated || pl.Status.CreatedMilestoneIID != 0 {
		writeError(w, http.StatusConflict, "a milestone was already created from this plan")
		return
	}
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	gl, tok, ok := s.writingGitLab(w, r, ap)
	if !ok {
		return
	}
	prev, base := pl.Status.Phase, pl.DeepCopy()
	pl.Status.Phase = aiv1.AgentPlanCreated
	switch err := s.writer.Status().Patch(r.Context(), pl, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); {
	case apierrors.IsConflict(err):
		writeError(w, http.StatusConflict, "the plan changed meanwhile; reload it and try again")
		return
	case err != nil:
		s.internalError(w, err)
		return
	}
	// The claim is ours now: settle it even when the client goes away.
	ctx, user := context.WithoutCancel(r.Context()), principalOf(r).Username
	owners, subjects := planMilestoneOwners(ap, tasks, user)
	desc := withHeader(stripOperatorLines(req.Description, ap.Spec.Workflow.WithDefaults().CommandPrefix), milestoneHeader(ap.Spec,
		milestoneHeaderOpts{Spec: req.Spec, Autopilot: req.Autopilot, Owners: owners, Subjects: subjects}))
	m, err := gl.CreateMilestone(ctx, ap.Spec.GitLab.Project, gitlab.CreateMilestoneOptions{Title: req.Title, Description: desc})
	claimed := pl.DeepCopy()
	if err != nil {
		pl.Status.Phase = prev
		if err := s.writer.Status().Patch(ctx, pl, client.MergeFrom(claimed)); err != nil {
			s.log.Error(err, "cannot restore the plan's phase", "namespace", pl.Namespace, "name", pl.Name, "phase", prev)
		}
		s.writeCreateMilestoneError(w, err, tok, "creating a milestone from a plan failed",
			"user", user, "gitlabUser", tok.Username, "namespace", pl.Namespace, "name", pl.Name, "project", ap.Name)
		return
	}
	url := milestoneURL(ap, m)
	pl.Status.CreatedMilestoneIID, pl.Status.CreatedMilestoneURL, pl.Status.Title = m.IID, url, cmp.Or(m.Title, req.Title)
	pl.Status.Draft, pl.Status.DraftRevision, pl.Status.DraftBy = req.Description, pl.Status.DraftRevision+1, "owner"
	if err := s.writer.Status().Patch(ctx, pl, client.MergeFrom(claimed)); err != nil {
		s.log.Error(err, "cannot record the plan's milestone", "namespace", pl.Namespace, "name", pl.Name, "milestone", m.IID)
	}
	s.log.Info("audit", "action", "plan.milestone", "user", user, "gitlabUser", tok.Username,
		"namespace", pl.Namespace, "name", pl.Name, "project", ap.Name, "created", m.IID)
	writeJSON(w, http.StatusCreated, FollowUpMilestoneView{IID: m.IID, WebURL: url})
}

// planMilestoneOwners are the owners and owner-subjects of a milestone user
// creates from a plan: those of the project's live Spec tasks (owners else
// its notify users), and user among the subjects.
func planMilestoneOwners(ap *aiv1.AgentProject, tasks []aiv1.AgentTask, user string) (owners, subjects []string) {
	for _, t := range tasks {
		if t.Spec.Type == aiv1.TaskTypeSpec && t.DeletionTimestamp == nil {
			owners, subjects = append(owners, t.Spec.Owners...), append(subjects, t.Spec.OwnerSubjects...)
		}
	}
	if len(owners) == 0 {
		owners = slices.Clone(ap.Spec.Notify.Users)
	}
	subjects = append(subjects, user)
	slices.Sort(owners)
	slices.Sort(subjects)
	return slices.Compact(owners), slices.Compact(subjects)
}

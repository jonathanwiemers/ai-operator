package ui

import (
	"cmp"
	"net/http"
	"slices"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// PlanOverview is the answer of GET /api/v1/plans: the plans of the
// projects, last active first, and the projects, with or without plans.
type PlanOverview struct {
	Plans    []PlanListItem `json:"plans"`
	Projects []PlanProject  `json:"projects"`
}

// PlanProject is a project of the plan lists. Steer: the caller may create
// and steer its plans.
type PlanProject struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Steer     bool   `json:"steer"`
}

// allPlans lists the plans of every project the user may see, or of the
// project of ?project=namespace/name (404 when the user may not see it).
// 404 without chat.
func (s *Server) allPlans(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ChatEnabled() {
		notFound(w)
		return
	}
	visible, tasks, ok := s.visibleProjects(w, r)
	if !ok {
		return
	}
	if visible, ok = projectFilter(w, r, visible); !ok {
		return
	}
	var list aiv1.AgentPlanList
	if err := s.reader.List(r.Context(), &list); err != nil {
		s.internalError(w, err)
		return
	}

	byProject := map[string][]aiv1.AgentTask{}
	for _, t := range tasks {
		k := t.Namespace + "/" + t.Spec.ProjectRef
		byProject[k] = append(byProject[k], t)
	}
	p := principalOf(r)
	out := PlanOverview{Plans: []PlanListItem{}, Projects: make([]PlanProject, 0, len(visible))}
	steer := make(map[string]bool, len(visible))
	for k, ap := range visible {
		steer[k] = s.planOwner(p, ap, byProject[k])
		out.Projects = append(out.Projects, PlanProject{Namespace: ap.Namespace, Name: ap.Name, Steer: steer[k]})
	}
	slices.SortFunc(out.Projects, func(a, b PlanProject) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	for i := range list.Items {
		pl := &list.Items[i]
		if k := pl.Namespace + "/" + pl.Spec.ProjectRef; visible[k] != nil {
			out.Plans = append(out.Plans, s.planItem(pl, steer[k]))
		}
	}
	slices.SortStableFunc(out.Plans, func(a, b PlanListItem) int {
		return cmp.Or(b.LastActivityAt.Compare(a.LastActivityAt), cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	s.setPlanMilestones(r.Context(), out.Plans, visible, tasks)
	writeJSON(w, http.StatusOK, out)
}

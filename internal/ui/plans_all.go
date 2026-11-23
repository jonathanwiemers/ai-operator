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

// PlanProject is a project of the plan lists: an AgentProject, or a dormant
// entry of a project set under the child name the set would give it. Path is
// the GitLab path. Set is the project set of a set's child or of a dormant
// entry, empty for an AgentProject no set owns. Dormant: a set entry without
// a child; the set creates it for the entry's first plan. Steer: the caller
// may create and steer its plans.
type PlanProject struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Set       string `json:"set,omitempty"`
	Dormant   bool   `json:"dormant,omitempty"`
	Steer     bool   `json:"steer"`
}

// allPlans lists the plans of every project the user may see, the dormant
// set entries (setEntryProjects) included, or of the project of
// ?project=namespace/name (404 when the user may not see it). 404 without
// chat.
func (s *Server) allPlans(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ChatEnabled() {
		notFound(w)
		return
	}
	visible, tasks, ok := s.visibleProjects(w, r)
	if !ok {
		return
	}
	p := principalOf(r)
	entries, err := s.setEntryProjects(r.Context(), p)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if ref := r.URL.Query().Get("project"); ref != "" {
		entries = slices.DeleteFunc(entries, func(e setEntryProject) bool { return e.Namespace+"/"+e.Name != ref })
		if len(entries) > 0 {
			// No AgentProject holds an entry's name.
			visible = map[string]*aiv1.AgentProject{}
		}
	}
	if len(entries) == 0 {
		if visible, ok = projectFilter(w, r, visible); !ok {
			return
		}
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
	out := PlanOverview{Plans: []PlanListItem{}, Projects: make([]PlanProject, 0, len(visible)+len(entries))}
	steer := make(map[string]bool, len(visible)+len(entries))
	for k, ap := range visible {
		steer[k] = s.planOwner(p, ap, byProject[k])
		out.Projects = append(out.Projects, PlanProject{Namespace: ap.Namespace, Name: ap.Name,
			Path: ap.Spec.GitLab.Project, Set: ap.Labels[aiv1.LabelProjectSet], Steer: steer[k]})
	}
	// A dormant entry has no tasks: steer is the grant alone.
	for _, e := range entries {
		k := e.Namespace + "/" + e.Name
		steer[k] = s.authz.CanSteer(p, e.Namespace, e.Name)
		out.Projects = append(out.Projects, PlanProject{Namespace: e.Namespace, Name: e.Name,
			Path: e.Path, Set: e.Set, Dormant: true, Steer: steer[k]})
	}
	slices.SortFunc(out.Projects, func(a, b PlanProject) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	for i := range list.Items {
		pl := &list.Items[i]
		if st, ok := steer[pl.Namespace+"/"+pl.Spec.ProjectRef]; ok {
			out.Plans = append(out.Plans, s.planItem(pl, st))
		}
	}
	slices.SortStableFunc(out.Plans, func(a, b PlanListItem) int {
		return cmp.Or(b.LastActivityAt.Compare(a.LastActivityAt), cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	s.setPlanMilestones(r.Context(), out.Plans, visible, tasks)
	writeJSON(w, http.StatusOK, out)
}

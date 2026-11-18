package ui

import (
	"net/http"
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// ProjectSetSummary is an AgentProjectSet with the result of its last
// discovery.
type ProjectSetSummary struct {
	Namespace         string                 `json:"namespace"`
	Name              string                 `json:"name"`
	Group             string                 `json:"group"`
	Include           []string               `json:"include"`
	Exclude           []string               `json:"exclude,omitempty"`
	PollInterval      string                 `json:"pollInterval"`
	LastDiscoveryTime *metav1.Time           `json:"lastDiscoveryTime,omitempty"`
	Counts            aiv1.ProjectSetCounts  `json:"counts"`
	Projects          []aiv1.ProjectSetEntry `json:"projects"`
	DroppedTargets    []string               `json:"droppedTargets,omitempty"`
	Conditions        []metav1.Condition     `json:"conditions,omitempty"`
}

// summarizeSet builds the summary of set.
func summarizeSet(set *aiv1.AgentProjectSet) ProjectSetSummary {
	projects := set.Status.Projects
	if projects == nil {
		projects = []aiv1.ProjectSetEntry{}
	}
	return ProjectSetSummary{
		Namespace:         set.Namespace,
		Name:              set.Name,
		Group:             set.Spec.GitLab.Group,
		Include:           set.Spec.Include,
		Exclude:           set.Spec.Exclude,
		PollInterval:      shortDuration(set.Spec.PollInterval.Duration),
		LastDiscoveryTime: set.Status.LastDiscoveryTime,
		Counts:            set.Status.Counts,
		Projects:          projects,
		DroppedTargets:    set.Status.DroppedTargets,
		Conditions:        set.Status.Conditions,
	}
}

// projectSets lists the AgentProjectSets the user may see. Without the CRD
// (a cluster whose CRDs lag the Desk) the list is empty.
func (s *Server) projectSets(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	out := []ProjectSetSummary{}
	var sets aiv1.AgentProjectSetList
	if err := s.reader.List(r.Context(), &sets); err != nil {
		if meta.IsNoMatchError(err) {
			writeJSON(w, http.StatusOK, out)
			return
		}
		s.internalError(w, err)
		return
	}
	for i := range sets.Items {
		set := &sets.Items[i]
		if !s.authz.CanView(p, set.Namespace, set.Name) {
			continue
		}
		out = append(out, summarizeSet(set))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, http.StatusOK, out)
}

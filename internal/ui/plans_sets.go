package ui

import (
	"cmp"
	"context"
	"net/http"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// setEntryProject is a project a set discovered that has no child yet: a
// dormant entry, under the child name the set would give it. URL is the
// set's GitLab instance, Path the entry's full project path there.
type setEntryProject struct {
	Set, Namespace, Name, Path, URL string
}

// setEntryProjects lists the dormant entries without a child of the sets p
// may view, under their computed child names, sorted by namespace and name.
// An entry is left out when any AgentProject holds its child name in the
// set's namespace, also one p cannot see (the set would answer nameTaken),
// or when p may not view that name. Without the AgentProjectSet CRD there
// are none.
func (s *Server) setEntryProjects(ctx context.Context, p Principal) ([]setEntryProject, error) {
	var sets aiv1.AgentProjectSetList
	if err := s.reader.List(ctx, &sets); err != nil {
		if meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(sets.Items) == 0 {
		return nil, nil
	}
	var projects aiv1.AgentProjectList
	if err := s.reader.List(ctx, &projects); err != nil {
		return nil, err
	}
	taken := make(map[string]bool, len(projects.Items))
	for _, ap := range projects.Items {
		taken[ap.Namespace+"/"+ap.Name] = true
	}
	slices.SortFunc(sets.Items, func(a, b aiv1.AgentProjectSet) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	var out []setEntryProject
	for i := range sets.Items {
		set := &sets.Items[i]
		if !s.authz.CanView(p, set.Namespace, set.Name) {
			continue
		}
		for _, e := range set.Status.Projects {
			if e.State != aiv1.ProjectSetDormant || e.Child != "" {
				continue
			}
			name := aiv1.ProjectSetChildName(set.Spec.GitLab.Group, e.Path)
			k := set.Namespace + "/" + name
			// taken also keeps a second set's entry of the same name out.
			if taken[k] || !s.authz.CanView(p, set.Namespace, name) {
				continue
			}
			taken[k] = true
			out = append(out, setEntryProject{Set: set.Name, Namespace: set.Namespace, Name: name, Path: e.Path, URL: set.Spec.GitLab.URL})
		}
	}
	slices.SortFunc(out, func(a, b setEntryProject) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// planProject reads the project of the request path and its tasks, like
// loadProject, but stands in for an offered dormant set entry without an
// AgentProject: the child name in the namespace, the set label, the entry's
// GitLab project (for uploads with the user's token), no tasks.
func (s *Server) planProject(w http.ResponseWriter, r *http.Request) (planScope, bool) {
	p, ns, name := principalOf(r), r.PathValue("namespace"), r.PathValue("name")
	if !s.authz.CanView(p, ns, name) {
		notFound(w)
		return planScope{}, false
	}
	var ap aiv1.AgentProject
	err := s.reader.Get(r.Context(), client.ObjectKey{Namespace: ns, Name: name}, &ap)
	if err == nil {
		tasks, err := s.projectTasks(r.Context(), ns, name)
		if err != nil {
			s.internalError(w, err)
			return planScope{}, false
		}
		return planScope{Project: &ap, Tasks: tasks}, true
	}
	var e setEntryProject
	ok := false
	if apierrors.IsNotFound(err) {
		e, ok, err = s.setEntryProject(r.Context(), p, ns, name)
	}
	switch {
	case err != nil:
		s.internalError(w, err)
	case !ok:
		notFound(w)
	default:
		return planScope{Project: &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
			Labels: map[string]string{aiv1.LabelProjectSet: e.Set}},
			Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: e.URL, Project: e.Path}}}, Set: e.Set}, true
	}
	return planScope{}, false
}

// setEntryProject is the entry of setEntryProjects named ns/name, if any.
// It does not check steer.
func (s *Server) setEntryProject(ctx context.Context, p Principal, ns, name string) (setEntryProject, bool, error) {
	entries, err := s.setEntryProjects(ctx, p)
	if err != nil {
		return setEntryProject{}, false, err
	}
	i := slices.IndexFunc(entries, func(e setEntryProject) bool { return e.Namespace == ns && e.Name == name })
	if i < 0 {
		return setEntryProject{}, false, nil
	}
	return entries[i], true, nil
}

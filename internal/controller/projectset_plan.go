package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/projectglob"
)

// projectSetInput is everything planProjectSet decides from.
type projectSetInput struct {
	// Set is the set, its spec with defaults.
	Set *aiv1.AgentProjectSet
	// Template is the decoded template, without AgentProject defaults.
	Template aiv1.AgentProjectSpec
	// Entries are the merged specs of the projects spec.projects names, by
	// normalized path: the template with the entry's spec on top.
	Entries   map[string]aiv1.AgentProjectSpec
	Discovery *gitlab.GroupDiscovery
	// Projects are all AgentProjects in every namespace.
	Projects []aiv1.AgentProject
	// TaskCounts are the AgentTasks per child name in the set's namespace.
	TaskCounts map[string]int
	// PlanCounts are the AgentPlans per spec.projectRef in the set's
	// namespace, without those being deleted.
	PlanCounts map[string]int
	Now        time.Time
}

// plannedChild is a child AgentProject the set writes.
type plannedChild struct {
	Name, Path string
	Spec       aiv1.AgentProjectSpec
	// Hash is the hex sha256 of the spec's JSON, for AnnotationTemplateHash.
	Hash string
	// Adopt is set when the child is an AgentProject the set does not own
	// yet: the annotated explicit project of a named entry.
	Adopt bool
}

// projectSetPlan is what the set writes: its children and its status.
type projectSetPlan struct {
	Children []plannedChild
	Entries  []aiv1.ProjectSetProjectStatus
	Counts   aiv1.ProjectSetCounts
	// Dropped are the crossRepo targets of the template and the entries that
	// the set's exclude matches, unique and sorted.
	Dropped []string
	// missing are the normalized paths of entries whose project is not in
	// the discovery: the set registers no project hook for them.
	missing map[string]bool
}

// namedReason is the reason of a project spec.projects names.
const namedReason = "named in spec.projects"

// planProjectSet decides, without I/O, the state of every project the set's
// include matches and which children the set writes.
func planProjectSet(in projectSetInput) projectSetPlan {
	set := in.Set
	spec := set.Spec.WithDefaults()
	tmpl, dropped := projectSetTemplate(in.Template, spec.Exclude)
	named := make(map[string]aiv1.AgentProjectSpec, len(in.Entries))
	for path, entry := range in.Entries {
		s, d := projectSetTemplate(entry, spec.Exclude)
		named[path] = s
		dropped = append(dropped, d...)
	}
	slices.Sort(dropped)
	plan := projectSetPlan{Dropped: slices.Compact(dropped), missing: map[string]bool{}}
	entries := make(map[string]aiv1.ProjectSetEntry, len(spec.Projects))
	for _, e := range spec.Projects {
		entries[aiv1.NormalizeProject(e.Path)] = e
	}

	// Ownership and watchers, by normalized project path and by name.
	owned := map[string]*aiv1.AgentProject{}
	watchers := map[string][]*aiv1.AgentProject{}
	byName := map[string]*aiv1.AgentProject{}
	for i := range in.Projects {
		p := &in.Projects[i]
		path := aiv1.NormalizeProject(p.Spec.GitLab.Project)
		if metav1.IsControlledBy(p, set) {
			owned[path] = p
		} else {
			watchers[path] = append(watchers[path], p)
		}
		if p.Namespace == set.Namespace {
			byName[p.Name] = p
		}
	}
	prev := map[string]aiv1.ProjectSetProjectStatus{}
	for _, e := range set.Status.Projects {
		prev[aiv1.NormalizeProject(e.Path)] = e
	}
	disc := in.Discovery
	if disc == nil {
		disc = &gitlab.GroupDiscovery{}
	}
	ready := map[string]bool{}
	for _, is := range disc.ReadyIssues {
		ready[aiv1.NormalizeProject(is.Project)] = true
	}
	sel := in.Template.WithDefaults()
	titleRE, reErr := titlePattern(sel)

	discovered, seen := projectSetOrder(disc.Projects, entries)
	planned := map[string]string{} // child name -> normalized path
	for _, dp := range discovered {
		path := aiv1.NormalizeProject(dp.FullPath)
		entry, isNamed := entries[path]
		child := owned[path]
		if !projectglob.MatchAny(spec.Include, path) {
			if child != nil || isNamed {
				plan.add(dp.FullPath, aiv1.ProjectSetExcluded, childName(child), namedBut(isNamed, "not matched by include"), prev[path].LastActiveTime)
			}
			continue
		}
		e := aiv1.ProjectSetProjectStatus{Path: dp.FullPath, LastActiveTime: prev[path].LastActiveTime}
		if child != nil {
			e.Child = child.Name
		}
		// An owned child keeps its name: a rename would delete its tasks.
		name := aiv1.ProjectSetChildName(spec.GitLab.Group, dp.FullPath)
		if entry.Name != "" {
			name = entry.Name
		}
		if child != nil {
			name = child.Name
		}
		// A named project may adopt the AgentProject that watches it; an
		// unnamed one leaves it explicit.
		var adopt, watcher *aiv1.AgentProject
		var refusal string
		if ws := watchers[path]; len(ws) > 0 {
			watcher = ws[0]
			if isNamed {
				adopt, watcher, refusal = projectSetAdoption(set, ws, name)
			}
		}
		if reason, ok := projectSetSkip(dp, spec.Exclude, watcher); ok {
			e.State, e.Reason = reason.state, namedBut(isNamed, reason.text)
			if reason.state == aiv1.ProjectSetExplicit && refusal != "" {
				e.Reason += "; " + refusal
			}
			plan.Entries = append(plan.Entries, e)
			continue
		}
		// A child whose milestones all settle is idle: their tasks and
		// markers do not count, a ready issue does.
		work := ready[path] || !onlySettling(child) && (in.TaskCounts[name] > 0 ||
			(reErr == nil && projectSetSelects(sel, titleRE, dp.Milestones)))
		// Plans count always, settling or not: the child keeps them. Plans of
		// an AgentProject outside the set that holds the name are its own.
		var plans int
		if p := byName[name]; p == nil || p == adopt || metav1.IsControlledBy(p, set) {
			plans = in.PlanCounts[name]
		}
		// A named project needs no work: it is never dormant.
		active := work || plans > 0 || isNamed
		now := metav1.NewTime(in.Now)
		switch {
		case active && child == nil:
			e.LastActiveTime = &now
			existing := byName[name]
			if existing == adopt {
				existing = nil // the adopted object holds the name already
			}
			if reason := projectSetNameTaken(name, path, existing, planned, set); reason != "" {
				adopt = nil
				e.State, e.Reason = aiv1.ProjectSetNameTaken, reason
				break
			}
			e.State, e.Child = aiv1.ProjectSetActive, name
		case active:
			e.State, e.LastActiveTime = aiv1.ProjectSetActive, &now
		case child != nil:
			if e.LastActiveTime == nil {
				e.LastActiveTime = &now
			}
			e.State = aiv1.ProjectSetActive
			if in.Now.Sub(e.LastActiveTime.Time) >= spec.DormantAfter.Duration {
				e.State, e.Reason = aiv1.ProjectSetDormant, fmt.Sprintf("no work for %s", spec.DormantAfter.Duration)
			}
		default:
			e.State, e.Reason = aiv1.ProjectSetDormant, "no milestone with the spec marker, ready issue or task"
		}
		switch {
		case e.State != aiv1.ProjectSetActive:
		case adopt != nil:
			e.Reason = fmt.Sprintf("adopted from AgentProject %s/%s", adopt.Namespace, adopt.Name)
		case isNamed:
			e.Reason = namedReason
			if child != nil && entry.Name != "" && entry.Name != child.Name {
				e.Reason += fmt.Sprintf("; the child keeps its name %s, name %s applies only to a new child", child.Name, entry.Name)
			}
		case !work && plans > 0:
			e.Reason = keptReason(0, plans, "")
		}
		// A dormant child is not planned: the set removes it, or revives it
		// with the planned spec once the project is active again.
		if e.Child != "" && e.State == aiv1.ProjectSetActive {
			planned[e.Child] = path
			childSpec, ok := named[path]
			if !ok {
				childSpec = tmpl
			}
			c := planChild(name, dp.FullPath, childSpec, spec)
			c.Adopt = adopt != nil
			plan.Children = append(plan.Children, c)
		}
		plan.Entries = append(plan.Entries, e)
	}
	for path, child := range owned {
		if !seen[path] {
			_, isNamed := entries[path]
			plan.add(child.Spec.GitLab.Project, aiv1.ProjectSetDormant, child.Name, namedBut(isNamed, "not found in the group"), prev[path].LastActiveTime)
			plan.missing[path] = true
		}
	}
	for path, e := range entries {
		if !seen[path] && owned[path] == nil {
			plan.add(e.Path, aiv1.ProjectSetDormant, "", namedBut(true, "not found in the group"), prev[path].LastActiveTime)
			plan.missing[path] = true
		}
	}

	slices.SortFunc(plan.Entries, func(a, b aiv1.ProjectSetProjectStatus) int {
		return strings.Compare(aiv1.NormalizeProject(a.Path), aiv1.NormalizeProject(b.Path))
	})
	slices.SortFunc(plan.Children, func(a, b plannedChild) int {
		return strings.Compare(aiv1.NormalizeProject(a.Path), aiv1.NormalizeProject(b.Path))
	})
	plan.Counts = projectSetCounts(plan.Entries)
	return plan
}

func (p *projectSetPlan) add(path string, state aiv1.ProjectSetState, child, reason string, last *metav1.Time) {
	p.Entries = append(p.Entries, aiv1.ProjectSetProjectStatus{Path: path, State: state, Child: child, Reason: reason, LastActiveTime: last})
}

// projectSetOrder returns the discovered projects without duplicate paths,
// the named ones first by path, so a named project wins a child name over an
// unnamed one whatever the discovery order; the others keep their order. seen
// holds every normalized path.
func projectSetOrder(projects []gitlab.DiscoveredProject, entries map[string]aiv1.ProjectSetEntry) ([]gitlab.DiscoveredProject, map[string]bool) {
	seen := map[string]bool{}
	var out []gitlab.DiscoveredProject
	for _, dp := range projects {
		if path := aiv1.NormalizeProject(dp.FullPath); !seen[path] {
			seen[path] = true
			out = append(out, dp)
		}
	}
	slices.SortStableFunc(out, func(a, b gitlab.DiscoveredProject) int {
		pa, pb := aiv1.NormalizeProject(a.FullPath), aiv1.NormalizeProject(b.FullPath)
		_, na := entries[pa]
		_, nb := entries[pb]
		switch {
		case na && nb:
			return strings.Compare(pa, pb)
		case na:
			return -1
		case nb:
			return 1
		}
		return 0
	})
	return out, seen
}

// namedBut prefixes the reason of a named project that gets no child, e.g.
// "named in spec.projects, but archived".
func namedBut(named bool, reason string) string {
	if !named {
		return reason
	}
	return namedReason + ", but " + reason
}

func childName(p *aiv1.AgentProject) string {
	if p == nil {
		return ""
	}
	return p.Name
}

type projectSetReason struct {
	state aiv1.ProjectSetState
	text  string
}

// projectSetSkip reports why a matched project gets no child whatever its
// work: archived, excluded or watched by an AgentProject the set does not own.
func projectSetSkip(dp gitlab.DiscoveredProject, exclude []string, watcher *aiv1.AgentProject) (projectSetReason, bool) {
	if dp.Archived {
		return projectSetReason{aiv1.ProjectSetExcluded, "archived"}, true
	}
	for _, pattern := range exclude {
		if projectglob.MatchAny([]string{pattern}, dp.FullPath) {
			return projectSetReason{aiv1.ProjectSetExcluded, "matches exclude " + pattern}, true
		}
	}
	if watcher != nil {
		return projectSetReason{aiv1.ProjectSetExplicit, fmt.Sprintf("watched by AgentProject %s/%s", watcher.Namespace, watcher.Name)}, true
	}
	return projectSetReason{}, false
}

// projectSetAdoption decides whether a named project adopts one of the
// AgentProjects outside the set that watch it (at least one), as the child
// called name. It returns the adopted object, or the watcher the project stays
// explicit for and why it is not adopted. The annotated watcher is checked
// first; adoption needs it in the set's namespace, without a controller,
// alone on the path and under the child's name, so nothing is deleted.
func projectSetAdoption(set *aiv1.AgentProjectSet, watchers []*aiv1.AgentProject, name string) (adopt, watcher *aiv1.AgentProject, why string) {
	w := watchers[0]
	for _, p := range watchers {
		if p.Annotations[aiv1.AnnotationProjectSet] == set.Name {
			w = p
			break
		}
	}
	refuse := func(format string, args ...any) (*aiv1.AgentProject, *aiv1.AgentProject, string) {
		return nil, w, "not adopted: " + fmt.Sprintf(format, args...)
	}
	switch v := w.Annotations[aiv1.AnnotationProjectSet]; {
	case v == "":
		return nil, w, fmt.Sprintf("annotate it with %s=%s to hand it to the set", aiv1.AnnotationProjectSet, set.Name)
	case v != set.Name:
		return refuse("annotated for set %s", v)
	case w.Namespace != set.Namespace:
		return refuse("in namespace %s, the set is in %s", w.Namespace, set.Namespace)
	}
	if ref := metav1.GetControllerOf(w); ref != nil {
		return refuse("controlled by %s/%s", ref.Kind, ref.Name)
	}
	for _, p := range watchers {
		if p != w {
			return refuse("also watched by %s/%s", p.Namespace, p.Name)
		}
	}
	if w.Name != name {
		return refuse("name %s, the entry's child name is %s", w.Name, name)
	}
	return w, nil, ""
}

// projectSetSelects reports whether a child would plan one of the milestones:
// the same selection as an AgentProject's, with the template.
func projectSetSelects(tmpl aiv1.AgentProjectSpec, titleRE *regexp.Regexp, ms []gitlab.DiscoveredMilestone) bool {
	for _, m := range ms {
		if ok, _ := milestoneSelection(tmpl, titleRE, gitlab.Milestone{IID: m.IID, Title: m.Title, Description: m.Description, State: "active"}); ok {
			return true
		}
	}
	return false
}

// projectSetNameTaken says why a new child cannot take its name: an object
// the set does not own has it, or another project of the set already does.
func projectSetNameTaken(name, path string, existing *aiv1.AgentProject, planned map[string]string, set *aiv1.AgentProjectSet) string {
	if existing != nil && !metav1.IsControlledBy(existing, set) {
		return fmt.Sprintf("name %s is taken by AgentProject %s/%s", name, existing.Namespace, existing.Name)
	}
	if existing != nil && aiv1.NormalizeProject(existing.Spec.GitLab.Project) != path {
		return fmt.Sprintf("name %s is taken by the child of %s", name, existing.Spec.GitLab.Project)
	}
	if other, ok := planned[name]; ok && other != path {
		return fmt.Sprintf("name %s is taken by the child of %s", name, other)
	}
	return ""
}

// projectSetTemplate drops the template's crossRepo targets that exclude
// matches and returns them.
func projectSetTemplate(t aiv1.AgentProjectSpec, exclude []string) (aiv1.AgentProjectSpec, []string) {
	t = *t.DeepCopy()
	if t.CrossRepo == nil {
		return t, nil
	}
	var kept []aiv1.CrossRepoTarget
	var dropped []string
	for _, target := range t.CrossRepo.Targets {
		switch {
		case !projectglob.MatchAny(exclude, target.Project):
			kept = append(kept, target)
		case !slices.Contains(dropped, target.Project):
			dropped = append(dropped, target.Project)
		}
	}
	t.CrossRepo.Targets = kept
	if len(kept) == 0 {
		t.CrossRepo = nil
	}
	return t, dropped
}

// planChild is the child spec of a project: tmpl, the template or the
// entry's merged spec, with the set's GitLab and the project's path, and its
// hash. The hash covers only that final spec, so a child without an entry
// keeps the hash it had before entries existed.
func planChild(name, path string, tmpl aiv1.AgentProjectSpec, spec aiv1.AgentProjectSetSpec) plannedChild {
	s := *tmpl.DeepCopy()
	s.GitLab = aiv1.GitLabSpec{URL: spec.GitLab.URL, Project: path, TokenSecretRef: spec.GitLab.TokenSecretRef}
	raw, err := json.Marshal(s)
	if err != nil {
		// An AgentProjectSpec always marshals; keep the hash stable anyway.
		raw = []byte(err.Error())
	}
	sum := sha256.Sum256(raw)
	return plannedChild{Name: name, Path: path, Spec: s, Hash: hex.EncodeToString(sum[:])}
}

// projectSetCounts counts the entries by state.
func projectSetCounts(entries []aiv1.ProjectSetProjectStatus) aiv1.ProjectSetCounts {
	var c aiv1.ProjectSetCounts
	for _, e := range entries {
		switch e.State {
		case aiv1.ProjectSetActive:
			c.Active++
		case aiv1.ProjectSetDormant:
			c.Dormant++
		case aiv1.ProjectSetExcluded:
			c.Excluded++
		case aiv1.ProjectSetExplicit:
			c.Explicit++
		case aiv1.ProjectSetNameTaken:
			c.NameTaken++
		}
	}
	return c
}

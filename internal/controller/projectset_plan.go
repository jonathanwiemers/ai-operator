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
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/projectglob"
)

// maxChildSlug is the length a child name's slug is cut to before the hash.
const maxChildSlug = 50

// projectSetChildName is the name of a set's child for a project: the slug of
// the path below the group, cut to 50 characters plus a hash of the full path
// when longer.
func projectSetChildName(group, path string) string {
	full := aiv1.NormalizeProject(path)
	rel := full
	if g := aiv1.NormalizeProject(group); g != "" {
		rel = strings.TrimPrefix(full, g+"/")
	}
	slug := naming.Slug(rel)
	if len(slug) <= maxChildSlug && slug != "" {
		return slug
	}
	sum := sha256.Sum256([]byte(full))
	slug = strings.Trim(slug[:min(len(slug), maxChildSlug)], "-")
	if slug == "" {
		slug = "project"
	}
	return slug + "-" + hex.EncodeToString(sum[:])[:6]
}

// projectSetInput is everything planProjectSet decides from.
type projectSetInput struct {
	// Set is the set, its spec with defaults.
	Set *aiv1.AgentProjectSet
	// Template is the decoded template, without AgentProject defaults.
	Template  aiv1.AgentProjectSpec
	Discovery *gitlab.GroupDiscovery
	// Projects are all AgentProjects in every namespace.
	Projects []aiv1.AgentProject
	// TaskCounts are the AgentTasks per child name in the set's namespace.
	TaskCounts map[string]int
	Now        time.Time
}

// plannedChild is a child AgentProject the set writes.
type plannedChild struct {
	Name, Path string
	Spec       aiv1.AgentProjectSpec
	// Hash is the hex sha256 of the spec's JSON, for AnnotationTemplateHash.
	Hash string
}

// projectSetPlan is what the set writes: its children and its status.
type projectSetPlan struct {
	Children []plannedChild
	Entries  []aiv1.ProjectSetEntry
	Counts   aiv1.ProjectSetCounts
	// Dropped are the template's crossRepo targets the set's exclude matches.
	Dropped []string
}

// planProjectSet decides, without I/O, the state of every project the set's
// include matches and which children the set writes.
func planProjectSet(in projectSetInput) projectSetPlan {
	set := in.Set
	spec := set.Spec.WithDefaults()
	tmpl, dropped := projectSetTemplate(in.Template, spec.Exclude)
	plan := projectSetPlan{Dropped: dropped}

	// Ownership and watchers, by normalized project path and by name.
	owned := map[string]*aiv1.AgentProject{}
	watchers := map[string]*aiv1.AgentProject{}
	byName := map[string]*aiv1.AgentProject{}
	for i := range in.Projects {
		p := &in.Projects[i]
		path := aiv1.NormalizeProject(p.Spec.GitLab.Project)
		switch {
		case metav1.IsControlledBy(p, set):
			owned[path] = p
		case watchers[path] == nil:
			watchers[path] = p
		}
		if p.Namespace == set.Namespace {
			byName[p.Name] = p
		}
	}
	prev := map[string]aiv1.ProjectSetEntry{}
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

	seen := map[string]bool{}
	planned := map[string]string{} // child name -> normalized path
	for _, dp := range disc.Projects {
		path := aiv1.NormalizeProject(dp.FullPath)
		if seen[path] {
			continue
		}
		seen[path] = true
		child := owned[path]
		if !projectglob.MatchAny(spec.Include, path) {
			if child != nil {
				plan.add(dp.FullPath, aiv1.ProjectSetExcluded, child.Name, "not matched by include", prev[path].LastActiveTime)
			}
			continue
		}
		e := aiv1.ProjectSetEntry{Path: dp.FullPath, LastActiveTime: prev[path].LastActiveTime}
		if child != nil {
			e.Child = child.Name
		}
		if reason, ok := projectSetSkip(dp, spec.Exclude, watchers[path]); ok {
			e.State, e.Reason = reason.state, reason.text
			plan.Entries = append(plan.Entries, e)
			continue
		}
		name := projectSetChildName(spec.GitLab.Group, dp.FullPath)
		if child != nil {
			name = child.Name
		}
		active := ready[path] || in.TaskCounts[name] > 0 ||
			(reErr == nil && projectSetSelects(sel, titleRE, dp.Milestones))
		now := metav1.NewTime(in.Now)
		switch {
		case active && child == nil:
			e.LastActiveTime = &now
			if reason := projectSetNameTaken(name, path, byName[name], planned, set); reason != "" {
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
		if e.Child != "" {
			planned[e.Child] = path
			plan.Children = append(plan.Children, planChild(name, dp.FullPath, tmpl, spec))
		}
		plan.Entries = append(plan.Entries, e)
	}
	for path, child := range owned {
		if !seen[path] {
			plan.add(child.Spec.GitLab.Project, aiv1.ProjectSetDormant, child.Name, "not found in the group", prev[path].LastActiveTime)
		}
	}

	slices.SortFunc(plan.Entries, func(a, b aiv1.ProjectSetEntry) int {
		return strings.Compare(aiv1.NormalizeProject(a.Path), aiv1.NormalizeProject(b.Path))
	})
	slices.SortFunc(plan.Children, func(a, b plannedChild) int {
		return strings.Compare(aiv1.NormalizeProject(a.Path), aiv1.NormalizeProject(b.Path))
	})
	plan.Counts = projectSetCounts(plan.Entries)
	return plan
}

func (p *projectSetPlan) add(path string, state aiv1.ProjectSetState, child, reason string, last *metav1.Time) {
	p.Entries = append(p.Entries, aiv1.ProjectSetEntry{Path: path, State: state, Child: child, Reason: reason, LastActiveTime: last})
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

// planChild is the child spec of a project: the template with the set's
// GitLab and the project's path, and its hash.
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
func projectSetCounts(entries []aiv1.ProjectSetEntry) aiv1.ProjectSetCounts {
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

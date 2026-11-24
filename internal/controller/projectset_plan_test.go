package controller

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

var planNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func planSet() *aiv1.AgentProjectSet {
	return &aiv1.AgentProjectSet{
		ObjectMeta: metav1.ObjectMeta{Name: "jo", Namespace: "agents", UID: types.UID("set-uid")},
		Spec: aiv1.AgentProjectSetSpec{
			GitLab: aiv1.ProjectSetGitLab{
				URL:            "https://gitlab.example",
				Group:          "jo.wiemers",
				TokenSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "gitlab"}, Key: "token"},
			},
			Include: []string{"jo.wiemers/**"},
			Exclude: []string{"jo.wiemers/archived/**"},
		}.WithDefaults(),
	}
}

func specMilestone(desc string) gitlab.DiscoveredMilestone {
	return gitlab.DiscoveredMilestone{IID: 3, Title: "Checkout", Description: desc}
}

func agentProject(ns, name, project string, owner *aiv1.AgentProjectSet) aiv1.AgentProject {
	p := aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: project}},
	}
	if owner != nil {
		p.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "ai.wiemers.me/v1alpha1", Kind: "AgentProjectSet",
			Name: owner.Name, UID: owner.UID, Controller: ptr.To(true),
		}}
	}
	return p
}

func otherSet() *aiv1.AgentProjectSet {
	return &aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "agents", UID: "other-uid"}}
}

func ago(d time.Duration) *metav1.Time {
	t := metav1.NewTime(planNow.Add(-d))
	return &t
}

func TestProjectSetPlanStates(t *testing.T) {
	set := planSet()
	type want struct {
		state  aiv1.ProjectSetState
		child  string
		reason string // substring
	}
	cases := []struct {
		name      string
		projects  []gitlab.DiscoveredProject
		ready     []gitlab.DiscoveredIssue
		existing  []aiv1.AgentProject
		tasks     map[string]int
		prev      []aiv1.ProjectSetProjectStatus
		want      map[string]want
		wantChild []string
	}{
		{
			name:      "spec milestone makes the project active",
			projects:  []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/devops/functions/pnpm", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec\n\nGoal")}}},
			want:      map[string]want{"jo.wiemers/devops/functions/pnpm": {aiv1.ProjectSetActive, "devops-functions-pnpm", ""}},
			wantChild: []string{"devops-functions-pnpm"},
		},
		{
			name:      "autopilot marker counts as spec",
			projects:  []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent autopilot\n\nGoal")}}},
			want:      map[string]want{"jo.wiemers/shop": {aiv1.ProjectSetActive, "shop", ""}},
			wantChild: []string{"shop"},
		},
		{
			name:      "ready issue only",
			projects:  []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/Shop"}},
			ready:     []gitlab.DiscoveredIssue{{Project: "JO.wiemers/shop", IID: 7}},
			want:      map[string]want{"jo.wiemers/Shop": {aiv1.ProjectSetActive, "shop", ""}},
			wantChild: []string{"shop"},
		},
		{
			name:      "task only",
			projects:  []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop"}},
			tasks:     map[string]int{"shop": 1},
			want:      map[string]want{"jo.wiemers/shop": {aiv1.ProjectSetActive, "shop", ""}},
			wantChild: []string{"shop"},
		},
		{
			name:     "milestone without marker and no issue is dormant",
			projects: []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop", Milestones: []gitlab.DiscoveredMilestone{specMilestone("just a milestone")}}},
			tasks:    map[string]int{"other": 3},
			want:     map[string]want{"jo.wiemers/shop": {aiv1.ProjectSetDormant, "", "no milestone"}},
		},
		{
			name:     "include does not match",
			projects: []gitlab.DiscoveredProject{{FullPath: "someone/else", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}}},
			want:     map[string]want{},
		},
		{
			name: "exclude beats include, archived is excluded",
			projects: []gitlab.DiscoveredProject{
				{FullPath: "jo.wiemers/Archived/old", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
				{FullPath: "jo.wiemers/gone", Archived: true, Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
			},
			want: map[string]want{
				"jo.wiemers/Archived/old": {aiv1.ProjectSetExcluded, "", "matches exclude jo.wiemers/archived/**"},
				"jo.wiemers/gone":         {aiv1.ProjectSetExcluded, "", "archived"},
			},
		},
		{
			name: "explicit AgentProject in another namespace, case-insensitive, and another set's child",
			projects: []gitlab.DiscoveredProject{
				{FullPath: "jo.wiemers/shop", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
				{FullPath: "jo.wiemers/infra", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
			},
			existing: []aiv1.AgentProject{
				agentProject("team", "my-shop", "Jo.Wiemers/Shop/", nil),
				agentProject("agents", "infra", "jo.wiemers/infra", otherSet()),
			},
			want: map[string]want{
				"jo.wiemers/shop":  {aiv1.ProjectSetExplicit, "", "watched by AgentProject team/my-shop"},
				"jo.wiemers/infra": {aiv1.ProjectSetExplicit, "", "watched by AgentProject agents/infra"},
			},
		},
		{
			name:     "name taken by a foreign object",
			projects: []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}}},
			existing: []aiv1.AgentProject{agentProject("agents", "shop", "elsewhere/shop", nil)},
			want:     map[string]want{"jo.wiemers/shop": {aiv1.ProjectSetNameTaken, "", "name shop is taken by AgentProject agents/shop"}},
		},
		{
			name:      "same name in another namespace is not taken",
			projects:  []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}}},
			existing:  []aiv1.AgentProject{agentProject("team", "shop", "elsewhere/shop", nil)},
			want:      map[string]want{"jo.wiemers/shop": {aiv1.ProjectSetActive, "shop", ""}},
			wantChild: []string{"shop"},
		},
		{
			name: "two paths with the same slug: the first keeps the name",
			projects: []gitlab.DiscoveredProject{
				{FullPath: "jo.wiemers/a/b-c", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
				{FullPath: "jo.wiemers/a-b/c", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
			},
			want: map[string]want{
				"jo.wiemers/a/b-c": {aiv1.ProjectSetActive, "a-b-c", ""},
				"jo.wiemers/a-b/c": {aiv1.ProjectSetNameTaken, "", "taken by the child of jo.wiemers/a/b-c"},
			},
			wantChild: []string{"a-b-c"},
		},
		{
			name:      "inactive child within dormantAfter stays active",
			projects:  []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop"}},
			existing:  []aiv1.AgentProject{agentProject("agents", "shop", "jo.wiemers/shop", set)},
			prev:      []aiv1.ProjectSetProjectStatus{{Path: "jo.wiemers/shop", State: aiv1.ProjectSetActive, Child: "shop", LastActiveTime: ago(167 * time.Hour)}},
			want:      map[string]want{"jo.wiemers/shop": {aiv1.ProjectSetActive, "shop", ""}},
			wantChild: []string{"shop"},
		},
		{
			name:     "inactive child after dormantAfter is dormant with its child, not planned",
			projects: []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop"}},
			existing: []aiv1.AgentProject{agentProject("agents", "shop", "jo.wiemers/shop", set)},
			prev:     []aiv1.ProjectSetProjectStatus{{Path: "jo.wiemers/shop", State: aiv1.ProjectSetActive, Child: "shop", LastActiveTime: ago(168 * time.Hour)}},
			want:     map[string]want{"jo.wiemers/shop": {aiv1.ProjectSetDormant, "shop", "no work for 168h"}},
		},
		{
			name:      "inactive child without a previous entry counts as active now",
			projects:  []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop"}},
			existing:  []aiv1.AgentProject{agentProject("agents", "shop", "jo.wiemers/shop", set)},
			want:      map[string]want{"jo.wiemers/shop": {aiv1.ProjectSetActive, "shop", ""}},
			wantChild: []string{"shop"},
		},
		{
			name: "children out of scope keep their entry and are not planned",
			projects: []gitlab.DiscoveredProject{
				{FullPath: "jo.wiemers/archived/old", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
				{FullPath: "jo.wiemers/dup", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
				{FullPath: "jo.wiemers/frozen", Archived: true},
			},
			existing: []aiv1.AgentProject{
				agentProject("agents", "archived-old", "jo.wiemers/archived/old", set),
				agentProject("agents", "dup", "jo.wiemers/dup", set),
				agentProject("team", "dup", "jo.wiemers/dup", nil),
				agentProject("agents", "frozen", "jo.wiemers/frozen", set),
				agentProject("agents", "vanished", "jo.wiemers/vanished", set),
			},
			want: map[string]want{
				"jo.wiemers/archived/old": {aiv1.ProjectSetExcluded, "archived-old", "matches exclude"},
				"jo.wiemers/dup":          {aiv1.ProjectSetExplicit, "dup", "watched by AgentProject team/dup"},
				"jo.wiemers/frozen":       {aiv1.ProjectSetExcluded, "frozen", "archived"},
				"jo.wiemers/vanished":     {aiv1.ProjectSetDormant, "vanished", "not found in the group"},
			},
		},
		{
			name:     "a child whose project include no longer matches",
			projects: []gitlab.DiscoveredProject{{FullPath: "someone/else"}},
			existing: []aiv1.AgentProject{agentProject("agents", "else", "someone/else", set)},
			want:     map[string]want{"someone/else": {aiv1.ProjectSetExcluded, "else", "not matched by include"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := set.DeepCopy()
			s.Status.Projects = tc.prev
			plan := planProjectSet(projectSetInput{
				Set:        s,
				Discovery:  &gitlab.GroupDiscovery{Projects: tc.projects, ReadyIssues: tc.ready},
				Projects:   tc.existing,
				TaskCounts: tc.tasks,
				Now:        planNow,
			})
			if len(plan.Entries) != len(tc.want) {
				t.Fatalf("entries = %+v, want %d", plan.Entries, len(tc.want))
			}
			for _, e := range plan.Entries {
				w, ok := tc.want[e.Path]
				if !ok {
					t.Fatalf("unexpected entry %+v", e)
				}
				if e.State != w.state || e.Child != w.child || !strings.Contains(e.Reason, w.reason) {
					t.Errorf("%s = %s/%q/%q, want %s/%q/~%q", e.Path, e.State, e.Child, e.Reason, w.state, w.child, w.reason)
				}
			}
			var children []string
			for _, c := range plan.Children {
				children = append(children, c.Name)
			}
			if strings.Join(children, ",") != strings.Join(tc.wantChild, ",") {
				t.Errorf("children = %v, want %v", children, tc.wantChild)
			}
		})
	}
}

func TestProjectSetPlanChildSpec(t *testing.T) {
	set := planSet()
	tmpl := aiv1.AgentProjectSpec{
		MaxConcurrentRuns: 2,
		CrossRepo: &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{
			{Project: "jo.wiemers/infra/cluster", Paths: []string{"apps/"}},
			{Project: "jo.wiemers/archived/old", Paths: []string{"a/"}},
			{Project: "Jo.Wiemers/Archived/Old", Paths: []string{"b/"}},
		}},
	}
	in := projectSetInput{
		Set:      set,
		Template: tmpl,
		Discovery: &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{
			{FullPath: "jo.wiemers/Shop", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
			{FullPath: "jo.wiemers/b", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent spec")}},
			{FullPath: "jo.wiemers/a"},
		}},
		Now: planNow,
	}
	plan := planProjectSet(in)
	if len(plan.Children) != 2 || plan.Children[0].Name != "b" || plan.Children[1].Name != "shop" {
		t.Fatalf("children = %+v", plan.Children)
	}
	c := plan.Children[1]
	want := aiv1.GitLabSpec{URL: "https://gitlab.example", Project: "jo.wiemers/Shop", TokenSecretRef: set.Spec.GitLab.TokenSecretRef}
	if c.Spec.GitLab != want || c.Path != "jo.wiemers/Shop" || c.Spec.MaxConcurrentRuns != 2 {
		t.Errorf("child spec = %+v", c.Spec)
	}
	if got := c.Spec.CrossRepo.Targets; len(got) != 1 || got[0].Project != "jo.wiemers/infra/cluster" {
		t.Errorf("targets = %+v", got)
	}
	if strings.Join(plan.Dropped, ",") != "jo.wiemers/archived/old,Jo.Wiemers/Archived/Old" {
		t.Errorf("dropped = %v", plan.Dropped)
	}
	if len(tmpl.CrossRepo.Targets) != 3 {
		t.Errorf("template was changed: %+v", tmpl.CrossRepo.Targets)
	}
	if len(c.Hash) != 64 || c.Hash == plan.Children[0].Hash {
		t.Errorf("hash = %q, other %q", c.Hash, plan.Children[0].Hash)
	}

	// Entries are sorted by path, counts follow them, LastActiveTime is now
	// for active ones.
	var paths []string
	for _, e := range plan.Entries {
		paths = append(paths, e.Path)
	}
	if strings.Join(paths, ",") != "jo.wiemers/a,jo.wiemers/b,jo.wiemers/Shop" {
		t.Errorf("paths = %v", paths)
	}
	if plan.Counts != (aiv1.ProjectSetCounts{Active: 2, Dormant: 1}) {
		t.Errorf("counts = %+v", plan.Counts)
	}
	if e := plan.Entries[1]; e.LastActiveTime == nil || !e.LastActiveTime.Time.Equal(planNow) {
		t.Errorf("lastActiveTime = %v", e.LastActiveTime)
	}

	// The same input plans the same hash; a changed template changes it.
	if again := planProjectSet(in); again.Children[1].Hash != c.Hash {
		t.Errorf("hash not stable")
	}
	in.Template.MaxConcurrentRuns = 3
	if changed := planProjectSet(in); changed.Children[1].Hash == c.Hash {
		t.Errorf("hash did not change with the template")
	}
}

func TestProjectSetPlanAllTargetsDropped(t *testing.T) {
	set := planSet()
	tmpl := aiv1.AgentProjectSpec{CrossRepo: &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{{Project: "jo.wiemers/archived/x"}}}}
	plan := planProjectSet(projectSetInput{
		Set: set, Template: tmpl, Now: planNow,
		Discovery:  &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop"}}},
		TaskCounts: map[string]int{"shop": 1},
	})
	if len(plan.Children) != 1 || plan.Children[0].Spec.CrossRepo != nil {
		t.Fatalf("children = %+v", plan.Children)
	}
	if len(plan.Dropped) != 1 {
		t.Errorf("dropped = %v", plan.Dropped)
	}
}

func TestProjectSetPlanTemplateSelection(t *testing.T) {
	set := planSet()
	proj := gitlab.DiscoveredProject{FullPath: "jo.wiemers/shop", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/plan this")}}
	cases := []struct {
		name string
		tmpl aiv1.AgentProjectSpec
		want aiv1.ProjectSetState
	}{
		{"default marker", aiv1.AgentProjectSpec{}, aiv1.ProjectSetDormant},
		{"template marker", aiv1.AgentProjectSpec{Milestones: aiv1.MilestoneSelector{SpecMarker: ptr.To("/plan")}}, aiv1.ProjectSetActive},
		{"title pattern misses", aiv1.AgentProjectSpec{Milestones: aiv1.MilestoneSelector{SpecMarker: ptr.To("/plan"), TitlePattern: "^M"}}, aiv1.ProjectSetDormant},
		{"invalid title pattern", aiv1.AgentProjectSpec{Milestones: aiv1.MilestoneSelector{SpecMarker: ptr.To("/plan"), TitlePattern: "("}}, aiv1.ProjectSetDormant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planProjectSet(projectSetInput{
				Set: set, Template: tc.tmpl, Now: planNow,
				Discovery: &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{proj}},
			})
			if got := plan.Entries[0].State; got != tc.want {
				t.Errorf("state = %s, want %s", got, tc.want)
			}
		})
	}
}

// A child whose only milestones settle is idle: its tasks and its spec
// milestone no longer refresh LastActiveTime, a running milestone or a ready
// issue still does.
func TestProjectSetPlanSettlingChildIsIdle(t *testing.T) {
	set := planSet()
	child := func(active, settling int32) aiv1.AgentProject {
		p := agentProject("agents", "shop", "jo.wiemers/shop", set)
		p.Status.ActiveMilestones, p.Status.SettlingMilestones = active, settling
		return p
	}
	cases := []struct {
		name       string
		child      aiv1.AgentProject
		ready      bool
		since      time.Duration // LastActiveTime of the previous entry
		wantState  aiv1.ProjectSetState
		wantActive *metav1.Time
	}{
		{"one settling milestone", child(0, 1), false, time.Hour, aiv1.ProjectSetActive, ago(time.Hour)},
		{"settling past dormantAfter", child(0, 1), false, 168 * time.Hour, aiv1.ProjectSetDormant, ago(168 * time.Hour)},
		{"a running milestone next to a settling one", child(1, 1), false, time.Hour, aiv1.ProjectSetActive, ago(0)},
		{"settling with a ready issue", child(0, 1), true, time.Hour, aiv1.ProjectSetActive, ago(0)},
		{"no settling milestone", child(0, 0), false, time.Hour, aiv1.ProjectSetActive, ago(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := set.DeepCopy()
			s.Status.Projects = []aiv1.ProjectSetProjectStatus{{Path: "jo.wiemers/shop", State: aiv1.ProjectSetActive, Child: "shop", LastActiveTime: ago(tc.since)}}
			var ready []gitlab.DiscoveredIssue
			if tc.ready {
				ready = []gitlab.DiscoveredIssue{{Project: "jo.wiemers/shop", IID: 7}}
			}
			plan := planProjectSet(projectSetInput{
				Set: s,
				Discovery: &gitlab.GroupDiscovery{
					Projects:    []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/shop", Milestones: []gitlab.DiscoveredMilestone{specMilestone("/agent autopilot")}}},
					ReadyIssues: ready,
				},
				Projects:   []aiv1.AgentProject{tc.child},
				TaskCounts: map[string]int{"shop": 4},
				Now:        planNow,
			})
			if len(plan.Entries) != 1 {
				t.Fatalf("entries = %+v", plan.Entries)
			}
			e := plan.Entries[0]
			if e.State != tc.wantState || e.Child != "shop" || !e.LastActiveTime.Equal(tc.wantActive) {
				t.Errorf("entry = %s/%q, last active %v, want %s/shop, %v", e.State, e.Child, e.LastActiveTime, tc.wantState, tc.wantActive)
			}
		})
	}
}

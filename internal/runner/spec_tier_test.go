package runner

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

func tierLabels(labels []string) (out []string) {
	for _, l := range labels {
		if strings.HasPrefix(l, "agent-profile::") || strings.HasPrefix(l, "agent-effort::") || strings.HasPrefix(l, "agent-model::") {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

func TestApplyPlanPicksOneTier(t *testing.T) {
	gl := gitlabfake.New()
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2"}
	gl.Milestones = []gitlab.Milestone{m}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	ctx := context.Background()
	plan := SpecPlan{Issues: []PlannedItem{
		{Key: "a", Title: "A", Description: "x", Profile: "strong", ProfileWhy: "crosses layers\nsecond line"},
		{Key: "b", Title: "B", Description: "y", Profile: "strong", ProfileWhy: "changes a CRD"},
		{Key: "c", Title: "C", Description: "z", Profile: "unknown"},
	}}
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, nil, planComment{botID: gl.Bot.ID, runID: "s#1"}); err != nil {
		t.Fatal(err)
	}
	a := gl.Issues[1]
	if got := tierLabels(a.Labels); !slices.Equal(got, []string{"agent-profile::strong"}) {
		t.Fatalf("exactly one profile label, no effort label: %v", a.Labels)
	}
	if !strings.Contains(a.Description, "\n\nTier: **strong** (claude-opus-5-5, effort medium): crosses layers\n") {
		t.Fatalf("tier line missing:\n%s", a.Description)
	}
	if got, _ := plannedLabels(a.Description); !slices.Equal(got, []string{"agent-profile::strong"}) {
		t.Fatalf("the marker holds the profile label only: %v", got)
	}
	if c := gl.Issues[3]; len(tierLabels(c.Labels)) != 0 || strings.Contains(c.Description, "Tier:") {
		t.Fatalf("an unknown profile gets no label and no tier line: %v\n%s", c.Labels, c.Description)
	}
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	got := planOrder(spec, plan, issues)
	if got[0].Profile != "strong" || got[0].ProfileWhy != "crosses layers" || got[2].Profile != "" || got[2].ProfileWhy != "" {
		t.Fatalf("planned issues: %+v", got)
	}

	// Re-plan: a's labels are an older plan's own and move to the new tier;
	// b carries a human's profile and effort, which stay.
	gl.SetLabels(1, "agent::draft", "ai-generated", "agent-profile::strong", "agent-effort::high")
	gl.Issues[1].Description = "x" + plannedMarker
	gl.SetLabels(2, "agent::draft", "ai-generated", "agent-profile::deep", "agent-effort::low", "agent-model::claude/sonnet")
	issues, _ = gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	plan.Issues[0].ExistingIID, plan.Issues[0].Profile = 1, "balanced"
	plan.Issues[1].ExistingIID, plan.Issues[1].Profile = 2, "balanced"
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, issues, planComment{botID: gl.Bot.ID, runID: "s#2"}); err != nil {
		t.Fatal(err)
	}
	if got := tierLabels(gl.Labels(1)); !slices.Equal(got, []string{"agent-profile::balanced"}) {
		t.Fatalf("the plan's own labels move to the new tier: %v", got)
	}
	if got := tierLabels(gl.Labels(2)); !slices.Equal(got, []string{"agent-effort::low", "agent-model::claude/sonnet", "agent-profile::deep"}) {
		t.Fatalf("a human's labels stay, no profile is added: %v", got)
	}
}

func TestTierLabelChange(t *testing.T) {
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	item := PlannedItem{Profile: "strong"}
	for _, tc := range []struct {
		name, desc  string
		labels      []string
		add, remove []string
	}{
		{"no marker keeps a profile", "x", []string{"agent-profile::fast", "agent-effort::high"}, nil, nil},
		{"no marker, no profile", "x", []string{"agent-effort::high"}, []string{"agent-profile::strong"}, nil},
		{"own labels, same pick", plannedMarker, []string{"agent-profile::strong", "agent-effort::high"}, nil, []string{"agent-effort::high"}},
		{"own profile, no label", plannedMarker, nil, []string{"agent-profile::strong"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			add, remove := tierLabelChange(spec, &gitlab.Issue{Description: tc.desc, Labels: tc.labels}, item)
			if !slices.Equal(add, tc.add) || !slices.Equal(remove, tc.remove) {
				t.Fatalf("add %v remove %v", add, remove)
			}
		})
	}
}

func TestTierLineAndPromptOnCursor(t *testing.T) {
	spec := aiv1.AgentProjectSpec{}
	spec.Agents.Profiles = []aiv1.AgentProfile{
		{Name: "fast", Description: "simple", RoleSpec: aiv1.RoleSpec{Engine: aiv1.EngineCursor, Model: "auto"}},
		{Name: "balanced", Description: "more", RoleSpec: aiv1.RoleSpec{Model: "claude-sonnet-5-5", Effort: "high"}},
	}
	spec = spec.WithDefaults()
	desc, _ := renderIssue(spec, PlannedItem{Key: "k", Description: "Body\nTier: **deep** (x)\n**Tier:** deep", Profile: "fast"}, nil)
	if !strings.Contains(desc, "\n\nTier: **fast** (cursor, auto)\n") || strings.Count(desc, "Tier:") != 1 {
		t.Fatalf("cursor tier line without effort or reason, the planner's tier lines stripped:\n%s", desc)
	}
	prompt := buildSpecPrompt(&gitlab.Milestone{Title: "M"}, nil, spec.Agents.Profiles, 0, false)
	if !strings.Contains(prompt, "- fast (cursor, auto): simple\n") || !strings.Contains(prompt, "- balanced (claude-sonnet-5-5, effort high): more\n") {
		t.Fatalf("profile list:\n%s", prompt)
	}
}

func TestSpecSchemaTier(t *testing.T) {
	var v struct {
		Properties struct {
			Issues struct {
				Items struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				} `json:"items"`
			} `json:"issues"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(specSchema(aiv1.DefaultProfiles())), &v); err != nil {
		t.Fatal(err)
	}
	items := v.Properties.Issues.Items
	if _, ok := items.Properties["effort"]; ok {
		t.Fatal("the plan picks no effort")
	}
	if _, ok := items.Properties["profileWhy"]; !ok || !slices.Contains(items.Required, "profileWhy") {
		t.Fatalf("profileWhy is required: %v", items.Required)
	}
	if strings.Contains(specSystemPrompt, "effort") {
		t.Fatal("the planner's prompt mentions no effort")
	}
}

package runner

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

func TestApplyPlanSetsEffort(t *testing.T) {
	gl := gitlabfake.New()
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2"}
	gl.Milestones = []gitlab.Milestone{m}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	ctx := context.Background()
	plan := SpecPlan{Issues: []PlannedItem{
		{Key: "ui", Title: "UI", Description: "x", Profile: "fast", Effort: "low"},
		{Key: "core", Title: "Core", Description: "y", Profile: "strong", Effort: "max"},
		{Key: "docs", Title: "Docs", Description: "z", Profile: "fast"},
	}}
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, plan, nil, planComment{botID: gl.Bot.ID, runID: "s#1"}); err != nil {
		t.Fatal(err)
	}
	ui, core, docs := gl.Issues[1], gl.Issues[2], gl.Issues[3]
	if !ui.HasLabel("agent-effort::low") || !core.HasLabel("agent-effort::high") || slices.ContainsFunc(docs.Labels, func(l string) bool { return strings.HasPrefix(l, "agent-effort::") }) {
		t.Fatalf("effort labels (capped at high, none without a choice): %v / %v / %v", ui.Labels, core.Labels, docs.Labels)
	}
	// A human's effort label survives a re-plan.
	gl.SetLabels(1, "agent::draft", "ai-generated", "agent-profile::fast", "agent-effort::medium")
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	plan.Issues[0].ExistingIID = 1
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, plan, issues, planComment{botID: gl.Bot.ID, runID: "s#2"}); err != nil {
		t.Fatal(err)
	}
	if l := gl.Labels(1); !slices.Contains(l, "agent-effort::medium") || slices.Contains(l, "agent-effort::low") {
		t.Fatalf("the human's effort must stay: %v", l)
	}
}

func TestImplementerUsesTheIssuesModelAndEffort(t *testing.T) {
	e := newImplementEnv(t)
	e.gl.SetLabels(42, "agent::working", "agent-model::claude/sonnet", "agent-effort::low")
	e.gl.AddNote(42, gitlab.User{ID: 1, Username: "jonathan"}, "/agent use claude sonnet low")
	engine := &scriptedEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	impl := engine.calls[0]
	if impl.ReadOnly || impl.Model != "claude-sonnet-5-5" || impl.Effort != "low" {
		t.Fatalf("implementer ran with %s/%s", impl.Model, impl.Effort)
	}
	if strings.Contains(impl.Prompt, "/agent use") {
		t.Fatalf("a pure model command is no input for the agent:\n%s", impl.Prompt)
	}
}

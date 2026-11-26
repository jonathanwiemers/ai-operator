package v1alpha1

import (
	"reflect"
	"testing"
)

func TestRoleEffortDefaults(t *testing.T) {
	spec := AgentProjectSpec{}.WithDefaults()
	for _, c := range []struct {
		name          string
		role          RoleSpec
		model, effort string
	}{
		{"spec", spec.Agents.Spec, DefaultModel, "high"},
		{"implement", spec.Agents.Implement.RoleSpec, DefaultModel, "medium"},
		{"review", spec.Agents.Review.RoleSpec, DefaultLightModel, "medium"},
		{"pilot", spec.Agents.Pilot, DefaultLightModel, "medium"},
		{"describe", spec.Agents.Describe.RoleSpec, DefaultLightModel, "low"},
	} {
		if c.role.Engine != EngineClaude || c.role.Model != c.model || c.role.Effort != c.effort {
			t.Errorf("%s = %s/%s·%s, want claude/%s·%s", c.name, c.role.Engine, c.role.Model, c.role.Effort, c.model, c.effort)
		}
	}
	if spec.Agents.Implement.Conflict != nil {
		t.Errorf("conflict role must stay unset: %+v", spec.Agents.Implement.Conflict)
	}
}

func TestDefaultProfilesTiers(t *testing.T) {
	want := []AgentProfile{
		{Name: "fast", Description: "Follows an existing pattern, touches a few files, adds no new API, type or migration, leaves no decision open (docs, config, tests of existing code, small UI changes).",
			RoleSpec: RoleSpec{Engine: EngineClaude, Model: "claude-sonnet-5-5", Effort: "medium"}},
		{Name: "balanced", Description: "New logic in one package or one area of the UI, with a clear spec and some edge cases.",
			RoleSpec: RoleSpec{Engine: EngineClaude, Model: "claude-sonnet-5-5", Effort: "high"}},
		{Name: "strong", Description: "Crosses layers (for example API, controller and Desk), changes a CRD or the store schema, or needs design within the issue.",
			RoleSpec: RoleSpec{Engine: EngineClaude, Model: "claude-opus-5-5", Effort: "medium"}},
		{Name: "deep", Description: "Concurrency, reconcile or state-machine logic, security, spend accounting, stream parsing, changes that can lose data, or work whose earlier attempt failed.",
			RoleSpec: RoleSpec{Engine: EngineClaude, Model: "claude-opus-5-5", Effort: "high"}},
	}
	if got := DefaultProfiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultProfiles() = %+v", got)
	}
	if got := (AgentProjectSpec{}).WithDefaults().Agents.Profiles; !reflect.DeepEqual(got, want) {
		t.Fatalf("defaulted profiles = %+v", got)
	}
}

func TestRoleEffortOverrides(t *testing.T) {
	spec := AgentProjectSpec{Agents: AgentsSpec{
		Spec:     RoleSpec{Effort: "max"},
		Review:   ReviewSpec{RoleSpec: RoleSpec{Effort: "low"}},
		Describe: DescribeSpec{RoleSpec: RoleSpec{Engine: EngineCursor}},
		Pilot:    RoleSpec{Engine: EngineCursor},
		Implement: ImplementSpec{RoleSpec: RoleSpec{
			Fallback: &RoleFallback{Engine: EngineCursor, Model: "auto"},
		}},
		Profiles: []AgentProfile{
			{Name: "own"},
			{Name: "cursor", RoleSpec: RoleSpec{Engine: EngineCursor, Model: "auto"}},
			{Name: "set", RoleSpec: RoleSpec{Effort: "xhigh"}},
		},
	}}.WithDefaults()
	if e := spec.Agents.Spec.Effort; e != "max" {
		t.Errorf("spec's own effort must win, got %q", e)
	}
	if e := spec.Agents.Review.Effort; e != "low" {
		t.Errorf("review's own effort must win, got %q", e)
	}
	if e := spec.Agents.Describe.Effort; e != "" {
		t.Errorf("cursor describer must get no effort, got %q", e)
	}
	if e := spec.Agents.Pilot.Effort; e != "" {
		t.Errorf("cursor pilot must get no effort, got %q", e)
	}
	if f := spec.Agents.Implement.Fallback; f.Effort != "" {
		t.Errorf("fallback's effort must stay its own, got %+v", f)
	}
	for name, effort := range map[string]string{"own": "medium", "cursor": "", "set": "xhigh"} {
		p, _ := spec.Profile(name)
		if p.Effort != effort {
			t.Errorf("profile %s effort = %q, want %q", name, p.Effort, effort)
		}
	}

	cursorReview := AgentProjectSpec{Agents: AgentsSpec{Review: ReviewSpec{RoleSpec: RoleSpec{Engine: EngineCursor}}}}.WithDefaults()
	if r := cursorReview.Agents.Review; r.Effort != "" || r.Model != "" {
		t.Errorf("cursor reviewer must get no model or effort: %+v", r.RoleSpec)
	}
}

func TestIssueRoleTiers(t *testing.T) {
	spec := AgentProjectSpec{}.WithDefaults()
	role, profile, _ := spec.IssueRole([]string{"agent-profile::balanced"})
	if profile != "balanced" || role.Model != "claude-sonnet-5-5" || role.Effort != "high" {
		t.Errorf("balanced = %s %s·%s", profile, role.Model, role.Effort)
	}
	role, profile, _ = spec.IssueRole(nil)
	if profile != "" || role.Model != DefaultModel || role.Effort != "medium" {
		t.Errorf("no label = %q %s·%s", profile, role.Model, role.Effort)
	}
	for _, name := range []string{"fast", "strong"} {
		if _, p, _ := spec.IssueRole([]string{"agent-profile::" + name}); p != name {
			t.Errorf("agent-profile::%s must stay valid, got %q", name, p)
		}
	}
}

func TestConflictRoleUnchangedByEffortDefaults(t *testing.T) {
	spec := AgentProjectSpec{Agents: AgentsSpec{Implement: ImplementSpec{
		RoleSpec: RoleSpec{Engine: EngineCursor, Model: "auto"},
		Conflict: &ConflictRoleSpec{RoleSpec: RoleSpec{Engine: EngineClaude}},
	}}}.WithDefaults()
	impl, _, _ := spec.IssueRole(nil)
	if impl.Effort != "" {
		t.Fatalf("cursor implementer must get no effort: %+v", impl)
	}
	role, _, ok := spec.ConflictRole(impl)
	if !ok || role.Engine != EngineClaude || role.Model != DefaultModel || role.Effort != "" {
		t.Fatalf("conflict role = %+v, want claude/%s without effort", role, DefaultModel)
	}
}

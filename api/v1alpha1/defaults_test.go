package v1alpha1

import "testing"

func TestRoleDefaults(t *testing.T) {
	spec := AgentProjectSpec{}.WithDefaults()
	if spec.Agents.Spec.Model != DefaultModel || spec.Agents.Implement.Model != DefaultModel {
		t.Fatalf("spec and implement must default to the strong model: %+v", spec.Agents)
	}
	if spec.Agents.Review.Model != DefaultLightModel {
		t.Fatalf("review must default to the light model, got %q", spec.Agents.Review.Model)
	}
	fast, ok := spec.Profile("fast")
	strong, ok2 := spec.Profile("strong")
	if !ok || !ok2 || fast.Model != DefaultLightModel || strong.Model != DefaultModel {
		t.Fatalf("unexpected default profiles %+v", spec.Agents.Profiles)
	}
}

func TestImplementRoleRouting(t *testing.T) {
	spec := AgentProjectSpec{Agents: AgentsSpec{Profiles: []AgentProfile{
		{Name: "fast", RoleSpec: RoleSpec{Engine: EngineCursor, Model: "auto"}},
		{Name: "strong"},
	}}}.WithDefaults()

	role, name := spec.ImplementRole([]string{"agent::ready", "agent-profile::fast"})
	if name != "fast" || role.Engine != EngineCursor || role.Model != "auto" {
		t.Fatalf("fast issue routed to %s %+v", name, role)
	}
	role, name = spec.ImplementRole([]string{"agent-profile::strong"})
	if name != "strong" || role.Engine != EngineClaude || role.Model != DefaultModel {
		t.Fatalf("strong profile must get claude defaults, got %s %+v", name, role)
	}
	for _, labels := range [][]string{{"agent-profile::unknown"}, {"agent::ready"}, nil} {
		if role, name := spec.ImplementRole(labels); name != "" || role.Model != DefaultModel {
			t.Fatalf("labels %v must fall back to agents.implement, got %s %+v", labels, name, role)
		}
	}
	if got := spec.ProfileLabel("fast"); got != "agent-profile::fast" {
		t.Fatalf("ProfileLabel = %q", got)
	}

	// Cursor reviews get no claude model.
	custom := AgentProjectSpec{Agents: AgentsSpec{Review: ReviewSpec{RoleSpec: RoleSpec{Engine: EngineCursor}}}}.WithDefaults()
	if r := custom.Agents.Review; r.Model != "" {
		t.Fatalf("cursor review must not get a claude model: %+v", r.RoleSpec)
	}
}

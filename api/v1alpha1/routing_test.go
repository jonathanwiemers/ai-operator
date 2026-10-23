package v1alpha1

import (
	"strings"
	"testing"
)

func routingSpec() AgentProjectSpec {
	s := AgentProjectSpec{}
	s.Agents.Profiles = []AgentProfile{
		{Name: "fast", RoleSpec: RoleSpec{Engine: EngineCursor, Model: "auto"}},
		{Name: "strong", RoleSpec: RoleSpec{Engine: EngineClaude, Model: "claude-opus-5-5", Effort: "medium"}},
	}
	return s.WithDefaults()
}

func TestIssueRole(t *testing.T) {
	s := routingSpec()
	for _, tc := range []struct {
		labels             []string
		engine, model, eff string
		note               string
	}{
		{[]string{"agent-profile::strong"}, "claude", "claude-opus-5-5", "medium", ""},
		{[]string{"agent-profile::fast", "agent-model::claude/sonnet", "agent-effort::low"}, "claude", "claude-sonnet-5-5", "low", ""},
		{[]string{"agent-profile::strong", "agent-model::cursor/auto", "agent-effort::high"}, "cursor", "auto", "medium", "no effort setting"},
		{[]string{"agent-profile::strong", "agent-effort::max"}, "claude", "claude-opus-5-5", "high", "above the project's maximum"},
		{[]string{"agent-profile::strong", "agent-model::claude/gpt-9"}, "claude", "claude-opus-5-5", "medium", "names no allowed model"},
		{[]string{"agent-effort::lots"}, "claude", DefaultModel, "", "no effort level"},
	} {
		role, _, notes := s.IssueRole(tc.labels)
		if string(role.Engine) != tc.engine || role.Model != tc.model || role.Effort != tc.eff {
			t.Errorf("%v: got %s/%s/%s", tc.labels, role.Engine, role.Model, role.Effort)
		}
		if got := strings.Join(notes, " "); (tc.note == "") != (got == "") || !strings.Contains(got, tc.note) {
			t.Errorf("%v: notes %q, want %q", tc.labels, got, tc.note)
		}
	}
}

func TestAllowedModels(t *testing.T) {
	s := routingSpec()
	if _, ok := s.FindModel(EngineClaude, "opus"); !ok {
		t.Error("the default list has opus")
	}
	if _, ok := s.FindModel(EngineCursor, "auto"); !ok {
		t.Error("a profile's model is allowed")
	}
	if _, ok := s.FindModel(EngineCursor, "opus"); ok {
		t.Error("an alias belongs to its engine")
	}
	// The implement role's model is listed once, though defaults and
	// profiles name it too.
	n := 0
	for _, o := range s.AllowedModels() {
		if o.Model == "claude-opus-5-5" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("claude-opus-5-5 listed %d times", n)
	}
}

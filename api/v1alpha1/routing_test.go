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

func TestFallbackRole(t *testing.T) {
	s := routingSpec()
	fast := RoleSpec{Engine: EngineCursor, Model: "auto", Instructions: "house rules",
		Fallback: &RoleFallback{Engine: EngineClaude, Model: "sonnet", Effort: "low"}}
	got, err := s.FallbackRole(fast)
	if err != nil || got.Engine != EngineClaude || got.Model != "claude-sonnet-5-5" || got.Effort != "low" ||
		got.Instructions != "house rules" || got.Fallback != nil {
		t.Fatalf("expected claude/claude-sonnet-5-5 at low effort with the role's instructions, got %+v %v", got, err)
	}
	// Effort is claude only.
	strong := RoleSpec{Engine: EngineClaude, Model: "claude-opus-5-5", Effort: "high", Fallback: &RoleFallback{Engine: EngineCursor, Model: "auto", Effort: "max"}}
	if got, err := s.FallbackRole(strong); err != nil || got.Engine != EngineCursor || got.Model != "auto" || got.Effort != "" {
		t.Fatalf("expected cursor/auto without effort, got %+v %v", got, err)
	}
	for _, fb := range []*RoleFallback{nil, {Engine: EngineCursor, Model: "auto"}, {Engine: EngineClaude, Model: "gpt-9"}} {
		if _, err := s.FallbackRole(RoleSpec{Engine: EngineCursor, Model: "auto", Fallback: fb}); err == nil {
			t.Errorf("fallback %+v must be refused", fb)
		}
	}
}

func TestFallbackErrors(t *testing.T) {
	s := routingSpec()
	if errs := s.FallbackErrors(); len(errs) != 0 {
		t.Fatalf("no fallbacks, no errors: %v", errs)
	}
	s.Agents.Review.Engine, s.Agents.Review.Model = EngineCursor, "auto"
	s.Agents.Review.Fallback = &RoleFallback{Engine: EngineClaude, Model: "sonnet"}
	s.Agents.Profiles[0].Fallback = &RoleFallback{Engine: EngineClaude, Model: "gpt-9"}
	s.Agents.Pilot.Fallback = &RoleFallback{Engine: EngineClaude, Model: "opus"}
	errs := s.FallbackErrors()
	if len(errs) != 2 || !strings.HasPrefix(errs[0], "pilot: ") || !strings.Contains(errs[0], "own engine") ||
		!strings.HasPrefix(errs[1], "profile fast: ") || !strings.Contains(errs[1], "not an allowed model") {
		t.Fatalf("expected the pilot's and the fast profile's fallbacks, got %v", errs)
	}
}

func TestCompanionRole(t *testing.T) {
	s := routingSpec()
	claudeFB := &RoleFallback{Engine: EngineClaude, Model: "sonnet", Effort: "low"}
	cursorFB := &RoleFallback{Engine: EngineCursor, Model: "auto"}

	// A claude role stays, without a cursor fallback.
	got, via, err := s.CompanionRole(RoleSpec{Engine: EngineClaude, Model: "claude-opus-5-5", Fallback: cursorFB})
	if err != nil || via != CompanionRoleOwn || got.Engine != EngineClaude || got.Fallback != nil {
		t.Fatalf("claude stays without its cursor fallback: %+v %q %v", got, via, err)
	}
	if got, _, _ := s.CompanionRole(RoleSpec{Engine: EngineClaude, Model: "claude-opus-5-5", Fallback: claudeFB}); got.Fallback == nil {
		t.Error("a claude fallback stays")
	}

	// Cursor with a claude fallback: the fallback.
	got, via, err = s.CompanionRole(RoleSpec{Engine: EngineCursor, Model: "auto", Fallback: claudeFB})
	if err != nil || via != CompanionRoleFallback || got.Engine != EngineClaude || got.Model != "claude-sonnet-5-5" || got.Effort != "low" {
		t.Fatalf("expected the claude fallback, got %+v %q %v", got, via, err)
	}

	// Cursor without a usable claude fallback: agents.implement.
	s.Agents.Implement.RoleSpec = RoleSpec{Engine: EngineClaude, Model: "claude-opus-5-5", Effort: "high", Fallback: cursorFB}
	for _, fb := range []*RoleFallback{nil, cursorFB, {Engine: EngineClaude, Model: "gpt-9"}} {
		got, via, err = s.CompanionRole(RoleSpec{Engine: EngineCursor, Model: "auto", Fallback: fb})
		if err != nil || via != CompanionRoleImplement || got.Engine != EngineClaude || got.Effort != "high" || got.Fallback != nil {
			t.Errorf("fallback %+v: expected agents.implement without its cursor fallback, got %+v %q %v", fb, got, via, err)
		}
	}

	// No claude anywhere: an error naming the engine.
	s.Agents.Implement.RoleSpec = RoleSpec{Engine: EngineCursor, Model: "auto", Fallback: cursorFB}
	if _, _, err := s.CompanionRole(RoleSpec{Engine: EngineCursor, Model: "gpt-5", Fallback: cursorFB}); err == nil ||
		!strings.Contains(err.Error(), "cursor/gpt-5") || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("expected an error naming cursor/gpt-5 and claude, got %v", err)
	}
}

func TestConflictRole(t *testing.T) {
	s := routingSpec()
	impl := RoleSpec{Engine: EngineClaude, Model: "claude-opus-5-5", Effort: "high", Instructions: "house rules",
		Fallback: &RoleFallback{Engine: EngineCursor, Model: "auto"}}

	// Unset: the implementer, in its own session.
	if got, fresh, ok := s.ConflictRole(impl); ok || fresh || got.Model != "claude-opus-5-5" || got.Effort != "high" {
		t.Fatalf("without a conflict role the implementer resolves: %+v fresh=%v ok=%v", got, fresh, ok)
	}

	// The model alone: the rest is the implementer's, in a fresh session.
	s.Agents.Implement.Conflict = &ConflictRoleSpec{RoleSpec: RoleSpec{Model: "claude-sonnet-5-5"}}
	s = s.WithDefaults()
	got, fresh, ok := s.ConflictRole(impl)
	if !ok || !fresh || got.Engine != EngineClaude || got.Model != "claude-sonnet-5-5" || got.Effort != "high" ||
		got.Instructions != "house rules" || got.Fallback == nil || got.Fallback.Engine != EngineCursor {
		t.Fatalf("expected sonnet over the implementer, fresh: %+v fresh=%v ok=%v", got, fresh, ok)
	}

	// Set fields win; freshSession false resumes.
	no := false
	s.Agents.Implement.Conflict = &ConflictRoleSpec{FreshSession: &no, RoleSpec: RoleSpec{Engine: EngineClaude, Model: "claude-haiku-5-5",
		Effort: "low", MaxBudgetUSD: "1", Instructions: "merge only", Fallback: &RoleFallback{Engine: EngineCursor, Model: "gpt-5"}}}
	got, fresh, _ = s.ConflictRole(impl)
	if fresh || got.Model != "claude-haiku-5-5" || got.Effort != "low" || got.MaxBudgetUSD != "1" || got.Instructions != "merge only" || got.Fallback.Model != "gpt-5" {
		t.Fatalf("the conflict role's fields win: %+v fresh=%v", got, fresh)
	}

	// Another engine: the implementer's model, effort and same-engine
	// fallback are not inherited; claude gets the default model.
	cursorImpl := RoleSpec{Engine: EngineCursor, Model: "auto", Fallback: &RoleFallback{Engine: EngineClaude, Model: "sonnet"}}
	s.Agents.Implement.Conflict = &ConflictRoleSpec{RoleSpec: RoleSpec{Engine: EngineClaude, Effort: "low"}}
	if got, _, _ := s.ConflictRole(cursorImpl); got.Engine != EngineClaude || got.Model != DefaultModel || got.Effort != "low" || got.Fallback != nil {
		t.Fatalf("expected claude with the default model and no claude fallback: %+v", got)
	}
	s.Agents.Implement.Conflict = &ConflictRoleSpec{RoleSpec: RoleSpec{Engine: EngineCursor}}
	if got, _, _ := s.ConflictRole(impl); got.Engine != EngineCursor || got.Model != "" || got.Effort != "" || got.Fallback != nil {
		t.Fatalf("expected cursor with its own default model, no effort, no cursor fallback: %+v", got)
	}
}

func TestConflictFallbackErrors(t *testing.T) {
	s := routingSpec()
	s.Agents.Implement.Conflict = &ConflictRoleSpec{RoleSpec: RoleSpec{Engine: EngineClaude, Model: "claude-sonnet-5-5",
		Fallback: &RoleFallback{Engine: EngineClaude, Model: "haiku"}}}
	if errs := s.FallbackErrors(); len(errs) != 1 || !strings.HasPrefix(errs[0], "implement.conflict: ") {
		t.Fatalf("expected the conflict role's fallback on its own engine, got %v", errs)
	}
}

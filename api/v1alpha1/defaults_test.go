package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func TestLifecycleDefaults(t *testing.T) {
	l := LifecycleSpec{}.WithDefaults()
	if l.IdleTimeout.Duration != DefaultIdleTimeout {
		t.Fatalf("IdleTimeout = %v", l.IdleTimeout)
	}
	if l.ChatIdleTimeout.Duration != DefaultChatIdleTimeout {
		t.Fatalf("ChatIdleTimeout = %v", l.ChatIdleTimeout)
	}
}

func TestUsageLimitBackoff(t *testing.T) {
	l := LifecycleSpec{}.WithDefaults()
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		if got := l.Backoff(int32(i + 1)); got != w {
			t.Errorf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
	now := time.Now()
	s := AgentProjectStatus{UsageLimits: []UsageLimit{{Engine: EngineClaude, Until: metav1.NewTime(now.Add(time.Minute))}}}
	if _, ok := s.ActiveUsageLimit(EngineClaude, now); !ok {
		t.Error("claude limit should be active")
	}
	if _, ok := s.ActiveUsageLimit(EngineClaude, now.Add(2*time.Minute)); ok {
		t.Error("expired limits are inactive")
	}
	if _, ok := s.ActiveUsageLimit(EngineCursor, now); ok {
		t.Error("limits are per engine")
	}
}

func TestFullestWindow(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *metav1.Time { return &metav1.Time{Time: now.Add(d)} }
	s := AgentProjectStatus{EngineUsage: []EngineUsage{
		{Engine: EngineClaude, Windows: []UsageWindow{
			{Name: "five_hour", Percent: 95, ResetsAt: at(-time.Minute)}, // reset since
			{Name: "seven_day", Percent: 82, ResetsAt: at(72 * time.Hour)},
			{Name: "seven_day_opus", Percent: 82, ResetsAt: at(24 * time.Hour)},
			{Name: "unknown_reset", Percent: 99},
		}},
		{Engine: EngineCursor, Windows: []UsageWindow{{Name: "monthly", Percent: 90, ResetsAt: at(time.Hour)}}},
	}}
	if w, ok := s.FullestWindow(EngineClaude, now); !ok || w.Name != "seven_day" {
		t.Errorf("claude: %+v %v, want seven_day (the first of the fullest not reset)", w, ok)
	}
	if w, ok := s.FullestWindow(EngineCursor, now); !ok || w.Percent != 90 {
		t.Errorf("cursor: %+v %v", w, ok)
	}
	if _, ok := s.FullestWindow(EngineClaude, now.Add(100*time.Hour)); ok {
		t.Error("every window has reset")
	}
	if _, ok := (AgentProjectStatus{}).FullestWindow(EngineClaude, now); ok {
		t.Error("no readings")
	}
}

func TestMergeEngineUsage(t *testing.T) {
	at := func(min int) metav1.Time { return metav1.NewTime(time.Date(2026, 10, 28, 12, min, 0, 0, time.UTC)) }
	list := MergeEngineUsage(nil, EngineUsage{Engine: EngineCursor, ObservedAt: at(1)})
	list = MergeEngineUsage(list, EngineUsage{Engine: EngineClaude, ObservedAt: at(2), Status: "allowed"})
	if len(list) != 2 || list[0].Engine != EngineClaude || list[1].Engine != EngineCursor {
		t.Fatalf("one reading per engine, sorted by engine: %+v", list)
	}
	if got := MergeEngineUsage(list, EngineUsage{Engine: EngineClaude, ObservedAt: at(1), Status: "rejected"}); got[0].Status != "allowed" {
		t.Fatalf("an older reading must not replace a newer one: %+v", got)
	}
	if got := MergeEngineUsage(list, EngineUsage{Engine: EngineClaude, ObservedAt: at(3), Status: "rejected"}); got[0].Status != "rejected" || list[0].Status != "allowed" {
		t.Fatalf("a newer reading replaces the old one in a copy: %+v / %+v", got, list)
	}
}

func TestWebhookDefaults(t *testing.T) {
	w := AgentProjectSpec{}.WithDefaults().Webhook
	if w.FallbackInterval.Duration != 10*time.Minute || w.SilenceThreshold.Duration != 30*time.Minute {
		t.Fatalf("webhook defaults: %+v", w)
	}
	custom := AgentProjectSpec{Webhook: WebhookSpec{FallbackInterval: metav1.Duration{Duration: time.Minute}}}.WithDefaults().Webhook
	if custom.FallbackInterval.Duration != time.Minute {
		t.Fatalf("a set fallbackInterval is kept: %+v", custom)
	}
}

func TestCheckpointDefaults(t *testing.T) {
	c := AgentProjectSpec{}.WithDefaults().Checkpoint
	if c.Disabled || c.MaxFileBytes == nil || *c.MaxFileBytes != 5<<20 {
		t.Fatalf("checkpoints are on with 5 MiB files: %+v", c)
	}
	n := int64(100)
	if got := (AgentProjectSpec{Checkpoint: CheckpointSpec{MaxFileBytes: &n}}).WithDefaults().Checkpoint.MaxFileBytes; *got != 100 {
		t.Fatalf("a set maxFileBytes is kept: %d", *got)
	}
}

func TestWorkflowTakes(t *testing.T) {
	for _, c := range []struct {
		inputs       []InputSource
		gitlab, desk bool
	}{
		{nil, true, true},
		{[]InputSource{InputDesk}, false, true},
		{[]InputSource{InputGitLab}, true, false},
		{[]InputSource{InputGitLab, InputDesk}, true, true},
	} {
		w := WorkflowSpec{Inputs: c.inputs}
		if w.Takes(InputGitLab) != c.gitlab || w.Takes(InputDesk) != c.desk {
			t.Errorf("inputs %v: gitlab=%v desk=%v", c.inputs, w.Takes(InputGitLab), w.Takes(InputDesk))
		}
	}
}

func TestReviewMaxInlineDiff(t *testing.T) {
	n := int32(50_000)
	r := ReviewSpec{ReviewerSettings: []ReviewerSettings{{Name: ReviewerBugbot, MaxInlineDiff: &n}, {Name: ReviewerSecurity}}}
	if got := r.MaxInlineDiff(ReviewerBugbot); got != 50_000 {
		t.Fatalf("bugbot = %d", got)
	}
	if got := r.MaxInlineDiff(ReviewerSecurity); got != DefaultMaxInlineDiff {
		t.Fatalf("security = %d", got)
	}
	if got := (ReviewSpec{}).MaxInlineDiff(ReviewerBugbot); got != 150_000 {
		t.Fatalf("default = %d", got)
	}
}

func TestConventionalCommitsDefaults(t *testing.T) {
	w := AgentProjectSpec{}.WithDefaults().Workflow
	if *w.ConventionalCommits || len(w.CommitTypes) != 11 || w.CommitTypes[0] != "feat" {
		t.Fatalf("off, default types: %+v", w.CommitTypes)
	}
	got := WorkflowSpec{CommitTypes: []string{"feat"}}.WithDefaults().CommitTypes
	if len(got) != 2 || got[1] != "chore" {
		t.Fatalf("chore is added: %v", got)
	}
	if got := (WorkflowSpec{CommitTypes: []string{"chore", "fix"}}).WithDefaults().CommitTypes; len(got) != 2 {
		t.Fatalf("chore is not added twice: %v", got)
	}
}

func TestMaxStepLines(t *testing.T) {
	n := func(v int32) *int32 { return &v }
	spec := AgentProjectSpec{Agents: AgentsSpec{
		Implement: ImplementSpec{MaxStepLines: n(400)},
		Profiles: []AgentProfile{
			{Name: "fast", MaxStepLines: n(150)},
			{Name: "strong"},
		},
	}}.WithDefaults()
	if got := spec.Agents.Implement.MaxStepLines; got == nil || *got != 400 {
		t.Fatalf("defaults keep the value: %v", got)
	}
	if spec.Agents.Profiles[1].MaxStepLines != nil {
		t.Fatal("defaults do not copy implement's value into a profile")
	}
	for profile, want := range map[string]int{"": 400, "fast": 150, "strong": 400, "missing": 400} {
		if got := spec.MaxStepLines(profile); got != want {
			t.Errorf("profile %q: %d, want %d", profile, got, want)
		}
	}
	off := AgentProjectSpec{}.WithDefaults()
	if off.Agents.Implement.MaxStepLines != nil || off.MaxStepLines("") != 0 || off.MaxStepLines("fast") != 0 {
		t.Fatal("unset is off")
	}
	off.Agents.Profiles[0].MaxStepLines = n(300)
	if off.MaxStepLines(off.Agents.Profiles[0].Name) != 300 || off.MaxStepLines("") != 0 {
		t.Fatal("a profile's value applies to that profile only")
	}
}

func TestConflictDefaults(t *testing.T) {
	if spec := (AgentProjectSpec{}).WithDefaults(); spec.Agents.Implement.Conflict != nil {
		t.Fatalf("no conflict role unless set: %+v", spec.Agents.Implement.Conflict)
	}
	in := AgentProjectSpec{}
	in.Agents.Implement.Conflict = &ConflictRoleSpec{RoleSpec: RoleSpec{Model: "claude-sonnet-5-5"}}
	spec := in.WithDefaults()
	if c := spec.Agents.Implement.Conflict; c.FreshSession == nil || !*c.FreshSession || !c.Fresh() {
		t.Fatalf("freshSession defaults to true: %+v", c)
	}
	if in.Agents.Implement.Conflict.FreshSession != nil {
		t.Fatal("WithDefaults must not change its input")
	}
	no := false
	if (&ConflictRoleSpec{FreshSession: &no}).Fresh() || (*ConflictRoleSpec)(nil).Fresh() {
		t.Fatal("freshSession false, or no conflict role, resumes")
	}
}

package v1alpha1

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResearchTimeout(t *testing.T) {
	for depth, want := range map[ResearchDepth]time.Duration{
		"":                 20 * time.Minute,
		ResearchDepthQuick: 20 * time.Minute,
		ResearchDepthDeep:  2 * time.Hour,
	} {
		if got := (AgentResearchSpec{Depth: depth}).Timeout(); got != want {
			t.Errorf("depth %q: timeout %s, want %s", depth, got, want)
		}
	}
}

func TestResearchRetention(t *testing.T) {
	if got := (AgentResearchSpec{}).RetentionOrDefault(); got != 720*time.Hour {
		t.Errorf("default retention %s", got)
	}
	if got := (AgentResearchSpec{Retention: &metav1.Duration{}}).RetentionOrDefault(); got != 720*time.Hour {
		t.Errorf("zero retention %s", got)
	}
	if got := (AgentResearchSpec{Retention: &metav1.Duration{Duration: 48 * time.Hour}}).RetentionOrDefault(); got != 48*time.Hour {
		t.Errorf("retention %s", got)
	}
}

func TestResearchOneOff(t *testing.T) {
	r := &AgentResearch{}
	if !r.OneOff() {
		t.Error("a research without schedule must be one-off")
	}
	r.Spec.Schedule = "0 7 * * *"
	if r.OneOff() {
		t.Error("a scheduled research is not one-off")
	}
}

func TestValidateResearchAgent(t *testing.T) {
	s := routingSpec()
	for _, tc := range []struct {
		agent ResearchAgent
		want  ResearchAgent
		err   string
	}{
		{ResearchAgent{Engine: EngineClaude, Model: "sonnet", Effort: "high"}, ResearchAgent{Engine: EngineClaude, Model: "claude-sonnet-5-5", Effort: "high"}, ""},
		{ResearchAgent{Engine: EngineClaude, Model: "claude-opus-5-5"}, ResearchAgent{Engine: EngineClaude, Model: "claude-opus-5-5"}, ""},
		{ResearchAgent{Engine: EngineCursor, Model: "auto"}, ResearchAgent{Engine: EngineCursor, Model: "auto"}, ""},
		{ResearchAgent{Engine: EngineClaude, Model: "gpt-9"}, ResearchAgent{}, "not an allowed model"},
		{ResearchAgent{Engine: EngineCursor, Model: "sonnet"}, ResearchAgent{}, "not an allowed model"},
		{ResearchAgent{Engine: EngineCursor, Model: "auto", Effort: "high"}, ResearchAgent{}, "no effort setting"},
		{ResearchAgent{Engine: EngineClaude, Model: "opus", Effort: "lots"}, ResearchAgent{}, "no effort level"},
	} {
		got, err := s.ResolveResearchAgent(tc.agent)
		if tc.err == "" {
			if err != nil || got != tc.want {
				t.Errorf("%+v: got %+v, %v; want %+v", tc.agent, got, err, tc.want)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%+v: error %v, want %q", tc.agent, err, tc.err)
		}
		if verr := ValidateResearchAgent(s, tc.agent); (verr == nil) != (err == nil) {
			t.Errorf("%+v: ValidateResearchAgent %v, ResolveResearchAgent %v", tc.agent, verr, err)
		}
	}
}

func TestSecretLoginDefaults(t *testing.T) {
	if got := (SecretLogin{Name: "w"}).WithDefaults(); got != (SecretLogin{Name: "w", UsernameKey: "username", PasswordKey: "password"}) {
		t.Errorf("defaults %+v", got)
	}
	l := SecretLogin{Name: "w", UsernameKey: "user", PasswordKey: "pass"}
	if got := l.WithDefaults(); got != l {
		t.Errorf("set keys changed: %+v", got)
	}
}

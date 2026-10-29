package research

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestChoose(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *metav1.Time { return &metav1.Time{Time: now.Add(d)} }
	claudeAt := func(percent int32, resets time.Duration) []aiv1.EngineUsage {
		return []aiv1.EngineUsage{{Engine: aiv1.EngineClaude, ObservedAt: *at(-time.Minute), Windows: []aiv1.UsageWindow{
			{Name: "five_hour", Percent: percent, ResetsAt: at(resets)},
			{Name: "seven_day", Percent: 30, ResetsAt: at(96 * time.Hour)},
		}}}
	}
	cursorLimited := []aiv1.UsageLimit{{Engine: aiv1.EngineCursor, Until: metav1.Time{Time: now.Add(15 * time.Hour)}}}
	models := append(aiv1.DefaultModels(), aiv1.ModelOption{Engine: aiv1.EngineCursor, Model: "auto"})
	sonnet := aiv1.ResearchAgent{Engine: aiv1.EngineClaude, Model: "claude-sonnet-5-5"}
	opus := aiv1.ResearchAgent{Engine: aiv1.EngineClaude, Model: "opus", Effort: "max"}
	cursor := aiv1.ResearchAgent{Engine: aiv1.EngineCursor, Model: "auto"}
	quick := []aiv1.ResearchAgent{sonnet, cursor}

	for name, tc := range map[string]struct {
		models     []aiv1.ModelOption
		maxEffort  string
		status     aiv1.AgentProjectStatus
		candidates []aiv1.ResearchAgent
		want       *aiv1.ResearchAgent
		reason     string
		waitEngine aiv1.Engine
		waitUntil  time.Time
	}{
		"free": {
			status: aiv1.AgentProjectStatus{EngineUsage: claudeAt(40, time.Hour)}, candidates: quick,
			want: &sonnet, reason: "chosen: claude/claude-sonnet-5-5",
		},
		"over threshold": {
			status: aiv1.AgentProjectStatus{EngineUsage: claudeAt(85, time.Hour)}, candidates: quick,
			want: &cursor, reason: "chosen: cursor/auto (sonnet: 5-hour window at 85 %)",
		},
		"at the threshold": {
			status: aiv1.AgentProjectStatus{EngineUsage: claudeAt(70, time.Hour)}, candidates: quick,
			want: &cursor, reason: "chosen: cursor/auto (sonnet: 5-hour window at 70 %)",
		},
		"limited": {
			status:     aiv1.AgentProjectStatus{UsageLimits: []aiv1.UsageLimit{{Engine: aiv1.EngineClaude, Until: *at(2 * time.Hour)}}},
			candidates: quick, want: &cursor, reason: "chosen: cursor/auto (claude limited until 2026-11-12 11:00 UTC)",
		},
		"stale reading": {
			status: aiv1.AgentProjectStatus{EngineUsage: claudeAt(99, -time.Minute)}, candidates: quick,
			want: &sonnet, reason: "chosen: claude/claude-sonnet-5-5",
		},
		"expired limit": {
			status:     aiv1.AgentProjectStatus{UsageLimits: []aiv1.UsageLimit{{Engine: aiv1.EngineClaude, Until: *at(-time.Second)}}},
			candidates: quick, want: &sonnet, reason: "chosen: claude/claude-sonnet-5-5",
		},
		"all busy": {
			status:     aiv1.AgentProjectStatus{EngineUsage: claudeAt(85, 5*time.Hour), UsageLimits: cursorLimited},
			candidates: []aiv1.ResearchAgent{opus, sonnet, cursor},
			reason:     "no candidate qualifies: opus: 5-hour window at 85 % until 2026-11-12 14:00 UTC; sonnet: 5-hour window at 85 % until 2026-11-12 14:00 UTC; cursor limited until 2026-11-13 00:00 UTC",
			waitEngine: aiv1.EngineClaude, waitUntil: now.Add(5 * time.Hour),
		},
		"all busy, the limit ends first": {
			status:     aiv1.AgentProjectStatus{EngineUsage: claudeAt(85, 20*time.Hour), UsageLimits: cursorLimited},
			candidates: quick,
			reason:     "no candidate qualifies: sonnet: 5-hour window at 85 % until 2026-11-13 05:00 UTC; cursor limited until 2026-11-13 00:00 UTC",
			waitEngine: aiv1.EngineCursor, waitUntil: now.Add(15 * time.Hour),
		},
		"engine not used": {
			models: aiv1.DefaultModels(), status: aiv1.AgentProjectStatus{EngineUsage: claudeAt(40, time.Hour)},
			candidates: []aiv1.ResearchAgent{cursor, sonnet}, want: &sonnet, reason: "chosen: claude/claude-sonnet-5-5",
		},
		"engine not used and all busy": {
			models: aiv1.DefaultModels(), status: aiv1.AgentProjectStatus{EngineUsage: claudeAt(85, time.Hour)},
			candidates: quick,
			reason:     "no candidate qualifies: sonnet: 5-hour window at 85 % until 2026-11-12 10:00 UTC; cursor/auto: the project does not use cursor",
			waitEngine: aiv1.EngineClaude, waitUntil: now.Add(time.Hour),
		},
		"nothing usable": {
			models: aiv1.DefaultModels(), candidates: []aiv1.ResearchAgent{cursor, {Engine: aiv1.EngineClaude, Model: "gpt-9"}},
			reason: "no candidate qualifies: cursor/auto: the project does not use cursor; claude/gpt-9: not an allowed model",
		},
		"effort cap": {
			maxEffort: "medium", candidates: []aiv1.ResearchAgent{opus},
			want: &aiv1.ResearchAgent{Engine: aiv1.EngineClaude, Model: "claude-opus-5-5", Effort: "medium"}, reason: "chosen: claude/claude-opus-5-5",
		},
		"effort under the cap": {
			candidates: []aiv1.ResearchAgent{{Engine: aiv1.EngineClaude, Model: "opus", Effort: "low"}},
			want:       &aiv1.ResearchAgent{Engine: aiv1.EngineClaude, Model: "claude-opus-5-5", Effort: "low"}, reason: "chosen: claude/claude-opus-5-5",
		},
		"no effort on cursor": {
			candidates: []aiv1.ResearchAgent{{Engine: aiv1.EngineCursor, Model: "auto", Effort: "high"}},
			want:       &cursor, reason: "chosen: cursor/auto",
		},
		"alias in the reason": {
			status: aiv1.AgentProjectStatus{EngineUsage: []aiv1.EngineUsage{{Engine: aiv1.EngineClaude,
				Windows: []aiv1.UsageWindow{{Name: "seven_day_opus", Percent: 82, ResetsAt: at(48 * time.Hour)}}}}},
			candidates: []aiv1.ResearchAgent{opus, cursor},
			want:       &cursor, reason: "chosen: cursor/auto (opus: 7-day opus window at 82 %)",
		},
		"no candidates": {reason: "no candidates"},
	} {
		t.Run(name, func(t *testing.T) {
			p := &aiv1.AgentProject{Spec: aiv1.AgentProjectSpec{Agents: aiv1.AgentsSpec{Models: models, MaxEffort: tc.maxEffort}}, Status: tc.status}
			if tc.models != nil {
				p.Spec.Agents.Models = tc.models
			}
			c := Choose(p, tc.candidates, 70, now)
			switch {
			case tc.want == nil && c.Agent != nil:
				t.Errorf("agent %+v, want none", *c.Agent)
			case tc.want != nil && (c.Agent == nil || *c.Agent != *tc.want):
				t.Errorf("agent %+v, want %+v", c.Agent, *tc.want)
			}
			if c.Reason != tc.reason {
				t.Errorf("reason\n %q\nwant\n %q", c.Reason, tc.reason)
			}
			if c.WaitEngine != tc.waitEngine || !c.WaitUntil.Equal(tc.waitUntil) {
				t.Errorf("wait %s until %v, want %s until %v", c.WaitEngine, c.WaitUntil, tc.waitEngine, tc.waitUntil)
			}
		})
	}
}

func TestParseCandidates(t *testing.T) {
	got, err := ParseCandidates(" claude/claude-opus-5-5@high, claude/sonnet ,cursor/auto,")
	want := []aiv1.ResearchAgent{
		{Engine: aiv1.EngineClaude, Model: "claude-opus-5-5", Effort: "high"},
		{Engine: aiv1.EngineClaude, Model: "sonnet"},
		{Engine: aiv1.EngineCursor, Model: "auto"},
	}
	if err != nil || len(got) != len(want) {
		t.Fatalf("got %+v, %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if got, err := ParseCandidates(""); err != nil || len(got) != 0 {
		t.Errorf("empty: %+v, %v", got, err)
	}
	for in, msg := range map[string]string{
		"claude":                  "want engine/model",
		"claude/":                 "want engine/model",
		"gemini/pro":              "unknown engine",
		"claude/opus@turbo":       "no effort level",
		"cursor/auto@high":        "no effort setting",
		"claude/opus,cursor-auto": "want engine/model",
	} {
		if _, err := ParseCandidates(in); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: %v, want %q", in, err, msg)
		}
	}
}

package runner

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const scoutJSON = `{"scout":{"description":"Searches the code","model":"claude-haiku-5-5","prompt":"Search, never edit.","tools":["Read","Grep","Glob"]}}`

func scout() []aiv1.Subagent {
	return []aiv1.Subagent{{Name: "scout", Description: "Searches the code", Model: "claude-haiku-5-5", Tools: []string{"Read", "Grep", "Glob"}, Prompt: "Search, never edit."}}
}

const delegationLine = "You have subagents (scout). Delegate searches, lookups and log reading to them, and read only the files you edit yourself."

func TestClaudeArgsAgentsOnlyWhenSet(t *testing.T) {
	c := &ClaudeCLI{}
	if args := strings.Join(c.Args(Invocation{SystemPrompt: "sys"}), " "); strings.Contains(args, "--agents") {
		t.Errorf("no subagents, no --agents: %q", args)
	}
	args := c.Args(Invocation{SystemPrompt: "sys", Agents: scoutJSON})
	if !strings.Contains(strings.Join(args, " "), "--agents "+scoutJSON) {
		t.Errorf("args %q miss --agents", args)
	}
}

// logLines captures the runner's log lines; reviewers log in parallel.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) count(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// subagentEnv runs one implement run with agents configured; no engine is
// limited unless status says so.
func subagentEnv(t *testing.T, agents func(*aiv1.AgentsSpec), status func(*aiv1.AgentProjectStatus)) (*recordedEngine, *logLines) {
	t.Helper()
	e, engine, rec := cursorLimitedEnv(t, agents, status)
	engine.limitIf = func(Invocation) bool { return false }
	logs := &logLines{}
	e.r.Log = funcr.New(func(prefix, args string) {
		logs.mu.Lock()
		logs.lines = append(logs.lines, args)
		logs.mu.Unlock()
	}, funcr.Options{})
	if !e.step() {
		t.Fatal("expected a run")
	}
	return rec, logs
}

func (r *recordedEngine) roleCalls(role string) []Invocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Invocation
	for _, c := range r.calls {
		if c.Role == role {
			out = append(out, c)
		}
	}
	return out
}

func TestImplementWithSubagentsPassesAgentsAndTheLine(t *testing.T) {
	rec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) { a.Implement.Subagents = scout() }, nil)
	calls := rec.roleCalls("implement")
	if len(calls) == 0 {
		t.Fatal("the implementer did not run")
	}
	for _, c := range calls {
		if c.Agents != scoutJSON || !strings.HasSuffix(c.SystemPrompt, "\n\n"+delegationLine) {
			t.Fatalf("expected the scout and the delegation line, got agents %q, system prompt …%q", c.Agents, c.SystemPrompt[max(0, len(c.SystemPrompt)-200):])
		}
	}
	// The reviewers have none of their own.
	for _, c := range rec.roleCalls("bugbot") {
		if c.Agents != "" || strings.Contains(c.SystemPrompt, "You have subagents") {
			t.Fatalf("bugbot has no subagents: %+v", c)
		}
	}
}

func TestImplementWithoutSubagentsIsUnchanged(t *testing.T) {
	rec, _ := subagentEnv(t, func(*aiv1.AgentsSpec) {}, nil)
	calls := rec.roleCalls("implement")
	if len(calls) == 0 {
		t.Fatal("the implementer did not run")
	}
	for _, c := range calls {
		if c.Agents != "" || strings.Contains(c.SystemPrompt, "You have subagents") {
			t.Fatalf("no subagents configured: agents %q", c.Agents)
		}
	}
}

func TestCursorRoleIgnoresSubagentsWithAWarning(t *testing.T) {
	rec, logs := subagentEnv(t, func(a *aiv1.AgentsSpec) {
		a.Review.Engine, a.Review.Model, a.Review.Subagents = aiv1.EngineCursor, "auto", scout()
	}, nil)
	calls := rec.roleCalls("bugbot")
	if len(calls) == 0 {
		t.Fatal("bugbot did not run")
	}
	for _, c := range append(calls, rec.roleCalls("security")...) {
		if c.Agents != "" || strings.Contains(c.SystemPrompt, "You have subagents") {
			t.Fatalf("cursor gets no subagents: agents %q", c.Agents)
		}
	}
	// Once per run and role, though bugbot reviews every round.
	if n := logs.count("subagents are claude only; ignored for role bugbot on cursor"); n != 1 {
		t.Fatalf("expected one warning for bugbot, got %d:\n%s", n, strings.Join(logs.lines, "\n"))
	}
}

func TestSubagentsDroppedOnACursorFallback(t *testing.T) {
	rec, logs := subagentEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement.Subagents = scout()
		a.Implement.Fallback = &aiv1.RoleFallback{Engine: aiv1.EngineCursor, Model: "auto"}
		a.Models = append(aiv1.DefaultModels(), aiv1.ModelOption{Engine: aiv1.EngineCursor, Model: "auto"})
	}, func(s *aiv1.AgentProjectStatus) {
		s.UsageLimits = []aiv1.UsageLimit{{Engine: aiv1.EngineClaude, Until: metav1.NewTime(time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC))}}
	})
	calls := rec.roleCalls("implement")
	if len(calls) == 0 || calls[0].Model != "auto" {
		t.Fatalf("expected the implementer on the cursor fallback, got %+v", calls)
	}
	for _, c := range calls {
		if c.Agents != "" || strings.Contains(c.SystemPrompt, "You have subagents") {
			t.Fatalf("the cursor fallback gets no subagents: agents %q", c.Agents)
		}
	}
	if n := logs.count("subagents are claude only; ignored for role implement on cursor"); n != 1 {
		t.Fatalf("expected one warning, got %d", n)
	}
}

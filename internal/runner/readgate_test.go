package runner

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const (
	gateSentence         = "Reads of files over 350 lines are blocked; ask a subagent or read a section with offset/limit."
	gateSentenceBuiltins = "Reads of files over 350 lines are blocked; ask a subagent such as Explore or read a section with offset/limit."
)

func gateSettings(subagents string) string {
	cmd := "ai-runner read-gate --min-lines 350"
	if subagents != "" {
		cmd += " --subagents " + subagents
	}
	return `{"hooks":{"PreToolUse":[` +
		`{"matcher":"Read","hooks":[{"type":"command","command":"` + cmd + `","timeout":5}]},` +
		`{"matcher":"Bash","hooks":[{"type":"command","command":"` + cmd + `","timeout":5}]}]}}`
}

func TestReadGateSettings(t *testing.T) {
	got := readGateSettings(350, []string{"scout", "writer"})
	if got != gateSettings("scout,writer") {
		t.Fatalf("settings:\n got %s\nwant %s", got, gateSettings("scout,writer"))
	}
	var parsed struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string
				Hooks   []struct {
					Type, Command string
					Timeout       int
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	var matchers []string
	for _, m := range parsed.Hooks.PreToolUse {
		matchers = append(matchers, m.Matcher)
		if len(m.Hooks) != 1 || m.Hooks[0].Type != "command" || m.Hooks[0].Timeout != 5 {
			t.Fatalf("hook of %s: %+v", m.Matcher, m.Hooks)
		}
	}
	if !slices.Equal(matchers, []string{"Read", "Bash"}) {
		t.Fatalf("matchers %v", matchers)
	}
	if got := readGateSettings(200, nil); got != strings.ReplaceAll(gateSettings(""), "350", "200") {
		t.Fatalf("without subagents no --subagents: %s", got)
	}
}

func TestClaudeArgsSettingsOnlyWhenSet(t *testing.T) {
	c := &ClaudeCLI{}
	if args := c.Args(Invocation{SystemPrompt: "sys", Agents: scoutJSON}); slices.Contains(args, "--settings") {
		t.Errorf("no settings, no --settings: %q", args)
	}
	args := c.Args(Invocation{SystemPrompt: "sys", Agents: scoutJSON, Settings: gateSettings("scout"), Resume: "s1"})
	i := slices.Index(args, "--agents")
	if i < 0 || len(args) < i+4 || args[i+2] != "--settings" || args[i+3] != gateSettings("scout") {
		t.Errorf("--settings should follow --agents: %q", args)
	}
	cursor := &CursorCLI{}
	inv := Invocation{Prompt: "p", Model: "auto"}
	gated := inv
	gated.Settings = gateSettings("")
	if a, b := cursor.Args(inv, cursor.Prompt(inv)), cursor.Args(gated, cursor.Prompt(gated)); !slices.Equal(a, b) {
		t.Errorf("cursor ignores settings: %q vs %q", a, b)
	}
}

func gate() *aiv1.ReadGate { return &aiv1.ReadGate{} }

func firstCall(t *testing.T, rec *recordedEngine, role string) Invocation {
	t.Helper()
	calls := rec.roleCalls(role)
	if len(calls) == 0 {
		t.Fatalf("%s did not run", role)
	}
	return calls[0]
}

func TestReadGateWithSubagentsJoinsTheirLine(t *testing.T) {
	rec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement.Subagents, a.Implement.ReadGate = scout(), gate()
	}, nil)
	for _, c := range rec.roleCalls("implement") {
		if c.Settings != gateSettings("scout") {
			t.Fatalf("settings %q", c.Settings)
		}
		if !strings.HasSuffix(c.SystemPrompt, "\n\n"+delegationLine+" "+gateSentence) {
			t.Fatalf("expected the delegation line with the gate sentence, got …%q", c.SystemPrompt[max(0, len(c.SystemPrompt)-300):])
		}
	}
	// --settings reaches the command line.
	inv := firstCall(t, rec, "implement")
	if args := (&ClaudeCLI{}).Args(inv); !slices.Contains(args, gateSettings("scout")) {
		t.Fatalf("args miss --settings: %q", args)
	}
	for _, c := range rec.roleCalls("bugbot") {
		if c.Settings != "" || strings.Contains(c.SystemPrompt, "are blocked") {
			t.Fatalf("bugbot has no read gate: %+v", c)
		}
	}
}

func TestReadGateWithoutSubagentsNamesTheBuiltins(t *testing.T) {
	rec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) { a.Implement.ReadGate = &aiv1.ReadGate{MinLines: 350} }, nil)
	for _, c := range rec.roleCalls("implement") {
		if c.Settings != gateSettings("") || c.Agents != "" {
			t.Fatalf("settings %q, agents %q", c.Settings, c.Agents)
		}
		if !strings.HasSuffix(c.SystemPrompt, "\n\n"+gateSentenceBuiltins) || strings.Contains(c.SystemPrompt, "You have subagents") {
			t.Fatalf("expected the gate sentence as its own paragraph, got …%q", c.SystemPrompt[max(0, len(c.SystemPrompt)-300):])
		}
	}
}

// Without readGate the invocation is what it was before the gate existed:
// the gated one minus the settings and the sentence.
func TestNoReadGateIsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name      string
		subagents []aiv1.Subagent
		sep       string
		sentence  string
	}{
		{"with subagents", scout(), " ", gateSentence},
		{"without subagents", nil, "\n\n", gateSentenceBuiltins},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plainRec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) { a.Implement.Subagents = tc.subagents }, nil)
			gatedRec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) { a.Implement.Subagents, a.Implement.ReadGate = tc.subagents, gate() }, nil)
			plain, gated := firstCall(t, plainRec, "implement"), firstCall(t, gatedRec, "implement")
			if plain.Settings != "" || strings.Contains(plain.SystemPrompt, "are blocked") {
				t.Fatalf("no gate configured: settings %q", plain.Settings)
			}
			if tc.subagents != nil && !strings.HasSuffix(plain.SystemPrompt, "\n\n"+delegationLine) {
				t.Fatalf("the delegation line changed: …%q", plain.SystemPrompt[max(0, len(plain.SystemPrompt)-200):])
			}
			if gated.SystemPrompt != plain.SystemPrompt+tc.sep+tc.sentence {
				t.Fatalf("the gate should only add its sentence:\nplain …%q\ngated …%q",
					plain.SystemPrompt[max(0, len(plain.SystemPrompt)-200):], gated.SystemPrompt[max(0, len(gated.SystemPrompt)-300):])
			}
			gated.Settings, gated.SystemPrompt = "", plain.SystemPrompt
			c := &ClaudeCLI{}
			if a, b := c.Args(plain), c.Args(gated); !slices.Equal(a, b) {
				t.Fatalf("args differ beyond the gate:\n%q\n%q", a, b)
			}
		})
	}
}

func TestCursorRoleIgnoresReadGateWithAWarning(t *testing.T) {
	rec, logs := subagentEnv(t, func(a *aiv1.AgentsSpec) {
		a.Review.Engine, a.Review.Model, a.Review.ReadGate = aiv1.EngineCursor, "auto", gate()
	}, nil)
	calls := append(rec.roleCalls("bugbot"), rec.roleCalls("security")...)
	if len(rec.roleCalls("bugbot")) == 0 {
		t.Fatal("bugbot did not run")
	}
	for _, c := range calls {
		if c.Settings != "" || strings.Contains(c.SystemPrompt, "are blocked") {
			t.Fatalf("cursor gets no read gate: settings %q", c.Settings)
		}
	}
	// Once per run and role, though bugbot reviews every round.
	if n := logs.count("read gate is claude only; ignored for role bugbot on cursor"); n != 1 {
		t.Fatalf("expected one warning for bugbot over %d calls, got %d:\n%s", len(rec.roleCalls("bugbot")), n, strings.Join(logs.lines, "\n"))
	}
}

func TestReadGateDroppedOnACursorFallback(t *testing.T) {
	rec, logs := subagentEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement.Subagents, a.Implement.ReadGate = scout(), gate()
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
		if c.Settings != "" || strings.Contains(c.SystemPrompt, "are blocked") {
			t.Fatalf("the cursor fallback gets no read gate: settings %q", c.Settings)
		}
	}
	if n := logs.count("read gate is claude only; ignored for role implement on cursor"); n != 1 {
		t.Fatalf("expected one warning, got %d", n)
	}
}

func TestMixedSubagentsWithReadGateEndWithTheGateSentence(t *testing.T) {
	subs := append(scout(), aiv1.Subagent{Name: "writer", Description: "Writes boilerplate", Tools: []string{"Read", "Write", "Edit"}, Prompt: "Write files."})
	rec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) { a.Implement.Subagents, a.Implement.ReadGate = subs, gate() }, nil)
	want := subagentsNote(subs) + " " + gateSentence
	for _, c := range rec.roleCalls("implement") {
		if !strings.HasSuffix(c.SystemPrompt, "\n\n"+want) || strings.Count(c.SystemPrompt, "are blocked") != 1 {
			t.Fatalf("expected the mixed note then the gate sentence once, got …%q", c.SystemPrompt[max(0, len(c.SystemPrompt)-600):])
		}
		if !strings.Contains(c.SystemPrompt, "to scout, and read only") || !strings.Contains(c.SystemPrompt, "to writer with a short spec") {
			t.Fatalf("each sentence should name its subagents: %q", c.SystemPrompt)
		}
	}
}

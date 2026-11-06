package runner

import (
	"context"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const stepGateSentence = "\nKeep a step under 400 changed lines where you can."

func stepGateHookJSON(maxLines string) string {
	return `[{"matcher":"Edit|Write|Bash","hooks":[{"type":"command","command":"ai-runner step-gate --max-lines ` + maxLines + `","timeout":15}]}]`
}

func TestStepGateSettings(t *testing.T) {
	// Read gate and step gate: one object, one hooks.PreToolUse and one
	// hooks.PostToolUse array.
	read := gateSettings("")
	both := stepGateSettings(read, 400)
	pre := strings.TrimSuffix(strings.TrimPrefix(read, `{"hooks":{"PreToolUse":`), `}}`)
	if want := `{"hooks":{"PostToolUse":` + stepGateHookJSON("400") + `,"PreToolUse":` + pre + `}}`; both != want {
		t.Fatalf("merged settings:\n got %s\nwant %s", both, want)
	}
	if got, want := stepGateSettings("", 250), `{"hooks":{"PostToolUse":`+stepGateHookJSON("250")+`}}`; got != want {
		t.Fatalf("step gate alone:\n got %s\nwant %s", got, want)
	}
	// Neither: the invocation stays as it is.
	r := &Runner{}
	inv := Invocation{Role: "implement", Settings: read, SystemPrompt: "sys"}
	if got := r.withStepGate(aiv1.RoleSpec{Engine: aiv1.EngineClaude}, inv); got.Settings != read || got.SystemPrompt != "sys" {
		t.Fatalf("without the gate the settings stay: %+v", got)
	}
	if got := r.withStepGate(aiv1.RoleSpec{Engine: aiv1.EngineClaude}, Invocation{}); got.Settings != "" {
		t.Fatalf("no gates, no settings: %q", got.Settings)
	}
}

func TestStepsNote(t *testing.T) {
	if stepsNote(0) != stepsNoteText {
		t.Fatal("without the gate the note is unchanged")
	}
	if got := stepsNote(400); got != stepsNoteText+stepGateSentence {
		t.Fatalf("note …%q", got[len(got)-120:])
	}
}

func stepLines(n int32) *int32 { return &n }

// stepwiseCalls splits the implementer's calls into the stepwise ones (with
// the steps note) and the others (fix rounds).
func stepwiseCalls(t *testing.T, rec *recordedEngine) (steps, others []Invocation) {
	t.Helper()
	for _, c := range rec.roleCalls("implement") {
		if strings.Contains(c.SystemPrompt, "Work in steps") {
			steps = append(steps, c)
		} else {
			others = append(others, c)
		}
	}
	if len(steps) == 0 {
		t.Fatal("no stepwise implement call")
	}
	return steps, others
}

func TestStepGateOnStepwiseCalls(t *testing.T) {
	rec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement.ReadGate, a.Implement.MaxStepLines = gate(), stepLines(400)
	}, nil)
	steps, others := stepwiseCalls(t, rec)
	want := stepGateSettings(gateSettings(""), 400)
	for _, c := range steps {
		if c.Settings != want {
			t.Fatalf("settings:\n got %s\nwant %s", c.Settings, want)
		}
		if !strings.Contains(c.SystemPrompt, stepsNoteText+stepGateSentence) {
			t.Fatalf("the steps note should name 400: …%q", c.SystemPrompt[max(0, len(c.SystemPrompt)-400):])
		}
	}
	for _, c := range others {
		if c.Settings != gateSettings("") || strings.Contains(c.SystemPrompt, "changed lines") {
			t.Fatalf("a call that is not stepwise gets no step gate: settings %s", c.Settings)
		}
	}
	if args := (&ClaudeCLI{}).Args(steps[0]); !slices.Contains(args, want) {
		t.Fatalf("args miss the merged --settings: %q", args)
	}
}

// Without maxStepLines the calls are what they were before the gate.
func TestNoStepGateIsUnchanged(t *testing.T) {
	plainRec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) {}, nil)
	gatedRec, _ := subagentEnv(t, func(a *aiv1.AgentsSpec) { a.Implement.MaxStepLines = stepLines(400) }, nil)
	plainSteps, _ := stepwiseCalls(t, plainRec)
	gatedSteps, _ := stepwiseCalls(t, gatedRec)
	plain, gated := plainSteps[0], gatedSteps[0]
	if plain.Settings != "" || strings.Contains(plain.SystemPrompt, "changed lines") {
		t.Fatalf("no gate configured: settings %q", plain.Settings)
	}
	if gated.Settings != stepGateSettings("", 400) || gated.SystemPrompt != strings.Replace(plain.SystemPrompt, stepsNoteText, stepsNoteText+stepGateSentence, 1) {
		t.Fatalf("the gate should only add its hook and sentence: settings %q", gated.Settings)
	}
	gated.Settings, gated.SystemPrompt = "", plain.SystemPrompt
	c := &ClaudeCLI{}
	if a, b := c.Args(plain), c.Args(gated); !slices.Equal(a, b) {
		t.Fatalf("args differ beyond the gate:\n%q\n%q", a, b)
	}
}

func TestStepGateProfileOverridesImplement(t *testing.T) {
	for name, tc := range map[string]struct {
		profile *int32
		want    string
	}{
		"profile value wins":     {stepLines(400), "400"},
		"unset profile inherits": {nil, "900"},
	} {
		t.Run(name, func(t *testing.T) {
			e, engine, rec := cursorLimitedEnv(t, func(a *aiv1.AgentsSpec) {
				a.Implement.MaxStepLines = stepLines(900)
				a.Profiles = []aiv1.AgentProfile{{Name: "fast", RoleSpec: aiv1.RoleSpec{Engine: aiv1.EngineClaude, Model: "sonnet"}, MaxStepLines: tc.profile}}
			}, nil)
			engine.limitIf = func(Invocation) bool { return false }
			e.gl.SetLabels(42, "agent::working", "agent-profile::fast")
			if !e.step() {
				t.Fatal("expected a run")
			}
			steps, _ := stepwiseCalls(t, rec)
			if steps[0].Model != "sonnet" {
				t.Fatalf("expected the fast profile, got model %q", steps[0].Model)
			}
			if steps[0].Settings != `{"hooks":{"PostToolUse":`+stepGateHookJSON(tc.want)+`}}` ||
				!strings.Contains(steps[0].SystemPrompt, "Keep a step under "+tc.want+" changed lines") {
				t.Fatalf("want the gate at %s: settings %q", tc.want, steps[0].Settings)
			}
		})
	}
}

func TestCursorImplementerIgnoresStepGateWithAWarning(t *testing.T) {
	rec, logs := subagentEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement.Engine, a.Implement.Model, a.Implement.MaxStepLines = aiv1.EngineCursor, "auto", stepLines(400)
	}, nil)
	steps, _ := stepwiseCalls(t, rec)
	for _, c := range steps {
		if c.Settings != "" {
			t.Fatalf("cursor gets no step gate: settings %q", c.Settings)
		}
	}
	if n := logs.count("step-size gate is claude only; ignored for role implement on cursor"); n != 1 {
		t.Fatalf("expected one warning, got %d", n)
	}
}

func TestImplementStepGate(t *testing.T) {
	withGate := func(t *testing.T) *implementRun {
		e := newImplementEnv(t)
		run := phaseRun(t, e, &scriptedEngine{})
		run.spec.Agents.Implement.MaxStepLines = stepLines(400)
		return run
	}
	t.Run("a stepwise run has the gate, a fix round has none", func(t *testing.T) {
		run := withGate(t)
		phases(t, run, upToPrompt(run)...)
		if !run.stepwise || run.inv.StepGateMaxLines != 400 || !strings.Contains(run.inv.SystemPrompt, stepGateSentence) {
			t.Fatalf("stepwise=%v gate=%d", run.stepwise, run.inv.StepGateMaxLines)
		}
		if err := run.implement(context.Background()); err != nil {
			t.Fatal(err)
		}
		if run.inv.StepGateMaxLines != 0 || strings.Contains(run.inv.SystemPrompt, "changed lines") {
			t.Fatalf("fix rounds get no step gate: gate=%d", run.inv.StepGateMaxLines)
		}
	})
	t.Run("a run that is not stepwise has none", func(t *testing.T) {
		run := withGate(t)
		off := false
		run.wf.StepCommits = &off
		phases(t, run, upToPrompt(run)...)
		if run.stepwise || run.inv.StepGateMaxLines != 0 || strings.Contains(run.inv.SystemPrompt, "changed lines") {
			t.Fatalf("stepwise=%v gate=%d", run.stepwise, run.inv.StepGateMaxLines)
		}
	})
}

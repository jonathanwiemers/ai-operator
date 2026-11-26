package runner

import (
	"context"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestImplementerUsesTheIssuesModelAndEffort(t *testing.T) {
	e := newImplementEnv(t)
	e.gl.SetLabels(42, "agent::working", "agent-model::claude/sonnet", "agent-effort::low")
	e.gl.AddNote(42, gitlab.User{ID: 1, Username: "jonathan"}, "/agent use claude sonnet low")
	engine := &scriptedEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	impl := engine.calls[0]
	if impl.ReadOnly || impl.Model != "claude-sonnet-5-5" || impl.Effort != "low" {
		t.Fatalf("implementer ran with %s/%s", impl.Model, impl.Effort)
	}
	if strings.Contains(impl.Prompt, "/agent use") {
		t.Fatalf("a pure model command is no input for the agent:\n%s", impl.Prompt)
	}
}

func TestPureCommandsDoNotCutAStep(t *testing.T) {
	notes := []gitlab.Note{{ID: 5, Body: "/agent use claude opus"}, {ID: 6, Body: "also do X"}, {ID: 7, Body: "/agent redo it"}}
	if got := firstCommand(notes, 9, "/agent"); got != 7 {
		t.Fatalf("the cut must be the first command that starts a run, got %d", got)
	}
}

func TestAnotherEngineStartsAFreshSession(t *testing.T) {
	e := newImplementEnv(t)
	e.configure(t, func(p *aiv1.AgentProjectSpec) {
		p.Agents.Models = append(aiv1.DefaultModels(), aiv1.ModelOption{Engine: aiv1.EngineCursor, Model: "auto"})
	}, func(*aiv1.AgentTaskSpec) {})
	engine := &describingEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil { // claude
		t.Fatal(err)
	}
	e.gl.SetLabels(42, "agent::working", "agent-model::cursor/auto")
	e.rerun(t, r) // cursor
	if st := savedState(t, r); st.ImplementEngine != aiv1.EngineCursor {
		t.Fatalf("the cursor run must have run: %+v", st)
	}
	e.gl.SetLabels(42, "agent::working")
	engine.mu.Lock()
	engine.calls = nil
	engine.mu.Unlock()
	e.rerun(t, r) // back to claude
	impl := engine.calls[0]
	if impl.ReadOnly || impl.Resume != "" || !strings.Contains(impl.Prompt, "Implement issue #42") {
		t.Fatalf("back on claude after cursor ran, the session must start fresh: resume=%q", impl.Resume)
	}
}

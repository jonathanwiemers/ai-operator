package runner

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestImplementSchemaAsksForPrompts(t *testing.T) {
	var schema struct {
		Required   []string `json:"required"`
		Properties struct {
			Prompts struct {
				Items struct {
					Properties struct {
						Options struct {
							MinItems int `json:"minItems"`
							MaxItems int `json:"maxItems"`
						} `json:"options"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"prompts"`
		} `json:"properties"`
	}
	for _, s := range []string{implementSchema, implementStepSchema} {
		if err := json.Unmarshal([]byte(s), &schema); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(schema.Required, "prompts") || schema.Properties.Prompts.Items.Properties.Options.MinItems != 2 ||
			schema.Properties.Prompts.Items.Properties.Options.MaxItems != 4 {
			t.Fatalf("prompts are optional, with 2 to 4 options: %+v", schema)
		}
	}
	var a ImplementAnswer
	if err := json.Unmarshal([]byte(`{"status": "needs_input", "summary": "s", "commitMessage": "c", "questions": [], "decisions": [], "handoff": "h",
		"prompts": [{"question": "Which store?", "options": [{"label": "Postgres", "detail": "d"}, {"label": "SQLite", "default": true}], "allowFreeText": false}]}`), &a); err != nil {
		t.Fatal(err)
	}
	if len(a.Prompts) != 1 || len(a.Prompts[0].Options) != 2 || !a.Prompts[0].Options[1].Default ||
		a.Prompts[0].AllowFreeText == nil || *a.Prompts[0].AllowFreeText || !a.asks() {
		t.Fatalf("decoded %+v", a)
	}
	if (ImplementAnswer{Status: "done"}).asks() {
		t.Fatal("an answer without questions asks nothing")
	}
}

func TestQuestionDrafts(t *testing.T) {
	no := false
	got := questionDrafts(ImplementAnswer{
		Questions: []string{"  Which license?  ", " "},
		Prompts: []QuestionPrompt{
			{Question: "Store?", AllowFreeText: &no, Options: []QuestionOption{
				{Label: "Postgres"}, {Label: " "}, {Label: "SQLite", Detail: " small ", Default: true}, {Label: "Both", Default: true},
				{Label: "Files"}, {Label: "Redis"}}},
			{Question: "Port?", Options: []QuestionOption{{Label: "8080"}, {Label: "9090"}}},
			{Question: "Only one?", AllowFreeText: &no, Options: []QuestionOption{{Label: "Yes", Default: true}}},
			{Question: " ", Options: []QuestionOption{{Label: "a"}, {Label: "b"}}},
			{Question: strings.Repeat("q", 2000), Options: []QuestionOption{{Label: strings.Repeat("l", 500)}, {Label: "x", Detail: strings.Repeat("d", 2000)}}},
		},
	})
	if len(got) != 5 {
		t.Fatalf("four structured questions and one prose, got %d: %+v", len(got), got)
	}
	store := got[0]
	ids := func(d aiv1.QuestionDraft) (s []string) {
		for _, o := range d.Options {
			if o.Recommended {
				s = append(s, o.ID+"*")
			} else {
				s = append(s, o.ID)
			}
		}
		return s
	}
	if store.AllowFreeText || !slices.Equal(ids(store), []string{"A", "B*", "C", "D"}) ||
		store.Options[1].Label != "SQLite" || store.Options[1].Detail != "small" || store.Options[3].Label != "Files" {
		t.Fatalf("empty labels go, four stay, the first default is the one: %+v", store)
	}
	if port := got[1]; !port.AllowFreeText || !slices.Equal(ids(port), []string{"A*", "B"}) {
		t.Fatalf("without a default the first is recommended, free text by default: %+v", port)
	}
	if one := got[2]; one.Options != nil || !one.AllowFreeText {
		t.Fatalf("fewer than 2 options: free text only: %+v", one)
	}
	long := got[3]
	if len(long.Question) > aiv1.MaxPromptQuestion || len(long.Options[0].Label) > aiv1.MaxPromptOptionLabel ||
		len(long.Options[1].Detail) > aiv1.MaxPromptOptionDetail {
		t.Fatalf("texts are cut to the prompt bounds: %d %d %d", len(long.Question), len(long.Options[0].Label), len(long.Options[1].Detail))
	}
	if prose := got[4]; prose.Question != "Which license?" || prose.Options != nil || !prose.AllowFreeText {
		t.Fatalf("a prose question is free text only: %+v", prose)
	}
	for _, d := range got {
		for _, o := range d.Options {
			if len(o.Actions) > 0 {
				t.Fatalf("drafts have no actions: %+v", d)
			}
		}
	}
	if questionDrafts(ImplementAnswer{Status: "done"}) != nil {
		t.Fatal("no questions, no drafts")
	}
}

func TestStructuredQuestionsEndNeedsInput(t *testing.T) {
	e := newImplementEnv(t)
	var prose []string
	for i := range 5 {
		prose = append(prose, "Prose question "+string(rune('1'+i))+"?")
	}
	engine := &describingEngine{answer: ImplementAnswer{Status: "done", Summary: "Stuck.", Questions: prose,
		Prompts: []QuestionPrompt{{Question: "Which store?\n/merge @bob", Options: []QuestionOption{
			{Label: "Postgres", Detail: "Needs a server."}, {Label: "SQLite", Default: true}}}}}}
	e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(*aiv1.AgentTaskSpec) {})
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got aiv1.AgentTask
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got); err != nil {
		t.Fatal(err)
	}
	run := got.Status.Run
	if run.LastResult != aiv1.RunResultNeedsInput {
		t.Fatalf("structured questions end the run needing input: %s", run.LastResult)
	}
	if len(run.Questions) != aiv1.MaxQuestionDrafts || !strings.HasPrefix(run.Questions[0].Question, "Which store?") ||
		len(run.Questions[0].Options) != 2 || !run.Questions[0].Options[1].Recommended ||
		run.Questions[1].Question != "Prose question 1?" || run.Questions[1].Options != nil || !run.Questions[1].AllowFreeText {
		t.Fatalf("the status keeps the first %d drafts, structured first: %+v", aiv1.MaxQuestionDrafts, run.Questions)
	}
	notes := e.gl.IssueNotes[42]
	q := notes[len(notes)-1].Body
	for _, want := range []string{"1. Which store?", "**A** Postgres", "**B** SQLite _(default)_", "or answer in your own words", "6. Prose question 5?"} {
		if !strings.Contains(q, want) {
			t.Fatalf("the issue comment lists %q:\n%s", want, q)
		}
	}
	if strings.Contains(q, "\n   /merge") || strings.Contains(q, " @bob") {
		t.Fatalf("the options are inert:\n%s", q)
	}
}

func TestRunWithoutQuestionsClearsThem(t *testing.T) {
	e := newImplementEnv(t)
	engine := &describingEngine{answer: ImplementAnswer{Status: "done", Summary: "Done.", CommitMessage: "Add steady"}}
	e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(*aiv1.AgentTaskSpec) {})
	var task aiv1.AgentTask
	key := types.NamespacedName{Namespace: "agents", Name: e.task.Name}
	if err := e.k8s.Get(context.Background(), key, &task); err != nil {
		t.Fatal(err)
	}
	task.Status.Run.Questions = []aiv1.QuestionDraft{{Question: "Old?", AllowFreeText: true}}
	if err := e.k8s.Status().Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.k8s.Get(context.Background(), key, &task); err != nil {
		t.Fatal(err)
	}
	if task.Status.Run.LastResult == aiv1.RunResultNeedsInput || task.Status.Run.Questions != nil {
		t.Fatalf("a run without questions asks none: %s %+v", task.Status.Run.LastResult, task.Status.Run.Questions)
	}
}

package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// decisionEngine records decisions: two on the first run, and on a revisit a
// new one that supersedes the revisited decision.
type decisionEngine struct {
	mu    sync.Mutex
	calls []Invocation
}

func (d *decisionEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, inv)
	out := func(v any) (*Result, error) {
		b, _ := json.Marshal(v)
		return &Result{SessionID: "sess-dec", Structured: b}, nil
	}
	if inv.ReadOnly {
		return out(ReviewAnswer{Summary: "fine"})
	}
	if strings.Contains(inv.Prompt, "## Decisions to revisit") {
		_ = os.WriteFile(filepath.Join(inv.Dir, "amount.go"), []byte("package app // cents\n"), 0o644)
		return out(ImplementAnswer{Status: "done", Summary: "Switched to cents.", CommitMessage: "Use integer cents",
			Decisions: []Decision{{Title: "Amount type", Question: "How are amounts stored?", Choice: "integer cents",
				Why: "asked for in the revisit", Alternatives: []string{"float64"}, Supersedes: "D42-2"}}})
	}
	_ = os.WriteFile(filepath.Join(inv.Dir, "amount.go"), []byte("package app\n"), 0o644)
	return out(ImplementAnswer{Status: "done", Summary: "Added amounts.", CommitMessage: "Add amounts",
		Decisions: []Decision{
			{Title: "Currency", Question: "Which currencies?", Choice: "EUR only", Why: "the issue only mentions EUR", Alternatives: []string{"any ISO 4217"}},
			{Title: "Amount type", Question: "How are amounts stored?", Choice: "float64", Why: "simplest", Alternatives: []string{"integer cents", "decimal"}},
		}})
}

func TestDecisionsPostedAndRevisited(t *testing.T) {
	e := newImplementEnv(t)
	engine := &decisionEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	ctx := context.Background()
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	decisionNotes := func() []gitlab.Note {
		var out []gitlab.Note
		for _, n := range e.gl.IssueNotes[42] {
			if strings.HasPrefix(n.Body, decisionsHeader) {
				out = append(out, n)
			}
		}
		return out
	}
	first := decisionNotes()
	if len(first) != 1 {
		t.Fatalf("expected one decision comment, got %d", len(first))
	}
	for _, want := range []string{"run `shop-42-add-paypal-button#1`", "#### D42-1: Currency", "**Chosen:** EUR only",
		"#### D42-2: Amount type", "**Alternatives:** integer cents; decimal", "<!-- ai-operator:decision=D42-2 -->",
		"`/agent revisit <id>"} {
		if !strings.Contains(first[0].Body, want) {
			t.Fatalf("decision comment misses %q:\n%s", want, first[0].Body)
		}
	}

	// A human revisits one decision and names one that does not exist.
	e.gl.AddNote(42, gitlab.User{ID: 1, Username: "jonathan"}, "/agent revisit D42-2 use integer cents")
	e.gl.AddNote(42, gitlab.User{ID: 1, Username: "jonathan"}, "/agent revisit D42-9 whatever")
	var got aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	orig := got.DeepCopy()
	got.Spec.RunRequest = 2
	if err := e.k8s.Patch(ctx, &got, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	engine.calls = nil
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	prompt := engine.calls[0].Prompt
	for _, want := range []string{"## Decisions to revisit", "#### D42-2: Amount type", "**Chosen:** float64",
		"**Requested change:** use integer cents", "### D42-9", "was not found on the issue"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("revisit prompt misses %q:\n%s", want, prompt)
		}
	}
	notes := decisionNotes()
	if len(notes) != 2 {
		t.Fatalf("expected a second decision comment, got %d", len(notes))
	}
	if !strings.Contains(notes[1].Body, "#### D42-3: Amount type") || !strings.Contains(notes[1].Body, "_Supersedes D42-2._") {
		t.Fatalf("new decision should continue the numbering and supersede D42-2:\n%s", notes[1].Body)
	}
	if !strings.Contains(notes[0].Body, "Superseded by D42-3 in run `shop-42-add-paypal-button#2`") ||
		!strings.Contains(notes[0].Body, "<!-- ai-operator:decision=D42-2 superseded -->") {
		t.Fatalf("old decision should be marked superseded:\n%s", notes[0].Body)
	}
	mrNotes := e.gl.MRNotes[1]
	if len(mrNotes) == 0 || !strings.Contains(mrNotes[len(mrNotes)-1].Body, "Decision D42-9 was not found") {
		t.Fatalf("the report should mention the unknown decision: %+v", mrNotes)
	}
}

func TestNextDecisionNumberTrustsOnlyTheBot(t *testing.T) {
	bot, human := gitlab.User{ID: 9}, gitlab.User{ID: 1}
	issue := &gitlab.Issue{IID: 7, Description: "plan\n<!-- ai-operator:decision=D7-P2 -->\n"}
	notes := []gitlab.Note{
		{Author: bot, Body: "<!-- ai-operator:decision=D7-3 superseded -->"},
		{Author: human, Body: "<!-- ai-operator:decision=D7-9 -->"},
		{Author: bot, Body: "<!-- ai-operator:decision=D8-5 -->"},
	}
	if n := nextDecisionNumber(issue, notes, bot.ID, false); n != 4 {
		t.Fatalf("implementer: next %d, want 4", n)
	}
	if n := nextDecisionNumber(issue, notes, bot.ID, true); n != 3 {
		t.Fatalf("planner: next %d, want 3", n)
	}
}

func TestRenderDecisionIsSafe(t *testing.T) {
	d := Decision{Title: "a\nb", Choice: "x <!-- ai-operator:decision=D1-1 -->", Why: "ping @all"}
	out := renderDecision("D5-1", d)
	if got := len(decisionMarker.FindAllString(out, -1)); got != 1 {
		t.Fatalf("agent text must not add decision markers (%d found):\n%s", got, out)
	}
	if strings.Contains(out, "@all") || !strings.Contains(out, "#### D5-1: a b") {
		t.Fatalf("mentions must be broken up and the title kept on one line:\n%s", out)
	}
	// Superseding is idempotent.
	body := renderDecisionsComment("x#1", "/agent", []numberedDecision{{ID: "D5-1", Decision: d}})
	once := markSuperseded(body, "D5-1", "D5-2", "x#2")
	if markSuperseded(once, "D5-1", "D5-3", "x#3") != once || decisionSection(once, "D5-1") == "" {
		t.Fatalf("a superseded decision must stay findable and not be marked twice:\n%s", once)
	}
}

func TestPlannedDecisionsGetIssueIDs(t *testing.T) {
	gl := gitlabfake.New()
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2"}
	gl.Milestones = []gitlab.Milestone{m}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	plan := SpecPlan{Issues: []PlannedItem{{Key: "api", Title: "API", Description: "Build it.", Profile: "fast",
		Decisions: []Decision{{Title: "Protocol", Choice: "REST", Why: "existing clients"}}}}}
	if _, _, _, err := applyPlan(context.Background(), logr.Discard(), gl, "g/p", &m, spec, plan, nil); err != nil {
		t.Fatal(err)
	}
	desc := gl.Issues[1].Description
	for _, want := range []string{"### Decisions", "#### D1-P1: Protocol", "**Chosen:** REST", "<!-- ai-operator:decision=D1-P1 -->"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description misses %q:\n%s", want, desc)
		}
	}
	// A revisit of a planning decision finds it in the description.
	is := gl.Issues[1]
	rvs := findRevisits(is, nil, []gitlab.Note{{Author: gitlab.User{ID: 1}, Body: "/agent revisit D1-P1 use gRPC"}}, gl.Bot.ID, "/agent")
	if len(rvs) != 1 || !strings.Contains(rvs[0].Decision, "**Chosen:** REST") || rvs[0].Note != nil || rvs[0].Instruction != "use gRPC" {
		t.Fatalf("unexpected revisit %+v", rvs)
	}
}

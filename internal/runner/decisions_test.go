package runner

import (
	"context"
	"encoding/json"
	"fmt"
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
		return out(ImplementAnswer{Status: "done", Summary: "Switched to cents.", CommitMessage: "Use integer cents", Handoff: "cents done",
			Decisions: []Decision{
				{Title: "Amount type", Question: "How are amounts stored?", Choice: "integer cents",
					Why: "asked for in the revisit", Alternatives: []string{"float64"}, Supersedes: "D42-2"},
				{Title: "Currency", Choice: "EUR and USD", Why: "asked for in the revisit", Supersedes: "D42-1"},
			}})
	}
	_ = os.WriteFile(filepath.Join(inv.Dir, "amount.go"), []byte("package app\n"), 0o644)
	return out(ImplementAnswer{Status: "done", Summary: "Added amounts.", CommitMessage: "Add amounts", Handoff: "amounts done",
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
	e.gl.AddNote(42, gitlab.User{ID: 1, Username: "jonathan"}, "/agent revisit d42-1 also USD")
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
	// Both revisited decisions of the same comment are marked.
	for _, want := range []string{"Superseded by D42-3 in run `shop-42-add-paypal-button#2`", "<!-- ai-operator:decision=D42-2 superseded -->",
		"Superseded by D42-4 in run", "<!-- ai-operator:decision=D42-1 superseded -->"} {
		if !strings.Contains(notes[0].Body, want) {
			t.Fatalf("old decisions should be marked superseded (%q):\n%s", want, notes[0].Body)
		}
	}
	// The revisit block is at the top; the new comments follow it.
	block, _, _ := strings.Cut(prompt, "There is new input")
	if strings.Contains(block, "<!-- ai-operator:decision") {
		t.Fatalf("the revisit block must not carry decision markers:\n%s", block)
	}

	// The runner state is lost: the agent notes tell which comments were
	// handled, so the old revisits are not applied again.
	if err := os.Remove(filepath.Join(e.tmp, "runner", "state.json")); err != nil {
		t.Fatal(err)
	}
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	orig = got.DeepCopy()
	got.Spec.RunRequest = 3
	if err := e.k8s.Patch(ctx, &got, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	engine.calls = nil
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if p := engine.calls[0].Prompt; strings.Contains(p, "## Decisions to revisit") {
		t.Fatalf("handled revisits must not be applied again:\n%s", p)
	}
	mentioned := false
	for _, n := range e.gl.MRNotes[1] {
		mentioned = mentioned || (strings.Contains(n.Body, "shop-42-add-paypal-button#2") && strings.Contains(n.Body, "Decision D42-9 was not found"))
	}
	if !mentioned {
		t.Fatalf("the report of run 2 should mention the unknown decision: %+v", e.gl.MRNotes[1])
	}
}

func TestNextDecisionNumberTrustsOnlyDecisionComments(t *testing.T) {
	bot, human := gitlab.User{ID: 9}, gitlab.User{ID: 1}
	issue := &gitlab.Issue{IID: 7, Description: "plan\n<!-- ai-operator:decision=D7-P2 -->\n"}
	notes := []gitlab.Note{
		{Author: bot, Body: decisionsHeader + " in run x\n<!-- ai-operator:decision=D7-3 superseded -->"},
		{Author: human, Body: decisionsHeader + "\n<!-- ai-operator:decision=D7-9 -->"},
		{Author: bot, Body: decisionsHeader + "\n<!-- ai-operator:decision=D8-5 -->"},
		// A report of the bot that quotes a marker is not a decision comment.
		{Author: bot, Body: "🤖 Summary <!-- ai-operator:decision=D7-8 -->"},
	}
	if n := nextDecisionNumber(issue, notes, bot.ID); n != 4 {
		t.Fatalf("next %d, want 4", n)
	}
}

func TestDecisionCommentsSplitWithoutCutting(t *testing.T) {
	var ds []Decision
	for i := 0; i < 10; i++ {
		ds = append(ds, Decision{Title: fmt.Sprintf("decision %d", i), Choice: strings.Repeat("x", 1500)})
	}
	bodies := renderDecisionsComments("x#1", "/agent", numberDecisions(ds, 4, false, 1))
	if len(bodies) < 2 {
		t.Fatalf("expected several comments, got %d", len(bodies))
	}
	found := 0
	for _, b := range bodies {
		if len(b) > maxDecisions || !strings.HasPrefix(b, decisionsHeader) {
			t.Fatalf("comment of %d bytes or without header:\n%.200s", len(b), b)
		}
		found += len(decisionMarker.FindAllString(b, -1))
	}
	notes := make([]gitlab.Note, len(bodies))
	for i, b := range bodies {
		notes[i] = gitlab.Note{Author: gitlab.User{ID: 9}, Body: b}
	}
	if found != 10 || nextDecisionNumber(&gitlab.Issue{IID: 4}, notes, 9) != 11 {
		t.Fatalf("every decision must be posted whole: %d markers", found)
	}
}

func TestRenderDecisionIsSafe(t *testing.T) {
	d := Decision{Title: "a\nb", Choice: "x <!-- ai-operator:decision=D1-1 --> `<!-- ai-operator:decision=D1-2 -->`",
		Why: "ping @all <details>", Alternatives: []string{"keep `<T>` as is"}}
	out := renderDecision("D5-1", d)
	if got := len(decisionMarker.FindAllString(out, -1)); got != 1 {
		t.Fatalf("agent text must not add decision markers (%d found):\n%s", got, out)
	}
	if strings.Contains(out, "@all") || !strings.Contains(out, "#### D5-1: a b") {
		t.Fatalf("mentions must be broken up and the title kept on one line:\n%s", out)
	}
	if strings.Contains(out, "<details>") || !strings.Contains(out, "&lt;details>") || !strings.Contains(out, "`<T>`") {
		t.Fatalf("HTML must be escaped outside code spans only:\n%s", out)
	}
	// Superseding is idempotent.
	body := renderDecisionsComments("x#1", "/agent", []numberedDecision{{ID: "D5-1", Decision: d}})[0]
	once := markSuperseded(body, "D5-1", "D5-2", "x#2")
	if markSuperseded(once, "D5-1", "D5-3", "x#3") != once || decisionSection(once, "D5-1") == "" {
		t.Fatalf("a superseded decision must stay findable and not be marked twice:\n%s", once)
	}
}

func TestPlannedDecisionsAreAComment(t *testing.T) {
	gl := gitlabfake.New()
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2"}
	gl.Milestones = []gitlab.Milestone{m}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	ctx := context.Background()
	plan := SpecPlan{Issues: []PlannedItem{{Key: "api", Title: "API", Description: "Build it.", Profile: "fast",
		Decisions: []Decision{{Title: "Protocol", Question: "Which protocol?", Choice: "REST", Why: "existing clients", Alternatives: []string{"gRPC"}}}}}}
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, nil, planComment{botID: gl.Bot.ID, runID: "shop-spec#1"}); err != nil {
		t.Fatal(err)
	}
	is := gl.Issues[1]
	if strings.Contains(is.Description, "#### D1-P1") || !strings.Contains(is.Description, "decisions are in a comment below") {
		t.Fatalf("the description keeps the spec only, with a pointer:\n%s", is.Description)
	}
	notes := gl.IssueNotes[1]
	if len(notes) != 1 || notes[0].Author.ID != gl.Bot.ID {
		t.Fatalf("expected one plan decisions comment of the bot, got %+v", notes)
	}
	for _, want := range []string{decisionsHeader + " of the plan in run `shop-spec#1`", "#### D1-P1: Protocol", "**Question:** Which protocol?",
		"**Chosen:** REST", "**Why:** existing clients", "**Alternatives:** gRPC", "<!-- ai-operator:decision=D1-P1 -->", planDecisionsMarker} {
		if !strings.Contains(notes[0].Body, want) {
			t.Fatalf("plan comment misses %q:\n%s", want, notes[0].Body)
		}
	}
	// A revisit of a planning decision finds it in the comment.
	rvs := findRevisits(is, notes, []gitlab.Note{{Author: gitlab.User{ID: 1}, Body: "/agent revisit D1-P1 use gRPC"}}, gl.Bot.ID, "/agent")
	if len(rvs) != 1 || !strings.Contains(rvs[0].Decision, "**Chosen:** REST") || rvs[0].Note == nil || rvs[0].Instruction != "use gRPC" {
		t.Fatalf("unexpected revisit %+v", rvs)
	}

	// A re-plan of the draft updates the comment instead of adding one; an
	// unchanged plan leaves it alone.
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	plan.Issues[0].Decisions[0].Choice = "gRPC"
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, issues, planComment{botID: gl.Bot.ID, runID: "shop-spec#2"}); err != nil {
		t.Fatal(err)
	}
	if notes := gl.IssueNotes[1]; len(notes) != 1 || !strings.Contains(notes[0].Body, "**Chosen:** gRPC") || !strings.Contains(notes[0].Body, "run `shop-spec#2`") {
		t.Fatalf("the plan comment should be updated in place: %+v", notes)
	}
	before := gl.IssueNotes[1][0].Body
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, issues, planComment{botID: gl.Bot.ID, runID: "shop-spec#3"}); err != nil {
		t.Fatal(err)
	}
	if gl.IssueNotes[1][0].Body != before {
		t.Fatalf("an unchanged plan must not touch the comment:\n%s", gl.IssueNotes[1][0].Body)
	}
	// Decisions dropped by the plan: the comment says so.
	plan.Issues[0].Decisions = nil
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, issues, planComment{botID: gl.Bot.ID, runID: "shop-spec#4"}); err != nil {
		t.Fatal(err)
	}
	if body := gl.IssueNotes[1][0].Body; !strings.Contains(body, "none since run `shop-spec#4`") || strings.Contains(body, "D1-P1") {
		t.Fatalf("dropped decisions must be removed from the comment:\n%s", body)
	}
}

func TestPlannedDecisionsInOldDescriptionsStillCount(t *testing.T) {
	is := &gitlab.Issue{IID: 1, Description: "Build it.\n\n### Decisions\n\n#### D1-P1: Protocol\n**Chosen:** REST\n<!-- ai-operator:decision=D1-P1 -->\n"}
	rvs := findRevisits(is, nil, []gitlab.Note{{Author: gitlab.User{ID: 1}, Body: "/agent revisit D1-P1 use gRPC"}}, 999, "/agent")
	if len(rvs) != 1 || !strings.Contains(rvs[0].Decision, "**Chosen:** REST") || rvs[0].Note != nil {
		t.Fatalf("planning decisions of older issues are found in the description: %+v", rvs)
	}
}

func TestRevisitParsing(t *testing.T) {
	bot, human := int64(9), gitlab.User{ID: 1}
	issue := &gitlab.Issue{IID: 4}
	decisions := gitlab.Note{ID: 1, Author: gitlab.User{ID: bot}, Body: renderDecisionsComments("x#1", "/agent",
		numberDecisions([]Decision{{Title: "A", Choice: "a"}}, 4, false, 1))[0]}
	note := func(id int64, body string) gitlab.Note { return gitlab.Note{ID: id, Author: human, Body: body} }
	rvs := findRevisits(issue, []gitlab.Note{decisions}, []gitlab.Note{
		note(2, "/agent revisit D4-1 use X"),
		note(3, "/agent: revisit d4-1 actually use Y"),
		note(4, "/agent revisit D4-1.5 nope"),
		note(5, "/agent please revisit the currency"),
		note(6, "/agentrevisit D4-1"),
		note(7, "/agent revisit D4-1x"),
	}, bot, "/agent")
	if len(rvs) != 4 {
		t.Fatalf("expected one revisit and three malformed ones, got %+v", rvs)
	}
	if rvs[0].ID != "D4-1" || rvs[0].Instruction != "actually use Y" || rvs[0].Decision == "" {
		t.Fatalf("the later revisit of a decision must win: %+v", rvs[0])
	}
	for _, rv := range rvs[1:] {
		if !rv.Malformed {
			t.Fatalf("expected a malformed revisit, got %+v", rv)
		}
	}
}

func TestCleanDecisionTextUnbalancedBackticks(t *testing.T) {
	if got := cleanDecisionText("use `foo <!-- x"); strings.Contains(got, "<!--") {
		t.Fatalf("an unbalanced backtick must not switch escaping off: %q", got)
	}
	if got := cleanDecisionText("keep `@Override <T>` and ping @all"); !strings.Contains(got, "`@Override <T>`") || strings.Contains(got, " @all") {
		t.Fatalf("code spans stay as they are, mentions outside are broken: %q", got)
	}
}

// followUpEngine records one decision that supersedes the decision named in
// supersedes.
type followUpEngine struct{ supersedes string }

func (f followUpEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	var v any = ReviewAnswer{Summary: "fine"}
	if !inv.ReadOnly {
		_ = os.WriteFile(filepath.Join(inv.Dir, "proto.go"), []byte("package app // grpc\n"), 0o644)
		v = ImplementAnswer{Status: "done", Summary: "Switched to gRPC.", CommitMessage: "Use gRPC", Handoff: "grpc done",
			Decisions: []Decision{{Title: "Protocol", Choice: "gRPC", Why: "asked for in the revisit", Supersedes: f.supersedes}}}
	}
	b, _ := json.Marshal(v)
	return &Result{SessionID: "sess-fu", Structured: b}, nil
}

func TestFollowUpSupersedesTheMergedDecision(t *testing.T) {
	for _, id := range []string{"D7-1", "D7-P1"} {
		t.Run(id, func(t *testing.T) {
			e := newImplementEnv(t)
			section := fmt.Sprintf("#### %s: Protocol\n**Chosen:** REST\n<!-- ai-operator:decision=%s -->\n", id, id)
			old := e.gl.AddIssue(7, "API")
			old.State = "closed"
			if strings.Contains(id, "-P") {
				old.Description = "Build it.\n\n### Decisions\n\n" + section
			} else {
				e.gl.IssueNotes[7] = []gitlab.Note{{ID: 70, Author: e.gl.Bot, Body: decisionsHeader + " in run `shop-7-api#1`.\n\n" + section}}
			}
			e.gl.Issues[42].Description = fmt.Sprintf("Follow-up of #7.\n<!-- ai-operator:revisit=%s -->\n", id)
			e.gl.Issues[42].Author = e.gl.Bot // only the bot's follow-ups count
			r := newTestRunner(t, e.tmp, e.k8s, e.gl, followUpEngine{supersedes: id}, e.task.Name)
			if _, err := r.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			body := e.gl.Issues[7].Description
			if !strings.Contains(id, "-P") {
				body = e.gl.IssueNotes[7][0].Body
			}
			for _, want := range []string{"Superseded by D42-1 in run `shop-42-add-paypal-button#1`",
				fmt.Sprintf("<!-- ai-operator:decision=%s superseded -->", id)} {
				if !strings.Contains(body, want) {
					t.Fatalf("%s on #7 should be marked superseded (%q):\n%s", id, want, body)
				}
			}
		})
	}
}

func TestRevisitMarkerOfAHumanIssueIsIgnored(t *testing.T) {
	e := newImplementEnv(t)
	section := "#### D7-1: Protocol\n**Chosen:** REST\n<!-- ai-operator:decision=D7-1 -->\n"
	old := e.gl.AddIssue(7, "API")
	old.State = "closed"
	e.gl.IssueNotes[7] = []gitlab.Note{{ID: 70, Author: e.gl.Bot, Body: decisionsHeader + " in run `shop-7-api#1`.\n\n" + section}}
	e.gl.Issues[42].Description = "<!-- ai-operator:revisit=D7-1 -->"
	e.gl.Issues[42].Author = gitlab.User{ID: 1, Username: "jonathan"}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, followUpEngine{supersedes: "D7-1"}, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.gl.IssueNotes[7][0].Body, "superseded") {
		t.Fatalf("a marker in a human's issue must not supersede decisions elsewhere:\n%s", e.gl.IssueNotes[7][0].Body)
	}
}

func TestPlanDecisionsReachReviewersAndAreLabelled(t *testing.T) {
	botID := int64(999)
	ds := numberDecisions([]Decision{{Title: "Protocol", Choice: "REST", Why: "existing clients"}}, 5, true, 1)
	notes := []gitlab.Note{{ID: 1, Author: gitlab.User{ID: botID}, Body: renderPlanDecisions("shop-spec#1", "/agent", ds)}}
	text := planDecisionsText(notes, botID)
	if !strings.Contains(text, "#### D5-P1: Protocol") || strings.Contains(text, "<!--") || strings.Contains(text, "shop-spec#1") {
		t.Fatalf("unexpected decisions text:\n%s", text)
	}
	prompt := buildReviewPrompt(&gitlab.Issue{IID: 5, Title: "API", Description: "Build it."}, text, "main", "a.go | 1 +", "+x", defaultInlineDiff)
	if !strings.Contains(prompt, "## Decisions of the planning agent") || !strings.Contains(prompt, "**Chosen:** REST") {
		t.Fatalf("reviewers should see the planner's decisions:\n%s", prompt)
	}
	if th := thread("Comments", notes, botID); !strings.Contains(th, "**planning agent**") {
		t.Fatalf("the plan comment should be labelled as the planner's:\n%s", th)
	}
	if planDecisionsText(nil, botID) != "" {
		t.Fatal("no plan comment, no text")
	}
}

func TestReplanLeavesHandedOverIssuesAlone(t *testing.T) {
	gl := gitlabfake.New()
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2"}
	gl.Milestones = []gitlab.Milestone{m}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	ctx := context.Background()
	plan := SpecPlan{Issues: []PlannedItem{{Key: "api", Title: "API", Description: "Build it.",
		Decisions: []Decision{{Title: "Protocol", Choice: "REST"}}}}}
	pc := planComment{botID: gl.Bot.ID, runID: "shop-spec#1"}
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, nil, pc); err != nil {
		t.Fatal(err)
	}
	// Labels that are not scoped: the draft label stays next to working.
	gl.SetLabels(1, "ai-generated", "agent::draft", "agent::working")
	before := gl.IssueNotes[1][0].Body
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	plan.Issues[0].Decisions[0].Choice = "gRPC"
	_, _, skipped, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, issues, planComment{botID: gl.Bot.ID, runID: "shop-spec#2"})
	if err != nil || skipped != 1 || gl.IssueNotes[1][0].Body != before {
		t.Fatalf("an issue in progress keeps its plan and decisions: skipped %d, err %v", skipped, err)
	}
}

func TestImplementerIsToldItsLimits(t *testing.T) {
	e := newImplementEnv(t)
	engine := &decisionEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	r.AgentMemoryLimit, r.AgentCPUs = "6Gi", "2"
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	writes := 0
	for _, inv := range engine.calls {
		if !inv.ReadOnly {
			writes++
		}
		if told := strings.Contains(inv.SystemPrompt, "a memory limit of 6Gi and 2 CPUs"); told == inv.ReadOnly {
			t.Fatalf("only the implementer runs builds and tests and is told (readOnly=%v): %q", inv.ReadOnly, inv.SystemPrompt)
		}
	}
	if writes == 0 {
		t.Fatal("expected implementer invocations")
	}
}

package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// revisitDecisions are two decisions of #1: D1-1 with alternatives, D1-2
// without.
const revisitDecisions = "📌 **Decisions** in run `shop-1-api#1`.\n\n" +
	"#### D1-1: Protocol\n**Question:** Which protocol?\n**Chosen:** REST\n**Why:** simplest\nand known\n**Alternatives:** gRPC; GraphQL\n" +
	"<!-- ai-operator:decision=D1-1 -->\n\n" +
	"#### D1-2: Store\n**Chosen:** Postgres\n<!-- ai-operator:decision=D1-2 -->\n"

func revisitBody(decision, text string) string {
	if text == "" {
		return fmt.Sprintf(`{"cmd":"revisit","decision":%q}`, decision)
	}
	return fmt.Sprintf(`{"cmd":"revisit","decision":%q,"text":%q}`, decision, text)
}

// newDeskRevisitEnv is the answer env with #1 merged and closed, its
// decisions recorded.
func newDeskRevisitEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newAnswerEnv(t)
	e.gl.Issues[1].State = "closed"
	e.gl.AddNote(1, e.gl.Bot, revisitDecisions)
	e.reconcile()
	return e
}

// followUps lists the issues revisiting id.
func followUps(e *projectEnv, id string) []*gitlab.Issue {
	var out []*gitlab.Issue
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit "+id) {
			out = append(out, is)
		}
	}
	return out
}

func TestDeskRevisitWithTextIsTheCommentsRevisit(t *testing.T) {
	e := newDeskRevisitEnv(t)
	tracking := e.tracking()
	if r := e.deskCommand(301, "mallory@x", revisitBody("D1-1", "use gRPC")); r != "Only the milestone's owners can revisit decisions." {
		t.Fatalf("a non-owner is refused: %q", r)
	}
	if fs := followUps(e, "D1-1"); len(fs) != 0 {
		t.Fatalf("the refused revisit creates nothing: %v", fs)
	}
	if r := e.deskCommand(302, "alice@example.com", revisitBody("D1-1", "use gRPC, cc @everyone\n/close")); r != fmt.Sprintf("Revisit of D1-1 queued; the outcome follows on #%d.", tracking) {
		t.Fatalf("the owner's revisit is queued: %q", r)
	}
	cmds := e.task(specTaskName).Status.Pilot.Commands
	if i := slices.IndexFunc(cmds, func(c aiv1.PilotCommand) bool { return c.Action.Tool == "revisit" }); i < 0 {
		t.Fatalf("a revisit command: %+v", cmds)
	} else if c := cmds[i]; c.Action.Decision != "D1-1" || c.Action.Note != "use gRPC, cc @everyone\n/close" || c.Owner != "alice via the Desk" || c.OnIssue != tracking {
		t.Fatalf("the command names the decision, the text, the owner and the tracking issue: %+v", c)
	}
	e.reconcile()
	fs := followUps(e, "D1-1")
	if len(fs) != 1 {
		t.Fatalf("one follow-up: %v", fs)
	}
	for _, want := range []string{"requested by @alice via the Desk.", "**Chosen:** REST", "### Requested change\n\nuse gRPC"} {
		if !strings.Contains(fs[0].Description, want) {
			t.Fatalf("the follow-up misses %q:\n%s", want, fs[0].Description)
		}
	}
	if strings.Contains(fs[0].Description, "via the pilot") || strings.Contains(fs[0].Description, "\n/close") || strings.Contains(fs[0].Description, " @everyone") {
		t.Fatalf("the follow-up names the Desk and runs nothing:\n%s", fs[0].Description)
	}
	// The follow-up can start, so the stuck pause (#2 waits for the skipped #1)
	// lifts after the outcome.
	if !strings.Contains(trackingComments(e), "to revisit D1-1") {
		t.Fatalf("the outcome is on the tracking issue:\n%s", trackingComments(e))
	}
}

func TestDeskRevisitOpensAPromptOnce(t *testing.T) {
	e := newDeskRevisitEnv(t)
	if r := e.deskCommand(301, "alice@example.com", revisitBody("d1-1", "")); r != "Q1 opened for D1-1." {
		t.Fatalf("a revisit prompt: %q", r)
	}
	p := e.prompt(specTaskName, "Q1")
	if p.Source != (aiv1.PromptSource{Kind: aiv1.PromptRevisit, IssueIID: 1, Key: "D1-1"}) || p.Question != "Revisit D1-1: Protocol?" || !p.AllowFreeText {
		t.Fatalf("the prompt is about D1-1 of #1: %+v", p)
	}
	if want := "**Question:** Which protocol?\n\n**Chosen:** REST\n\n**Why:** simplest\nand known"; p.Context != want {
		t.Fatalf("the context is the recorded decision:\n%s\nwant:\n%s", p.Context, want)
	}
	if got := answerLabels(p); got != "A:Keep REST* B:Switch to gRPC C:Switch to GraphQL" {
		t.Fatalf("keep, or switch to an alternative: %s", got)
	}
	if a := p.Options[0].Actions; len(a) != 0 {
		t.Fatalf("keeping does nothing: %+v", a)
	}
	if a := p.Options[1].Actions; len(a) != 1 || a[0] != (aiv1.PilotAction{Tool: "revisit", Decision: "D1-1", Note: "Choose instead: gRPC"}) {
		t.Fatalf("switching revisits the decision: %+v", a)
	}

	if r := e.deskCommand(302, "alice@example.com", revisitBody("D1-1", "")); r != "Q1 is already open for D1-1." {
		t.Fatalf("a second click names the open prompt: %q", r)
	}
	if n := len(e.task(specTaskName).Status.Prompts); n != 1 {
		t.Fatalf("no second prompt: %d", n)
	}

	if r := e.deskCommand(303, "alice@example.com", answerBody("Q1", "B")); r != "Q1 answered: Switch to gRPC. The outcome follows on the prompt." {
		t.Fatalf("switching: %q", r)
	}
	e.reconcile()
	e.reconcile()
	fs := followUps(e, "D1-1")
	if len(fs) != 1 || !strings.Contains(fs[0].Description, "### Requested change\n\nChoose instead: gRPC") ||
		!strings.Contains(fs[0].Description, "requested by @alice via the pilot") {
		t.Fatalf("one follow-up asking for the alternative: %v", fs)
	}
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptAnswered {
		t.Fatalf("the prompt is answered, its decision's issue closed or not: %+v", p)
	}
	// Answered, a click opens a new one.
	if r := e.deskCommand(304, "alice@example.com", revisitBody("D1-1", "")); r != "Q2 opened for D1-1." {
		t.Fatalf("a new revisit prompt: %q", r)
	}
}

func TestDeskRevisitFreeTextIsAnOptionToConfirm(t *testing.T) {
	e := newDeskRevisitEnv(t)
	if r := e.deskCommand(301, "alice@example.com", revisitBody("D1-2", "")); r != "Q1 opened for D1-2." {
		t.Fatalf("a revisit prompt: %q", r)
	}
	if got := answerLabels(e.prompt(specTaskName, "Q1")); got != "A:Keep Postgres* B:Decide later" {
		t.Fatalf("without alternatives: keep or decide later: %s", got)
	}
	runs := e.pilot().Spec.RunRequest
	if r := e.deskText(302, "alice@example.com", textBody("Q1", "use SQLite")); r != "Q1 has option C from your answer; confirm it there." {
		t.Fatalf("the text is an option: %q", r)
	}
	if r := e.deskText(303, "alice@example.com", textBody("Q1", "use SQLite, cc @everyone")); r != "Q1 has option C from your answer; confirm it there." {
		t.Fatalf("a second text replaces it: %q", r)
	}
	if pilot := e.pilot(); pilot.Spec.RunRequest != runs || len(pilot.Spec.Source.PromptRefines) != 0 {
		t.Fatalf("no pilot turn: run request %d → %d, refines %+v", runs, pilot.Spec.RunRequest, pilot.Spec.Source.PromptRefines)
	}
	p := e.prompt(specTaskName, "Q1")
	if got := answerLabels(p); got != "A:Keep Postgres* B:Decide later C:From your answer: revisit D1-2 asking for: use SQLite, cc "+inert("@everyone")+"+" {
		t.Fatalf("the option from the answer follows: %s", got)
	}
	if a := p.Options[2].Actions; len(a) != 1 || a[0] != (aiv1.PilotAction{Tool: "revisit", Decision: "D1-2", Note: "use SQLite, cc @everyone"}) {
		t.Fatalf("it revisits the decision with the text: %+v", a)
	}
	if p.State != aiv1.PromptOpen || len(followUps(e, "D1-2")) != 0 {
		t.Fatalf("nothing runs before the owner confirms: %+v", p)
	}

	if r := e.deskCommand(304, "alice@example.com", answerBody("Q1", "C")); !strings.HasPrefix(r, "Q1 answered: From your answer: revisit D1-2") {
		t.Fatalf("confirming: %q", r)
	}
	e.reconcile()
	e.reconcile()
	fs := followUps(e, "D1-2")
	if len(fs) != 1 || !strings.Contains(fs[0].Description, "### Requested change\n\nuse SQLite, cc") || strings.Contains(fs[0].Description, " @everyone") {
		t.Fatalf("one follow-up asking for the text, inert: %v", fs)
	}
}

func TestDeskRevisitWithoutTheDecisionText(t *testing.T) {
	e := newDeskRevisitEnv(t)
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Items[0].Decisions = []aiv1.DecisionRef{{ID: "D1-9", Title: "Cache"}}
	})
	if r := e.deskCommand(301, "alice@example.com", revisitBody("D1-9", "")); r != "Q1 opened for D1-9." {
		t.Fatalf("a revisit prompt: %q", r)
	}
	p := e.prompt(specTaskName, "Q1")
	if p.Question != "Revisit D1-9: Cache?" || !strings.Contains(p.Context, "Decision text not found on #1") {
		t.Fatalf("the queue's title, and the text not found: %+v", p)
	}
	if got := answerLabels(p); got != "A:Keep the decision* B:Decide later" || !p.AllowFreeText {
		t.Fatalf("keep, decide later or free text: %s", got)
	}
}

func TestDeskRevisitRefusals(t *testing.T) {
	e := newDeskRevisitEnv(t)
	e.gl.AddNote(1, e.gl.Bot, "📌 **Decisions** in run `x`.\n\n#### D1-3: Old\n**Chosen:** a\n<!-- ai-operator:decision=D1-3 superseded -->\n")
	for i, tc := range []struct{ body, want string }{
		{revisitBody("D99-1", ""), "Decision D99-1 belongs to #99, which does not exist."},
		{revisitBody("D2-1", ""), "#2 is still open: comment `/agent revisit D2-1 …` there, and its next run changes the decision."},
		{revisitBody("D1-3", ""), "Decision D1-3 was already superseded; revisit the decision that replaced it."},
	} {
		if r := e.deskCommand(int64(301+i), "alice@example.com", tc.body); r != tc.want {
			t.Errorf("%s: %q, want %q", tc.body, r, tc.want)
		}
	}
	if n := len(e.task(specTaskName).Status.Prompts); n != 0 {
		t.Fatalf("no prompt: %d", n)
	}
}

func TestParseDecision(t *testing.T) {
	d := parseDecision("#### D4-P2: Title here\n**Chosen:** one\ntwo\n**Alternatives:** x; y;  ; z\n_Supersedes D4-1._\n<!-- ai-operator:decision=D4-P2 -->\n")
	if d.Title != "Title here" || d.Question != "" || d.Choice != "one\ntwo" || d.Why != "" || !slices.Equal(d.Alternatives, []string{"x", "y", "z"}) {
		t.Fatalf("%+v", d)
	}
	if d := parseDecision(""); d.Title != "" || d.Choice != "" || d.Alternatives != nil {
		t.Fatalf("nothing: %+v", d)
	}
}

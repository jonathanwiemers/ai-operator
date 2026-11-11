package controller

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// newBudgetPromptEnv is newAnswerEnv (alice@example.com answers in the
// Desk) with the line "/agent budget <usd>" and spent spent.
func newBudgetPromptEnv(t *testing.T, usd string, spent float64) (*projectEnv, *fakeSpend) {
	t.Helper()
	e := newAnswerEnv(t)
	src := &fakeSpend{spend: store.Spend{Totals: store.SpendTotals{CostUSD: store.USD(spent)}}}
	e.r.Spend = src
	e.setBudget(usd)
	e.reconcile()
	return e, src
}

// setBudget sets the milestone description's budget line, keeping its
// other lines.
func (e *projectEnv) setBudget(usd string) {
	e.gl.Milestones[0].Description = rules.SetBudget(e.gl.Milestones[0].Description, "/agent", usd)
}

// budgetPrompts are the budget prompts of the milestone, in order.
func (e *projectEnv) budgetPrompts() []aiv1.Prompt {
	e.t.Helper()
	var out []aiv1.Prompt
	for _, p := range e.task(specTaskName).Status.Prompts {
		if p.Source.Kind == aiv1.PromptBudget {
			out = append(out, p)
		}
	}
	return out
}

func TestBudgetAmounts(t *testing.T) {
	for _, c := range []struct {
		budget, spent float64
		lo, hi        string
	}{
		{40, 32, "41", "48"},      // 1.25 × 32 is the budget: at least budget + 1
		{40, 33.1, "42", "50"},    // rounded up to whole dollars
		{40, 41, "52", "62"},      // over the budget
		{40, 40, "50", "60"},      // exactly 1.25 × 40, no float rounding up
		{1, 0.8, "2", "3"},        // the second stays above the first
		{12.5, 10, "14", "15"},    // a budget with cents
		{100, 0.01, "101", "102"}, // nearly nothing spent
	} {
		if lo, hi := budgetAmounts(c.budget, c.spent); lo != c.lo || hi != c.hi {
			t.Errorf("budgetAmounts(%v, %v) = %s, %s; want %s, %s", c.budget, c.spent, lo, hi, c.lo, c.hi)
		}
	}
}

func TestBudgetPromptAt80PercentOncePerBudget(t *testing.T) {
	e, src := newBudgetPromptEnv(t, "40", 30)
	if ps := e.budgetPrompts(); len(ps) != 0 {
		t.Fatalf("75 %% spent asks nothing: %+v", ps)
	}

	src.spend.Totals.CostUSD.USD = 32.4
	e.reconcile()
	ps := e.budgetPrompts()
	if len(ps) != 1 {
		t.Fatalf("80 %% spent posts one budget prompt: %+v", ps)
	}
	p := ps[0]
	if p.State != aiv1.PromptOpen || p.Source.Key != "budget/80/40.0000" || p.Source.IssueIID != 0 || !strings.Contains(p.Question, "$32.40 of its budget of $40.00 (81 %)") {
		t.Fatalf("the prompt is about the milestone's budget: %+v", p)
	}
	if got, want := optionLabels(p), "A:Raise the budget to $41 B:Raise the budget to $49 C:Keep going* D:Pause now"; got != want {
		t.Fatalf("options:\n%s\nwant\n%s", got, want)
	}
	if a := p.Options[1].Actions; len(a) != 1 || a[0] != (aiv1.PilotAction{Tool: "raiseBudget", Amount: "49"}) {
		t.Fatalf("a raise sets the amount: %+v", a)
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.Budget.WarnedAt == nil {
		t.Fatalf("80 %% is a heads-up, the autopilot runs: %+v", ap)
	}
	if !strings.Contains(trackingComments(e), "$32.40 of its budget of $40.00") {
		t.Fatalf("the prompt is mirrored on the tracking issue:\n%s", trackingComments(e))
	}

	src.spend.Totals.CostUSD.USD = 35
	e.reconcile()
	if ps := e.budgetPrompts(); len(ps) != 1 || ps[0].State != aiv1.PromptOpen {
		t.Fatalf("a budget is warned about once: %+v", ps)
	}

	// The owner raises it in one click; the prompt closes, and the new
	// budget, 85 % spent, warns again.
	if r := e.deskCommand(401, "alice@example.com", answerBody(p.ID, "B")); r != p.ID+" answered: Raise the budget to $49. The outcome follows on the prompt." {
		t.Fatalf("the owner's answer: %q", r)
	}
	if desc := e.gl.Milestones[0].Description; !strings.Contains(desc, "\n/agent budget 49") || strings.Contains(desc, "budget 40") {
		t.Fatalf("the raise rewrites the budget line:\n%s", desc)
	}
	if got := e.prompt(specTaskName, p.ID); got.State != aiv1.PromptAnswered || !strings.Contains(got.Answer.Outcome, "**raiseBudget** to $49.00") {
		t.Fatalf("the prompt is answered with the outcome: %+v", got)
	}
	planned := e.task(specTaskName).Spec.RunRequest
	e.reconcile()
	e.reconcile()
	if st := e.task(specTaskName); st.Spec.BudgetUSD != "49.0000" || st.Spec.RunRequest != planned {
		t.Fatalf("the new budget re-plans nothing: %+v", st.Spec)
	}
	ps = e.budgetPrompts()
	if len(ps) != 1 || ps[0].State != aiv1.PromptAnswered {
		t.Fatalf("35 of 49 is under 80 %%: no new prompt: %+v", ps)
	}
	src.spend.Totals.CostUSD.USD = 40
	e.reconcile()
	if ps = e.budgetPrompts(); len(ps) != 2 || ps[1].Source.Key != "budget/80/49.0000" || ps[1].State != aiv1.PromptOpen {
		t.Fatalf("the new budget is warned about once more: %+v", ps)
	}
}

func TestBudgetPromptIsSupersededByANewBudget(t *testing.T) {
	e, src := newBudgetPromptEnv(t, "40", 33)
	if ps := e.budgetPrompts(); len(ps) != 1 || ps[0].State != aiv1.PromptOpen {
		t.Fatalf("80 %% spent: %+v", ps)
	}
	e.setBudget("100") // by hand
	e.reconcile()
	e.reconcile()
	if ps := e.budgetPrompts(); len(ps) != 1 || ps[0].State != aiv1.PromptSuperseded {
		t.Fatalf("another budget supersedes the prompt: %+v", ps)
	}

	// Back to a budget asked about before: it is not asked again.
	e.setBudget("40")
	e.reconcile()
	e.reconcile()
	if ps := e.budgetPrompts(); len(ps) != 1 {
		t.Fatalf("a budget value is asked about once: %s", promptStates(e.task(specTaskName)))
	}

	// At 100 % the pause asks; the 80 % prompt of the same budget gives way.
	e, src = newBudgetPromptEnv(t, "40", 33)
	src.spend.Totals.CostUSD.USD = 41
	e.reconcile()
	e.reconcile()
	ps := e.budgetPrompts()
	if len(ps) != 2 || ps[0].Source.Key != "budget/80/40.0000" || ps[0].State != aiv1.PromptSuperseded ||
		ps[1].Source.Key != "budget/100/40.0000" || ps[1].State != aiv1.PromptOpen {
		t.Fatalf("the 100 %% prompt follows the 80 %% one: %s", promptStates(e.task(specTaskName)))
	}
}

func TestBudgetPauseAsksAndARaiseResumes(t *testing.T) {
	e, src := newBudgetPromptEnv(t, "40", 41)
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseBudget {
		t.Fatalf("a used budget pauses: %+v", ap)
	}
	ps := e.budgetPrompts()
	if len(ps) != 1 || ps[0].Source.Key != "budget/100/40.0000" || !strings.Contains(ps[0].Question, "used its budget of $40.00 ($41.00 spent), so autopilot is paused") {
		t.Fatalf("the pause comes with a prompt: %+v", ps)
	}
	p := ps[0]
	if got, want := optionLabels(p), "A:Raise the budget to $52* B:Raise the budget to $62 C:End the milestone here D:Stay paused"; got != want {
		t.Fatalf("options:\n%s\nwant\n%s", got, want)
	}
	e.reconcile()
	if ps := e.budgetPrompts(); len(ps) != 1 {
		t.Fatalf("the pause asks once: %+v", ps)
	}

	if r := e.deskCommand(402, "alice@example.com", answerBody(p.ID, "A")); !strings.HasPrefix(r, p.ID+" answered: Raise the budget to $52.") {
		t.Fatalf("the owner's answer: %q", r)
	}
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.PauseKind != "" || ap.Budget.LimitUSD != "52.0000" {
		t.Fatalf("the raise resumes the autopilot: %+v", ap)
	}
	if !strings.Contains(trackingComments(e), "Autopilot continues: the budget is $52.00 now ($41.00 spent) (ended by the operator: the cause went away).") {
		t.Fatalf("the resume is explained:\n%s", trackingComments(e))
	}
	if ps := e.budgetPrompts(); len(ps) != 1 || ps[0].State != aiv1.PromptAnswered {
		t.Fatalf("41 of 52 is under 80 %%: nothing new: %+v", ps)
	}
	_ = src
}

func TestRaiseBudgetReadsTheLineAgain(t *testing.T) {
	e, _ := newBudgetPromptEnv(t, "40", 41)
	var project aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project); err != nil {
		t.Fatal(err)
	}
	st := e.task(specTaskName)
	// Raised by hand above the option meanwhile: the raise keeps it.
	e.setBudget("100")
	if err := e.r.raiseBudget(e.ctx, e.gl, project.Spec.WithDefaults(), st, "52"); err == nil || err.Error() != "the budget is $100.00 already" {
		t.Fatalf("a raise never lowers the budget: %v", err)
	}
	// Removed by hand: no budget comes back.
	e.gl.Milestones[0].Description = "/agent autopilot\nBuild a new checkout."
	if err := e.r.raiseBudget(e.ctx, e.gl, project.Spec.WithDefaults(), st, "52"); err == nil || !strings.Contains(err.Error(), "no budget line") {
		t.Fatalf("a removed budget stays removed: %v", err)
	}
	if desc := e.gl.Milestones[0].Description; desc != "/agent autopilot\nBuild a new checkout." {
		t.Fatalf("a refused raise changes nothing:\n%s", desc)
	}
}

func TestEndMilestoneAtTheBudgetPause(t *testing.T) {
	e, _ := newBudgetPromptEnv(t, "40", 41)
	p := e.budgetPrompts()[0]
	if e.autopilot().Items[0].State != aiv1.AutopilotItemActive {
		t.Fatalf("#1 is active: %+v", e.autopilot().Items)
	}
	e.reconcile()
	if task := e.implTaskOf(1); !task.Spec.BudgetStop {
		t.Fatal("the pause stops #1's runs")
	}

	if r := e.deskCommand(404, "alice@example.com", answerBody(p.ID, "C")); !strings.HasPrefix(r, p.ID+" answered: End the milestone here.") {
		t.Fatalf("the owner's answer: %q", r)
	}
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotDone || ap.PauseKind != "" {
		t.Fatalf("the milestone ends here: %+v", ap)
	}
	for _, it := range ap.Items {
		if it.State != aiv1.AutopilotItemSkipped {
			t.Fatalf("the active and the pending issues are skipped: %+v", ap.Items)
		}
	}
	if e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatal("no new issue is picked")
	}
	comments := trackingComments(e)
	if !strings.Contains(comments, "The milestone ends here (@alice via the pilot): #1, #2 skipped.") {
		t.Fatalf("the end is explained:\n%s", comments)
	}
	if !strings.Contains(comments, "Autopilot is done") {
		t.Fatalf("the autopilot is done:\n%s", comments)
	}
	e.reconcile()
	if task := e.implTaskOf(1); !task.Spec.BudgetStop {
		t.Fatal("with the budget used, the done milestone's runs stay stopped")
	}
}

func TestEndMilestoneWhileRunningFinishesTheActiveIssue(t *testing.T) {
	e, _ := newBudgetPromptEnv(t, "40", 1)
	e.releaseOff()
	st := e.task(specTaskName)
	if err := e.r.queueCommand(e.ctx, st, aiv1.PilotCommand{Action: aiv1.PilotAction{Tool: "endMilestone"}, Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[1].IID != 2 || ap.Items[1].State != aiv1.AutopilotItemSkipped || ap.Items[0].State != aiv1.AutopilotItemActive {
		t.Fatalf("the pending issue is skipped, the active one goes on: %+v", ap)
	}
	if !strings.Contains(trackingComments(e), "The milestone ends here (@alice via the pilot): #2 skipped. Autopilot picks no further issue and is done after #1.") {
		t.Fatalf("the end is explained:\n%s", trackingComments(e))
	}
	e.finishRun(1, "sha-api", nil)
	e.reconcile()
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotDone || e.gl.Issues[2].HasLabel("agent::ready") {
		t.Fatalf("after the active issue the milestone is done, #2 never starts: %+v", ap)
	}
}

func TestOwnerToolsAreNotTheModels(t *testing.T) {
	e, _ := newBudgetPromptEnv(t, "40", 1)
	prompt := func() aiv1.Prompt {
		return aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment}, Question: "money?", Options: []aiv1.PromptOption{
			{Label: "Raise", Recommended: true, Actions: []aiv1.PilotAction{{Tool: "raiseBudget", Amount: "100"}}},
			{Label: "End", Actions: []aiv1.PilotAction{{Tool: "endMilestone"}}},
			{Label: "Do nothing"},
		}}
	}
	p := prompt()
	if _, err := e.validOptions(&p, nil, true); err == nil || !strings.Contains(err.Error(), "`raiseBudget` is only offered by the operator") {
		t.Fatalf("model-made options cannot raise the budget or end the milestone: %v %+v", err, p.Options)
	}
	p = prompt()
	if _, err := e.validOptions(&p, nil, false); err != nil || len(p.Options) != 3 {
		t.Fatalf("the operator's own options can: %v %+v", err, p.Options)
	}

	// A raise below the budget, or of no amount, is refused.
	st := e.task(specTaskName)
	for _, amount := range []string{"40", "12", "", "lots"} {
		if err := validAction(aiv1.PilotAction{Tool: "raiseBudget", Amount: amount}, st, nil, nil); err == nil {
			t.Errorf("raiseBudget %q must be refused", amount)
		}
	}
	if err := validAction(aiv1.PilotAction{Tool: "raiseBudget", Amount: "41"}, st, nil, nil); err != nil {
		t.Errorf("raiseBudget above the budget: %v", err)
	}
}

func TestBudgetResolved(t *testing.T) {
	st := func(limit, spent string) *aiv1.AgentTask {
		ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
		if limit != "" {
			ap.Budget = &aiv1.BudgetStatus{LimitUSD: limit, SpentUSD: spent}
		}
		return &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: ap}}
	}
	at := func(key string) aiv1.Prompt {
		return aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptBudget, Key: key}}
	}
	for name, c := range map[string]struct {
		p    aiv1.Prompt
		st   *aiv1.AgentTask
		want bool
	}{
		"80 %, still":          {at("budget/80/40.0000"), st("40.0000", "33.0000"), false},
		"80 %, under it":       {at("budget/80/40.0000"), st("40.0000", "31.0000"), true},
		"80 %, used":           {at("budget/80/40.0000"), st("40.0000", "40.0000"), true},
		"80 %, another budget": {at("budget/80/40.0000"), st("50.0000", "33.0000"), true},
		"no budget any more":   {at("budget/80/40.0000"), st("", ""), true},
		"unknown spend":        {at("budget/80/40.0000"), st("40.0000", ""), false},
		"100 %, still":         {at("budget/100/40.0000"), st("40.0000", "41.0000"), false},
		"100 %, another":       {at("budget/100/40.0000"), st("52.0000", "41.0000"), true},
		"no spec task":         {at("budget/100/40.0000"), nil, false},
		"unknown key":          {at("something"), st("", ""), false},
	} {
		if got := budgetResolved(c.p, c.st); got != c.want {
			t.Errorf("%s: budgetResolved = %v, want %v", name, got, c.want)
		}
	}
}

// D83-P1: the 80 % prompt is posted once per budget value. Falling back
// under 80 % supersedes it, and crossing 80 % again with the same budget
// posts nothing: WarnedAt holds until the budget changes.
func TestBudgetPromptHysteresisAtTheSameBudget(t *testing.T) {
	e, src := newBudgetPromptEnv(t, "40", 33)
	ps := e.budgetPrompts()
	if len(ps) != 1 || ps[0].State != aiv1.PromptOpen || e.autopilot().Budget.WarnedAt == nil {
		t.Fatalf("80 %% spent posts one prompt: %s", promptStates(e.task(specTaskName)))
	}
	comments := strings.Count(trackingComments(e), "of its budget of $40.00")

	src.spend.Totals.CostUSD.USD = 31 // under 80 % again
	e.reconcile()
	e.reconcile()
	if ps := e.budgetPrompts(); len(ps) != 1 || ps[0].State != aiv1.PromptSuperseded {
		t.Fatalf("under 80 %% the prompt closes: %s", promptStates(e.task(specTaskName)))
	}
	if e.autopilot().Budget.WarnedAt == nil {
		t.Fatalf("the budget stays warned about")
	}

	src.spend.Totals.CostUSD.USD = 34 // over 80 % again, same budget
	e.reconcile()
	e.reconcile()
	if ps := e.budgetPrompts(); len(ps) != 1 || ps[0].State != aiv1.PromptSuperseded {
		t.Fatalf("the same budget is not warned about again: %s", promptStates(e.task(specTaskName)))
	}
	if n := strings.Count(trackingComments(e), "of its budget of $40.00"); n != comments {
		t.Fatalf("no second comment: %d, was %d\n%s", n, comments, trackingComments(e))
	}

	// A new budget re-arms it.
	e.setBudget("42")
	e.reconcile()
	src.spend.Totals.CostUSD.USD = 34.5
	e.reconcile()
	e.reconcile()
	if ps := e.budgetPrompts(); len(ps) != 2 || ps[1].Source.Key != "budget/80/42.0000" || ps[1].State != aiv1.PromptOpen {
		t.Fatalf("a new budget warns again, once: %s", promptStates(e.task(specTaskName)))
	}
}

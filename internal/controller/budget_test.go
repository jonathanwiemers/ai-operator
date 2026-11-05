package controller

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// newBudgetEnv is newAutopilotEnv with the line "/agent budget <usd>" and
// a spend source that has spent spent.
func newBudgetEnv(t *testing.T, usd string, spent float64) (*projectEnv, *fakeSpend) {
	t.Helper()
	e := newAutopilotEnv(t)
	src := &fakeSpend{spend: store.Spend{Totals: store.SpendTotals{CostUSD: spent, CostKnown: true}}}
	e.r.Spend = src
	setBudgetLine(e, usd)
	return e, src
}

func setBudgetLine(e *projectEnv, usd string) {
	e.gl.Milestones[0].Description = "/agent autopilot\nBuild a new checkout.\n\n/agent budget " + usd
}

func TestBudgetBelowTheLimitDoesNotPause(t *testing.T) {
	e, src := newBudgetEnv(t, "5", 4.99)
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || !e.gl.Issues[1].HasLabel("agent::ready") {
		t.Fatalf("below its budget the autopilot runs: %+v", ap)
	}
	if ap.Budget == nil || *ap.Budget != (aiv1.BudgetStatus{LimitUSD: "5.0000", SpentUSD: "4.9900"}) {
		t.Fatalf("the status shows the budget and the spend: %+v", ap.Budget)
	}
	st := e.tasks()[specTaskName]
	if len(src.scopes) != 1 || src.scopes[0] != (store.SpendScope{Namespace: st.Namespace, Project: st.Spec.ProjectRef, MilestoneIID: 3}) {
		t.Fatalf("the milestone's spend is read once per poll: %+v", src.scopes)
	}
}

func TestBudgetPausesAtTheLimitAndResumesWhenRaised(t *testing.T) {
	e, src := newBudgetEnv(t, "5", 1)
	e.reconcile()
	e.reconcile()
	planned := e.tasks()[specTaskName].Spec
	if planned.BudgetUSD != "5.0000" {
		t.Fatalf("the budget is carried to the spec task: %q", planned.BudgetUSD)
	}

	src.spend.Totals.CostUSD = 5.123
	e.finishRun(1, "sha-api", nil)
	e.reconcile()
	ap := e.autopilot()
	want := "the budget of $5.00 is used ($5.12 spent). Raise it with `/agent budget <usd>` in the milestone description"
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseBudget || ap.PausedReason != want || ap.PausedFor != 0 {
		t.Fatalf("a spent budget pauses the autopilot: %+v", ap)
	}
	if len(e.gl.Merged) != 0 {
		t.Fatalf("nothing merges during a budget pause: %v", e.gl.Merged)
	}
	if !strings.Contains(trackingComments(e), "Autopilot paused: "+want+".\n\nIt continues by itself once the budget is above the spend.") {
		t.Fatalf("the pause is explained on the tracking issue:\n%s", trackingComments(e))
	}
	e.reconcile()
	if n := strings.Count(trackingComments(e), "Autopilot paused"); n != 1 || len(e.gl.Merged) != 0 {
		t.Fatalf("the pause holds and is announced once: %d comments, merged %v", n, e.gl.Merged)
	}

	// A resume while the budget is spent pauses again at once.
	e.gl.AddNote(ap.TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseBudget || len(e.gl.Merged) != 0 {
		t.Fatalf("resume does not lift a spent budget: %+v, merged %v", ap, e.gl.Merged)
	}

	// The owner raises the budget: the autopilot continues and merges, and
	// nothing is planned again.
	setBudgetLine(e, "50")
	e.reconcile()
	st := e.tasks()[specTaskName]
	if st.Spec.BudgetUSD != "50.0000" || st.Spec.RunRequest != planned.RunRequest || st.Spec.Source.DescriptionHash != planned.Source.DescriptionHash {
		t.Fatalf("a new budget re-plans nothing: %+v", st.Spec)
	}
	ap = e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.PauseKind != "" || ap.PausedReason != "" ||
		ap.Budget == nil || *ap.Budget != (aiv1.BudgetStatus{LimitUSD: "50.0000", SpentUSD: "5.1230"}) {
		t.Fatalf("a raised budget resumes the autopilot: %+v %+v", ap, ap.Budget)
	}
	if !strings.Contains(trackingComments(e), "Autopilot continues: the budget is $50.00 now ($5.12 spent).") {
		t.Fatalf("the resume is explained on the tracking issue:\n%s", trackingComments(e))
	}
	e.reconcile()
	if len(e.gl.Merged) != 1 {
		t.Fatalf("the autopilot merges again: %v", e.gl.Merged)
	}
}

func TestBudgetPauseEndsWhenTheLineIsRemoved(t *testing.T) {
	e, _ := newBudgetEnv(t, "5", 6)
	e.reconcile()
	if ap := e.autopilot(); ap.PauseKind != aiv1.PauseBudget || e.gl.Issues[1].HasLabel("agent::ready") {
		t.Fatalf("a spent budget starts nothing: %+v", ap)
	}
	e.gl.Milestones[0].Description = "/agent autopilot\nBuild a new checkout."
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.Budget != nil {
		t.Fatalf("without a budget the pause ends: %+v", ap)
	}
}

func TestBudgetIsNotEnforcedWithoutTheSpend(t *testing.T) {
	e, src := newBudgetEnv(t, "5", 6)
	src.err = errors.New("database down")
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Budget == nil || *ap.Budget != (aiv1.BudgetStatus{LimitUSD: "5.0000"}) {
		t.Fatalf("a failed query pauses nothing and shows no spend: %+v %+v", ap, ap.Budget)
	}
	e.r.Spend = nil
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("without a database the budget is not enforced: %+v", ap)
	}
}

func TestInvalidBudgetLineIsNoBudget(t *testing.T) {
	e, src := newBudgetEnv(t, "five", 6)
	e.reconcile()
	if st := e.tasks()[specTaskName]; st.Spec.BudgetUSD != "" || e.autopilot().Phase != aiv1.AutopilotRunning || e.autopilot().Budget != nil || len(src.scopes) != 0 {
		t.Fatalf("an invalid budget line is ignored: %q %+v", st.Spec.BudgetUSD, e.autopilot())
	}
}

func (e *projectEnv) setPerIssueBudget(usd string) {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	p.Spec.Budget = &aiv1.BudgetSpec{PerIssueUSD: usd}
	if err := e.c.Update(e.ctx, &p); err != nil {
		e.t.Fatal(err)
	}
}

// issueSpend is a milestone's spend with its own calls and the issues'.
func issueSpend(perIssue map[int64]float64) store.Spend {
	sp := store.Spend{Children: []store.SpendChild{{Level: store.SpendMilestone, Key: "3", Totals: store.SpendTotals{CostUSD: 100, CostKnown: true}}}}
	sp.Totals.CostUSD = 100
	for iid, usd := range perIssue {
		sp.Totals.CostUSD += usd
		sp.Children = append(sp.Children, store.SpendChild{Level: store.SpendIssue, Key: strconv.FormatInt(iid, 10), Totals: store.SpendTotals{CostUSD: usd, CostKnown: true}})
	}
	return sp
}

func TestIssueBudgetPausesAtTheDefaultAndTheOverrideResumes(t *testing.T) {
	e := newAutopilotEnv(t)
	src := &fakeSpend{spend: issueSpend(map[int64]float64{1: 5})}
	e.r.Spend = src
	e.setPerIssueBudget("20")
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Budget != nil {
		t.Fatalf("below its budget the issue runs, and the milestone has none: %+v", ap)
	}
	if it := ap.Items[0]; it.IID != 1 || it.Budget == nil || *it.Budget != (aiv1.BudgetStatus{LimitUSD: "20.0000", SpentUSD: "5.0000"}) {
		t.Fatalf("the item shows the project's default and its spend: %+v", it)
	}
	if it := ap.Items[1]; it.Budget == nil || *it.Budget != (aiv1.BudgetStatus{LimitUSD: "20.0000", SpentUSD: "0.0000"}) {
		t.Fatalf("a pending issue has the default too: %+v", it)
	}
	st := e.tasks()[specTaskName]
	if len(src.scopes) != 2 || src.scopes[1] != (store.SpendScope{Namespace: st.Namespace, Project: st.Spec.ProjectRef, MilestoneIID: 3}) {
		t.Fatalf("one query of the milestone per poll, not one per issue: %+v", src.scopes)
	}

	src.spend = issueSpend(map[int64]float64{1: 20})
	e.finishRun(1, "sha-api", nil)
	e.reconcile()
	ap = e.autopilot()
	want := "#1 used its budget of $20.00 ($20.00 spent). Raise it with `/agent budget <usd>` on the issue"
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseBudget || ap.PausedFor != 1 || ap.PausedReason != want || len(e.gl.Merged) != 0 {
		t.Fatalf("an issue that spent its budget pauses autopilot for it: %+v, merged %v", ap, e.gl.Merged)
	}
	e.reconcile()
	if ap := e.autopilot(); ap.PausedFor != 1 || strings.Count(trackingComments(e), "Autopilot paused") != 1 || len(e.gl.Merged) != 0 {
		t.Fatalf("the pause holds and is announced once: %+v\n%s", ap, trackingComments(e))
	}
	if len(src.scopes) != 4 {
		t.Fatalf("one spend query per poll: %d", len(src.scopes))
	}

	// The owner raises the issue's budget on the issue.
	e.gl.AddNote(1, gitlab.User{ID: 1, Username: "jonathan"}, "/agent budget 40")
	e.reconcile()
	ap = e.autopilot()
	if task := e.implTaskOf(1); task.Spec.BudgetUSD != "40.0000" {
		t.Fatalf("the comment sets the issue's budget: %q", task.Spec.BudgetUSD)
	}
	if ap.Phase != aiv1.AutopilotRunning || ap.PausedFor != 0 || *ap.Items[0].Budget != (aiv1.BudgetStatus{LimitUSD: "40.0000", SpentUSD: "20.0000"}) ||
		*ap.Items[1].Budget != (aiv1.BudgetStatus{LimitUSD: "20.0000", SpentUSD: "0.0000"}) {
		t.Fatalf("the override wins and resumes autopilot: %+v %+v", ap, ap.Items)
	}
	if !strings.Contains(trackingComments(e), "Autopilot continues: the budget of #1 is $40.00 now ($20.00 spent).") {
		t.Fatalf("the resume is explained:\n%s", trackingComments(e))
	}
	e.reconcile()
	if len(e.gl.Merged) != 1 {
		t.Fatalf("autopilot merges again: %v", e.gl.Merged)
	}
}

func TestIssueBudgetPauseOfAPendingIssue(t *testing.T) {
	e := newAutopilotEnv(t)
	// #2 spent its budget before (an earlier attempt), #1 is fine.
	e.r.Spend = &fakeSpend{spend: issueSpend(map[int64]float64{2: 25})}
	e.setPerIssueBudget("20")
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.PauseKind != aiv1.PauseBudget || ap.PausedFor != 2 || e.gl.Issues[1].HasLabel("agent::ready") {
		t.Fatalf("a pending issue over its budget pauses, and nothing starts: %+v", ap)
	}
	e.reconcile()
	if ap := e.autopilot(); ap.PausedFor != 2 || ap.PauseKind != aiv1.PauseBudget {
		t.Fatalf("the pause stays for the pending issue: %+v", ap)
	}
	// Skipping it ends its budget, and the pause.
	e.gl.AddNote(ap.TrackingIssueIID, gitlab.User{ID: 1, Username: "jonathan"}, "/agent skip #2")
	e.reconcile()
	ap = e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[1].Budget != nil {
		t.Fatalf("a skipped issue has no budget any more: %+v %+v", ap, ap.Items)
	}
}

func TestIssueBudgetUnsetIsNoBudget(t *testing.T) {
	e := newAutopilotEnv(t)
	src := &fakeSpend{spend: issueSpend(map[int64]float64{1: 500})}
	e.r.Spend = src
	e.setPerIssueBudget("0")
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[0].Budget != nil || len(src.scopes) != 0 {
		t.Fatalf("without a budget nothing is read or paused: %+v %d", ap, len(src.scopes))
	}
	// An override alone is a budget.
	e.gl.AddNote(1, gitlab.User{ID: 1, Username: "jonathan"}, "/agent budget 40")
	e.reconcile()
	if ap := e.autopilot(); ap.PauseKind != aiv1.PauseBudget || ap.PausedFor != 1 {
		t.Fatalf("an issue's own budget applies without a default: %+v", ap)
	}
}

func TestMilestoneBudgetComesBeforeTheIssues(t *testing.T) {
	e, src := newBudgetEnv(t, "5", 0)
	src.spend = issueSpend(map[int64]float64{1: 30})
	e.setPerIssueBudget("20")
	e.reconcile()
	ap := e.autopilot()
	if ap.PauseKind != aiv1.PauseBudget || ap.PausedFor != 0 || !strings.HasPrefix(ap.PausedReason, "the budget of $5.00 is used ($130.00 spent)") {
		t.Fatalf("the milestone's budget pauses first: %+v", ap)
	}
	// Raised, the issue's budget still holds: the pause moves to it.
	setBudgetLine(e, "500")
	e.reconcile()
	if ap := e.autopilot(); ap.PauseKind != aiv1.PauseBudget || ap.PausedFor != 1 || !strings.Contains(trackingComments(e), "Autopilot paused: #1 used its budget") {
		t.Fatalf("the pause moves to the issue over its budget: %+v\n%s", ap, trackingComments(e))
	}
	// Without the issue default, it ends.
	e.setPerIssueBudget("")
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("without a budget for #1 the pause ends: %+v", ap)
	}
}

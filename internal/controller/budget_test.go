package controller

import (
	"errors"
	"strings"
	"testing"

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

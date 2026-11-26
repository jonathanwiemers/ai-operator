package controller

import (
	"context"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// spendByIssue is a spend source with each issue's own spend, by its IID.
type spendByIssue struct {
	usd    map[int64]float64
	scopes []store.SpendScope
}

func (f *spendByIssue) Spend(_ context.Context, sc store.SpendScope) (store.Spend, error) {
	f.scopes = append(f.scopes, sc)
	return store.Spend{Totals: store.SpendTotals{CostUSD: store.USD(f.usd[sc.IssueIID])}}, nil
}

// pollUntil reconciles up to n times until done holds; it fails with what
// if it never does.
func (e *projectEnv) pollUntil(n int, what string, done func() bool) {
	e.t.Helper()
	for range n {
		if done() {
			return
		}
		e.reconcile()
	}
	if !done() {
		e.t.Fatalf("%s: items %v", what, e.looseItems())
	}
}

func TestLooseFreezeStartsAndMergesNothing(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 2)
	e.reconcile()
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 99})
	e.finishRun(350, "sha-350", nil)
	e.say(tracking, bob, "/agent drop #421") // a free slot
	for range 4 {
		e.reconcile()
	}
	e.wantItems("nothing starts while frozen", "350:Active", "421:Left", "46:Pending")
	if len(e.gl.Merged) != 0 || e.looseItem(350).Hold != nil {
		t.Fatalf("nothing merges, and the green #350 is not held: merged %v, %+v", e.gl.Merged, e.looseItem(350))
	}
	line := "🧊 Frozen: the default branch is red (#99); nothing starts or merges until it is green."
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, line) {
		t.Fatalf("the tracking issue says why:\n%s", d)
	}
	e.wantNotes(tracking, line, 1)
	if ap := e.queue().Status.Autopilot; ap.Phase != aiv1.AutopilotRunning || ap.FrozenAt == nil {
		t.Fatalf("frozen, not paused: %+v", ap)
	}
	if slices.Contains(e.gl.Labels(350), "agent::blocked") {
		t.Fatalf("the queue blocks no issue: %v", e.gl.Labels(350))
	}

	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen})
	e.pollUntil(4, "#350 merges after the freeze", func() bool { return e.looseItem(350).State == merged })
	e.pollUntil(2, "#46 starts after the freeze", func() bool { return e.looseItem(46).State == active })
	e.wantNotes(tracking, "▶️ The freeze is over: the default branch is green again. The loose issues queue goes on.", 1)
	e.wantNotes(tracking, line, 1)
	if d := e.gl.Issues[tracking].Description; strings.Contains(d, "Frozen") || e.queue().Status.Autopilot.FrozenAt != nil {
		t.Fatalf("the freeze is over:\n%s", d)
	}
}

func TestLooseFreezeEndsWithTheFreezeSwitchedOff(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRetrying, IssueIID: 99})
	e.reconcile()
	if e.queue().Status.Autopilot.FrozenAt == nil {
		t.Fatal("a retried red branch freezes")
	}
	off := false
	e.setMainHealth(aiv1.MainHealthSpec{FreezeAutopilot: &off})
	e.reconcile()
	e.wantNotes(tracking, "▶️ The freeze is over: the merge freeze is switched off (`mainHealth.freezeAutopilot`). The loose issues queue goes on.", 1)
}

func TestLooseBudgetHoldsOnlyItsIssue(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 2)
	src := &spendByIssue{usd: map[int64]float64{350: 12, 421: 1}}
	e.r.Spend = src
	e.setPerIssueBudget("10")
	e.reconcile()
	e.reconcile()
	want := "#350 used its budget of $10.00 ($12.00 spent). Raise it with `/agent budget <usd>` on the issue"
	if it := e.looseItem(350); it.Hold == nil || it.Hold.Kind != aiv1.PauseBudget || it.Hold.Reason != want {
		t.Fatalf("a spent budget holds #350: %+v", it)
	}
	if it := e.looseItem(421); it.Hold != nil || it.Budget == nil || it.Budget.SpentUSD != "1.0000" {
		t.Fatalf("#421 goes on: %+v", it)
	}
	ap := e.queue().Status.Autopilot
	if ap.Phase != aiv1.AutopilotRunning || ap.Budget != nil {
		t.Fatalf("the queue is not paused and has no budget of its own: %+v", ap)
	}
	for _, sc := range src.scopes {
		if sc.MilestoneIID != 0 || sc.IssueIID == 0 || sc.IssueIID == 46 {
			t.Fatalf("one query per active issue, without a milestone: %+v", src.scopes)
		}
	}
	if task := e.implTaskOf(350); !task.Spec.BudgetStop {
		t.Fatalf("the held issue's task stops at its budget: %+v", task.Spec)
	}
	if task := e.implTaskOf(421); task.Spec.BudgetStop {
		t.Fatal("the other issue's task does not stop")
	}
	src.usd[350] = 12.5 // the last call ends
	e.reconcile()
	e.wantNotes(tracking, "⏸️ #350 is held: #350 used its budget", 1)

	e.mergeLoose(421)
	e.pollUntil(2, "#46 starts", func() bool { return e.looseItem(46).State == active })

	// The owner raises the issue's budget on the issue.
	e.gl.AddNote(350, bob, "/agent budget 40")
	e.reconcile()
	e.reconcile()
	if task := e.implTaskOf(350); task.Spec.BudgetUSD != "40.0000" {
		t.Fatalf("the comment sets the issue's budget: %q", task.Spec.BudgetUSD)
	}
	if it := e.looseItem(350); it.Hold != nil {
		t.Fatalf("the raise lifts the hold: %+v", it)
	}
	if c := strings.Join(botComments(e, tracking), "\n"); !strings.Contains(c, "the budget of #350 is $40.00 now ($12.50 spent)") {
		t.Fatalf("the end of the hold is explained:\n%s", c)
	}
	if task := e.implTaskOf(350); task.Spec.BudgetStop {
		t.Fatal("the task runs again")
	}
}

func TestLooseCompanionHold(t *testing.T) {
	e, tracking := newLooseRunEnv(t, 0)
	e.reconcile()
	e.finishRun(350, "sha-350", func(r *aiv1.RunStatus) {
		r.Companion = &aiv1.CompanionRun{Project: "group/infra", MRIID: 3, PushedSHA: "c-1"}
	})
	e.reconcile()
	e.reconcile()
	it := e.looseItem(350)
	if it.Hold == nil || it.Hold.Reason != looseCompanion || len(e.gl.Merged) != 0 {
		t.Fatalf("a companion holds the item, unmerged: %+v, merged %v", it, e.gl.Merged)
	}
	e.wantNotes(tracking, "⏸️ #350 is held: companion repositories need a milestone.", 1)
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "- #350: companion repositories need a milestone") {
		t.Fatalf("the tracking issue shows the hold:\n%s", d)
	}
	if ap := e.queue().Status.Autopilot; ap.Phase != aiv1.AutopilotRunning || len(ap.Releases) != 0 {
		t.Fatalf("the queue runs on, no companion release: %+v", ap)
	}

	// Merged by hand: no companion entry is recorded for a queue issue.
	mr := e.looseItem(350).MergeRequestIID
	e.gl.MRs[mr].State = "merged"
	e.gl.Issues[350].State = "closed"
	e.gl.ClosedBy(350, mr)
	e.reconcile()
	e.reconcile()
	if it := e.looseItem(350); it.State != merged {
		t.Fatalf("merged by hand: %+v", it)
	}
	if cs := e.project().Status.Companions; len(cs) != 0 {
		t.Fatalf("no companion of a queue issue is recorded: %+v", cs)
	}
}

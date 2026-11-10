package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// budget holds the milestone to its budget (spec.budgetUSD) and every open
// issue of its queue to the issue's (its implement task's spec.budgetUSD,
// else the project's budget.perIssueUSD), once per poll and so between
// agent calls: one of them spent, a running autopilot pauses (a running
// call finishes, nothing new starts or merges), PausedFor the issue or 0
// for the milestone. A budget pause ends by itself once the owner raised
// that budget above the spend or removed it. The spend is one query of the
// milestone, its issues as children; from it the issues also get their
// spend heads-up (budgetAlerts), which pauses nothing. Without it (no
// database, or the query failed) the budgets are not enforced and nothing
// is alerted.
func (a *autopilot) budget(ctx context.Context) error {
	limit := a.st.Spec.BudgetUSD
	if limit == "" {
		a.ap.Budget = nil
	} else {
		b := &aiv1.BudgetStatus{LimitUSD: limit}
		if old := a.ap.Budget; old != nil && old.LimitUSD == limit {
			b.WarnedAt = old.WarnedAt // a new budget is warned about again
		}
		a.ap.Budget = b
	}
	var issues []*aiv1.AutopilotItem
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		it.Budget = nil
		if l := a.issueBudget(it); l != "" {
			it.Budget = &aiv1.BudgetStatus{LimitUSD: l}
			issues = append(issues, it)
		}
	}

	// A pause whose budget is gone ends without the spend.
	if a.budgetPaused() {
		if b := a.pausedBudget(); b == nil {
			why := "the milestone has no budget any more"
			if a.ap.PausedFor != 0 {
				why = fmt.Sprintf("#%d has no budget any more, or is merged or skipped", a.ap.PausedFor)
			}
			if err := a.resumeBudget(ctx, why); err != nil {
				return err
			}
		}
	}
	alerts := a.rearmAlerts()
	if a.ap.Budget == nil && len(issues) == 0 && alerts == 0 {
		return nil
	}
	if a.r.Spend == nil {
		a.log.Info("no history database: the budgets are not enforced", "budgetUSD", limit, "issueBudgets", len(issues), "issueAlerts", alerts)
		return nil
	}
	sp, err := a.r.Spend.Spend(ctx, store.SpendScope{Namespace: a.st.Namespace, Project: a.st.Spec.ProjectRef, MilestoneIID: a.st.Spec.Milestone.IID})
	if err != nil {
		a.log.Error(err, "cannot read the milestone's spend: its budgets are not enforced", "budgetUSD", limit, "issueBudgets", len(issues))
		return nil
	}
	if a.ap.Budget != nil {
		a.ap.Budget.SpentUSD = aiv1.FormatCost(sp.Totals.CostUSD)
	}
	children := map[string]*store.SpendChild{}
	for i, c := range sp.Children {
		if c.Level == store.SpendIssue {
			children[c.Key] = &sp.Children[i]
		}
	}
	for _, it := range issues {
		spent := 0.0
		if c := children[strconv.FormatInt(it.IID, 10)]; c != nil {
			spent = c.Totals.CostUSD
		}
		it.Budget.SpentUSD = aiv1.FormatCost(spent)
	}
	if err := a.budgetAlerts(ctx, children); err != nil {
		return err
	}

	// The spent budget that pauses: the milestone's first, then the
	// issues' in queue order.
	over, reason := int64(-1), ""
	prefix := a.spec.Workflow.CommandPrefix
	if b := a.ap.Budget; b != nil && aiv1.BudgetLeft(b.LimitUSD, b.SpentUSD, 0) <= 0 {
		over, reason = 0, fmt.Sprintf("the budget of $%s is used ($%s spent). Raise it with `%s budget <usd>` in the milestone description",
			showBudget(b.LimitUSD), showBudget(b.SpentUSD), prefix)
	} else {
		for _, it := range issues {
			if b := it.Budget; aiv1.BudgetLeft(b.LimitUSD, b.SpentUSD, 0) <= 0 {
				over, reason = it.IID, fmt.Sprintf("#%d used its budget of $%s ($%s spent). Raise it with `%s budget <usd>` on the issue",
					it.IID, showBudget(b.LimitUSD), showBudget(b.SpentUSD), prefix)
				break
			}
		}
	}
	switch paused := a.budgetPaused(); {
	case over >= 0 && (a.ap.Phase == aiv1.AutopilotRunning || paused && a.ap.PausedFor != over):
		if err := a.pause(ctx, aiv1.PauseBudget, over, reason); err != nil {
			return err
		}
	case over < 0 && paused:
		b, why := a.pausedBudget(), "the budget"
		if a.ap.PausedFor != 0 {
			why = fmt.Sprintf("the budget of #%d", a.ap.PausedFor)
		}
		return a.resumeBudget(ctx, fmt.Sprintf("%s is $%s now ($%s spent)", why, showBudget(b.LimitUSD), showBudget(b.SpentUSD)))
	}
	return a.budgetPrompts(ctx)
}

// issueBudget is the budget of an item that can still spend (pending or
// active), formatted for storage: its task's override, else the project's
// default; "" for none.
func (a *autopilot) issueBudget(it *aiv1.AutopilotItem) string {
	if it.State != aiv1.AutopilotItemPending && it.State != aiv1.AutopilotItemActive {
		return ""
	}
	if t := a.tasks[it.IID]; t != nil && t.Spec.BudgetUSD != "" {
		return t.Spec.BudgetUSD
	}
	if b := a.spec.Budget; b != nil && aiv1.CostOf(b.PerIssueUSD) > 0 {
		return aiv1.FormatCost(aiv1.CostOf(b.PerIssueUSD))
	}
	return ""
}

// alertThresholds are the spends of item it that post its heads-up, as
// stored: the project's budget.perIssueAlertUSD and 80 % of the issue's
// budget (it.Budget, set by budget before), "" for none. Only items that
// can still spend have them.
func (a *autopilot) alertThresholds(it *aiv1.AutopilotItem) (alert, warn string) {
	if it.State != aiv1.AutopilotItemPending && it.State != aiv1.AutopilotItemActive {
		return "", ""
	}
	if b := a.spec.Budget; b != nil && aiv1.CostOf(b.PerIssueAlertUSD) > 0 {
		alert = aiv1.FormatCost(aiv1.CostOf(b.PerIssueAlertUSD))
	}
	if it.Budget != nil {
		warn = aiv1.BudgetWarnUSD(it.Budget.LimitUSD)
	}
	return alert, warn
}

// rearmAlerts forgets the heads-up of the items whose threshold changed
// (another alert value or budget, or none any more), so the new one is
// alerted again, and counts the items that have a threshold. Items that
// cannot spend any more keep theirs.
func (a *autopilot) rearmAlerts() int {
	n := 0
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		if it.State != aiv1.AutopilotItemPending && it.State != aiv1.AutopilotItemActive {
			continue
		}
		alert, warn := a.alertThresholds(it)
		if alert != "" || warn != "" {
			n++
		}
		if it.BudgetAlertedUSD != "" && it.BudgetAlertedUSD != alert && it.BudgetAlertedUSD != warn {
			it.BudgetAlertedAt, it.BudgetAlertedUSD = nil, ""
		}
	}
	return n
}

// topSpend is how many kinds and models a heads-up names.
const topSpend = 3

// budgetAlerts posts the spend heads-up of every issue that can still spend
// and passed a threshold of it (alertThresholds) not alerted yet: the
// highest one passed, once (it.BudgetAlertedAt); 80 % of the budget only
// while the issue is under it, at the budget the pause says it. It names
// the issue's spend and its top kinds and models (children: the issues'
// spend by IID), and pauses nothing. Not on a done milestone.
func (a *autopilot) budgetAlerts(ctx context.Context, children map[string]*store.SpendChild) error {
	if a.ap.Phase == aiv1.AutopilotDone {
		return nil
	}
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		alert, warn := a.alertThresholds(it)
		c := children[strconv.FormatInt(it.IID, 10)]
		if c == nil || alert == "" && warn == "" {
			continue
		}
		spent := c.Totals.CostUSD
		if warn != "" && spent >= aiv1.CostOf(it.Budget.LimitUSD) {
			warn = ""
		}
		at := ""
		for _, t := range []string{alert, warn} {
			if t != "" && spent >= aiv1.CostOf(t) && (at == "" || aiv1.CostOf(t) > aiv1.CostOf(at)) {
				at = t
			}
		}
		if at == "" || it.BudgetAlertedUSD == at {
			continue
		}
		if err := a.comment(ctx, a.alertText(it, c, at == warn)); err != nil {
			return err
		}
		a.log.Info("issue spend heads-up posted", "issue", it.IID, "thresholdUSD", at, "spentUSD", aiv1.FormatCost(spent))
		now := metav1.NewTime(a.r.now())
		it.BudgetAlertedAt, it.BudgetAlertedUSD = &now, at
	}
	return nil
}

// alertText is the heads-up of item it with spend c; ofBudget: for 80 % of
// its budget, else for budget.perIssueAlertUSD.
func (a *autopilot) alertText(it *aiv1.AutopilotItem, c *store.SpendChild, ofBudget bool) string {
	var b strings.Builder
	spent := aiv1.ShowCost(c.Totals.CostUSD)
	if ofBudget {
		lim := showBudget(it.Budget.LimitUSD)
		fmt.Fprintf(&b, "💸 Heads-up: #%d spent $%s, %.0f %% of its budget of $%s. Autopilot keeps going and pauses for it at $%s.",
			it.IID, spent, math.Floor(100*c.Totals.CostUSD/aiv1.CostOf(it.Budget.LimitUSD)), lim, lim)
	} else {
		fmt.Fprintf(&b, "💸 Heads-up: #%d spent $%s, past the spend alert of $%s per issue (`budget.perIssueAlertUSD`). Autopilot keeps going.",
			it.IID, spent, showBudget(a.spec.Budget.PerIssueAlertUSD))
	}
	cost := func(usd float64, known bool) string {
		if !known {
			return "cost unknown"
		}
		return "$" + aiv1.ShowCost(usd)
	}
	var kinds, models []string
	for _, k := range c.Kinds[:min(len(c.Kinds), topSpend)] {
		kinds = append(kinds, fmt.Sprintf("%s %s (%d calls)", k.Key, cost(k.Totals.CostUSD, k.Totals.CostKnown), k.Totals.Calls))
	}
	// The most expensive models: Models is in token order.
	ms := slices.Clone(c.Models)
	slices.SortStableFunc(ms, func(x, y store.ModelSplit) int { return cmp.Compare(y.CostUSD, x.CostUSD) })
	for _, m := range ms[:min(len(ms), topSpend)] {
		models = append(models, fmt.Sprintf("%s %s", m.Model, cost(m.CostUSD, m.CostKnown)))
	}
	if len(kinds) > 0 {
		b.WriteString("\n\n- Top kinds: " + strings.Join(kinds, ", "))
	}
	if len(models) > 0 {
		b.WriteString("\n- Top models: " + strings.Join(models, ", "))
	}
	return b.String()
}

// budgetPaused reports whether autopilot is paused for a budget.
func (a *autopilot) budgetPaused() bool {
	return a.ap.Phase == aiv1.AutopilotPaused && a.ap.PauseKind == aiv1.PauseBudget
}

// budgetStopped reports whether the implement task of issue iid gets
// spec.budgetStop: the autopilot of its milestone (spec task st) is paused
// for the issue's budget, or for the milestone's (every issue of it), or it
// is done with the milestone's budget used (ended at its budget prompt).
// Like the pause reason, a pause from this poll reaches the task next poll,
// and a resume clears it.
func budgetStopped(st *aiv1.AgentTask, iid int64) bool {
	ap := st.Status.Autopilot
	if !st.Spec.Autopilot || ap == nil {
		return false
	}
	if b := ap.Budget; ap.Phase == aiv1.AutopilotDone && b != nil && b.SpentUSD != "" && aiv1.BudgetLeft(b.LimitUSD, b.SpentUSD, 0) <= 0 {
		return true
	}
	return ap.Phase == aiv1.AutopilotPaused && ap.PauseKind == aiv1.PauseBudget && (ap.PausedFor == 0 || ap.PausedFor == iid)
}

// pausedBudget is the budget a budget pause is for, nil once it is gone.
func (a *autopilot) pausedBudget() *aiv1.BudgetStatus {
	if a.ap.PausedFor == 0 {
		return a.ap.Budget
	}
	for i := range a.ap.Items {
		if a.ap.Items[i].IID == a.ap.PausedFor {
			return a.ap.Items[i].Budget
		}
	}
	return nil
}

func showBudget(usd string) string { return aiv1.ShowCost(aiv1.CostOf(usd)) }

// resumeBudget ends a budget pause and says why.
func (a *autopilot) resumeBudget(ctx context.Context, why string) error {
	a.log.Info("autopilot continues by itself", "why", why)
	return a.resumed(ctx, byOperator, why)
}

// warnAt is the share of the milestone's budget spent that posts the 80 %
// prompt.
const warnAt = aiv1.BudgetWarnShare

// Keys of the budget prompts: "budget/80/<limitUSD>" and
// "budget/100/<limitUSD>", once per budget value.
const (
	budgetKey80  = "budget/80/"
	budgetKey100 = "budget/100/"
)

// budgetPrompts puts the milestone's budget to the owners: at 80 % of it
// spent (once per budget value, b.WarnedAt) a heads-up that offers two
// raises, keeping going and pausing now; during a pause for it (100 %)
// two raises, ending the milestone here and staying paused. A prompt of its
// key in the status is not posted again. Issue budgets get no prompt: while
// their 80 % heads-up holds (AutopilotItem.BudgetWarned), the Desk derives
// an Inbox item from it (budgetWarning).
func (a *autopilot) budgetPrompts(ctx context.Context) error {
	b := a.ap.Budget
	if b == nil || b.SpentUSD == "" || a.ap.Phase == aiv1.AutopilotDone {
		return nil
	}
	limit, spent := aiv1.CostOf(b.LimitUSD), aiv1.CostOf(b.SpentUSD)
	if limit <= 0 {
		return nil
	}
	var key string
	switch {
	case a.budgetPaused() && a.ap.PausedFor == 0:
		key = budgetKey100 + b.LimitUSD
	case spent >= warnAt*limit && spent < limit && b.WarnedAt == nil:
		key = budgetKey80 + b.LimitUSD
	default:
		return nil
	}
	posted := slices.ContainsFunc(a.st.Status.Prompts, func(p aiv1.Prompt) bool {
		return p.Source.Kind == aiv1.PromptBudget && p.Source.Key == key
	})
	if !posted {
		p := a.budgetPrompt(key, limit, spent)
		q, err := a.r.addPrompt(ctx, a.gl, a.st, a.st, p)
		if err != nil {
			return fmt.Errorf("posting the budget prompt: %w", err)
		}
		a.log.Info("budget prompt posted", "prompt", q.ID, "key", key, "budgetUSD", b.LimitUSD, "spentUSD", b.SpentUSD)
	}
	if b.WarnedAt == nil {
		now := metav1.NewTime(a.r.now())
		b.WarnedAt = &now
	}
	return nil
}

// budgetPrompt is the budget prompt of key, for a budget of limit with
// spent spent.
func (a *autopilot) budgetPrompt(key string, limit, spent float64) aiv1.Prompt {
	lo, hi := budgetAmounts(limit, spent)
	prefix := a.spec.Workflow.CommandPrefix
	raise := func(id, usd string, recommended bool) aiv1.PromptOption {
		return aiv1.PromptOption{ID: id, Label: "Raise the budget to $" + usd, Recommended: recommended,
			Detail:  fmt.Sprintf("The milestone description's budget line becomes `%s budget %s`.", prefix, usd),
			Actions: []aiv1.PilotAction{{Tool: "raiseBudget", Amount: usd}}}
	}
	p := aiv1.Prompt{
		Source:  aiv1.PromptSource{Kind: aiv1.PromptBudget, Task: a.st.Name, Key: key},
		Context: "The raises are 1.25 and 1.5 times the spend so far, in whole dollars. The spend is what the milestone's agent calls cost (`agent_calls`).",
	}
	if strings.HasPrefix(key, budgetKey100) {
		p.Question = fmt.Sprintf("The milestone used its budget of $%s ($%s spent), so autopilot is paused. How should it go on?",
			aiv1.ShowCost(limit), aiv1.ShowCost(spent))
		p.Options = []aiv1.PromptOption{raise("A", lo, true), raise("B", hi, false),
			{ID: "C", Label: "End the milestone here", Actions: []aiv1.PilotAction{{Tool: "endMilestone"}},
				Detail: "No further issue is picked: the pending ones are skipped, and so is the active one, which cannot go on without spend. Autopilot is done."},
			{ID: "D", Label: "Stay paused", Detail: "Nothing changes; autopilot continues once the budget line is above the spend."}}
		// The pause comment mentioned the owners.
		p.Quiet = a.spec.Notify.Wants(aiv1.NotifyAutopilotPaused)
		return p
	}
	p.Question = fmt.Sprintf("The milestone spent $%s of its budget of $%s (%.0f %%). How should it go on?",
		aiv1.ShowCost(spent), aiv1.ShowCost(limit), math.Floor(100*spent/limit))
	p.Options = []aiv1.PromptOption{raise("A", lo, false), raise("B", hi, false),
		{ID: "C", Label: "Keep going", Recommended: true, Detail: fmt.Sprintf("Nothing changes; autopilot pauses once $%s are spent.", aiv1.ShowCost(limit))}}
	if a.ap.Phase == aiv1.AutopilotRunning {
		p.Options = append(p.Options, aiv1.PromptOption{ID: "D", Label: "Pause now", Actions: []aiv1.PilotAction{{Tool: "pause"}},
			Detail: fmt.Sprintf("Autopilot pauses now; `%s resume` continues it.", prefix)})
	}
	return p
}

// budgetAmounts are the two raises a budget prompt offers for a budget of
// budget with spent spent: 1.25 and 1.5 times the spend, rounded up to
// whole dollars, the first at least budget + 1, the second above the first.
func budgetAmounts(budget, spent float64) (lo, hi string) {
	up := func(v float64) float64 { return math.Ceil(v - 1e-9) } // 1.25 * 32 is 40, not 41
	l := max(up(spent*1.25), up(budget+1))
	h := max(up(spent*1.5), l+1)
	return strconv.FormatFloat(l, 'f', 0, 64), strconv.FormatFloat(h, 'f', 0, 64)
}

// budgetResolved reports whether the budget condition of budget prompt p
// no longer holds on the milestone of spec task st: its budget is another
// (raised, lowered or removed), or the spend is under the prompt's
// threshold of it. The 80 % prompt also gives way once the budget is used:
// the 100 % prompt follows. An unknown spend resolves nothing.
func budgetResolved(p aiv1.Prompt, st *aiv1.AgentTask) bool {
	limit, threshold := "", 0.0
	switch key := p.Source.Key; {
	case strings.HasPrefix(key, budgetKey80):
		limit, threshold = strings.TrimPrefix(key, budgetKey80), warnAt
	case strings.HasPrefix(key, budgetKey100):
		limit, threshold = strings.TrimPrefix(key, budgetKey100), 1
	default:
		return false
	}
	if st == nil || st.Status.Autopilot == nil {
		return false
	}
	b := st.Status.Autopilot.Budget
	switch {
	case b == nil || b.LimitUSD != limit:
		return true
	case b.SpentUSD == "":
		return false
	}
	spent, lim := aiv1.CostOf(b.SpentUSD), aiv1.CostOf(b.LimitUSD)
	return spent < threshold*lim || threshold < 1 && spent >= lim
}

// validRaise checks a raiseBudget against the milestone (spec task st): a
// budget in USD above the milestone's.
func validRaise(a aiv1.PilotAction, st *aiv1.AgentTask) error {
	usd, err := aiv1.ParseBudget(a.Amount)
	switch {
	case err != nil:
		return err
	case st.Spec.BudgetUSD == "":
		return errors.New("the milestone has no budget any more")
	case aiv1.CostOf(usd) <= aiv1.CostOf(st.Spec.BudgetUSD):
		return fmt.Errorf("the budget is $%s already", showBudget(st.Spec.BudgetUSD))
	}
	return nil
}

// raiseBudget sets the milestone description's budget line to usd, read
// again: a budget raised (or removed) by hand meanwhile stays. The next
// poll reads it, and a pause for the budget ends by itself.
func (r *AgentProjectReconciler) raiseBudget(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, usd string) error {
	prefix := spec.Workflow.CommandPrefix
	m, err := gl.GetMilestone(ctx, spec.GitLab.Project, st.Spec.Milestone.ID)
	if err != nil {
		return fmt.Errorf("reading the milestone: %w", err)
	}
	cur, err := rules.Budget(m.Description, prefix)
	switch {
	case err != nil || cur == "":
		return errors.New("the milestone description has no budget line any more")
	case aiv1.CostOf(cur) >= aiv1.CostOf(usd):
		return fmt.Errorf("the budget is $%s already", showBudget(cur))
	}
	if _, err := gl.UpdateMilestoneDescription(ctx, spec.GitLab.Project, m.ID, rules.SetBudget(m.Description, prefix, usd)); err != nil {
		return err
	}
	log.FromContext(ctx).Info("milestone budget raised", "milestone", st.Spec.Milestone.Title, "from", cur, "to", usd)
	return nil
}

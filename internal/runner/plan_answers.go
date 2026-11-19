package runner

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// planAnswersApply ends the answers section of a turn prompt that changes
// decisions.
const planAnswersApply = "Apply each change to the draft and to the decisions list. If you do not apply one, say why in your reply."

// keepsPlanDecision says whether answer a keeps decision d as it is: a keep,
// or a pick of the recommended option, of a locked decision. On an open
// question either one is a change that picks the recommendation (D614-P1,
// D614-P2).
func keepsPlanDecision(d aiv1.PlanDecision, a aiv1.PlanDecisionAnswer) bool {
	if d.State == aiv1.PlanDecisionOpen || a.Other != "" {
		return false
	}
	return a.Keep || a.Option != nil && *a.Option == d.Recommended
}

// renderPlanAnswers renders the owner's answers for a turn prompt in fixed
// sentences, against the plan's current list: the kept IDs on one line, each
// change on its own. An answer to a decision the list no longer has, or to
// an option it does not have, is left out; "" when nothing is left.
func renderPlanAnswers(list []aiv1.PlanDecision, answers []aiv1.PlanDecisionAnswer) string {
	answers = slices.Clone(answers)
	slices.SortStableFunc(answers, func(a, b aiv1.PlanDecisionAnswer) int {
		return cmp.Compare(planDecisionNum(a.ID), planDecisionNum(b.ID))
	})
	var kept []int32
	var changes []string
	for _, a := range answers {
		i := slices.IndexFunc(list, func(d aiv1.PlanDecision) bool { return d.ID == a.ID })
		if i < 0 || list[i].Recommended < 0 || int(list[i].Recommended) >= len(list[i].Options) {
			continue
		}
		d := list[i]
		rec := d.Options[d.Recommended]
		switch {
		case keepsPlanDecision(d, a):
			kept = append(kept, planDecisionNum(d.ID))
		case a.Other != "":
			changes = append(changes, fmt.Sprintf("- %s (%s): the owner answers in their own words: %s",
				d.ID, d.Title, strings.ReplaceAll(strings.TrimSpace(a.Other), "\n", "\n  ")))
		case a.Keep || a.Option != nil && *a.Option == d.Recommended:
			changes = append(changes, fmt.Sprintf("- %s (%s): the owner picks the recommended %q.", d.ID, d.Title, rec))
		case a.Option != nil && *a.Option >= 0 && int(*a.Option) < len(d.Options):
			changes = append(changes, fmt.Sprintf("- %s (%s): the owner picks %q instead of the recommended %q.",
				d.ID, d.Title, d.Options[*a.Option], rec))
		}
	}
	var blocks []string
	if len(kept) > 0 {
		blocks = append(blocks, planIDRanges(kept)+" kept as recommended.")
	}
	if len(changes) > 0 {
		blocks = append(blocks, strings.Join(changes, "\n"), planAnswersApply)
	}
	return strings.Join(blocks, "\n\n")
}

// planIDRanges lists decision numbers, ascending, as IDs and collapses runs
// of three or more into ranges: "D1, D3–D6".
func planIDRanges(nums []int32) string {
	var parts []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		switch {
		case j-i >= 2:
			parts = append(parts, fmt.Sprintf("D%d–D%d", nums[i], nums[j]))
		default:
			for k := i; k <= j; k++ {
				parts = append(parts, fmt.Sprintf("D%d", nums[k]))
			}
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// currentPlanAnswers maps each ID that answers answers at its current
// version in list to whether the answer keeps it. Answers to another
// version sign off nothing (D614-P3); without an author nothing counts.
func currentPlanAnswers(list []aiv1.PlanDecision, answers []aiv1.PlanDecisionAnswer, author string) map[string]bool {
	if author == "" || len(answers) == 0 {
		return nil
	}
	keeps := make(map[string]bool, len(answers))
	for _, a := range answers {
		i := slices.IndexFunc(list, func(d aiv1.PlanDecision) bool { return d.ID == a.ID })
		if i >= 0 && list[i].Version == a.Version && (a.Keep || a.Option != nil || a.Other != "") {
			keeps[a.ID] = keepsPlanDecision(list[i], a)
		}
	}
	return keeps
}

// signOff signs d's version off by the owner author, replacing an earlier
// sign-off.
func signOff(d *aiv1.PlanDecision, author, name string, now time.Time) {
	d.SignedOffBy, d.SignedOffByName, d.SignedOffAt = author, name, &metav1.Time{Time: now}
}

// signOffKept is the plan's list after a turn that did not change it: the
// keeps among answers sign their decisions off; changes sign off nothing.
func signOffKept(list []aiv1.PlanDecision, answers []aiv1.PlanDecisionAnswer, author, name string, now time.Time) []aiv1.PlanDecision {
	keeps := currentPlanAnswers(list, answers, author)
	if len(keeps) == 0 {
		return list
	}
	out := make([]aiv1.PlanDecision, len(list))
	for i := range list {
		out[i] = *list[i].DeepCopy()
		if keeps[out[i].ID] {
			signOff(&out[i], author, name, now)
		}
	}
	return out
}

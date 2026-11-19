package runner

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// planDecisionsInvalid ends the reply of a turn whose decision list was
// refused.
const planDecisionsInvalid = "(This turn's decision list broke a rule and was not applied; the previous list is kept.)"

// The limits of a plan's decision list, as in the CRD; text in runes after
// trimming.
const (
	maxPlanDecisions     = 30
	maxPlanDecisionTitle = 120
	maxPlanDecisionText  = 1000 // question and why
	minPlanOptions       = 2
	maxPlanOptions       = 5
	maxPlanOption        = 500
)

var planDecisionID = regexp.MustCompile(`^D([1-9][0-9]*)$`)

// trimmed is d with its text trimmed, as it is checked and stored.
func (d planTurnDecision) trimmed() planTurnDecision {
	d.ID, d.Title, d.Question, d.Why, d.State = strings.TrimSpace(d.ID), strings.TrimSpace(d.Title),
		strings.TrimSpace(d.Question), strings.TrimSpace(d.Why), strings.TrimSpace(d.State)
	opts := make([]string, len(d.Options))
	for i, o := range d.Options {
		opts[i] = strings.TrimSpace(o)
	}
	d.Options = opts
	return d
}

// planDecisionNum is the number of a decision ID, 0 when it is no valid ID.
func planDecisionNum(id string) int32 {
	m := planDecisionID.FindStringSubmatch(id)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseInt(m[1], 10, 32)
	if err != nil {
		return 0
	}
	return int32(n)
}

// textLen checks that s has lo to hi runes.
func textLen(what, s string, lo, hi int) error {
	if n := utf8.RuneCountInString(s); n < lo || n > hi {
		return fmt.Errorf("%s has %d characters, want %d to %d", what, n, lo, hi)
	}
	return nil
}

// validPlanDecisions checks the agent's list against the limits and the
// plan's current list prev: IDs are unique, and an ID not in prev is new,
// numbered above seq, the highest number ever issued, so a dropped ID
// cannot come back.
func validPlanDecisions(prev []aiv1.PlanDecision, seq int32, in []planTurnDecision) error {
	if len(in) > maxPlanDecisions {
		return fmt.Errorf("%d decisions, at most %d", len(in), maxPlanDecisions)
	}
	seen := map[string]bool{}
	for _, d := range in {
		d = d.trimmed()
		n := planDecisionNum(d.ID)
		switch {
		case n == 0:
			return fmt.Errorf("decision ID %q is not D1, D2, …", d.ID)
		case seen[d.ID]:
			return fmt.Errorf("decision %s appears twice", d.ID)
		case n <= seq && !slices.ContainsFunc(prev, func(p aiv1.PlanDecision) bool { return p.ID == d.ID }):
			return fmt.Errorf("decision %s reuses a dropped or never issued ID (highest issued D%d)", d.ID, seq)
		}
		seen[d.ID] = true
		if err := textLen(d.ID+"'s title", d.Title, 1, maxPlanDecisionTitle); err != nil {
			return err
		}
		if err := textLen(d.ID+"'s question", d.Question, 1, maxPlanDecisionText); err != nil {
			return err
		}
		if err := textLen(d.ID+"'s why", d.Why, 0, maxPlanDecisionText); err != nil {
			return err
		}
		if len(d.Options) < minPlanOptions || len(d.Options) > maxPlanOptions {
			return fmt.Errorf("decision %s has %d options, want %d to %d", d.ID, len(d.Options), minPlanOptions, maxPlanOptions)
		}
		for i, o := range d.Options {
			if err := textLen(fmt.Sprintf("%s's option %d", d.ID, i), o, 1, maxPlanOption); err != nil {
				return err
			}
		}
		if d.Recommended < 0 || d.Recommended >= len(d.Options) {
			return fmt.Errorf("decision %s recommends option %d of %d", d.ID, d.Recommended, len(d.Options))
		}
		if s := aiv1.PlanDecisionState(d.State); s != aiv1.PlanDecisionOpen && s != aiv1.PlanDecisionLocked {
			return fmt.Errorf("decision %s has state %q, want open or locked", d.ID, d.State)
		}
	}
	return nil
}

// planDecisionIDs lists the IDs of ds, in order.
func planDecisionIDs(ds []aiv1.PlanDecision) []string {
	ids := make([]string, len(ds))
	for i, d := range ds {
		ids[i] = d.ID
	}
	return ids
}

// mergePlanDecisions turns the agent's valid list into the plan's new list,
// in the agent's order, and the new highest decision number. Entries left
// out are dropped. answers are the answered message's, from the owner
// author: a new version of a decision they changed is decided and signed
// off by them, a version they kept is signed off by them, and a change the
// agent did not apply signs off nothing.
func mergePlanDecisions(prev []aiv1.PlanDecision, seq int32, in []planTurnDecision,
	answers []aiv1.PlanDecisionAnswer, author, authorName string, now time.Time) ([]aiv1.PlanDecision, int32) {
	keeps := currentPlanAnswers(prev, answers, author)
	out := make([]aiv1.PlanDecision, 0, len(in))
	for _, d := range in {
		d = d.trimmed()
		var old *aiv1.PlanDecision
		if i := slices.IndexFunc(prev, func(p aiv1.PlanDecision) bool { return p.ID == d.ID }); i >= 0 {
			old = &prev[i]
		}
		next := nextPlanDecision(old, d, now)
		keep, answered := keeps[d.ID]
		switch {
		case !answered || old == nil:
		case next.Version == old.Version:
			if keep {
				signOff(&next, author, authorName, now)
			}
		case !keep:
			next.DecidedBy, next.DecidedByName = author, authorName
			signOff(&next, author, authorName, now)
		}
		out = append(out, next)
		seq = max(seq, planDecisionNum(d.ID))
	}
	return out, seq
}

// nextPlanDecision is decision d after a turn, given its current version
// old (nil for a new decision). A change of the title, question, options,
// recommended option or state is a new version the agent decided, not
// signed off; otherwise the version and who-fields stay and only why is
// taken.
func nextPlanDecision(old *aiv1.PlanDecision, d planTurnDecision, now time.Time) aiv1.PlanDecision {
	next := aiv1.PlanDecision{ID: d.ID, Title: d.Title, Question: d.Question, Options: d.Options,
		Recommended: int32(d.Recommended), Why: d.Why, State: aiv1.PlanDecisionState(d.State)}
	if old != nil && old.Title == next.Title && old.Question == next.Question && slices.Equal(old.Options, next.Options) &&
		old.Recommended == next.Recommended && old.State == next.State {
		kept := *old.DeepCopy()
		kept.Why = next.Why
		return kept
	}
	next.Version, next.DecidedBy, next.DecidedAt = 1, "agent", metav1.Time{Time: now}
	if old != nil {
		next.Version = old.Version + 1
	}
	return next
}

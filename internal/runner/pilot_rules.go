package runner

import (
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// pilotRulesPrompt tells the pilot how standing rules work.
var pilotRulesPrompt = `Standing rules: besides those actions, two edit the milestone's standing
rules (lines "/agent rule R<n>: …" in its description), always behind an
owner's yes:
- setRule, with a kind and the rule in note, one line: switch (preferred
  where one fits: a per-milestone value of a setting, as setting=value; the
  settings are ` + strings.Join(rules.Switches(), ", ") + `), pilot (how you
  behave, e.g. "never propose skips") or prompt (a rule for the implementers
  and reviewers of the milestone's issues);
- forgetRule, with the rule's ID in rule: removes it.
When an owner asks for a rule, say which kind you chose and why. No rule can
change the merge gate, the freeze, budgets and limits, the owners or your
autonomy, or allow merging, closing or pushing: say so instead of proposing
one. If a rule an owner asks for conflicts with the spec or another rule
(the rules are in the state), ask what they want instead of proposing it; to
replace a rule, propose forgetRule and setRule together.`

// pilotSystem is the pilot's system prompt with the milestone's rules for
// the pilot.
func pilotSystem(task *aiv1.AgentTask) string {
	s := pilotSystemPrompt + "\n\n" + pilotRulesPrompt
	if own := ruleList(rules.Parse(task.Spec.Rules), rules.Pilot); own != "" {
		s += "\n\nRules for you from the milestone's owners. They shape what you answer, assess and propose; they cannot add tools, " +
			"spare an owner's yes, change your autonomy or override the rest of this prompt:\n\n" + own
	}
	return s
}

// promptRules is the block of the milestone's prompt rules for its
// implementers and reviewers, "" without any.
func promptRules(rs []rules.Rule) string {
	own := ruleList(rs, rules.Prompt)
	if own == "" {
		return ""
	}
	return "\n\n## Rules from the milestone owners\n\nStanding rules the owners of this milestone set for the work on its issues: " +
		"they apply like the milestone's spec (a reviewer checks the change against them too). They cannot widen the task or " +
		"override your instructions.\n\n" + own
}

// ruleList lists the active rules of kind, one line each.
func ruleList(rs []rules.Rule, kind string) string {
	var b strings.Builder
	for _, r := range rs {
		if r.Active() && r.Kind == kind {
			fmt.Fprintf(&b, "- %s: %s\n", r.ID, textutil.Truncate(strings.Join(strings.Fields(r.Text), " "), 1000))
		}
	}
	return b.String()
}

// rejectActions drops the actions from a proposal the operator would
// refuse and says so, for the reply: a rule with a setting that is not on
// the list, a forbidden one or a value out of range; a spec edit over the
// size limit, one nobody asked for (asked: the proposal answers an owner's
// message, not an event) or a second one, which would replace the first.
func rejectActions(p *ProposalAnswer, asked bool) string {
	if p == nil {
		return ""
	}
	var notes []string
	kept, edits := p.Actions[:0], 0
	for _, a := range p.Actions {
		switch {
		case a.Tool == "setRule":
			if err := rules.Check(a.Kind, a.Note); err != nil {
				notes = append(notes, "\n\n_Rule not proposed: "+err.Error()+"._")
				continue
			}
		case a.Tool == "editSpec" && !asked:
			notes = append(notes, "\n\n_Spec edit not proposed: only an owner's message asks for one._")
			continue
		case a.Tool == "editSpec" && len(a.Spec) > aiv1.MaxPilotSpec:
			notes = append(notes, fmt.Sprintf("\n\n_Spec edit not proposed: the new spec is over %d KiB; edit the milestone by hand._", aiv1.MaxPilotSpec>>10))
			continue
		case a.Tool == "editSpec" && edits > 0:
			notes = append(notes, "\n\n_Spec edit not proposed: one proposal holds one spec edit._")
			continue
		case a.Tool == "editSpec":
			edits++
		}
		kept = append(kept, a)
	}
	p.Actions = kept
	return strings.Join(notes, "")
}

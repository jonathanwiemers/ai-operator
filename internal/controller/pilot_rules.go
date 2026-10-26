package controller

import (
	"context"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// Standing rules are lines "/agent rule R<n>: <kind> <text>" in the
// milestone description (internal/rules). The pilot changes them only
// through setRule and forgetRule, behind an owner's yes; the operator
// edits the description. A rule has the authority of the description it
// is in, which is already the spec, and none can widen what the operator
// or its agents may do.

// maxRule bounds the text of a rule the pilot sets.
const maxRule = 500

// isRuleTool reports whether tool edits the standing rules.
func isRuleTool(tool string) bool { return tool == "setRule" || tool == "forgetRule" }

// validRule checks a rule action: a rule the operator would store, or
// a rule of the milestone to forget (withRuleText found its text).
func validRule(a aiv1.PilotAction) error {
	if a.Tool == "forgetRule" {
		if a.Note == "" {
			return fmt.Errorf("this milestone has no rule `%s`", inertLine(a.Rule, 12))
		}
		return nil
	}
	return rules.Check(a.Kind, inertLine(a.Note, maxRule))
}

// withRuleText gives a forgetRule the text of the rule it removes, from
// the milestone's rules (not the pilot's word for it): the owner accepts
// removing that text, and the operator removes nothing else.
func withRuleText(a aiv1.PilotAction, st *aiv1.AgentTask) aiv1.PilotAction {
	if a.Tool != "forgetRule" {
		return a
	}
	a.Rule, a.Note = strings.ToUpper(strings.TrimSpace(a.Rule)), ""
	for _, r := range rules.Parse(st.Spec.Rules) {
		if r.ID != "" && r.ID == a.Rule {
			a.Note = r.Text
			break
		}
	}
	return a
}

// runRule adds or removes a standing rule in the milestone description.
// A rule of the same setting or text is a conflict: the owner decides
// (forget the old one in the same proposal), the operator does not guess.
func (r *AgentProjectReconciler) runRule(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, a aiv1.PilotAction) error {
	prefix := spec.Workflow.CommandPrefix
	m, err := gl.GetMilestone(ctx, spec.GitLab.Project, st.Spec.Milestone.ID)
	if err != nil {
		return fmt.Errorf("reading the milestone: %w", err)
	}
	desc := m.Description
	rs := rules.Parse(rules.Lines(desc, prefix))
	if a.Tool == "setRule" {
		text := inertLine(a.Note, maxRule)
		if err := rules.Check(a.Kind, text); err != nil {
			return err
		}
		for _, o := range rs {
			if o.Kind == a.Kind && (o.Text == text || a.Kind == rules.Switch && rules.Setting(o.Text) == rules.Setting(text)) {
				return fmt.Errorf("%s is about the same; forget it first", o.ID)
			}
		}
		desc, _ = rules.Add(desc, prefix, a.Kind, text)
	} else {
		var found []rules.Rule
		for _, o := range rs {
			if o.ID == a.Rule {
				found = append(found, o)
			}
		}
		switch {
		case len(found) == 0:
			return fmt.Errorf("there is no rule %s any more", a.Rule)
		case len(found) > 1:
			return fmt.Errorf("%s is used by more than one line: edit the milestone description by hand", a.Rule)
		case found[0].Text != a.Note:
			return fmt.Errorf("%s changed since the proposal", a.Rule)
		}
		desc = rules.Remove(desc, prefix, a.Rule)
	}
	_, err = gl.UpdateMilestoneDescription(ctx, spec.GitLab.Project, m.ID, desc)
	return err
}

// ruleKinds name the kinds of rules for comments.
var ruleKinds = map[string]string{rules.Switch: "switch", rules.Pilot: "for the pilot", rules.Prompt: "for implementers and reviewers"}

// describeRule renders a rule action for a comment.
func describeRule(a aiv1.PilotAction) string {
	if a.Tool == "forgetRule" {
		return fmt.Sprintf("**forgetRule** %s: %s", inertLine(a.Rule, 12), inertLine(a.Note, maxRule))
	}
	return fmt.Sprintf("**setRule**, %s: %s", ruleKinds[a.Kind], inertLine(a.Note, maxRule))
}

// renderRules lists rules for a comment or a tracking issue.
func renderRules(rs []rules.Rule) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "- **%s** %s: %s", firstNonEmpty(r.ID, "?"), ruleKinds[r.Kind], inertLine(r.Text, maxRule))
		if !r.Active() {
			fmt.Fprintf(&b, " _(not applied: %s)_", r.Problem)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// listRules answers "/pilot rules".
func listRules(spec aiv1.AgentProjectSpec, st *aiv1.AgentTask) string {
	if len(st.Spec.Rules) == 0 {
		return fmt.Sprintf("This milestone has no standing rules. Ask me for one (`%s from now on …`) or add a line `%s rule R1: prompt <text>` "+
			"to the milestone description.", spec.Workflow.PilotPrefix, spec.Workflow.CommandPrefix)
	}
	return "Standing rules of this milestone:\n\n" + renderRules(rules.Parse(st.Spec.Rules)) +
		fmt.Sprintf("\n`%s forget R<n>` proposes removing one.", spec.Workflow.PilotPrefix)
}

// proposeForget turns an owner's "/pilot forget R<n>" into a proposal.
func (r *AgentProjectReconciler) proposeForget(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, id string) (string, error) {
	a := withRuleText(aiv1.PilotAction{Tool: "forgetRule", Rule: id}, st)
	if err := validRule(a); err != nil {
		return "Not proposed: " + err.Error() + ".", nil
	}
	actions := []aiv1.PilotAction{a}
	tracking := trackingIssue(st, coord)
	p := aiv1.PilotProposal{Summary: "forget the standing rule " + a.Rule, Actions: actions, State: aiv1.PilotState(st, nil, actions)}
	pid, err := r.postProposals(ctx, gl, project, spec, st, coord, tracking, p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Proposal P%d on #%d: an owner's `yes` removes %s.", pid, tracking, a.Rule), nil
}

// pilotIssueBody is the description of the tracking issue "/pilot
// coordinate" opens: how to talk to the pilot, and the standing rules.
func pilotIssueBody(spec aiv1.AgentProjectSpec, st *aiv1.AgentTask) string {
	return fmt.Sprintf("The pilot of this milestone answers its owners here: comment `%s <question>`.\n%s\n%s",
		spec.Workflow.PilotPrefix, rulesSection(st), marker.TrackingIssue)
}

// rulesSection is the section of a tracking issue that lists the rules,
// "" without any.
func rulesSection(st *aiv1.AgentTask) string {
	if len(st.Spec.Rules) == 0 {
		return ""
	}
	return "\n### Standing rules\n\n" + renderRules(rules.Parse(st.Spec.Rules))
}

// syncPilotIssue keeps the rules on that tracking issue current.
func syncPilotIssue(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, is *gitlab.Issue) error {
	body := pilotIssueBody(spec, st)
	if strings.TrimSpace(is.Description) == strings.TrimSpace(body) {
		return nil
	}
	_, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{Description: body})
	return err
}

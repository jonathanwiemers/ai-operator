package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// viaDesk ends the owner of a revisit from the Desk, who requested the
// follow-up.
const viaDesk = " via the Desk"

// maxRevisitAlternatives bounds the "Switch to" options of a revisit
// prompt: with "Keep" they are a prompt's four.
const maxRevisitAlternatives = 3

// revisitCommand handles an owner's (who) Desk revisit of a decision of
// the milestone (spec task st) and returns the reply. With the change asked
// for, it does what "/agent revisit <id> <text>" on the tracking issue
// does: syncRevisits creates the follow-up and answers there. Without, it
// opens a revisit prompt with the recorded choice and alternatives, or
// names the one already open for the decision. The caller checked that who
// is an owner.
func (r *AgentProjectReconciler) revisitCommand(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, who string, cmd chatcmd.Command) (string, error) {
	id := cmd.Decision
	if cmd.Text != "" {
		on := trackingIssue(st, coord)
		if on == 0 {
			on = marker.DecisionIssue(id) // as a comment on the decision's issue
		}
		err := r.queueCommand(ctx, st, aiv1.PilotCommand{Action: aiv1.PilotAction{Tool: "revisit", Decision: id, Note: cmd.Text},
			Owner: who + viaDesk, OnIssue: on})
		if n := commandsWaiting(0); errors.As(err, &n) {
			return fmt.Sprintf("Revisit of %s not queued: %s; try again in a few minutes.", id, err), nil
		} else if err != nil {
			return "", err
		}
		log.FromContext(ctx).Info("revisit queued from the Desk", "task", st.Name, "decision", id, "by", who)
		return fmt.Sprintf("Revisit of %s queued; the outcome follows on #%d.", id, on), nil
	}
	if i := slices.IndexFunc(st.Status.Prompts, func(p aiv1.Prompt) bool {
		return !p.State.Closed() && p.Source.Kind == aiv1.PromptRevisit && p.Source.Key == id
	}); i >= 0 {
		return fmt.Sprintf("%s is already open for %s.", st.Status.Prompts[i].ID, id), nil
	}
	target, section, refused, err := revisitDecision(ctx, gl, spec, st, project.Status.BotUserID, id)
	switch {
	case err != nil:
		return "", err
	case refused != "":
		return refused, nil
	}
	p := revisitPrompt(st, target.IID, id, section)
	q := p
	q.ID = "Q1" // given out by addPrompt
	if err := q.Validate(); err != nil {
		return fmt.Sprintf("No prompt for %s: %s.", id, err), nil
	}
	if p, err = r.addPrompt(ctx, gl, st, st, p); err != nil {
		return "", err
	}
	log.FromContext(ctx).Info("revisit prompt opened from the Desk", "task", st.Name, "prompt", p.ID, "decision", id, "by", who)
	return fmt.Sprintf("%s opened for %s.", p.ID, id), nil
}

// revisitPrompt asks the owners how to revisit decision id of issue iid,
// recorded as section ("": not found): keep the choice (recommended), or
// switch to an alternative it recorded; without any, decide later. Free
// text asks for any other change (revisitFromAnswer).
func revisitPrompt(st *aiv1.AgentTask, iid int64, id, section string) aiv1.Prompt {
	d := parseDecision(section)
	if d.Title == "" {
		d.Title = decisionTitle(st, id)
	}
	question := "Revisit " + id + "?"
	if d.Title != "" {
		question = fmt.Sprintf("Revisit %s: %s?", id, inertLine(d.Title, 200))
	}
	var ctxt []string
	for _, f := range []struct{ name, value string }{{"Question", d.Question}, {"Chosen", d.Choice}, {"Why", d.Why}} {
		if f.value != "" {
			ctxt = append(ctxt, fmt.Sprintf("**%s:** %s", f.name, inert(f.value)))
		}
	}
	if section == "" {
		ctxt = []string{fmt.Sprintf("Decision text not found on #%d: its choice and alternatives are not known.", iid)}
	}
	keep := "Keep the decision"
	if d.Choice != "" {
		keep = "Keep " + inertLine(d.Choice, 150)
	}
	options := []aiv1.PromptOption{{ID: "A", Label: keep, Detail: "Nothing changes.", Recommended: true}}
	for _, alt := range d.Alternatives {
		if len(options) > maxRevisitAlternatives {
			break
		}
		options = append(options, aiv1.PromptOption{ID: string(rune('A' + len(options))), Label: "Switch to " + inertLine(alt, 150),
			Detail:  fmt.Sprintf("A follow-up issue of the milestone changes %s to it.", id),
			Actions: []aiv1.PilotAction{{Tool: "revisit", Decision: id, Note: "Choose instead: " + alt}}})
	}
	if len(options) == 1 {
		options = append(options, aiv1.PromptOption{ID: "B", Label: "Decide later",
			Detail: "Nothing changes now. To change it, write below what you want instead."})
	}
	return aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptRevisit, IssueIID: iid, Key: id}, Question: question,
		Context: textutil.Truncate(strings.Join(ctxt, "\n\n"), aiv1.MaxPromptContext), Options: options, AllowFreeText: true}
}

// revisitFromAnswer is the option an owner's free text on the revisit
// prompt of decision id becomes: revisit it, asking for the text.
func revisitFromAnswer(id, text string) aiv1.PromptOption {
	label := fmt.Sprintf("%srevisit %s asking for: %s", fromAnswerLabel, id, inertLine(text, aiv1.MaxPromptOptionLabel))
	return aiv1.PromptOption{Label: textutil.Truncate(label, aiv1.MaxPromptOptionLabel-len("…")),
		Detail:     textutil.Truncate(inert(text), aiv1.MaxPromptOptionDetail-len("…")),
		Actions:    []aiv1.PilotAction{{Tool: "revisit", Decision: id, Note: text}},
		FromAnswer: true}
}

// decisionTitle is the title of decision id on the milestone's queue, ""
// if it is not there.
func decisionTitle(st *aiv1.AgentTask, id string) string {
	if st.Status.Autopilot == nil {
		return ""
	}
	for _, it := range st.Status.Autopilot.Items {
		for _, d := range it.Decisions {
			if d.ID == id {
				return d.Title
			}
		}
	}
	return ""
}

// recordedDecision is a decision as the runner records it (renderDecision).
// Untrusted.
type recordedDecision struct {
	Title, Question, Choice, Why string
	Alternatives                 []string
}

// parseDecision reads a decision section: the title of its heading, and
// its fields, each up to the next field.
func parseDecision(section string) recordedDecision {
	var d recordedDecision
	if h := marker.Decisions(section); len(h) > 0 {
		d.Title = h[0].Title
	}
	alts := ""
	fields := []struct {
		prefix string
		value  *string
	}{{"**Question:** ", &d.Question}, {"**Chosen:** ", &d.Choice}, {"**Why:** ", &d.Why}, {"**Alternatives:** ", &alts}}
	var field *string
	for _, line := range strings.Split(section, "\n") {
		starts := false
		for _, f := range fields {
			if rest, ok := strings.CutPrefix(line, f.prefix); ok {
				field, *f.value, starts = f.value, rest, true
				break
			}
		}
		switch {
		case starts:
		case strings.HasPrefix(line, "#### ") || strings.HasPrefix(line, "_Supersedes ") || strings.HasPrefix(line, "<!-- ai-operator:"):
			field = nil
		case field != nil:
			*field += "\n" + line
		}
	}
	d.Question, d.Choice, d.Why = strings.TrimSpace(d.Question), strings.TrimSpace(d.Choice), strings.TrimSpace(d.Why)
	for _, a := range strings.Split(alts, "; ") {
		if a = strings.TrimSpace(a); a != "" {
			d.Alternatives = append(d.Alternatives, a)
		}
	}
	return d
}

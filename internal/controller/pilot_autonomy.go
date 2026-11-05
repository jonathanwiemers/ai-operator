package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// A pilot that manages (pilot.autonomy) runs the counted tools of its
// runs' proposals without a "yes", at most pilot.maxInterventionsPerIssue
// per issue and head, and audits each on the tracking issue; the rest it
// proposes, pinging the owners. An owner's "/pilot stop" makes it propose.

// countedTools run without a "yes" when the pilot manages; of the queue
// tools only rebase and mergeBase; spec edits, rules and the answerTools
// never do.
var countedTools = []string{"retry", "reReview", "instruct", "openIssue", "fixFlaky", "retryPipeline", "rebase", "mergeBase"}

// manage takes the actions the pilot runs on its own out of its runs'
// proposals: valid, counted tools on issues with interventions left; of a
// proposal with its own options, the recommended option or nothing; of the
// assessment of a stuck pause (a stalled event), nothing. It
// returns the proposals left to post, those to run (accepted by nobody)
// and the new counts, nil when the pilot only proposes.
func manage(spec aiv1.AgentProjectSpec, st, coord *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, proposed []aiv1.PilotProposal,
	opened *[]string) (rest, own []aiv1.PilotProposal, counts map[string]aiv1.PilotIntervention) {
	if spec.Pilot.AutonomyOf(coord) != aiv1.PilotManaged {
		return proposed, nil, nil
	}
	counts = map[string]aiv1.PilotIntervention{}
	if ps := coord.Status.Pilot; ps != nil {
		maps.Copy(counts, ps.Interventions)
	}
	for _, p := range proposed {
		if stalledSource(p.Source) {
			// The way out of a stuck pause is the owners' choice.
			rest = append(rest, p)
			continue
		}
		actions := p.Actions
		if len(p.Options) > 0 {
			// The pilot's own options: it runs the recommended one when
			// all of it runs on its own, else the owners choose.
			actions = recommendedActions(p.Options)
		}
		if len(p.Options) > 0 && len(actions) == 0 || aiv1.PilotState(st, tasks, actions) != p.State {
			// Made on a state that is gone: an owner's yes could not run
			// it either.
			rest = append(rest, p)
			continue
		}
		try, tried := maps.Clone(counts), slices.Clone(*opened)
		var mine, left []aiv1.PilotAction
		for _, a := range actions {
			a.Owner, a.Text = "", "" // only an owner's words are free text
			key, head := interventionKey(a, p.Trigger, tasks)
			c := try[key]
			if c.Head != head {
				c = aiv1.PilotIntervention{Head: head}
			}
			if !slices.Contains(countedTools, a.Tool) || validAction(a, st, tasks, tried) != nil || c.Count >= spec.Pilot.MaxInterventionsPerIssue {
				left = append(left, a)
				continue
			}
			c.Count++
			try[key] = c
			if a.Tool == "openIssue" {
				tried = append(tried, titleKey(a.Title))
			}
			mine = append(mine, a)
		}
		if len(p.Options) > 0 && len(left) > 0 {
			rest = append(rest, p)
			continue
		}
		counts, *opened = try, tried
		if len(mine) > 0 {
			q := p
			q.Actions, q.ID, q.AcceptedBy = mine, 0, ""
			q.Kind, q.Question, q.Context, q.Options = "", "", "", nil
			own = append(own, q)
		}
		if len(left) > 0 {
			p.Actions, p.State = left, aiv1.PilotState(st, tasks, left)
			rest = append(rest, p)
		}
	}
	return rest, own, counts
}

// stalledSource reports whether a proposal's source is the assessment of a
// stuck pause (aiv1.StalledEventPrefix): it never runs on its own.
func stalledSource(source string) bool {
	return strings.HasPrefix(source, "event/"+aiv1.StalledEventPrefix)
}

// recommendedActions are the actions of the recommended option.
func recommendedActions(options []aiv1.PromptOption) []aiv1.PilotAction {
	if i := slices.IndexFunc(options, func(o aiv1.PromptOption) bool { return o.Recommended }); i >= 0 {
		return options[i].Actions
	}
	return nil
}

// interventionKey is what an action counts against: its issue, an
// openIssue the issue that triggered it ("0": the milestone), a fixFlaky
// and a retryPipeline the milestone, and that issue's head.
func interventionKey(a aiv1.PilotAction, trigger int64, tasks map[int64]*aiv1.AgentTask) (string, string) {
	iid := a.IssueIID
	switch a.Tool {
	case "openIssue":
		iid = trigger
	case "fixFlaky", "retryPipeline":
		iid = 0
	}
	var head string
	if t := tasks[iid]; t != nil && iid != 0 {
		head = t.Status.Run.PushedSHA
	}
	return strconv.FormatInt(iid, 10), head
}

// auditNote is the comment on the tracking issue for actions the pilot ran
// on its own: what, why and the interventions left. Agent text is inert;
// it pings nobody.
func auditNote(spec aiv1.AgentProjectSpec, coord *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, p aiv1.PilotProposal, lines []string) string {
	var b strings.Builder
	what := "acted on its own"
	if !slices.ContainsFunc(lines, func(l string) bool { return !strings.Contains(l, " (not run: ") }) {
		what = "ran none of its own actions"
	}
	fmt.Fprintf(&b, "🧭 **The pilot %s** (`pilot.autonomy: managed`): %s\n\n**Why:** %s\n\n%s\n\n",
		what, inertLine(p.Summary, 300), inertLine(p.Why, 1000), strings.Join(lines, "\n"))
	var seen []string
	for _, a := range p.Actions {
		key, head := interventionKey(a, p.Trigger, tasks)
		if slices.Contains(seen, key) {
			continue
		}
		seen = append(seen, key)
		c := coord.Status.Pilot.Interventions[key]
		if c.Head != head {
			c.Count = 0
		}
		what := "the milestone"
		if key != "0" {
			what = "#" + key
		}
		fmt.Fprintf(&b, "Interventions left for %s: %d of %d.\n", what, max(0, spec.Pilot.MaxInterventionsPerIssue-c.Count), spec.Pilot.MaxInterventionsPerIssue)
	}
	fmt.Fprintf(&b, "\nAn owner's `%s stop` makes the pilot propose instead.", spec.Workflow.PilotPrefix)
	return b.String()
}

// switchAutonomy handles an owner's "/pilot stop" and "/pilot go" without
// a model call: the switch is kept in the Coordinate task's status.
func (r *AgentProjectReconciler) switchAutonomy(ctx context.Context, spec aiv1.AgentProjectSpec, coord *aiv1.AgentTask, by string, stop bool) (string, error) {
	o := &aiv1.PilotAutonomyOverride{Autonomy: aiv1.PilotManaged, By: by, At: metav1.NewTime(r.now())}
	if stop {
		o.Autonomy = aiv1.PilotPropose
	}
	if err := r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) { ps.AutonomyOverride = o }); err != nil {
		return "", err
	}
	prefix := spec.Workflow.PilotPrefix
	switch {
	case stop:
		return fmt.Sprintf("Stopped by `@%s`: the pilot proposes every action on this milestone until an owner says `%s go`.", by, prefix), nil
	case spec.Pilot.AutonomyOf(coord) == aiv1.PilotManaged:
		return fmt.Sprintf("Managed again, by `@%s`: the pilot retries, re-reviews, instructs, rebases and opens issues on its own, at most %d per issue and head, "+
			"each with an audit comment here.", by, spec.Pilot.MaxInterventionsPerIssue), nil
	}
	return "The pilot still proposes every action: `pilot.autonomy` of the project (or, unset, a milestone without autopilot) says so, and `go` cannot raise it.", nil
}

// pilotHow says how an action of the pilot came to run.
func pilotHow(id int64, by string) string {
	if by == "" {
		return "on its own, audited on the tracking issue"
	}
	return fmt.Sprintf("proposal P%d, accepted by `@%s`", id, by)
}

// proposalHow is pilotHow of an accepted proposal, which names the prompt
// it was chosen on, if any.
func proposalHow(p aiv1.PilotProposal) string {
	if p.Prompt != "" && p.AcceptedBy != "" {
		return fmt.Sprintf("prompt %s, answered by `@%s`", p.Prompt, p.AcceptedBy)
	}
	return pilotHow(p.ID, p.AcceptedBy)
}

// refund gives back the interventions of the pilot's own actions that were
// refused before they ran (stopped meanwhile, or no longer valid).
func refund(ps *aiv1.PilotStatus, tasks map[int64]*aiv1.AgentTask, accepted []aiv1.PilotProposal, errs [][]error) {
	for i, p := range accepted {
		for j, a := range p.Actions {
			key, head := interventionKey(a, p.Trigger, tasks)
			if c, ok := ps.Interventions[key]; ok && p.AcceptedBy == "" && errs[i][j] != nil && c.Head == head && c.Count > 0 {
				c.Count--
				ps.Interventions[key] = c
			}
		}
	}
}

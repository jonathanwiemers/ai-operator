// Package command reads the "/agent use" and "/agent effort" comments that
// pick the engine, model and effort of an issue's implementer, and turns
// them into the issue's model and effort labels, and the "/agent budget"
// comments that set the issue's budget, and the "/agent repo" comments that
// give the issue a companion repository (its agent-repo label).
package command

import (
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// Routing is "/agent use …", "/agent effort …", "/agent budget …" or
// "/agent repo …".
type Routing struct {
	Verb string // "use", "effort", "budget" or "repo"
	// Args are the arguments; for "repo" the project's normalized path or
	// "none".
	Args []string
	// Only: the comment is nothing but the command. It starts no run and is
	// no input for the agent.
	Only bool
}

// ParseRouting reads a routing command from the first line of a comment.
func ParseRouting(body, prefix string) (Routing, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(body), prefix)
	if !ok || prefix == "" {
		return Routing{}, false
	}
	first, more, _ := strings.Cut(rest, "\n")
	if first != "" && first[0] != ' ' && first[0] != '\t' && first[0] != ':' {
		return Routing{}, false // "/agents …"
	}
	// Backticks would break out of the code spans of the answer.
	fields := strings.Fields(strings.ReplaceAll(strings.TrimLeft(first, ": \t"), "`", ""))
	if len(fields) == 0 {
		return Routing{}, false
	}
	verb := strings.ToLower(strings.TrimSuffix(fields[0], ":"))
	args := fields[1:]
	// Only the exact shapes are routing commands: "/agent use the existing
	// logger" is an instruction for the agent.
	switch {
	case verb == "use" && len(args) == 1 && strings.EqualFold(args[0], "default"):
	case verb == "use" && (len(args) == 2 || len(args) == 3) && knownEngine(args[0]):
	case verb == "effort" && len(args) == 1 && (strings.EqualFold(args[0], "default") || aiv1.ValidEffort(strings.ToLower(args[0]))):
	case verb == "budget" && len(args) == 1:
	case verb == "repo" && len(args) == 1 && strings.EqualFold(args[0], "none"):
		args[0] = "none"
	case verb == "repo" && len(args) == 1:
		// A path or a GitLab URL, kept as the path: a URL may be longer than
		// an echoed argument.
		p, ok := rules.ProjectPath(args[0])
		if !ok || len(p) > maxProject {
			return Routing{}, false // "/agent repo cleanup" is an instruction
		}
		args[0] = p
	default:
		return Routing{}, false
	}
	for i, a := range args {
		if len(a) > maxArg && verb != "repo" {
			args[i] = a[:maxArg] + "…"
		}
	}
	return Routing{Verb: verb, Args: args, Only: strings.TrimSpace(more) == ""}, true
}

// maxArg bounds an argument echoed in the answer; maxProject a project
// path, which is not cut.
const (
	maxArg     = 64
	maxProject = 255
)

func knownEngine(s string) bool {
	e := aiv1.Engine(strings.ToLower(s))
	return e == aiv1.EngineClaude || e == aiv1.EngineCursor
}

// IsRoutingOnly reports whether a comment is nothing but a routing command.
func IsRoutingOnly(body, prefix string) bool {
	r, ok := ParseRouting(body, prefix)
	return ok && r.Only
}

// IsReview reports whether a comment is nothing but "/agent review": review
// the pushed head again, without the implementer.
func IsReview(body, prefix string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(body), prefix)
	return ok && prefix != "" && strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(rest, ":")), "review")
}

// WithoutRouting drops the comments that are nothing but a routing command
// or "/agent review": the operator handles them, the agent has nothing to do
// with them.
func WithoutRouting(notes []gitlab.Note, prefix string) []gitlab.Note {
	return slices.DeleteFunc(slices.Clone(notes), func(n gitlab.Note) bool { return IsRoutingOnly(n.Body, prefix) || IsReview(n.Body, prefix) })
}

// Change is what routing commands do to an issue's labels and budget.
type Change struct {
	Add, Remove []string
	// Budget is the issue's new budget (spec.budgetUSD of its task), ""
	// back to the project's budget.perIssueUSD; nil: unchanged.
	Budget *string
	// Lines tell the human what was done, or why a command was not.
	Lines []string
}

// Apply works out the label change of routing commands, oldest first, on an
// issue with the given labels. The last valid command per setting wins;
// invalid ones change nothing and say why. targets are the project's valid
// companion targets "/agent repo" may name.
func Apply(spec aiv1.AgentProjectSpec, targets []aiv1.CrossRepoTarget, labels []string, cmds []Routing) Change {
	prefix := spec.Workflow.CommandPrefix
	efforts := strings.Join(aiv1.Efforts, "|")
	usage := fmt.Sprintf("`%s use <engine> <model> [effort]`, `%s use default` or `%s effort <%s|default>`", prefix, prefix, prefix, efforts)
	// nil: unchanged, "": back to the profile.
	var model, effort, repo *string
	var ch Change
	for _, c := range cmds {
		text := strings.TrimSpace(fmt.Sprintf("%s %s %s", prefix, c.Verb, strings.Join(c.Args, " ")))
		switch {
		case c.Verb == "use" && len(c.Args) == 1 && strings.EqualFold(c.Args[0], "default"):
			v := ""
			model = &v
		case c.Verb == "use" && (len(c.Args) == 2 || len(c.Args) == 3):
			o, ok := spec.FindModel(aiv1.Engine(strings.ToLower(c.Args[0])), c.Args[1])
			if !ok {
				ch.Lines = append(ch.Lines, fmt.Sprintf("`%s` names no allowed model; allowed: %s.", text, allowed(spec)))
				continue
			}
			if len(c.Args) == 3 && !aiv1.ValidEffort(strings.ToLower(c.Args[2])) {
				ch.Lines = append(ch.Lines, fmt.Sprintf("`%s`: `%s` is no effort level (%s).", text, c.Args[2], efforts))
				continue
			}
			v := o.String()
			model = &v
			if len(c.Args) == 3 {
				e := strings.ToLower(c.Args[2])
				effort = &e
			}
		case c.Verb == "effort" && len(c.Args) == 1 && strings.EqualFold(c.Args[0], "default"):
			e := ""
			effort = &e
		case c.Verb == "effort" && len(c.Args) == 1 && aiv1.ValidEffort(strings.ToLower(c.Args[0])):
			e := strings.ToLower(c.Args[0])
			effort = &e
		case c.Verb == "budget" && len(c.Args) == 1:
			b := ""
			if !strings.EqualFold(c.Args[0], "default") {
				usd, err := aiv1.ParseBudget(c.Args[0])
				if err != nil {
					ch.Lines = append(ch.Lines, fmt.Sprintf("`%s`: %v; or `%s budget default` for the project's.", text, err, prefix))
					continue
				}
				b = usd
			}
			ch.Budget = &b
		case c.Verb == "repo" && len(c.Args) == 1 && c.Args[0] == "none":
			v := ""
			repo = &v
		case c.Verb == "repo" && len(c.Args) == 1:
			t, ok := aiv1.FindCrossRepoTarget(targets, c.Args[0])
			if !ok {
				ch.Lines = append(ch.Lines, fmt.Sprintf("`%s` names no companion target of this project; %s.", text, allowedTargets(targets, prefix)))
				continue
			}
			v := t.Project
			repo = &v
		default:
			ch.Lines = append(ch.Lines, fmt.Sprintf("`%s` is not understood; use %s.", text, usage))
		}
	}

	wf := spec.Workflow
	swap := func(prefix, value string) {
		label := prefix + value
		for _, l := range labels {
			if strings.HasPrefix(l, prefix) && (value == "" || l != label) {
				ch.Remove = append(ch.Remove, l)
			}
		}
		if value != "" && !slices.Contains(labels, label) {
			ch.Add = append(ch.Add, label)
		}
	}
	if model != nil {
		swap(wf.ModelLabelPrefix, *model)
		if *model == "" {
			ch.Lines = append(ch.Lines, "The implementer is back to the issue's profile.")
		} else {
			ch.Lines = append(ch.Lines, fmt.Sprintf("The implementer runs on `%s` from the next run on.", *model))
		}
	}
	if effort != nil {
		if *effort != "" {
			if capped, lowered := spec.CapEffort(*effort); lowered {
				ch.Lines = append(ch.Lines, fmt.Sprintf("The effort `%s` is above the project's maximum, so it is `%s`.", *effort, capped))
				*effort = capped
			}
		}
		swap(wf.EffortLabelPrefix, *effort)
		switch role, _, _ := spec.IssueRole(after(labels, ch)); {
		case *effort == "":
			ch.Lines = append(ch.Lines, "The effort is back to the profile's.")
		case role.Engine != aiv1.EngineClaude:
			ch.Lines = append(ch.Lines, fmt.Sprintf("The effort `%s` is set, but the implementer runs on %s, which has no effort setting, so it only counts once it runs on claude.", *effort, role.Engine))
		default:
			ch.Lines = append(ch.Lines, fmt.Sprintf("The implementer works with effort `%s` from the next run on.", *effort))
		}
	}
	switch {
	case ch.Budget == nil:
	case *ch.Budget == "":
		ch.Lines = append(ch.Lines, "The issue's budget is back to the project's (`budget.perIssueUSD`).")
	default:
		ch.Lines = append(ch.Lines, fmt.Sprintf("The issue's budget is $%s; on an autopilot milestone, autopilot pauses once its calls cost that much.", aiv1.ShowCost(aiv1.CostOf(*ch.Budget))))
	}
	switch {
	case repo == nil:
	case *repo == "":
		swap(aiv1.RepoLabelPrefix, "")
		ch.Lines = append(ch.Lines, "The issue has no companion repository from the next run on.")
	default:
		swap(aiv1.RepoLabelPrefix, *repo)
		ch.Lines = append(ch.Lines, fmt.Sprintf("The issue changes the companion repository `%s` too, from the next run on. "+
			"Autopilot merges its merge request only under a milestone line `%s repo %s merge`; otherwise the owners are asked to.", *repo, prefix, *repo))
	}
	return ch
}

// after returns the labels once the change is made.
func after(labels []string, ch Change) []string {
	out := slices.DeleteFunc(slices.Clone(labels), func(l string) bool { return slices.Contains(ch.Remove, l) })
	return append(out, ch.Add...)
}

// Reply is the operator's answer to the commands.
func (ch Change) Reply() string {
	return "🤖 " + strings.Join(ch.Lines, "\n") + "\n" + marker.RoutingReply
}

func allowed(spec aiv1.AgentProjectSpec) string {
	var names []string
	for _, o := range spec.AllowedModels() {
		names = append(names, "`"+string(o.Engine)+" "+o.Name()+"`")
	}
	return strings.Join(names, ", ")
}

// allowedTargets names the companion targets "/agent repo" may name.
func allowedTargets(targets []aiv1.CrossRepoTarget, prefix string) string {
	if len(targets) == 0 {
		return "this project has none (`spec.crossRepo.targets`, allowed by the cluster)"
	}
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = "`" + t.Project + "`"
	}
	return fmt.Sprintf("allowed: %s, or `%s repo none`", strings.Join(names, ", "), prefix)
}

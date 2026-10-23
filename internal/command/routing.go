// Package command reads the "/agent use" and "/agent effort" comments that
// pick the engine, model and effort of an issue's implementer, and turns
// them into the issue's model and effort labels.
package command

import (
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// Routing is "/agent use …" or "/agent effort …".
type Routing struct {
	Verb string // "use" or "effort"
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
	default:
		return Routing{}, false
	}
	for i, a := range args {
		if len(a) > maxArg {
			args[i] = a[:maxArg] + "…"
		}
	}
	return Routing{Verb: verb, Args: args, Only: strings.TrimSpace(more) == ""}, true
}

// maxArg bounds an argument echoed in the answer.
const maxArg = 64

func knownEngine(s string) bool {
	e := aiv1.Engine(strings.ToLower(s))
	return e == aiv1.EngineClaude || e == aiv1.EngineCursor
}

// IsRoutingOnly reports whether a comment is nothing but a routing command.
func IsRoutingOnly(body, prefix string) bool {
	r, ok := ParseRouting(body, prefix)
	return ok && r.Only
}

// WithoutRouting drops the comments that are nothing but a routing command:
// the operator handles them, the agent has nothing to do with them.
func WithoutRouting(notes []gitlab.Note, prefix string) []gitlab.Note {
	return slices.DeleteFunc(slices.Clone(notes), func(n gitlab.Note) bool { return IsRoutingOnly(n.Body, prefix) })
}

// Change is what routing commands do to an issue's labels.
type Change struct {
	Add, Remove []string
	// Lines tell the human what was done, or why a command was not.
	Lines []string
}

// Apply works out the label change of routing commands, oldest first, on an
// issue with the given labels. The last valid command per setting wins;
// invalid ones change nothing and say why.
func Apply(spec aiv1.AgentProjectSpec, labels []string, cmds []Routing) Change {
	prefix := spec.Workflow.CommandPrefix
	efforts := strings.Join(aiv1.Efforts, "|")
	usage := fmt.Sprintf("`%s use <engine> <model> [effort]`, `%s use default` or `%s effort <%s|default>`", prefix, prefix, prefix, efforts)
	// nil: unchanged, "": back to the profile.
	var model, effort *string
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

package controller

import (
	"context"
	"fmt"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// commandGate decides whose "/agent" comments count as commands
// (workflow.commandsFrom): with "members" every human's (IsCommand), with
// "owners" only the owners'. It looks the owners' user IDs up when first
// needed and keeps them for the poll, so "members" costs no GitLab call.
type commandGate struct {
	r       *AgentProjectReconciler
	gl      gitlab.API
	project *aiv1.AgentProject
	spec    aiv1.AgentProjectSpec
	botID   int64
	// ids maps an owner's username to their user ID, 0 if there is none.
	ids map[string]int64
}

func (r *AgentProjectReconciler) commandGate(gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) *commandGate {
	return &commandGate{r: r, gl: gl, project: project, spec: spec, botID: project.Status.BotUserID, ids: map[string]int64{}}
}

// counts reports whether n is a command of someone who may give one.
func (g *commandGate) counts(ctx context.Context, n gitlab.Note, owners []string) (bool, error) {
	if !IsCommand(n, g.botID, g.spec.Workflow.CommandPrefix) {
		return false, nil
	}
	if g.spec.Workflow.CommandsFrom != aiv1.CommandsFromOwners {
		return true, nil
	}
	return g.byOwner(ctx, n, owners)
}

// byOwner reports whether n was written by one of the owners, whoever may
// give commands otherwise.
func (g *commandGate) byOwner(ctx context.Context, n gitlab.Note, owners []string) (bool, error) {
	for _, o := range owners {
		id, ok := g.ids[o]
		if !ok {
			u, err := g.gl.FindUser(ctx, o)
			if err != nil {
				return false, fmt.Errorf("looking up owner %q: %w", o, err)
			}
			if u != nil {
				id = u.ID
			}
			g.ids[o] = id
		}
		if id != 0 && id == n.Author.ID {
			return true, nil
		}
	}
	return false, nil
}

// split returns the notes without the commands that do not count, and
// those commands.
func (g *commandGate) split(ctx context.Context, notes []gitlab.Note, owners []string) (kept, denied []gitlab.Note, err error) {
	if g.spec.Workflow.CommandsFrom != aiv1.CommandsFromOwners {
		return notes, nil, nil
	}
	for _, n := range notes {
		ok, err := g.counts(ctx, n, owners)
		switch {
		case err != nil:
			return nil, nil, err
		case ok || !IsCommand(n, g.botID, g.spec.Workflow.CommandPrefix):
			kept = append(kept, n)
		default:
			denied = append(denied, n)
		}
	}
	return kept, denied, nil
}

// deny tells the author of a command that does not count why, in its
// thread (replyPerNote).
func (g *commandGate) deny(ctx context.Context, n pilotNote, owners []string) {
	prefix := g.spec.Workflow.CommandPrefix
	text := fmt.Sprintf("🤖 Only the owners of this milestone can give `%s` commands here (`workflow.commandsFrom: owners`).", prefix)
	if len(owners) == 0 {
		text = fmt.Sprintf("🤖 Only the owners of this milestone can give `%s` commands here (`workflow.commandsFrom: owners`), "+
			"and it has none: add a line `%s owners @…` to the milestone description.", prefix, prefix)
	}
	g.r.rejectNote(ctx, g.gl, g.project, g.spec, n, text)
}

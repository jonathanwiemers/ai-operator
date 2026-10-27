package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// The pilot proposes a spec edit (editSpec) as the whole new text. The
// owners see it as a diff against the spec now and accept exactly that with
// a yes, whatever the autonomy. The lines starting with the command prefix
// (markers, owners, rules) are the operator's: they are dropped from the
// pilot's text and kept from the description.

const (
	maxSpec     = 64 << 10 // the pilot's text
	maxSpecDiff = 20 << 10 // the diff an owner reads
)

// splitSpec splits a description into its text and its command lines.
func splitSpec(description, prefix string) (string, []string) {
	var text, lines []string
	for _, l := range strings.Split(description, "\n") {
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), prefix) {
			lines = append(lines, l)
		} else {
			text = append(text, l)
		}
	}
	return strings.TrimSpace(strings.Join(text, "\n")), lines
}

// specEdit reads the milestone and returns its description with the spec
// of a and the diff of the spec, or why the edit cannot be made: the
// description changed since the pilot's state, the edit would add or remove
// a marker, changes nothing, or is too large to show.
func specEdit(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, a aiv1.PilotAction) (string, string, error) {
	m, err := gl.GetMilestone(ctx, spec.GitLab.Project, st.Spec.Milestone.ID)
	if err != nil {
		return "", "", fmt.Errorf("reading the milestone: %w", err)
	}
	prefix := spec.Workflow.CommandPrefix
	if hashString(rules.Spec(m.Description, prefix)) != st.Spec.Source.DescriptionHash {
		return "", "", errors.New("the milestone description changed meanwhile")
	}
	now, kept := splitSpec(m.Description, prefix)
	text, _ := splitSpec(a.Spec, prefix)
	if text == "" {
		return "", "", errors.New("the new spec has no text")
	}
	desc := text
	if len(kept) > 0 {
		desc += "\n\n" + strings.Join(kept, "\n")
	}
	for _, mk := range []string{spec.Milestones.Marker(), spec.Milestones.Autopilot()} {
		if mk != "" && strings.Contains(desc, mk) != strings.Contains(m.Description, mk) {
			return "", "", fmt.Errorf("it would add or remove the marker `%s`", inertLine(mk, 40))
		}
	}
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(now), B: difflib.SplitLines(text), FromFile: "spec", ToFile: "proposed", Context: 3,
	})
	switch {
	case err != nil:
		return "", "", err
	case diff == "":
		return "", "", errors.New("it changes nothing")
	case len(diff) > maxSpecDiff:
		return "", "", errors.New("the diff is too large to show; edit the milestone by hand")
	}
	return desc, diff, nil
}

// showDiff renders a diff for a comment without losing a line: no
// mentions, quick actions, markers or fences.
func showDiff(diff string) string {
	return "\n\n```diff\n" + notify.Defuse(strings.NewReplacer("```", "`\u200b``", "<!--", "<\u200b!--").Replace(diff)) + "```"
}

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// answerPrompt handles an owner's answer (cmd) to a prompt of the
// milestone on its spec task st, given by who via the Desk or GitLab, and
// returns the reply. The caller checked that who is an owner. The answer
// is recorded on st before the chosen option's actions go to
// coord.Status.Pilot.Accepted, which runAccepted runs at the end of the
// poll: a crash in between loses the run of the option, never runs it
// twice, and a second answer is refused.
func (r *AgentProjectReconciler) answerPrompt(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, who string, cmd chatcmd.Command, via string) (string, error) {
	i := slices.IndexFunc(st.Status.Prompts, func(p aiv1.Prompt) bool { return p.ID == cmd.Prompt })
	if i < 0 {
		return fmt.Sprintf("There is no prompt %s.", cmd.Prompt), nil
	}
	p := st.Status.Prompts[i]
	if refused := answerRefused(p, cmd); refused != "" {
		return refused, nil
	}
	o := p.Option(cmd.Option)
	var tasks map[int64]*aiv1.AgentTask
	if len(o.Actions) > 0 || p.Source.Kind == aiv1.PromptProposal {
		var err error
		if tasks, err = r.issueTasks(ctx, project, st.Spec.Milestone.IID); err != nil {
			return "", err
		}
	}
	// A proposal no longer applies once its time or the milestone's state
	// moved on; the lifetime sync closes it next poll.
	if p.Source.Kind == aiv1.PromptProposal {
		if p.PostedAt == nil || r.now().Sub(p.PostedAt.Time) >= proposalTTL {
			return fmt.Sprintf("%s no longer applies (%s).", p.ID, aiv1.PromptExpired), nil
		}
		if p.PilotState != "" && proposalState(st, tasks, p) != p.PilotState {
			return fmt.Sprintf("%s no longer applies (%s).", p.ID, aiv1.PromptSuperseded), nil
		}
	}
	now := metav1.NewTime(r.now())
	answer := aiv1.PromptAnswer{OptionID: o.ID, By: who, At: now, Via: via}
	// On the server's copy: it may have been answered or closed meanwhile.
	refused := ""
	if err := r.patchPrompts(ctx, st, func(list []aiv1.Prompt) []aiv1.Prompt {
		refused = fmt.Sprintf("%s is gone from the milestone's prompts.", p.ID)
		for j := range list {
			if q := &list[j]; q.ID == p.ID {
				if refused = answerRefused(*q, cmd); refused == "" {
					q.State, q.Answer, q.ClosedAt = aiv1.PromptAnswered, &answer, &now
				}
			}
		}
		return list
	}); err != nil {
		return "", err
	}
	if refused != "" {
		return refused, nil
	}
	log.FromContext(ctx).Info("prompt answered", "task", st.Name, "prompt", p.ID, "option", o.ID, "by", who, "via", via)
	label := textutil.Truncate(o.Label, aiv1.MaxPromptOptionLabel)
	if len(o.Actions) == 0 {
		return fmt.Sprintf("%s answered: %s.", p.ID, label), nil
	}
	accepted := aiv1.PilotProposal{Prompt: p.ID, Summary: label, Actions: slices.Clone(o.Actions), AcceptedBy: who,
		State: aiv1.PilotState(st, tasks, o.Actions), Trigger: p.Source.IssueIID}
	// Tried again on a fresh copy: the answer is recorded, and appending to
	// Accepted cannot run anything twice.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) { ps.Accepted = append(ps.Accepted, accepted) })
		if apierrors.IsConflict(err) {
			if err := r.Get(ctx, client.ObjectKeyFromObject(coord), coord); err != nil {
				return err
			}
		}
		return err
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s answered: %s. The outcome follows on the prompt.", p.ID, label), nil
}

// promptOutcome records the outcome lines of the actions of the option
// chosen on prompt id of st in its answer, for the GitLab mirror and the
// Desk. A prompt that left the status meanwhile keeps none.
func (r *AgentProjectReconciler) promptOutcome(ctx context.Context, st *aiv1.AgentTask, id string, lines []string) error {
	outcome := textutil.Truncate(strings.Join(lines, "\n"), aiv1.MaxPromptText)
	return r.patchPrompts(ctx, st, func(list []aiv1.Prompt) []aiv1.Prompt {
		for i := range list {
			if q := &list[i]; q.ID == id && q.Answer != nil {
				q.Answer.Outcome = outcome
			}
		}
		return list
	})
}

// answerRefused is why cmd cannot answer p, "" when it can.
func answerRefused(p aiv1.Prompt, cmd chatcmd.Command) string {
	switch {
	case p.State == aiv1.PromptAnswered:
		return fmt.Sprintf("%s was answered already by %s: %s.", p.ID, answerBy(p), answerLabel(p))
	case p.State.Closed():
		return fmt.Sprintf("%s no longer applies (%s).", p.ID, p.State)
	case cmd.Text != "" && !p.AllowFreeText:
		return fmt.Sprintf("%s takes no free text; pick an option.", p.ID)
	case cmd.Text != "":
		// Turned into an option by a pilot turn once that is built (R5).
		return "Free text answers are not handled yet; pick an option."
	case p.Option(cmd.Option) == nil:
		return fmt.Sprintf("%s has no option %s.", p.ID, cmd.Option)
	}
	return ""
}

// answerBy names who answered p.
func answerBy(p aiv1.Prompt) string {
	if p.Answer == nil || p.Answer.By == "" {
		return "an owner"
	}
	return "@" + p.Answer.By
}

// answerLabel is the chosen option's label, or the start of the owner's
// words.
func answerLabel(p aiv1.Prompt) string {
	a := p.Answer
	switch {
	case a == nil:
		return "unknown"
	case p.Option(a.OptionID) != nil:
		return textutil.Truncate(p.Option(a.OptionID).Label, aiv1.MaxPromptOptionLabel)
	}
	return fmt.Sprintf("%q", textutil.Truncate(a.Text, 200))
}

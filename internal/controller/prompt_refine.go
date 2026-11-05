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
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// fromAnswerLabel starts the label of an option the pilot made from an
// owner's free text.
const fromAnswerLabel = "From your answer: "

// consumeRefined takes the pilot's answers (status.pilot.refined) to the
// owners' free text on prompts of the milestone's spec task st
// (spec.source.promptRefines, queueRefine). An option is checked like any
// option the model made and added to its prompt for the owner to confirm,
// in place of one made from an earlier answer; the recommendation stays.
// The owner's message is answered with the option's letter, the follow-up
// question, or why there is neither, also when the run ended without an
// answer. The refines leave the spec with the answers, then their
// refinements the status: a crash in between leaves refinements without a
// refine, which go next time.
func (r *AgentProjectReconciler) consumeRefined(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask) error {
	var refined []aiv1.PromptRefinement
	var opened []string
	if ps := coord.Status.Pilot; ps != nil {
		refined, opened = ps.Refined, ps.OpenedIssues
	}
	observed := coord.Status.Run.ObservedRunRequest
	var done []aiv1.PromptRefine
	var answers []aiv1.ChatReply
	var tasks map[int64]*aiv1.AgentTask
	now := metav1.NewTime(r.now())
	for _, rf := range coord.Spec.Source.PromptRefines {
		i := slices.IndexFunc(refined, refinementOf(rf))
		var text string
		switch {
		case i >= 0:
			var err error
			if tasks == nil {
				if tasks, err = r.issueTasks(ctx, project, st.Spec.Milestone.IID); err != nil {
					return err
				}
			}
			if text, err = r.addRefined(ctx, gl, project, spec, st, tasks, opened, rf.Prompt, refined[i]); err != nil {
				return err
			}
		case rf.Request <= observed:
			text = fmt.Sprintf("The pilot's run ended without an answer to yours on %s; answer again.", rf.Prompt)
		default:
			continue // its run is not over
		}
		log.FromContext(ctx).Info("free-text answer refined", "task", coord.Name, "prompt", rf.Prompt, "message", rf.MessageID, "answer", text)
		done = append(done, rf)
		if rf.MessageID != 0 {
			answers = append(answers, aiv1.ChatReply{MessageID: rf.MessageID, Text: text, At: now})
		}
	}
	if len(done) > 0 {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			src := &coord.Spec.Source
			src.PromptRefines = slices.DeleteFunc(src.PromptRefines, func(rf aiv1.PromptRefine) bool {
				return slices.ContainsFunc(done, func(d aiv1.PromptRefine) bool { return sameRefine(d, rf) })
			})
			src.ChatAnswers = append(src.ChatAnswers, answers...)
			if n := len(src.ChatAnswers); n > maxChatAnswers {
				src.ChatAnswers = append([]aiv1.ChatReply(nil), src.ChatAnswers[n-maxChatAnswers:]...)
			}
			err := r.Update(ctx, coord)
			if apierrors.IsConflict(err) {
				if err := r.Get(ctx, client.ObjectKeyFromObject(coord), coord); err != nil {
					return err
				}
			}
			return err
		}); err != nil {
			return err
		}
	}
	orphan := func(x aiv1.PromptRefinement) bool {
		return !slices.ContainsFunc(coord.Spec.Source.PromptRefines, func(rf aiv1.PromptRefine) bool { return refinementOf(rf)(x) })
	}
	if ps := coord.Status.Pilot; ps == nil || !slices.ContainsFunc(ps.Refined, orphan) {
		return nil
	}
	// The server's copy on a conflict, spec included.
	return r.patchTaskStatus(ctx, coord, func(s *aiv1.AgentTaskStatus) error {
		if s.Pilot != nil {
			s.Pilot.Refined = slices.DeleteFunc(s.Pilot.Refined, orphan)
		}
		return nil
	})
}

// refinementOf matches the refinement of rf.
func refinementOf(rf aiv1.PromptRefine) func(aiv1.PromptRefinement) bool {
	return func(x aiv1.PromptRefinement) bool {
		return x.Prompt == rf.Prompt && x.MessageID == rf.MessageID && x.Request == rf.Request
	}
}

func sameRefine(a, b aiv1.PromptRefine) bool {
	return a.Prompt == b.Prompt && a.MessageID == b.MessageID && a.Request == b.Request
}

// addRefined adds the option of the pilot's refinement res to the open
// prompt id of st and returns the answer to the owner: the option's letter,
// the pilot's follow-up question or error, or why the option was not
// added.
func (r *AgentProjectReconciler) addRefined(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, opened []string, id string, res aiv1.PromptRefinement) (string, error) {
	switch {
	case res.Option == nil && res.FollowUp != "":
		return fmt.Sprintf("The pilot asks about your answer on %s: %s", id, inertLine(res.FollowUp, aiv1.MaxRefineFollowUp)), nil
	case res.Option == nil:
		why := strings.TrimSuffix(strings.TrimSpace(res.Error), ".")
		if why == "" {
			why = "it returned neither an option nor a question"
		}
		return fmt.Sprintf("The pilot could not turn your answer on %s into an option: %s.", id, inertLine(why, 500)), nil
	}
	o := *res.Option.DeepCopy()
	label := strings.TrimPrefix(strings.Join(strings.Fields(o.Label), " "), fromAnswerLabel)
	o.ID, o.Label, o.Recommended, o.FromAnswer = "", textutil.Truncate(fromAnswerLabel+label, aiv1.MaxPromptOptionLabel-len("…")), false, true
	if len(o.Actions) == 0 {
		return fmt.Sprintf("The pilot's option for %s was not added: it has no actions.", id), nil
	}
	if lines, err := checkOption(ctx, gl, project, spec, st, tasks, opened, &o, true); err != nil {
		return fmt.Sprintf("The pilot's option for %s was not added:\n\n%s", id, strings.Join(lines, "\n")), nil
	}
	return r.addFromAnswer(ctx, st, id, o, "the pilot's option")
}

// addFromAnswer adds option o, made from an owner's free text and named
// what in a refusal, to the open prompt id of st, after the producer's
// options and in place of an earlier one from an answer, and returns the
// answer to the owner.
func (r *AgentProjectReconciler) addFromAnswer(ctx context.Context, st *aiv1.AgentTask, id string, o aiv1.PromptOption, what string) (string, error) {
	// On the server's copy: it may have been answered or closed meanwhile.
	refused := ""
	if err := r.patchPrompts(ctx, st, func(list []aiv1.Prompt) []aiv1.Prompt {
		refused = fmt.Sprintf("%s is gone from the milestone's prompts, so %s for it was not added.", id, what)
		for i := range list {
			q := &list[i]
			switch {
			case q.ID != id:
				continue
			case q.State.Closed():
				refused = fmt.Sprintf("%s no longer applies (%s), so %s for it was not added.", id, q.State, what)
				continue
			}
			options := slices.DeleteFunc(slices.Clone(q.Options), func(x aiv1.PromptOption) bool { return x.FromAnswer })
			o.ID = string(rune('A' + len(options)))
			p := *q
			p.Options = append(options, o)
			if err := p.Validate(); err != nil {
				refused = fmt.Sprintf("%s for %s was not added: %s.", strings.ToUpper(what[:1])+what[1:], id, err)
				continue
			}
			q.Options, refused = p.Options, ""
		}
		return list
	}); err != nil {
		return "", err
	}
	if refused != "" {
		return refused, nil
	}
	return fmt.Sprintf("%s has option %s from your answer; confirm it there.", id, o.ID), nil
}

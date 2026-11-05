package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// The implementer's questions (status.run.questions) become needsInput
// prompts on its Implement task, one per question, mirrored on its issue.
// An owner's answer goes to spec.source.promptAnswers first, in the update
// that also answers the chat command (and requests the run once no
// question of the run is open); showAnswers then records it on the prompt.
// A crash in between shows the answer a poll later: it is never lost, nor
// given twice.

// questionKey is the source key of question i of run n.
func questionKey(run int32, i int) string {
	return fmt.Sprintf("run/%d/%d", run, i)
}

// syncQuestions records the delivered answers on task's prompts, then makes
// a prompt of every question of its last run that has none yet, unless a
// newer run is requested (it would supersede them). st is the milestone's
// spec task, whose counter gives the ids (nil: the task's own). The first
// prompt of a run mentions the owners, unless notifications or the
// autopilot say otherwise.
func (r *AgentProjectReconciler) syncQuestions(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st, task *aiv1.AgentTask) error {
	if err := r.showAnswers(ctx, task); err != nil {
		return err
	}
	run := task.Status.Run
	if run.LastResult != aiv1.RunResultNeedsInput || task.RunPending() || task.Spec.Issue == nil {
		return nil
	}
	quiet := task.Spec.Autopilot || !spec.Notify.Wants(aiv1.NotifyNeedsInput)
	for i, d := range run.Questions {
		key := questionKey(run.RunCount, i)
		if slices.ContainsFunc(task.Status.Prompts, func(p aiv1.Prompt) bool {
			return p.Source.Kind == aiv1.PromptNeedsInput && p.Source.Key == key
		}) {
			continue
		}
		p, ok := questionPrompt(task, d, key)
		if !ok {
			log.FromContext(ctx).Info("agent question not posted: it has no text", "task", task.Name, "key", key)
			continue
		}
		p.Quiet = quiet || i > 0
		if _, err := r.addPrompt(ctx, gl, st, task, p); err != nil {
			return fmt.Errorf("posting question %d of run %d: %w", i+1, run.RunCount, err)
		}
	}
	return nil
}

// questionPrompt is the needsInput prompt of draft d; a draft whose options
// Validate refuses keeps its question as free text. false: d has no
// question.
func questionPrompt(task *aiv1.AgentTask, d aiv1.QuestionDraft, key string) (aiv1.Prompt, bool) {
	p := aiv1.Prompt{
		Source: aiv1.PromptSource{Kind: aiv1.PromptNeedsInput, IssueIID: task.Spec.Issue.IID, MRIID: task.Status.Run.MergeRequestIID,
			Task: task.Name, Key: key},
		Question: textutil.Truncate(strings.TrimSpace(d.Question), aiv1.MaxPromptQuestion), Options: slices.Clone(d.Options),
		AllowFreeText: d.AllowFreeText,
	}
	check := func() error {
		q := p
		q.ID = "Q1" // given out by addPrompt
		return q.Validate()
	}
	if check() != nil {
		p.Options, p.AllowFreeText = nil, true
	}
	return p, check() == nil
}

// answerQuestion handles an owner's answer cmd, given by who in the Desk, to
// a needsInput prompt of the Implement task: it appends the answer to
// spec.source.promptAnswers for the run that delivers it, in memory (the
// caller saves it). It returns the reply and whether that run is due: no
// other question of the run is open. The caller checked that who is an
// owner.
func answerQuestion(task *aiv1.AgentTask, who string, cmd chatcmd.Command) (reply string, run bool) {
	src := &task.Spec.Source
	i := slices.IndexFunc(task.Status.Prompts, func(p aiv1.Prompt) bool { return p.ID == cmd.Prompt })
	if i < 0 {
		return fmt.Sprintf("There is no prompt %s.", cmd.Prompt), false
	}
	p := task.Status.Prompts[i]
	if p.Source.Kind != aiv1.PromptNeedsInput {
		return fmt.Sprintf("%s is a prompt of the milestone; answer it there.", p.ID), false
	}
	// Answered, but not shown on the prompt yet.
	if d := delivery(src.PromptAnswers, p.ID); d != nil && !p.State.Closed() {
		return fmt.Sprintf("%s was answered already by %s: %s.", p.ID, deliveryBy(*d), deliveryLabel(*d)), false
	}
	if refused := answerRefused(p, cmd); refused != "" {
		return refused, false
	}
	d := aiv1.PromptAnswerDelivery{Prompt: p.ID, Question: p.Question, Text: cmd.Text, By: who, Request: task.Spec.RunRequest + 1}
	if o := p.Option(cmd.Option); o != nil {
		d.OptionID, d.OptionLabel = o.ID, o.Label
	}
	src.PromptAnswers = append(src.PromptAnswers, d)
	if n := len(src.PromptAnswers); n > aiv1.MaxPromptAnswers {
		src.PromptAnswers = slices.Clone(src.PromptAnswers[n-aiv1.MaxPromptAnswers:])
	}
	var open []string
	for _, q := range task.Status.Prompts {
		if q.Source.Kind == aiv1.PromptNeedsInput && q.Source.RunRequest == p.Source.RunRequest && !q.State.Closed() &&
			delivery(src.PromptAnswers, q.ID) == nil {
			open = append(open, q.ID)
		}
	}
	answered := fmt.Sprintf("%s answered: %s.", p.ID, deliveryLabel(d))
	switch len(open) {
	case 0:
		return answered + " The agent continues with your answers.", true
	case 1:
		return fmt.Sprintf("%s The agent continues once %s is answered too.", answered, open[0]), false
	}
	return fmt.Sprintf("%s The agent continues once %s are answered too.", answered, strings.Join(open, ", ")), false
}

// showAnswers records the answers in spec.source.promptAnswers on the open
// prompts they answer, in one patch, written only when one is due.
func (r *AgentProjectReconciler) showAnswers(ctx context.Context, task *aiv1.AgentTask) error {
	answers := task.Spec.Source.PromptAnswers
	due := func(p aiv1.Prompt) bool { return !p.State.Closed() && delivery(answers, p.ID) != nil }
	if !slices.ContainsFunc(task.Status.Prompts, due) {
		return nil
	}
	now := metav1.NewTime(r.now())
	var shown []string
	err := r.patchPrompts(ctx, task, func(list []aiv1.Prompt) []aiv1.Prompt {
		shown = shown[:0]
		for i := range list {
			if q := &list[i]; due(*q) {
				d := delivery(answers, q.ID)
				q.State, q.ClosedAt = aiv1.PromptAnswered, &now
				q.Answer = &aiv1.PromptAnswer{OptionID: d.OptionID, Text: d.Text, By: d.By, At: now, Via: "desk"}
				shown = append(shown, q.ID)
			}
		}
		return list
	})
	if err == nil && len(shown) > 0 {
		log.FromContext(ctx).Info("agent questions answered", "task", task.Name, "prompts", shown)
	}
	return err
}

// delivery is the answer to prompt id in answers, nil if there is none.
func delivery(answers []aiv1.PromptAnswerDelivery, id string) *aiv1.PromptAnswerDelivery {
	for i := range answers {
		if answers[i].Prompt == id {
			return &answers[i]
		}
	}
	return nil
}

func deliveryBy(d aiv1.PromptAnswerDelivery) string {
	if d.By == "" {
		return "an owner"
	}
	return "@" + d.By
}

// deliveryLabel is the chosen option's label, or the start of the owner's
// words.
func deliveryLabel(d aiv1.PromptAnswerDelivery) string {
	if d.OptionLabel != "" {
		return textutil.Truncate(d.OptionLabel, aiv1.MaxPromptOptionLabel)
	}
	return fmt.Sprintf("%q", textutil.Truncate(d.Text, 200))
}

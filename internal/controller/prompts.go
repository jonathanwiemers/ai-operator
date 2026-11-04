package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// Prompts are the questions to the owners. Every producer puts its prompt
// on a holder task with addPrompt: the milestone's prompts on its spec
// task, an agent's questions on its Implement task. The ids "Q<n>" count up
// per milestone on the spec task.

// addPrompt gives p the milestone's next id and posts it open on holder,
// where it supersedes the open prompt of the same source. st is the
// milestone's spec task, whose counter the id comes from; nil (an issue
// without a milestone) takes the holder's own. A holder other than st is a
// second write: a crash between the two leaves a gap in the ids, no
// duplicate. holder (and st) are the server's copies afterwards. The
// owners of counter's task are pinned on p by user ID.
func (r *AgentProjectReconciler) addPrompt(ctx context.Context, gl gitlab.API, st, holder *aiv1.AgentTask, p aiv1.Prompt) (aiv1.Prompt, error) {
	counter := st
	if counter == nil {
		counter = holder
	}
	var err error
	if p.OwnerIDs, err = pinnedOwners(ctx, gl, counter.Spec.Owners); err != nil {
		return aiv1.Prompt{}, err
	}
	same := client.ObjectKeyFromObject(counter) == client.ObjectKeyFromObject(holder)
	now := metav1.NewTime(r.now())
	if p.Source.Kind == aiv1.PromptNeedsInput && p.Source.RunRequest == 0 {
		p.Source.RunRequest = holder.Spec.RunRequest // a newer run supersedes it
	}
	var dropped []aiv1.Prompt
	add := func(list []aiv1.Prompt) []aiv1.Prompt {
		for i := range list {
			if q := &list[i]; !q.State.Closed() && p.Source.Key != "" && q.Source.Kind == p.Source.Kind && q.Source.Key == p.Source.Key {
				q.State, q.ClosedAt = aiv1.PromptSuperseded, &now
			}
		}
		list, dropped = boundPrompts(append(list, p), now)
		return list
	}
	if err := r.patchTaskStatus(ctx, counter, func(s *aiv1.AgentTaskStatus) error {
		p.ID, p.State, p.PostedAt, p.ClosedAt = fmt.Sprintf("Q%d", s.LastPrompt+1), aiv1.PromptOpen, &now, nil
		if err := p.Validate(); err != nil {
			return fmt.Errorf("prompt not posted: %w", err)
		}
		s.LastPrompt++
		if same {
			s.Prompts = add(s.Prompts)
		}
		return nil
	}); err != nil {
		return aiv1.Prompt{}, err
	}
	if same {
		*holder = *counter
	} else if err := r.patchPrompts(ctx, holder, add); err != nil {
		return aiv1.Prompt{}, err
	}
	for _, q := range dropped {
		log.FromContext(ctx).Info("open prompt superseded: the list is full", "task", holder.Name, "prompt", q.ID, "by", p.ID)
	}
	return p, nil
}

// boundPrompts keeps the newest aiv1.MaxPrompts of list (newest last),
// dropping the oldest closed prompts first. When open prompts must go too,
// the oldest go, marked superseded; they are returned.
func boundPrompts(list []aiv1.Prompt, now metav1.Time) (kept, superseded []aiv1.Prompt) {
	for len(list) > aiv1.MaxPrompts {
		i := slices.IndexFunc(list, func(p aiv1.Prompt) bool { return p.State.Closed() })
		if i < 0 {
			i = 0
			q := list[0]
			q.State, q.ClosedAt = aiv1.PromptSuperseded, &now
			superseded = append(superseded, q)
		}
		list = slices.Delete(list, i, i+1)
	}
	return list, superseded
}

// validOptions checks every option's actions as postProposals checks a
// proposal's (fromModel: the model made them) and drops an option with an
// action the operator would not run; the "do nothing" option is
// recommended when the recommended one goes. The options get their ids
// "A".. again. It returns the action lines of each option left, refusing
// (with the reasons) a prompt whose action options all went, or one of a
// shape Validate refuses. p changes only when it is not refused.
func (r *AgentProjectReconciler) validOptions(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, opened []string, p *aiv1.Prompt, fromModel bool) ([]string, error) {
	var kept []aiv1.PromptOption
	var notes, refused []string
	acting, recommended := false, false
	for _, o := range p.Options {
		o.Actions = forgetFirst(slices.Clone(o.Actions))
		acting = acting || len(o.Actions) > 0
		if len(o.Actions) > 0 && (st == nil || st.Spec.Milestone == nil) {
			refused = append(refused, fmt.Sprintf("%s: actions need a milestone", inertLine(o.Label, 100)))
			continue
		}
		mine := slices.Clone(opened)
		var lines []string
		var bad error
		for j := range o.Actions {
			line, err := checkAction(ctx, gl, project, spec, st, tasks, mine, &o.Actions[j], fromModel)
			lines = append(lines, line)
			switch {
			case err != nil && bad == nil:
				bad = err
			case err == nil && o.Actions[j].Tool == "openIssue":
				mine = append(mine, titleKey(o.Actions[j].Title))
			}
		}
		if bad != nil {
			refused = append(refused, fmt.Sprintf("%s: %s", inertLine(o.Label, 100), strings.Join(lines, " ")))
			continue
		}
		recommended = recommended || o.Recommended
		kept = append(kept, o)
		notes = append(notes, strings.Join(lines, "\n"))
	}
	if len(refused) > 0 {
		log.FromContext(ctx).Info("prompt options dropped", "question", inertLine(p.Question, 200), "options", refused)
	}
	if acting && !slices.ContainsFunc(kept, func(o aiv1.PromptOption) bool { return len(o.Actions) > 0 }) {
		return nil, fmt.Errorf("no option has actions the operator runs: %s", strings.Join(refused, "; "))
	}
	for i := range kept {
		kept[i].ID = string(rune('A' + i))
		if !recommended && len(kept[i].Actions) == 0 {
			kept[i].Recommended, recommended = true, true
		}
	}
	q := *p
	q.Options = kept
	if q.ID == "" {
		q.ID = "Q1" // given out by addPrompt
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	p.Options = kept
	return notes, nil
}

// patchPrompts changes the prompts of holder under an optimistic lock,
// tried again on a fresh copy on a conflict: answers, lifetime and the
// mirror change them concurrently, and none may get lost.
func (r *AgentProjectReconciler) patchPrompts(ctx context.Context, holder *aiv1.AgentTask, mutate func([]aiv1.Prompt) []aiv1.Prompt) error {
	return r.patchTaskStatus(ctx, holder, func(s *aiv1.AgentTaskStatus) error {
		s.Prompts = mutate(s.Prompts)
		return nil
	})
}

// patchTaskStatus patches the status of t under an optimistic lock, tried
// again on a fresh copy on a conflict. t is the server's copy afterwards, or
// keeps its status when mutate or the patch fails.
func (r *AgentProjectReconciler) patchTaskStatus(ctx context.Context, t *aiv1.AgentTask, mutate func(*aiv1.AgentTaskStatus) error) error {
	first := true
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if !first {
			var fresh aiv1.AgentTask
			if err := r.Get(ctx, client.ObjectKeyFromObject(t), &fresh); err != nil {
				return err
			}
			*t = fresh
		}
		first = false
		orig := t.DeepCopy()
		if err := mutate(&t.Status); err != nil {
			t.Status = orig.Status
			return err
		}
		if err := r.Status().Patch(ctx, t, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})); err != nil {
			t.Status = orig.Status
			return err
		}
		return nil
	})
}

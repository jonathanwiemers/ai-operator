package runner

import (
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// maxPromptsDelivered is how many delivered prompt ids the runner remembers.
const maxPromptsDelivered = 50

// promptAnswers are the owners' answers to the implementer's questions due
// by the task's run request: those not delivered yet, or, with all, every
// one (for a fresh session, which has seen none of them).
func promptAnswers(task *aiv1.AgentTask, st state, all bool) []aiv1.PromptAnswerDelivery {
	var out []aiv1.PromptAnswerDelivery
	for _, a := range task.Spec.Source.PromptAnswers {
		if a.Request > task.Spec.RunRequest || (!all && slices.Contains(st.PromptsDelivered, a.Prompt)) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// buildPromptAnswers is one user turn with the answers: the owners' own
// words, like an owner's instruction, not data.
func buildPromptAnswers(answers []aiv1.PromptAnswerDelivery) string {
	var b strings.Builder
	b.WriteString("## Answers to your questions\n\nThe owners answered; continue the work with their answers.")
	for _, a := range answers {
		b.WriteString("\n\nAnswer to your question " + a.Prompt)
		if q := strings.TrimSpace(a.Question); q != "" {
			fmt.Fprintf(&b, " (\"%s\")", q)
		}
		by := a.By
		if by == "" {
			by = "an owner"
		} else {
			by = "@" + by
		}
		b.WriteString(" from " + by + ":")
		if l := strings.TrimSpace(a.OptionLabel); l != "" {
			b.WriteString(" " + l)
		}
		if t := strings.TrimSpace(a.Text); t != "" {
			b.WriteString("\n\n" + t)
		}
	}
	return b.String()
}

// deliveredPrompts records answers as delivered.
func (s *state) deliveredPrompts(answers []aiv1.PromptAnswerDelivery) {
	for _, a := range answers {
		if !slices.Contains(s.PromptsDelivered, a.Prompt) {
			s.PromptsDelivered = append(s.PromptsDelivered, a.Prompt)
		}
	}
	if n := len(s.PromptsDelivered); n > maxPromptsDelivered {
		s.PromptsDelivered = s.PromptsDelivered[n-maxPromptsDelivered:]
	}
}

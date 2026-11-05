package runner

import (
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// maxQuestionOptions bounds a question's options.
const maxQuestionOptions = 4

// questionDrafts normalises the implementer's questions: the structured ones
// first, then each prose question as free text only. A question keeps at
// most maxQuestionOptions options without an empty label, exactly one of
// them recommended (the first marked default, else the first); one with
// fewer than 2 becomes free text only. Texts are cut to the prompt bounds.
// All are returned; the status keeps the first aiv1.MaxQuestionDrafts.
func questionDrafts(a ImplementAnswer) []aiv1.QuestionDraft {
	var out []aiv1.QuestionDraft
	for _, p := range a.Prompts {
		q := strings.TrimSpace(p.Question)
		if q == "" {
			continue
		}
		d := aiv1.QuestionDraft{Question: textutil.Truncate(q, aiv1.MaxPromptQuestion-len("…")),
			AllowFreeText: p.AllowFreeText == nil || *p.AllowFreeText}
		def := -1
		for _, o := range p.Options {
			label := strings.Join(strings.Fields(o.Label), " ")
			if label == "" || len(d.Options) == maxQuestionOptions {
				continue
			}
			if def < 0 && o.Default {
				def = len(d.Options)
			}
			d.Options = append(d.Options, aiv1.PromptOption{Label: textutil.Truncate(label, aiv1.MaxPromptOptionLabel-len("…")),
				Detail: textutil.Truncate(strings.TrimSpace(o.Detail), aiv1.MaxPromptOptionDetail-len("…"))})
		}
		if len(d.Options) < 2 {
			d.Options, d.AllowFreeText = nil, true
		}
		for i := range d.Options {
			d.Options[i].ID = string(rune('A' + i))
			d.Options[i].Recommended = i == max(def, 0)
		}
		out = append(out, d)
	}
	for _, q := range a.Questions {
		if q = strings.TrimSpace(q); q != "" {
			out = append(out, aiv1.QuestionDraft{Question: textutil.Truncate(q, aiv1.MaxPromptQuestion-len("…")), AllowFreeText: true})
		}
	}
	return out
}

// renderQuestions lists the questions with their options, numbered.
func renderQuestions(drafts []aiv1.QuestionDraft) string {
	var b strings.Builder
	for i, d := range drafts {
		fmt.Fprintf(&b, "%d. %s\n", i+1, indent(d.Question, "   "))
		for _, o := range d.Options {
			fmt.Fprintf(&b, "   - **%s** %s", o.ID, o.Label)
			if o.Recommended {
				b.WriteString(" _(default)_")
			}
			if o.Detail != "" {
				b.WriteString(": " + strings.Join(strings.Fields(o.Detail), " "))
			}
			b.WriteString("\n")
		}
		if len(d.Options) > 0 && d.AllowFreeText {
			b.WriteString("   - or answer in your own words\n")
		}
	}
	return b.String()
}

// indent indents the lines after the first, so a multi-line question stays
// in its list item.
func indent(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}

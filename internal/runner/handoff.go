package runner

import (
	"fmt"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// The handoff note is one comment of the bot on the issue, edited after every
// run. It holds the implementer's own notes on the state of the work, so a
// run that cannot resume the agent's session (the workspace was archived, or
// the session is gone) does not start from zero. Humans can read it in the
// issue and correct the agent in a comment.
const (
	handoffBegin = "<!-- ai-operator:handoff:begin -->"
	handoffEnd   = "<!-- ai-operator:handoff:end -->"
	// maxHandoff bounds the notes kept in the issue and sent to the model.
	maxHandoff = 8000
)

// splitHandoff takes the bot's handoff note out of the issue notes. Only
// notes written by the bot count: anyone can post a comment that contains
// the markers.
func splitHandoff(notes []gitlab.Note, botID int64) (*gitlab.Note, []gitlab.Note) {
	var handoff *gitlab.Note
	rest := make([]gitlab.Note, 0, len(notes))
	for i, n := range notes {
		if botID != 0 && n.Author.ID == botID && strings.Contains(n.Body, handoffBegin) {
			handoff = &notes[i]
			continue
		}
		rest = append(rest, n)
	}
	return handoff, rest
}

// handoffText returns the notes stored in a handoff note.
func handoffText(n *gitlab.Note) string {
	if n == nil {
		return ""
	}
	_, after, ok := strings.Cut(n.Body, handoffBegin)
	if !ok {
		return ""
	}
	text, _, _ := strings.Cut(after, handoffEnd)
	return strings.TrimSpace(text)
}

// renderHandoff renders the handoff note for the notes of run runID.
func renderHandoff(text, runID string) string {
	return fmt.Sprintf("🧠 **Agent notes**: what the agent knows about this issue, updated after every run. "+
		"A run that cannot resume the agent's session starts from these notes. Comment if something here is wrong.\n\n"+
		"<details><summary>Notes after run <code>%s</code></summary>\n\n%s\n%s\n%s\n\n</details>\n",
		runID, handoffBegin, truncate(strings.TrimSpace(text), maxHandoff), handoffEnd)
}

package runner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// The handoff note is one comment of the bot on the issue, edited after every
// run that returns notes. It holds the implementer's own notes on the state of
// the work, so a run that cannot resume the agent's session (the workspace was
// archived, or the session is gone) does not start from zero. Humans can read
// it in the issue and correct the agent in a comment.
const (
	handoffHeader = "🧠 **Agent notes**"
	handoffBegin  = "<!-- ai-operator:handoff:begin -->"
	handoffEnd    = "<!-- ai-operator:handoff:end -->"
	// maxHandoff bounds the notes kept in the issue and sent to the model.
	maxHandoff = 8000
)

// handoffAfter records the newest comment the run that wrote the notes had
// seen, so a later run can tell which comments are newer than the notes.
var handoffAfter = regexp.MustCompile(`<!-- ai-operator:handoff:after-note=(\d+) -->`)

// handoff is the content of a handoff note.
type handoff struct {
	Text string
	// AfterNote is the newest note ID the writing run had seen.
	AfterNote int64
}

// splitHandoff takes the bot's handoff note out of the issue notes. Only a
// note written by the bot that starts with the header counts: anyone can post
// a comment with the markers, and the bot's own reports may quote them. If
// there are several, the oldest wins, as later runs edit that one.
func splitHandoff(notes []gitlab.Note, botID int64) (*gitlab.Note, []gitlab.Note) {
	var note *gitlab.Note
	rest := make([]gitlab.Note, 0, len(notes))
	for i, n := range notes {
		if note == nil && botID != 0 && n.Author.ID == botID && !n.System &&
			strings.HasPrefix(n.Body, handoffHeader) && strings.Contains(n.Body, handoffBegin) {
			note = &notes[i]
			continue
		}
		rest = append(rest, n)
	}
	return note, rest
}

// parseHandoff returns the notes stored in a handoff note.
func parseHandoff(n *gitlab.Note) handoff {
	if n == nil {
		return handoff{}
	}
	_, after, ok := strings.Cut(n.Body, handoffBegin)
	if !ok {
		return handoff{}
	}
	if i := strings.LastIndex(after, handoffEnd); i >= 0 {
		after = after[:i]
	}
	h := handoff{Text: strings.TrimSpace(after)}
	if m := handoffAfter.FindStringSubmatch(n.Body); m != nil {
		h.AfterNote, _ = strconv.ParseInt(m[1], 10, 64)
	}
	return h
}

// renderHandoff renders the handoff note for the notes of run runID, which had
// seen the comments up to afterNote.
func renderHandoff(text, runID string, afterNote int64) string {
	text = truncate(strings.TrimSpace(text), maxHandoff)
	// The agent's text must not be able to end the notes early or start
	// another set of them.
	text = strings.ReplaceAll(text, "<!-- ai-operator:", "<!-- ai-operator :")
	if strings.Count(text, "```")%2 == 1 {
		text += "\n```" // truncation cut a code block
	}
	return fmt.Sprintf("%s: what the agent knows about this issue, updated after its runs. "+
		"A run that cannot resume the agent's session starts from these notes. Comment if something here is wrong.\n\n"+
		"<details><summary>Notes after run <code>%s</code></summary>\n\n%s\n%s\n%s\n\n</details>\n"+
		"<!-- ai-operator:handoff:after-note=%d -->\n",
		handoffHeader, runID, handoffBegin, text, handoffEnd, afterNote)
}

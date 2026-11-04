package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const letterHint = "Owners can also reply here with the option's letter."

// postedPrompt adds p to the milestone's prompts, posts its comment and
// returns the comment.
func (e *projectEnv) postedPrompt(p aiv1.Prompt) gitlab.Note {
	e.t.Helper()
	st := e.task(specTaskName)
	q := e.addPrompt(st, st, p)
	e.reconcile()
	iid := q.Source.IssueIID
	if iid == 0 {
		iid = e.autopilot().TrackingIssueIID
	}
	notes := promptNotes(e, iid)
	if len(notes) == 0 {
		e.t.Fatalf("prompt %s is not posted on #%d", q.ID, iid)
	}
	return notes[len(notes)-1]
}

// botThreadReplies are the bot's replies in the thread of root.
func botThreadReplies(e *projectEnv, iid, root int64) []string {
	var out []string
	for _, n := range e.gl.IssueNotes[iid] {
		if n.Author.ID == e.gl.Bot.ID && e.gl.Replies[n.ID] == root && !strings.Contains(n.Body, "<!-- ai-operator:prompt-answer=") {
			out = append(out, n.Body)
		}
	}
	return out
}

func TestPromptPinsTheOwnersIDs(t *testing.T) {
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	if p := e.addPrompt(st, st, stuckPrompt()); !slices.Equal(p.OwnerIDs, []int64{alice.ID}) {
		t.Fatalf("the owners' IDs are pinned: %v", p.OwnerIDs)
	}
	e.gl.Users = nil // the name of no user any more
	if p := e.addPrompt(st, st, mainPrompt("x")); !slices.Equal(p.OwnerIDs, []int64{0}) {
		t.Fatalf("no owner: nobody answers it: %v", p.OwnerIDs)
	}
}

func TestPromptOwnerAnswersWithALetterInTheThread(t *testing.T) {
	e := newAnswerEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	root := e.postedPrompt(stuckPrompt())
	if !strings.Contains(root.Body, "\n\n"+letterHint) {
		t.Fatalf("the comment says owners can reply with a letter:\n%s", root.Body)
	}
	before := e.toolEffect()
	e.gl.AddReply(tracking, root.ID, alice, "A — the runner was flaky")
	e.reconcile()
	e.reconcile()

	p := e.prompt(specTaskName, "Q1")
	if a := p.Answer; p.State != aiv1.PromptAnswered || a == nil || a.OptionID != "A" || a.By != "alice" || a.Via != "gitlab" || a.Text != "" {
		t.Fatalf("the reply answers Q1 via gitlab, its text ignored: %+v %+v", p, a)
	}
	if after := e.toolEffect(); after.runs != before.runs+1 {
		t.Fatalf("the option's retry ran: %+v → %+v", before, after)
	}
	edited := e.gl.IssueNotes[tracking][slices.IndexFunc(e.gl.IssueNotes[tracking], func(n gitlab.Note) bool { return n.ID == root.ID })]
	if !strings.HasPrefix(edited.Body, "✅ Answered by `@alice` in GitLab: Retry #1") || strings.Contains(edited.Body, letterHint) {
		t.Fatalf("the mirror edits the comment:\n%s", edited.Body)
	}
	if r := botThreadReplies(e, tracking, root.ID); len(r) != 0 {
		t.Fatalf("an answer gets no reply but the mirror's: %q", r)
	}

	// A second answer is refused, naming the first.
	e.gl.AddReply(tracking, root.ID, alice, "b")
	e.reconcile()
	if r := botThreadReplies(e, tracking, root.ID); len(r) != 1 || r[0] != "🧭 Q1 was answered already by @alice: Retry #1." {
		t.Fatalf("a second answer is refused in the thread: %q", r)
	}
	if p := e.prompt(specTaskName, "Q1"); p.Answer.OptionID != "A" {
		t.Fatalf("the first answer stays: %+v", p.Answer)
	}
}

func TestPromptLetterFromSomeoneElseChangesNothing(t *testing.T) {
	e := newAnswerEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	root := e.postedPrompt(stuckPrompt())
	before := e.toolEffect()
	e.gl.AddReply(tracking, root.ID, mallory, "A")
	e.gl.AddReply(tracking, root.ID, gitlab.User{ID: 9, Username: "alice"}, "A") // an owner's name, another user
	e.reconcile()
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen || p.Answer != nil {
		t.Fatalf("no owner, no answer: %+v", p)
	}
	if after := e.toolEffect(); after != before {
		t.Fatalf("nothing runs: %+v → %+v", before, after)
	}
	want := "🧭 Only the milestone's owners can answer Q1; this reply changes nothing."
	if r := botThreadReplies(e, tracking, root.ID); len(r) != 2 || r[0] != want || r[1] != want {
		t.Fatalf("one reply each, in the thread: %q", r)
	}
}

func TestPromptLetterCountsOnlyInThePromptsThread(t *testing.T) {
	e := newAnswerEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	root := e.postedPrompt(stuckPrompt())
	other := e.gl.AddNote(tracking, alice, "Which runner was it?")
	e.reconcile()
	e.gl.AddReply(tracking, other.ID, alice, "A")          // another thread
	e.gl.AddNote(tracking, alice, "A")                     // no thread
	e.gl.AddReply(tracking, root.ID, alice, "A lot to do") // discussion, not an answer
	e.gl.AddReply(tracking, root.ID, alice, "> A\n\nmallory said that")
	e.reconcile()
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen {
		t.Fatalf("no letter in the prompt's thread, no answer: %+v", p)
	}
	if r := botThreadReplies(e, tracking, root.ID); len(r) != 0 {
		t.Fatalf("and no reply: %q", r)
	}
}

func TestPromptLetterOnTheIssueOfItsSource(t *testing.T) {
	e := newAnswerEnv(t)
	root := e.postedPrompt(issuePrompt(aiv1.PromptAssessment, 1))
	if !strings.Contains(root.Body, letterHint) {
		t.Fatalf("the hint is on the issue's comment too:\n%s", root.Body)
	}
	e.gl.AddReply(1, root.ID, alice, "B.")
	e.reconcile()
	p := e.prompt(specTaskName, "Q1")
	if a := p.Answer; p.State != aiv1.PromptAnswered || a == nil || a.OptionID != "B" || a.Via != "gitlab" {
		t.Fatalf("the reply on the issue answers it: %+v %+v", p, a)
	}
}

func TestPromptThreadsAreReadOnlyForALetter(t *testing.T) {
	e := newAnswerEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	root := e.postedPrompt(stuckPrompt())
	e.reconcile()
	lists := e.gl.DiscussionLists
	e.gl.AddReply(tracking, root.ID, alice, "Looks good to me")
	e.gl.AddNote(tracking, alice, "Thanks")
	e.reconcile()
	if e.gl.DiscussionLists != lists {
		t.Fatalf("no letter, no threads read: %d lists", e.gl.DiscussionLists-lists)
	}
}

func TestPromptLettersAreNotReadWithDeskInputOnly(t *testing.T) {
	e := newAnswerEnv(t)
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Workflow.Inputs = []aiv1.InputSource{aiv1.InputDesk} })
	tracking := e.autopilot().TrackingIssueIID
	root := e.postedPrompt(stuckPrompt())
	if strings.Contains(root.Body, letterHint) {
		t.Fatalf("no hint without gitlab input:\n%s", root.Body)
	}
	e.reconcile()
	lists := e.gl.DiscussionLists
	e.gl.AddReply(tracking, root.ID, alice, "A")
	e.reconcile()
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen || e.gl.DiscussionLists != lists {
		t.Fatalf("with inputs [desk] no thread is read: %+v, %d lists", p, e.gl.DiscussionLists-lists)
	}
}

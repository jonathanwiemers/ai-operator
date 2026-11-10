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

// newMRAnswerEnv is newAnswerEnv with merge request !5 of #1's task.
func newMRAnswerEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newAnswerEnv(t)
	e.gl.MRs[5] = &gitlab.MergeRequest{IID: 5, State: "opened", SHA: "sha1", SourceBranch: "agent/1"}
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) { s.Run.MergeRequestIID = 5 })
	e.reconcile()
	return e
}

// mrPrompt is a prompt about #1 whose comment goes on its merge request !5.
func mrPrompt() aiv1.Prompt {
	p := issuePrompt(aiv1.PromptAssessment, 1)
	p.Source.MRIID = 5
	return p
}

// postedMRPrompt adds p to the milestone's prompts and returns its comment
// on merge request !5.
func (e *projectEnv) postedMRPrompt(p aiv1.Prompt) gitlab.Note {
	e.t.Helper()
	st := e.task(specTaskName)
	q := e.addPrompt(st, st, p)
	e.reconcile()
	for _, n := range e.gl.MRNotes[5] {
		if n.Author.ID == e.gl.Bot.ID && strings.Contains(n.Body, promptMarker(q)) {
			return n
		}
	}
	e.t.Fatalf("prompt %s is not posted on !5", q.ID)
	return gitlab.Note{}
}

// botMRReplies are the bot's notes on merge request !5 in the thread of
// root (0: outside any thread), answer replies included if answers.
func botMRReplies(e *projectEnv, root int64, answers bool) []string {
	var out []string
	for _, n := range e.gl.MRNotes[5] {
		if n.Author.ID == e.gl.Bot.ID && e.gl.Replies[n.ID] == root && (answers || !strings.Contains(n.Body, "<!-- ai-operator:prompt-answer=")) {
			out = append(out, n.Body)
		}
	}
	return out
}

func TestPromptOwnerAnswersWithALetterInTheMRThread(t *testing.T) {
	e := newMRAnswerEnv(t)
	root := e.postedMRPrompt(mrPrompt())
	if !strings.Contains(root.Body, "\n\n"+letterHint) {
		t.Fatalf("the merge request's comment says owners can reply with a letter:\n%s", root.Body)
	}
	e.gl.AddMRReply(5, root.ID, alice, "B: leave it")
	e.reconcile()
	e.reconcile()

	p := e.prompt(specTaskName, "Q1")
	if a := p.Answer; p.State != aiv1.PromptAnswered || a == nil || a.OptionID != "B" || a.By != "alice" || a.Via != "gitlab" || a.Text != "" {
		t.Fatalf("the reply on the merge request answers Q1 via gitlab: %+v %+v", p, a)
	}
	edited := e.gl.MRNotes[5][slices.IndexFunc(e.gl.MRNotes[5], func(n gitlab.Note) bool { return n.ID == root.ID })]
	if !strings.HasPrefix(edited.Body, "✅ Answered by `@alice` in GitLab: Do nothing") || strings.Contains(edited.Body, letterHint) {
		t.Fatalf("the mirror edits the comment:\n%s", edited.Body)
	}
	if r := botMRReplies(e, root.ID, true); len(r) != 1 || !strings.Contains(r[0], answerMarker(p)) {
		t.Fatalf("the mirror's answer reply is in the prompt's thread, and nothing else: %q", r)
	}
	if r := botMRReplies(e, 0, true); len(r) != 1 {
		t.Fatalf("no comment outside the thread but the prompt's: %q", r)
	}

	// A second answer is refused in the thread, naming the first.
	e.gl.AddMRReply(5, root.ID, alice, "a")
	e.reconcile()
	if r := botMRReplies(e, root.ID, false); len(r) != 1 || r[0] != "🧭 Q1 was answered already by @alice: Do nothing." {
		t.Fatalf("a second answer is refused in the thread: %q", r)
	}
	if p := e.prompt(specTaskName, "Q1"); p.Answer.OptionID != "B" {
		t.Fatalf("the first answer stays: %+v", p.Answer)
	}
}

func TestPromptLetterFromSomeoneElseInTheMRThread(t *testing.T) {
	e := newMRAnswerEnv(t)
	root := e.postedMRPrompt(mrPrompt())
	e.gl.AddMRReply(5, root.ID, mallory, "A")
	e.gl.AddMRReply(5, root.ID, alice, "A lot of work, this") // discussion, not an answer
	e.reconcile()
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen || p.Answer != nil {
		t.Fatalf("no owner, no answer: %+v", p)
	}
	if r := botMRReplies(e, root.ID, false); len(r) != 1 || r[0] != "🧭 Only the milestone's owners can answer Q1; this reply changes nothing." {
		t.Fatalf("one refusal, in the thread: %q", r)
	}
}

func TestPromptLetterOutsideTheMRPromptsThreadChangesNothing(t *testing.T) {
	e := newMRAnswerEnv(t)
	root := e.postedMRPrompt(mrPrompt())
	review := e.gl.AddMRNote(5, alice, "Please rename this function.") // a review thread, or a diff note's
	own := e.gl.AddMRNote(5, e.gl.Bot, "🤖 Review: two findings.")      // the bot's, no prompt
	old := e.gl.AddMRNote(5, e.gl.Bot, "🧭 **Q1:** Older\n\n<!-- ai-operator:prompt=Q1 posted=12345 -->")
	e.reconcile()
	before := len(e.gl.MRNotes[5])
	lists := e.gl.DiscussionLists
	e.gl.AddMRReply(5, review.ID, alice, "A")
	e.gl.AddMRReply(5, review.ID, mallory, "B")
	e.gl.AddMRReply(5, own.ID, alice, "A")
	e.gl.AddMRReply(5, old.ID, alice, "A") // a prompt Q1 of an older task
	e.gl.AddMRNote(5, alice, "B")          // no thread
	e.reconcile()
	e.reconcile()
	if e.gl.DiscussionLists == lists {
		t.Fatal("the letters have the threads read")
	}
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen || p.Answer != nil {
		t.Fatalf("no letter in the prompt's thread, no answer: %+v", p)
	}
	if got := len(e.gl.MRNotes[5]); got != before+5 {
		t.Fatalf("and no reply anywhere: %q", botMRReplies(e, review.ID, true))
	}
	if r := botMRReplies(e, root.ID, true); len(r) != 0 {
		t.Fatalf("nothing in the prompt's thread either: %q", r)
	}
}

func TestPromptMRLettersAreNotReadWithDeskInputOnly(t *testing.T) {
	e := newMRAnswerEnv(t)
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Workflow.Inputs = []aiv1.InputSource{aiv1.InputDesk} })
	root := e.postedMRPrompt(mrPrompt())
	if strings.Contains(root.Body, letterHint) {
		t.Fatalf("no hint without gitlab input:\n%s", root.Body)
	}
	e.reconcile()
	lists := e.gl.DiscussionLists
	e.gl.AddMRReply(5, root.ID, alice, "A")
	e.reconcile()
	e.reconcile()
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen || e.gl.DiscussionLists != lists {
		t.Fatalf("with inputs [desk] no thread is read: %+v, %d lists", p, e.gl.DiscussionLists-lists)
	}
	if r := botMRReplies(e, root.ID, true); len(r) != 0 {
		t.Fatalf("and nothing is replied: %q", r)
	}
}

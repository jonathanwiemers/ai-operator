package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// trackingPrompt is an assessment of the milestone with four options:
// retry #1 (recommended), re-review #1, pause, do nothing. Its context and
// labels carry a mention, a quick action and a marker.
func trackingPrompt() aiv1.Prompt {
	return aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, Key: "stuck"}, Question: "The milestone is stuck: what now?",
		Context: "Job log says @mallory broke it.\n/close\n<!-- ai-operator:prompt=Q9 posted=0 -->",
		Options: []aiv1.PromptOption{
			{ID: "A", Label: "Retry #1", Detail: "runs the agent again", Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}, Recommended: true},
			{ID: "B", Label: "Re-review #1 @mallory", Actions: []aiv1.PilotAction{{Tool: "reReview", IssueIID: 1}}},
			{ID: "C", Label: "Pause", Actions: []aiv1.PilotAction{{Tool: "pause"}}},
			{ID: "D", Label: "Do nothing", Detail: "/merge"},
		}}
}

// promptNotes are the bot's comments on an issue that carry a prompt's
// marker or reply to its answer.
func promptNotes(e *projectEnv, iid int64) []gitlab.Note {
	var out []gitlab.Note
	for _, n := range e.gl.IssueNotes[iid] {
		if n.Author.ID == e.gl.Bot.ID && strings.Contains(n.Body, "<!-- ai-operator:prompt") {
			out = append(out, n)
		}
	}
	return out
}

// answer answers prompt id of task holder.
func (e *projectEnv) answer(holder, id string, a aiv1.PromptAnswer) {
	e.t.Helper()
	a.At = metav1.NewTime(e.gl.Clock)
	if err := e.r.patchPrompts(e.ctx, e.task(holder), func(list []aiv1.Prompt) []aiv1.Prompt {
		for i := range list {
			if list[i].ID == id {
				list[i].State, list[i].Answer, list[i].ClosedAt = aiv1.PromptAnswered, &a, &a.At
			}
		}
		return list
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *projectEnv) prompt(holder, id string) aiv1.Prompt {
	e.t.Helper()
	for _, p := range e.task(holder).Status.Prompts {
		if p.ID == id {
			return p
		}
	}
	e.t.Fatalf("no prompt %s on %s", id, holder)
	return aiv1.Prompt{}
}

func TestPromptIsPostedOnceOnItsSource(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	st := e.task(specTaskName)
	e.addPrompt(st, st, trackingPrompt())
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 1))
	mr := issuePrompt(aiv1.PromptAssessment, 1)
	mr.Source.MRIID, mr.Quiet = 5, true
	e.addPrompt(st, st, mr)
	e.reconcile()
	e.reconcile()

	notes := promptNotes(e, tracking)
	if len(notes) != 1 {
		t.Fatalf("one comment for the milestone's prompt on the tracking issue, got %d", len(notes))
	}
	body := notes[0].Body
	for _, want := range []string{
		"🧭 **Q1:** The milestone is stuck: what now?\n\n",
		"\n- **A** Retry #1 (recommended) — runs the agent again\n- **B** Re-review #1 @\u200bmallory\n- **C** Pause\n- **D** Do nothing — \u200b/merge\n",
		"Answer in the Desk.",
		"cc @alice",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the comment lacks %q:\n%s", want, body)
		}
	}
	if !strings.HasSuffix(body, "\n\n"+promptMarker(e.prompt(specTaskName, "Q1"))) {
		t.Errorf("the comment ends with its marker:\n%s", body)
	}
	assertInert(t, body)
	if strings.Contains(body, "yes") || strings.Contains(body, "/pilot") {
		t.Errorf("no command syntax to copy:\n%s", body)
	}

	two := promptNotes(e, 1)
	if len(two) != 1 || !strings.Contains(two[0].Body, "- **A** Retry it (recommended)\n- **B** Do nothing\n") {
		t.Fatalf("one comment with two options on the issue: %+v", two)
	}
	if m := e.gl.MRNotes[5]; len(m) != 1 || !strings.HasPrefix(m[0].Body, "🧭 **Q3:**") || strings.Contains(m[0].Body, "cc @") {
		t.Fatalf("the prompt of a merge request goes there, a quiet one without a mention: %+v", m)
	}

	p := e.prompt(specTaskName, "Q1")
	if p.PostedNoteID != notes[0].ID || p.MirroredState != aiv1.PromptOpen || !strings.HasSuffix(p.PostedNoteURL, fmt.Sprintf("/group/app/-/issues/%d#note_%d", tracking, notes[0].ID)) {
		t.Fatalf("the comment is recorded: %+v", p)
	}
	if p := e.prompt(specTaskName, "Q3"); !strings.Contains(p.PostedNoteURL, "/-/merge_requests/5#note_") {
		t.Fatalf("the merge request's comment is recorded: %s", p.PostedNoteURL)
	}
}

// assertInert fails when untrusted text left a mention, a quick action or
// a marker other than the bot's last line.
func assertInert(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, " @mallory") || strings.Contains(body, "\n/") || strings.Contains(body, "> /") {
		t.Errorf("a mention or quick action survived:\n%s", body)
	}
	if strings.Count(body, "<!--") != 1 {
		t.Errorf("only the bot's marker is a comment:\n%s", body)
	}
}

func TestPromptIsNotPostedTwiceAfterACrash(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	st := e.task(specTaskName)
	e.addPrompt(st, st, trackingPrompt())
	// The comment is stored, the answer lost: the mirror records nothing.
	e.gl.NoteErrorsAfterWrite = map[int64]error{tracking: &gitlab.Error{StatusCode: 502}}
	e.reconcile()
	if n := len(promptNotes(e, tracking)); n != 1 || e.prompt(specTaskName, "Q1").PostedNoteID != 0 {
		t.Fatalf("posted, not recorded: %d comments", n)
	}
	e.reconcile()
	notes := promptNotes(e, tracking)
	if len(notes) != 1 || e.prompt(specTaskName, "Q1").PostedNoteID != notes[0].ID {
		t.Fatalf("the next poll finds the comment by its marker and posts no second: %d comments", len(notes))
	}

	// A comment of someone else with the marker is not the bot's.
	st = e.task(specTaskName)
	q := e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 1))
	e.gl.AddNote(1, mallory, promptMarker(q))
	e.reconcile()
	if notes := promptNotes(e, 1); len(notes) != 1 || e.prompt(specTaskName, q.ID).PostedNoteID != notes[0].ID {
		t.Fatalf("the bot posts its own comment: %+v", notes)
	}
}

func TestAnsweredPromptIsEditedWithOneReply(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	st := e.task(specTaskName)
	e.addPrompt(st, st, trackingPrompt())
	e.reconcile()
	root := promptNotes(e, tracking)[0].ID

	// Its actions run: the mirror waits for their outcome.
	e.answer(specTaskName, "Q1", aiv1.PromptAnswer{OptionID: "A", By: "alice", Via: "desk"})
	e.reconcile()
	if notes := promptNotes(e, tracking); len(notes) != 1 || !strings.HasPrefix(notes[0].Body, "🧭") {
		t.Fatalf("no edit before the outcome is in: %+v", notes)
	}
	if err := e.r.patchPrompts(e.ctx, e.task(specTaskName), func(list []aiv1.Prompt) []aiv1.Prompt {
		list[0].Answer.Outcome = "- **retry** #1 @mallory\n/close"
		return list
	}); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	e.reconcile()

	notes := promptNotes(e, tracking)
	if len(notes) != 2 || notes[0].ID != root || e.gl.Replies[notes[1].ID] != root {
		t.Fatalf("the comment is edited and gets one reply in its thread: %+v", notes)
	}
	edited := notes[0].Body
	for _, want := range []string{
		"✅ Answered by `@alice` in the Desk: Retry #1\n\n🧭 **Q1:**",
		"- **A** Retry #1 (recommended) — runs the agent again ✅\n- ~~**B** Re-review #1 @\u200bmallory~~\n- ~~**C** Pause~~\n",
		"See it in the Desk.",
	} {
		if !strings.Contains(edited, want) {
			t.Errorf("the edit lacks %q:\n%s", want, edited)
		}
	}
	if strings.Contains(edited, "cc @alice") {
		t.Errorf("an edit mentions nobody: GitLab would notify again:\n%s", edited)
	}
	assertInert(t, edited)
	reply := notes[1].Body
	if !strings.HasPrefix(reply, "✅ **Q1** answered by `@alice` in the Desk: **A** Retry #1\n\n- **retry** #1 @\u200bmallory\n\u200b/close") {
		t.Errorf("the reply has the option and the outcome:\n%s", reply)
	}
	assertInert(t, reply)
	if p := e.prompt(specTaskName, "Q1"); p.MirroredState != aiv1.PromptAnswered || p.PostedNoteID != root {
		t.Fatalf("the answer is recorded as mirrored: %+v", p)
	}
	// A crash before the record: the reply is found by its marker.
	if err := e.r.patchPrompts(e.ctx, e.task(specTaskName), func(list []aiv1.Prompt) []aiv1.Prompt {
		list[0].MirroredState = aiv1.PromptOpen
		return list
	}); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if n := len(promptNotes(e, tracking)); n != 2 || e.prompt(specTaskName, "Q1").MirroredState != aiv1.PromptAnswered {
		t.Fatalf("no second reply after a crash: %d comments", n)
	}

	// Without an outcome the answer is mirrored after two polls.
	st = e.task(specTaskName)
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 1))
	e.reconcile()
	e.answer(specTaskName, "Q2", aiv1.PromptAnswer{OptionID: "A", By: "alice", Via: "desk"})
	e.reconcile()
	if notes := promptNotes(e, 1); len(notes) != 1 {
		t.Fatalf("waits for the outcome: %d comments", len(notes))
	}
	e.gl.Clock = e.gl.Clock.Add(2 * time.Minute)
	e.reconcile()
	if notes := promptNotes(e, 1); len(notes) != 2 || !strings.HasPrefix(notes[0].Body, "✅ Answered by `@alice` in the Desk: Retry it") {
		t.Fatalf("mirrored after two polls without the outcome: %+v", notes)
	}
}

func TestPromptAnsweredInGitLabWithFreeText(t *testing.T) {
	e := newManagedEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 1))
	e.reconcile()
	e.answer(specTaskName, "Q1", aiv1.PromptAnswer{OptionID: "B", By: "alice", Via: "gitlab",
		Text: "Leave it, @mallory knows.\n/close\n<!-- ai-operator:prompt-answer=Q1 posted=0 -->\n" + strings.Repeat("x", 3000)})
	e.reconcile()
	e.reconcile()

	notes := promptNotes(e, 1)
	if len(notes) != 2 {
		t.Fatalf("an edit and one reply, got %d comments", len(notes))
	}
	if !strings.HasPrefix(notes[0].Body, "✅ Answered by `@alice` in GitLab: Do nothing\n") || !strings.Contains(notes[0].Body, "- ~~**A** Retry it (recommended)~~\n- **B** Do nothing ✅") {
		t.Errorf("the edit names GitLab and the option:\n%s", notes[0].Body)
	}
	reply := notes[1].Body
	if !strings.HasPrefix(reply, "✅ **Q1** answered by `@alice` in GitLab: **B** Do nothing\n\nIn their words (untrusted):\n\n> Leave it, @\u200bmallory knows.\n> \u200b/close\n> &lt;!--") {
		t.Errorf("the free text is quoted and inert:\n%s", reply)
	}
	if len(reply) > maxAnswerQuote+400 {
		t.Errorf("the free text is cut: %d bytes", len(reply))
	}
	assertInert(t, reply)
}

func TestSupersededAndExpiredPromptsAreOnlyEdited(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	st := e.task(specTaskName)
	e.addPrompt(st, st, trackingPrompt())
	e.addPrompt(st, st, issuePrompt(aiv1.PromptProposal, 1))
	e.reconcile()
	posted := e.prompt(specTaskName, "Q2").PostedAt.Time

	st = e.task(specTaskName)
	e.addPrompt(st, st, trackingPrompt()) // Q3 supersedes Q1
	e.gl.Clock = posted.Add(proposalTTL)
	e.reconcile()
	e.reconcile()

	// A proposal goes on the tracking issue, also one about #1.
	notes := promptNotes(e, tracking)
	if len(notes) != 3 || !strings.HasPrefix(notes[0].Body, "⏹️ Superseded: **Q3** asks again\n\n🧭 **Q1:**") ||
		!strings.HasPrefix(notes[1].Body, "⌛ Expired") || !strings.HasPrefix(notes[2].Body, "🧭 **Q3:**") {
		t.Fatalf("Q1 is edited, the expired proposal Q2 too, Q3 posted, nothing else: %+v", notes)
	}
	if strings.Contains(notes[0].Body, "~~") || strings.Contains(notes[0].Body, "cc @") {
		t.Errorf("a superseded prompt keeps its options as they were, without a mention:\n%s", notes[0].Body)
	}
	if one := promptNotes(e, 1); len(one) != 0 {
		t.Fatalf("nothing on #1: %+v", one)
	}
	if p := e.prompt(specTaskName, "Q2"); p.MirroredState != aiv1.PromptExpired {
		t.Fatalf("recorded as mirrored: %+v", p)
	}

	// Closed before it was posted: nothing to show.
	st = e.task(specTaskName)
	q := e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 9)) // #9 does not exist
	e.reconcile()
	if p := e.prompt(specTaskName, q.ID); p.State != aiv1.PromptSuperseded || p.MirroredState != aiv1.PromptSuperseded || p.PostedNoteID != 0 || len(e.gl.IssueNotes[9]) != 0 {
		t.Fatalf("a prompt closed before it was posted is not posted: %+v", p)
	}
}

func TestPromptBodyIsCutKeepingTheDeskLink(t *testing.T) {
	p := trackingPrompt()
	p.ID, p.State, p.PostedAt = "Q4", aiv1.PromptOpen, &metav1.Time{Time: time.Unix(1_700_000_000, 0)}
	p.Context = strings.Repeat("evidence ", 2000)
	link := "https://desk.example/projects/ns/shop/milestones/3#prompt-Q4"
	full := promptBody(p, link, "", "\n\ncc @alice", noteBodyBytes)
	if !strings.Contains(full, "Answer in the Desk: "+link) || strings.Contains(full, "Cut here") {
		t.Fatalf("a body within the limit is whole:\n%s", full)
	}
	cut := promptBody(p, link, "", "\n\ncc @alice", 2000)
	if len(cut) > 2000 {
		t.Fatalf("cut to the limit: %d bytes", len(cut))
	}
	for _, want := range []string{"🧭 **Q4:**", "_Cut here: [the full text is in the Desk](" + link + ")._", "Answer in the Desk: " + link, "cc @alice", promptMarker(p)} {
		if !strings.Contains(cut, want) {
			t.Errorf("the cut body keeps %q:\n%s", want, cut)
		}
	}
	if !strings.HasSuffix(cut, promptMarker(p)) {
		t.Errorf("the marker stays last:\n%s", cut)
	}
}

// fromAnswer adds the pilot's option label, made from an owner's answer, to
// the open prompt id of the milestone, as consumeRefined does.
func (e *projectEnv) fromAnswer(id, label string) {
	e.t.Helper()
	o := pilotOption(label, aiv1.PilotAction{Tool: "retry", IssueIID: 1})
	if reply, err := e.r.addFromAnswer(e.ctx, e.task(specTaskName), id, *o, "the pilot's option"); err != nil || !strings.Contains(reply, "from your answer") {
		e.t.Fatalf("the option is added: %q %v", reply, err)
	}
}

// promptNoteBody is the comment of prompt id on issue iid.
func promptNoteBody(e *projectEnv, iid int64, id string) string {
	e.t.Helper()
	p := e.prompt(specTaskName, id)
	for _, n := range promptNotes(e, iid) {
		if n.ID == p.PostedNoteID {
			return n.Body
		}
	}
	e.t.Fatalf("no comment of %s on #%d", id, iid)
	return ""
}

func TestOpenPromptCommentShowsTheOptionFromAnAnswer(t *testing.T) {
	e := newAnswerEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	root := e.postedPrompt(freeTextPrompt())
	if !strings.Contains(root.Body, "cc @alice") || e.prompt(specTaskName, "Q1").MirroredOptions != e.prompt(specTaskName, "Q1").OptionsDigest() {
		t.Fatalf("the first post mentions the owners and records its options:\n%s", root.Body)
	}

	e.fromAnswer("Q1", "re-review #1, then retry")
	edits := e.gl.NoteEdits
	e.reconcile()
	if e.gl.NoteEdits != edits+1 {
		t.Fatalf("the next poll edits the comment once: %d edits", e.gl.NoteEdits-edits)
	}
	body := promptNoteBody(e, tracking, "Q1")
	for _, want := range []string{
		"- **D** Do nothing — \u200b/merge\n- **E** From your answer: re-review #1, then retry (from an owner's answer) — d\n",
		"Answer in the Desk.",
		"\n\n" + letterHint,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the edit lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "@alice") {
		t.Errorf("an edit mentions nobody: GitLab would notify again:\n%s", body)
	}
	assertInert(t, body)
	if p := e.prompt(specTaskName, "Q1"); p.MirroredOptions != p.OptionsDigest() || p.MirroredState != aiv1.PromptOpen || len(promptNotes(e, tracking)) != 1 {
		t.Fatalf("the options are recorded as mirrored, nothing posted: %+v", p)
	}

	edits = e.gl.NoteEdits
	e.reconcile()
	e.reconcile()
	if e.gl.NoteEdits != edits || promptNoteBody(e, tracking, "Q1") != body {
		t.Fatalf("an unchanged prompt is not edited again: %d edits", e.gl.NoteEdits-edits)
	}

	// A newer answer's option takes its place.
	e.fromAnswer("Q1", "pause instead")
	e.reconcile()
	body = promptNoteBody(e, tracking, "Q1")
	if !strings.Contains(body, "- **E** From your answer: pause instead (from an owner's answer) — d\n") || strings.Contains(body, "re-review #1, then retry") {
		t.Fatalf("the replaced option is shown, the old one gone:\n%s", body)
	}

	// Answered: the option from the answer is struck through like the others.
	e.answer(specTaskName, "Q1", aiv1.PromptAnswer{OptionID: "D", By: "alice", Via: "desk"})
	e.reconcile()
	body = promptNoteBody(e, tracking, "Q1")
	for _, want := range []string{
		"✅ Answered by `@alice` in the Desk: Do nothing\n\n",
		"- ~~**C** Pause~~\n- **D** Do nothing — \u200b/merge ✅\n- ~~**E** From your answer: pause instead (from an owner's answer) — d~~\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the close edit lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, letterHint) {
		t.Errorf("an answered prompt has no letter hint:\n%s", body)
	}
	if p := e.prompt(specTaskName, "Q1"); p.MirroredState != aiv1.PromptAnswered || p.MirroredOptions != p.OptionsDigest() {
		t.Fatalf("the close edit records its options: %+v", p)
	}
}

func TestPromptsMirroredBeforeTheOptionsDigest(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	st := e.task(specTaskName)
	e.addPrompt(st, st, trackingPrompt())
	mr := issuePrompt(aiv1.PromptAssessment, 1)
	mr.Source.MRIID = 5
	e.addPrompt(st, st, mr)
	e.addPrompt(st, st, issuePrompt(aiv1.PromptAssessment, 1))
	e.reconcile()
	e.answer(specTaskName, "Q3", aiv1.PromptAnswer{OptionID: "B", By: "alice", Via: "desk"})
	e.reconcile()
	e.reconcile()
	before := promptNoteBody(e, tracking, "Q1")
	closed := promptNoteBody(e, 1, "Q3")

	// As an operator before mirroredOptions left them.
	if err := e.r.patchPrompts(e.ctx, e.task(specTaskName), func(list []aiv1.Prompt) []aiv1.Prompt {
		for i := range list {
			list[i].MirroredOptions = ""
		}
		return list
	}); err != nil {
		t.Fatal(err)
	}
	edits := e.gl.NoteEdits
	e.reconcile()
	if e.gl.NoteEdits != edits+2 {
		t.Fatalf("each open prompt gets one edit, the closed one none: %d edits", e.gl.NoteEdits-edits)
	}
	if after := promptNoteBody(e, tracking, "Q1"); after != strings.Replace(before, "\n\ncc @alice", "", 1) {
		t.Fatalf("the edit shows the same, without the mention:\n%s\n---\n%s", before, after)
	}
	if m := e.gl.MRNotes[5]; len(m) != 1 || strings.Contains(m[0].Body, "cc @") || !strings.Contains(m[0].Body, "- **A** Retry it (recommended)\n- **B** Do nothing\n") {
		t.Fatalf("the merge request's comment is edited too: %+v", m)
	}
	if promptNoteBody(e, 1, "Q3") != closed {
		t.Fatal("a closed prompt's comment is left alone")
	}
	for _, id := range []string{"Q1", "Q2"} {
		if p := e.prompt(specTaskName, id); p.MirroredOptions != p.OptionsDigest() {
			t.Fatalf("%s records its options: %+v", id, p)
		}
	}
	edits = e.gl.NoteEdits
	e.reconcile()
	if e.gl.NoteEdits != edits {
		t.Fatalf("once: %d more edits", e.gl.NoteEdits-edits)
	}
}

func TestOptionsDigest(t *testing.T) {
	p := trackingPrompt()
	d := p.OptionsDigest()
	if len(d) != 12 || d != trackingPrompt().OptionsDigest() {
		t.Fatalf("12 hex characters, stable: %q", d)
	}
	q := trackingPrompt()
	q.Options[0].Actions = append(q.Options[0].Actions, aiv1.PilotAction{Tool: "pause"})
	q.Context = "other"
	if q.OptionsDigest() != d {
		t.Fatal("actions and context are not shown in the options")
	}
	for name, change := range map[string]func(*aiv1.PromptOption){
		"label":       func(o *aiv1.PromptOption) { o.Label += "!" },
		"detail":      func(o *aiv1.PromptOption) { o.Detail += "!" },
		"recommended": func(o *aiv1.PromptOption) { o.Recommended = !o.Recommended },
		"fromAnswer":  func(o *aiv1.PromptOption) { o.FromAnswer = !o.FromAnswer },
		"id":          func(o *aiv1.PromptOption) { o.ID = "Z" },
	} {
		q := trackingPrompt()
		change(&q.Options[3])
		if q.OptionsDigest() == d {
			t.Errorf("a changed %s changes the digest", name)
		}
	}
	q = trackingPrompt()
	q.Options = q.Options[:3]
	if q.OptionsDigest() == d {
		t.Error("a removed option changes the digest")
	}
}

func TestOpenEditIsCutKeepingTheDeskLink(t *testing.T) {
	p := trackingPrompt()
	p.ID, p.State, p.PostedAt = "Q4", aiv1.PromptOpen, &metav1.Time{Time: time.Unix(1_700_000_000, 0)}
	p.Context = strings.Repeat("evidence ", 2000)
	p.Options = append(p.Options, *pilotOption("re-review #1", aiv1.PilotAction{Tool: "reReview", IssueIID: 1}))
	p.Options[4].ID = "E"
	link := "https://desk.example/projects/ns/shop/milestones/3#prompt-Q4"
	hint := "\n\n" + letterHint
	if full := promptBody(p, link, "", hint, noteBodyBytes); !strings.Contains(full, "- **E** From your answer: re-review #1 (from an owner's answer) — d\n") {
		t.Fatalf("a body within the limit has the option from the answer:\n%s", full)
	}
	cut := promptBody(p, link, "", hint, 2000)
	if len(cut) > 2000 {
		t.Fatalf("cut to the limit: %d bytes", len(cut))
	}
	for _, want := range []string{"_Cut here: [the full text is in the Desk](" + link + ")._", "Answer in the Desk: " + link + hint, promptMarker(p)} {
		if !strings.Contains(cut, want) {
			t.Errorf("the cut edit keeps %q:\n%s", want, cut)
		}
	}
	if !strings.HasSuffix(cut, promptMarker(p)) || strings.Contains(cut, "@alice") {
		t.Errorf("the marker stays last, no mention:\n%s", cut)
	}
}

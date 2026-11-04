package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// The GitLab mirror of the prompts: every prompt is one comment of the bot
// on its source (its merge request, its issue, else the milestone's
// tracking issue), edited when the prompt closes; an answer gets one reply
// too. mirrorPrompts compares each prompt's State with MirroredState, so
// every producer and every answer path is mirrored without code of its
// own. GitLab errors are logged and tried again next poll.

// maxNoteBytes is GitLab's limit for a comment; bodies keep a margin.
const (
	maxNoteBytes   = 1_000_000
	noteBodyBytes  = maxNoteBytes - 10_000
	maxAnswerQuote = 2000
)

// mirrorPrompts brings the GitLab comments of holder's prompts up to their
// state. tracking is the milestone's tracking issue, where prompts without
// an issue or merge request go (0: none). Only errors writing the status
// are returned.
func (r *AgentProjectReconciler) mirrorPrompts(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	holder *aiv1.AgentTask, tracking int64) error {
	var errs []error
	for _, p := range slices.Clone(holder.Status.Prompts) {
		if p.State == "" || p.State == p.MirroredState {
			continue
		}
		if err := r.mirrorPrompt(ctx, gl, project, spec, holder, tracking, p); err != nil {
			errs = append(errs, fmt.Errorf("mirroring prompt %s of %s: %w", p.ID, holder.Name, err))
		}
	}
	return errors.Join(errs...)
}

// mirrorPrompt posts p or edits its comment, and replies to an answer. A
// comment is recorded after it is posted; before posting, the comments are
// searched for its marker, so a crash in between does not post it twice.
// An answer whose option has actions waits for their outcome, two polls
// at most.
func (r *AgentProjectReconciler) mirrorPrompt(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	holder *aiv1.AgentTask, tracking int64, p aiv1.Prompt) error {
	logger := log.FromContext(ctx).WithValues("task", holder.Name, "prompt", p.ID)
	n := promptNoteOf(gl, spec, holder, p, tracking)
	if n.iid == 0 {
		logger.V(1).Info("prompt not mirrored: it has no issue, merge request or tracking issue")
		return nil
	}
	if p.State == aiv1.PromptAnswered && !r.outcomeDue(spec, p) {
		return nil
	}
	bot := project.Status.BotUserID
	link := desklink.Prompt(r.DeskURL, holder, p.ID)
	body := promptBody(p, link, supersededBy(holder.Status.Prompts, p), "", noteBodyBytes)
	id, url, posted := p.PostedNoteID, p.PostedNoteURL, false
	if id == 0 {
		notes, err := n.list(ctx)
		if err != nil {
			logger.Info("could not look for the comment of a prompt", "error", err.Error())
			return nil
		}
		id = botNote(notes, bot, promptMarker(p))
		switch {
		case id == 0 && (p.State == aiv1.PromptSuperseded || p.State == aiv1.PromptExpired):
			// Closed before anyone saw it: nothing to show.
			return r.recordMirror(ctx, holder, p.ID, 0, "", p.State)
		case id == 0:
			extra := ""
			if p.State == aiv1.PromptOpen && letterAnswers(spec, holder, p, n) {
				extra = "\n\nOwners can also reply here with the option's letter."
			}
			if p.State == aiv1.PromptOpen && !p.Quiet {
				extra += notify.Mention(holder.Spec.Owners)
			}
			note, err := n.create(ctx, promptBody(p, link, "", extra, noteBodyBytes))
			if err != nil {
				logger.Info("could not post the comment of a prompt", "issue", n.iid, "mr", n.mr, "error", err.Error())
				return nil
			}
			id, posted = note.ID, true
		}
		url = n.url(spec, id)
	}
	if p.State != aiv1.PromptOpen {
		// An edit carries no mention: GitLab would notify again.
		if !posted {
			if err := n.update(ctx, id, body); err != nil {
				logger.Info("could not edit the comment of a prompt", "note", id, "error", err.Error())
				return nil
			}
		}
		if p.State == aiv1.PromptAnswered && p.Answer != nil {
			if err := n.reply(ctx, bot, id, answerReply(p, noteBodyBytes), answerMarker(p)); err != nil {
				logger.Info("could not reply with the answer to a prompt", "note", id, "error", err.Error())
				return nil
			}
		}
	}
	logger.Info("prompt mirrored", "state", p.State, "note", id)
	return r.recordMirror(ctx, holder, p.ID, id, url, p.State)
}

// outcomeDue reports whether the answer to p can be mirrored: its option
// runs no actions, their outcome is in, or two polls went by without it.
func (r *AgentProjectReconciler) outcomeDue(spec aiv1.AgentProjectSpec, p aiv1.Prompt) bool {
	a := p.Answer
	if a == nil || a.Outcome != "" {
		return true
	}
	if o := p.Option(a.OptionID); o == nil || len(o.Actions) == 0 {
		return true
	}
	return !r.now().Before(a.At.Add(2 * spec.PollInterval.Duration))
}

// recordMirror records the comment of prompt id on holder (kept when it
// has one) and the state it shows.
func (r *AgentProjectReconciler) recordMirror(ctx context.Context, holder *aiv1.AgentTask, id string, noteID int64, url string, state aiv1.PromptState) error {
	return r.patchPrompts(ctx, holder, func(list []aiv1.Prompt) []aiv1.Prompt {
		for i := range list {
			if q := &list[i]; q.ID == id {
				if q.PostedNoteID == 0 && noteID != 0 {
					q.PostedNoteID, q.PostedNoteURL = noteID, url
				}
				q.MirroredState = state
			}
		}
		return list
	})
}

// promptMarker and answerMarker find the comment of a prompt and the reply
// to its answer again. inert turns "<!--" in untrusted text into
// "&lt;!--", so no text forges them.
func promptMarker(p aiv1.Prompt) string {
	return fmt.Sprintf("<!-- ai-operator:prompt=%s posted=%d -->", p.ID, postedUnix(p))
}

func answerMarker(p aiv1.Prompt) string {
	return fmt.Sprintf("<!-- ai-operator:prompt-answer=%s posted=%d -->", p.ID, postedUnix(p))
}

// postedUnix tells a prompt from one with the same id before it, e.g. of a
// recreated task.
func postedUnix(p aiv1.Prompt) int64 {
	if p.PostedAt == nil {
		return 0
	}
	return p.PostedAt.Unix()
}

// botNote is the id of the bot's note with the marker, 0 if there is none.
func botNote(notes []gitlab.Note, bot int64, marker string) int64 {
	for _, n := range notes {
		if n.Author.ID == bot && !n.System && strings.Contains(n.Body, marker) {
			return n.ID
		}
	}
	return 0
}

// supersededBy is the id of the newer prompt of the same source that
// superseded p, "" if none did.
func supersededBy(list []aiv1.Prompt, p aiv1.Prompt) string {
	if p.State != aiv1.PromptSuperseded || p.Source.Key == "" {
		return ""
	}
	i := slices.IndexFunc(list, func(q aiv1.Prompt) bool { return q.ID == p.ID })
	for _, q := range list[i+1:] {
		if q.Source.Kind == p.Source.Kind && q.Source.Key == p.Source.Key {
			return q.ID
		}
	}
	return ""
}

// promptBody is the comment of p: its state once it is closed, the
// question, the inert context, the options and the Desk link, then
// extra (the letter hint and the mention, only in a new comment) and the
// marker. A body over limit loses the end of the context and options,
// never the link or the marker.
func promptBody(p aiv1.Prompt, link, newer, extra string, limit int) string {
	var b strings.Builder
	if s := stateLine(p, newer); s != "" {
		b.WriteString(s + "\n\n")
	}
	fmt.Fprintf(&b, "🧭 **%s:** %s", p.ID, inertLine(p.Question, aiv1.MaxPromptQuestion))
	if c := inert(p.Context); c != "" {
		b.WriteString("\n\n" + c)
	}
	if len(p.Options) > 0 {
		b.WriteString("\n\n" + optionList(p))
	}
	tail := "\n\n" + deskLine(link, p.State.Closed()) + extra + "\n\n" + promptMarker(p)
	return fitNote(b.String(), tail, link, limit)
}

// stateLine is the first line of a closed prompt's comment.
func stateLine(p aiv1.Prompt, newer string) string {
	switch p.State {
	case aiv1.PromptAnswered:
		a := p.Answer
		if a == nil {
			return "✅ Answered"
		}
		s := fmt.Sprintf("✅ Answered by `@%s` %s", inertLine(a.By, 100), answeredVia(a))
		if o := p.Option(a.OptionID); o != nil {
			return s + ": " + inertLine(o.Label, aiv1.MaxPromptOptionLabel)
		}
		return s + " in their own words"
	case aiv1.PromptSuperseded:
		return "⏹️ Superseded: " + supersededReason(p, newer)
	case aiv1.PromptExpired:
		return "⌛ Expired: nobody answered it within 24 h"
	}
	return ""
}

func answeredVia(a *aiv1.PromptAnswer) string {
	if a.Via == "gitlab" {
		return "in GitLab"
	}
	return "in the Desk"
}

// supersededReason says why p no longer applies, as far as the prompt
// tells.
func supersededReason(p aiv1.Prompt, newer string) string {
	src := p.Source
	switch {
	case newer != "":
		return fmt.Sprintf("**%s** asks again", newer)
	case src.Kind == aiv1.PromptProposal:
		return "the milestone's state changed since it was made"
	case src.Kind == aiv1.PromptMainHealth:
		return "the default branch is healthy again, or its issue is closed"
	case src.Kind == aiv1.PromptNeedsInput:
		return "the agent ran again, or the issue is closed"
	case src.IssueIID != 0:
		return fmt.Sprintf("#%d is merged or closed", src.IssueIID)
	}
	return "its source resolved"
}

// optionList lists the options, the recommended one marked; once answered
// all but the chosen one are struck through.
func optionList(p aiv1.Prompt) string {
	chosen := ""
	if p.State == aiv1.PromptAnswered && p.Answer != nil {
		chosen = p.Answer.OptionID
	}
	var lines []string
	for _, o := range p.Options {
		line := fmt.Sprintf("**%s** %s", o.ID, inertLine(o.Label, aiv1.MaxPromptOptionLabel))
		if o.Recommended {
			line += " (recommended)"
		}
		if o.FromAnswer {
			line += " (from an owner's answer)"
		}
		if d := inertLine(o.Detail, aiv1.MaxPromptOptionDetail); d != "" {
			line += " — " + d
		}
		switch {
		case p.State != aiv1.PromptAnswered:
		case o.ID == chosen:
			line += " ✅"
		default:
			line = "~~" + line + "~~"
		}
		lines = append(lines, "- "+line)
	}
	return strings.Join(lines, "\n")
}

// deskLine points to the prompt in the Desk.
func deskLine(link string, closed bool) string {
	switch {
	case link == "" && closed:
		return "See it in the Desk."
	case link == "":
		return "Answer in the Desk."
	case closed:
		return "[See it in the Desk](" + link + ")"
	}
	return "Answer in the Desk: " + link
}

// answerReply is the reply to the comment of an answered prompt: the
// chosen option, the owner's words quoted as untrusted, and the outcome
// of the option's actions.
func answerReply(p aiv1.Prompt, limit int) string {
	a := p.Answer
	var b strings.Builder
	fmt.Fprintf(&b, "✅ **%s** answered by `@%s` %s", p.ID, inertLine(a.By, 100), answeredVia(a))
	if o := p.Option(a.OptionID); o != nil {
		fmt.Fprintf(&b, ": **%s** %s", o.ID, inertLine(o.Label, aiv1.MaxPromptOptionLabel))
	}
	if t := inert(textutil.Truncate(strings.TrimSpace(a.Text), maxAnswerQuote)); t != "" {
		b.WriteString("\n\nIn their words (untrusted):\n\n" + quote(t))
	}
	if out := inert(a.Outcome); out != "" {
		b.WriteString("\n\n" + out)
	}
	return fitNote(b.String(), "\n\n"+answerMarker(p), "", limit)
}

// quote makes s a markdown block quote.
func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

// fitNote is body+tail, body cut to fit limit; a cut body ends with a
// pointer to the Desk.
func fitNote(body, tail, link string, limit int) string {
	if len(body)+len(tail) <= limit {
		return body + tail
	}
	cut := "\n\n_Cut here: the full text is in the Desk._"
	if link != "" {
		cut = "\n\n_Cut here: [the full text is in the Desk](" + link + ")._"
	}
	return textutil.Truncate(body, max(0, limit-len(tail)-len(cut)-len("…"))) + cut + tail
}

// promptNote is where the comment of a prompt goes: a merge request or an
// issue.
type promptNote struct {
	gl      gitlab.API
	project string
	iid     int64
	mr      bool
}

// promptNoteOf is the merge request of p, else its issue, else the
// tracking issue (an Implement task's issue for its own prompts). A
// proposal goes on the tracking issue, where the pilot is asked and
// assesses.
func promptNoteOf(gl gitlab.API, spec aiv1.AgentProjectSpec, holder *aiv1.AgentTask, p aiv1.Prompt, tracking int64) promptNote {
	n := promptNote{gl: gl, project: spec.GitLab.Project}
	switch {
	case p.Source.Kind == aiv1.PromptProposal && tracking != 0:
		n.iid = tracking
	case p.Source.MRIID != 0:
		n.iid, n.mr = p.Source.MRIID, true
	case p.Source.IssueIID != 0:
		n.iid = p.Source.IssueIID
	case tracking != 0:
		n.iid = tracking
	case holder.Spec.Type == aiv1.TaskTypeImplement && holder.Spec.Issue != nil:
		n.iid = holder.Spec.Issue.IID
	}
	return n
}

func (n promptNote) list(ctx context.Context) ([]gitlab.Note, error) {
	if n.mr {
		return n.gl.ListMergeRequestNotes(ctx, n.project, n.iid)
	}
	return n.gl.ListIssueNotes(ctx, n.project, n.iid)
}

func (n promptNote) create(ctx context.Context, body string) (*gitlab.Note, error) {
	if n.mr {
		return n.gl.CreateMergeRequestNote(ctx, n.project, n.iid, body)
	}
	return n.gl.CreateIssueNote(ctx, n.project, n.iid, body)
}

func (n promptNote) update(ctx context.Context, id int64, body string) error {
	var err error
	if n.mr {
		_, err = n.gl.UpdateMergeRequestNote(ctx, n.project, n.iid, id, body)
	} else {
		_, err = n.gl.UpdateIssueNote(ctx, n.project, n.iid, id, body)
	}
	return err
}

// reply posts body in the thread of note root, unless the bot's note with
// the marker is there already. A merge request, or an issue whose thread
// is not found, gets it as a comment of its own.
func (n promptNote) reply(ctx context.Context, bot, root int64, body, marker string) error {
	if n.mr {
		notes, err := n.list(ctx)
		if err != nil || botNote(notes, bot, marker) != 0 {
			return err
		}
		_, err = n.create(ctx, body)
		return err
	}
	threads, err := n.gl.ListIssueDiscussions(ctx, n.project, n.iid)
	if err != nil {
		return err
	}
	thread := ""
	for _, d := range threads {
		if botNote(d.Notes, bot, marker) != 0 {
			return nil
		}
		if len(d.Notes) > 0 && d.Notes[0].ID == root {
			thread = d.ID
		}
	}
	if thread == "" {
		_, err = n.create(ctx, body)
	} else {
		_, err = n.gl.ReplyIssueDiscussion(ctx, n.project, n.iid, thread, body)
	}
	return err
}

func (n promptNote) url(spec aiv1.AgentProjectSpec, id int64) string {
	kind := "issues"
	if n.mr {
		kind = "merge_requests"
	}
	return fmt.Sprintf("%s/%s/-/%s/%d#note_%d", strings.TrimRight(spec.GitLab.URL, "/"), spec.GitLab.Project, kind, n.iid, id)
}

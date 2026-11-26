package runner

import (
	"context"
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// runSteps continues an implementer that returned "in_progress": the step is
// committed and pushed without CI, and the session resumed with the comments
// written in the meantime, until the agent is done (or out of steps). answer
// collects the steps: decisions add up, the rest is the last step's. It
// returns the newest comment handed over.
//
// A command ("/agent …") is not handed over: the operator starts a run for
// it, which handles it (a revisit with its decision) once this one is done.
// So comments are read up to the first command only.
//
// With freshSteps (stepSession: fresh) every step after the first starts a
// new session with the run's prompt, the earlier steps' summaries, the agent
// notes and the comments handed over so far. Only the last step's session
// becomes the implementer's, once the steps are done.
//
// A step whose comments link uploads (fresh steps: those handed over and
// new) gets them in .attachments, after the run's links atts, and listed in
// its prompt; otherwise the folder stays as the run's prompt left it.
func (r *Runner) runSteps(ctx context.Context, gl gitlab.API, glProject string, issue *gitlab.Issue, mrIID, botID int64, prefix, branch, base string, types []string,
	role aiv1.RoleSpec, freshSteps bool, inv Invocation, fullPrompt string, atts runAttachments, res *Result, answer *ImplementAnswer, st *state, out *Outcome) (*Result, int64, error) {
	var seen int64
	pushSteps := true
	summaries := []string{answer.Summary}
	var handedIssue, handedMR []gitlab.Note
	// last is the last fresh step that ran: the next run resumes it.
	var last *Result
	defer func() {
		if last == nil {
			return
		}
		st.setSession("implement", ranOn(role, last), last.SessionID)
		st.ImplementEngine = ranOn(role, last)
		if err := r.saveState(*st); err != nil {
			r.log().Error(err, "saving the implementer's session")
		}
	}()
	for step := 1; answer.Status == "in_progress" && step < maxSteps; step++ {
		if err := r.commit(ctx, branch, base, commitMessage(answer.CommitMessage, issue, types)); err != nil {
			return res, seen, err
		}
		// The companion's step commit: the push below checks it.
		if c := r.companion; c != nil {
			if err := r.commitCompanion(ctx, c, commitMessage(answer.CommitMessage, issue, types)); err != nil {
				return res, seen, err
			}
		}
		if pushSteps {
			s := r.step(ctx, fmt.Sprintf("push step %d", step), "branch", branch)
			pushed, err := r.publish(ctx, branch, base, true)
			if _, refused := AsCompanionViolation(err); refused {
				// Not a race with another push: the run fails, unpushed.
				return res, seen, s.fail(err)
			}
			switch {
			case err != nil:
				// Someone pushed to the branch meanwhile: the steps are pushed
				// with the run, which reports a conflict then.
				pushSteps = false
				s.done("not pushed, the next steps neither: "+err.Error(), "error", err.Error())
			case pushed:
				s.done(fmt.Sprintf("%s at %s, without CI", branch, shortSHA(r.pushedHead)), "sha", r.pushedHead)
			default:
				s.done("nothing new to push")
			}
		}

		// Comments written during the step steer the next one.
		// Without GitLab input only the MR's review threads do.
		s := r.step(ctx, "read comments")
		var issueNotes []gitlab.Note
		if r.projectSpec.Workflow.Takes(aiv1.InputGitLab) {
			var err error
			if issueNotes, err = gl.ListIssueNotes(ctx, glProject, issue.IID); err != nil {
				return res, seen, s.fail(fmt.Errorf("listing issue notes: %w", err))
			}
			_, issueNotes = splitHandoff(issueNotes, botID)
			issueNotes = r.withoutPilot(issueNotes)
		}
		mrNotes, err := gl.ListMergeRequestNotes(ctx, glProject, mrIID)
		if err != nil {
			return res, seen, s.fail(fmt.Errorf("listing merge request notes: %w", err))
		}
		mrNotes = r.withoutPilot(slices.DeleteFunc(mrNotes, func(n gitlab.Note) bool { return isProgress(n, botID) }))
		newIssue, newMR := newerThan(issueNotes, st.LastIssueNoteID), newerThan(mrNotes, st.LastMRNoteID)
		if cut := firstCommand(append(slices.Clone(newIssue), newMR...), botID, prefix); cut != 0 {
			fromCut := func(n gitlab.Note) bool { return n.ID >= cut }
			newIssue, newMR = slices.DeleteFunc(newIssue, fromCut), slices.DeleteFunc(newMR, fromCut)
		}
		fresh := humanNotes(newIssue, botID) + humanNotes(newMR, botID)
		s.done(fmt.Sprintf("%d new", fresh), "newNotes", fresh)
		next := inv
		next.Kind = aiv1.CallKindImplement
		next.Resume = res.SessionID
		next.Prompt = stepContinue
		// shownIssue and shownMR are the new comments the prompt shows.
		var shownIssue, shownMR []gitlab.Note
		if fresh > 0 {
			shownIssue, shownMR = newIssue, newMR
			next.Prompt = stepContinue + "\n\n" + thread("New comments on the issue", newIssue, botID) + thread("New comments on the merge request", newMR, botID)
		}
		var stepPrompt string
		if freshSteps {
			next.Resume = ""
			section := r.stepAttachments(ctx, gl, glProject, atts, "The files linked in the comments since the run started, newest first.",
				slices.Concat(handedIssue, shownIssue), slices.Concat(handedMR, shownMR))
			stepPrompt = fullPrompt + "\n\n" + buildFreshStepPrompt(summaries, answer.Handoff, handedIssue, handedMR, botID) + "\n\n" + section + next.Prompt
		} else {
			next.Prompt += r.stepAttachments(ctx, gl, glProject, atts, "The files linked in the new comments, newest first.", shownIssue, shownMR)
			// Should the session be gone, a fresh one also gets the new comments.
			stepPrompt = fullPrompt + "\n\n" + next.Prompt
		}

		s = r.step(ctx, fmt.Sprintf("run implementer, step %d", step+1), "engine", role.Engine, "model", role.Model, "newNotes", fresh, "resume", next.Resume != "")
		r.cutStep(false)
		res, err = r.invoke(ctx, role, next, stepPrompt)
		if res != nil {
			out.CostUSD += res.CostUSD
		}
		if err != nil {
			return res, seen, s.fail(err)
		}
		if freshSteps {
			last = res
			handedIssue, handedMR = append(handedIssue, newIssue...), append(handedMR, newMR...)
		} else {
			st.setSession("implement", ranOn(role, res), res.SessionID)
			st.ImplementEngine = ranOn(role, res)
		}
		st.LastIssueNoteID = maxNoteID(newIssue, st.LastIssueNoteID)
		st.LastMRNoteID = maxNoteID(newMR, st.LastMRNoteID)
		seen = max(seen, maxNoteID(newIssue, 0), maxNoteID(newMR, 0))
		if err := r.saveState(*st); err != nil {
			return res, seen, s.fail(err)
		}
		var more ImplementAnswer
		if err := res.Decode(&more); err != nil {
			return res, seen, s.fail(fmt.Errorf("decoding implementer answer: %w", err))
		}
		more.Decisions = append(answer.Decisions, more.Decisions...)
		if strings.TrimSpace(more.Handoff) == "" {
			more.Handoff = answer.Handoff
		}
		*answer = more
		summaries = append(summaries, more.Summary)
		s.done(agentResult(role, res, answer.Status), "session", res.SessionID, "costUSD", res.CostUSD, "status", answer.Status)
	}
	return res, seen, nil
}

// firstCommand returns the ID of the oldest human comment that is a command,
// 0 if there is none.
func firstCommand(notes []gitlab.Note, botID int64, prefix string) int64 {
	var first int64
	for _, n := range notes {
		// A pure model or effort command starts no run: it is no cut.
		if n.System || (botID != 0 && n.Author.ID == botID) || !strings.HasPrefix(strings.TrimSpace(n.Body), prefix) || command.IsRoutingOnly(n.Body, prefix) {
			continue
		}
		if first == 0 || n.ID < first {
			first = n.ID
		}
	}
	return first
}

// ciRound is the CI fix round a push of this run counts as: a CI fix run
// is one more, a run the pilot started keeps the count (so it cannot start
// another full set of fix rounds), any other run starts over.
func ciRound(task *aiv1.AgentTask, ciFix bool) int32 {
	switch {
	case ciFix:
		return task.Status.Run.CIRound + 1
	case task.Spec.Source.PilotRun != 0 && task.Spec.Source.PilotRun == task.Spec.RunRequest:
		return task.Status.Run.CIRound
	}
	return 0
}

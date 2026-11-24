package runner

import (
	"context"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// report describes the change in the merge request, comments the run's
// report and moves the issue to the next column.
func (run *implementRun) report(ctx context.Context) error {
	r, gl, glProject, out, answer, task, spec, wf, issue, mr := run.r, run.gl, run.glProject, run.out, &run.answer, run.task, run.spec, run.wf, run.issue, run.mr
	// The description covers the whole change; the first one replaces the
	// first report's summary (the task status keeps it).
	// An agent may leave the summary empty (cursor does when it changed
	// nothing): the report and the task status still say what the run did.
	if strings.TrimSpace(answer.Summary) == "" {
		answer.Summary = emptySummary(run.pushed || r.stepHead() != "", out.PushedSHA)
	}
	reported := *answer
	if !spec.Agents.Describe.Disabled {
		first, notice, err := r.describe(ctx, spec.Agents.Describe, issue, answer.Summary, describeTarget{
			gl: gl, project: glProject, mrIID: mr.IID, repo: homeTree, base: run.base, described: &run.state.DescribedDiff, links: []string{run.homeLink},
			types: run.types, title: &run.state.MRTitle,
		}, run.state, out)
		if err != nil {
			return err
		}
		if notice != "" {
			run.notices = append(run.notices, notice)
		}
		if notice, err = run.describeCompanion(ctx); err != nil {
			return err
		}
		if notice != "" {
			run.notices = append(run.notices, notice)
		}
		// A run that stops for input keeps saying why.
		if first && answer.Status != "needs_input" && !answer.asks() {
			reported.Summary = "📝 Described the change in the merge request description."
		}
	}
	// Report back and move the issue to the next column - unless another
	// run was requested meanwhile, in which case the issue stays "working".
	// The run log covers the steps up to here.
	needsInput := answer.Status == "needs_input" || answer.asks()
	run.needsInput = needsInput
	// With the CI loop, a fresh push stays in "working" until the operator
	// has seen its pipeline go green (or hands it back); so does a fresh
	// companion push next to a pushed home head.
	pushedCI := out.PushedSHA != "" || out.CompanionPushed && task.Status.Run.PushedSHA != ""
	waitForCI := !spec.CI.Disabled && pushedCI && !needsInput
	pending := !waitForCI && r.morePending(ctx, task.Spec.RunRequest)
	// A merge request that never had a pushed head stays out of review: the
	// merge gate would only find it there. A checkpoint counts as no push.
	nothingPushed := !needsInput && !waitForCI && !pending && out.PushedSHA == "" && task.Status.Run.PushedSHA == "" &&
		r.stepHead() == "" && !out.CompanionPushed
	var nothingPushedText string
	if nothingPushed {
		why := "the agent changed nothing"
		if run.checkpointHead {
			why = "its head is a checkpoint of unfinished work"
		}
		nothingPushedText = fmt.Sprintf("This run pushed no commits to !%d, so #%d stays out of review: %s. %s, or close the issue.", mr.IID, issue.IID, why,
			r.steer(spec, task, fmt.Sprintf("Comment `%s …` with what to change", wf.CommandPrefix), "Say what to change"))
		run.notices = append(run.notices, nothingPushedText)
	}
	drafts := questionDrafts(*answer)
	out.Questions = drafts[:min(len(drafts), aiv1.MaxQuestionDrafts)]
	desk := desklink.Task(r.DeskURL, task.Namespace, task.Spec.ProjectRef, task.Name)
	notices := append(append(run.reviewNotes, run.notices...), r.run.keptNotices()...)
	if n := r.run.rewriteNotice(); n != "" {
		notices = append(notices, n)
	}
	report := renderReport(reported, run.remaining, notices, desk)
	if run.ciFix {
		report = "CI fix: " + report
	}
	// The owners are mentioned where the answer is wanted: questions on the
	// issue, by the operator's comments of their prompts, everything else on
	// the MR. On autopilot nobody is: it merges what reaches review, and its
	// pause on the tracking issue mentions them for the rest.
	toReview := !needsInput && !waitForCI && !pending && !run.checkpointHead && !nothingPushed
	var mrMention string
	switch {
	case task.Spec.Autopilot:
	case len(drafts) > 0:
	case needsInput && spec.Notify.Wants(aiv1.NotifyNeedsInput),
		toReview && spec.Notify.Wants(aiv1.NotifyReview):
		mrMention = notify.Mention(task.Spec.Owners)
	}
	runLogMD := r.run.markdown(out.CostUSD)
	s := r.step(ctx, "comment")
	note, err := gl.CreateMergeRequestNote(ctx, glProject, mr.IID, "🤖 "+report+mrMention+runLogMD)
	if err != nil {
		return s.fail(fmt.Errorf("commenting on merge request: %w", err))
	}
	// The questions go on the issue as prompts: the operator posts one
	// comment per question from out.Questions.
	s.done(fmt.Sprintf("on !%d", mr.IID))
	s = r.step(ctx, "update issue labels")
	switch {
	case waitForCI:
		s.done(fmt.Sprintf("stays in %s until the pipeline is green", wf.WorkingLabel))
	case pending:
		s.done(fmt.Sprintf("stays in %s, another run is pending", wf.WorkingLabel))
	default:
		next := wf.ReviewLabel
		if needsInput || nothingPushed {
			next = wf.NeedsInputLabel
			// The cause goes before the label; the final patch keeps it.
			out.NeedsInputCause, out.NeedsInputURL = aiv1.NeedsInputAgentQuestion, noteURL(mr.WebURL, note)
			if nothingPushed {
				out.NeedsInputCause = aiv1.NeedsInputNothingPushed
				run.reportNothingPushed(ctx, nothingPushedText)
			}
			if err := r.patchRun(ctx, func(s *aiv1.RunStatus) {
				s.NeedsInputCause, s.NeedsInputURL = out.NeedsInputCause, out.NeedsInputURL
			}); err != nil {
				return s.fail(fmt.Errorf("recording the needs-input cause: %w", err))
			}
		}
		if _, err := gl.UpdateIssue(ctx, glProject, issue.IID, gitlab.UpdateIssueOptions{
			AddLabels: []string{next}, RemoveLabels: []string{wf.WorkingLabel, wf.ReadyLabel},
		}); err != nil {
			return s.fail(fmt.Errorf("updating issue labels: %w", err))
		}
		s.done("moved to "+next, "label", next)
		if next == wf.ReviewLabel && !task.Spec.Autopilot && *spec.Notify.RequestReview && len(task.Spec.Owners) > 0 {
			s = r.step(ctx, "request review")
			if run.checkpointHead {
				s.done("none requested: the head is a checkpoint")
			} else if names, err := notify.RequestReview(ctx, gl, glProject, mr.IID, task.Spec.Owners); err != nil {
				s.done("failed: "+textutil.Truncate(err.Error(), 300), "error", err.Error())
			} else if len(names) == 0 {
				s.done("none requested: the merge request has reviewers, or the owners are unknown")
			} else {
				s.done("@"+strings.Join(names, ", @"), "reviewers", names)
			}
		}
	}
	return nil
}

// reportNothingPushed says on the issue why it stays out of review. The
// owners are mentioned as for a question, never on autopilot: its pause
// mentions them. A failed comment only costs the issue note, the MR report
// says it too.
func (run *implementRun) reportNothingPushed(ctx context.Context, text string) {
	r, task, spec := run.r, run.task, run.spec
	body := "🤖 " + text
	if !task.Spec.Autopilot && spec.Notify.Wants(aiv1.NotifyNeedsInput) {
		body += notify.Mention(task.Spec.Owners)
	}
	if _, err := run.gl.CreateIssueNote(ctx, run.glProject, run.issue.IID, body); err != nil {
		r.log().Error(err, "commenting on the issue that nothing was pushed")
	}
}

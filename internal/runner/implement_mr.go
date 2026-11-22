package runner

import (
	"context"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// setDraft marks the MR draft or ready, if it is not already, and reports
// whether it marked it ready. A draft the runner did not make after it last
// marked the MR ready is a human holding it, and stays. A failure is reported
// in the run log but does not fail the run: the work is done.
func (r *Runner) setDraft(ctx context.Context, gl gitlab.API, glProject string, mr *gitlab.MergeRequest, st *state, draft bool) bool {
	// The run can take long: draft state and title as they are now, so a
	// rename in the meantime is not undone.
	if cur, err := gl.GetMergeRequest(ctx, glProject, mr.IID); err == nil {
		mr.Draft, mr.Title = cur.Draft, cur.Title
	}
	defer func() {
		if err := r.saveState(*st); err != nil {
			r.log().Error(err, "saving the draft state")
		}
	}()
	if mr.Draft == draft {
		st.Ready = !draft
		return false
	}
	name, title := "mark ready", gitlab.ReadyTitle(mr.Title)
	if draft {
		name, title = "mark draft", gitlab.DraftTitle(mr.Title)
	}
	s := r.step(ctx, name, "mr", mr.IID)
	if !draft && st.Ready {
		s.done(fmt.Sprintf("!%d stays a draft: marked draft by hand", mr.IID))
		return false
	}
	res, err := gl.UpdateMergeRequest(ctx, glProject, mr.IID, gitlab.UpdateMergeRequestOptions{Title: title})
	if err != nil {
		_ = s.fail(fmt.Errorf("updating merge request !%d: %w", mr.IID, err))
		return false
	}
	mr.Draft, mr.Title = res.Draft, res.Title
	st.Ready = !res.Draft
	s.done(fmt.Sprintf("!%d %q", mr.IID, res.Title))
	return !res.Draft
}

// startPipeline starts a merge request pipeline for the head of a MR this run
// marked ready without pushing, unless CI already checked that head. A failure
// only means the head goes without CI, so it is not returned.
func (r *Runner) startPipeline(ctx context.Context, gl gitlab.API, glProject string, mr *gitlab.MergeRequest, task *aiv1.AgentTask, head string) {
	if ci := task.Status.CI; ci != nil && ci.SHA == head && ci.State != aiv1.CIStateNone {
		return
	}
	// Merge request pipelines also run for pushes with ci.skip: one may be
	// running for the head already. A skipped one does not count.
	if ps, err := gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{SHA: head, Limit: 5}); err == nil {
		for _, p := range ps {
			if p.Status != "skipped" {
				return
			}
		}
	}
	s := r.step(ctx, "start pipeline", "mr", mr.IID)
	err := gl.CreateMergeRequestPipeline(ctx, glProject, mr.IID)
	if code := gitlab.StatusCode(err); code >= 400 && code < 500 {
		// No merge request pipelines in this project: a branch pipeline.
		if _, berr := gl.CreatePipeline(ctx, glProject, mr.SourceBranch); berr == nil {
			s.done("branch pipeline for " + mr.SourceBranch)
			return
		}
	}
	if err != nil {
		s.done("none: "+err.Error(), "error", err.Error())
		return
	}
	s.done(fmt.Sprintf("for !%d", mr.IID))
}

// hasChanges reports whether the work branch changes anything against the
// default branch; an MR with only the start commit is not ready.
func (r *Runner) hasChanges(ctx context.Context, base string) bool {
	diff, _, err := r.Agent.Diff(ctx, homeTree, base)
	return err != nil || strings.TrimSpace(diff) != ""
}

// openMergeRequest opens the draft merge request of an issue before the
// agent starts. A new branch gets an empty start commit, pushed without CI;
// the agent's checkout then continues from it.
func (r *Runner) openMergeRequest(ctx context.Context, gl gitlab.API, glProject string, issue *gitlab.Issue, branch, base string, types []string) (*gitlab.MergeRequest, error) {
	s := r.step(ctx, "open merge request", "branch", branch, "target", base)
	started := false
	if !r.Mirror.HasRemoteBranch(ctx, branch) {
		if _, err := r.Mirror.StartBranch(ctx, branch, base, startMessage(issue.IID, types)); err != nil {
			return nil, s.fail(fmt.Errorf("creating branch %s: %w", branch, err))
		}
		started = true
	}
	opts := gitlab.CreateMergeRequestOptions{
		SourceBranch:       branch,
		TargetBranch:       base,
		Title:              "Draft: " + issue.Title,
		Description:        newDescription(issue.IID),
		RemoveSourceBranch: true,
	}
	// The issue's milestone lists the MR in its "Merge requests" tab.
	if issue.Milestone != nil {
		opts.MilestoneID = issue.Milestone.ID
	}
	// A create that failed with a 5xx or timed out may have landed: before
	// trying again, an open MR of the branch is taken as the result.
	var mr *gitlab.MergeRequest
	tried := false
	err := retryOneOff(ctx, s.log, "create merge request", gitlab.IsTransient, func() (err error) {
		if tried {
			if mr, err = gl.FindOpenMergeRequest(ctx, glProject, branch); err != nil || mr != nil {
				return err
			}
		}
		tried = true
		mr, err = gl.CreateMergeRequest(ctx, glProject, opts)
		return err
	})
	if err != nil {
		return nil, s.fail(fmt.Errorf("creating merge request: %w", err))
	}
	s.done(fmt.Sprintf("!%d", mr.IID), "mr", mr.IID, "url", mr.WebURL, "newBranch", started)
	if _, err := gl.CreateIssueNote(ctx, glProject, issue.IID, fmt.Sprintf("🤖 Working on this in %s; progress and reports are there.", mr.WebURL)); err != nil {
		r.log().Info("could not point the issue to the merge request", "error", err.Error())
	}
	if started {
		// The checkout was made before the branch existed.
		if err := r.prepareCheckout(ctx, branch, base); err != nil {
			return nil, err
		}
	}
	return mr, nil
}

// updateHandoff stores the implementer's handoff notes in the bot's handoff
// note on the issue, creating it on the first run. A failure is logged but
// does not fail the run: the work itself is pushed already.
func (r *Runner) updateHandoff(ctx context.Context, gl gitlab.API, glProject string, iid int64, note *gitlab.Note, text string, seenNote int64) {
	if strings.TrimSpace(text) == "" {
		return
	}
	s := r.step(ctx, "update agent notes")
	body := renderHandoff(text, r.run.id, seenNote)
	var err error
	if note != nil {
		_, err = gl.UpdateIssueNote(ctx, glProject, iid, note.ID, body)
	} else {
		_, err = gl.CreateIssueNote(ctx, glProject, iid, body)
	}
	if err != nil {
		_ = s.fail(fmt.Errorf("updating agent notes: %w", err))
		return
	}
	if note != nil {
		s.done(fmt.Sprintf("updated (%d bytes)", len(text)), "note", note.ID)
	} else {
		s.done(fmt.Sprintf("created (%d bytes)", len(text)))
	}
}

package runner

import (
	"context"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// The companion's push and draft merge request
// (docs/specs/cross-repo-changes.spec.md, decision 6). The home branch is
// pushed first; a run with a companion change then pushes the companion
// branch, opens its draft merge request once (later runs find it again) and
// links the two merge requests both ways inside the runner's description
// block. A run that ends without a companion change closes the merge request
// and deletes its branch. status.run.companion records it all.

// linkMarker ends the runner's link lines at the head of the description
// block ("Companion: …", "Lands after: …"), so they are found again and
// survive the describer's rewrites. Agent prose cannot forge it: prose
// escapes "<!--".
const linkMarker = "<!-- ai-operator:link -->"

// withLinks puts links at the head of a description block's body, in place
// of the link lines it had.
func withLinks(body string, links ...string) string {
	body = stripLinks(body)
	var head []string
	for _, l := range links {
		if l = strings.TrimSpace(l); l != "" {
			head = append(head, l+" "+linkMarker)
		}
	}
	if len(head) == 0 {
		return body
	}
	if body == "" {
		return strings.Join(head, "\n\n")
	}
	return strings.Join(head, "\n\n") + "\n\n" + body
}

// stripLinks removes the runner's link lines from a block's body.
func stripLinks(body string) string {
	lines := strings.Split(body, "\n")
	out := lines[:0]
	for _, l := range lines {
		if !strings.HasSuffix(strings.TrimSpace(l), linkMarker) {
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// companionLink is the home merge request's line naming the companion's.
func companionLink(project string, iid int64) string {
	return fmt.Sprintf("Companion: %s!%d", project, iid)
}

// landsAfterLink is the companion merge request's line naming the home
// merge request it waits for.
func landsAfterLink(homeProject string, homeIID int64) string {
	return fmt.Sprintf("Lands after: %s!%d is merged and live.", homeProject, homeIID)
}

// companionTitle is the companion merge request's title, without the draft
// prefix.
func companionTitle(homeTitle, homeProject string, issueIID int64) string {
	return fmt.Sprintf("%s (companion of %s#%d)", homeTitle, homeProject, issueIID)
}

// companionRef is "<project>!<iid>", how GitLab links a merge request of
// another project.
func companionRef(project string, iid int64) string {
	return fmt.Sprintf("%s!%d", project, iid)
}

// companionHead is the companion branch head this run ends with: the one it
// imported, else the remote branch an earlier run pushed; "" without either.
// remote is the remote branch head.
func (c *companionTree) companionHead(ctx context.Context) (head, remote string) {
	remote, _ = c.mirror.resolve(ctx, "refs/remotes/origin/"+c.spec.Branch)
	if c.head != "" {
		return c.head, remote
	}
	return remote, remote
}

// companionGL is the GitLab client for the companion project, nil when the
// pod has no companion token.
func (run *implementRun) companionGL() gitlab.API {
	if run.r.CompanionToken == "" {
		return nil
	}
	return run.r.companionGitLab(run.spec, run.task)
}

// pushCompanion runs after the home push. With a companion change it pushes
// the companion branch (a normal push: its CI runs), finds or opens its draft
// merge request and links the home merge request to it. Without one it
// removes what an earlier run left (removeCompanion). A failed push or a
// merge request GitLab refuses fails the run: the home branch is pushed
// already, and the next run tries both again.
func (run *implementRun) pushCompanion(ctx context.Context) error {
	r, c, prev := run.r, run.companion, run.task.Status.Run.Companion
	if prev != nil && (c == nil || aiv1.NormalizeProject(prev.Project) != aiv1.NormalizeProject(c.spec.Project)) {
		run.removeCompanion(ctx, *prev, "the issue has no companion repository in it any more")
	}
	if c == nil {
		return nil
	}
	head, remote := c.companionHead(ctx)
	if head == "" || !c.mirror.HasChanges(ctx, c.base, head) {
		if prev != nil && aiv1.NormalizeProject(prev.Project) == aiv1.NormalizeProject(c.spec.Project) {
			run.removeCompanion(ctx, *prev, "this run ends without a change in it")
		}
		return nil
	}
	rec := aiv1.CompanionRun{Project: c.spec.Project, Branch: c.spec.Branch}
	if prev != nil && aiv1.NormalizeProject(prev.Project) == aiv1.NormalizeProject(c.spec.Project) {
		rec = *prev
		rec.Branch = c.spec.Branch
	}
	if head != remote {
		s := r.step(ctx, "push companion", "repo", c.slug, "branch", c.spec.Branch)
		// Only the head checkCompanion passed is pushed; every home push
		// checked the companion before it.
		if head != c.checked {
			return s.fail(fmt.Errorf("companion %s: %s at %s was not checked; nothing of it is pushed", c.spec.Project, c.spec.Branch, shortSHA(head)))
		}
		if err := c.mirror.Push(ctx, c.spec.Branch, false); err != nil {
			return s.fail(fmt.Errorf("pushing the companion branch %s of %s failed after the home branch was pushed; the next run pushes both again: %w",
				c.spec.Branch, c.spec.Project, err))
		}
		s.done(fmt.Sprintf("%s at %s", c.spec.Branch, shortSHA(head)), "sha", head)
		run.out.CompanionPushed = true
		if run.out.PushedSHA == "" {
			// A companion-only push counts as a fix round like a home one
			// (the rounds are shared); with a home push it is set already.
			run.out.CIRound = ciRound(run.task, run.ciFix)
		}
	}
	rec.PushedSHA = head
	if run.out.ReviewedSHA != "" && run.reviewedCompanion {
		// The last review saw both trees as they are pushed now: the
		// companion head with the home ReviewedSHA. A run without a review
		// keeps the earlier one, so a new head stays unreviewed.
		rec.ReviewedSHA = head
	}
	gl := run.companionGL()
	mr, opened, err := run.companionMergeRequest(ctx, gl, c)
	if err != nil {
		// The push is recorded; the next run opens the merge request.
		rec.MRIID, rec.MRURL = 0, ""
		run.recordCompanion(ctx, &rec)
		return err
	}
	rec.MRIID, rec.MRURL = mr.IID, mr.WebURL
	if err := run.recordCompanion(ctx, &rec); err != nil {
		return err
	}
	run.companionMR = mr
	run.homeLink = companionLink(c.spec.Project, mr.IID)
	run.linkHome(ctx)
	if opened {
		run.notices = append(run.notices, fmt.Sprintf("🔗 Opened the companion merge request %s as a draft. It stays a draft until this merge request is merged and live.",
			companionRef(c.spec.Project, mr.IID)))
	}
	return nil
}

// recordCompanion writes status.run.companion (nil clears it), also into
// the run's copy of the task.
func (run *implementRun) recordCompanion(ctx context.Context, rec *aiv1.CompanionRun) error {
	if err := run.r.patchRun(ctx, func(s *aiv1.RunStatus) { s.Companion = rec }); err != nil {
		return fmt.Errorf("recording the companion in the task status: %w", err)
	}
	run.task.Status.Run.Companion = rec
	return nil
}

// companionMergeRequest finds the companion branch's open merge request, or
// opens it as a draft. A found one a human marked ready goes back to draft:
// the operator marks it ready when it lands, never before (D157-P2).
func (run *implementRun) companionMergeRequest(ctx context.Context, gl gitlab.API, c *companionTree) (*gitlab.MergeRequest, bool, error) {
	r, project := run.r, c.spec.Project
	s := r.step(ctx, "find companion merge request", "project", project, "branch", c.spec.Branch)
	if gl == nil {
		return nil, false, s.fail(fmt.Errorf("companion %s: this pod has no companion token", project))
	}
	mr, err := gl.FindOpenMergeRequest(ctx, project, c.spec.Branch)
	if err != nil {
		return nil, false, s.fail(fmt.Errorf("looking up the companion merge request in %s: %w", project, err))
	}
	if mr != nil {
		s.done(companionRef(project, mr.IID), "mr", mr.IID)
		if !mr.Draft {
			s = r.step(ctx, "mark companion draft", "mr", mr.IID)
			if res, err := gl.UpdateMergeRequest(ctx, project, mr.IID, gitlab.UpdateMergeRequestOptions{Title: gitlab.DraftTitle(mr.Title)}); err != nil {
				_ = s.fail(fmt.Errorf("marking %s draft: %w", companionRef(project, mr.IID), err))
			} else {
				mr.Draft, mr.Title = res.Draft, res.Title
				s.done(fmt.Sprintf("%s %q", companionRef(project, mr.IID), res.Title))
				run.notices = append(run.notices, fmt.Sprintf("The companion merge request %s was ready and is a draft again: it is marked ready when it can land.",
					companionRef(project, mr.IID)))
			}
		}
		return mr, false, nil
	}
	s.done("none yet")
	s = r.step(ctx, "open companion merge request", "project", project, "branch", c.spec.Branch, "target", c.base)
	mr, err = gl.CreateMergeRequest(ctx, project, gitlab.CreateMergeRequestOptions{
		SourceBranch:       c.spec.Branch,
		TargetBranch:       c.base,
		Title:              "Draft: " + companionTitle(run.issue.Title, run.glProject, run.issue.IID),
		Description:        descriptionBlock(withLinks(descriptionPlaceholder, landsAfterLink(run.glProject, run.mr.IID))),
		RemoveSourceBranch: true,
		Labels:             aiv1.CompanionMRLabel,
	})
	if err != nil {
		return nil, false, s.fail(fmt.Errorf("opening the companion merge request in %s: %w", project, err))
	}
	s.done(companionRef(project, mr.IID), "mr", mr.IID, "url", mr.WebURL)
	return mr, true, nil
}

// linkHome writes the home merge request's link line (run.homeLink, none
// when it is empty) into the runner's block of its description. A failure
// is a notice: the describer's next rewrite writes the line too.
func (run *implementRun) linkHome(ctx context.Context) {
	r, gl := run.r, run.gl
	s := r.step(ctx, "link merge requests", "mr", run.mr.IID)
	mr, err := gl.GetMergeRequest(ctx, run.glProject, run.mr.IID)
	if err != nil {
		s.done("not linked: reading the merge request failed", "error", err.Error())
		run.notices = append(run.notices, "The companion line of the merge request description was not updated: reading the merge request failed.")
		return
	}
	body, ok := managedDescription(mr.Description)
	if !ok {
		body = descriptionPlaceholder
	}
	next := withDescription(mr.Description, withLinks(body, run.homeLink), run.issue.IID)
	if next == mr.Description {
		s.done("up to date")
		return
	}
	if _, err := gl.UpdateMergeRequest(ctx, run.glProject, run.mr.IID, gitlab.UpdateMergeRequestOptions{Description: next}); err != nil {
		s.done("not linked: GitLab refused it", "error", err.Error())
		run.notices = append(run.notices, "The companion line of the merge request description was not updated: GitLab refused it.")
		return
	}
	if run.homeLink == "" {
		s.done("companion line removed")
	} else {
		s.done(run.homeLink)
	}
}

// removeCompanion closes what an earlier run left in the companion project
// once a run ends without a companion change: it closes the merge request,
// deletes the branch, removes the home merge request's companion line and
// clears status.run.companion, and says so in the report. It never fails
// the run. A transient GitLab error keeps the record for the next run;
// anything else (no token, the guard, a merge request merged by hand) clears
// it and asks for a hand.
func (run *implementRun) removeCompanion(ctx context.Context, prev aiv1.CompanionRun, why string) {
	r := run.r
	ref := prev.Project
	if prev.MRIID != 0 {
		ref = companionRef(prev.Project, prev.MRIID)
	}
	s := r.step(ctx, "close companion", "project", prev.Project, "mr", prev.MRIID, "branch", prev.Branch)
	clear := func() bool {
		if err := run.recordCompanion(ctx, nil); err != nil {
			_ = s.fail(err)
			return false
		}
		run.state.DescribedCompanionDiff = ""
		if err := r.saveState(*run.state); err != nil {
			r.log().Error(err, "saving the state after removing the companion")
		}
		run.homeLink = ""
		run.linkHome(ctx)
		return true
	}
	byHand := func(what string, err error) {
		if gitlab.IsTransient(err) {
			// Kept: the next run tries again.
			if prev.MRIID != 0 {
				run.homeLink = companionLink(prev.Project, prev.MRIID)
			}
			s.done("not closed, tried again with the next run: "+what, "error", err.Error())
			run.notices = append(run.notices, fmt.Sprintf("The companion %s was not closed (%s); the next run tries again.", ref, what))
			return
		}
		if clear() {
			msg := "not closed: " + what
			if err != nil {
				msg += ": " + textutil.Truncate(err.Error(), 300)
			}
			s.done(msg)
			run.notices = append(run.notices, fmt.Sprintf("The companion %s is no longer part of this issue (%s), but it was not closed: %s. Close it and delete its branch `%s` by hand.",
				ref, why, what, prev.Branch))
		}
	}
	gl := run.companionGL()
	if gl == nil {
		byHand("this pod has no token for the companion project", nil)
		return
	}
	closed := false
	if prev.MRIID != 0 {
		mr, err := gl.GetMergeRequest(ctx, prev.Project, prev.MRIID)
		if err != nil {
			byHand("reading its merge request failed", err)
			return
		}
		switch mr.State {
		case "merged":
			// Merged by hand: nothing to close, and its branch is gone or
			// belongs to the merge.
			if clear() {
				s.done(ref + " was merged already")
				run.notices = append(run.notices, fmt.Sprintf("The companion merge request %s is no longer part of this issue (%s), but it was merged already, so it was left as it is.", ref, why))
			}
			return
		case "opened":
			if _, err := gl.UpdateMergeRequest(ctx, prev.Project, prev.MRIID, gitlab.UpdateMergeRequestOptions{StateEvent: "close"}); err != nil {
				byHand("closing its merge request failed", err)
				return
			}
			closed = true
		}
	}
	if prev.Branch != "" {
		if err := gl.DeleteBranch(ctx, prev.Project, prev.Branch); err != nil && gitlab.StatusCode(err) != 404 {
			byHand("deleting its branch failed", err)
			return
		}
	}
	if !clear() {
		return
	}
	what := "its branch was deleted"
	if closed {
		what = "its merge request was closed and its branch deleted"
	}
	s.done(fmt.Sprintf("%s: %s", ref, what), "closed", closed)
	run.notices = append(run.notices, fmt.Sprintf("🗑️ The companion %s is no longer part of this issue (%s): %s.", ref, why, what))
}

// describeCompanion writes the companion merge request's description: the
// describer's text for the companion diff with the "Lands after" line.
func (run *implementRun) describeCompanion(ctx context.Context) (string, error) {
	c, mr := run.companion, run.companionMR
	if c == nil || mr == nil {
		return "", nil
	}
	gl := run.companionGL()
	if gl == nil {
		return "", nil
	}
	_, notice, err := run.r.describe(ctx, run.spec.Agents.Describe, run.issue, run.answer.Summary, describeTarget{
		gl: gl, project: c.spec.Project, mrIID: mr.IID, repo: c.slug, base: c.base,
		described: &run.state.DescribedCompanionDiff, links: []string{landsAfterLink(run.glProject, run.mr.IID)},
		companionOf: companionRef(run.glProject, run.mr.IID),
	}, run.state, run.out)
	return notice, err
}

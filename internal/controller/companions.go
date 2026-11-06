package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// The landing of a companion merge request after its home merge
// (docs/specs/cross-repo-changes.spec.md, decision 8). The AgentProject's
// status.companions tracks each one from the home merge on, since the home
// issue's AgentTask is deleted once the issue closes:
//
//	waitingRelease -> the home change is live (docs-only, the operator's own
//	                  release, a tag, or merged is enough)
//	rebasing       -> GitLab rebases it, once per entry
//	waitingCI      -> a green pipeline on its head, and mergeable
//	ready          -> the end state here; who merges is decided elsewhere
//	blocked        -> a human must act; autopilot pauses with kind companion
//	merged, closed -> by hand at any point; removed CompanionRetention later
//
// No model decides any of it, and nothing here merges.

// tagWindow is how recent a tag on a merge commit must be for a project to
// count as one that tags its default branch.
const tagWindow = 30 * 24 * time.Hour

// syncCompanions records the companions whose home merge request was merged
// and checks each one that has not ended, once per poll. tasks are all the
// project's tasks of this poll, also those of closed issues that are deleted
// after it; homeMRs are the home merge requests the issue sync read, by task
// name. A task whose home merge could not be checked is kept (keep), so it
// is not deleted before its companion is recorded.
func (r *AgentProjectReconciler) syncCompanions(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	tasks []aiv1.AgentTask, homeMRs map[string]*gitlab.MergeRequest, autopilots map[int64]*aiv1.AgentTask, keep map[string]bool) error {
	logger := log.FromContext(ctx)
	st := &project.Status
	var errs []error
	for i := range tasks {
		t := &tasks[i]
		run := t.Status.Run
		c := run.Companion
		if t.Spec.Type != aiv1.TaskTypeImplement || t.Spec.Issue == nil || c == nil || c.MRIID == 0 || run.MergeRequestIID == 0 ||
			findCompanion(st.Companions, c.Project, c.MRIID) != nil {
			continue
		}
		mr, ok := homeMRs[t.Name]
		if !ok || mr == nil {
			var err error
			if mr, err = gl.GetMergeRequest(ctx, spec.GitLab.Project, run.MergeRequestIID); err != nil {
				if gitlab.IsNotFound(err) {
					continue
				}
				keep[t.Name] = true
				errs = append(errs, fmt.Errorf("issue #%d: getting merge request !%d: %w", t.Spec.Issue.IID, run.MergeRequestIID, err))
				continue
			}
		}
		if mr.State != "merged" {
			continue
		}
		e, err := r.newCompanion(ctx, gl, spec, t, mr)
		if err != nil {
			keep[t.Name] = true
			errs = append(errs, fmt.Errorf("issue #%d: recording its companion %s: %w", t.Spec.Issue.IID, companionRef(c), err))
			continue
		}
		logger.Info("home merge request merged, the companion waits to land", "issue", e.HomeIssueIID, "mr", e.HomeMRIID,
			"companion", companionRef(c), "sha", e.HomeMergeSHA, "docsOnly", e.DocsOnly)
		st.Companions = append(st.Companions, e)
	}
	now := r.now()
	kept := st.Companions[:0]
	for _, e := range st.Companions {
		if e.State.Ended() && e.EndedAt != nil && now.Sub(e.EndedAt.Time) >= aiv1.CompanionRetention {
			logger.Info("companion entry removed", "companion", landingRef(&e), "state", e.State)
			continue
		}
		kept = append(kept, e)
	}
	st.Companions = kept
	for i := range st.Companions {
		e := &st.Companions[i]
		if e.State.Ended() {
			continue
		}
		was := e.State
		if err := r.landCompanion(ctx, gl, spec, e); err != nil {
			errs = append(errs, fmt.Errorf("companion %s: %w", landingRef(e), err))
			continue
		}
		if e.State != was {
			logger.Info("companion landing", "companion", landingRef(e), "issue", e.HomeIssueIID, "from", was, "to", e.State, "reason", e.Reason)
		}
		if err := r.noticeBlocked(ctx, gl, spec, e, autopilots); err != nil {
			errs = append(errs, fmt.Errorf("companion %s: %w", landingRef(e), err))
		}
	}
	return errors.Join(errs...)
}

// newCompanion is the entry of task's companion after its home merge
// request mr was merged.
func (r *AgentProjectReconciler) newCompanion(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, t *aiv1.AgentTask, mr *gitlab.MergeRequest) (aiv1.CompanionStatus, error) {
	c := t.Status.Run.Companion
	now := metav1.NewTime(r.now())
	e := aiv1.CompanionStatus{
		HomeIssueIID: t.Spec.Issue.IID, HomeMRIID: mr.IID,
		HomeMergeSHA: firstNonEmpty(mr.MergeCommitSHA, mr.SquashCommitSHA, mr.SHA),
		Project:      c.Project, MRIID: c.MRIID, MRURL: c.MRURL,
		HeadSHA: c.PushedSHA, HeadSince: &now,
		State: aiv1.LandingWaitingRelease, Owners: slices.Clone(t.Spec.Owners),
	}
	if m := t.Spec.Milestone; m != nil {
		e.MilestoneIID, e.MilestoneTitle = m.IID, m.Title
	}
	if sc := t.Spec.Companion; sc != nil && aiv1.NormalizeProject(sc.Project) == aiv1.NormalizeProject(c.Project) {
		e.AutoMerge = sc.AutoMerge
	}
	diffs, err := gl.ListMergeRequestDiffs(ctx, spec.GitLab.Project, mr.IID)
	if err != nil {
		return e, fmt.Errorf("listing the diffs of !%d: %w", mr.IID, err)
	}
	e.DocsOnly = docsOnly(diffs)
	cmr, err := gl.GetMergeRequest(ctx, c.Project, c.MRIID)
	switch {
	case gitlab.IsNotFound(err):
		return e, nil // landCompanion records it as gone
	case err != nil:
		return e, fmt.Errorf("getting the companion merge request: %w", err)
	}
	e.MRURL = cmp.Or(e.MRURL, cmr.WebURL)
	if e.HeadSHA == "" {
		e.HeadSHA = cmr.SHA
	}
	// The change the landing holds the companion to: the agent's pushed
	// head's. A head someone else pushed meanwhile has none, so it blocks.
	if cmr.State == "opened" && cmr.SHA == e.HeadSHA {
		if e.Diff, err = diffHash(ctx, gl, c.Project, c.MRIID); err != nil {
			return e, err
		}
	}
	return e, nil
}

// landCompanion checks one entry that has not ended and moves it on.
func (r *AgentProjectReconciler) landCompanion(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus) error {
	now := metav1.NewTime(r.now())
	ref := landingRef(e)
	mr, err := gl.GetMergeRequest(ctx, e.Project, e.MRIID)
	switch {
	case gitlab.IsNotFound(err):
		e.State, e.Reason, e.EndedAt = aiv1.LandingClosed, fmt.Sprintf("the companion merge request %s is gone", ref), &now
		return nil
	case err != nil:
		return fmt.Errorf("getting the companion merge request: %w", err)
	}
	switch mr.State {
	case "merged":
		e.State, e.Reason, e.EndedAt = aiv1.LandingMerged, "", &now
		e.MergeSHA = firstNonEmpty(mr.MergeCommitSHA, mr.SquashCommitSHA, mr.SHA)
		return nil
	case "closed":
		e.State, e.Reason, e.EndedAt = aiv1.LandingClosed, "closed without a merge", &now
		return nil
	}
	e.MRURL = cmp.Or(e.MRURL, mr.WebURL)
	if e.State == aiv1.LandingReady || e.State == aiv1.LandingMerging {
		return nil // only a merge or close by hand moves it on here
	}
	set := func(s aiv1.LandingState, format string, args ...any) error {
		e.State, e.Reason = s, fmt.Sprintf(format, args...)
		return nil
	}
	blocked := func(format string, args ...any) error { return set(aiv1.LandingBlocked, format, args...) }

	if e.State == aiv1.LandingWaitingRelease {
		live, release, why, err := r.homeLive(ctx, gl, spec, e)
		if err != nil {
			return err
		}
		if !live {
			e.Reason = why
			return nil
		}
		e.Release = release
		log.FromContext(ctx).Info("the home change is live", "companion", ref, "issue", e.HomeIssueIID, "release", release, "docsOnly", e.DocsOnly)
		e.State, e.Reason = aiv1.LandingWaitingCI, ""
	}

	if mr.RebaseInProgress {
		return set(aiv1.LandingRebasing, "GitLab rebases %s", ref)
	}
	if mr.SHA != e.HeadSHA {
		// A new head (the rebase, or someone's) is taken over only if its
		// change is the one recorded at the home merge.
		diff, err := diffHash(ctx, gl, e.Project, e.MRIID)
		if err != nil {
			return err
		}
		if e.Diff == "" || diff != e.Diff {
			return blocked("the head of the companion merge request %s (%s) does not carry the change it had at the home merge (someone pushed to its branch, "+
				"or a rebase changed lines); check it and merge it by hand, or close it", ref, short(mr.SHA))
		}
		log.FromContext(ctx).Info("companion head taken over, its change is the same", "companion", ref, "from", e.HeadSHA, "to", mr.SHA)
		e.HeadSHA, e.HeadSince = mr.SHA, &now
	}

	status := mr.DetailedMergeStatus
	if status == "draft_status" && mr.HasConflicts {
		status = "conflict" // a draft shows no other status
	}
	switch status {
	case "need_rebase", "conflict":
		switch {
		case e.RebasedSHA == "":
			e.RebasedSHA, e.RebasedAt = mr.SHA, &now
			if err := gl.RebaseMergeRequest(ctx, e.Project, e.MRIID); err != nil {
				if gitlab.IsTransient(err) {
					e.RebasedSHA, e.RebasedAt = "", nil // tried again on the next poll
					return fmt.Errorf("rebasing: %w", err)
				}
				return blocked("GitLab could not rebase the companion merge request %s (%v); rebase it by hand", ref, err)
			}
			log.FromContext(ctx).Info("companion rebased", "companion", ref, "from", mr.SHA, "status", status)
			return set(aiv1.LandingRebasing, "GitLab rebases %s onto its default branch", ref)
		case mr.SHA == e.RebasedSHA && mr.MergeError != "":
			return blocked("the rebase of the companion merge request %s failed: %s", ref, mr.MergeError)
		case mr.SHA == e.RebasedSHA && status == "need_rebase" && e.RebasedAt != nil && now.Sub(e.RebasedAt.Time) < rebaseGrace:
			return set(aiv1.LandingRebasing, "GitLab rebases %s onto its default branch", ref) // GitLab refreshes the MR after the rebase
		case status == "conflict":
			return blocked("the companion merge request %s has merge conflicts with its default branch, and the one rebase did not resolve them; resolve them on its branch", ref)
		default:
			return blocked("the companion merge request %s still needs a rebase after one; rebase it by hand", ref)
		}
	case "not_approved":
		return blocked("the companion merge request %s needs an approval", ref)
	case "discussions_not_resolved":
		return blocked("the companion merge request %s has unresolved discussions", ref)
	case "mergeable", "draft_status", "checking", "unchecked", "preparing", "approvals_syncing", "ci_still_running", "ci_must_pass":
	default:
		return blocked("GitLab reports the companion merge request %s as %q", ref, mr.DetailedMergeStatus)
	}

	// A green pipeline on the head; none within ci.noPipelineTimeout is a
	// companion without CI, like in the CI loop.
	var pipelines []gitlab.Pipeline
	if mr.HeadPipeline != nil && mr.HeadPipeline.SHA == mr.SHA {
		pipelines = []gitlab.Pipeline{*mr.HeadPipeline}
	} else if pipelines, err = gl.ListPipelines(ctx, e.Project, gitlab.ListPipelinesOptions{SHA: mr.SHA, Limit: 1}); err != nil {
		return fmt.Errorf("listing the pipelines of %s: %w", short(mr.SHA), err)
	}
	pipelines = slices.DeleteFunc(pipelines, func(p gitlab.Pipeline) bool { return p.Status == "skipped" })
	switch {
	case len(pipelines) == 0 && e.HeadSince != nil && now.Sub(e.HeadSince.Time) >= spec.CI.NoPipelineTimeout.Duration:
		// no CI for it
	case len(pipelines) == 0:
		return set(aiv1.LandingWaitingCI, "waiting for a pipeline on the head %s of %s", short(mr.SHA), ref)
	case pipelines[0].Status == "success" || pipelines[0].Status == "manual":
	case pipelines[0].Status == "failed" || pipelines[0].Status == "canceled":
		p := pipelines[0]
		return blocked("the pipeline %s of the companion merge request %s is %s on its head %s; retry or fix it", p.WebURL, ref, p.Status, short(mr.SHA))
	default:
		return set(aiv1.LandingWaitingCI, "the pipeline of %s on its head %s is %s", ref, short(mr.SHA), pipelines[0].Status)
	}
	if status != "mergeable" && status != "draft_status" {
		return set(aiv1.LandingWaitingCI, "GitLab reports %s as %q", ref, mr.DetailedMergeStatus)
	}
	e.State, e.Reason, e.ReadyAt = aiv1.LandingReady, "", &now
	return nil
}

// homeLive reports whether the home change of e is live, the release that
// made it so, and else why not:
//   - a home merge that touches only docs/** and *.md is live at merge;
//   - the operator's own project (--self-project): the running operator is
//     at least the first release that contains the merge;
//   - a project that tags its default branch: a tag contains the merge;
//   - any other project: merged is enough.
func (r *AgentProjectReconciler) homeLive(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus) (live bool, release, why string, err error) {
	if e.DocsOnly {
		return true, "", "", nil
	}
	home := spec.GitLab.Project
	sha := e.HomeMergeSHA
	if r.SelfProject != "" && aiv1.NormalizeProject(home) == r.SelfProject {
		running, ok := parseRelease(r.Version)
		if !ok {
			return false, "", fmt.Sprintf("the running operator's version %q is no release (YEAR.MONTH.N), so it never runs the merge %s", r.Version, short(sha)), nil
		}
		refs, err := gl.ListCommitRefs(ctx, home, sha, "tag")
		if err != nil {
			return false, "", "", fmt.Errorf("listing the tags that contain %s: %w", short(sha), err)
		}
		first, ok := firstRelease(refs)
		switch {
		case !ok:
			return false, "", fmt.Sprintf("no release contains the merge %s yet", short(sha)), nil
		case compareReleases(running, first) < 0:
			return false, "", fmt.Sprintf("the operator runs %s; the merge %s is first in %s", r.Version, short(sha), releaseName(first)), nil
		}
		return true, releaseName(first), "", nil
	}
	tagging, err := r.tagsDefaultBranch(ctx, gl, home)
	switch {
	case err != nil:
		return false, "", "", err
	case !tagging:
		return true, "", "", nil // merged is enough
	}
	refs, err := gl.ListCommitRefs(ctx, home, sha, "tag")
	if err != nil {
		return false, "", "", fmt.Errorf("listing the tags that contain %s: %w", short(sha), err)
	}
	if len(refs) == 0 {
		return false, "", fmt.Sprintf("%s tags its default branch, and no tag contains the merge %s yet", home, short(sha)), nil
	}
	if first, ok := firstRelease(refs); ok {
		return true, releaseName(first), "", nil
	}
	return true, refs[0].Name, "", nil
}

// tagsDefaultBranch reports whether project tags its default branch: one
// of its newest tags points to a merge commit created within tagWindow.
func (r *AgentProjectReconciler) tagsDefaultBranch(ctx context.Context, gl gitlab.API, project string) (bool, error) {
	tags, err := gl.ListTags(ctx, project, gitlab.ListTagsOptions{Limit: 20})
	if err != nil {
		return false, fmt.Errorf("listing the tags of %s: %w", project, err)
	}
	now := r.now()
	return slices.ContainsFunc(tags, func(t gitlab.Tag) bool {
		c := t.Commit
		return len(c.ParentIDs) > 1 && c.CreatedAt != nil && now.Sub(*c.CreatedAt) <= tagWindow
	}), nil
}

// docsOnly reports whether a change touches only docs/** and *.md files
// (both sides of a rename). No change is not docs-only.
func docsOnly(diffs []gitlab.Diff) bool {
	doc := func(p string) bool { return strings.HasPrefix(p, "docs/") || strings.HasSuffix(p, ".md") }
	return len(diffs) > 0 && !slices.ContainsFunc(diffs, func(d gitlab.Diff) bool { return !doc(d.OldPath) || !doc(d.NewPath) })
}

// release is a calendar version, YEAR.MONTH.N.
type release [3]int

// parseRelease parses a calendar version: three dot-separated numbers.
func parseRelease(s string) (release, bool) {
	var out release
	parts := strings.Split(s, ".")
	if len(parts) != len(out) {
		return out, false
	}
	for i, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return out, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// compareReleases orders releases segment by segment, numerically.
func compareReleases(a, b release) int {
	return slices.Compare(a[:], b[:])
}

func releaseName(v release) string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// firstRelease is the smallest calendar version among the tags of refs;
// other tags are ignored.
func firstRelease(refs []gitlab.CommitRef) (release, bool) {
	var first release
	found := false
	for _, ref := range refs {
		v, ok := parseRelease(ref.Name)
		if ref.Type == "tag" && ok && (!found || compareReleases(v, first) < 0) {
			first, found = v, true
		}
	}
	return first, found
}

// noticeBlocked tells the companion merge request, once per block, why it
// cannot land and mentions the owners, unless the home milestone is on
// autopilot (its pause says so on the tracking issue).
func (r *AgentProjectReconciler) noticeBlocked(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus, autopilots map[int64]*aiv1.AgentTask) error {
	if e.State != aiv1.LandingBlocked {
		e.BlockedNotice = ""
		return nil
	}
	if e.BlockedNotice == e.Reason {
		return nil
	}
	if st := autopilots[e.MilestoneIID]; e.MilestoneIID != 0 && st != nil && st.Spec.Autopilot {
		return nil
	}
	body := fmt.Sprintf("🤖 This companion of %s#%d (merged in !%d) cannot land: %s.", spec.GitLab.Project, e.HomeIssueIID, e.HomeMRIID, notify.Defuse(e.Reason)) +
		notify.Mention(e.Owners)
	if _, err := gl.CreateMergeRequestNote(ctx, e.Project, e.MRIID, body); err != nil {
		return fmt.Errorf("commenting on the blocked companion: %w", err)
	}
	e.BlockedNotice = e.Reason
	return nil
}

// findCompanion returns the entry of a companion merge request, or nil.
func findCompanion(entries []aiv1.CompanionStatus, project string, mrIID int64) *aiv1.CompanionStatus {
	p := aiv1.NormalizeProject(project)
	for i := range entries {
		if entries[i].MRIID == mrIID && aiv1.NormalizeProject(entries[i].Project) == p {
			return &entries[i]
		}
	}
	return nil
}

// landingRef names an entry's merge request, "group/infra!3".
func landingRef(e *aiv1.CompanionStatus) string {
	return fmt.Sprintf("%s!%d", e.Project, e.MRIID)
}

// blockedLanding is the first blocked companion of the milestone, or nil.
func (a *autopilot) blockedLanding() *aiv1.CompanionStatus {
	for i := range a.project.Status.Companions {
		if e := &a.project.Status.Companions[i]; e.MilestoneIID == a.st.Spec.Milestone.IID && e.State == aiv1.LandingBlocked {
			return e
		}
	}
	return nil
}

// landingPause reports whether the autopilot is paused over a companion's
// landing (kind companion, about no active issue).
func (a *autopilot) landingPause() bool {
	return a.ap.Phase == aiv1.AutopilotPaused && a.ap.PauseKind == aiv1.PauseCompanion && a.ap.PausedFor == 0
}

func landingPauseReason(e *aiv1.CompanionStatus) string {
	return fmt.Sprintf("the companion %s of #%d cannot land: %s", landingRef(e), e.HomeIssueIID, e.Reason)
}

// companionLanding pauses the milestone with kind companion while one of
// its companions is blocked (once per reason), and lifts that pause once
// none is: rebased, fixed, merged or closed by hand.
func (a *autopilot) companionLanding(ctx context.Context) error {
	e := a.blockedLanding()
	switch {
	case e == nil && a.landingPause():
		a.log.Info("autopilot continues: no companion of the milestone is blocked any more")
		if err := a.comment(ctx, "▶️ Autopilot continues: the companions of the milestone can land again (or were merged or closed by hand)."); err != nil {
			return err
		}
		a.lift(aiv1.ResolvedByOperator)
	case e != nil && (a.ap.Phase == aiv1.AutopilotRunning || a.landingPause()):
		return a.pause(ctx, aiv1.PauseCompanion, 0, landingPauseReason(e))
	}
	return nil
}

// retryLandings lets the milestone's blocked companions be checked again,
// with one more rebase: an owner resumed the landing pause.
func (a *autopilot) retryLandings(by string) {
	for i := range a.project.Status.Companions {
		e := &a.project.Status.Companions[i]
		if e.MilestoneIID != a.st.Spec.Milestone.IID || e.State != aiv1.LandingBlocked {
			continue
		}
		a.log.Info("companion checked again after a resume", "companion", landingRef(e), "by", by)
		e.State, e.Reason = aiv1.LandingWaitingCI, "checked again after a resume by "+by
		e.RebasedSHA, e.RebasedAt = "", nil
	}
}

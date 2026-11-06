package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// Who merges a ready companion (docs/specs/cross-repo-changes.spec.md,
// decision 9). Autopilot merges it only when every switch holds when it is
// about to (mergeSwitches): the entry's autoMerge from the home task, the
// milestone's "/agent repo <project> merge" line, its autopilot marker, and
// a valid target with autoMerge. The merge/push guard checks the last one
// again, so it never refuses in normal operation. Otherwise the owners are
// asked once, and reminded once after companionReminder.

// companionReminder is how long after the owners were asked to merge a ready
// companion they are reminded, once.
const companionReminder = 7 * 24 * time.Hour

// maxMergeRefusals is how many merges of autopilot GitLab may refuse before
// the entry is blocked; the first sends it back to the ready checks.
const maxMergeRefusals = 2

// companionOwners are the owners a companion's comments mention: the
// milestone's (its spec task's), else the home task's, else the project's
// notify users or the default owners.
func (r *AgentProjectReconciler) companionOwners(spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus, specTasks map[int64]*aiv1.AgentTask) []string {
	if st := specTasks[e.MilestoneIID]; e.MilestoneIID != 0 && st != nil && len(st.Spec.Owners) > 0 {
		return st.Spec.Owners
	}
	if len(e.Owners) > 0 {
		return e.Owners
	}
	return notify.Resolve("", spec.Workflow.CommandPrefix, spec.Notify.Users, r.DefaultOwners)
}

// landReady moves on a ready or merging entry whose merge request mr is
// open: autopilot merges it with all its switches, else the owners are
// asked.
func (r *AgentProjectReconciler) landReady(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus, mr *gitlab.MergeRequest, owners []string) error {
	if e.MergeStartedAt != nil {
		// GitLab accepted the merge and merges asynchronously.
		if r.now().Sub(e.MergeStartedAt.Time) < rebaseGrace {
			e.State, e.Reason = aiv1.LandingMerging, fmt.Sprintf("GitLab merges %s", landingRef(e))
			return nil
		}
		e.MergeStartedAt = nil
		return mergeRefused(ctx, e, fmt.Errorf("GitLab accepted the merge, but the merge request is still open after %s", rebaseGrace))
	}
	ok, why, err := r.mergeSwitches(ctx, gl, spec, e)
	if err != nil {
		return err
	}
	if !ok {
		if e.AutoMerge || e.State == aiv1.LandingMerging {
			log.FromContext(ctx).Info("autopilot does not merge the companion, the owners are asked", "companion", landingRef(e), "why", why)
		}
		e.State, e.AutoMerge = aiv1.LandingReady, false
		return r.askOwners(ctx, gl, spec, e, mr, owners, why, true)
	}
	return r.mergeCompanion(ctx, gl, spec, e, mr, owners)
}

// mergeSwitches reports whether autopilot may merge e now, else why not. It
// reads the milestone again: an owner who removed "merge" or the autopilot
// marker since the home merge stops the merge.
func (r *AgentProjectReconciler) mergeSwitches(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus) (bool, string, error) {
	prefix := spec.Workflow.CommandPrefix
	if !e.AutoMerge {
		return false, fmt.Sprintf("its issue was not set to have it merged by autopilot: that takes a milestone line `%s repo %s merge`, "+
			"the milestone on autopilot and the target's `autoMerge: true`", prefix, e.Project), nil
	}
	target, ok := aiv1.FindCrossRepoTarget(r.crossRepoTargets(spec), e.Project)
	switch {
	case !ok:
		return false, fmt.Sprintf("%s is no valid companion target of this project any more", e.Project), nil
	case !target.AutoMerge:
		return false, fmt.Sprintf("the companion target %s does not have `autoMerge: true`", e.Project), nil
	case e.MilestoneIID == 0:
		return false, "its home issue has no milestone", nil
	}
	m, err := r.landingMilestone(ctx, gl, spec, e)
	switch {
	case err != nil:
		return false, "", err
	case m == nil:
		return false, "its milestone is gone or closed", nil
	}
	titleRE, err := titlePattern(spec)
	if err != nil {
		return false, "", err
	}
	if _, autopilot := milestoneSelection(spec, titleRE, *m); !autopilot {
		return false, fmt.Sprintf("the milestone %q is not on autopilot any more", m.Title), nil
	}
	project := aiv1.NormalizeProject(e.Project)
	if !slices.ContainsFunc(rules.Repos(m.Description, prefix), func(l rules.RepoLine) bool {
		return l.Problem == "" && l.Project == project && l.Merge
	}) {
		return false, fmt.Sprintf("the milestone %q has no line `%s repo %s merge` any more", m.Title, prefix, e.Project), nil
	}
	return true, "", nil
}

// landingMilestone reads the milestone of e again, or nil if it is gone.
// An entry recorded without the milestone's ID finds it among the active
// milestones by IID.
func (r *AgentProjectReconciler) landingMilestone(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus) (*gitlab.Milestone, error) {
	if e.MilestoneID == 0 {
		milestones, err := gl.ListMilestones(ctx, spec.GitLab.Project)
		if err != nil {
			return nil, fmt.Errorf("listing milestones: %w", err)
		}
		for i := range milestones {
			if milestones[i].IID == e.MilestoneIID {
				e.MilestoneID = milestones[i].ID
				return &milestones[i], nil
			}
		}
		return nil, nil
	}
	m, err := gl.GetMilestone(ctx, spec.GitLab.Project, e.MilestoneID)
	switch {
	case gitlab.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading the milestone %%%d: %w", e.MilestoneIID, err)
	}
	return m, nil
}

// mergeCompanion is autopilot's merge of a ready companion: it marks the
// merge request ready, has a green pipeline on the checked head (starting
// one if there is none) and merges that head.
func (r *AgentProjectReconciler) mergeCompanion(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus, mr *gitlab.MergeRequest, owners []string) error {
	logger := log.FromContext(ctx)
	now := metav1.NewTime(r.now())
	ref := landingRef(e)
	set := func(s aiv1.LandingState, format string, args ...any) error {
		e.State, e.Reason = s, fmt.Sprintf(format, args...)
		return nil
	}
	if e.State == aiv1.LandingReady {
		draft := mr.Draft || mr.DetailedMergeStatus == "draft_status"
		if draft {
			res, err := gl.UpdateMergeRequest(ctx, e.Project, e.MRIID, gitlab.UpdateMergeRequestOptions{Title: gitlab.ReadyTitle(mr.Title)})
			switch {
			case gitlab.IsTransient(err):
				return fmt.Errorf("marking %s ready: %w", ref, err)
			case err != nil:
				e.AutoMerge = false
				return r.askOwners(ctx, gl, spec, e, mr, owners, fmt.Sprintf("it could not mark it ready (%v)", err), false)
			case res.Draft:
				e.AutoMerge = false
				return r.askOwners(ctx, gl, spec, e, mr, owners, fmt.Sprintf("it could not mark it ready (its title is %q)", res.Title), false)
			}
			logger.Info("autopilot marked the companion merge request ready", "companion", ref)
		}
		e.PipelineStartedAt = nil
		e.State, e.Reason = aiv1.LandingMerging, "autopilot merges "+ref
		if draft {
			return nil // merged on a later poll, once GitLab rechecked it
		}
	}

	if mr.Draft {
		// Marked draft again after autopilot marked it ready: someone holds
		// it, so the owners merge it.
		e.State, e.AutoMerge = aiv1.LandingReady, false
		return r.askOwners(ctx, gl, spec, e, mr, owners, "someone marked it draft again", false)
	}
	if mr.SHA != e.HeadSHA {
		// The ready checks take a rebased head over, or block on another.
		return set(aiv1.LandingWaitingCI, "the head of %s moved to %s before the merge; checking it again", ref, short(mr.SHA))
	}
	status := mr.DetailedMergeStatus
	switch status {
	case "mergeable", "draft_status", "checking", "unchecked", "preparing", "approvals_syncing", "ci_still_running", "ci_must_pass":
	default:
		// The ready checks rebase it once, or block.
		return set(aiv1.LandingWaitingCI, "GitLab reports %s as %q before the merge; checking it again", ref, status)
	}

	pipelines, err := headPipelines(ctx, gl, e.Project, mr)
	if err != nil {
		return err
	}
	switch {
	case len(pipelines) == 0 && e.PipelineStartedAt == nil:
		// Marked ready without a pipeline (the project may skip drafts):
		// one is started for the head before anything is merged.
		err := gl.CreateMergeRequestPipeline(ctx, e.Project, e.MRIID)
		switch code := gitlab.StatusCode(err); {
		case err == nil:
			logger.Info("autopilot started a pipeline for the companion's head", "companion", ref, "sha", mr.SHA)
			e.PipelineStartedAt = &now
			return set(aiv1.LandingMerging, "waiting for the pipeline autopilot started on the head %s of %s", short(mr.SHA), ref)
		case code == http.StatusBadRequest || code == http.StatusUnprocessableEntity:
			// GitLab has no merge request pipeline for it (no merge request
			// pipelines, or workflow rules filter it out). A pipeline of the
			// branch may still come; none within ci.noPipelineTimeout is no
			// CI, as in the ready checks.
			logger.Info("no merge request pipeline possible for the companion, waiting for any pipeline of its head", "companion", ref, "error", err.Error())
			e.PipelineStartedAt = &now
			return set(aiv1.LandingMerging, "GitLab starts no merge request pipeline for %s; waiting for a pipeline on its head %s", ref, short(mr.SHA))
		case code == http.StatusUnauthorized || code == http.StatusForbidden:
			// The bot may not run CI there: that is no evidence the project
			// has none, so it is not merged without CI.
			e.State, e.AutoMerge = aiv1.LandingReady, false
			return r.askOwners(ctx, gl, spec, e, mr, owners, fmt.Sprintf("it may not start a pipeline for its head (HTTP %d)", code), false)
		default:
			// A rate limit, a GitLab error or anything else: tried again on
			// the next poll.
			return fmt.Errorf("starting a pipeline for %s: %w", ref, err)
		}
	case len(pipelines) == 0 && now.Sub(e.PipelineStartedAt.Time) < spec.CI.NoPipelineTimeout.Duration:
		return set(aiv1.LandingMerging, "waiting for the pipeline autopilot started on the head %s of %s", short(mr.SHA), ref)
	case len(pipelines) == 0:
		// None came: no CI for it.
	case pipelines[0].Status == "success" || pipelines[0].Status == "manual":
	case pipelines[0].Status == "failed" || pipelines[0].Status == "canceled":
		p := pipelines[0]
		return set(aiv1.LandingBlocked, "the pipeline %s of the companion merge request %s is %s on its head %s; retry or fix it", p.WebURL, ref, p.Status, short(mr.SHA))
	default:
		return set(aiv1.LandingMerging, "the pipeline of %s on its head %s is %s", ref, short(mr.SHA), pipelines[0].Status)
	}
	if status != "mergeable" {
		return set(aiv1.LandingMerging, "GitLab reports %s as %q", ref, status)
	}
	return r.mergeNow(ctx, gl, spec, e)
}

// headPipelines is the newest pipeline of mr's head that was not skipped,
// if there is one.
func headPipelines(ctx context.Context, gl gitlab.API, project string, mr *gitlab.MergeRequest) ([]gitlab.Pipeline, error) {
	var pipelines []gitlab.Pipeline
	if mr.HeadPipeline != nil && mr.HeadPipeline.SHA == mr.SHA {
		pipelines = []gitlab.Pipeline{*mr.HeadPipeline}
	} else {
		var err error
		if pipelines, err = gl.ListPipelines(ctx, project, gitlab.ListPipelinesOptions{SHA: mr.SHA, Limit: 1}); err != nil {
			return nil, fmt.Errorf("listing the pipelines of %s: %w", short(mr.SHA), err)
		}
	}
	return slices.DeleteFunc(pipelines, func(p gitlab.Pipeline) bool { return p.Status == "skipped" }), nil
}

// mergeNow merges the checked head of e. A merge GitLab refuses goes back
// to the ready checks once, then blocks.
func (r *AgentProjectReconciler) mergeNow(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus) error {
	now := metav1.NewTime(r.now())
	ref := landingRef(e)
	log.FromContext(ctx).Info("autopilot merges the companion", "companion", ref, "sha", e.HeadSHA, "issue", e.HomeIssueIID)
	res, err := gl.MergeMergeRequest(ctx, e.Project, e.MRIID, gitlab.MergeOptions{SHA: e.HeadSHA, ShouldRemoveSourceBranch: true})
	var refused *gitlab.GuardError
	switch code := gitlab.StatusCode(err); {
	case err == nil:
		e.MergeStartedAt = &now
		e.State, e.Reason = aiv1.LandingMerging, fmt.Sprintf("GitLab merges %s", ref)
		if res.State != "merged" {
			return nil // GitLab merges asynchronously; seen on the next poll
		}
		e.MergeSHA = firstNonEmpty(res.MergeCommitSHA, res.SquashCommitSHA, res.SHA)
		return r.followMerge(ctx, gl, spec, e)
	case errors.As(err, &refused):
		// Never in normal operation: mergeSwitches checks the same target.
		// It stays blocked until a resume.
		e.MergeRefusals = maxMergeRefusals
		e.State, e.Reason = aiv1.LandingBlocked, fmt.Sprintf("the merge/push guard refused to merge %s: %s", ref, refused.Reason)
		return nil
	case code >= 400 && code < 500:
		return mergeRefused(ctx, e, err)
	default:
		return fmt.Errorf("merging %s: %w", ref, err)
	}
}

// mergeRefused counts a merge GitLab refused (a moved head, not mergeable):
// the first goes back to the ready checks, the second blocks.
func mergeRefused(ctx context.Context, e *aiv1.CompanionStatus, cause error) error {
	ref := landingRef(e)
	e.MergeRefusals++
	log.FromContext(ctx).Info("GitLab refused autopilot's merge of the companion", "companion", ref, "refusals", e.MergeRefusals, "error", cause.Error())
	if e.MergeRefusals < maxMergeRefusals {
		e.State, e.Reason = aiv1.LandingWaitingCI, fmt.Sprintf("GitLab refused to merge %s (%v); checking it again", ref, cause)
		return nil
	}
	e.State, e.Reason = aiv1.LandingBlocked, fmt.Sprintf("GitLab refused to merge %s again (%v); merge it by hand, or resume for one more try", ref, cause)
	return nil
}

// followMerge follows the default branch's pipeline of autopilot's merge
// commit: green (or none within ci.noPipelineTimeout) is merged, red or
// canceled blocks, which pauses the milestone.
func (r *AgentProjectReconciler) followMerge(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus) error {
	now := metav1.NewTime(r.now())
	ref := landingRef(e)
	proj, err := gl.GetProject(ctx, e.Project)
	if err != nil {
		return fmt.Errorf("reading the default branch of %s: %w", e.Project, err)
	}
	branch := proj.DefaultBranch
	pipelines, err := gl.ListPipelines(ctx, e.Project, gitlab.ListPipelinesOptions{SHA: e.MergeSHA, Ref: branch, Limit: 1})
	if err != nil {
		return fmt.Errorf("listing the pipelines of %s on %s: %w", short(e.MergeSHA), branch, err)
	}
	pipelines = slices.DeleteFunc(pipelines, func(p gitlab.Pipeline) bool { return p.Status == "skipped" })
	merged := func() error {
		log.FromContext(ctx).Info("companion merged, its default branch is green", "companion", ref, "sha", e.MergeSHA)
		e.State, e.Reason, e.EndedAt = aiv1.LandingMerged, "", &now
		return nil
	}
	switch {
	case len(pipelines) == 0 && now.Sub(e.MergeStartedAt.Time) >= spec.CI.NoPipelineTimeout.Duration:
		return merged() // no CI on its default branch
	case len(pipelines) == 0:
		e.State, e.Reason = aiv1.LandingMerging, fmt.Sprintf("autopilot merged %s; waiting for the pipeline of %s on %s", ref, short(e.MergeSHA), branch)
	case pipelines[0].Status == "success" || pipelines[0].Status == "manual":
		return merged()
	case pipelines[0].Status == "failed" || pipelines[0].Status == "canceled":
		p := pipelines[0]
		e.State, e.Reason = aiv1.LandingBlocked, fmt.Sprintf("autopilot merged %s, and the pipeline %s of the merge %s on %s's default branch %s is %s; "+
			"fix %s and retry the pipeline, or resume to go on", ref, p.WebURL, short(e.MergeSHA), e.Project, branch, p.Status, branch)
	default:
		e.State, e.Reason = aiv1.LandingMerging, fmt.Sprintf("autopilot merged %s; the pipeline of %s on %s is %s", ref, short(e.MergeSHA), branch, pipelines[0].Status)
	}
	return nil
}

// askOwners is the owners' path of a ready companion: autopilot does not
// merge it, why says why. The merge request is marked ready (if markReady)
// and gets one comment for the owners; one reminder follows after
// companionReminder while it stays open. Nothing is merged.
func (r *AgentProjectReconciler) askOwners(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, e *aiv1.CompanionStatus, mr *gitlab.MergeRequest,
	owners []string, why string, markReady bool) error {
	logger := log.FromContext(ctx)
	now := metav1.NewTime(r.now())
	ref := landingRef(e)
	home := fmt.Sprintf("%s!%d", spec.GitLab.Project, e.HomeMRIID)
	e.State, e.Reason = aiv1.LandingReady, "waiting for an owner to merge it; autopilot does not: "+why
	switch {
	case e.NotifiedAt == nil:
		var hint string
		if markReady && (mr.Draft || mr.DetailedMergeStatus == "draft_status") {
			res, err := gl.UpdateMergeRequest(ctx, e.Project, e.MRIID, gitlab.UpdateMergeRequestOptions{Title: gitlab.ReadyTitle(mr.Title)})
			switch {
			case gitlab.IsTransient(err):
				return fmt.Errorf("marking %s ready: %w", ref, err)
			case err != nil:
				hint = fmt.Sprintf(" It could not be marked ready (%v), so mark it ready first.", err)
			case res.Draft:
				hint = " It could not be marked ready, so mark it ready first."
			default:
				logger.Info("companion merge request marked ready for the owners", "companion", ref)
			}
		}
		body := fmt.Sprintf("🤖 This companion of %s#%d can land now: its home change %s is %s, and this merge request is green and mergeable. "+
			"Autopilot does not merge it (%s), so please merge it.%s", spec.GitLab.Project, e.HomeIssueIID, home, liveness(e), why, hint)
		if _, err := gl.CreateMergeRequestNote(ctx, e.Project, e.MRIID, notify.Defuse(body)+notify.Mention(owners)); err != nil {
			return fmt.Errorf("asking the owners to merge %s: %w", ref, err)
		}
		e.NotifiedAt = &now
		logger.Info("the owners are asked to merge the companion", "companion", ref, "owners", owners)
	case e.RemindedAt == nil && now.Sub(e.NotifiedAt.Time) >= companionReminder:
		body := fmt.Sprintf("🤖 Reminder: this companion of %s#%d has been ready to land since %s (its home change %s is %s). Please merge or close it.",
			spec.GitLab.Project, e.HomeIssueIID, e.NotifiedAt.UTC().Format(time.DateOnly), home, liveness(e))
		if _, err := gl.CreateMergeRequestNote(ctx, e.Project, e.MRIID, notify.Defuse(body)+notify.Mention(owners)); err != nil {
			return fmt.Errorf("reminding the owners of %s: %w", ref, err)
		}
		e.RemindedAt = &now
		logger.Info("the owners are reminded to merge the companion", "companion", ref, "owners", owners)
	}
	return nil
}

// liveness says how the home change of e became live.
func liveness(e *aiv1.CompanionStatus) string {
	switch {
	case e.Release != "":
		return "live in release " + e.Release
	case e.DocsOnly:
		return "merged (docs only, so live at merge)"
	default:
		return "merged"
	}
}

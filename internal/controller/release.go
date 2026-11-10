package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// maxReleaseAttempts is how often a release pipeline runs for one decision:
// the first attempt and one retry.
const maxReleaseAttempts = 2

// releaseHeadPipelines is how many of the newest pipelines of the default branch
// greenHead reads to find the newest one that is no release pipeline.
const releaseHeadPipelines = 10

// releaser carries out the release decisions of one record list (a
// milestone's, or the project's outside milestones), see
// docs/usage.md#releases. The contract with the project's CI: a pipeline on
// the default branch with AGENT_RELEASE=true runs the release job.
type releaser struct {
	r    *AgentProjectReconciler
	gl   gitlab.API
	spec aiv1.AgentProjectSpec
	// project reads the GitLab project (default branch, web URL).
	project func(context.Context) (*gitlab.Project, error)
	// issues and mrs are the merged items of the milestone, in queue order.
	issues, mrs []int64
	log         logr.Logger

	// tagging caches tagsDefaultBranch for one step.
	tagging *bool
}

// newReleaser is the releaser of the milestone's records.
func (a *autopilot) newReleaser() *releaser {
	x := &releaser{r: a.r, gl: a.gl, spec: a.spec, project: a.glProject, log: a.log}
	for _, it := range a.ap.Items {
		if it.State != aiv1.AutopilotItemMerged {
			continue
		}
		x.issues = append(x.issues, it.IID)
		if it.MergeRequestIID != 0 {
			x.mrs = append(x.mrs, it.MergeRequestIID)
		}
	}
	return x
}

// releases runs the milestone's release decisions once per poll, also while
// the queue is paused; the queue does not wait for them.
func (a *autopilot) releases(ctx context.Context) error {
	if len(a.ap.Releases) == 0 {
		return nil
	}
	err := a.newReleaser().step(ctx, a.ap.Releases)
	a.ap.Releases = aiv1.TrimReleases(a.ap.Releases, aiv1.MaxMilestoneReleases)
	return err
}

// replanDropped is the reason of a planner record a re-plan withdrew.
const replanDropped = "the re-plan dropped it"

// planReleases syncs the planner's records with the plan, on every poll: a
// planned issue with a release gets one Planned record, unless a planner
// record after it exists in any state; a Planned planner record whose issue
// the plan no longer releases after (or that left the plan) is Dropped.
// Records past Planned are never changed by a re-plan.
func (a *autopilot) planReleases() {
	release := map[int64]bool{}
	for _, p := range a.st.Status.Plan {
		release[p.IID] = p.Release
	}
	for i := range a.ap.Releases {
		rec := &a.ap.Releases[i]
		if rec.By == aiv1.ReleaseByPlanner && rec.State == aiv1.ReleasePlanned && !release[rec.AfterIssue] {
			now := metav1.NewTime(a.r.now())
			rec.State, rec.Reason, rec.EndedAt = aiv1.ReleaseDropped, replanDropped, &now
			a.log.Info("planned release dropped", "release", rec.ID, "issue", rec.AfterIssue)
		}
	}
	for _, p := range a.st.Status.Plan {
		if !p.Release || slices.ContainsFunc(a.ap.Releases, func(o aiv1.ReleaseRecord) bool {
			return o.By == aiv1.ReleaseByPlanner && o.AfterIssue == p.IID
		}) {
			continue
		}
		rec := aiv1.ReleaseRecord{ID: nextReleaseID(a.ap.Releases), By: aiv1.ReleaseByPlanner, AfterIssue: p.IID,
			Why: p.ReleaseWhy, State: aiv1.ReleasePlanned, DecidedAt: metav1.NewTime(a.r.now())}
		a.ap.Releases = append(a.ap.Releases, rec)
		a.log.Info("release planned", "release", rec.ID, "issue", p.IID)
	}
}

// releaseDue makes the Planned records after the merged it Waiting, with its
// merge commit. Without a known merge commit they stay Planned: the final
// release covers them.
func (a *autopilot) releaseDue(it *aiv1.AutopilotItem) {
	if it.MergedSHA == "" {
		return
	}
	for i := range a.ap.Releases {
		rec := &a.ap.Releases[i]
		if rec.State == aiv1.ReleasePlanned && rec.AfterIssue == it.IID {
			rec.State, rec.SHA = aiv1.ReleaseWaiting, it.MergedSHA
			a.log.Info("release due", "release", rec.ID, "issue", it.IID, "sha", it.MergedSHA)
		}
	}
}

// nextReleaseID is the largest ID of recs plus one.
func nextReleaseID(recs []aiv1.ReleaseRecord) int64 {
	var id int64
	for _, r := range recs {
		id = max(id, r.ID)
	}
	return id + 1
}

// step moves the records that have not ended and are not Planned on, a
// Running one first, then the others in ID order:
//
//	mode Off                          -> NotApplicable
//	a CalVer tag contains the commit  -> Released (our release) or AlreadyReleased
//	the project does not tag          -> NotApplicable
//	another record Running            -> Waiting
//	within minInterval (not Final)    -> Waiting
//	main's newest pipeline not green,
//	or its head lacks the commit      -> Waiting
//	otherwise                         -> Running (release pipeline started)
//
// A Running record ends Released once its pipeline succeeded and a tag
// contains the commit; a failed attempt is retried once, then Failed.
// GitLab errors are returned: the next poll tries again.
func (x *releaser) step(ctx context.Context, recs []aiv1.ReleaseRecord) error {
	order := make([]int, 0, len(recs))
	for i, rec := range recs {
		if !rec.State.Ended() && rec.State != aiv1.ReleasePlanned {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(i, j int) int {
		ri, rj := recs[i].State == aiv1.ReleaseRunning, recs[j].State == aiv1.ReleaseRunning
		if ri != rj {
			if ri {
				return -1
			}
			return 1
		}
		return cmp.Compare(recs[i].ID, recs[j].ID)
	})
	for _, i := range order {
		rec := &recs[i]
		if rec.State == aiv1.ReleaseRunning {
			if err := x.running(ctx, rec); err != nil {
				return err
			}
		}
		if rec.State == aiv1.ReleaseWaiting {
			if err := x.waiting(ctx, recs, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

// running polls the release pipeline of rec.
func (x *releaser) running(ctx context.Context, rec *aiv1.ReleaseRecord) error {
	proj, err := x.project(ctx)
	if err != nil {
		return err
	}
	p, err := x.gl.GetPipeline(ctx, x.spec.GitLab.Project, rec.PipelineID)
	switch {
	case gitlab.IsNotFound(err):
		x.failedAttempt(rec, fmt.Sprintf("the release pipeline %d is gone", rec.PipelineID))
		return nil
	case err != nil:
		return fmt.Errorf("getting the release pipeline %d: %w", rec.PipelineID, err)
	}
	switch p.Status {
	case "success":
		v, ok, err := x.containingRelease(ctx, rec.SHA)
		if err != nil {
			return err
		}
		if !ok {
			x.failedAttempt(rec, fmt.Sprintf("the pipeline passed but no tag contains %s: does its CI know AGENT_RELEASE?", short(rec.SHA)))
			return nil
		}
		x.ended(rec, aiv1.ReleaseReleased, "")
		rec.Version, rec.ReleaseURL = v, releaseURL(proj, v)
		x.log.Info("released", "release", rec.ID, "version", v)
	case "failed", "canceled", "skipped":
		x.failedAttempt(rec, fmt.Sprintf("the release pipeline %s is %s", p.WebURL, p.Status))
	}
	return nil
}

// failedAttempt records a failed release pipeline: the record waits for its
// retry, or has failed after the last attempt.
func (x *releaser) failedAttempt(rec *aiv1.ReleaseRecord, why string) {
	x.log.Info("release attempt failed", "release", rec.ID, "attempt", rec.Attempts, "why", why)
	if rec.Attempts >= maxReleaseAttempts {
		x.ended(rec, aiv1.ReleaseFailed, why)
		return
	}
	rec.State, rec.Reason = aiv1.ReleaseWaiting, fmt.Sprintf("attempt %d failed: %s", rec.Attempts, why)
}

// retryWaits joins a failed attempt and why its retry waits.
const retryWaits = "; the retry waits: "

// waitRelease records why rec waits. After a failed attempt the reason
// keeps that failure in front.
func waitRelease(rec *aiv1.ReleaseRecord, why string) {
	if rec.Attempts > 0 {
		failed, _, _ := strings.Cut(rec.Reason, retryWaits)
		why = failed + retryWaits + why
	}
	rec.Reason = why
}

// waiting checks whether rec is released already, not applicable, or can be
// released now, and starts its release pipeline when it can.
func (x *releaser) waiting(ctx context.Context, recs []aiv1.ReleaseRecord, rec *aiv1.ReleaseRecord) error {
	if !x.spec.Workflow.Release.Auto() {
		x.ended(rec, aiv1.ReleaseNotApplicable, "release.mode is Off")
		return nil
	}
	if rec.SHA == "" {
		return nil // nothing to release yet
	}
	proj, err := x.project(ctx)
	if err != nil {
		return err
	}
	if v, ok, err := x.containingRelease(ctx, rec.SHA); err != nil {
		return err
	} else if ok {
		if slices.ContainsFunc(recs, func(o aiv1.ReleaseRecord) bool { return o.State == aiv1.ReleaseReleased && o.Version == v }) {
			x.ended(rec, aiv1.ReleaseReleased, "contained in "+v)
		} else {
			x.ended(rec, aiv1.ReleaseAlreadyReleased, fmt.Sprintf("the tag %s already contains %s", v, short(rec.SHA)))
		}
		rec.Version, rec.ReleaseURL = v, releaseURL(proj, v)
		return nil
	}
	if x.tagging == nil {
		tagging, err := x.r.tagsDefaultBranch(ctx, x.gl, x.spec.GitLab.Project)
		if err != nil {
			return err
		}
		x.tagging = &tagging
	}
	if !*x.tagging {
		x.ended(rec, aiv1.ReleaseNotApplicable, x.spec.GitLab.Project+" does not tag its default branch")
		return nil
	}
	if slices.ContainsFunc(recs, func(o aiv1.ReleaseRecord) bool { return o.State == aiv1.ReleaseRunning }) {
		waitRelease(rec, "after the running release")
		return nil
	}
	now := x.r.now()
	if !rec.Final {
		if next := x.nextStart(recs); now.Before(next) {
			waitRelease(rec, fmt.Sprintf("at most one release per %s: waits until %s", x.minInterval(), next.UTC().Format("2006-01-02 15:04 UTC")))
			return nil
		}
	}
	base := proj.DefaultBranch
	head, why, err := x.greenHead(ctx, recs, x.spec.GitLab.Project, base, rec.SHA)
	if err != nil {
		return err
	}
	if why != "" {
		waitRelease(rec, why)
		return nil
	}
	p, err := x.gl.CreatePipeline(ctx, x.spec.GitLab.Project, base,
		gitlab.PipelineVariable{Key: "AGENT_RELEASE", Value: "true"},
		gitlab.PipelineVariable{Key: "AGENT_RELEASE_NOTES", Value: releaseNotes(*rec)})
	if err != nil {
		return fmt.Errorf("starting the release pipeline on %s: %w", base, err)
	}
	started := metav1.NewTime(now)
	rec.State, rec.Reason = aiv1.ReleaseRunning, ""
	rec.Attempts++
	rec.StartedAt = &started
	rec.PipelineID, rec.PipelineURL = p.ID, p.WebURL
	rec.ReleasedSHA = firstNonEmpty(p.SHA, head)
	rec.Issues, rec.MergeRequests = x.contents(recs)
	x.log.Info("release pipeline started", "release", rec.ID, "pipeline", p.ID, "attempt", rec.Attempts)
	return nil
}

// containingRelease is the first CalVer tag that contains sha.
func (x *releaser) containingRelease(ctx context.Context, sha string) (string, bool, error) {
	refs, err := x.gl.ListCommitRefs(ctx, x.spec.GitLab.Project, sha, "tag")
	if err != nil {
		return "", false, fmt.Errorf("listing the tags that contain %s: %w", short(sha), err)
	}
	v, ok := firstRelease(refs)
	if !ok {
		return "", false, nil
	}
	return releaseName(v), true, nil
}

// greenHead is the SHA of the newest pipeline on base, or why it cannot be
// released: that pipeline is not green, or its SHA does not contain sha.
// The release pipelines of recs are skipped: they run on base too, and a
// failed one must not keep its own retry waiting for a green main.
func (x *releaser) greenHead(ctx context.Context, recs []aiv1.ReleaseRecord, project, base, sha string) (head, why string, err error) {
	ps, err := x.gl.ListPipelines(ctx, project, gitlab.ListPipelinesOptions{Ref: base, Limit: releaseHeadPipelines})
	if err != nil {
		return "", "", fmt.Errorf("listing the pipelines of %s: %w", base, err)
	}
	ps = slices.DeleteFunc(ps, func(p gitlab.Pipeline) bool {
		return slices.ContainsFunc(recs, func(o aiv1.ReleaseRecord) bool { return o.PipelineID == p.ID })
	})
	if len(ps) == 0 {
		return "", fmt.Sprintf("%s has no pipeline yet", base), nil
	}
	p := ps[0]
	if p.Status != "success" {
		return "", fmt.Sprintf("the newest pipeline of %s (%s) is %s", base, short(p.SHA), p.Status), nil
	}
	if p.SHA == sha {
		return p.SHA, "", nil
	}
	c, err := x.gl.CompareCommits(ctx, project, p.SHA, sha)
	if err != nil {
		return "", "", fmt.Errorf("comparing %s with %s: %w", short(p.SHA), short(sha), err)
	}
	if len(c.Commits) > 0 {
		return "", fmt.Sprintf("the newest green pipeline of %s (%s) does not contain %s yet", base, short(p.SHA), short(sha)), nil
	}
	return p.SHA, "", nil
}

// nextStart is when the next non-final release may start: minInterval after
// the newest start of a non-final record.
func (x *releaser) nextStart(recs []aiv1.ReleaseRecord) time.Time {
	var last time.Time
	for _, o := range recs {
		if !o.Final && o.StartedAt != nil && o.StartedAt.After(last) {
			last = o.StartedAt.Time
		}
	}
	if last.IsZero() {
		return last
	}
	return last.Add(x.minInterval())
}

func (x *releaser) minInterval() time.Duration {
	if d := x.spec.Workflow.Release.MinInterval; d != nil {
		return d.Duration
	}
	return aiv1.DefaultReleaseMinInterval
}

// contents are the milestone's merged issues and merge requests that no
// earlier ended record lists.
func (x *releaser) contents(recs []aiv1.ReleaseRecord) (issues, mrs []int64) {
	var doneIssues, doneMRs []int64
	for _, o := range recs {
		if o.State.Ended() {
			doneIssues = append(doneIssues, o.Issues...)
			doneMRs = append(doneMRs, o.MergeRequests...)
		}
	}
	for _, iid := range x.issues {
		if !slices.Contains(doneIssues, iid) {
			issues = append(issues, iid)
		}
	}
	for _, iid := range x.mrs {
		if !slices.Contains(doneMRs, iid) {
			mrs = append(mrs, iid)
		}
	}
	return issues, mrs
}

func (x *releaser) ended(rec *aiv1.ReleaseRecord, state aiv1.ReleaseState, reason string) {
	now := metav1.NewTime(x.r.now())
	rec.State, rec.Reason, rec.EndedAt = state, reason, &now
}

// releaseNotes is the text of AGENT_RELEASE_NOTES: one line "<by>: <why>",
// defused. A placeholder until the release notes list the milestone.
func releaseNotes(rec aiv1.ReleaseRecord) string {
	line := string(rec.By)
	if why := strings.Join(strings.Fields(rec.Why), " "); why != "" {
		line += ": " + why
	}
	return notify.Defuse(line)
}

// releaseURL is the GitLab release page of version v.
func releaseURL(proj *gitlab.Project, v string) string {
	if proj.WebURL == "" {
		return ""
	}
	return strings.TrimSuffix(proj.WebURL, "/") + "/-/releases/" + v
}

package controller

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// maxReleaseAttempts is how often a release's tag pipeline runs for one
// decision: the first run and one retry.
const maxReleaseAttempts = 2

// tagPipelineWait is how long after creating a release the operator looks
// for its tag pipeline. A project whose CI runs nothing for tags has none:
// the tag is the release.
const tagPipelineWait = 10 * time.Minute

// releaseHeadPipelines is how many of the newest pipelines of the default branch
// greenHead reads to find the newest one that is no release pipeline.
const releaseHeadPipelines = 10

// releaser carries out the release decisions of one record list (a
// milestone's, or the project's outside milestones), see
// docs/usage.md#releases. The operator creates the tag and the GitLab
// release itself; the project's CI builds whatever it builds for tags.
type releaser struct {
	r    *AgentProjectReconciler
	gl   gitlab.API
	spec aiv1.AgentProjectSpec
	// project reads the GitLab project (default branch, web URL).
	project func(context.Context) (*gitlab.Project, error)
	// items are the merged issues of the milestone, in queue order.
	items []releaseItem
	// milestone is the milestone the release notes name; zero outside
	// milestones.
	milestone aiv1.MilestoneRef
	log       logr.Logger
}

// newReleaser is the releaser of the milestone's records.
func (a *autopilot) newReleaser() *releaser {
	x := &releaser{r: a.r, gl: a.gl, spec: a.spec, project: a.glProject, log: a.log}
	if ms := a.st.Spec.Milestone; ms != nil {
		x.milestone = *ms
	}
	titles := map[int64]string{}
	for _, p := range a.st.Status.Plan {
		titles[p.IID] = p.Title
	}
	for _, it := range a.ap.Items {
		if it.State == aiv1.AutopilotItemMerged {
			x.items = append(x.items, releaseItem{IID: it.IID, MR: it.MergeRequestIID, Title: titles[it.IID]})
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
	// Also after an error: what step changed before it is commented.
	if cerr := a.announceReleases(ctx); err == nil {
		err = cerr
	}
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

// finalWhy is the Why of the final release record.
const finalWhy = "the milestone's last merge"

// notMergedDropped is the reason of a Planned record left over at done.
const notMergedDropped = "its issue was not merged"

// lastMerge is the merged item merged last (MergedAt; items merged before it
// was recorded count as older, in queue order), nil without a merge commit.
func (a *autopilot) lastMerge() *aiv1.AutopilotItem {
	var last *aiv1.AutopilotItem
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		if it.State != aiv1.AutopilotItemMerged || it.MergedSHA == "" {
			continue
		}
		if last == nil || last.MergedAt == nil || it.MergedAt != nil && !it.MergedAt.Before(last.MergedAt) {
			last = it
		}
	}
	return last
}

// queueThrough reports whether no issue of the queue is pending or active.
func (a *autopilot) queueThrough() bool {
	return !slices.ContainsFunc(a.ap.Items, func(it aiv1.AutopilotItem) bool {
		return it.State == aiv1.AutopilotItemPending || it.State == aiv1.AutopilotItemActive
	})
}

// finalRecord is the milestone's final release record, nil before done.
func (a *autopilot) finalRecord() *aiv1.ReleaseRecord {
	for i := range a.ap.Releases {
		if a.ap.Releases[i].Final {
			return &a.ap.Releases[i]
		}
	}
	return nil
}

// finalRelease holds done until a release contains the milestone's last
// merge (decision 7) and reports whether done may go on. It creates the
// final record once (NotNeeded when nothing release-relevant changed since
// the newest tag), moves it to a newer last merge while it is not Running,
// and runs the release records right away. A Failed final release pauses
// (PauseRelease). Once it has ended, the Planned records left are Dropped.
func (a *autopilot) finalRelease(ctx context.Context) (bool, error) {
	last := a.lastMerge()
	if last == nil {
		return true, nil // nothing merged: no final release
	}
	rec := a.finalRecord()
	if rec == nil || rec.SHA != last.MergedSHA && rec.State != aiv1.ReleaseRunning {
		next := aiv1.ReleaseRecord{By: aiv1.ReleaseByFinal, Final: true, Why: finalWhy, AfterIssue: last.IID,
			SHA: last.MergedSHA, State: aiv1.ReleaseWaiting, DecidedAt: metav1.NewTime(a.r.now())}
		why, err := a.notNeeded(ctx, last.MergedSHA)
		if err != nil {
			return false, err
		}
		if why != "" {
			now := metav1.NewTime(a.r.now())
			next.State, next.Reason, next.EndedAt = aiv1.ReleaseNotNeeded, why, &now
		}
		if rec == nil {
			next.ID = nextReleaseID(a.ap.Releases)
			a.ap.Releases = append(a.ap.Releases, next)
			rec = &a.ap.Releases[len(a.ap.Releases)-1]
		} else {
			// A later merge: the same decision for the newer commit, from
			// the start (its pipeline ID kept, see greenHead).
			next.ID, next.DecidedAt, next.PipelineID = rec.ID, rec.DecidedAt, rec.PipelineID
			if !rec.State.Ended() {
				next.Announced = rec.Announced // a run waiting for its retry goes on
			}
			*rec = next
		}
		a.log.Info("final release", "release", rec.ID, "sha", rec.SHA, "state", rec.State, "why", rec.Reason)
	}
	if !rec.State.Ended() {
		if err := a.newReleaser().step(ctx, a.ap.Releases); err != nil {
			return false, err
		}
	}
	// Before the pause or done, so their comments follow the release's.
	if err := a.announceReleases(ctx); err != nil {
		return false, err
	}
	switch rec.State {
	case aiv1.ReleaseFailed:
		return false, a.pause(ctx, aiv1.PauseRelease, 0, "the final release failed: "+rec.Reason)
	case aiv1.ReleaseReleased, aiv1.ReleaseAlreadyReleased, aiv1.ReleaseNotNeeded, aiv1.ReleaseNotApplicable, aiv1.ReleaseDropped:
	default:
		a.ap.Phase = aiv1.AutopilotRunning
		return false, nil
	}
	now := metav1.NewTime(a.r.now())
	for i := range a.ap.Releases {
		if o := &a.ap.Releases[i]; o.State == aiv1.ReleasePlanned {
			o.State, o.Reason, o.EndedAt = aiv1.ReleaseDropped, notMergedDropped, &now
		}
	}
	return true, nil
}

// notNeeded says why the final release of sha is not needed: the newest
// CalVer tag does not contain sha, and the change since it is empty or only
// docs (landing's rule). "" leaves it to the state machine: release.mode
// Off, a tag contains sha (already released), the project has no CalVer
// tag, or GitLab cannot compare them.
func (a *autopilot) notNeeded(ctx context.Context, sha string) (string, error) {
	project := a.spec.GitLab.Project
	if !a.spec.Workflow.Release.Auto() {
		return "", nil
	}
	if _, ok, err := a.newReleaser().containingRelease(ctx, sha); err != nil || ok {
		return "", err
	}
	tag, found, err := newestReleaseTag(ctx, a.gl, project)
	if err != nil || !found {
		return "", err
	}
	c, err := a.gl.CompareCommits(ctx, project, tag, sha)
	switch {
	case gitlab.IsNotFound(err):
		a.log.Info("final release: cannot compare with the newest tag", "tag", tag, "sha", sha)
		return "", nil
	case err != nil:
		return "", fmt.Errorf("comparing %s with %s: %w", tag, short(sha), err)
	case len(c.Diffs) == 0:
		return "no change since " + tag, nil
	case docsOnly(c.Diffs):
		return "nothing release-relevant since " + tag, nil
	}
	return "", nil
}

// newestReleaseTag is the newest CalVer tag among the project's newest 20
// tags; false when there is none.
func newestReleaseTag(ctx context.Context, gl gitlab.API, project string) (string, bool, error) {
	tags, err := gl.ListTags(ctx, project, gitlab.ListTagsOptions{Limit: 20})
	if err != nil {
		return "", false, fmt.Errorf("listing the tags of %s: %w", project, err)
	}
	var newest release
	found := false
	for _, t := range tags {
		if v, ok := parseRelease(t.Name); ok && (!found || compareReleases(v, newest) > 0) {
			newest, found = v, true
		}
	}
	if !found {
		return "", false, nil
	}
	return releaseName(newest), true, nil
}

// retryFinalRelease starts the Failed final release over (`resume` during
// a release pause).
func (a *autopilot) retryFinalRelease(by string) {
	rec := a.finalRecord()
	if rec == nil || rec.State != aiv1.ReleaseFailed {
		return
	}
	a.log.Info("final release retried", "release", rec.ID, "by", by)
	if rec.Version != "" {
		// The tag exists: running retries its failed pipeline as a fresh
		// first attempt, never a new tag.
		rec.State, rec.Attempts, rec.Reason, rec.EndedAt = aiv1.ReleaseRunning, 0, "", nil
		return
	}
	rec.State, rec.Attempts, rec.Reason, rec.EndedAt = aiv1.ReleaseWaiting, 0, "", nil
}

// dropFinalRelease ends a Failed final release without one (`endMilestone`,
// the way out of a release pause). One that has not ended is left to run.
func (a *autopilot) dropFinalRelease(by string) {
	rec := a.finalRecord()
	if rec == nil || rec.State != aiv1.ReleaseFailed {
		return
	}
	now := metav1.NewTime(a.r.now())
	rec.State, rec.Reason, rec.EndedAt = aiv1.ReleaseDropped, "ended without a release by "+by, &now
	a.log.Info("final release dropped", "release", rec.ID, "by", by)
}

// releasedByHand lifts a release pause once a tag contains the final
// release's commit: the record is AlreadyReleased and done goes on.
func (a *autopilot) releasedByHand(ctx context.Context) error {
	rec := a.finalRecord()
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PauseKind != aiv1.PauseRelease || rec == nil {
		return nil
	}
	// Its own tag, whose build failed, is no release by hand.
	v, ok, err := a.newReleaser().containingRelease(ctx, rec.SHA, rec.Version)
	if err != nil || !ok {
		return err
	}
	proj, err := a.glProject(ctx)
	if err != nil {
		return err
	}
	why := fmt.Sprintf("the tag %s contains the milestone's last merge %s", v, short(rec.SHA))
	if err := a.resumed(ctx, byOperator, why); err != nil {
		return err
	}
	now := metav1.NewTime(a.r.now())
	rec.State, rec.Reason, rec.EndedAt = aiv1.ReleaseAlreadyReleased, fmt.Sprintf("the tag %s already contains %s", v, short(rec.SHA)), &now
	rec.Version, rec.ReleaseURL = v, releaseURL(proj, v)
	rec.Announced = rec.State // the resume comment says so
	return nil
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
//	another record Running            -> Waiting
//	within minInterval (not Final)    -> Waiting
//	main's newest pipeline not green,
//	or its head lacks the commit      -> Waiting
//	the next version's tag exists     -> Waiting (the next poll counts on)
//	otherwise                         -> Running (tag and release created)
//
// A Running record ends Released once its tag pipeline succeeded, or when
// none started within tagPipelineWait; a failed tag pipeline is retried
// once, then the record is Failed.
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

// running follows the tag pipeline of rec's release.
func (x *releaser) running(ctx context.Context, rec *aiv1.ReleaseRecord) error {
	project := x.spec.GitLab.Project
	if rec.PipelineID == 0 {
		ps, err := x.gl.ListPipelines(ctx, project, gitlab.ListPipelinesOptions{Ref: rec.Version, Limit: 1})
		if err != nil {
			return fmt.Errorf("listing the pipelines of the tag %s: %w", rec.Version, err)
		}
		if len(ps) == 0 {
			if rec.StartedAt == nil || x.r.now().Sub(rec.StartedAt.Time) >= tagPipelineWait {
				x.ended(rec, aiv1.ReleaseReleased, fmt.Sprintf("no pipeline for the tag %s: the tag is the release", rec.Version))
				x.log.Info("released", "release", rec.ID, "version", rec.Version, "pipeline", false)
			}
			return nil
		}
		rec.PipelineID, rec.PipelineURL = ps[0].ID, ps[0].WebURL
	}
	p, err := x.gl.GetPipeline(ctx, project, rec.PipelineID)
	switch {
	case gitlab.IsNotFound(err):
		rec.PipelineID, rec.PipelineURL = 0, "" // look for the tag's pipeline again
		return nil
	case err != nil:
		return fmt.Errorf("getting the tag pipeline %d: %w", rec.PipelineID, err)
	}
	switch p.Status {
	case "success", "skipped":
		x.ended(rec, aiv1.ReleaseReleased, "")
		x.log.Info("released", "release", rec.ID, "version", rec.Version)
	case "failed", "canceled":
		why := fmt.Sprintf("the tag pipeline %s is %s", p.WebURL, p.Status)
		if rec.Attempts >= maxReleaseAttempts {
			x.ended(rec, aiv1.ReleaseFailed, why)
			return nil
		}
		if _, err := x.gl.RetryPipeline(ctx, project, p.ID); err != nil {
			return fmt.Errorf("retrying the tag pipeline %d: %w", p.ID, err)
		}
		if rec.Attempts == 0 {
			rec.Reason = fmt.Sprintf("%s; retried on resume", why) // retryFinalRelease
		} else {
			rec.Reason = fmt.Sprintf("attempt %d failed: %s; retried", rec.Attempts, why)
		}
		rec.Attempts++
		x.log.Info("tag pipeline retried", "release", rec.ID, "pipeline", p.ID, "attempt", rec.Attempts)
	}
	return nil
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
// released now, and creates its tag and GitLab release when it can.
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
	// The tag of a Failed release does not count: its build failed.
	var failed []string
	for _, o := range recs {
		if o.State == aiv1.ReleaseFailed && o.Version != "" {
			failed = append(failed, o.Version)
		}
	}
	if v, ok, err := x.containingRelease(ctx, rec.SHA, failed...); err != nil {
		return err
	} else if ok {
		if slices.ContainsFunc(recs, func(o aiv1.ReleaseRecord) bool { return o.State == aiv1.ReleaseRunning && o.Version == v }) {
			waitRelease(rec, "after the running release "+v)
			return nil
		}
		if slices.ContainsFunc(recs, func(o aiv1.ReleaseRecord) bool { return o.State == aiv1.ReleaseReleased && o.Version == v }) {
			x.ended(rec, aiv1.ReleaseReleased, "contained in "+v)
		} else {
			x.ended(rec, aiv1.ReleaseAlreadyReleased, fmt.Sprintf("the tag %s already contains %s", v, short(rec.SHA)))
		}
		rec.Version, rec.ReleaseURL = v, releaseURL(proj, v)
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
	tags, err := x.gl.ListTags(ctx, x.spec.GitLab.Project, gitlab.ListTagsOptions{Limit: 100})
	if err != nil {
		return fmt.Errorf("listing the tags of %s: %w", x.spec.GitLab.Project, err)
	}
	version := nextRelease(tags, now)
	issues, mrs := x.contents(recs)
	notes := releaseNotes(*rec, issues, mrs, x.items, x.milestone.Title, x.milestoneURL(proj))
	err = x.gl.CreateRelease(ctx, x.spec.GitLab.Project, version, head, notes)
	switch code := gitlab.StatusCode(err); {
	case code == http.StatusConflict || code == http.StatusBadRequest || code == http.StatusUnprocessableEntity:
		// Someone tagged meanwhile: the next poll counts on from that tag.
		waitRelease(rec, fmt.Sprintf("the tag %s exists already: the next poll takes the next version", version))
		x.log.Info("release tag exists", "release", rec.ID, "version", version, "error", err.Error())
		return nil
	case err != nil:
		return fmt.Errorf("creating the release %s at %s: %w", version, short(head), err)
	}
	started := metav1.NewTime(now)
	rec.State, rec.Reason = aiv1.ReleaseRunning, ""
	rec.Attempts++
	rec.StartedAt = &started
	rec.Version, rec.ReleaseURL = version, releaseURL(proj, version)
	rec.PipelineID, rec.PipelineURL = 0, ""
	rec.ReleasedSHA = head
	rec.Issues, rec.MergeRequests = issues, mrs
	x.log.Info("release created", "release", rec.ID, "version", version, "sha", short(head))
	return nil
}

// nextRelease is the calendar version YEAR.MONTH.N after tags: N counts the
// releases of now's month (UTC) from 1. A release tag of a later month (a
// clock behind GitLab's) is counted on, so a version never goes back.
func nextRelease(tags []gitlab.Tag, now time.Time) string {
	now = now.UTC()
	next := release{now.Year(), int(now.Month()), 1}
	for _, t := range tags {
		if v, ok := parseRelease(t.Name); ok && compareReleases(v, next) >= 0 {
			next = release{v[0], v[1], v[2] + 1}
		}
	}
	return releaseName(next)
}

// containingRelease is the first CalVer tag that contains sha, except the
// tags named in except.
func (x *releaser) containingRelease(ctx context.Context, sha string, except ...string) (string, bool, error) {
	refs, err := x.gl.ListCommitRefs(ctx, x.spec.GitLab.Project, sha, "tag")
	if err != nil {
		return "", false, fmt.Errorf("listing the tags that contain %s: %w", short(sha), err)
	}
	refs = slices.DeleteFunc(refs, func(r gitlab.CommitRef) bool { return slices.Contains(except, r.Name) })
	v, ok := firstRelease(refs)
	if !ok {
		return "", false, nil
	}
	return releaseName(v), true, nil
}

// greenHead is the SHA of the newest pipeline on base, or why it cannot be
// released: that pipeline is not green, or its SHA does not contain sha. A
// base without any pipeline (a project without CI on it) releases sha itself.
// The pipelines of recs are skipped: a record from before tag pipelines ran
// its release pipeline on base, and a failed one must not keep its own retry
// waiting for a green main. A pipeline that did not succeed on a head a
// release ran on is skipped as well.
func (x *releaser) greenHead(ctx context.Context, recs []aiv1.ReleaseRecord, project, base, sha string) (head, why string, err error) {
	ps, err := x.gl.ListPipelines(ctx, project, gitlab.ListPipelinesOptions{Ref: base, Limit: releaseHeadPipelines})
	if err != nil {
		return "", "", fmt.Errorf("listing the pipelines of %s: %w", base, err)
	}
	if len(ps) == 0 {
		return sha, "", nil // no CI on base: nothing to wait for
	}
	ps = slices.DeleteFunc(ps, func(p gitlab.Pipeline) bool {
		return slices.ContainsFunc(recs, func(o aiv1.ReleaseRecord) bool {
			return o.PipelineID == p.ID || p.Status != "success" && o.ReleasedSHA != "" && o.ReleasedSHA == p.SHA
		})
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
	for _, it := range x.items {
		if !slices.Contains(doneIssues, it.IID) {
			issues = append(issues, it.IID)
		}
		if it.MR != 0 && !slices.Contains(doneMRs, it.MR) {
			mrs = append(mrs, it.MR)
		}
	}
	return issues, mrs
}

func (x *releaser) ended(rec *aiv1.ReleaseRecord, state aiv1.ReleaseState, reason string) {
	now := metav1.NewTime(x.r.now())
	rec.State, rec.Reason, rec.EndedAt = state, reason, &now
}

// releaseURL is the GitLab release page of version v.
func releaseURL(proj *gitlab.Project, v string) string {
	if proj.WebURL == "" {
		return ""
	}
	return strings.TrimSuffix(proj.WebURL, "/") + "/-/releases/" + v
}

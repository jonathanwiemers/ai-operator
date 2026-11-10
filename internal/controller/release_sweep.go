package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// releasePhase carries out the release decisions outside milestones, after
// the companions and main's health were synced.
type releasePhase struct {
	*projectPoll
}

func (ph releasePhase) run(ctx context.Context) {
	if err := ph.r.syncProjectReleases(ctx, ph.gl, ph.project, ph.spec, ph.specTasks); err != nil {
		ph.errs = append(ph.errs, fmt.Errorf("releases: %w", err))
	}
}

// syncProjectReleases keeps the release decisions outside milestones in the
// project's status.releases (decisions 12 and 13, docs/usage.md#releases):
// while no autopilot of the project runs or is paused, a companion waiting
// for a release and the sweep add records, then the releaser moves them on.
// While one runs, the milestone's releases cover everything: a record
// already Running is followed to its end, nothing else moves.
func (r *AgentProjectReconciler) syncProjectReleases(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, specTasks []*aiv1.AgentTask) error {
	var proj *gitlab.Project
	x := &releaser{r: r, gl: gl, spec: spec, log: log.FromContext(ctx).WithValues("releases", "project"),
		project: func(ctx context.Context) (*gitlab.Project, error) {
			if proj == nil {
				p, err := gl.GetProject(ctx, spec.GitLab.Project)
				if err != nil {
					return nil, fmt.Errorf("getting project: %w", err)
				}
				proj = p
			}
			return proj, nil
		}}
	defer func() {
		project.Status.Releases = aiv1.TrimReleases(project.Status.Releases, aiv1.MaxProjectReleases)
	}()
	if autopilotRunning(specTasks) {
		for i := range project.Status.Releases {
			if rec := &project.Status.Releases[i]; rec.State == aiv1.ReleaseRunning {
				if err := x.running(ctx, rec); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if spec.Workflow.Release.Auto() {
		r.companionProjectReleases(x, project)
		if err := r.sweepRelease(ctx, x, project, spec); err != nil {
			return err
		}
	}
	return x.step(ctx, project.Status.Releases)
}

// autopilotRunning reports whether an autopilot of the project is Running
// or Paused.
func autopilotRunning(specTasks []*aiv1.AgentTask) bool {
	return slices.ContainsFunc(specTasks, func(st *aiv1.AgentTask) bool {
		ap := st.Status.Autopilot
		return ap != nil && (ap.Phase == aiv1.AutopilotRunning || ap.Phase == aiv1.AutopilotPaused)
	})
}

// releasesOpen reports whether a record of recs has not ended.
func releasesOpen(recs []aiv1.ReleaseRecord) bool {
	return slices.ContainsFunc(recs, func(o aiv1.ReleaseRecord) bool { return !o.State.Ended() })
}

// companionProjectReleases adds a release decision for a companion that
// waits for its home change to be live, with no autopilot running (decision
// 12 outside milestones): {companion, after the home issue, its merge
// commit, Waiting}. Not while another record has not ended (one at a time:
// the next poll adds it, if that release did not contain it), and never twice
// for one home merge: a failed one is not tried again, the sweep covers it.
func (r *AgentProjectReconciler) companionProjectReleases(x *releaser, project *aiv1.AgentProject) {
	recs := &project.Status.Releases
	for i := range project.Status.Companions {
		e := &project.Status.Companions[i]
		if e.State != aiv1.LandingWaitingRelease || e.DocsOnly || e.HomeMergeSHA == "" {
			continue
		}
		if releasesOpen(*recs) {
			return
		}
		if slices.ContainsFunc(*recs, func(o aiv1.ReleaseRecord) bool {
			return o.By == aiv1.ReleaseByCompanion && o.SHA == e.HomeMergeSHA
		}) {
			continue
		}
		rec := aiv1.ReleaseRecord{ID: nextReleaseID(*recs), By: aiv1.ReleaseByCompanion, AfterIssue: e.HomeIssueIID,
			SHA: e.HomeMergeSHA, State: aiv1.ReleaseWaiting, DecidedAt: metav1.NewTime(r.now()),
			Why: fmt.Sprintf("%s waits for #%d to be live", landingRef(e), e.HomeIssueIID)}
		*recs = append(*recs, rec)
		x.log.Info("companion release added", "release", rec.ID, "companion", landingRef(e), "sha", e.HomeMergeSHA)
	}
}

// sweepRelease adds the sweep's release decision (decision 13): when the
// project tags its default branch, no record has not ended, the change from
// the newest CalVer tag to the head of the default branch is release-relevant
// (not empty, not docs only), its oldest commit is older than
// maxUnreleasedAge, and main's health is green (or flaky: green after a
// retry) on that head; with mainHealth.disabled the releaser's green-head
// check alone decides. The oldest
// commit of the whole compare counts, also when that one alone changed only
// docs: one API call per poll instead of one per commit.
func (r *AgentProjectReconciler) sweepRelease(ctx context.Context, x *releaser, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) error {
	if releasesOpen(project.Status.Releases) {
		return nil
	}
	repo := spec.GitLab.Project
	tagging, err := r.tagsDefaultBranch(ctx, x.gl, repo)
	if err != nil || !tagging {
		return err
	}
	x.tagging = &tagging
	tag, ok, err := newestReleaseTag(ctx, x.gl, repo)
	if err != nil || !ok {
		return err
	}
	proj, err := x.project(ctx)
	if err != nil {
		return err
	}
	c, err := x.gl.CompareCommits(ctx, repo, tag, proj.DefaultBranch)
	switch {
	case gitlab.IsNotFound(err):
		x.log.Info("sweep: cannot compare with the newest tag", "tag", tag, "branch", proj.DefaultBranch)
		return nil
	case err != nil:
		return fmt.Errorf("comparing %s with %s: %w", tag, proj.DefaultBranch, err)
	case len(c.Commits) == 0 || len(c.Diffs) == 0 || docsOnly(c.Diffs):
		return nil
	}
	oldest := landedSince(c.Commits)
	age := aiv1.DefaultMaxUnreleasedAge
	if d := spec.Workflow.Release.MaxUnreleasedAge; d != nil {
		age = d.Duration
	}
	if oldest.IsZero() || r.now().Sub(oldest) <= age {
		return nil
	}
	head := c.Commits[len(c.Commits)-1].ID
	// One sweep decision per head: a Failed one is not tried again until
	// another merge moves the head.
	if slices.ContainsFunc(project.Status.Releases, func(o aiv1.ReleaseRecord) bool {
		return o.By == aiv1.ReleaseBySweep && o.SHA == head
	}) {
		return nil
	}
	// Without the watcher, the releaser's green-head check alone decides.
	if h := project.Status.MainHealth; !spec.MainHealth.Disabled &&
		(h == nil || h.SHA != head || h.State != aiv1.MainHealthGreen && h.State != aiv1.MainHealthFlaky) {
		return nil
	}
	rec := aiv1.ReleaseRecord{ID: nextReleaseID(project.Status.Releases), By: aiv1.ReleaseBySweep, SHA: head,
		State: aiv1.ReleaseWaiting, DecidedAt: metav1.NewTime(r.now()),
		Why: sweepWhy(len(c.Commits), tag, oldest, age)}
	project.Status.Releases = append(project.Status.Releases, rec)
	x.log.Info("sweep release added", "release", rec.ID, "sha", head, "why", rec.Why)
	return nil
}

// sweepWhy is the reason of a sweep record.
func sweepWhy(n int, tag string, oldest time.Time, age time.Duration) string {
	commits := fmt.Sprintf("%d commits since %s are", n, tag)
	if n == 1 {
		commits = "1 commit since " + tag + " is"
	}
	return fmt.Sprintf("%s unreleased, the oldest since %s (more than %s)",
		commits, oldest.UTC().Format("2006-01-02 15:04 UTC"), roundDuration(age))
}

// landedSince is when the oldest of commits (a compare, oldest first)
// landed on the branch: the oldest committed date along the first parents
// from the head, which are the merge commits and the squashed or rebased
// ones. The commits of a merged branch are older than their merge and do not
// count. Zero without dates.
func landedSince(commits []gitlab.Commit) time.Time {
	byID := map[string]*gitlab.Commit{}
	for i := range commits {
		byID[commits[i].ID] = &commits[i]
	}
	var oldest time.Time
	for cm := &commits[len(commits)-1]; cm != nil; {
		if d := cm.CommittedDate; d != nil && (oldest.IsZero() || d.Before(oldest)) {
			oldest = *d
		}
		if len(cm.ParentIDs) == 0 {
			break
		}
		cm = byID[cm.ParentIDs[0]]
	}
	return oldest
}

// roundDuration is d without trailing zero units: "24h", "1h30m", "15m".
func roundDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

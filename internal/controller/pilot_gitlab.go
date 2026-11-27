package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/timefmt"
)

// The pilot takes the human's part for GitLab's rate limit: it hears of
// each deferral (a gitlab/<iid>/<unix> event), leaves a pending deferral to
// the operator, retries an issue whose deferrals are used up once the
// project's limit has passed, and tells the owners of a pattern.

// maxGitLabDeferrals is runner.MaxDeferrals, the deferrals of one run
// request before it fails.
const maxGitLabDeferrals = 20

// gitLabEventPrefix starts the keys of gitlab events.
const gitLabEventPrefix = "gitlab/"

// gitLabEvent is the event of a run deferred on GitLab, one per deferral:
// its key holds the time the run was deferred. A run held only by another
// run's limit (deferForProjectLimit) has counted no deferral yet.
func gitLabEvent(iid int64, t *aiv1.AgentTask) aiv1.PilotEvent {
	run := t.Status.Run
	var at int64
	if la := run.LastActivityTime; la != nil {
		at = la.Unix()
	}
	until := "the reset"
	if du := run.DeferredUntil; du != nil {
		until = timefmt.UTCMinute(du.Time)
	}
	count := ""
	if run.Deferrals > 0 {
		count = fmt.Sprintf(" (deferral %d of %d)", run.Deferrals, maxGitLabDeferrals)
	}
	return aiv1.PilotEvent{Key: fmt.Sprintf("%s%d/%d", gitLabEventPrefix, iid, at), IssueIID: iid,
		Text: fmt.Sprintf("#%d is deferred by GitLab's rate limit until %s%s; the operator retries it, nothing to do.", iid, until, count)}
}

// gitLabCapped reports whether task t failed on GitLab after its deferrals
// were used up.
func gitLabCapped(t *aiv1.AgentTask) bool {
	b := t.Status.Blocked
	return b != nil && b.Reason == aiv1.BlockInfra && t.Status.Run.FailureKind == aiv1.FailureGitLab
}

// gitLabCappedText is the event text of a task gitLabCapped, with the
// project's limitedUntil (zero: it has passed).
func gitLabCappedText(iid int64, until time.Time) string {
	when := "it has passed"
	if !until.IsZero() {
		when = "`limitedUntil` " + timefmt.UTCMinute(until)
	}
	return fmt.Sprintf("#%d is blocked (infra): GitLab's rate limit outlasted the automatic retries; `retry` is allowed once the limit has passed (%s).", iid, when)
}

// gitLabLimitedUntil is the project's limitedUntil while it is in the
// future, else zero.
func gitLabLimitedUntil(project *aiv1.AgentProject, now time.Time) time.Time {
	if g := project.Status.GitLab; g != nil && g.RateLimit != nil && g.RateLimit.LimitedUntil != nil && g.RateLimit.LimitedUntil.After(now) {
		return g.RateLimit.LimitedUntil.Time
	}
	return time.Time{}
}

// gitLabLimit is GitLab's rate limit as the pilot's retry sees it: the
// project's limitedUntil (zero once passed, gitLabLimitedUntil) at now.
type gitLabLimit struct{ until, now time.Time }

// gitLabLimitOf is the limit of project at the reconciler's clock.
func (r *AgentProjectReconciler) gitLabLimitOf(project *aiv1.AgentProject) gitLabLimit {
	now := r.now()
	return gitLabLimit{until: gitLabLimitedUntil(project, now), now: now}
}

// validRetry checks a retry against GitLab's rate limit: none while the
// issue's run waits out a deferral (the operator retries it), and after the
// deferrals are used up (gitLabCapped) only once the project's limit has
// passed. Other tools and tasks pass.
func validRetry(a aiv1.PilotAction, tasks map[int64]*aiv1.AgentTask, l gitLabLimit) error {
	t := tasks[a.IssueIID]
	if a.Tool != "retry" || t == nil {
		return nil
	}
	if run := t.Status.Run; run.BlockReason == aiv1.BlockGitLab && run.DeferredUntil != nil && run.DeferredUntil.After(l.now) {
		return fmt.Errorf("#%d waits out GitLab's rate limit until %s; the operator retries it", a.IssueIID, timefmt.UTCMinute(run.DeferredUntil.Time))
	}
	if gitLabCapped(t) && l.until.After(l.now) {
		return fmt.Errorf("GitLab's rate limit holds until %s; #%d can be retried after it", timefmt.UTCMinute(l.until), a.IssueIID)
	}
	return nil
}

// gitLabPatternPrefix starts the marker key of the hour (unix of its start)
// whose pilot note carried the GitLab pattern line.
const gitLabPatternPrefix = "gitlab-pattern/"

// gitLabPattern is the line for the owners when the pilot's events hold
// three or more GitLab deferrals within the hour before now, with their
// times and the project's peak count (0: unknown), and the marker of the
// hour it is posted in; "" when there is none or the hour had it.
func gitLabPattern(events []string, peak int32, now time.Time) (line, marker string) {
	marker = fmt.Sprintf("%s%d", gitLabPatternPrefix, now.Truncate(time.Hour).Unix())
	if slices.Contains(events, marker) {
		return "", ""
	}
	var at []time.Time
	for _, k := range events {
		rest, ok := strings.CutPrefix(k, gitLabEventPrefix)
		_, unix, cut := strings.Cut(rest, "/")
		sec, err := strconv.ParseInt(unix, 10, 64)
		if t := time.Unix(sec, 0); ok && cut && err == nil && sec > 0 && t.After(now.Add(-time.Hour)) && !t.After(now) {
			at = append(at, t)
		}
	}
	if len(at) < 3 {
		return "", ""
	}
	slices.SortFunc(at, time.Time.Compare)
	times := make([]string, len(at))
	for i, t := range at {
		times[i] = timefmt.UTCMinute(t)
	}
	tail := ""
	if peak > 0 {
		tail += fmt.Sprintf("; peak %d requests/min observed", peak)
	}
	return fmt.Sprintf("GitLab rate-limited runs %d× in the last hour (%s%s).", len(at), strings.Join(times, ", "), tail), marker
}

// postPilotNote posts body for the pilot on the tracking issue, with the
// GitLab pattern line if the hour has not had it. The hour's marker is
// recorded first in the pilot's Events (an older marker goes, and the bound
// on ended keys never drops it): a failed write leaves the line out, a
// failed post loses it for the hour, and neither repeats it.
func (r *AgentProjectReconciler) postPilotNote(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	coord *aiv1.AgentTask, tracking int64, body string) error {
	var events []string
	if coord.Status.Pilot != nil {
		events = coord.Status.Pilot.Events
	}
	var peak int32
	if g := project.Status.GitLab; g != nil && g.RateLimit != nil {
		peak = g.RateLimit.PeakObserved
	}
	if line, marker := gitLabPattern(events, peak, r.now()); line != "" {
		if err := r.patchPilot(ctx, coord, func(ps *aiv1.PilotStatus) {
			ps.Events = append([]string{marker}, slices.DeleteFunc(ps.Events, func(k string) bool { return strings.HasPrefix(k, gitLabPatternPrefix) })...)
		}); err != nil {
			log.FromContext(ctx).Info("could not record the GitLab pattern line, leaving it out", "task", coord.Name, "error", err.Error())
		} else {
			body += "\n\n" + line
		}
	}
	_, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, tracking, body)
	return err
}

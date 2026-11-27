package runner

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/timefmt"
)

// rateLimiter is a GitLab client that knows its token's rate limit reading.
type rateLimiter interface {
	RateLimit() (gitlab.RateLimitReading, bool)
}

// projectGitLabLimit is when the project's shared GitLab limit ends, if it
// still holds at now.
func projectGitLabLimit(status aiv1.AgentProjectStatus, now time.Time) (time.Time, bool) {
	if status.GitLab == nil {
		return time.Time{}, false
	}
	rl := status.GitLab.RateLimit
	if rl == nil || rl.LimitedUntil == nil || !rl.LimitedUntil.After(now) {
		return time.Time{}, false
	}
	return rl.LimitedUntil.Time, true
}

// deferForProjectLimit postpones a run that would start while another run of
// the project is deferred on GitLab, to the same time, without calling
// GitLab. It met no limit itself, so it does not count towards MaxDeferrals.
// The active request is left alone: an interrupted attempt stays one.
func (r *Runner) deferForProjectLimit(ctx context.Context, until time.Time) error {
	now := r.now()
	r.log().Info("the project's GitLab limit holds, deferring run", "until", until.UTC())
	return r.patchRun(ctx, func(s *aiv1.RunStatus) {
		s.DeferredUntil = &metav1.Time{Time: until}
		s.BlockReason = aiv1.BlockGitLab
		if s.GitLabLimitedAt == nil {
			s.GitLabLimitedAt = &metav1.Time{Time: now}
		}
		s.LastActivityTime = &metav1.Time{Time: now}
		s.LastResult = aiv1.RunResultDeferred
		s.Summary = "Waiting for the project's GitLab rate limit until " + timefmt.UTCMinute(until)
	})
}

// postGitLabLimitNote posts the late ⏳ note of a deferral chain on the
// issue, once the run starts again after the limit, and clears
// gitLabLimitedAt so the chain gets one note. The field is cleared first: a
// failed status write then loses nothing, and a retried start cannot post
// the note twice. A lost note is only logged.
func (r *Runner) postGitLabLimitNote(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask) error {
	at := task.Status.Run.GitLabLimitedAt
	if at == nil {
		return nil
	}
	if err := r.patchRun(ctx, func(s *aiv1.RunStatus) { s.GitLabLimitedAt = nil }); err != nil {
		return err
	}
	if task.Spec.Type == aiv1.TaskTypeImplement && task.Spec.Issue != nil {
		body := fmt.Sprintf("⏳ GitLab rate-limited the run at %s; it waited until %s and runs again.",
			timefmt.UTCMinute(at.Time), timefmt.UTCMinute(r.now()))
		err := gitlab.RetryTransient(ctx, func() error {
			_, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body)
			return err
		})
		if err != nil {
			r.log().Info("could not post the GitLab limit note", "error", err.Error())
		}
	}
	return nil
}

// recordGitLabRateLimit copies the client's reading into the run status when
// it is newer than the one there.
func (r *Runner) recordGitLabRateLimit(s *aiv1.RunStatus) {
	if r.rateLimit == nil {
		return
	}
	reading, ok := r.rateLimit.RateLimit()
	if !ok || reading.ReadAt.IsZero() {
		return
	}
	if cur := s.GitLabRateLimit; cur != nil && cur.ReadAt != nil && !reading.ReadAt.After(cur.ReadAt.Time) {
		return
	}
	st := reading.Status()
	s.GitLabRateLimit = &st
}

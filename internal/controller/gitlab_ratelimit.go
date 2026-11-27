package controller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// rateLimiter is a GitLab client that knows its token's rate limit reading.
type rateLimiter interface {
	RateLimit() (gitlab.RateLimitReading, bool)
}

// peakWindow is how long status.gitLab.rateLimit keeps its peak.
const peakWindow = time.Hour

// recordGitLabRateLimit sets status.gitLab.rateLimit from the poll's own
// reading and its tasks' (the status the poll writes once), and the
// remaining gauge.
func (ph cleanupPhase) recordGitLabRateLimit(tasks []aiv1.AgentTask) {
	project := ph.project
	var own *aiv1.GitLabRateLimitReading
	if ph.rateLimit != nil {
		if r, ok := ph.rateLimit.RateLimit(); ok {
			s := r.Status()
			own = &s
		}
	}
	var prev *aiv1.GitLabRateLimitStatus
	if project.Status.GitLab != nil {
		prev = project.Status.GitLab.RateLimit
	}
	rl := projectGitLabRateLimit(prev, own, tasks, ph.r.now())
	if rl == nil && project.Status.GitLab == nil {
		return
	}
	if project.Status.GitLab == nil {
		project.Status.GitLab = &aiv1.GitLabProjectStatus{}
	}
	project.Status.GitLab.RateLimit = rl
	if rl != nil && rl.ReadAt != nil && project.Status.BotUsername != "" {
		remaining := rl.Remaining
		if rl.ResetAt != nil && !rl.ResetAt.After(ph.r.now()) && rl.Limit > 0 {
			remaining = rl.Limit // the window of the last reading has reset
		}
		gitlabRateLimitRemaining.WithLabelValues(project.Status.BotUsername).Set(float64(remaining))
	}
}

// projectGitLabRateLimit is a project's GitLab rate limit after a poll:
//   - the freshest reading by readAt among the last one (prev), the
//     operator's own and the tasks' runs';
//   - limitedUntil, the latest future deferredUntil of the tasks deferred on
//     GitLab, gone once it has passed;
//   - the highest observed count within the last hour, with its time.
//
// It is nil when there is nothing to say.
func projectGitLabRateLimit(prev *aiv1.GitLabRateLimitStatus, own *aiv1.GitLabRateLimitReading, tasks []aiv1.AgentTask, now time.Time) *aiv1.GitLabRateLimitStatus {
	var rl aiv1.GitLabRateLimitStatus
	if prev != nil {
		rl = *prev.DeepCopy()
		if rl.PeakAt != nil && now.Sub(rl.PeakAt.Time) > peakWindow {
			rl.PeakObserved, rl.PeakAt = 0, nil
		}
	}
	rl.LimitedUntil = nil
	candidates := []*aiv1.GitLabRateLimitReading{own}
	if prev != nil {
		// The last reading, so an expired peak is seeded again from it.
		candidates = append(candidates, prev.GitLabRateLimitReading.DeepCopy())
	}
	for i := range tasks {
		run := &tasks[i].Status.Run
		candidates = append(candidates, run.GitLabRateLimit)
		if d := run.DeferredUntil; run.BlockReason == aiv1.BlockGitLab && d != nil && d.After(now) &&
			(rl.LimitedUntil == nil || d.After(rl.LimitedUntil.Time)) {
			rl.LimitedUntil = d.DeepCopy()
		}
	}
	for _, c := range candidates {
		if c == nil || c.ReadAt == nil {
			continue
		}
		if rl.ReadAt == nil || c.ReadAt.After(rl.ReadAt.Time) {
			rl.GitLabRateLimitReading = *c.DeepCopy()
		}
		if now.Sub(c.ReadAt.Time) <= peakWindow && (rl.PeakAt == nil || c.Observed > rl.PeakObserved) {
			rl.PeakObserved, rl.PeakAt = c.Observed, &metav1.Time{Time: c.ReadAt.Time}
		}
	}
	if rl.ReadAt == nil && rl.LimitedUntil == nil {
		return nil
	}
	return &rl
}

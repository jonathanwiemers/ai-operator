package runner

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"regexp"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// gitHTTPLimit matches git's report of a GitLab rate limit or server error on
// a fetch or push over HTTPS.
var gitHTTPLimit = regexp.MustCompile(`The requested URL returned error: (429|5\d\d)\b`)

// gitLabTransient reports whether err is a rate limit or a server error of
// GitLab itself: a 429 or 5xx from the API, a call the client did not send
// because of the rate limit, or git reporting HTTP 429 or 5xx. A bare
// timeout or EOF is not enough: it may be the engine's.
func gitLabTransient(err error) bool {
	if err == nil {
		return false
	}
	var ge *gitlab.Error
	var pe *gitlab.PacedError
	if (errors.As(err, &ge) && (ge.StatusCode == http.StatusTooManyRequests || ge.StatusCode >= 500) || errors.As(err, &pe)) &&
		gitlab.IsTransient(err) {
		return true
	}
	return gitHTTPLimit.MatchString(err.Error())
}

// gitLabRetryJitter spreads the restarts of runs deferred to the same reset.
func gitLabRetryJitter() time.Duration {
	return 10*time.Second + rand.N(30*time.Second) // #nosec G404 -- jitter, not a secret
}

// deferForGitLab postpones the current run request after GitLab rate-limited
// it or failed: until the reset GitLab named plus jitter, else by the
// lifecycle's backoff. Nothing is posted while the limit holds; the operator
// blocks the issue (gitlab) until the run starts again after DeferredUntil.
// The checkpoint is kept.
func (r *Runner) deferForGitLab(ctx context.Context, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, err error) error {
	now := r.now()
	deferrals := task.Status.Run.Deferrals + 1
	until := now.Add(spec.Lifecycle.Backoff(deferrals))
	if reset, ok := gitlab.RateLimitReset(err); ok && reset.After(now) {
		until = reset.Add(gitLabRetryJitter())
	}
	r.log().Info("GitLab limit reached, deferring run", "until", until.UTC(), "deferrals", deferrals,
		"error", err.Error(), "steps", r.run.lines())
	return r.patchRunWithCalls(ctx, func(s *aiv1.RunStatus) {
		s.ClearDeferringLimit()
		// The request stays pending, but nothing is in progress any more.
		s.ActiveRunRequest = s.ObservedRunRequest
		s.CurrentStep = ""
		s.CurrentStepSince = nil
		s.DeferredUntil = &metav1.Time{Time: until}
		s.Deferrals = deferrals
		s.BlockReason = aiv1.BlockGitLab
		s.FailureKind = ""
		if s.GitLabLimitedAt == nil {
			s.GitLabLimitedAt = &metav1.Time{Time: now}
		}
		s.LastActivityTime = &metav1.Time{Time: now}
		s.LastResult = aiv1.RunResultDeferred
		s.Summary = textutil.Truncate(err.Error(), 2000)
		s.CostUSD = aiv1.AddCost(s.CostUSD, out.CostUSD)
	})
}

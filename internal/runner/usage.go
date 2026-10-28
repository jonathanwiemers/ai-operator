package runner

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// recordUsage keeps an engine's newest usage reading on the task and in the
// project status the run works with. Reviewers call engines in parallel, so
// the whole list is written under a lock. A reading that cannot be written
// never fails the run.
func (r *Runner) recordUsage(ctx context.Context, engine aiv1.Engine, u *aiv1.EngineUsage) {
	if u == nil {
		return
	}
	reading := *u
	reading.Engine, reading.ObservedAt = engine, metav1.Time{Time: r.now()}
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	r.usage = aiv1.MergeEngineUsage(r.usage, reading)
	r.projectStatus.EngineUsage = aiv1.MergeEngineUsage(r.projectStatus.EngineUsage, reading)
	usage := r.usage
	if err := r.patchRun(ctx, func(s *aiv1.RunStatus) { s.EngineUsage = usage }); err != nil {
		r.log().Info("could not record the engine's usage", "engine", engine, "error", err.Error())
	}
}

// activeLimit returns the engine's limit the project knows of, including
// those runs of this task went around.
func (r *Runner) activeLimit(engine aiv1.Engine) (aiv1.UsageLimit, bool) {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	return r.projectStatus.ActiveUsageLimit(engine, r.now())
}

// fallBack runs a limited role's request on its fallback: the same
// invocation with the fallback's model and effort. A session is resumed only
// if it belongs to the fallback's engine; otherwise the fallback starts
// fresh with the full prompt. Without a usable fallback, or while its engine
// is limited too, the limit stands and the run is deferred.
func (r *Runner) fallBack(ctx context.Context, role aiv1.RoleSpec, inv Invocation, fullPrompt string, res *Result, err error, lim *UsageLimitError) (*Result, error) {
	fb, ferr := r.projectSpec.FallbackRole(role)
	if ferr != nil {
		r.log().Info("fallback ignored", "role", inv.Role, "error", ferr.Error())
		return res, err
	}
	if _, limited := r.activeLimit(fb.Engine); limited {
		return res, err
	}
	r.routeAround(ctx, lim, inv.Role, fb)
	r.usageMu.Lock()
	if r.sessionEngines[inv.Resume] != fb.Engine {
		inv.Resume = ""
	}
	r.usageMu.Unlock()
	inv.Model, inv.Effort = fb.Model, fb.Effort
	fres, ferr := r.invokeOn(ctx, fb, inv, fullPrompt)
	if fres != nil {
		fres.FallbackFrom = lim.Engine
		if res != nil {
			fres.CostUSD += res.CostUSD
		}
	}
	return fres, ferr
}

// routeAround records a limit a fallback goes around: in the run's project
// status, so later calls of the run skip the engine, and on the task, so the
// operator tells the other tasks. A limit without a reset time is tried
// again after lifecycle.usageLimitBackoff. The run log says which role ran
// where.
func (r *Runner) routeAround(ctx context.Context, lim *UsageLimitError, role string, fb aiv1.RoleSpec) {
	now := r.now()
	until := lim.ResetAt
	if !until.After(now) {
		until = now.Add(r.projectSpec.Lifecycle.Backoff(1))
	}
	l := aiv1.UsageLimit{Engine: lim.Engine, Until: metav1.Time{Time: until}, Message: truncate(lim.Message, 300)}
	r.log().Info("usage limit reached, running on the fallback", "role", role, "engine", lim.Engine, "until", until.UTC(), "fallback", fb.Engine, "model", fb.Model)
	r.run.noteFallback(fmt.Sprintf("↪️ `%s` reached its usage limit (until %s UTC), so %s ran on `%s/%s` instead.",
		lim.Engine, until.UTC().Format("2006-01-02 15:04"), role, fb.Engine, fb.Model))
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	r.routed = mergeLimit(r.routed, l)
	r.projectStatus.UsageLimits = mergeLimit(r.projectStatus.UsageLimits, l)
	routed := r.routed
	if err := r.patchRun(ctx, func(s *aiv1.RunStatus) { s.UsageLimits = routed }); err != nil {
		r.log().Info("could not record the usage limit", "engine", lim.Engine, "error", err.Error())
	}
}

// rememberSession notes the engine of a session the run started or resumed.
func (r *Runner) rememberSession(res *Result) {
	if res == nil || res.SessionID == "" {
		return
	}
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	if r.sessionEngines == nil {
		r.sessionEngines = map[string]aiv1.Engine{}
	}
	r.sessionEngines[res.SessionID] = res.Engine
}

// mergeLimit returns limits with l in place of its engine's limit.
func mergeLimit(limits []aiv1.UsageLimit, l aiv1.UsageLimit) []aiv1.UsageLimit {
	out := []aiv1.UsageLimit{l}
	for _, cur := range limits {
		if cur.Engine != l.Engine {
			out = append(out, cur)
		}
	}
	return out
}

// ranOn is the engine a call ran on: the fallback's, if one stood in.
func ranOn(role aiv1.RoleSpec, res *Result) aiv1.Engine {
	if res != nil && res.Engine != "" {
		return res.Engine
	}
	return role.Engine
}

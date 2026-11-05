package v1alpha1

import (
	"slices"
	"strings"
	"time"
)

// MergeUsageLimit returns limits with l merged into its engine's limit, the
// one with the later until winning (l on a tie), sorted by engine.
func MergeUsageLimit(limits []UsageLimit, l UsageLimit) []UsageLimit {
	out := slices.Clone(limits)
	if i := slices.IndexFunc(out, func(cur UsageLimit) bool { return cur.Engine == l.Engine }); i >= 0 {
		if !out[i].Until.After(l.Until.Time) {
			out[i] = l
		}
		return out
	}
	out = append(out, l)
	slices.SortFunc(out, func(a, b UsageLimit) int { return strings.Compare(string(a.Engine), string(b.Engine)) })
	return out
}

// activeUsageLimits returns the limits still in effect at now, one per
// engine (the latest), sorted by engine.
func activeUsageLimits(limits []UsageLimit, now time.Time) []UsageLimit {
	var out []UsageLimit
	for _, l := range limits {
		if l.Until.After(now) {
			out = MergeUsageLimit(out, l)
		}
	}
	return out
}

// DeferringLimit returns the limit that deferred the run: the usage limit
// marked deferred, or, for a task an older release deferred, LimitedEngine
// until DeferredUntil.
func (s RunStatus) DeferringLimit() (UsageLimit, bool) {
	if i := slices.IndexFunc(s.UsageLimits, func(l UsageLimit) bool { return l.Deferred }); i >= 0 {
		return s.UsageLimits[i], true
	}
	if s.LimitedEngine == "" || s.DeferredUntil == nil {
		return UsageLimit{}, false
	}
	return UsageLimit{Engine: s.LimitedEngine, Until: *s.DeferredUntil, Message: s.Summary, Deferred: true}, true
}

// ActiveUsageLimits returns the run's limits still in effect at now, the
// deferring one included, one per engine.
func (s RunStatus) ActiveUsageLimits(now time.Time) []UsageLimit {
	limits := s.UsageLimits
	if l, ok := s.DeferringLimit(); ok && !slices.ContainsFunc(limits, func(l UsageLimit) bool { return l.Deferred }) {
		limits = append(slices.Clone(limits), l)
	}
	return activeUsageLimits(limits, now)
}

// SetDeferringLimit merges l into the run's limits and marks its engine's
// limit as the one that deferred the run, in place of an earlier one. It
// also sets the deprecated LimitedEngine.
func (s *RunStatus) SetDeferringLimit(l UsageLimit) {
	s.ClearDeferringLimit()
	s.UsageLimits = MergeUsageLimit(s.UsageLimits, l)
	for i := range s.UsageLimits {
		s.UsageLimits[i].Deferred = s.UsageLimits[i].Engine == l.Engine
	}
	s.LimitedEngine = l.Engine
}

// ClearDeferringLimit unmarks the limit that deferred the run. The limit
// itself stays until it expires, like one the run went around.
func (s *RunStatus) ClearDeferringLimit() {
	s.LimitedEngine = ""
	if !slices.ContainsFunc(s.UsageLimits, func(l UsageLimit) bool { return l.Deferred }) {
		return
	}
	s.UsageLimits = slices.Clone(s.UsageLimits)
	for i := range s.UsageLimits {
		s.UsageLimits[i].Deferred = false
	}
}

// ProjectUsageLimits collects the active limits of a project's tasks and of
// the current runs of its researches (those whose projectRef is project),
// one per engine, the latest winning. The runners read them from the
// project status, so a limit one run hit makes the others wait or fall back
// instead of hitting it again.
func ProjectUsageLimits(tasks []AgentTask, researches []AgentResearch, project string, now time.Time) []UsageLimit {
	var all []UsageLimit
	for _, t := range tasks {
		all = append(all, t.Status.Run.ActiveUsageLimits(now)...)
	}
	for _, r := range researches {
		if r.Spec.ProjectRef == project && r.Status.Run != nil {
			all = append(all, r.Status.Run.UsageLimits...)
		}
	}
	limits := activeUsageLimits(all, now)
	for i := range limits {
		limits[i].Deferred = false // the project's limits defer no run of their own
	}
	return limits
}

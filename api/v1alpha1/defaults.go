package v1alpha1

import (
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The CRD carries the same defaults via +kubebuilder:default markers; these
// helpers make the code independent of whether the API server applied them.

const (
	DefaultModel           = "claude-opus-5-5"
	DefaultLightModel      = "claude-sonnet-5-5"
	DefaultSpecMarker      = "/agent spec"
	DefaultCommandPrefix   = "/agent"
	DefaultServiceAccount  = "ai-operator-agent"
	DefaultPollInterval    = time.Minute
	DefaultIdleTimeout     = 2 * time.Hour
	DefaultRetentionPeriod = 14 * 24 * time.Hour
	DefaultMaxRunsPerTask  = 20

	DefaultUsageLimitBackoff    = 5 * time.Minute
	DefaultMaxUsageLimitBackoff = time.Hour
	DefaultMaxConcurrent        = 3
	DefaultCIFixRounds          = 2
	DefaultNoPipelineTimeout    = 10 * time.Minute
	DefaultReviewIters          = 2
)

// WithDefaults returns a copy of the workflow labels with defaults filled in.
func (w WorkflowSpec) WithDefaults() WorkflowSpec {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&w.DraftLabel, "agent::draft")
	def(&w.ReadyLabel, "agent::ready")
	def(&w.WorkingLabel, "agent::working")
	def(&w.NeedsInputLabel, "agent::needs-input")
	def(&w.ReviewLabel, "agent::review")
	def(&w.DoneLabel, "agent::done")
	def(&w.GeneratedLabel, "ai-generated")
	def(&w.ProfileLabelPrefix, "agent-profile::")
	def(&w.CommandPrefix, DefaultCommandPrefix)
	return w
}

// ActiveLabels are the labels that mean "an agent task exists for this issue".
func (w WorkflowSpec) ActiveLabels() []string {
	return []string{w.ReadyLabel, w.WorkingLabel, w.NeedsInputLabel, w.ReviewLabel}
}

// Marker returns the effective spec marker.
func (m MilestoneSelector) Marker() string {
	if m.SpecMarker == nil {
		return DefaultSpecMarker
	}
	return *m.SpecMarker
}

// WithDefaults fills in role defaults using the strong default model.
func (r RoleSpec) WithDefaults() RoleSpec {
	return r.withDefaultModel(DefaultModel)
}

func (r RoleSpec) withDefaultModel(model string) RoleSpec {
	if r.Engine == "" {
		r.Engine = EngineClaude
	}
	if r.Model == "" && r.Engine == EngineClaude {
		r.Model = model
	}
	return r
}

// WithDefaults fills in review defaults. Reviews run on the light model.
func (r ReviewSpec) WithDefaults() ReviewSpec {
	r.RoleSpec = r.RoleSpec.withDefaultModel(DefaultLightModel)
	if r.Reviewers == nil {
		r.Reviewers = []ReviewerKind{ReviewerBugbot, ReviewerSecurity}
	}
	if r.MaxIterations == nil {
		n := int32(DefaultReviewIters)
		r.MaxIterations = &n
	}
	return r
}

// WithDefaults fills in lifecycle defaults.
func (l LifecycleSpec) WithDefaults() LifecycleSpec {
	if l.IdleTimeout.Duration == 0 {
		l.IdleTimeout = metav1.Duration{Duration: DefaultIdleTimeout}
	}
	if l.RetentionPeriod.Duration == 0 {
		l.RetentionPeriod = metav1.Duration{Duration: DefaultRetentionPeriod}
	}
	if l.MaxRunsPerTask == 0 {
		l.MaxRunsPerTask = DefaultMaxRunsPerTask
	}
	if l.UsageLimitBackoff.Duration == 0 {
		l.UsageLimitBackoff = metav1.Duration{Duration: DefaultUsageLimitBackoff}
	}
	if l.MaxUsageLimitBackoff.Duration == 0 {
		l.MaxUsageLimitBackoff = metav1.Duration{Duration: DefaultMaxUsageLimitBackoff}
	}
	return l
}

// WithDefaults returns a copy of the spec with all defaults filled in.
func (s AgentProjectSpec) WithDefaults() AgentProjectSpec {
	if s.GitLab.URL == "" {
		s.GitLab.URL = "https://gitlab.com"
	}
	s.Workflow = s.Workflow.WithDefaults()
	s.Lifecycle = s.Lifecycle.WithDefaults()
	s.CI = s.CI.WithDefaults()
	s.Agents.Spec = s.Agents.Spec.WithDefaults()
	s.Agents.Implement = s.Agents.Implement.WithDefaults()
	if len(s.Agents.Profiles) == 0 {
		s.Agents.Profiles = DefaultProfiles()
	}
	profiles := make([]AgentProfile, len(s.Agents.Profiles))
	for i, p := range s.Agents.Profiles {
		p.RoleSpec = p.RoleSpec.WithDefaults()
		profiles[i] = p
	}
	s.Agents.Profiles = profiles
	s.Agents.Review = s.Agents.Review.WithDefaults()
	if s.Agents.ServiceAccountName == "" {
		s.Agents.ServiceAccountName = DefaultServiceAccount
	}
	if s.Agents.WorkspaceSize.IsZero() {
		s.Agents.WorkspaceSize = resource.MustParse("10Gi")
	}
	if s.Agents.ImagePullPolicy == "" {
		s.Agents.ImagePullPolicy = "IfNotPresent"
	}
	if s.PollInterval.Duration == 0 {
		s.PollInterval = metav1.Duration{Duration: DefaultPollInterval}
	}
	if s.MaxConcurrentRuns == 0 {
		s.MaxConcurrentRuns = DefaultMaxConcurrent
	}
	return s
}

// DefaultProfiles are used when an AgentProject defines no profiles.
func DefaultProfiles() []AgentProfile {
	return []AgentProfile{
		{
			Name:        "fast",
			Description: "Small, well-defined changes that follow existing patterns, touch few files and leave no design decisions open.",
			RoleSpec:    RoleSpec{Engine: EngineClaude, Model: DefaultLightModel},
		},
		{
			Name:        "strong",
			Description: "Everything else: design decisions, cross-cutting or risky changes, unclear scope.",
			RoleSpec:    RoleSpec{Engine: EngineClaude, Model: DefaultModel},
		},
	}
}

// Profile returns the profile with the given name.
func (s AgentProjectSpec) Profile(name string) (AgentProfile, bool) {
	for _, p := range s.Agents.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return AgentProfile{}, false
}

// ProfileLabel returns the label that selects a profile.
func (s AgentProjectSpec) ProfileLabel(name string) string {
	return s.Workflow.ProfileLabelPrefix + name
}

// ImplementRole returns the implementing role for an issue with the given
// labels and the name of the selected profile ("" for agents.implement).
// Unknown profile names fall back to agents.implement.
func (s AgentProjectSpec) ImplementRole(labels []string) (RoleSpec, string) {
	for _, l := range labels {
		if name, ok := strings.CutPrefix(l, s.Workflow.ProfileLabelPrefix); ok {
			if p, ok := s.Profile(name); ok {
				return p.RoleSpec, p.Name
			}
		}
	}
	return s.Agents.Implement, ""
}

// ProfileOf returns the profile label name an issue carries, if any.
func (s AgentProjectSpec) ProfileOf(labels []string) string {
	for _, l := range labels {
		if name, ok := strings.CutPrefix(l, s.Workflow.ProfileLabelPrefix); ok {
			return name
		}
	}
	return ""
}

// Backoff returns the wait before retrying after the given number of
// consecutive deferrals (1 for the first one).
func (l LifecycleSpec) Backoff(deferrals int32) time.Duration {
	d := l.UsageLimitBackoff.Duration
	for i := int32(1); i < deferrals && d < l.MaxUsageLimitBackoff.Duration; i++ {
		d *= 2
	}
	return min(d, l.MaxUsageLimitBackoff.Duration)
}

// ActiveUsageLimit returns the limit of an engine that is still in effect.
func (s AgentProjectStatus) ActiveUsageLimit(engine Engine, now time.Time) (UsageLimit, bool) {
	for _, l := range s.UsageLimits {
		if l.Engine == engine && l.Until.After(now) {
			return l, true
		}
	}
	return UsageLimit{}, false
}

// WithDefaults fills in CI defaults.
func (c CISpec) WithDefaults() CISpec {
	if c.MaxFixRounds == nil {
		n := int32(DefaultCIFixRounds)
		c.MaxFixRounds = &n
	}
	if c.RetryFailedJobs == nil {
		t := true
		c.RetryFailedJobs = &t
	}
	if c.NoPipelineTimeout.Duration == 0 {
		c.NoPipelineTimeout = metav1.Duration{Duration: DefaultNoPipelineTimeout}
	}
	return c
}

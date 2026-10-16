package v1alpha1

import (
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The CRD carries the same defaults via +kubebuilder:default markers; these
// helpers make the code independent of whether the API server applied them.

const (
	DefaultModel           = "claude-opus-5-5"
	DefaultSpecMarker      = "/agent spec"
	DefaultCommandPrefix   = "/agent"
	DefaultServiceAccount  = "ai-operator-agent"
	DefaultPollInterval    = time.Minute
	DefaultIdleTimeout     = 2 * time.Hour
	DefaultRetentionPeriod = 14 * 24 * time.Hour
	DefaultMaxRunsPerTask  = 20
	DefaultMaxConcurrent   = 3
	DefaultReviewIters     = 2
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

// WithDefaults fills in role defaults.
func (r RoleSpec) WithDefaults() RoleSpec {
	if r.Engine == "" {
		r.Engine = EngineClaude
	}
	if r.Model == "" && r.Engine == EngineClaude {
		r.Model = DefaultModel
	}
	return r
}

// WithDefaults fills in review defaults.
func (r ReviewSpec) WithDefaults() ReviewSpec {
	r.RoleSpec = r.RoleSpec.WithDefaults()
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
	return l
}

// WithDefaults returns a copy of the spec with all defaults filled in.
func (s AgentProjectSpec) WithDefaults() AgentProjectSpec {
	if s.GitLab.URL == "" {
		s.GitLab.URL = "https://gitlab.com"
	}
	s.Workflow = s.Workflow.WithDefaults()
	s.Lifecycle = s.Lifecycle.WithDefaults()
	s.Agents.Spec = s.Agents.Spec.WithDefaults()
	s.Agents.Implement = s.Agents.Implement.WithDefaults()
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

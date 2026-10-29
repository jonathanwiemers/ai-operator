package v1alpha1

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// AnnotationCreatedBy names the UI user who created a research.
	AnnotationCreatedBy = "ai.wiemers.me/created-by"
	// LabelResearch marks the pods of a research's runs with its name.
	LabelResearch = "ai.wiemers.me/research"

	DefaultResearchRetention = 720 * time.Hour
	QuickResearchTimeout     = 20 * time.Minute
	DeepResearchTimeout      = 2 * time.Hour
)

// ResearchView is how the UI shows a research's results.
// +kubebuilder:validation:Enum=newsflash;report
type ResearchView string

const (
	ResearchViewNewsflash ResearchView = "newsflash"
	ResearchViewReport    ResearchView = "report"
)

// ResearchDepth sets a run's timeout and its default agent candidates.
// +kubebuilder:validation:Enum=quick;deep
type ResearchDepth string

const (
	ResearchDepthQuick ResearchDepth = "quick"
	ResearchDepthDeep  ResearchDepth = "deep"
)

// AgentResearchSpec is a research question, run once or on a schedule.
type AgentResearchSpec struct {
	// ProjectRef names the AgentProject in the same namespace whose
	// credentials, agent image, engines and usage readings the runs use.
	// +kubebuilder:validation:MinLength=1
	ProjectRef string `json:"projectRef"`

	// Prompt is the instructions for the agent.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=16384
	Prompt string `json:"prompt"`

	// Topics the results are grouped by, e.g. the sections of a news flash.
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=100
	// +optional
	Topics []string `json:"topics,omitempty"`

	// View is how the UI shows the results.
	// +kubebuilder:default=report
	// +optional
	View ResearchView `json:"view,omitempty"`

	// Depth sets the timeout (quick 20m, deep 2h) and the default agent
	// candidates.
	// +kubebuilder:default=quick
	// +optional
	Depth ResearchDepth `json:"depth,omitempty"`

	// Schedule is a standard cron expression, e.g. "0 7 * * *". Without one
	// the research runs once.
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// TimeZone is the IANA time zone of the schedule, e.g. Europe/Berlin.
	// Defaults to UTC.
	// +optional
	TimeZone string `json:"timeZone,omitempty"`

	// Retention is how long the results are kept.
	// +kubebuilder:default="720h"
	// +optional
	Retention *metav1.Duration `json:"retention,omitempty"`

	// Agent fixes the engine and model. Omitted, the operator chooses one
	// from the research candidates of the run's depth.
	// +optional
	Agent *ResearchAgent `json:"agent,omitempty"`
}

// ResearchAgent is an engine and model a research runs on. The model must be
// an allowed model of the project (see agents.models) or its alias.
type ResearchAgent struct {
	Engine Engine `json:"engine"`

	// Model passed to the engine, or its alias, e.g. sonnet.
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`

	// Effort level (claude engine only).
	// +kubebuilder:validation:Enum=low;medium;high;xhigh;max
	// +optional
	Effort string `json:"effort,omitempty"`
}

// ResearchPhase is where a research stands.
// +kubebuilder:validation:Enum=Scheduled;Running;Waiting;Succeeded;Failed
type ResearchPhase string

const (
	ResearchPhaseScheduled ResearchPhase = "Scheduled"
	ResearchPhaseRunning   ResearchPhase = "Running"
	ResearchPhaseWaiting   ResearchPhase = "Waiting"
	ResearchPhaseSucceeded ResearchPhase = "Succeeded"
	ResearchPhaseFailed    ResearchPhase = "Failed"
)

// ResearchOutcome is how a run ended: degraded means the answer failed
// validation and was stored as plain markdown; limited means it hit a usage
// limit.
// +kubebuilder:validation:Enum=succeeded;degraded;failed;limited
type ResearchOutcome string

const (
	ResearchOutcomeSucceeded ResearchOutcome = "succeeded"
	ResearchOutcomeDegraded  ResearchOutcome = "degraded"
	ResearchOutcomeFailed    ResearchOutcome = "failed"
	ResearchOutcomeLimited   ResearchOutcome = "limited"
)

// AgentResearchStatus is written by the operator and the runner.
type AgentResearchStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	Phase ResearchPhase `json:"phase,omitempty"`

	// LastRun is when the last run started.
	// +optional
	LastRun *metav1.Time `json:"lastRun,omitempty"`

	// NextRun is when the next scheduled run starts.
	// +optional
	NextRun *metav1.Time `json:"nextRun,omitempty"`

	// LastResultID is the stored result of the last successful run.
	// +optional
	LastResultID string `json:"lastResultID,omitempty"`

	// LastEngine and LastModel ran the last run.
	// +optional
	LastEngine Engine `json:"lastEngine,omitempty"`
	// +optional
	LastModel string `json:"lastModel,omitempty"`

	// SkippedReason says why the last scheduled run was skipped, e.g. which
	// agent was limited until when.
	// +optional
	SkippedReason string `json:"skippedReason,omitempty"`

	// Waiting is set while a run waits for an engine's usage to reset.
	// +optional
	Waiting *ResearchWaiting `json:"waiting,omitempty"`

	// Run is the current or last run.
	// +optional
	Run *ResearchRunStatus `json:"run,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ResearchWaiting is the engine a run waits for and until when.
type ResearchWaiting struct {
	Engine Engine      `json:"engine"`
	Until  metav1.Time `json:"until"`
}

// ResearchRunStatus is one run, written by the operator at its start and by
// the runner at its end.
type ResearchRunStatus struct {
	// ID of the run in the research store.
	// +optional
	ID string `json:"id,omitempty"`
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
	// +optional
	Engine Engine `json:"engine,omitempty"`
	// +optional
	Model string `json:"model,omitempty"`
	// +optional
	Effort string `json:"effort,omitempty"`

	// Reason says why this agent was chosen, e.g. "opus: 7-day window at
	// 82 %".
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Outcome ResearchOutcome `json:"outcome,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`

	// CostUSD is the model spend of the run, as a decimal string.
	// +optional
	CostUSD string `json:"costUSD,omitempty"`

	// EngineUsage is the newest usage reading per engine of this run; the
	// project collects it like a task's.
	// +optional
	EngineUsage []EngineUsage `json:"engineUsage,omitempty"`

	// UsageLimits the run hit.
	// +optional
	UsageLimits []UsageLimit `json:"usageLimits,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ar
// +kubebuilder:printcolumn:name="View",type=string,JSONPath=`.spec.view`
// +kubebuilder:printcolumn:name="Depth",type=string,JSONPath=`.spec.depth`
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Last run",type=date,JSONPath=`.status.lastRun`
// +kubebuilder:printcolumn:name="Next run",type=date,JSONPath=`.status.nextRun`
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.status.lastModel`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentResearch is a research question an agent answers with blocks, once or
// on a schedule; the results are kept in the research database.
type AgentResearch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   AgentResearchSpec   `json:"spec,omitempty"`
	Status AgentResearchStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentResearchList contains a list of AgentResearch.
type AgentResearchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentResearch `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentResearch{}, &AgentResearchList{})
}

// Timeout is how long a run of this depth may take.
func (s AgentResearchSpec) Timeout() time.Duration {
	if s.Depth == ResearchDepthDeep {
		return DeepResearchTimeout
	}
	return QuickResearchTimeout
}

// RetentionOrDefault is spec.retention, 720h when unset.
func (s AgentResearchSpec) RetentionOrDefault() time.Duration {
	if s.Retention == nil || s.Retention.Duration <= 0 {
		return DefaultResearchRetention
	}
	return s.Retention.Duration
}

// OneOff reports whether the research runs once (it has no schedule).
func (r *AgentResearch) OneOff() bool { return strings.TrimSpace(r.Spec.Schedule) == "" }

// ProjectResearch configures the research runs of an AgentProject.
type ProjectResearch struct {
	// WriterSecret is the database login the runner of a research run writes
	// its results with, a Secret in the project's namespace. The host,
	// port, database and sslMode come from the operator's configuration.
	// +optional
	WriterSecret *SecretLogin `json:"writerSecret,omitempty"`

	// Candidates override the chart's research.candidates for this project.
	// +optional
	Candidates ResearchCandidates `json:"candidates,omitempty"`
}

// SecretLogin is a Secret holding a database user name and password, as
// the Zalando postgres-operator creates them.
type SecretLogin struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:default=username
	// +optional
	UsernameKey string `json:"usernameKey,omitempty"`
	// +kubebuilder:default=password
	// +optional
	PasswordKey string `json:"passwordKey,omitempty"`
}

// WithDefaults returns a copy with the default keys filled in.
func (l SecretLogin) WithDefaults() SecretLogin {
	if l.UsernameKey == "" {
		l.UsernameKey = "username"
	}
	if l.PasswordKey == "" {
		l.PasswordKey = "password"
	}
	return l
}

// ResearchCandidates are the agents tried in order for automatically chosen
// research runs, per depth. An empty list keeps the chart's.
type ResearchCandidates struct {
	// +kubebuilder:validation:MaxItems=5
	// +optional
	Quick []ResearchAgent `json:"quick,omitempty"`
	// +kubebuilder:validation:MaxItems=5
	// +optional
	Deep []ResearchAgent `json:"deep,omitempty"`
}

// ResolveResearchAgent returns the agent with its model's alias resolved. It
// refuses a model the project does not allow and an effort on an engine
// without one, like FallbackRole.
func (s AgentProjectSpec) ResolveResearchAgent(a ResearchAgent) (ResearchAgent, error) {
	o, ok := s.FindModel(a.Engine, a.Model)
	if !ok {
		return ResearchAgent{}, fmt.Errorf("the agent %s/%s is not an allowed model", a.Engine, a.Model)
	}
	if a.Effort != "" {
		if !ValidEffort(a.Effort) {
			return ResearchAgent{}, fmt.Errorf("the effort %q is no effort level (%s)", a.Effort, strings.Join(Efforts, ", "))
		}
		if o.Engine != EngineClaude {
			return ResearchAgent{}, fmt.Errorf("the %s engine has no effort setting", o.Engine)
		}
	}
	return ResearchAgent{Engine: o.Engine, Model: o.Model, Effort: a.Effort}, nil
}

// ValidateResearchAgent checks a research's fixed agent against the project.
func ValidateResearchAgent(project AgentProjectSpec, a ResearchAgent) error {
	_, err := project.ResolveResearchAgent(a)
	return err
}

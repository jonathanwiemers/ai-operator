package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentPlanSpec is one milestone-planning conversation the Desk started.
type AgentPlanSpec struct {
	// ProjectRef names the AgentProject in the same namespace.
	// +kubebuilder:validation:MinLength=1
	ProjectRef string `json:"projectRef"`
	// Title of the plan; may be empty.
	// +optional
	Title string `json:"title,omitempty"`
	// CreatedBy is the Desk subject (principal username) who created it.
	// +kubebuilder:validation:MinLength=1
	CreatedBy string `json:"createdBy"`
	// MaxBudgetUSD is the plan's cap after an owner raised it, as a decimal
	// string; empty means the operator default plan.maxBudgetUSD.
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	// +optional
	MaxBudgetUSD string `json:"maxBudgetUSD,omitempty"`
	// ChatMessages are the Desk messages the operator handed to the plan's
	// pod and the runner has not answered yet.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	ChatMessages []ChatMessage `json:"chatMessages,omitempty"`
}

// AgentPlanPhase is where a plan stands.
// +kubebuilder:validation:Enum=Starting;Ready;Working;Idle;Created;Failed
type AgentPlanPhase string

const (
	AgentPlanStarting AgentPlanPhase = "Starting"
	AgentPlanReady    AgentPlanPhase = "Ready"
	AgentPlanWorking  AgentPlanPhase = "Working"
	AgentPlanIdle     AgentPlanPhase = "Idle"
	AgentPlanCreated  AgentPlanPhase = "Created"
	AgentPlanFailed   AgentPlanPhase = "Failed"
)

// AgentPlanStatus is the plan's draft, its milestone once created, and its
// spend.
type AgentPlanStatus struct {
	// +optional
	Phase AgentPlanPhase `json:"phase,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// Title is the plan's title from the agent, then the created
	// milestone's title.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Title string `json:"title,omitempty"`
	// Draft is the milestone description in markdown.
	// +optional
	// +kubebuilder:validation:MaxLength=65536
	Draft string `json:"draft,omitempty"`
	// DraftRevision counts the draft's versions, by agent or owner.
	// +optional
	DraftRevision int64 `json:"draftRevision,omitempty"`
	// DraftBy is who wrote the current draft.
	// +optional
	// +kubebuilder:validation:Enum=agent;owner
	DraftBy string `json:"draftBy,omitempty"`
	// DraftSHA is the default branch SHA the agent read for the draft.
	// +optional
	DraftSHA string `json:"draftSHA,omitempty"`
	// +optional
	CreatedMilestoneIID int64 `json:"createdMilestoneIID,omitempty"`
	// +optional
	CreatedMilestoneURL string `json:"createdMilestoneURL,omitempty"`
	// SpendUSD is the plan's model spend as a decimal string (4 decimals).
	// +optional
	SpendUSD string `json:"spendUSD,omitempty"`
	// +optional
	Tokens int64 `json:"tokens,omitempty"`
	// +optional
	Chat *ChatStatus `json:"chat,omitempty"`
	// Calls are the plan's engine calls, newest last.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	Calls []AgentCall `json:"calls,omitempty"`
	// +optional
	PodName string `json:"podName,omitempty"`
	// PodFailures counts the plan's pods that failed in a row; the plan
	// fails at MaxPlanPodFailures.
	// +optional
	PodFailures int32 `json:"podFailures,omitempty"`
	// Decisions are the plan's current decisions, in the agent's order: the
	// questions it asks (open) and the decisions in the draft (locked).
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=30
	Decisions []PlanDecision `json:"decisions,omitempty"`
	// DecisionSeq is the highest decision number ever issued, so a dropped
	// decision's ID is never reused.
	// +optional
	// +kubebuilder:validation:Minimum=0
	DecisionSeq int32 `json:"decisionSeq,omitempty"`
}

// PlanDecision is one decision of a plan, at its current version.
type PlanDecision struct {
	// ID is stable across versions and never reused: D1, D2, …
	// +kubebuilder:validation:Pattern=`^D[1-9][0-9]*$`
	ID string `json:"id"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=120
	Title string `json:"title"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1000
	Question string `json:"question"`
	// +listType=atomic
	// +kubebuilder:validation:MinItems=2
	// +kubebuilder:validation:MaxItems=5
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=500
	Options []string `json:"options"`
	// Recommended is the 0-based index of the agent's pick in Options.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4
	Recommended int32 `json:"recommended"`
	// Why the agent recommends its pick.
	// +optional
	// +kubebuilder:validation:MaxLength=1000
	Why string `json:"why,omitempty"`
	// State is open or locked.
	State PlanDecisionState `json:"state"`
	// Version rises whenever the title, question, options, recommended
	// option or state changes.
	// +kubebuilder:validation:Minimum=1
	Version int64 `json:"version"`
	// DecidedBy is "agent" or the OIDC subject of the owner who decided
	// this version.
	// +kubebuilder:validation:MinLength=1
	DecidedBy string `json:"decidedBy"`
	// +optional
	DecidedByName string `json:"decidedByName,omitempty"`
	// DecidedAt is when this version was made.
	DecidedAt metav1.Time `json:"decidedAt"`
	// SignedOffBy is the OIDC subject of the owner who last signed this
	// version off; empty while it is not signed off.
	// +optional
	SignedOffBy string `json:"signedOffBy,omitempty"`
	// +optional
	SignedOffByName string `json:"signedOffByName,omitempty"`
	// +optional
	SignedOffAt *metav1.Time `json:"signedOffAt,omitempty"`
}

// PlanDecisionState is open for a question the agent asks in its reply,
// locked for a decision in the draft.
// +kubebuilder:validation:Enum=open;locked
type PlanDecisionState string

const (
	PlanDecisionOpen   PlanDecisionState = "open"
	PlanDecisionLocked PlanDecisionState = "locked"
)

// MaxPlanPodFailures is how many pods of a plan may fail in a row before
// the plan fails.
const MaxPlanPodFailures = 3

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.projectRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Milestone",type=integer,JSONPath=`.status.createdMilestoneIID`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentPlan is an owner's conversation with an agent that drafts a new
// milestone of a project, until the Desk creates it.
type AgentPlan struct {
	metav1.TypeMeta   `json:",inline" tstype:",extends"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentPlanSpec   `json:"spec"`
	Status AgentPlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentPlanList contains a list of AgentPlan.
type AgentPlanList struct {
	metav1.TypeMeta `json:",inline" tstype:",extends"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentPlan{}, &AgentPlanList{})
}

// BudgetUSD is the plan's spend cap: its raised spec value, else def.
func (p *AgentPlan) BudgetUSD(def string) string {
	if p.Spec.MaxBudgetUSD != "" {
		return p.Spec.MaxBudgetUSD
	}
	return def
}

// Active reports whether the plan may still take turns: it has neither
// created its milestone nor failed.
func (p *AgentPlan) Active() bool {
	return p.Status.Phase != AgentPlanCreated && p.Status.Phase != AgentPlanFailed
}

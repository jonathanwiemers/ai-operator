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
}

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

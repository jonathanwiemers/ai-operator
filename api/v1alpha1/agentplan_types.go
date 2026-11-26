package v1alpha1

import (
	"slices"

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

// AgentPlanStatus is the plan's drafts, their milestones once created, and
// its spend.
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
	// Drafts are the plan's milestone drafts, in the order their milestones
	// are created.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=5
	Drafts []PlanDraft `json:"drafts,omitempty"`
	// DraftSeq is the highest draft key ever given, so a removed draft's key
	// is never reused.
	// +optional
	// +kubebuilder:validation:Minimum=0
	DraftSeq int64 `json:"draftSeq,omitempty"`
	// Draft is the milestone description in markdown.
	//
	// Deprecated: the single draft of a plan from before Drafts; MoveDrafts
	// moves it to key 1.
	// +optional
	// +kubebuilder:validation:MaxLength=65536
	Draft string `json:"draft,omitempty"`
	// DraftRevision counts the draft's versions, by agent or owner.
	//
	// Deprecated: see Draft.
	// +optional
	DraftRevision int64 `json:"draftRevision,omitempty"`
	// DraftBy is who wrote the current draft.
	//
	// Deprecated: see Draft.
	// +optional
	// +kubebuilder:validation:Enum=agent;owner
	DraftBy string `json:"draftBy,omitempty"`
	// DraftSHA is the default branch SHA the agent read for the draft.
	//
	// Deprecated: see Draft.
	// +optional
	DraftSHA string `json:"draftSHA,omitempty"`
	// CreatedMilestoneIID is the first milestone created from the plan, for
	// the printer column.
	// +optional
	CreatedMilestoneIID int64 `json:"createdMilestoneIID,omitempty"`
	// CreatedMilestoneURL is the created milestone's web URL.
	//
	// Deprecated: see Draft; each draft holds its own.
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

// PlanDraft is one milestone draft of a plan.
type PlanDraft struct {
	// Key identifies the draft within the plan; it is never reused.
	// +kubebuilder:validation:Minimum=1
	Key int64 `json:"key"`
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Title string `json:"title,omitempty"`
	// Draft is the milestone description in markdown.
	// +optional
	// +kubebuilder:validation:MaxLength=65536
	Draft string `json:"draft,omitempty"`
	// Revision counts the draft's versions, by agent or owner.
	// +optional
	Revision int64 `json:"revision,omitempty"`
	// By is who wrote the current version.
	// +optional
	// +kubebuilder:validation:Enum=agent;owner
	By string `json:"by,omitempty"`
	// SHA is the default branch SHA the agent read for the draft.
	// +optional
	SHA string `json:"sha,omitempty"`
	// CreatedMilestoneIID is the draft's milestone; -1 while the UI server
	// creates it.
	// +optional
	CreatedMilestoneIID int64 `json:"createdMilestoneIID,omitempty"`
	// +optional
	CreatedMilestoneURL string `json:"createdMilestoneURL,omitempty"`
	// PendingRefs are the references to later drafts that the created
	// milestone's description names by their quoted title, until those drafts
	// are created.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=5
	PendingRefs []PlanDraftRef `json:"pendingRefs,omitempty"`
}

// PlanDraftRef is a reference to a later draft, written in a created draft's
// milestone in place of its %IID.
type PlanDraftRef struct {
	// Key is the later draft's key.
	// +kubebuilder:validation:Minimum=1
	Key int64 `json:"key"`
	// Text is the exact text written in its place: the later draft's title in
	// straight double quotes.
	// +kubebuilder:validation:MaxLength=300
	Text string `json:"text"`
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

// Active reports whether the plan may still take turns: it has not failed,
// and its phase is not Created, or some of its drafts have milestones and
// others not yet. A Created plan without any created draft is claiming its
// first milestone (or lost the record of it) and is not active.
func (p *AgentPlan) Active() bool {
	switch p.Status.Phase {
	case AgentPlanFailed:
		return false
	case AgentPlanCreated:
		list := p.DraftList()
		return slices.ContainsFunc(list, func(d PlanDraft) bool { return d.CreatedMilestoneIID > 0 }) && !p.AllDraftsCreated()
	}
	return true
}

// DraftList is the plan's drafts: status.drafts, else the single draft of a
// plan from before them as key 1, when it has text or a created milestone.
func (p *AgentPlan) DraftList() []PlanDraft {
	if len(p.Status.Drafts) > 0 {
		return p.Status.Drafts
	}
	if d, ok := p.Status.oldDraft(); ok {
		return []PlanDraft{d}
	}
	return nil
}

// AllDraftsCreated reports whether every draft has its milestone; false
// without drafts and while one is being created.
func (p *AgentPlan) AllDraftsCreated() bool {
	list := p.DraftList()
	return len(list) > 0 && !slices.ContainsFunc(list, func(d PlanDraft) bool { return d.CreatedMilestoneIID <= 0 })
}

// oldDraft is the single draft of a plan from before Drafts as key 1, and
// whether there is one.
func (s *AgentPlanStatus) oldDraft() (PlanDraft, bool) {
	return PlanDraft{Key: 1, Title: s.Title, Draft: s.Draft, Revision: s.DraftRevision, By: s.DraftBy, SHA: s.DraftSHA,
		CreatedMilestoneIID: s.CreatedMilestoneIID, CreatedMilestoneURL: s.CreatedMilestoneURL}, s.Draft != "" || s.CreatedMilestoneIID != 0
}

// MoveDrafts moves the single draft of a plan from before Drafts to key 1
// and clears the deprecated fields, all but CreatedMilestoneIID. Every
// writer of a draft calls it first; a no-op once Drafts is used.
func (s *AgentPlanStatus) MoveDrafts() {
	d, ok := s.oldDraft()
	if len(s.Drafts) > 0 || !ok {
		return
	}
	s.Drafts, s.DraftSeq = []PlanDraft{d}, max(s.DraftSeq, 1)
	s.Draft, s.DraftRevision, s.DraftBy, s.DraftSHA, s.CreatedMilestoneURL = "", 0, "", "", ""
}

// NextDraftKey gives out the next draft key, one above every key given so
// far, and records it in DraftSeq.
func (s *AgentPlanStatus) NextDraftKey() int64 {
	for _, d := range s.Drafts {
		s.DraftSeq = max(s.DraftSeq, d.Key)
	}
	s.DraftSeq++
	return s.DraftSeq
}

// DraftIndex is the index of the draft with key in Drafts, -1 without one.
func (s *AgentPlanStatus) DraftIndex(key int64) int {
	return slices.IndexFunc(s.Drafts, func(d PlanDraft) bool { return d.Key == key })
}

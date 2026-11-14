package ui

import (
	"net/http"
	"slices"
	"sort"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// PlanListItem is one milestone plan of a project, without its draft.
type PlanListItem struct {
	Name                string              `json:"name"`
	Title               string              `json:"title"`
	Phase               aiv1.AgentPlanPhase `json:"phase"`
	CreatedBy           string              `json:"createdBy"`
	CreatedAt           time.Time           `json:"createdAt"`
	LastActivityAt      time.Time           `json:"lastActivityAt"`
	SpendUSD            string              `json:"spendUSD"`
	Tokens              int64               `json:"tokens"`
	BudgetUSD           string              `json:"budgetUSD"`
	BudgetLimitUSD      string              `json:"budgetLimitUSD"`
	CreatedMilestoneIID int64               `json:"createdMilestoneIID,omitempty"`
	CreatedMilestoneURL string              `json:"createdMilestoneURL,omitempty"`
	Permissions         PlanPermissions     `json:"permissions"`
}

// PlanPermissions says what the caller may do with the project's plans.
type PlanPermissions struct {
	// Steer: post messages, edit drafts and create plans and milestones.
	Steer bool `json:"steer"`
}

// PlanView is one plan with its draft.
type PlanView struct {
	PlanListItem  `json:",inline" tstype:",extends"`
	Draft         string `json:"draft"`
	DraftRevision int64  `json:"draftRevision"`
	DraftBy       string `json:"draftBy,omitempty"`
	DraftSHA      string `json:"draftSHA,omitempty"`
}

func (s *Server) planItem(pl *aiv1.AgentPlan, steer bool) PlanListItem {
	last := pl.CreationTimestamp.Time
	if c := pl.Status.Chat; c != nil && c.LastTurnTime != nil && c.LastTurnTime.After(last) {
		last = c.LastTurnTime.Time
	}
	return PlanListItem{Name: pl.Name, Title: pl.Spec.Title, Phase: pl.Status.Phase, CreatedBy: pl.Spec.CreatedBy,
		CreatedAt: pl.CreationTimestamp.Time, LastActivityAt: last, SpendUSD: pl.Status.SpendUSD, Tokens: pl.Status.Tokens,
		BudgetUSD: pl.BudgetUSD(s.cfg.Plan.MaxBudgetUSD), BudgetLimitUSD: s.cfg.Plan.MaxBudgetUSDLimit,
		CreatedMilestoneIID: pl.Status.CreatedMilestoneIID, CreatedMilestoneURL: pl.Status.CreatedMilestoneURL,
		Permissions: PlanPermissions{Steer: steer}}
}

// planOwner reports whether p may steer the project's plans: the steer grant
// and p among the owner-subjects of its live Spec tasks, the grant alone
// when none has any.
func (s *Server) planOwner(p Principal, ap *aiv1.AgentProject, tasks []aiv1.AgentTask) bool {
	if !s.authz.CanSteer(p, ap.Namespace, ap.Name) {
		return false
	}
	owners := false
	for _, t := range tasks {
		if t.Spec.Type == aiv1.TaskTypeSpec && t.DeletionTimestamp == nil && len(t.Spec.OwnerSubjects) > 0 {
			if slices.Contains(t.Spec.OwnerSubjects, p.Username) {
				return true
			}
			owners = true
		}
	}
	return !owners
}

// projectPlans loads the project and its plans, newest first: 404 without
// chat or a view of the project.
func (s *Server) projectPlans(w http.ResponseWriter, r *http.Request) ([]aiv1.AgentPlan, bool, bool) {
	if !s.cfg.ChatEnabled() {
		notFound(w)
		return nil, false, false
	}
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return nil, false, false
	}
	var list aiv1.AgentPlanList
	if err := s.reader.List(r.Context(), &list, client.InNamespace(ap.Namespace)); err != nil {
		s.internalError(w, err)
		return nil, false, false
	}
	plans := slices.DeleteFunc(list.Items, func(pl aiv1.AgentPlan) bool { return pl.Spec.ProjectRef != ap.Name })
	sort.SliceStable(plans, func(i, j int) bool { return plans[j].CreationTimestamp.Before(&plans[i].CreationTimestamp) })
	return plans, s.planOwner(principalOf(r), ap, tasks), true
}

// plans lists a project's plans.
func (s *Server) plans(w http.ResponseWriter, r *http.Request) {
	plans, steer, ok := s.projectPlans(w, r)
	if !ok {
		return
	}
	out := []PlanListItem{}
	for i := range plans {
		out = append(out, s.planItem(&plans[i], steer))
	}
	writeJSON(w, http.StatusOK, out)
}

// plan returns one plan with its draft.
func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	plans, steer, ok := s.projectPlans(w, r)
	if !ok {
		return
	}
	for i := range plans {
		if pl := &plans[i]; pl.Name == r.PathValue("plan") {
			writeJSON(w, http.StatusOK, PlanView{PlanListItem: s.planItem(pl, steer), Draft: pl.Status.Draft,
				DraftRevision: pl.Status.DraftRevision, DraftBy: pl.Status.DraftBy, DraftSHA: pl.Status.DraftSHA})
			return
		}
	}
	notFound(w)
}

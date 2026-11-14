package ui

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
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
func (s *Server) projectPlans(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, []aiv1.AgentPlan, bool, bool) {
	if !s.cfg.ChatEnabled() {
		notFound(w)
		return nil, nil, false, false
	}
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return nil, nil, false, false
	}
	var list aiv1.AgentPlanList
	if err := s.reader.List(r.Context(), &list, client.InNamespace(ap.Namespace)); err != nil {
		s.internalError(w, err)
		return nil, nil, false, false
	}
	plans := slices.DeleteFunc(list.Items, func(pl aiv1.AgentPlan) bool { return pl.Spec.ProjectRef != ap.Name })
	sort.SliceStable(plans, func(i, j int) bool { return plans[j].CreationTimestamp.Before(&plans[i].CreationTimestamp) })
	return ap, plans, s.planOwner(principalOf(r), ap, tasks), true
}

// plans lists a project's plans.
func (s *Server) plans(w http.ResponseWriter, r *http.Request) {
	_, plans, steer, ok := s.projectPlans(w, r)
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
	pl, steer, ok := s.projectPlan(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, PlanView{PlanListItem: s.planItem(pl, steer), Draft: pl.Status.Draft,
		DraftRevision: pl.Status.DraftRevision, DraftBy: pl.Status.DraftBy, DraftSHA: pl.Status.DraftSHA})
}

// Limits of the plans owners start from the Desk.
const (
	maxPlanTitle   = 255
	plansPerWindow = 10
)

// ownPlans is projectPlans for a change: 404 without the writer client, 403
// unless the caller steers the project's plans.
func (s *Server) ownPlans(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, []aiv1.AgentPlan, bool) {
	ap, plans, steer, ok := s.projectPlans(w, r)
	switch {
	case !ok:
	case s.writer == nil:
		notFound(w)
	case !steer:
		writeError(w, http.StatusForbidden, "only the project's owners change its plans")
	default:
		return ap, plans, true
	}
	return nil, nil, false
}

// createPlan starts a plan of the project {"title"?}, named by the server,
// owned by the project and stamped with its creator.
func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	ap, _, ok := s.ownPlans(w, r)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		badRequest(w, "invalid request")
		return
	}
	if req.Title = strings.TrimSpace(req.Title); utf8.RuneCountInString(req.Title) > maxPlanTitle || strings.ContainsAny(req.Title, "\r\n") {
		fieldError(w, "title", fmt.Sprintf("the title must be one line of at most %d characters", maxPlanTitle))
		return
	}
	p := principalOf(r)
	if ok, wait := s.planCreates.allow(p.Username, time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("at most %d plans per hour, try again later", plansPerWindow))
		return
	}
	pl := &aiv1.AgentPlan{
		ObjectMeta: metav1.ObjectMeta{Namespace: ap.Namespace, Name: planName(time.Now()), OwnerReferences: []metav1.OwnerReference{
			{APIVersion: aiv1.GroupVersion.String(), Kind: "AgentProject", Name: ap.Name, UID: ap.UID}}},
		Spec: aiv1.AgentPlanSpec{ProjectRef: ap.Name, Title: req.Title, CreatedBy: p.Username},
	}
	if err := s.writer.Create(r.Context(), pl); err != nil {
		s.internalError(w, err)
		return
	}
	s.log.Info("audit", "action", "plan.create", "user", p.Username, "namespace", pl.Namespace, "name", pl.Name, "project", ap.Name)
	writeJSON(w, http.StatusCreated, PlanView{PlanListItem: s.planItem(pl, true)})
}

// deletePlan deletes one of the project's plans; its pod and PVC follow.
func (s *Server) deletePlan(w http.ResponseWriter, r *http.Request) {
	ap, plans, ok := s.ownPlans(w, r)
	if !ok {
		return
	}
	i := slices.IndexFunc(plans, func(pl aiv1.AgentPlan) bool { return pl.Name == r.PathValue("plan") })
	if i < 0 {
		notFound(w)
		return
	}
	if err := s.writer.Delete(r.Context(), &plans[i]); client.IgnoreNotFound(err) != nil {
		s.internalError(w, err)
		return
	}
	s.log.Info("audit", "action", "plan.delete", "user", principalOf(r).Username, "namespace", ap.Namespace, "name", plans[i].Name, "project", ap.Name)
	w.WriteHeader(http.StatusNoContent)
}

// projectPlan is projectPlans for the plan of the request path: 404 when the
// project has none of that name.
func (s *Server) projectPlan(w http.ResponseWriter, r *http.Request) (*aiv1.AgentPlan, bool, bool) {
	_, plans, steer, ok := s.projectPlans(w, r)
	if !ok {
		return nil, false, false
	}
	i := slices.IndexFunc(plans, func(pl aiv1.AgentPlan) bool { return pl.Name == r.PathValue("plan") })
	if i < 0 {
		notFound(w)
		return nil, false, false
	}
	return &plans[i], steer, true
}

// postPlanChat posts the owner's message or interrupt to a plan that has not
// created its milestone yet. The audit line never includes the text.
func (s *Server) postPlanChat(w http.ResponseWriter, r *http.Request) {
	pl, steer, ok := s.projectPlan(w, r)
	switch {
	case !ok:
		return
	case !steer:
		writeError(w, http.StatusForbidden, "only the project's owners change its plans")
		return
	case pl.Status.Phase == aiv1.AgentPlanCreated:
		writeError(w, http.StatusConflict, "the plan has created its milestone")
		return
	}
	req, ok := readChat(w, r)
	if !ok {
		return
	}
	if req.Kind == "command" {
		badRequest(w, "commands are not supported on a plan")
		return
	}
	id, created, ok := s.insertChat(w, r, store.ChatMessage{Namespace: pl.Namespace, Project: pl.Spec.ProjectRef, Plan: pl.Name,
		Kind: req.Kind, Body: req.Body, ClientKey: req.ClientKey})
	if !ok {
		return
	}
	s.log.Info("audit", "action", "plan.chat", "user", principalOf(r).Username, "namespace", pl.Namespace, "name", pl.Name,
		"project", pl.Spec.ProjectRef, "kind", req.Kind, "id", id, "created", created)
	writeChatPosted(w, id, created)
}

// getPlanChat returns the plan's messages, newest first, paged like getChat.
func (s *Server) getPlanChat(w http.ResponseWriter, r *http.Request) {
	pl, _, ok := s.projectPlan(w, r)
	if !ok {
		return
	}
	s.writeChatPage(w, r, func(st *store.Store, before, after int64, limit int) ([]store.ChatMessage, error) {
		return st.PlanMessages(r.Context(), pl.Namespace, pl.Spec.ProjectRef, pl.Name, before, after, limit)
	})
}

// planName is plan-<yyyymmdd>-<6 random characters>.
func planName(now time.Time) string {
	return "plan-" + now.UTC().Format("20060102") + "-" + strings.ToLower(rand.Text()[:6])
}

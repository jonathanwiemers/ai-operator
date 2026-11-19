package ui

import (
	"cmp"
	"context"
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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// PlanListItem is one milestone plan of a project, without its draft.
type PlanListItem struct {
	Name                string              `json:"name"`
	Namespace           string              `json:"namespace"`
	Project             string              `json:"project"`
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
	// Milestone is the created milestone's row on the Desk, nil without
	// one.
	Milestone *PlanMilestone `json:"milestone,omitempty"`
}

// PlanMilestone is where a plan's created milestone stands, from its row
// on the milestones dashboard. Stored: the row is the history snapshot's.
type PlanMilestone struct {
	State    MilestoneState    `json:"state"`
	Progress MilestoneProgress `json:"progress"`
	Stored   bool              `json:"stored"`
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
	// Decisions is the current list in status order, never null;
	// DecisionsPending counts the open or not signed-off entries.
	Decisions        []aiv1.PlanDecision `json:"decisions"`
	DecisionsPending int                 `json:"decisionsPending"`
}

func (s *Server) planItem(pl *aiv1.AgentPlan, steer bool) PlanListItem {
	last := pl.CreationTimestamp.Time
	if c := pl.Status.Chat; c != nil && c.LastTurnTime != nil && c.LastTurnTime.After(last) {
		last = c.LastTurnTime.Time
	}
	return PlanListItem{Name: pl.Name, Namespace: pl.Namespace, Project: pl.Spec.ProjectRef, Title: cmp.Or(pl.Status.Title, pl.Spec.Title), Phase: pl.Status.Phase, CreatedBy: pl.Spec.CreatedBy,
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
	ap, _, plans, steer, ok := s.loadPlans(w, r)
	return ap, plans, steer, ok
}

// loadPlans is projectPlans with the project's tasks.
func (s *Server) loadPlans(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, []aiv1.AgentTask, []aiv1.AgentPlan, bool, bool) {
	if !s.cfg.ChatEnabled() {
		notFound(w)
		return nil, nil, nil, false, false
	}
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return nil, nil, nil, false, false
	}
	var list aiv1.AgentPlanList
	if err := s.reader.List(r.Context(), &list, client.InNamespace(ap.Namespace)); err != nil {
		s.internalError(w, err)
		return nil, nil, nil, false, false
	}
	plans := slices.DeleteFunc(list.Items, func(pl aiv1.AgentPlan) bool { return pl.Spec.ProjectRef != ap.Name })
	sort.SliceStable(plans, func(i, j int) bool { return plans[j].CreationTimestamp.Before(&plans[i].CreationTimestamp) })
	return ap, tasks, plans, s.planOwner(principalOf(r), ap, tasks), true
}

// plans lists a project's plans.
func (s *Server) plans(w http.ResponseWriter, r *http.Request) {
	ap, tasks, plans, steer, ok := s.loadPlans(w, r)
	if !ok {
		return
	}
	out := []PlanListItem{}
	for i := range plans {
		out = append(out, s.planItem(&plans[i], steer))
	}
	s.setPlanMilestones(r.Context(), out, map[string]*aiv1.AgentProject{ap.Namespace + "/" + ap.Name: ap}, tasks)
	writeJSON(w, http.StatusOK, out)
}

// plan returns one plan with its draft and its milestone's row.
func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	ap, tasks, plans, steer, ok := s.loadPlans(w, r)
	if !ok {
		return
	}
	pl, ok := pathPlan(w, r, plans)
	if !ok {
		return
	}
	v := s.planView(pl, steer)
	items := []PlanListItem{v.PlanListItem}
	s.setPlanMilestones(r.Context(), items, map[string]*aiv1.AgentProject{ap.Namespace + "/" + ap.Name: ap}, tasks)
	v.PlanListItem = items[0]
	writeJSON(w, http.StatusOK, v)
}

// setPlanMilestones sets the items' milestone from the milestone row with
// the same namespace, project and IID. The rows are read only when an item
// of a visible project created a milestone, and only for those projects.
func (s *Server) setPlanMilestones(ctx context.Context, items []PlanListItem, visible map[string]*aiv1.AgentProject, tasks []aiv1.AgentTask) {
	projects := map[string]*aiv1.AgentProject{}
	for _, it := range items {
		if k := it.Namespace + "/" + it.Project; it.CreatedMilestoneIID != 0 && visible[k] != nil {
			projects[k] = visible[k]
		}
	}
	if len(projects) == 0 {
		return
	}
	rows, _ := s.mergedMilestoneRows(ctx, projects, tasks, time.Now())
	byKey := make(map[milestoneKey]*MilestoneRow, len(rows))
	for i := range rows {
		byKey[milestoneKey{rows[i].Namespace, rows[i].Project, rows[i].IID}] = &rows[i]
	}
	for i := range items {
		it := &items[i]
		if m := byKey[milestoneKey{it.Namespace, it.Project, it.CreatedMilestoneIID}]; m != nil && it.CreatedMilestoneIID != 0 {
			it.Milestone = &PlanMilestone{State: m.State, Progress: m.Progress, Stored: m.Stored}
		}
	}
}

func (s *Server) planView(pl *aiv1.AgentPlan, steer bool) PlanView {
	return PlanView{PlanListItem: s.planItem(pl, steer), Draft: pl.Status.Draft,
		DraftRevision: pl.Status.DraftRevision, DraftBy: pl.Status.DraftBy, DraftSHA: pl.Status.DraftSHA,
		Decisions: append([]aiv1.PlanDecision{}, pl.Status.Decisions...), DecisionsPending: planDecisionsPending(pl.Status.Decisions)}
}

// Limits of the plans owners start from the Desk.
const (
	maxPlanTitle   = 255
	maxPlanDraft   = 64 << 10
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
	pl, ok := pathPlan(w, r, plans)
	return pl, steer, ok
}

// pathPlan is the plan of the request path among plans, 404 without it.
func pathPlan(w http.ResponseWriter, r *http.Request, plans []aiv1.AgentPlan) (*aiv1.AgentPlan, bool) {
	i := slices.IndexFunc(plans, func(pl aiv1.AgentPlan) bool { return pl.Name == r.PathValue("plan") })
	if i < 0 {
		notFound(w)
		return nil, false
	}
	return &plans[i], true
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
	if code, msg := checkPlanAnswers(pl, req.Kind, req.Decisions); code != 0 {
		writeError(w, code, msg)
		return
	}
	id, created, ok := s.insertChat(w, r, store.ChatMessage{Namespace: pl.Namespace, Project: pl.Spec.ProjectRef, Plan: pl.Name,
		Kind: req.Kind, Body: req.Body, ClientKey: req.ClientKey, Decisions: req.Decisions})
	if !ok {
		return
	}
	s.log.Info("audit", "action", "plan.chat", "user", principalOf(r).Username, "namespace", pl.Namespace, "name", pl.Name,
		"project", pl.Spec.ProjectRef, "kind", req.Kind, "answers", len(req.Decisions), "id", id, "created", created)
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

// ownPlan is projectPlan for a change by the plan's owners, decoding the
// request into req: 404 without the writer client, 403 for others.
func (s *Server) ownPlan(w http.ResponseWriter, r *http.Request, limit int64, req any) (*aiv1.AgentPlan, bool) {
	pl, steer, ok := s.projectPlan(w, r)
	switch {
	case !ok:
	case s.writer == nil:
		notFound(w)
	case !steer:
		writeError(w, http.StatusForbidden, "only the project's owners change its plans")
	case json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(req) != nil:
		badRequest(w, "invalid request")
	default:
		return pl, true
	}
	return nil, false
}

// savePlanDraft saves the owner's edit {baseRevision, draft} as the next
// revision: 409 with the current view when the draft changed since.
func (s *Server) savePlanDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseRevision int64  `json:"baseRevision"`
		Draft        string `json:"draft"`
	}
	pl, ok := s.ownPlan(w, r, 8*maxPlanDraft, &req) // JSON escapes a byte in up to 6
	switch {
	case !ok:
	case pl.Status.Phase == aiv1.AgentPlanCreated:
		writeError(w, http.StatusConflict, "the plan has created its milestone")
	case len(req.Draft) > maxPlanDraft:
		fieldError(w, "draft", "the draft must be at most 64 KiB")
	case pl.Status.DraftRevision != req.BaseRevision:
		writeJSON(w, http.StatusConflict, s.planView(pl, true))
	default:
		base := pl.DeepCopy()
		pl.Status.Draft, pl.Status.DraftRevision, pl.Status.DraftBy = req.Draft, req.BaseRevision+1, "owner"
		err := s.writer.Status().Patch(r.Context(), pl, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
		s.planPatched(w, r, pl, err, "plan.draft", "revision", pl.Status.DraftRevision)
	}
}

// raisePlanBudget raises the plan's cap {maxBudgetUSD} above its current one,
// up to plan.maxBudgetUSDLimit.
func (s *Server) raisePlanBudget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxBudgetUSD string `json:"maxBudgetUSD"`
	}
	pl, ok := s.ownPlan(w, r, 4<<10, &req)
	if !ok {
		return
	}
	cur, limit := pl.BudgetUSD(s.cfg.Plan.MaxBudgetUSD), s.cfg.Plan.MaxBudgetUSDLimit
	usd, err := aiv1.ParseBudget(req.MaxBudgetUSD)
	if err != nil || aiv1.CostOf(usd) <= aiv1.CostOf(cur) || aiv1.CostOf(usd) > aiv1.CostOf(limit) {
		fieldError(w, "maxBudgetUSD", fmt.Sprintf("the budget must be above $%s and at most $%s", cur, limit))
		return
	}
	base := pl.DeepCopy()
	pl.Spec.MaxBudgetUSD = usd
	s.planPatched(w, r, pl, s.writer.Patch(r.Context(), pl, client.MergeFrom(base)), "plan.budget", "from", cur, "to", usd)
}

// planPatched answers the patch of pl with err: the new view and an audit
// line, 409 with the current view when the plan changed since it was read.
func (s *Server) planPatched(w http.ResponseWriter, r *http.Request, pl *aiv1.AgentPlan, err error, action string, kv ...any) {
	if apierrors.IsConflict(err) {
		if err = s.writer.Get(r.Context(), client.ObjectKeyFromObject(pl), pl); err == nil {
			writeJSON(w, http.StatusConflict, s.planView(pl, true))
			return
		}
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	s.log.Info("audit", append([]any{"action", action, "user", principalOf(r).Username, "namespace", pl.Namespace,
		"name", pl.Name, "project", pl.Spec.ProjectRef}, kv...)...)
	writeJSON(w, http.StatusOK, s.planView(pl, true))
}

// planName is plan-<yyyymmdd>-<6 random characters>.
func planName(now time.Time) string {
	return "plan-" + now.UTC().Format("20060102") + "-" + strings.ToLower(rand.Text()[:6])
}

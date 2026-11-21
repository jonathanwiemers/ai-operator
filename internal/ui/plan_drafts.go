package ui

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// removePlanDraft removes the plan's uncreated draft {key}: 404 for an
// unknown key, 409 for a created draft. When every remaining draft is
// created, the plan's phase becomes Created.
func (s *Server) removePlanDraft(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.ownPlan(w, r, 0, nil)
	if !ok {
		return
	}
	key, err := strconv.ParseInt(r.PathValue("key"), 10, 64)
	base := pl.DeepCopy()
	st := &pl.Status
	st.MoveDrafts()
	i := st.DraftIndex(key)
	switch {
	case planDraftsLocked(base) != "":
		writeError(w, http.StatusConflict, planDraftsLocked(base))
	case err != nil || i < 0:
		notFound(w)
	case st.Drafts[i].CreatedMilestoneIID != 0:
		writeError(w, http.StatusConflict, draftReadOnly(st.Drafts[i]))
	default:
		st.Drafts = slices.Delete(st.Drafts, i, i+1)
		if pl.AllDraftsCreated() {
			st.Phase = aiv1.AgentPlanCreated
		}
		err := s.writer.Status().Patch(r.Context(), pl, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
		s.planPatched(w, r, pl, err, "plan.draft.remove", "key", key)
	}
}

// orderPlanDrafts puts the plan's drafts in the order {keys}: 422 unless keys
// lists each draft once, 409 when a created draft would leave its index.
func (s *Server) orderPlanDrafts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keys []int64 `json:"keys"`
	}
	pl, ok := s.ownPlan(w, r, 4<<10, &req)
	if !ok {
		return
	}
	base := pl.DeepCopy()
	st := &pl.Status
	st.MoveDrafts()
	order := make([]aiv1.PlanDraft, 0, len(req.Keys))
	for _, k := range req.Keys {
		if i := st.DraftIndex(k); i >= 0 && !slices.ContainsFunc(order, func(d aiv1.PlanDraft) bool { return d.Key == k }) {
			order = append(order, st.Drafts[i])
		}
	}
	moved := -1
	for i, d := range st.Drafts {
		if d.CreatedMilestoneIID != 0 && slices.Index(req.Keys, d.Key) != i && moved < 0 {
			moved = i
		}
	}
	switch {
	case planDraftsLocked(base) != "":
		writeError(w, http.StatusConflict, planDraftsLocked(base))
	case len(order) != len(st.Drafts) || len(req.Keys) != len(st.Drafts):
		writeErrorResponse(w, http.StatusUnprocessableEntity, ErrorResponse{Error: "keys must list each of the plan's drafts once", Field: "keys"})
	case moved >= 0:
		writeError(w, http.StatusConflict, fmt.Sprintf("the draft is created as %%%d and cannot move", st.Drafts[moved].CreatedMilestoneIID))
	default:
		st.Drafts = order
		err := s.writer.Status().Patch(r.Context(), pl, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
		s.planPatched(w, r, pl, err, "plan.draft.order", "keys", req.Keys)
	}
}

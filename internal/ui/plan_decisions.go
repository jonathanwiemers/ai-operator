package ui

import (
	"context"
	"net/http"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// PlanDecisionHistoryView is the stored history of a plan's decisions, by
// decision number, dropped decisions included. Available is false without
// the history database.
type PlanDecisionHistoryView struct {
	Available bool                      `json:"available"`
	Decisions []PlanDecisionHistoryItem `json:"decisions"`
}

// PlanDecisionHistoryItem is one decision with its versions, newest first.
type PlanDecisionHistoryItem struct {
	ID       string                    `json:"id"`
	Versions []PlanDecisionVersionView `json:"versions"`
}

// PlanDecisionVersionView is one version of a decision with its sign-offs,
// newest first. DecidedBy is "agent" or the owner's OIDC subject.
type PlanDecisionVersionView struct {
	Title         string                 `json:"title"`
	Question      string                 `json:"question"`
	Options       []string               `json:"options"`
	Recommended   int                    `json:"recommended"`
	Why           string                 `json:"why"`
	State         aiv1.PlanDecisionState `json:"state"`
	Version       int64                  `json:"version"`
	DecidedBy     string                 `json:"decidedBy"`
	DecidedByName string                 `json:"decidedByName"`
	DecidedAt     time.Time              `json:"decidedAt"`
	SignOffs      []PlanSignOffView      `json:"signOffs"`
}

// PlanSignOffView is one owner's sign-off of a version: By is the owner's
// OIDC subject.
type PlanSignOffView struct {
	By     string    `json:"by"`
	ByName string    `json:"byName"`
	At     time.Time `json:"at"`
}

type planDecisionReader interface {
	PlanDecisionHistory(ctx context.Context, namespace, project, plan string) ([]store.PlanDecisionVersion, error)
}

// planDecisionHistory returns the stored history of the plan's decisions to
// anyone who may read the plan.
func (s *Server) planDecisionHistory(w http.ResponseWriter, r *http.Request) {
	pl, sc, ok := s.projectPlan(w, r)
	if !ok {
		return
	}
	if sc.Set != "" {
		writeError(w, http.StatusConflict, planWaiting)
		return
	}
	var st planDecisionReader = s.planDecisionReads
	if db := s.research.Load(); st == nil && db != nil {
		st = db
	}
	if st == nil {
		writeJSON(w, http.StatusOK, PlanDecisionHistoryView{Decisions: []PlanDecisionHistoryItem{}})
		return
	}
	rows, err := st.PlanDecisionHistory(r.Context(), pl.Namespace, pl.Spec.ProjectRef, pl.Name)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PlanDecisionHistoryView{Available: true, Decisions: planDecisionHistoryItems(rows)})
}

// planDecisionHistoryItems groups the store's rows, which come by decision
// and newest version first, by decision ID, keeping their order.
func planDecisionHistoryItems(rows []store.PlanDecisionVersion) []PlanDecisionHistoryItem {
	out := []PlanDecisionHistoryItem{}
	for _, v := range rows {
		if n := len(out); n == 0 || out[n-1].ID != v.ID {
			out = append(out, PlanDecisionHistoryItem{ID: v.ID, Versions: []PlanDecisionVersionView{}})
		}
		signOffs := make([]PlanSignOffView, 0, len(v.SignOffs))
		for _, o := range v.SignOffs {
			signOffs = append(signOffs, PlanSignOffView{By: o.By, ByName: o.ByName, At: o.At})
		}
		it := &out[len(out)-1]
		it.Versions = append(it.Versions, PlanDecisionVersionView{Title: v.Title, Question: v.Question,
			Options: append([]string{}, v.Options...), Recommended: v.Recommended, Why: v.Why, State: aiv1.PlanDecisionState(v.State),
			Version: v.Version, DecidedBy: v.DecidedBy, DecidedByName: v.DecidedByName, DecidedAt: v.DecidedAt, SignOffs: signOffs})
	}
	return out
}

// planDecisionsPending counts the decisions that are open or whose current
// version is not signed off.
func planDecisionsPending(list []aiv1.PlanDecision) int {
	n := 0
	for _, d := range list {
		if d.State == aiv1.PlanDecisionOpen || d.SignedOffBy == "" {
			n++
		}
	}
	return n
}

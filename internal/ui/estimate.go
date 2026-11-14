package ui

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// EstimateView is the estimated cost of a plan's draft or a milestone.
// Reason says why there is none (NotEnoughHistory).
type EstimateView struct {
	store.EstimateResult `tstype:",extends"`
	Reason               string `json:"reason,omitempty"`
}

type estimateReader interface {
	MilestoneSpendHistory(ctx context.Context, project store.ProjectKey, limit int) ([]store.MilestoneSpend, error)
	MilestoneRequirements(ctx context.Context, key store.ProjectKey, iid int64) (int, error)
}

// estimate estimates ?plan=<name> (its saved draft's requirements) or
// ?milestone=<iid> (its planned issues, else its requirements) from the
// project's last 10 finished milestones.
func (s *Server) estimate(w http.ResponseWriter, r *http.Request) {
	plan, m := r.URL.Query().Get("plan"), r.URL.Query().Get("milestone")
	iid, perr := strconv.ParseInt(m, 10, 64)
	var st estimateReader = s.estimateReads
	if db := s.research.Load(); st == nil && db != nil {
		st = db
	}
	switch {
	case !s.cfg.Research.Enabled():
		notFound(w)
		return
	case (plan == "") == (m == "") || m != "" && (perr != nil || iid <= 0):
		badRequest(w, "estimate needs either plan or milestone (an IID)")
		return
	case st == nil:
		writeError(w, http.StatusServiceUnavailable, "the history database is not connected yet")
		return
	}
	var ap *aiv1.AgentProject
	var ok bool
	unit, n, err := store.UnitRequirement, 0, error(nil)
	if plan != "" {
		var plans []aiv1.AgentPlan
		if ap, plans, _, ok = s.projectPlans(w, r); !ok {
			return
		} else if i := slices.IndexFunc(plans, func(pl aiv1.AgentPlan) bool { return pl.Name == plan }); i < 0 {
			notFound(w)
			return
		} else {
			n = rules.CountRequirements(plans[i].Status.Draft)
		}
	} else {
		var tasks []aiv1.AgentTask
		if ap, tasks, ok = s.loadProject(w, r); !ok {
			return
		}
		for i := range tasks {
			t := &tasks[i]
			if t.Spec.Type == aiv1.TaskTypeSpec && t.Spec.Milestone != nil && t.Spec.Milestone.IID == iid && len(t.Status.Plan) > 0 {
				unit, n = store.UnitIssue, len(t.Status.Plan)
			}
		}
	}
	key := store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}
	if unit == store.UnitRequirement && plan == "" {
		n, err = st.MilestoneRequirements(r.Context(), key, iid)
	}
	var hist []store.MilestoneSpend
	if err == nil && n > 0 {
		hist, err = st.MilestoneSpendHistory(r.Context(), key, 10)
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	v := EstimateView{EstimateResult: store.Estimate(hist, unit, n)}
	switch {
	case n == 0:
		v.Reason = "the requirements (R lines) are unknown"
	case v.NotEnoughHistory:
		v.Reason = "not enough history: fewer than 3 finished milestones with a known cost"
	}
	writeJSON(w, http.StatusOK, v)
}

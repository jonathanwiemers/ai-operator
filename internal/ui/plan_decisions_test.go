package ui

import (
	"context"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const shopPlanDecisions = shopPlans + "/shop-plan-new/decisions"

func TestPlanViewDecisions(t *testing.T) {
	at := metav1.NewTime(time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC))
	s := planEnv(t, true, "devs").server
	pl := &aiv1.AgentPlan{Status: aiv1.AgentPlanStatus{Decisions: []aiv1.PlanDecision{
		{ID: "D1", State: aiv1.PlanDecisionOpen, Version: 1, DecidedBy: "agent"},
		{ID: "D2", State: aiv1.PlanDecisionLocked, Version: 2, DecidedBy: "agent"},
		{ID: "D3", State: aiv1.PlanDecisionLocked, Version: 1, DecidedBy: "ada", SignedOffBy: "ada", SignedOffAt: &at},
	}}}
	v := s.planView(pl, false)
	if v.DecisionsPending != 2 {
		t.Errorf("pending = %d, want 2 (open D1, unsigned D2)", v.DecisionsPending)
	}
	if len(v.Decisions) != 3 || v.Decisions[0].ID != "D1" || v.Decisions[1].ID != "D2" || v.Decisions[2].ID != "D3" {
		t.Errorf("decisions = %+v, want D1, D2, D3 in status order", v.Decisions)
	}

	// An open entry that carries a sign-off still counts once.
	pl.Status.Decisions[0].SignedOffBy = "ada"
	if v := s.planView(pl, false); v.DecisionsPending != 2 {
		t.Errorf("pending with a signed-off open entry = %d, want 2", v.DecisionsPending)
	}

	empty := s.planView(&aiv1.AgentPlan{}, false)
	sameJSON(t, struct {
		Decisions []aiv1.PlanDecision `json:"decisions"`
		Pending   int                 `json:"decisionsPending"`
	}{empty.Decisions, empty.DecisionsPending}, `{"decisions":[],"decisionsPending":0}`)
}

type fakePlanDecisions struct {
	rows []store.PlanDecisionVersion
	got  []string
}

func (f *fakePlanDecisions) PlanDecisionHistory(_ context.Context, namespace, project, plan string) ([]store.PlanDecisionVersion, error) {
	f.got = []string{namespace, project, plan}
	return f.rows, nil
}

func TestPlanDecisionHistory(t *testing.T) {
	at := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	version := func(id string, v int64, by string, offs ...store.PlanDecisionSignOff) store.PlanDecisionVersion {
		return store.PlanDecisionVersion{PlanDecision: store.PlanDecision{ID: id, Version: v, Title: id + " title",
			Question: "Q?", Options: []string{"a", "b"}, Recommended: 1, Why: "because", State: "locked",
			DecidedBy: by, DecidedAt: at.Add(time.Duration(v) * time.Hour)}, SignOffs: offs}
	}
	fake := &fakePlanDecisions{rows: []store.PlanDecisionVersion{
		version("D1", 2, "ada", store.PlanDecisionSignOff{By: "bea", ByName: "Bea", At: at.Add(5 * time.Hour)},
			store.PlanDecisionSignOff{By: "ada", ByName: "Ada", At: at.Add(3 * time.Hour)}),
		version("D1", 1, "agent"),
		version("D2", 1, "agent"),
	}}
	env := planEnv(t, true, "devs")
	env.server.planDecisionReads = fake

	var got PlanDecisionHistoryView
	if code := env.get(t, shopPlanDecisions, &got); code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	if fake.got == nil || fake.got[0] != "team-a" || fake.got[1] != "shop" || fake.got[2] != "shop-plan-new" {
		t.Errorf("store read %v, want team-a/shop/shop-plan-new", fake.got)
	}
	sameJSON(t, got, `{"available":true,"decisions":[
		{"id":"D1","versions":[
			{"title":"D1 title","question":"Q?","options":["a","b"],"recommended":1,"why":"because","state":"locked","version":2,
				"decidedBy":"ada","decidedByName":"","decidedAt":"2026-11-18T11:00:00Z","signOffs":[
				{"by":"bea","byName":"Bea","at":"2026-11-18T14:00:00Z"},{"by":"ada","byName":"Ada","at":"2026-11-18T12:00:00Z"}]},
			{"title":"D1 title","question":"Q?","options":["a","b"],"recommended":1,"why":"because","state":"locked","version":1,
				"decidedBy":"agent","decidedByName":"","decidedAt":"2026-11-18T10:00:00Z","signOffs":[]}]},
		{"id":"D2","versions":[
			{"title":"D2 title","question":"Q?","options":["a","b"],"recommended":1,"why":"because","state":"locked","version":1,
				"decidedBy":"agent","decidedByName":"","decidedAt":"2026-11-18T10:00:00Z","signOffs":[]}]}]}`)

	fake.got = nil
	for _, path := range []string{shopPlans + "/billing-plan/decisions", shopPlans + "/nope/decisions",
		"/api/v1/projects/team-b/billing/plans/billing-plan/decisions"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, code)
		}
	}
	if fake.got != nil {
		t.Errorf("another project's plan reached the store: %v", fake.got)
	}

	fake.rows = nil
	got = PlanDecisionHistoryView{}
	if code := env.get(t, shopPlanDecisions, &got); code != http.StatusOK || !got.Available || got.Decisions == nil || len(got.Decisions) != 0 {
		t.Errorf("no rows: %d %+v, want available with []", code, got)
	}
}

func TestPlanDecisionHistoryWithoutDatabase(t *testing.T) {
	env := planEnv(t, true, "devs")
	code, body := env.getJSON(t, shopPlanDecisions)
	if code != http.StatusOK || body["available"] != false {
		t.Fatalf("status %d, body %v", code, body)
	}
	if ds, ok := body["decisions"].([]any); !ok || len(ds) != 0 {
		t.Errorf("decisions %v, want []", body["decisions"])
	}
}

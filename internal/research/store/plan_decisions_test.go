package store_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

var roadmap = store.PlanKey{Namespace: "agents", Project: "orbit", Plan: "orbit-plan-1"}

// planDecision returns version v of decision id by the agent, with text
// derived from both.
func planDecision(id string, v int64) store.PlanDecisionRow {
	at := posted.Add(time.Duration(v) * time.Hour)
	return store.PlanDecisionRow{PlanDecision: store.PlanDecision{
		ID: id, Version: v, Title: fmt.Sprintf("Title of %s v%d", id, v), Question: "Question of " + id,
		Options: []string{"A of " + id, "B of " + id}, Recommended: 1, Why: "Why of " + id, State: "locked",
		DecidedBy: "agent", DecidedAt: at,
	}}
}

// signedOff returns d signed off by owner at.
func signedOff(d store.PlanDecisionRow, owner string, at time.Time) store.PlanDecisionRow {
	d.SignOff = &store.PlanDecisionSignOff{By: owner, ByName: "Name of " + owner, At: at}
	return d
}

func savePlanDecisions(t *testing.T, s *store.Store, key store.PlanKey, exp time.Time, list ...store.PlanDecisionRow) {
	t.Helper()
	if err := s.SavePlanDecisions(t.Context(), key, list, exp); err != nil {
		t.Fatalf("SavePlanDecisions = %v", err)
	}
}

func planHistory(t *testing.T, s *store.Store, key store.PlanKey) []store.PlanDecisionVersion {
	t.Helper()
	got, err := s.PlanDecisionHistory(t.Context(), key.Namespace, key.Project, key.Plan)
	if err != nil {
		t.Fatalf("PlanDecisionHistory = %v", err)
	}
	return got
}

// versionsOf lists the history as "ID vN: signer, …" lines.
func versionsOf(h []store.PlanDecisionVersion) []string {
	out := []string{}
	for _, v := range h {
		line := fmt.Sprintf("%s v%d:", v.ID, v.Version)
		for _, o := range v.SignOffs {
			line += " " + o.By
		}
		out = append(out, line)
	}
	return out
}

func wantHistory(t *testing.T, what string, got []store.PlanDecisionVersion, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if g := versionsOf(got); !reflect.DeepEqual(g, want) {
		t.Errorf("%s: history %q, want %q", what, g, want)
	}
}

func TestPlanDecisionsVersionsAndSignOffs(t *testing.T) {
	s := storetest.New(t)
	d1, d2 := planDecision("D1", 1), planDecision("D2", 1)
	d1.DecidedByName = "agent name"
	savePlanDecisions(t, s, roadmap, future(), d1, d2)

	// The next turn changes D2: its new version is decided and signed off by
	// owner-x, who also keeps D1.
	d2v2 := planDecision("D2", 2)
	d2v2.DecidedBy, d2v2.DecidedByName = "owner-x", "Owner X"
	keptX := posted.Add(10 * time.Hour)
	savePlanDecisions(t, s, roadmap, future(), signedOff(d1, "owner-x", keptX), signedOff(d2v2, "owner-x", keptX))

	// Owner Y keeps D1 later: the status now names only Y.
	keptY := keptX.Add(time.Hour)
	savePlanDecisions(t, s, roadmap, future(), signedOff(d1, "owner-y", keptY), signedOff(d2v2, "owner-x", keptX))

	got := planHistory(t, s, roadmap)
	wantHistory(t, "after three turns", got, "D1 v1: owner-y owner-x", "D2 v2: owner-x", "D2 v1:")

	// The content and sign-offs read back as stored.
	if want := (store.PlanDecisionVersion{PlanDecision: d1.PlanDecision, SignOffs: []store.PlanDecisionSignOff{
		*signedOff(d1, "owner-y", keptY).SignOff, *signedOff(d1, "owner-x", keptX).SignOff,
	}}); !reflect.DeepEqual(normalize(got[0]), normalize(want)) {
		t.Errorf("D1 v1 = %+v, want %+v", got[0], want)
	}
	if got[1].DecidedBy != "owner-x" || got[1].DecidedByName != "Owner X" || got[1].Title != "Title of D2 v2" {
		t.Errorf("D2 v2 = %+v, want decided by owner-x with its own title", got[1])
	}

	// Another plan of the project has its own history.
	other := roadmap
	other.Plan = "orbit-plan-2"
	wantHistory(t, "another plan", planHistory(t, s, other))
}

// normalize makes the times of v comparable with reflect.DeepEqual.
func normalize(v store.PlanDecisionVersion) store.PlanDecisionVersion {
	v.DecidedAt = v.DecidedAt.UTC()
	v.SignOffs = append([]store.PlanDecisionSignOff(nil), v.SignOffs...)
	for i := range v.SignOffs {
		v.SignOffs[i].At = v.SignOffs[i].At.UTC()
	}
	return v
}

func TestPlanDecisionsIdempotent(t *testing.T) {
	s := storetest.New(t)
	list := []store.PlanDecisionRow{signedOff(planDecision("D1", 1), "owner-x", posted), planDecision("D2", 3)}
	savePlanDecisions(t, s, roadmap, future(), list...)
	before := planHistory(t, s, roadmap)
	savePlanDecisions(t, s, roadmap, future(), list...)
	if c := count(t, s.Pool(), "plan_decision_versions"); c != 2 {
		t.Errorf("plan_decision_versions has %d rows, want 2", c)
	}
	if c := count(t, s.Pool(), "plan_decision_signoffs"); c != 1 {
		t.Errorf("plan_decision_signoffs has %d rows, want 1", c)
	}
	if after := planHistory(t, s, roadmap); !reflect.DeepEqual(after, before) {
		t.Errorf("history changed on a second save:\n%+v\nwant\n%+v", after, before)
	}
	if err := s.SavePlanDecisions(t.Context(), roadmap, nil, future()); err != nil {
		t.Errorf("SavePlanDecisions(nil) = %v", err)
	}
}

func TestPlanDecisionsVersionContentImmutable(t *testing.T) {
	s := storetest.New(t)
	d := planDecision("D1", 1)
	savePlanDecisions(t, s, roadmap, future(), d)
	// Same version: only a new why is taken.
	changed := d
	changed.Title, changed.Why, changed.Options = "Another title", "A better why", []string{"X", "Y", "Z"}
	savePlanDecisions(t, s, roadmap, future(), changed)
	got := planHistory(t, s, roadmap)
	if len(got) != 1 || got[0].Title != d.Title || got[0].Why != "A better why" || !reflect.DeepEqual(got[0].Options, d.Options) {
		t.Errorf("history = %+v, want the first content with the new why", got)
	}
}

func TestPlanDecisionHistoryOrder(t *testing.T) {
	s := storetest.New(t)
	// Saved out of order; D10 sorts after D2 by number.
	d10, d2v1, d2v2, d1 := planDecision("D10", 1), planDecision("D2", 1), planDecision("D2", 2), planDecision("D1", 4)
	d1.State = "open"
	savePlanDecisions(t, s, roadmap, future(), d10, d2v2)
	savePlanDecisions(t, s, roadmap, future(), d2v1, d1)
	wantHistory(t, "order", planHistory(t, s, roadmap), "D1 v4:", "D2 v2:", "D2 v1:", "D10 v1:")
}

func TestPlanDecisionsRejectsUnknownState(t *testing.T) {
	s := storetest.New(t)
	d := planDecision("D1", 1)
	d.State = "maybe"
	if err := s.SavePlanDecisions(t.Context(), roadmap, []store.PlanDecisionRow{d, planDecision("D2", 1)}, future()); err == nil {
		t.Fatal("SavePlanDecisions with state maybe: want an error")
	}
	// Nothing of the batch is stored.
	wantHistory(t, "after a failed save", planHistory(t, s, roadmap))
}

func TestPlanDecisionsExpiry(t *testing.T) {
	s := storetest.New(t)
	now := time.Now()
	past := now.Add(-time.Hour)
	savePlanDecisions(t, s, roadmap, past, signedOff(planDecision("D1", 1), "owner-x", posted))
	savePlanDecisions(t, s, roadmap, future(), planDecision("D2", 1), signedOff(planDecision("D1", 1), "owner-y", posted))

	// Expired rows are not read: D1 v1 itself expired.
	wantHistory(t, "before retention", planHistory(t, s, roadmap), "D2 v1:")

	n, err := s.DeleteExpiredPlanDecisions(t.Context(), now)
	if err != nil || n != 2 {
		t.Fatalf("DeleteExpiredPlanDecisions = %d, %v; want 2 (a version and a sign-off)", n, err)
	}
	if c := count(t, s.Pool(), "plan_decision_versions"); c != 1 {
		t.Errorf("plan_decision_versions has %d rows, want 1", c)
	}
	if c := count(t, s.Pool(), "plan_decision_signoffs"); c != 1 {
		t.Errorf("plan_decision_signoffs has %d rows, want 1", c)
	}
	if n, err := s.DeleteExpiredPlanDecisions(t.Context(), now); err != nil || n != 0 {
		t.Errorf("second DeleteExpiredPlanDecisions = %d, %v; want 0", n, err)
	}

	// More than a batch.
	var many []store.PlanDecisionRow
	for i := range 1001 {
		many = append(many, planDecision("D3", int64(i+1)))
	}
	savePlanDecisions(t, s, roadmap, past, many...)
	if n, err := s.DeleteExpiredPlanDecisions(t.Context(), now); err != nil || n != 1001 {
		t.Errorf("DeleteExpiredPlanDecisions = %d, %v; want 1001", n, err)
	}
}

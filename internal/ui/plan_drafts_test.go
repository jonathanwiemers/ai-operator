package ui

import (
	"context"
	"net/http"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// withDrafts gives shop-plan-new three drafts: key 1 created as %41, key 2
// at revision 3 with two requirements and key 4 at revision 1 (key 3 was
// removed).
func withDrafts(t *testing.T, env *testEnv) {
	t.Helper()
	pl := env.getPlan(t, "shop-plan-new")
	pl.Status.Drafts, pl.Status.DraftSeq, pl.Status.CreatedMilestoneIID = []aiv1.PlanDraft{
		{Key: 1, Title: "One", Draft: "## One", Revision: 2, By: "agent", CreatedMilestoneIID: 41, CreatedMilestoneURL: "https://gitlab.example.com/m/41"},
		{Key: 2, Title: "Two", Draft: "- **R1** a\n- **R2** b\n", Revision: 3, By: "agent", SHA: "abc"},
		{Key: 4, Title: "Four", Draft: "## Four", Revision: 1, By: "owner"},
	}, 4, 41
	if err := env.reader.Status().Update(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
}

type keyedEdit struct {
	Key          int64   `json:"key,omitempty"`
	BaseRevision int64   `json:"baseRevision"`
	Draft        string  `json:"draft"`
	Title        *string `json:"title,omitempty"`
}

func draftKeys(v PlanView) []int64 {
	var keys []int64
	for _, d := range v.Drafts {
		keys = append(keys, d.Key)
	}
	return keys
}

func TestPlanViewDrafts(t *testing.T) {
	env := planWriterEnv(t, "leads")
	withDrafts(t, env)
	var v PlanView
	if code := env.get(t, shopPlans+"/shop-plan-new", &v); code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	if k := draftKeys(v); len(k) != 3 || k[0] != 1 || k[1] != 2 || k[2] != 4 {
		t.Errorf("keys %v", k)
	}
	if d := v.Drafts[1]; d.Title != "Two" || d.Revision != 3 || d.By != "agent" || d.SHA != "abc" || d.CreatedMilestoneIID != 0 {
		t.Errorf("draft 2: %+v", d)
	}
	if d := v.Drafts[0]; d.CreatedMilestoneIID != 41 || d.CreatedMilestoneURL != "https://gitlab.example.com/m/41" {
		t.Errorf("draft 1: %+v", d)
	}
	code, list := shopPlanList(t, env)
	if code != http.StatusOK || len(list) != 2 {
		t.Fatalf("list: %d", code)
	}
	if l := list["shop-plan-new"]; l.DraftsTotal != 3 || l.DraftsCreated != 1 || len(l.CreatedMilestones) != 1 ||
		l.CreatedMilestones[0] != (PlanCreatedMilestone{41, "https://gitlab.example.com/m/41"}) || l.CreatedMilestoneIID != 41 {
		t.Errorf("list item: %+v", l)
	}

	// A plan without drafts has empty lists, not null.
	var empty PlanView
	pl := env.getPlan(t, "shop-plan-new")
	pl.Status = aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanIdle}
	if err := env.reader.Status().Update(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	if code := env.get(t, shopPlans+"/shop-plan-new", &empty); code != http.StatusOK || empty.Drafts == nil || empty.CreatedMilestones == nil {
		t.Errorf("empty: %d %+v", code, empty)
	}
}

func TestSavePlanDraftByKey(t *testing.T) {
	env := planWriterEnv(t, "leads")
	withDrafts(t, env)
	title := "  Second  "
	var v PlanView
	if code := env.send(t, http.MethodPut, newDraft, keyedEdit{4, 1, "## Four, edited", &title}, &v); code != http.StatusOK {
		t.Fatalf("edit 4: %d", code)
	}
	if d := v.Drafts[2]; d.Draft != "## Four, edited" || d.Revision != 2 || d.By != "owner" || d.Title != "Second" {
		t.Errorf("draft 4: %+v", d)
	}
	if d := v.Drafts[1]; d.Revision != 3 || d.Title != "Two" {
		t.Errorf("draft 2 changed: %+v", d)
	}

	// A stale revision of draft 2 gets the current view; nothing changes.
	var stale PlanView
	if code := env.send(t, http.MethodPut, newDraft, keyedEdit{2, 2, "stale", nil}, &stale); code != http.StatusConflict ||
		len(stale.Drafts) != 3 || stale.Drafts[1].Revision != 3 {
		t.Fatalf("stale: %d %+v", code, stale)
	}
	if pl := env.getPlan(t, "shop-plan-new"); pl.Status.Drafts[1].Draft == "stale" || pl.Status.Drafts[0].Revision != 2 {
		t.Errorf("stale edit saved: %+v", pl.Status.Drafts)
	}

	// Without a key the edit goes to the mirrored draft, key 2.
	if code := env.send(t, http.MethodPut, newDraft, keyedEdit{0, 3, "## Two", nil}, &v); code != http.StatusOK || v.Drafts[1].Revision != 4 || v.Drafts[1].Title != "Two" {
		t.Errorf("no key: %d %+v", code, v.Drafts)
	}

	var out map[string]any
	if code := env.send(t, http.MethodPut, newDraft, keyedEdit{1, 2, "late", nil}, &out); code != http.StatusConflict || out["error"] != "the draft is created as %41 and read-only" {
		t.Errorf("created draft: %d %v", code, out)
	}
	if code := env.send(t, http.MethodPut, newDraft, keyedEdit{3, 0, "x", nil}, nil); code != http.StatusNotFound {
		t.Errorf("removed key: %d", code)
	}
	bad := "a\nb"
	if code := env.send(t, http.MethodPut, newDraft, keyedEdit{4, 2, "x", &bad}, &out); code != http.StatusBadRequest || out["field"] != "title" {
		t.Errorf("two-line title: %d %v", code, out)
	}
}

// An old plan's single draft is edited as key 1 without a key, and moved
// into status.drafts.
func TestSavePlanDraftOldPlan(t *testing.T) {
	env := planWriterEnv(t, "leads")
	pl := env.getPlan(t, "shop-plan-new")
	pl.Status.Draft, pl.Status.DraftRevision, pl.Status.DraftBy, pl.Status.Title = "## Old", 2, "agent", "Old"
	if err := env.reader.Status().Update(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	var v PlanView
	if code := env.get(t, shopPlans+"/shop-plan-new", &v); code != http.StatusOK || len(v.Drafts) != 1 || v.Drafts[0].Key != 1 || v.Drafts[0].Title != "Old" {
		t.Fatalf("old view: %d %+v", code, v.Drafts)
	}
	if code := env.send(t, http.MethodPut, newDraft, draftEdit{2, "## New"}, &v); code != http.StatusOK || v.Drafts[0].Revision != 3 {
		t.Fatalf("save: %d %+v", code, v)
	}
	if s := env.getPlan(t, "shop-plan-new").Status; len(s.Drafts) != 1 || s.Drafts[0].Draft != "## New" || s.Drafts[0].Title != "Old" || s.Draft != "" || s.DraftSeq != 1 {
		t.Errorf("status: %+v", s)
	}
}

func TestEstimateByDraft(t *testing.T) {
	env, _ := estimateEnv(t)
	withDrafts(t, env)
	for q, want := range map[string]int{"&draft=2": 2, "&draft=4": 0, "": 2} {
		var got EstimateView
		if code := env.get(t, shopEstimate+"?plan=shop-plan-new"+q, &got); code != http.StatusOK || got.Count != want || got.Unit != store.UnitRequirement {
			t.Errorf("%q: %d %+v", q, code, got)
		}
	}
	for q, want := range map[string]int{"&draft=3": http.StatusNotFound, "&draft=x": http.StatusBadRequest, "&draft=0": http.StatusBadRequest} {
		if code := env.get(t, shopEstimate+"?plan=shop-plan-new"+q, nil); code != want {
			t.Errorf("%q: %d, want %d", q, code, want)
		}
	}
}

const newDrafts = shopPlans + "/shop-plan-new/drafts"

func TestRemovePlanDraft(t *testing.T) {
	env := planWriterEnv(t, "leads")
	withDrafts(t, env)
	var out map[string]any
	if code := env.send(t, http.MethodDelete, newDrafts+"/1", nil, &out); code != http.StatusConflict || out["error"] != "the draft is created as %41 and read-only" {
		t.Errorf("created: %d %v", code, out)
	}
	for _, k := range []string{"3", "x"} {
		if code := env.send(t, http.MethodDelete, newDrafts+"/"+k, nil, nil); code != http.StatusNotFound {
			t.Errorf("key %s: %d", k, code)
		}
	}
	var v PlanView
	if code := env.send(t, http.MethodDelete, newDrafts+"/2", nil, &v); code != http.StatusOK || len(v.Drafts) != 2 || v.Drafts[1].Key != 4 || v.Phase != aiv1.AgentPlanIdle {
		t.Fatalf("remove 2: %d %+v", code, v)
	}
	// Removing the last uncreated draft finishes the plan.
	if code := env.send(t, http.MethodDelete, newDrafts+"/4", nil, &v); code != http.StatusOK || len(v.Drafts) != 1 || v.Phase != aiv1.AgentPlanCreated {
		t.Fatalf("remove 4: %d %+v", code, v)
	}
	if s := env.getPlan(t, "shop-plan-new").Status; len(s.Drafts) != 1 || s.DraftSeq != 4 || s.Phase != aiv1.AgentPlanCreated {
		t.Errorf("status: %+v", s)
	}
	if code := env.send(t, http.MethodDelete, newDrafts+"/1", nil, nil); code != http.StatusConflict {
		t.Errorf("created plan: %d", code)
	}
}

func TestOrderPlanDrafts(t *testing.T) {
	env := planWriterEnv(t, "leads")
	withDrafts(t, env)
	for _, keys := range [][]int64{{1, 4}, {1, 4, 2, 2}, {1, 4, 3}, {1, 4, 4}, {}, nil} {
		var out map[string]any
		if code := env.send(t, http.MethodPut, newDrafts+"/order", map[string]any{"keys": keys}, &out); code != http.StatusUnprocessableEntity || out["field"] != "keys" {
			t.Errorf("%v: %d %v", keys, code, out)
		}
	}
	var out map[string]any
	if code := env.send(t, http.MethodPut, newDrafts+"/order", map[string]any{"keys": []int64{2, 1, 4}}, &out); code != http.StatusConflict || out["error"] != "the draft is created as %41 and cannot move" {
		t.Errorf("created moved: %d %v", code, out)
	}
	var v PlanView
	if code := env.send(t, http.MethodPut, newDrafts+"/order", map[string]any{"keys": []int64{1, 4, 2}}, &v); code != http.StatusOK {
		t.Fatalf("order: %d", code)
	}
	if k := draftKeys(v); len(k) != 3 || k[0] != 1 || k[1] != 4 || k[2] != 2 || v.Drafts[2].Revision != 3 {
		t.Errorf("view: %v %+v", k, v.Drafts)
	}
	if s := env.getPlan(t, "shop-plan-new").Status; s.Drafts[1].Key != 4 || s.Drafts[2].Title != "Two" {
		t.Errorf("status: %+v", s.Drafts)
	}
	if code := env.send(t, http.MethodPut, newDrafts+"/order", "not an object", nil); code != http.StatusBadRequest {
		t.Errorf("invalid body: %d", code)
	}
}

func TestPlanDraftsRefusedOnFailedPlan(t *testing.T) {
	env := planWriterEnv(t, "leads")
	withDrafts(t, env)
	pl := env.getPlan(t, "shop-plan-new")
	pl.Status.Phase = aiv1.AgentPlanFailed
	if err := env.reader.Status().Update(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	for name, code := range map[string]int{
		"edit":   env.send(t, http.MethodPut, newDraft, keyedEdit{4, 1, "x", nil}, nil),
		"remove": env.send(t, http.MethodDelete, newDrafts+"/4", nil, nil),
		"order":  env.send(t, http.MethodPut, newDrafts+"/order", map[string]any{"keys": []int64{1, 4, 2}}, nil),
	} {
		if code != http.StatusConflict {
			t.Errorf("%s: %d", name, code)
		}
	}
}

func TestPlanDraftsAccess(t *testing.T) {
	for name, owners := range map[string][]string{"viewer": nil, "steer, not an owner-subject": {"bea"}} {
		t.Run(name, func(t *testing.T) {
			roles := []string{"leads"}
			if owners == nil {
				roles = []string{"devs"}
			}
			env := planWriterEnv(t, roles...)
			withDrafts(t, env)
			if owners != nil {
				setOwners(t, env, owners...)
			}
			if code := env.send(t, http.MethodDelete, newDrafts+"/4", nil, nil); code != http.StatusForbidden {
				t.Errorf("remove: %d", code)
			}
			if code := env.send(t, http.MethodPut, newDrafts+"/order", map[string]any{"keys": []int64{1, 4, 2}}, nil); code != http.StatusForbidden {
				t.Errorf("order: %d", code)
			}
			if s := env.getPlan(t, "shop-plan-new").Status; len(s.Drafts) != 3 || s.Drafts[1].Key != 2 {
				t.Errorf("plan changed: %+v", s.Drafts)
			}
		})
	}
}

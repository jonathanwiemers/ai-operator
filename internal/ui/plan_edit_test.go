package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func (e *testEnv) getPlan(t *testing.T, name string) *aiv1.AgentPlan {
	t.Helper()
	var pl aiv1.AgentPlan
	if err := e.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: name}, &pl); err != nil {
		t.Fatal(err)
	}
	return &pl
}

// send sends body as JSON with method and decodes the answer into out.
func (e *testEnv) send(t *testing.T, method, path string, body, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode
}

type draftEdit struct {
	BaseRevision int64  `json:"baseRevision"`
	Draft        string `json:"draft"`
}

const newDraft = shopPlans + "/shop-plan-new/draft"

// firstDraft is v's first draft, zero without drafts.
func firstDraft(v PlanView) PlanDraftView {
	if len(v.Drafts) == 0 {
		return PlanDraftView{}
	}
	return v.Drafts[0]
}

func TestSavePlanDraft(t *testing.T) {
	env := planWriterEnv(t, "leads")
	var got PlanView
	if code := env.send(t, http.MethodPut, newDraft, draftEdit{0, "## Goal\n\nOwner's words."}, &got); code != http.StatusOK {
		t.Fatalf("save: %d", code)
	}
	if d := firstDraft(got); len(got.Drafts) != 1 || d.Draft != "## Goal\n\nOwner's words." || d.Revision != 1 || d.By != "owner" || d.Key != 1 || !got.Permissions.Steer {
		t.Errorf("view: %+v", got)
	}
	pl := env.getPlan(t, "shop-plan-new")
	if s, d := pl.Status, planDraft(pl); d.Draft != firstDraft(got).Draft || d.Revision != 1 || d.By != "owner" || d.Key != 1 || s.DraftSeq != 1 || s.Phase != aiv1.AgentPlanIdle {
		t.Errorf("status: %+v", s)
	}

	// The next edit builds on revision 1; an edit of revision 0 is stale.
	if code := env.send(t, http.MethodPut, newDraft, draftEdit{1, "## Goal\n\nSecond."}, &got); code != http.StatusOK || firstDraft(got).Revision != 2 {
		t.Fatalf("second save: %d %+v", code, got)
	}
	var stale PlanView
	if code := env.send(t, http.MethodPut, newDraft, draftEdit{1, "stale"}, &stale); code != http.StatusConflict {
		t.Fatalf("stale: %d", code)
	}
	if d := firstDraft(stale); d.Draft != "## Goal\n\nSecond." || d.Revision != 2 || stale.Name != "shop-plan-new" {
		t.Errorf("stale answer is not the current view: %+v", stale)
	}
	if s := planDraft(env.getPlan(t, "shop-plan-new")); s.Draft != "## Goal\n\nSecond." || s.Revision != 2 {
		t.Errorf("stale edit saved: %+v", s)
	}
}

func TestSavePlanDraftRefused(t *testing.T) {
	env := planWriterEnv(t, "leads")
	var out map[string]any
	if code := env.send(t, http.MethodPut, newDraft, draftEdit{0, strings.Repeat("<", maxPlanDraft+1)}, &out); code != http.StatusBadRequest || out["field"] != "draft" {
		t.Errorf("too large: %d %v", code, out)
	}
	// 64 KiB that JSON escapes to six times the size still fit.
	if code := env.send(t, http.MethodPut, newDraft, draftEdit{0, strings.Repeat("<", maxPlanDraft)}, nil); code != http.StatusOK {
		t.Errorf("64 KiB: %d", code)
	}
	if code := env.send(t, http.MethodPut, shopPlans+"/shop-plan-old/draft", draftEdit{4, "late"}, nil); code != http.StatusConflict {
		t.Errorf("created plan: %d", code)
	}
	if code := env.send(t, http.MethodPut, newDraft, "not an object", nil); code != http.StatusBadRequest {
		t.Errorf("invalid body: %d", code)
	}
	if code := env.send(t, http.MethodPut, shopPlans+"/nope/draft", draftEdit{0, "x"}, nil); code != http.StatusNotFound {
		t.Errorf("unknown plan: %d", code)
	}
	if s := planDraft(env.getPlan(t, "shop-plan-old")); s.Draft != "# Refunds" || s.Revision != 4 {
		t.Errorf("created plan changed: %+v", s)
	}
}

// The cache may lag behind the agent's draft: the resourceVersion conflict is
// a 409 with the agent's draft, read from the API.
func TestSavePlanDraftResourceVersionConflict(t *testing.T) {
	env := planEnv(t, true, "leads")
	writer := interceptor.NewClient(env.reader.(client.WithWatch), interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			agent := env.getPlan(t, obj.GetName())
			agent.Status.Drafts, agent.Status.DraftSeq = []aiv1.PlanDraft{{Key: 1, Draft: "## Agent", Revision: 1, By: "agent"}}, 1
			if err := c.Status().Update(ctx, agent); err != nil {
				t.Fatal(err)
			}
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	})
	env.server.SetResearchWriter(writer)
	var got PlanView
	if code := env.send(t, http.MethodPut, newDraft, draftEdit{0, "## Owner"}, &got); code != http.StatusConflict {
		t.Fatalf("conflict: %d", code)
	}
	if d := firstDraft(got); d.Draft != "## Agent" || d.Revision != 1 || d.By != "agent" {
		t.Errorf("view: %+v", got)
	}
	if s := planDraft(env.getPlan(t, "shop-plan-new")); s.Draft != "## Agent" {
		t.Errorf("owner's edit overwrote the agent's: %+v", s)
	}
}

func TestRaisePlanBudget(t *testing.T) {
	env := planWriterEnv(t, "leads")
	budget := shopPlans + "/shop-plan-new/budget"
	for _, v := range []string{"5", "4.99", "20.01", "0", "-1", "ten", ""} {
		var out map[string]any
		if code := env.send(t, http.MethodPost, budget, map[string]string{"maxBudgetUSD": v}, &out); code != http.StatusBadRequest || out["field"] != "maxBudgetUSD" {
			t.Errorf("%q: %d %v", v, code, out)
		}
	}
	if code := env.send(t, http.MethodPost, budget, map[string]float64{"maxBudgetUSD": 10}, nil); code != http.StatusBadRequest {
		t.Errorf("a number: %d", code)
	}
	if b := env.getPlan(t, "shop-plan-new").Spec.MaxBudgetUSD; b != "" {
		t.Fatalf("refused raise saved: %q", b)
	}

	var got PlanView
	if code := env.send(t, http.MethodPost, budget, map[string]string{"maxBudgetUSD": "$7.5"}, &got); code != http.StatusOK || got.BudgetUSD != "7.5000" {
		t.Fatalf("raise: %d %+v", code, got)
	}
	if code := env.send(t, http.MethodPost, budget, map[string]string{"maxBudgetUSD": "7.50"}, nil); code != http.StatusBadRequest {
		t.Errorf("same cap again: %d", code)
	}
	if code := env.send(t, http.MethodPost, budget, map[string]string{"maxBudgetUSD": "20"}, &got); code != http.StatusOK || got.BudgetUSD != "20.0000" {
		t.Errorf("up to the limit: %d %+v", code, got)
	}
	if b := env.getPlan(t, "shop-plan-new").Spec.MaxBudgetUSD; b != "20.0000" {
		t.Errorf("spec: %q", b)
	}
}

func TestPlanEditAccess(t *testing.T) {
	cases := []struct {
		name   string
		chat   bool
		writer bool
		roles  []string
		owners []string
		want   int
	}{
		{"viewer", true, true, []string{"devs"}, nil, http.StatusForbidden},
		{"steer, not an owner-subject", true, true, []string{"leads"}, []string{"bea"}, http.StatusForbidden},
		{"no view of the project", true, true, nil, nil, http.StatusNotFound},
		{"without chat", false, true, []string{"leads"}, nil, http.StatusNotFound},
		{"without writer", true, false, []string{"leads"}, nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := planEnv(t, tc.chat, tc.roles...)
			if tc.writer {
				env.server.SetResearchWriter(env.reader)
			}
			if tc.owners != nil {
				setOwners(t, env, tc.owners...)
			}
			if code := env.send(t, http.MethodPut, newDraft, draftEdit{0, "x"}, nil); code != tc.want {
				t.Errorf("draft: %d, want %d", code, tc.want)
			}
			if code := env.send(t, http.MethodPost, shopPlans+"/shop-plan-new/budget", map[string]string{"maxBudgetUSD": "6"}, nil); code != tc.want {
				t.Errorf("budget: %d, want %d", code, tc.want)
			}
			if pl := env.getPlan(t, "shop-plan-new"); len(pl.DraftList()) != 0 || pl.Spec.MaxBudgetUSD != "" {
				t.Errorf("plan changed: %+v %+v", pl.Spec, pl.Status)
			}
		})
	}
}

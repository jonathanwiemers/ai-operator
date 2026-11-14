package v1alpha1

import (
	"strings"
	"testing"
)

func TestAgentPlanBudgetUSD(t *testing.T) {
	p := &AgentPlan{}
	if got := p.BudgetUSD("5"); got != "5" {
		t.Errorf("no raise: %q, want the default 5", got)
	}
	p.Spec.MaxBudgetUSD = "12.50"
	if got := p.BudgetUSD("5"); got != "12.50" {
		t.Errorf("raised: %q, want 12.50", got)
	}
}

func TestAgentPlanActive(t *testing.T) {
	for phase, want := range map[AgentPlanPhase]bool{
		"":                true,
		AgentPlanStarting: true,
		AgentPlanReady:    true,
		AgentPlanWorking:  true,
		AgentPlanIdle:     true,
		AgentPlanCreated:  false,
		AgentPlanFailed:   false,
	} {
		p := &AgentPlan{Status: AgentPlanStatus{Phase: phase}}
		if got := p.Active(); got != want {
			t.Errorf("phase %q: Active() = %v, want %v", phase, got, want)
		}
	}
}

func TestAgentPlanSchema(t *testing.T) {
	for _, path := range []string{"../../config/crd/bases/ai.wiemers.me_agentplans.yaml", "../../charts/ai-operator/files/crds/ai.wiemers.me_agentplans.yaml"} {
		crd := loadCRDFile(t, path)
		ok := map[string]any{
			"spec": map[string]any{"projectRef": "shop", "createdBy": "jo", "maxBudgetUSD": "7.5",
				"chatMessages": []any{map[string]any{"id": 1, "kind": "message", "text": "hi", "author": "jo", "at": "2026-11-14T10:00:00Z"}}},
			"status": map[string]any{"phase": "Working", "draft": "# Goal", "draftRevision": 2, "draftBy": "owner", "createdMilestoneIID": 22},
		}
		if _, errs := crd.admit(ok); len(errs) > 0 {
			t.Errorf("%s: valid plan rejected: %v", path, errs)
		}
		for name, tc := range map[string]struct {
			spec, status map[string]any
			want         string
		}{
			"no project":   {map[string]any{"createdBy": "jo"}, nil, "spec.projectRef"},
			"no creator":   {map[string]any{"projectRef": "shop"}, nil, "spec.createdBy"},
			"budget":       {map[string]any{"projectRef": "shop", "createdBy": "jo", "maxBudgetUSD": "$5"}, nil, "spec.maxBudgetUSD"},
			"messages":     {map[string]any{"projectRef": "shop", "createdBy": "jo", "chatMessages": repeat(map[string]any{"id": 1, "kind": "message", "text": "x", "author": "jo", "at": "2026-11-14T10:00:00Z"}, 21)}, nil, "spec.chatMessages"},
			"phase":        {map[string]any{"projectRef": "shop", "createdBy": "jo"}, map[string]any{"phase": "Done"}, "status.phase"},
			"draft by":     {map[string]any{"projectRef": "shop", "createdBy": "jo"}, map[string]any{"draftBy": "pilot"}, "status.draftBy"},
			"long draft":   {map[string]any{"projectRef": "shop", "createdBy": "jo"}, map[string]any{"draft": strings.Repeat("x", 65537)}, "status.draft"},
			"unknown spec": {map[string]any{"projectRef": "shop", "createdBy": "jo", "owner": "jo"}, nil, "spec.owner"},
		} {
			obj := map[string]any{"spec": tc.spec}
			if tc.status != nil {
				obj["status"] = tc.status
			}
			if _, errs := crd.admit(obj); !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Errorf("%s: %s: errors %v, want one on %s", path, name, errs, tc.want)
			}
		}
	}
}

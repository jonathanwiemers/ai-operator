package v1alpha1

import (
	"reflect"
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
		"":               true,
		AgentPlanIdle:    true,
		AgentPlanCreated: false,
		AgentPlanFailed:  false,
	} {
		p := &AgentPlan{Status: AgentPlanStatus{Phase: phase}}
		if got := p.Active(); got != want {
			t.Errorf("phase %q: Active() = %v, want %v", phase, got, want)
		}
	}
}

func TestAgentPlanActiveDrafts(t *testing.T) {
	two := func(a, b int64) []PlanDraft {
		return []PlanDraft{{Key: 1, CreatedMilestoneIID: a}, {Key: 2, CreatedMilestoneIID: b}}
	}
	for name, tc := range map[string]struct {
		st   AgentPlanStatus
		want bool
	}{
		"some created":          {AgentPlanStatus{Phase: AgentPlanIdle, Drafts: two(41, 0)}, true},
		"some created, Created": {AgentPlanStatus{Phase: AgentPlanCreated, Drafts: two(41, 0)}, true},
		"all created":           {AgentPlanStatus{Phase: AgentPlanCreated, Drafts: two(41, 42)}, false},
		"failed":                {AgentPlanStatus{Phase: AgentPlanFailed, Drafts: two(0, 0)}, false},
		"old created":           {AgentPlanStatus{Phase: AgentPlanCreated, Draft: "# Goal", CreatedMilestoneIID: 7}, false},
		"old claimed":           {AgentPlanStatus{Phase: AgentPlanCreated, Draft: "# Goal"}, false},
		"claimed, none created": {AgentPlanStatus{Phase: AgentPlanCreated, Drafts: two(0, 0)}, false},
	} {
		p := &AgentPlan{Status: tc.st}
		if got := p.Active(); got != tc.want {
			t.Errorf("%s: Active() = %v, want %v", name, got, tc.want)
		}
	}
}

// A draft being created (IID -1) is not created yet.
func TestAgentPlanAllDraftsCreated(t *testing.T) {
	for _, tc := range []struct {
		iids []int64
		want bool
	}{{nil, false}, {[]int64{41, 42}, true}, {[]int64{41, -1}, false}} {
		p := &AgentPlan{}
		for i, iid := range tc.iids {
			p.Status.Drafts = append(p.Status.Drafts, PlanDraft{Key: int64(i + 1), CreatedMilestoneIID: iid})
		}
		if got := p.AllDraftsCreated(); got != tc.want {
			t.Errorf("%v: AllDraftsCreated() = %v, want %v", tc.iids, got, tc.want)
		}
	}
}

func TestAgentPlanDraftList(t *testing.T) {
	for name, tc := range map[string]struct {
		st         AgentPlanStatus
		want       []PlanDraft
		allCreated bool
	}{
		"empty": {AgentPlanStatus{Title: "Refunds"}, nil, false},
		"drafts": {AgentPlanStatus{Drafts: []PlanDraft{{Key: 2, Draft: "a"}, {Key: 4, Draft: "b", CreatedMilestoneIID: 9}}, Draft: "stale"},
			[]PlanDraft{{Key: 2, Draft: "a"}, {Key: 4, Draft: "b", CreatedMilestoneIID: 9}}, false},
		"old draft": {AgentPlanStatus{Title: "Refunds", Draft: "# Goal", DraftRevision: 3, DraftBy: "owner", DraftSHA: "abc"},
			[]PlanDraft{{Key: 1, Title: "Refunds", Draft: "# Goal", Revision: 3, By: "owner", SHA: "abc"}}, false},
		"old milestone only": {AgentPlanStatus{CreatedMilestoneIID: 22, CreatedMilestoneURL: "https://gl/m/22"},
			[]PlanDraft{{Key: 1, CreatedMilestoneIID: 22, CreatedMilestoneURL: "https://gl/m/22"}}, true},
	} {
		p := &AgentPlan{Status: tc.st}
		if got := p.DraftList(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: DraftList() = %+v, want %+v", name, got, tc.want)
		}
		if got := p.AllDraftsCreated(); got != tc.allCreated {
			t.Errorf("%s: AllDraftsCreated() = %v, want %v", name, got, tc.allCreated)
		}
	}
}

func TestAgentPlanMoveDrafts(t *testing.T) {
	s := AgentPlanStatus{Title: "Refunds", Draft: "# Goal", DraftRevision: 3, DraftBy: "agent", DraftSHA: "abc",
		CreatedMilestoneIID: 22, CreatedMilestoneURL: "https://gl/m/22"}
	s.MoveDrafts()
	want := AgentPlanStatus{Title: "Refunds", CreatedMilestoneIID: 22, DraftSeq: 1, Drafts: []PlanDraft{{Key: 1, Title: "Refunds",
		Draft: "# Goal", Revision: 3, By: "agent", SHA: "abc", CreatedMilestoneIID: 22, CreatedMilestoneURL: "https://gl/m/22"}}}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("moved: %+v, want %+v", s, want)
	}
	s.Drafts[0].Draft = "# Goal 2"
	s.MoveDrafts()
	if want.Drafts[0].Draft = "# Goal 2"; !reflect.DeepEqual(s, want) {
		t.Fatalf("a second move must be a no-op: %+v", s)
	}
	empty := AgentPlanStatus{Title: "Refunds"}
	if empty.MoveDrafts(); empty.Drafts != nil || empty.DraftSeq != 0 {
		t.Fatalf("nothing to move: %+v", empty)
	}
}

func TestAgentPlanNextDraftKey(t *testing.T) {
	var s AgentPlanStatus
	for want := int64(1); want <= 3; want++ {
		if k := s.NextDraftKey(); k != want {
			t.Fatalf("key %d, want %d", k, want)
		}
		s.Drafts = append(s.Drafts, PlanDraft{Key: want})
	}
	s.Drafts = s.Drafts[:2] // key 3 removed
	if k := s.NextDraftKey(); k != 4 || s.DraftSeq != 4 {
		t.Fatalf("after a removal: key %d, seq %d, want 4", k, s.DraftSeq)
	}
	if i := s.DraftIndex(2); i != 1 {
		t.Errorf("DraftIndex(2) = %d, want 1", i)
	}
	if i := s.DraftIndex(3); i != -1 {
		t.Errorf("DraftIndex(3) = %d, want -1", i)
	}
}

func TestAgentPlanSchema(t *testing.T) {
	for _, path := range []string{"../../config/crd/bases/ai.wiemers.me_agentplans.yaml", "../../charts/ai-operator/files/crds/ai.wiemers.me_agentplans.yaml"} {
		crd := loadCRDFile(t, path)
		ok := map[string]any{
			"spec": map[string]any{"projectRef": "shop", "createdBy": "jo", "maxBudgetUSD": "7.5",
				"chatMessages": []any{map[string]any{"id": 1, "kind": "message", "text": "hi", "author": "jo", "at": "2026-11-14T10:00:00Z"}}},
			"status": map[string]any{"phase": "Working", "draft": "# Goal", "draftRevision": 2, "draftBy": "owner", "createdMilestoneIID": 22,
				"title": "Desk: version in the rail", "draftSeq": 5,
				"drafts": repeat(map[string]any{"key": 1, "title": "Refunds", "draft": strings.Repeat("x", 65536), "revision": 2, "by": "agent",
					"sha": "abc", "createdMilestoneIID": 41, "createdMilestoneURL": "https://gl/m/41"}, 5)},
		}
		draft := func(d map[string]any) map[string]any { return map[string]any{"drafts": []any{d}} }
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
			"long title":   {map[string]any{"projectRef": "shop", "createdBy": "jo"}, map[string]any{"title": strings.Repeat("x", 256)}, "status.title"},
			"unknown spec": {map[string]any{"projectRef": "shop", "createdBy": "jo", "owner": "jo"}, nil, "spec.owner"},
			"6 drafts":     {map[string]any{"projectRef": "shop", "createdBy": "jo"}, map[string]any{"drafts": repeat(map[string]any{"key": 1}, 6)}, "status.drafts"},
			"long drafts":  {map[string]any{"projectRef": "shop", "createdBy": "jo"}, draft(map[string]any{"key": 1, "draft": strings.Repeat("x", 65537)}), "status.drafts[0].draft"},
			"drafts title": {map[string]any{"projectRef": "shop", "createdBy": "jo"}, draft(map[string]any{"key": 1, "title": strings.Repeat("x", 256)}), "status.drafts[0].title"},
			"drafts by":    {map[string]any{"projectRef": "shop", "createdBy": "jo"}, draft(map[string]any{"key": 1, "by": "pilot"}), "status.drafts[0].by"},
			"drafts key":   {map[string]any{"projectRef": "shop", "createdBy": "jo"}, draft(map[string]any{"title": "x"}), "status.drafts[0].key"},
			"draft seq":    {map[string]any{"projectRef": "shop", "createdBy": "jo"}, map[string]any{"draftSeq": -1}, "status.draftSeq"},
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

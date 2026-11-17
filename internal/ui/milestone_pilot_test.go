package ui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestMilestonePilotActedAndInterventions(t *testing.T) {
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"},
		Spec: aiv1.AgentProjectSpec{Pilot: aiv1.PilotSpec{MaxInterventionsPerIssue: 3}}}
	st := lanesSpec(aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemActive}, aiv1.AutopilotItem{IID: 2, State: aiv1.AutopilotItemActive})
	impl := func(iid int64, sha string) aiv1.AgentTask {
		return aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop-" + string(rune('0'+iid))},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, Milestone: st.Spec.Milestone, Issue: &aiv1.IssueRef{IID: iid}},
			Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: sha}}}
	}
	older, newer := metav1.NewTime(time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)), metav1.NewTime(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	coord := aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop-pilot-m9"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeCoordinate, Milestone: st.Spec.Milestone},
		Status: aiv1.AgentTaskStatus{Pilot: &aiv1.PilotStatus{
			// Oldest first, as in status.
			Acted: []aiv1.PilotActed{
				{At: older, Summary: "retry #1", Why: "flaky", Lines: []string{"retry #1: started"}, Issues: []int64{1}, Ran: true},
				{At: newer, By: "ada", Summary: "retry #2", Issues: []int64{2}, Ran: true},
			},
			Interventions: map[string]aiv1.PilotIntervention{
				"2": {Head: "old-sha", Count: 2}, // #2 was pushed since: stale
				"1": {Head: "sha-1", Count: 2},   // on #1's current head
				"0": {Count: 1},                  // the milestone has no head
			},
		}}}
	v := milestoneView(p, []aiv1.AgentTask{st, impl(1, "sha-1"), impl(2, "new-sha"), coord}, &st, &coord, time.Now())
	pv := v.Pilot
	if pv == nil {
		t.Fatal("no pilot")
	}
	if len(pv.Acted) != 2 || pv.Acted[0].Summary != "retry #2" || pv.Acted[0].By != "ada" || pv.Acted[1].Summary != "retry #1" ||
		!slices.Equal(pv.Acted[1].Lines, []string{"retry #1: started"}) {
		t.Errorf("acted = %+v, want newest first", pv.Acted)
	}
	want := []InterventionView{{IssueIID: 0, Used: 1, Max: 3}, {IssueIID: 1, Used: 2, Max: 3}, {IssueIID: 2, Used: 0, Max: 3}}
	if !slices.Equal(pv.Interventions, want) {
		t.Errorf("interventions = %+v, want %+v", pv.Interventions, want)
	}

	// Without a pilot status: empty lists, never null, and the default max.
	coord.Status.Pilot = nil
	v = milestoneView(&aiv1.AgentProject{ObjectMeta: p.ObjectMeta}, []aiv1.AgentTask{st, coord}, &st, &coord, time.Now())
	b, err := json.Marshal(v.Pilot)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, `"acted":[]`) || !strings.Contains(s, `"interventions":[]`) {
		t.Errorf("pilot = %s, want empty lists", s)
	}
	if got := interventionViews(&aiv1.AgentProject{}, nil, &aiv1.PilotStatus{Interventions: map[string]aiv1.PilotIntervention{"0": {Count: 1}}}); got[0].Max != aiv1.DefaultPilotInterventions {
		t.Errorf("max = %d without a setting, want the default %d", got[0].Max, aiv1.DefaultPilotInterventions)
	}
}

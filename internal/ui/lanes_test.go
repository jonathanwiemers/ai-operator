package ui

import (
	"maps"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestPlanWaves(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan []aiv1.PlannedIssue
		want map[int64]int
	}{
		{"chain, listed backwards", []aiv1.PlannedIssue{{IID: 3, DependsOn: []int64{2}}, {IID: 2, DependsOn: []int64{1}}, {IID: 1}},
			map[int64]int{1: 1, 2: 2, 3: 3}},
		{"diamond", []aiv1.PlannedIssue{{IID: 1}, {IID: 2, DependsOn: []int64{1}}, {IID: 3, DependsOn: []int64{1}}, {IID: 4, DependsOn: []int64{2, 3}}},
			map[int64]int{1: 1, 2: 2, 3: 2, 4: 3}},
		{"two roots and a dependant", []aiv1.PlannedIssue{{IID: 1}, {IID: 2}, {IID: 3, DependsOn: []int64{1, 2}}},
			map[int64]int{1: 1, 2: 1, 3: 2}},
		{"cycle", []aiv1.PlannedIssue{{IID: 1, DependsOn: []int64{2}}, {IID: 2, DependsOn: []int64{1}}, {IID: 3, DependsOn: []int64{3}}},
			map[int64]int{1: 2, 2: 1, 3: 1}},
		{"dependency outside the plan", []aiv1.PlannedIssue{{IID: 1, DependsOn: []int64{99}}}, map[int64]int{1: 1}},
		{"no plan", nil, map[int64]int{}},
	} {
		if got := planWaves(tc.plan); !maps.Equal(got, tc.want) {
			t.Errorf("%s: waves = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// lanesSpec is a milestone with the plan #1, #2 (roots) and #3 (depends on
// both) and the queue items.
func lanesSpec(items ...aiv1.AutopilotItem) aiv1.AgentTask {
	return aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop-spec-m9"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 9, Title: "Lanes"}},
		Status: aiv1.AgentTaskStatus{
			Plan:      []aiv1.PlannedIssue{{IID: 1, Title: "One"}, {IID: 2, Title: "Two"}, {IID: 3, Title: "Three", DependsOn: []int64{1, 2}}},
			Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, TrackingIssueIID: 70, MaxParallel: 3, Items: items}}}
}

func lanesOf(v MilestoneView) (lanes []string, waves []int, waiting [][]int64) {
	for _, q := range v.Autopilot.Items {
		lanes, waves, waiting = append(lanes, q.Lane), append(waves, q.Wave), append(waiting, q.WaitingFor)
	}
	return lanes, waves, waiting
}

func TestMilestoneViewLanes(t *testing.T) {
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	ready := metav1.NewTime(time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC))
	hold := &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "job `lint` failed", Since: ready}

	st := lanesSpec(aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemActive, ReadyAt: &ready},
		aiv1.AutopilotItem{IID: 2, State: aiv1.AutopilotItemActive, Hold: hold},
		aiv1.AutopilotItem{IID: 3, State: aiv1.AutopilotItemPending})
	st.Status.Autopilot.Landing = 1
	v := milestoneView(p, []aiv1.AgentTask{st}, &st, nil, time.Now())
	lanes, waves, waiting := lanesOf(v)
	if !slices.Equal(lanes, []string{"landing", "held", "waiting"}) || !slices.Equal(waves, []int{1, 1, 2}) ||
		waiting[0] != nil || waiting[1] != nil || !slices.Equal(waiting[2], []int64{1, 2}) {
		t.Errorf("lanes %v, waves %v, waiting for %v", lanes, waves, waiting)
	}
	a := v.Autopilot
	if a.Running != 0 || a.MaxParallel != 3 || a.Landing != 1 || a.Items[1].Hold != hold || a.Items[0].ReadyAt != &ready {
		t.Errorf("autopilot = %+v", a)
	}

	st = lanesSpec(aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemActive, ReadyAt: &ready},
		aiv1.AutopilotItem{IID: 2, State: aiv1.AutopilotItemActive},
		aiv1.AutopilotItem{IID: 3, State: aiv1.AutopilotItemPending},
		aiv1.AutopilotItem{IID: 4, State: aiv1.AutopilotItemActive})
	v = milestoneView(p, []aiv1.AgentTask{st}, &st, nil, time.Now())
	if lanes, waves, waiting = lanesOf(v); !slices.Equal(lanes, []string{"ready", "running", "waiting", "running"}) ||
		!slices.Equal(waves, []int{1, 1, 2, 0}) || !slices.Equal(waiting[2], []int64{1, 2}) || v.Autopilot.Running != 2 {
		t.Errorf("ready and running, #4 outside the plan: lanes %v, waves %v, waiting for %v, running %d", lanes, waves, waiting, v.Autopilot.Running)
	}

	st = lanesSpec(aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemMerged},
		aiv1.AutopilotItem{IID: 2, State: aiv1.AutopilotItemSkipped},
		aiv1.AutopilotItem{IID: 3, State: aiv1.AutopilotItemPending})
	v = milestoneView(p, []aiv1.AgentTask{st}, &st, nil, time.Now())
	if lanes, _, waiting = lanesOf(v); !slices.Equal(lanes, []string{"done", "done", "waiting"}) || !slices.Equal(waiting[2], []int64{2}) {
		t.Errorf("merged and skipped: lanes %v, waiting for %v", lanes, waiting)
	}
	st.Status.Autopilot.Items[2].StartAnyway = true
	v = milestoneView(p, []aiv1.AgentTask{st}, &st, nil, time.Now())
	if _, _, waiting = lanesOf(v); waiting[2] != nil {
		t.Errorf("started anyway, the skipped one is met: waiting for %v", waiting[2])
	}
}

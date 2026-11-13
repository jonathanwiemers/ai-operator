package ui

import (
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestFinishing(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	posted := metav1.NewTime(now.Add(-time.Hour))
	withRetro := func(t aiv1.AgentTask, r *aiv1.PilotRetrospective) aiv1.AgentTask {
		t.Status.Pilot = &aiv1.PilotStatus{Retrospective: r}
		return t
	}
	coord := func(iid int64) aiv1.AgentTask {
		return aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeCoordinate, Milestone: &aiv1.MilestoneRef{IID: iid}}}
	}
	settled := &aiv1.PilotRetrospective{PostedAt: &posted}

	waits := planned(aiv1.AutopilotDone)
	waits.CloseWaitsFor, waits.CloseWaitsForCompanions = []int64{42, 43}, []string{"group/lib!7"}
	impl := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Milestone: &aiv1.MilestoneRef{IID: 6},
		Issue: &aiv1.IssueRef{IID: 42, Title: "Fix the cart", WebURL: "https://gl/i/42"}}}
	replan := queueSpec(2, planned(aiv1.AutopilotDone))
	replan.Spec.RunRequest = 1

	for _, tc := range []struct {
		name  string
		tasks []aiv1.AgentTask
		want  []FinishingEntry
	}{
		{"running milestones are not listed", []aiv1.AgentTask{
			queueSpec(3, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemActive)),
			queueSpec(4, nil),
			withRetro(queueSpec(5, planned(aiv1.AutopilotPaused)), settled),
		}, []FinishingEntry{}},
		{"open issues and companions", []aiv1.AgentTask{withRetro(queueSpec(6, waits), settled), impl},
			[]FinishingEntry{{IID: 6, Title: "M6", Task: "shop-spec-m6", OpenIssues: []FinishingIssue{
				{IID: 42, Title: "Fix the cart", WebURL: "https://gl/i/42"},
				{IID: 43, WebURL: "https://gitlab.example.com/team/shop/-/issues/43"},
			}, Companions: []string{"group/lib!7"}}}},
		{"retrospective being written", []aiv1.AgentTask{queueSpec(7, planned(aiv1.AutopilotDone)), coord(7)},
			[]FinishingEntry{{IID: 7, Title: "M7", Task: "shop-spec-m7", RetroWriting: true, OpenIssues: []FinishingIssue{}, Companions: []string{}}}},
		{"follow-ups to settle, oldest first", []aiv1.AgentTask{
			withRetro(queueSpec(8, planned(aiv1.AutopilotDone)), &aiv1.PilotRetrospective{PostedAt: &posted, FollowUps: []aiv1.PilotFollowUp{{Title: "a"}}}),
			replan,
		}, []FinishingEntry{
			{IID: 2, Title: "M2", Task: "shop-spec-m2", Replanning: true, OpenIssues: []FinishingIssue{}, Companions: []string{}},
			{IID: 8, Title: "M8", Task: "shop-spec-m8", FollowUps: true, OpenIssues: []FinishingIssue{}, Companions: []string{}},
		}},
	} {
		if got := finishing(tc.tasks, "https://gitlab.example.com/team/shop", now); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.name, got, tc.want)
		}
	}
}

package ui

import (
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestHandledOf(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *metav1.Time { m := metav1.NewTime(now.Add(-ago)); return &m }
	ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit"}}
	tasks := []aiv1.AgentTask{
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "pilot"},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeCoordinate, Milestone: &aiv1.MilestoneRef{IID: 5}},
			Status: aiv1.AgentTaskStatus{
				Prompts: []aiv1.Prompt{
					{ID: "Q1", Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 12}, Question: "Retry #12?",
						State: aiv1.PromptAnswered, ClosedAt: at(time.Hour), ClosedReason: aiv1.ClosedAnswered, ClosedBy: aiv1.ResolvedByOwner,
						Answer: &aiv1.PromptAnswer{OptionID: "retry", By: "jo"}},
					{ID: "Q2", Question: "old", State: aiv1.PromptExpired, ClosedAt: at(8 * 24 * time.Hour)},
					{ID: "Q3", Question: "open", State: aiv1.PromptOpen},
				},
				Pilot: &aiv1.PilotStatus{Acted: []aiv1.PilotActed{
					{At: *at(2 * time.Hour), Summary: "Retry #14 and #15", Issues: []int64{0, 14, 15}, Ran: true},
					{At: *at(3 * time.Hour), By: "ann", Summary: "Rework the milestone"},
					{At: *at(9 * 24 * time.Hour), Summary: "too old"},
				}},
			}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "hidden"},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "secret", Type: aiv1.TaskTypeCoordinate, Milestone: &aiv1.MilestoneRef{IID: 1}},
			Status: aiv1.AgentTaskStatus{
				Prompts: []aiv1.Prompt{{ID: "Q1", Question: "secret", State: aiv1.PromptExpired, ClosedAt: at(time.Minute)}},
				Pilot:   &aiv1.PilotStatus{Acted: []aiv1.PilotActed{{At: *at(time.Minute), Summary: "secret"}}},
			}},
	}
	items := handledOf(map[string]*aiv1.AgentProject{"agents/orbit": ap}, tasks, now)
	if len(items) != 3 {
		t.Fatalf("want Q1 and the two recent actions only (no old, open or hidden ones): %+v", items)
	}
	q := items[0]
	if q.Kind != "prompt" || q.PromptID != "Q1" || q.IssueIID != 12 || q.MilestoneIID != 5 || q.By != "jo" ||
		q.ClosedReason != aiv1.ClosedAnswered || q.ClosedBy != aiv1.ResolvedByOwner || q.Title != "Retry #12?" || q.Namespace != "agents" || q.Project != "orbit" {
		t.Errorf("answered prompt = %+v", q)
	}
	if a := items[1]; a.Kind != "acted" || a.IssueIID != 14 || a.By != "" || a.Title != "Retry #14 and #15" || a.MilestoneIID != 5 {
		t.Errorf("action gets its first issue = %+v", a)
	}
	if a := items[2]; a.Kind != "acted" || a.IssueIID != 0 || a.By != "ann" {
		t.Errorf("milestone action = %+v", a)
	}
}

func TestHandledOfKeepsNewest50(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit"}}
	var prompts []aiv1.Prompt
	for i := range 60 {
		closed := metav1.NewTime(now.Add(-time.Duration(60-i) * time.Minute))
		prompts = append(prompts, aiv1.Prompt{ID: fmt.Sprintf("Q%d", i), State: aiv1.PromptSuperseded, ClosedAt: &closed})
	}
	tasks := []aiv1.AgentTask{{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "spec"},
		Spec:   aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 5}},
		Status: aiv1.AgentTaskStatus{Prompts: prompts}}}
	items := handledOf(map[string]*aiv1.AgentProject{"agents/orbit": ap}, tasks, now)
	if len(items) != handledMax {
		t.Fatalf("len = %d, want %d", len(items), handledMax)
	}
	if items[0].PromptID != "Q59" || items[handledMax-1].PromptID != "Q10" {
		t.Errorf("first %s, last %s: want the newest first", items[0].PromptID, items[handledMax-1].PromptID)
	}
	for i := 1; i < len(items); i++ {
		if items[i].At.After(items[i-1].At) {
			t.Fatalf("not newest first at %d", i)
		}
	}
}

func TestHandledOfEmptyIsNotNil(t *testing.T) {
	if items := handledOf(nil, nil, time.Now()); items == nil {
		t.Error("handled is null")
	}
}

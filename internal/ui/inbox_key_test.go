package ui

import (
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestInboxKeyAndCategory(t *testing.T) {
	base := InboxItem{Namespace: "team", Project: "shop", Task: "shop-impl-12", MilestoneIID: 3, IssueIID: 12}
	with := func(f func(*InboxItem)) InboxItem {
		it := base
		f(&it)
		return it
	}
	cases := []struct {
		name     string
		item     InboxItem
		key      string
		category string
	}{
		{"task needs input", with(func(it *InboxItem) { it.Kind, it.Reason = "task", "needs-input" }), "team/shop/task/shop-impl-12", "question"},
		{"task failed", with(func(it *InboxItem) { it.Kind, it.Reason = "task", "failed" }), "team/shop/task/shop-impl-12", "failed"},
		{"task blocked", with(func(it *InboxItem) { it.Kind, it.Reason = "task", "blocked" }), "team/shop/task/shop-impl-12", "failed"},
		{"proposal", with(func(it *InboxItem) { it.Kind, it.ProposalID = "proposal", 4 }), "team/shop/proposal/3/4", "proposal"},
		{"prompt", with(func(it *InboxItem) { it.Kind, it.PromptID = "prompt", "Q1" }), "team/shop/prompt/shop-impl-12/Q1", "question"},
		{"milestone pause", with(func(it *InboxItem) { it.Kind, it.IssueIID, it.PauseKind = "pause", 0, aiv1.PauseKind("budget") }), "team/shop/pause/3//budget", "problem"},
		{"hold", with(func(it *InboxItem) { it.Kind, it.PauseKind, it.Hold = "pause", aiv1.PauseKind("budget"), true }), "team/shop/pause/3/12/budget/hold", "problem"},
		{"pilotError", with(func(it *InboxItem) { it.Kind, it.Title, it.Reason = "pilotError", "pilot", "denied" }), "team/shop/pilotError/shop-impl-12/pilot/denied", ""},
		{"companionReady", with(func(it *InboxItem) { it.Kind, it.Title, it.Reason = "companionReady", "group/infra!7", "approved" }),
			"team/shop/companionReady/12/group/infra!7", "companion"},
		{"budgetWarning", with(func(it *InboxItem) {
			it.Kind, it.Title, it.Reason = "budgetWarning", "Checkout", "80 % of the issue's budget of $20.00 spent"
		}), "team/shop/budgetWarning/3/12/80 % of the issue's budget of $20.00 spent", "budget"},
		{"open problem", with(func(it *InboxItem) { it.Kind, it.Reason = "problem", "question: Split #12?" }), "team/shop/problem/3/12/open", "problem"},
		{"paused problem", with(func(it *InboxItem) { it.Kind, it.IssueIID, it.PauseKind = "problem", 0, aiv1.PauseKind("budget") }), "team/shop/problem/3//budget", "problem"},
	}
	for _, c := range cases {
		if got := inboxKey(c.item); got != c.key {
			t.Errorf("%s: key %q, want %q", c.name, got, c.key)
		}
		if got := notifyCategory(c.item); got != c.category {
			t.Errorf("%s: category %q, want %q", c.name, got, c.category)
		}
	}
	pause := with(func(it *InboxItem) { it.Kind, it.PauseKind = "pause", aiv1.PauseKind("budget") })
	hold := pause
	hold.Hold = true
	if inboxKey(pause) == inboxKey(hold) {
		t.Errorf("a hold and the milestone's pause over the issue share the key %q", inboxKey(hold))
	}
	reworded := cases[10].item
	reworded.Reason = "stuck: CI failed 3 times"
	if inboxKey(reworded) != cases[10].key {
		t.Errorf("a problem's key changed with its reason: %q", inboxKey(reworded))
	}
}

func TestInboxOfSetsKeys(t *testing.T) {
	posted := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit"}}
	assess := func(id string, iid int64) aiv1.Prompt {
		return aiv1.Prompt{ID: id, State: aiv1.PromptOpen, Question: "Split?", PostedAt: &posted,
			Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: iid}}
	}
	tasks := []aiv1.AgentTask{{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "spec"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 5, Title: "Poker"}},
		Status: aiv1.AgentTaskStatus{
			Plan:    []aiv1.PlannedIssue{{IID: 7, Title: "Blinds"}, {IID: 8, Title: "Chips"}},
			Prompts: []aiv1.Prompt{assess("Q1", 7), assess("Q2", 8)},
		}}}
	items := inboxOf(map[string]*aiv1.AgentProject{"agents/orbit": ap}, tasks, time.Now())
	if len(items) != 2 {
		t.Fatalf("want two problems: %+v", items)
	}
	keys := []string{items[0].Key, items[1].Key}
	slices.Sort(keys)
	if want := []string{"agents/orbit/problem/5/7/open", "agents/orbit/problem/5/8/open"}; !slices.Equal(keys, want) {
		t.Errorf("keys %q, want %q", keys, want)
	}
	for _, it := range items {
		if it.Notify != "problem" {
			t.Errorf("category of %+v", it)
		}
	}
}

// TestInboxOfQueueProblem checks a held queue issue: one problem item with
// the queue flag and an empty milestone part in its key, its assessment
// prompt folded in; the queue pilot's proposal carries the flag too, a
// milestone's problem does not.
func TestInboxOfQueueProblem(t *testing.T) {
	since := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit"}}
	assess := aiv1.Prompt{ID: "Q1", State: aiv1.PromptOpen, Question: "Retry #421?", PostedAt: &since,
		Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 421}}
	tasks := []aiv1.AgentTask{
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit-queue"},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeQueue},
			Status: aiv1.AgentTaskStatus{Prompts: []aiv1.Prompt{assess}, Autopilot: &aiv1.AutopilotStatus{Items: []aiv1.AutopilotItem{
				{IID: 421, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseStuck, Reason: "CI stays red", Since: since}},
				{IID: 422, State: aiv1.AutopilotItemPending},
			}}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit-421-flaky"},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: 421, Title: "Flaky helm-lint"}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit-pilot-queue"},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeCoordinate, QueueTask: "orbit-queue"},
			Status: aiv1.AgentTaskStatus{Pilot: &aiv1.PilotStatus{Proposals: []aiv1.PilotProposal{{ID: 3, Summary: "retry #421", PostedAt: &since}}}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "spec"},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 5, Title: "Poker"}},
			Status: aiv1.AgentTaskStatus{Prompts: []aiv1.Prompt{{ID: "Q2", State: aiv1.PromptOpen, Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 7}}}}},
	}
	byKey := map[string]InboxItem{}
	for _, it := range inboxOf(map[string]*aiv1.AgentProject{"agents/orbit": ap}, tasks, time.Now()) {
		if _, dup := byKey[it.Key]; dup {
			t.Errorf("duplicate key %q", it.Key)
		}
		byKey[it.Key] = it
	}
	if len(byKey) != 3 {
		t.Fatalf("want the queue problem, the proposal and the milestone problem: %+v", byKey)
	}
	p, ok := byKey["agents/orbit/problem//421/stuck"]
	if !ok || !p.Queue || p.Notify != "problem" || p.Title != "Flaky helm-lint" || !p.Hold || p.MilestoneIID != 0 ||
		p.Reason != "held (stuck): CI stays red" || p.At == nil || !p.At.Equal(since.Time) {
		t.Errorf("queue problem %+v", p)
	}
	if pr, ok := byKey["agents/orbit/proposal//3"]; !ok || !pr.Queue || pr.Notify != "proposal" {
		t.Errorf("queue pilot's proposal %+v", pr)
	}
	if m, ok := byKey["agents/orbit/problem/5/7/open"]; !ok || m.Queue {
		t.Errorf("milestone problem %+v", m)
	}
}

func TestProblemItemsQueue(t *testing.T) {
	q := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeQueue}}
	if got := problemItems(InboxItem{}, q, nil, time.Now()); got != nil {
		t.Errorf("a queue without autopilot status: %+v", got)
	}
	q.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotOff}
	if got := problemItems(InboxItem{}, q, nil, time.Now()); got != nil {
		t.Errorf("an off queue: %+v", got)
	}
	q.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedReason: "main is red"}
	got := problemItems(InboxItem{}, q, nil, time.Now())
	if len(got) != 1 || got[0].IssueIID != 0 || got[0].Title != "Loose issues" {
		t.Errorf("a paused queue: %+v", got)
	}
}

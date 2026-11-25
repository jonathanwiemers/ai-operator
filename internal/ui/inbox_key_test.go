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

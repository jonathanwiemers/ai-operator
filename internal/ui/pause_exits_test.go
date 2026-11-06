package ui

import (
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func exitTask(iid int64, ci aiv1.CIState, pending bool) aiv1.AgentTask {
	t := aiv1.AgentTask{}
	t.Spec.Type, t.Spec.Issue = aiv1.TaskTypeImplement, &aiv1.IssueRef{IID: iid}
	t.Status.CI = &aiv1.CIStatus{State: ci}
	if pending {
		t.Spec.RunRequest = 1
	}
	return t
}

func exitProject(state aiv1.MainHealthState, sha string) *aiv1.AgentProject {
	p := &aiv1.AgentProject{}
	if state != "" {
		p.Status.MainHealth = &aiv1.MainHealthStatus{State: state, SHA: sha}
	}
	return p
}

func pausedOn(kind aiv1.PauseKind, it aiv1.AutopilotItem) *aiv1.AutopilotStatus {
	return &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PauseKind: kind, PausedFor: it.IID, Items: []aiv1.AutopilotItem{it}}
}

// The live status of the automatic exits, per pause kind.
func TestExitViewStatus(t *testing.T) {
	active := aiv1.AutopilotItem{IID: 88, State: aiv1.AutopilotItemActive, MergeRequestIID: 735}
	rebasing, healed := active, active
	rebasing.RebasedFrom = "2dc61015aa"
	healed.HealedOn = "ffe0123456"
	budget := pausedOn(aiv1.PauseBudget, active)
	budget.Budget = &aiv1.BudgetStatus{LimitUSD: "5.00", SpentUSD: "5.20"}
	raised := pausedOn(aiv1.PauseBudget, active)
	raised.Budget = &aiv1.BudgetStatus{LimitUSD: "10.00", SpentUSD: "5.20"}
	unread := pausedOn(aiv1.PauseBudget, active)
	unread.Budget = &aiv1.BudgetStatus{LimitUSD: "10.00"}
	ownBudget := active
	ownBudget.Budget = &aiv1.BudgetStatus{LimitUSD: "1.00", SpentUSD: "1.50"}
	stuck := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PauseKind: aiv1.PauseStuck}
	baseFailing := []aiv1.AgentTask{exitTask(88, aiv1.CIStateBaseFailing, false)}

	type want struct {
		status ExitStatus
		detail string
	}
	cases := []struct {
		name    string
		ap      *aiv1.AutopilotStatus
		tasks   []aiv1.AgentTask
		project *aiv1.AgentProject
		want    map[string]want
	}{
		{"ciRed, main red", pausedOn(aiv1.PauseCIRed, active), baseFailing, exitProject(aiv1.MainHealthRed, "abc1234567"),
			map[string]want{"mainGreenRebase": {ExitStatusWaiting, "main is red at abc1234"}, "laterRunPasses": {ExitStatusWaiting, ""}, "mergedByHand": {ExitStatusUnknown, ""}}},
		{"ciRed, main green on a new commit", pausedOn(aiv1.PauseCIRed, active), baseFailing, exitProject(aiv1.MainHealthGreen, "ffe0123456"),
			map[string]want{"mainGreenRebase": {ExitStatusMet, "main is green at ffe0123"}}},
		{"ciRed, rebase in flight", pausedOn(aiv1.PauseCIRed, rebasing), baseFailing, exitProject(aiv1.MainHealthGreen, "ffe0123456"),
			map[string]want{"mainGreenRebase": {ExitStatusInProgress, "rebase of !735 in flight"}}},
		{"ciRed, rebased onto the green commit", pausedOn(aiv1.PauseCIRed, healed), baseFailing, exitProject(aiv1.MainHealthGreen, "ffe0123456"),
			map[string]want{"mainGreenRebase": {ExitStatusInProgress, "rebased onto ffe0123, the new pipeline decides"}}},
		{"ciRed, own failure", pausedOn(aiv1.PauseCIRed, active), []aiv1.AgentTask{exitTask(88, aiv1.CIStateFailed, false)}, exitProject(aiv1.MainHealthGreen, "ffe0123456"),
			map[string]want{"mainGreenRebase": {ExitStatusWaiting, "the failing jobs pass on main"}}},
		{"ciRed, main not watched", pausedOn(aiv1.PauseCIRed, active), baseFailing, exitProject("", ""),
			map[string]want{"mainGreenRebase": {ExitStatusUnknown, "the default branch's health is not known"}}},
		{"run pending", pausedOn(aiv1.PauseRunFailed, active), []aiv1.AgentTask{exitTask(88, aiv1.CIStateNone, true)}, exitProject("", ""),
			map[string]want{"laterRunPasses": {ExitStatusInProgress, "run of #88 pending"}}},
		{"main red", pausedOn(aiv1.PauseMainRed, active), nil, exitProject(aiv1.MainHealthRed, "abc1234567"),
			map[string]want{"mainGreen": {ExitStatusWaiting, "main is red at abc1234"}}},
		{"main green", pausedOn(aiv1.PauseMainRed, active), nil, exitProject(aiv1.MainHealthGreen, "ffe0123456"),
			map[string]want{"mainGreen": {ExitStatusMet, "main is green at ffe0123"}}},
		{"budget spent", budget, nil, nil, map[string]want{"budgetRaised": {ExitStatusWaiting, "the milestone spent 5.20 of 5.00 USD"}}},
		{"budget raised", raised, nil, nil, map[string]want{"budgetRaised": {ExitStatusMet, ""}}},
		{"budget unread", unread, nil, nil, map[string]want{"budgetRaised": {ExitStatusUnknown, "the spend of the milestone cannot be read"}}},
		{"issue budget spent", pausedOn(aiv1.PauseBudget, ownBudget), nil, nil, map[string]want{"budgetRaised": {ExitStatusWaiting, "#88 spent 1.50 of 1.00 USD"}}},
		{"stuck", stuck, nil, nil, map[string]want{"somethingCanStart": {ExitStatusWaiting, ""}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			views := exitViews(c.ap, c.tasks, c.project, MilestonePermissions{})
			exits := aiv1.PauseExits(c.ap.PauseKind)
			if len(views) != len(exits) {
				t.Fatalf("%d views for %d exits", len(views), len(exits))
			}
			for i, v := range views {
				if v.PauseExit != exits[i] {
					t.Errorf("view %d is %+v, not %+v", i, v.PauseExit, exits[i])
				}
				if v.Auto != (v.Status != "") {
					t.Errorf("%s: an automatic exit has a status, a remedy none: %q", v.ID, v.Status)
				}
				if w, ok := c.want[v.ID]; ok && (v.Status != w.status || v.Detail != w.detail) {
					t.Errorf("%s: %q %q, want %q %q", v.ID, v.Status, v.Detail, w.status, w.detail)
				}
			}
			for id := range c.want {
				found := false
				for _, v := range views {
					found = found || v.ID == id
				}
				if !found {
					t.Errorf("no exit %s", id)
				}
			}
		})
	}
}

// Every kind has its exits in the view; none while running.
func TestExitViewsEveryKind(t *testing.T) {
	for _, kind := range aiv1.PauseKinds {
		views := exitViews(&aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PauseKind: kind}, nil, nil, MilestonePermissions{Chat: true})
		if len(views) == 0 || len(views) != len(aiv1.PauseExits(kind)) {
			t.Errorf("%s: %d views", kind, len(views))
		}
	}
	if v := exitViews(&aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, PauseKind: aiv1.PauseCIRed}, nil, nil, MilestonePermissions{}); v != nil {
		t.Errorf("running: %+v", v)
	}
	if v := exitViews(nil, nil, nil, MilestonePermissions{}); v != nil {
		t.Errorf("no autopilot: %+v", v)
	}
}

// The grants decide which remedies are buttons: none without chat, merge
// only with merge; labels, GitLab actions, prompts and the commands the
// Desk cannot send never.
func TestExitViewsAllowed(t *testing.T) {
	none, chat, owner := MilestonePermissions{GitLab: true, Steer: true}, MilestonePermissions{Chat: true}, MilestonePermissions{Chat: true, Merge: true}
	cases := []struct {
		kind  aiv1.PauseKind
		perms MilestonePermissions
		want  []string
	}{
		{aiv1.PauseReviewErrors, none, nil},
		{aiv1.PauseReviewErrors, chat, []string{"retry", "skip", "review"}},
		{aiv1.PauseReviewErrors, owner, []string{"retry", "skip", "review", "mergeAnyway"}},
		{aiv1.PauseCIRed, owner, []string{"retry", "skip"}},
		{aiv1.PauseStuck, owner, []string{"skip", "resume"}},
		{aiv1.PauseCycle, chat, []string{"skip", "resume"}},
		{aiv1.PauseNeedsInput, chat, []string{"retry", "skip"}},
		{aiv1.PauseOwner, none, nil},
		{aiv1.PauseOwner, chat, []string{"resume"}},
		{aiv1.PauseBudget, owner, nil},
	}
	for _, c := range cases {
		var got []string
		for _, v := range exitViews(&aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PauseKind: c.kind}, nil, nil, c.perms) {
			if v.Allowed {
				got = append(got, v.ID)
			}
		}
		if len(got) != len(c.want) {
			t.Errorf("%s %+v: allowed %v, want %v", c.kind, c.perms, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s %+v: allowed %v, want %v", c.kind, c.perms, got, c.want)
				break
			}
		}
	}
}

package ui

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestMilestoneViewHolds(t *testing.T) {
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	since := metav1.NewTime(time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC))
	red := &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "job `lint` failed", Since: since}
	errs := &aiv1.ItemHold{Kind: aiv1.PauseReviewErrors, Reason: "1 reviewers failed on #3", Since: since}
	st := lanesSpec(aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemActive},
		aiv1.AutopilotItem{IID: 2, State: aiv1.AutopilotItemActive, Hold: red},
		aiv1.AutopilotItem{IID: 3, State: aiv1.AutopilotItemActive, Hold: errs},
		// A stale hold on a merged item is no hold.
		aiv1.AutopilotItem{IID: 4, State: aiv1.AutopilotItemMerged, Hold: red})
	reviewErrors := []aiv1.ReviewError{{Reviewer: aiv1.ReviewerBugbot, Kind: aiv1.ReviewErrorUsageLimit}}
	impl := aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop-3"},
		Spec:   aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, Milestone: st.Spec.Milestone, Issue: &aiv1.IssueRef{IID: 3}},
		Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "3a0e000", ReviewErrorKinds: reviewErrors}}}
	v := milestoneView(p, []aiv1.AgentTask{st, impl}, &st, nil, time.Now())
	h := v.Autopilot.Holds
	if len(h) != 2 || h[0].IID != 2 || h[0].Title != "Two" || h[0].Kind != aiv1.PauseCIRed || h[0].Reason != red.Reason || !h[0].Since.Equal(&since) || h[0].Head != "" ||
		h[1].IID != 3 || h[1].Kind != aiv1.PauseReviewErrors || h[1].Head != "3a0e000" || !slices.Equal(h[1].ReviewErrors, reviewErrors) {
		t.Errorf("holds = %+v", h)
	}
	if v.Autopilot.PausedFor != 0 || v.Autopilot.Head != "" || v.Autopilot.Exits != nil {
		t.Errorf("holds do not pause the milestone: %+v", v.Autopilot)
	}

	st = lanesSpec(aiv1.AutopilotItem{IID: 1, State: aiv1.AutopilotItemActive})
	if v = milestoneView(p, []aiv1.AgentTask{st}, &st, nil, time.Now()); v.Autopilot.Holds == nil || len(v.Autopilot.Holds) != 0 {
		t.Errorf("without holds, holds is an empty array: %#v", v.Autopilot.Holds)
	}
}

func TestPauseExitViewsHold(t *testing.T) {
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{{IID: 7, State: aiv1.AutopilotItemActive,
		Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed}}}}
	pending := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, RunRequest: 2, Issue: &aiv1.IssueRef{IID: 7}},
		Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{ObservedRunRequest: 1}}}
	views := pauseExitViews(aiv1.PauseCIRed, 7, ap, []aiv1.AgentTask{pending}, nil, MilestonePermissions{Chat: true})
	var exits []aiv1.PauseExit
	for _, v := range views {
		exits = append(exits, v.PauseExit)
	}
	if !slices.Equal(exits, aiv1.PauseExits(aiv1.PauseCIRed)) {
		t.Fatalf("the ways out of a ciRed hold: %+v", views)
	}
	for _, v := range views {
		switch v.ID {
		case "laterRunPasses":
			if v.Status != ExitStatusInProgress || v.Detail != "run of #7 pending" {
				t.Errorf("laterRunPasses while #7's run is pending: %+v", v)
			}
		case "resume":
			if !v.Allowed {
				t.Errorf("resume is a Desk command, allowed with chat: %+v", v)
			}
		}
	}
	if v := exitViews(ap, nil, nil, MilestonePermissions{Chat: true}); v != nil {
		t.Errorf("a running milestone has no milestone exits: %+v", v)
	}
}

func TestMilestoneViewHoldExits(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	ctx := context.Background()
	var st aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	since := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	a := st.Status.Autopilot
	a.Phase, a.PausedReason, a.PauseKind, a.PausedFor = aiv1.AutopilotRunning, "", "", 0
	a.Items[0].Hold = &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "job `lint` failed", Since: since}
	a.Items[1] = aiv1.AutopilotItem{IID: 43, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseFindings, Reason: "2 findings", Since: since}}
	if err := env.reader.Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
	var issue aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-42-add-paypal"}, &issue); err != nil {
		t.Fatal(err)
	}
	issue.Spec.RunRequest = issue.Status.Run.ObservedRunRequest + 1
	if err := env.reader.Update(ctx, &issue); err != nil {
		t.Fatal(err)
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	h := m.Autopilot.Holds
	if len(h) != 2 || h[0].IID != 42 || h[0].Kind != aiv1.PauseCIRed || h[0].Reason != "job `lint` failed" || h[1].IID != 43 || h[1].Kind != aiv1.PauseFindings {
		t.Fatalf("holds = %+v", h)
	}
	for _, hv := range h {
		var exits []aiv1.PauseExit
		for _, e := range hv.Exits {
			exits = append(exits, e.PauseExit)
			if e.Allowed {
				t.Errorf("a viewer without chat gets no buttons: %+v", e)
			}
		}
		if !slices.Equal(exits, aiv1.PauseExits(hv.Kind)) {
			t.Errorf("the ways out of the hold of #%d: %+v", hv.IID, hv.Exits)
		}
	}
	if i := slices.IndexFunc(h[0].Exits, func(e ExitView) bool { return e.ID == "laterRunPasses" }); i < 0 || h[0].Exits[i].Status != ExitStatusInProgress {
		t.Errorf("laterRunPasses while #42's run is pending: %+v", h[0].Exits)
	}
	if m.Autopilot.Exits != nil {
		t.Errorf("a running milestone has no milestone exits: %+v", m.Autopilot.Exits)
	}
}

func TestInboxHolds(t *testing.T) {
	ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit"}}
	pausedAt := metav1.NewTime(time.Now().Add(-3 * time.Hour).Truncate(time.Second))
	heldAt := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	hold := &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "job `lint` failed", Since: heldAt}
	spec := func(name string, iid int64, a *aiv1.AutopilotStatus) aiv1.AgentTask {
		return aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: name},
			Spec:   aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: iid, Title: name}},
			Status: aiv1.AgentTaskStatus{Autopilot: a}}
	}
	tasks := []aiv1.AgentTask{
		spec("running", 22, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
			{IID: 1, State: aiv1.AutopilotItemActive, Hold: hold},
			{IID: 2, State: aiv1.AutopilotItemActive},
			{IID: 3, State: aiv1.AutopilotItemMerged, Hold: hold},
		}}),
		spec("paused", 23, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PauseKind: aiv1.PauseBudget, PausedReason: "budget spent", PausedAt: &pausedAt,
			Items: []aiv1.AutopilotItem{{IID: 5, State: aiv1.AutopilotItemActive, Hold: &aiv1.ItemHold{Kind: aiv1.PauseFindings, Reason: "2 findings", Since: heldAt}}}}),
		spec("done", 24, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, Items: []aiv1.AutopilotItem{{IID: 9, State: aiv1.AutopilotItemActive, Hold: hold}}}),
	}
	items := inboxOf(map[string]*aiv1.AgentProject{"agents/orbit": ap}, tasks, time.Now())
	// One problem per issue; the budget pause with one held item is that
	// item's problem (aiv1.ProblemIssue); a done milestone has none.
	if len(items) != 2 {
		t.Fatalf("inbox = %+v", items)
	}
	if p := items[0]; p.Kind != "problem" || !p.Hold || p.MilestoneIID != 22 || p.Title != "running" || p.IssueIID != 1 || p.PauseKind != aiv1.PauseCIRed ||
		p.Reason != "held (ciRed): job `lint` failed" || p.PausedSince == nil || !p.PausedSince.Equal(heldAt.Time) || p.At == nil || !p.At.Equal(heldAt.Time) {
		t.Errorf("a hold of a running milestone: %+v", p)
	}
	if p := items[1]; p.Kind != "problem" || !p.Hold || p.MilestoneIID != 23 || p.IssueIID != 5 || p.PauseKind != aiv1.PauseFindings || p.Reason != "held (findings): 2 findings" {
		t.Errorf("a hold of a paused milestone: %+v", p)
	}
}

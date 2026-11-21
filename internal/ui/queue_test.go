package ui

import (
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// queueSpec is the autopilot spec task of milestone iid with status ap (nil:
// not planned yet).
func queueSpec(iid int64, ap *aiv1.AutopilotStatus) aiv1.AgentTask {
	t := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Autopilot: true,
		Milestone: &aiv1.MilestoneRef{IID: iid, Title: "M" + string(rune('0'+iid))}}}
	t.Name = "shop-spec-m" + string(rune('0'+iid))
	t.Status.Autopilot = ap
	return t
}

func planned(phase aiv1.AutopilotPhase, states ...aiv1.AutopilotItemState) *aiv1.AutopilotStatus {
	ap := &aiv1.AutopilotStatus{Phase: phase}
	for i, s := range states {
		ap.Items = append(ap.Items, aiv1.AutopilotItem{IID: int64(40 + i), State: s})
	}
	return ap
}

func TestQueue(t *testing.T) {
	mark := planned(aiv1.AutopilotRunning)
	mark.StartRequestedBy, mark.StartRequestedAt = "@ada", &metav1.Time{Time: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)}
	legacyStart := planned(aiv1.AutopilotRunning)
	legacyStart.StartRequestedBy = "@ada" // no time: plans ahead, no mark
	paused := planned(aiv1.AutopilotPaused, aiv1.AutopilotItemActive)
	paused.PauseKind, paused.PausedReason = aiv1.PauseCIRed, "CI is red"
	budget := planned(aiv1.AutopilotPaused, aiv1.AutopilotItemActive)
	budget.PauseKind, budget.PausedReason = aiv1.PauseBudget, "budget spent"
	issueBudget := planned(aiv1.AutopilotPaused, aiv1.AutopilotItemActive)
	issueBudget.PauseKind = aiv1.PauseStuck
	stopped := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, BudgetStop: true,
		Milestone: &aiv1.MilestoneRef{IID: 3}, Issue: &aiv1.IssueRef{IID: 40}}}
	other := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Milestone: &aiv1.MilestoneRef{IID: 3}}}
	other.Status.Run.CostUSD = "1.25"

	type row struct {
		iid        int64
		state      string
		holdingFor int64
	}
	for _, tc := range []struct {
		name  string
		order []int64
		tasks []aiv1.AgentTask
		want  []row
	}{
		{"age order without a stored order or marks", nil, []aiv1.AgentTask{
			queueSpec(5, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemPending)),
			queueSpec(3, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemMerged, aiv1.AutopilotItemActive)),
		}, []row{{3, "running", 0}, {5, "holding", 3}}},
		{"without a stored order a mark passes an older milestone", nil, []aiv1.AgentTask{
			queueSpec(3, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemActive)), queueSpec(5, mark),
		}, []row{{5, "running", 0}, {3, "holding", 5}}},
		{"a start without a time is no mark", nil, []aiv1.AgentTask{
			queueSpec(3, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemActive)), queueSpec(5, legacyStart),
		}, []row{{3, "running", 0}, {5, "waiting", 3}}},
		{"stored order, marks or not", []int64{6, 3, 5}, []aiv1.AgentTask{
			queueSpec(3, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemActive)), queueSpec(5, mark), queueSpec(6, nil),
		}, []row{{6, "running", 0}, {3, "holding", 6}, {5, "waiting", 6}}},
		{"unlisted milestones at the bottom by iid", []int64{5}, []aiv1.AgentTask{
			queueSpec(6, nil), queueSpec(3, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemActive)), queueSpec(5, nil),
		}, []row{{5, "running", 0}, {3, "holding", 5}, {6, "waiting", 5}}},
		{"unplanned milestones wait to plan", nil, []aiv1.AgentTask{
			queueSpec(3, paused), queueSpec(5, nil), queueSpec(6, planned(aiv1.AutopilotRunning)),
		}, []row{{3, "paused", 0}, {5, "waiting", 3}, {6, "waiting", 3}}},
		{"budget pauses", nil, []aiv1.AgentTask{
			queueSpec(3, issueBudget), stopped, queueSpec(5, budget),
		}, []row{{3, "budget", 0}, {5, "budget", 0}}},
		{"done milestones and specs without autopilot are left out", []int64{2, 5}, []aiv1.AgentTask{
			queueSpec(2, planned(aiv1.AutopilotDone, aiv1.AutopilotItemMerged)),
			{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 1}}},
			queueSpec(5, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemActive)),
		}, []row{{5, "running", 0}}},
		{"a second spec task of a milestone is one row", nil, []aiv1.AgentTask{
			queueSpec(3, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemActive)),
			queueSpec(3, planned(aiv1.AutopilotRunning, aiv1.AutopilotItemActive)),
			queueSpec(5, nil),
		}, []row{{3, "running", 0}, {5, "waiting", 3}}},
		{"no autopilot milestones", nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := queue(tc.order, tc.tasks, map[int64]float64{})
			if q == nil {
				t.Fatal("queue is nil, want an array")
			}
			var got []row
			for _, e := range q {
				got = append(got, row{e.IID, e.State, e.HoldingFor})
			}
			if len(got) != len(tc.want) {
				t.Fatalf("queue = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("queue = %+v, want %+v", got, tc.want)
				}
			}
		})
	}

	// The fields of one entry.
	q := queue([]int64{5, 3}, []aiv1.AgentTask{queueSpec(3, paused), queueSpec(5, mark), other}, map[int64]float64{3: 1.25})
	if e := q[0]; e.IID != 5 || e.Task != "shop-spec-m5" || e.Title != "M5" {
		t.Errorf("first entry = %+v", e)
	}
	if e := q[1]; e.PausedReason != "CI is red" || e.Done != 0 || e.Total != 1 || e.CostUSD != "1.25" {
		t.Errorf("paused entry = %+v", e)
	}
	done := queue(nil, []aiv1.AgentTask{queueSpec(3, planned(aiv1.AutopilotRunning,
		aiv1.AutopilotItemMerged, aiv1.AutopilotItemSkipped, aiv1.AutopilotItemActive))}, nil)
	if e := done[0]; e.Done != 2 || e.Total != 3 {
		t.Errorf("done of total = %d of %d, want 2 of 3", e.Done, e.Total)
	}
}

func TestProjectQueueCommands(t *testing.T) {
	for _, tc := range []struct {
		name       string
		deskChat   bool
		chatGrant  bool
		wantButton bool
	}{
		{"chat grant and Desk chat", true, true, true},
		{"no chat grant", true, false, false},
		{"Desk chat off", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, gl := newSteeringEnv(t,
				Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}},
				Rule{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: tc.chatGrant})
			if tc.deskChat {
				withChat(env.server.cfg)
			}
			withAutopilot(t, env, gl)
			env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
			env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}, "chatters": map[string]any{}}}
			env.login(t, "/")

			var d ProjectDetail
			if code := env.get(t, "/api/v1/projects/team-a/shop", &d); code != http.StatusOK {
				t.Fatalf("project: status %d", code)
			}
			if d.QueueCommands != tc.wantButton {
				t.Errorf("queueCommands = %v, want %v", d.QueueCommands, tc.wantButton)
			}
			if len(d.Queue) != 1 || d.Queue[0].IID != 3 || d.Queue[0].State != "paused" || d.Queue[0].Task != "shop-spec-m3-checkout" {
				t.Errorf("queue = %+v", d.Queue)
			}
		})
	}
}

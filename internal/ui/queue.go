package ui

import (
	"cmp"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/milestoneorder"
)

// QueueEntry is an open autopilot milestone; task: its spec task. holding
// (planned) and waiting (to plan) wait for holdingFor, the first in rank.
// markedBy: the run-next mark. done of total: issues merged or skipped.
type QueueEntry struct {
	IID          int64  `json:"iid"`
	Title        string `json:"title"`
	Task         string `json:"task"`
	State        string `json:"state" tstype:"'running' | 'holding' | 'waiting' | 'paused' | 'budget'"`
	HoldingFor   int64  `json:"holdingFor,omitempty"`
	PausedReason string `json:"pausedReason,omitempty"`
	MarkedBy     string `json:"markedBy,omitempty"`
	Done         int    `json:"done"`
	Total        int    `json:"total"`
	Cost         `tstype:",extends"`
}

// queue lists the open autopilot milestones in run order, ranked like the
// controller ranks them (milestoneorder); costs by milestone iid.
func queue(tasks []aiv1.AgentTask, costs map[int64]float64) []QueueEntry {
	specs, budgetStop := map[int64]*aiv1.AgentTask{}, map[int64]bool{}
	var entries []milestoneorder.Entry
	for i := range tasks {
		t, ms := &tasks[i], tasks[i].Spec.Milestone
		if ms != nil && t.Spec.Type == aiv1.TaskTypeSpec && t.Spec.Autopilot && specs[ms.IID] == nil {
			specs[ms.IID] = t
			entries = append(entries, milestoneorder.EntryOf(ms.IID, t))
		} else if ms != nil && t.Spec.BudgetStop {
			budgetStop[ms.IID] = true
		}
	}
	q, rank := []QueueEntry{}, milestoneorder.Rank(entries)
	for _, iid := range rank {
		t := specs[iid]
		ap := cmp.Or(t.Status.Autopilot, &aiv1.AutopilotStatus{})
		e := QueueEntry{IID: iid, Title: t.Spec.Milestone.Title, Task: t.Name, State: "running", PausedReason: ap.PausedReason,
			Done: itemsDone(ap), Total: len(ap.Items), Cost: tasksCost(costs[iid])}
		if milestoneorder.EntryOf(iid, t).MarkedAt != nil {
			e.MarkedBy = ap.StartRequestedBy
		}
		switch paused := ap.Phase == aiv1.AutopilotPaused; {
		case paused && (ap.PauseKind == aiv1.PauseBudget || budgetStop[iid]):
			e.State = "budget"
		case paused:
			e.State = "paused"
		case len(q) > 0 && e.Total > 0:
			e.State, e.HoldingFor = "holding", rank[0]
		case len(q) > 0:
			e.State, e.HoldingFor = "waiting", rank[0]
		}
		q = append(q, e)
	}
	return q
}

// itemsDone counts the issues of ap merged or skipped.
func itemsDone(ap *aiv1.AutopilotStatus) (n int) {
	for _, it := range ap.Items {
		if it.State == aiv1.AutopilotItemMerged || it.State == aiv1.AutopilotItemSkipped {
			n++
		}
	}
	return n
}

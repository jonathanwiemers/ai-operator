package controller

import (
	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// queueOrder orders the plan topologically: dependencies first, ties in plan
// order. It returns the issues of a dependency cycle, if there is one.
func queueOrder(plan []aiv1.PlannedIssue) ([]int64, []int64) {
	index := map[int64]int{}
	for i, p := range plan {
		index[p.IID] = i
	}
	waiting := make([]int, len(plan))
	dependants := map[int64][]int64{}
	for _, p := range plan {
		for _, d := range p.DependsOn {
			if _, ok := index[d]; ok {
				waiting[index[p.IID]]++
				dependants[d] = append(dependants[d], p.IID)
			}
		}
	}
	var order []int64
	done := make([]bool, len(plan))
	for len(order) < len(plan) {
		next := -1
		for i := range plan {
			if !done[i] && waiting[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			var cycle []int64
			for i, p := range plan {
				if !done[i] {
					cycle = append(cycle, p.IID)
				}
			}
			return order, cycle
		}
		done[next] = true
		order = append(order, plan[next].IID)
		for _, dep := range dependants[plan[next].IID] {
			waiting[index[dep]]--
		}
	}
	return order, nil
}

// mergeQueue keeps the state of known items and appends new ones in queue
// order. Items the plan dropped stay once they were started, so the history
// is kept; pending ones are dropped with the plan. Revisit follow-ups and
// flaky fixes are not in the plan and always stay.
func mergeQueue(items []aiv1.AutopilotItem, order []int64) []aiv1.AutopilotItem {
	known := map[int64]aiv1.AutopilotItem{}
	for _, it := range items {
		known[it.IID] = it
	}
	out := make([]aiv1.AutopilotItem, 0, len(order))
	seen := map[int64]bool{}
	for _, iid := range order {
		it, ok := known[iid]
		if !ok {
			it = aiv1.AutopilotItem{IID: iid, State: aiv1.AutopilotItemPending}
		}
		out = append(out, it)
		seen[iid] = true
	}
	for _, it := range items {
		if !seen[it.IID] && (it.State != aiv1.AutopilotItemPending || it.RevisitOf != "" || it.FlakyFix) {
			out = append(out, it)
		}
	}
	return out
}

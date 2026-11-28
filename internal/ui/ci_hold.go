package ui

import aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"

// ciGreenSinceHold reports whether the implement task t is still held while
// the pipeline of its pushed head turned green since: the operator recorded
// the recovery (status.ci.recoveredAt) and an autopilot hold that started
// before it, or the red-CI cause, remains. A task whose CI was green before
// the hold has no recovery and no flag.
func ciGreenSinceHold(t *aiv1.AgentTask, hold *aiv1.ItemHold) bool {
	if t == nil {
		return false
	}
	ci, run := t.Status.CI, t.Status.Run
	if ci == nil || ci.RecoveredAt == nil || ci.SHA != run.PushedSHA || ci.State != aiv1.CIStateSuccess {
		return false
	}
	return hold != nil && !ci.RecoveredAt.Before(&hold.Since) || run.NeedsInputCause == aiv1.NeedsInputCIRed
}

// taskHold is the autopilot hold over the issue of the implement task t:
// its item in the milestone's spec task, or in the Queue task for a loose
// issue, both among tasks. Nil when it is not held.
func taskHold(tasks []aiv1.AgentTask, t *aiv1.AgentTask) *aiv1.ItemHold {
	if t.Spec.Type != aiv1.TaskTypeImplement || t.Spec.Issue == nil {
		return nil
	}
	var holder *aiv1.AgentTask
	if m := t.Spec.Milestone; m != nil {
		holder, _ = milestoneTasks(tasks, m.IID)
	} else {
		for i := range tasks {
			if tasks[i].Spec.Type == aiv1.TaskTypeQueue {
				holder = &tasks[i]
			}
		}
	}
	if holder == nil || holder.Status.Autopilot == nil {
		return nil
	}
	for _, it := range holder.Status.Autopilot.Items {
		if it.IID == t.Spec.Issue.IID && it.State == aiv1.AutopilotItemActive {
			return it.Hold
		}
	}
	return nil
}

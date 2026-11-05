package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// queueCheck is what a task without a pod learns about its project before it
// takes a run slot.
type queueCheck struct {
	// running is the number of other tasks with a run in flight.
	running int
	// ahead are the other tasks that wait in the queue and rank ahead of the
	// task, best first.
	ahead []*aiv1.AgentTask
}

// admit reports whether the task may start with the given capacity: a slot
// is free, and fewer queued tasks than free slots rank ahead of it, so the
// top N of a queue start together.
func (q queueCheck) admit(capacity int) bool {
	return q.running < capacity && len(q.ahead) < capacity-q.running
}

// queueCheck lists the project's tasks once and counts the running ones and
// the queued ones ahead of the task.
func (r *AgentTaskReconciler) queueCheck(ctx context.Context, task *aiv1.AgentTask, maxRuns int32) (queueCheck, error) {
	var tasks aiv1.AgentTaskList
	if err := r.List(ctx, &tasks, client.InNamespace(task.Namespace), client.MatchingLabels{LabelProject: task.Spec.ProjectRef}); err != nil {
		return queueCheck{}, err
	}
	q := queueCheck{running: runningTasks(tasks.Items, task)}
	now := r.now()
	for i := range tasks.Items {
		t := &tasks.Items[i]
		if t.Name != task.Name && t.Status.Phase == aiv1.TaskPhaseQueued && t.Spec.Type != aiv1.TaskTypeCoordinate &&
			wantsSlot(t, now, maxRuns) && aiv1.QueueLess(t, task) {
			q.ahead = append(q.ahead, t)
		}
	}
	slices.SortFunc(q.ahead, func(a, b *aiv1.AgentTask) int {
		switch {
		case aiv1.QueueLess(a, b):
			return -1
		case aiv1.QueueLess(b, a):
			return 1
		}
		return 0
	})
	return q, nil
}

// leaveQueue gives up a queued task's place after an error that may repeat:
// a task that cannot start must not hold back the ones behind it. The error
// retries the task, which then queues again by rank.
func (r *AgentTaskReconciler) leaveQueue(ctx context.Context, task, orig *aiv1.AgentTask, cause error) error {
	if task.Status.Phase != aiv1.TaskPhaseQueued {
		return cause
	}
	task.Status.Phase = aiv1.TaskPhasePending
	task.Status.Message = cause.Error()
	return errors.Join(cause, r.patchStatus(ctx, task, orig))
}

// wantsSlot reports whether a queued task still asks for a run slot. Its
// phase is Queued until it is reconciled again, so a task that has become
// suspended, waiting for a usage limit or the budget, or blocked since must
// not hold the others back. It follows the reconciler's own conditions.
func wantsSlot(t *aiv1.AgentTask, now time.Time, maxRuns int32) bool {
	pending := t.RunPending()
	inProgress := t.Status.Run.ActiveRunRequest > t.Status.Run.ObservedRunRequest
	budgetWait := pending && !inProgress && t.Spec.BudgetStop
	blocked := pending && !inProgress && t.Status.Run.RunCount >= maxRuns
	d := t.Status.Run.DeferredUntil
	waiting := pending && !inProgress && d != nil && d.After(now)
	return (pending && !budgetWait || t.HasChatTurns()) && !blocked && !waiting && !t.Spec.Suspend
}

// queueMessage is the Queued message: the capacity, then the place in line
// and the task directly ahead.
func queueMessage(running int, capacity int32, ahead []*aiv1.AgentTask) string {
	msg := fmt.Sprintf("waiting for capacity (%d/%d runs in flight)", running, capacity)
	if len(ahead) == 0 {
		return msg + ", next in line"
	}
	return fmt.Sprintf("%s, %s in line behind %s", msg, ordinal(len(ahead)+1), describeTask(ahead[len(ahead)-1]))
}

// describeTask names a task by its issue and milestone.
func describeTask(t *aiv1.AgentTask) string {
	hasIssue := t.Spec.Issue != nil && t.Spec.Issue.IID != 0
	switch {
	case hasIssue && t.Spec.Milestone != nil:
		return fmt.Sprintf("#%d (milestone %%%d)", t.Spec.Issue.IID, t.Spec.Milestone.IID)
	case hasIssue:
		return fmt.Sprintf("#%d", t.Spec.Issue.IID)
	case t.Spec.Milestone != nil:
		return fmt.Sprintf("milestone %%%d", t.Spec.Milestone.IID)
	}
	return t.Name
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// runningTasks counts the tasks of the list other than self that currently
// have a run in flight; pilots hold no slot.
func runningTasks(tasks []aiv1.AgentTask, self *aiv1.AgentTask) int {
	n := 0
	for i := range tasks {
		t := &tasks[i]
		if t.Name != self.Name && t.Status.Phase == aiv1.TaskPhaseRunning && t.Spec.Type != aiv1.TaskTypeCoordinate {
			n++
		}
	}
	return n
}

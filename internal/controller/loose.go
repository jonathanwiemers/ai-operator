package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// maxLooseFinished is how many finished items (merged or left) the loose
// issues queue keeps in its status and shows on its tracking issue: the
// queue never ends, so its status would grow forever.
const maxLooseFinished = 50

// syncQueueTask keeps the project's one Queue task, the loose issues queue,
// while spec.looseIssues is enabled. Switched off, a Queue task with a
// tracking issue stays (in phase Off once syncLoose closed it), so switching
// on again reopens the same issue; one without is deleted by the cleanup.
func (p *projectPoll) syncQueueTask(ctx context.Context) error {
	name := naming.QueueTask(p.project.Name)
	task := p.findQueueTask()
	loose := p.spec.LooseIssues
	if !loose.Enabled {
		if task != nil && task.Status.Autopilot != nil && task.Status.Autopilot.TrackingIssueIID != 0 {
			p.keep[task.Name], p.queueTask = true, task
		}
		return nil
	}
	owners := loose.Owners
	if len(owners) == 0 {
		owners = p.owners("")
	}
	subjects := loose.OwnerSubjects
	if task == nil {
		t, err := p.r.newTask(p.project, name, aiv1.TaskTypeQueue)
		if err != nil {
			return err
		}
		t.Spec.Owners, t.Spec.OwnerSubjects = owners, subjects
		if err := p.r.Create(ctx, t); err != nil {
			return fmt.Errorf("creating the loose issues queue task: %w", err)
		}
		log.FromContext(ctx).Info("loose issues queue task created", "task", name)
		p.keep[t.Name], p.queueTask = true, t
		return nil
	}
	p.keep[task.Name], p.queueTask = true, task
	if slices.Equal(task.Spec.Owners, owners) && slices.Equal(task.Spec.OwnerSubjects, subjects) {
		return nil
	}
	task.Spec.Owners, task.Spec.OwnerSubjects = owners, subjects
	if err := p.r.Update(ctx, task); err != nil {
		return fmt.Errorf("updating the owners of the loose issues queue task: %w", err)
	}
	return nil
}

// findQueueTask is the project's Queue task among the poll's tasks, nil if
// there is none.
func (p *projectPoll) findQueueTask() *aiv1.AgentTask {
	name := naming.QueueTask(p.project.Name)
	for i := range p.tasks.Items {
		if t := &p.tasks.Items[i]; t.Spec.Type == aiv1.TaskTypeQueue && t.Name == name {
			return t
		}
	}
	return nil
}

// queueItem is the pending, active or held item of issue iid in the loose
// issues queue, with the Queue task; nil while the queue is off or the issue
// is not in it. The issue phases run before syncQueueTask, so it looks the
// task up itself.
func (p *projectPoll) queueItem(iid int64) (*aiv1.AgentTask, *aiv1.AutopilotItem) {
	qt := p.queueTask
	if qt == nil {
		qt = p.findQueueTask()
	}
	if !p.spec.LooseIssues.Enabled || qt == nil || qt.Status.Autopilot == nil {
		return nil, nil
	}
	for i := range qt.Status.Autopilot.Items {
		if it := &qt.Status.Autopilot.Items[i]; it.IID == iid && !finished(*it) {
			return qt, it
		}
	}
	return nil, nil
}

// syncLoose drives the loose issues queue of qt, one step per poll, and
// switches it off once spec.looseIssues is disabled. It never takes a
// milestone path: no planning, releases, settling or closing a milestone.
// tasks and issues are the poll's implement tasks and agent-labelled issues
// by IID. It returns the tracking issue's comments it read, for the queue's
// pilot; nil when it read none.
func (r *AgentProjectReconciler) syncLoose(ctx context.Context, gl gitlab.API, gate *commandGate, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	qt *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, issues map[int64]gitlab.Issue) ([]gitlab.Note, error) {
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
	if qt.Status.Autopilot != nil {
		ap = qt.Status.Autopilot.DeepCopy()
	}
	a := &autopilot{r: r, gl: gl, gate: gate, project: project, spec: spec, st: qt, ap: ap, tasks: tasks, issues: issues, loose: true,
		log: log.FromContext(ctx).WithValues("queue", qt.Name)}
	var err error
	switch {
	case spec.LooseIssues.Enabled:
		err = a.syncQueue(ctx)
	case ap.TrackingIssueIID != 0 && ap.Phase != aiv1.AutopilotOff:
		err = a.looseOff(ctx)
	}
	// Saved also after a failed step: its GitLab side effects have happened.
	if serr := a.save(ctx); err == nil {
		err = serr
	}
	if err == nil {
		err = r.writeChatAnswers(ctx, qt, a.chatMark, a.chatAnswers)
	}
	if err == nil && !spec.LooseIssues.Enabled {
		err = r.refuseChatCommands(ctx, qt, "The loose issues queue is off (`looseIssues.enabled` is false).")
	}
	return a.notes, err
}

// syncQueue is one poll of the loose issues queue: its issues, the owners'
// commands, the items that leave, the holds that end by themselves, then the
// milestone's merge gate for the active items and the start of pending ones,
// and the tracking issue.
func (a *autopilot) syncQueue(ctx context.Context) error {
	if a.ap.Phase == aiv1.AutopilotOff || a.ap.Phase == "" {
		a.ap.Phase = aiv1.AutopilotRunning
	}
	a.ap.MaxParallel = a.spec.LooseIssues.Parallel()
	a.ap.Items = pruneFinished(a.ap.Items, maxLooseFinished)
	if err := a.loadQueueIssues(ctx); err != nil {
		return err
	}
	if err := a.ensureTracking(ctx); err != nil {
		return err
	}
	if err := a.looseCommands(ctx); err != nil {
		return err
	}
	if err := a.looseChatCommands(ctx); err != nil {
		return err
	}
	if err := a.leaveQueue(ctx); err != nil {
		return err
	}
	// After the commands: a resume while a budget is spent holds again.
	if err := a.budget(ctx); err != nil {
		return err
	}
	if err := a.resumeHolds(ctx); err != nil {
		return err
	}
	if err := a.looseFreeze(ctx); err != nil {
		return err
	}
	if a.ap.Phase == aiv1.AutopilotRunning {
		if err := a.step(ctx); err != nil {
			return err
		}
	}
	return a.updateTracking(ctx)
}

// looseOff switches the queue off: its pending items are dropped, its active
// ones continue in the manual flow (their labels stay, their runs end, a
// human merges), and the tracking issue is closed with a note naming them.
// The task stays in phase Off, so switching on again reopens the issue.
func (a *autopilot) looseOff(ctx context.Context) error {
	a.log.Info("loose issues queue switched off")
	now := &metav1.Time{Time: a.r.now()}
	var dropped, manual []int64
	for i := range a.ap.Items {
		switch it := &a.ap.Items[i]; it.State {
		case aiv1.AutopilotItemPending:
			dropped = append(dropped, it.IID)
			leave(it, "the queue was switched off", now)
		case aiv1.AutopilotItemActive:
			manual = append(manual, it.IID)
			leave(it, "the queue was switched off; continues in the manual flow", now)
		}
	}
	a.ap.Landing = 0
	body := "⏹️ The loose issues queue is off (`looseIssues.enabled` is false). Nothing is started or merged any more."
	if len(dropped) > 0 {
		body += " Dropped from the queue: " + issueList(dropped) + "."
	}
	if len(manual) > 0 {
		body += " Continue in the manual flow, their runs end and a human merges them: " + issueList(manual) + "."
	}
	if err := a.comment(ctx, body); err != nil {
		return err
	}
	prev := a.ap.Phase
	a.ap.Phase = aiv1.AutopilotOff // for the description
	if err := a.closeTracking(ctx); err != nil {
		a.ap.Phase = prev // tried again on the next poll
		return err
	}
	a.ap.PausedReason, a.ap.PauseKind, a.ap.PausedFor, a.ap.ResumeWhenReady = "", "", 0, false
	return nil
}

// leave takes an item out of the loose issues queue with its reason.
func leave(it *aiv1.AutopilotItem, reason string, at *metav1.Time) {
	it.State, it.LeftReason, it.LeftAt = aiv1.AutopilotItemLeft, reason, at
	it.Hold, it.ReadyAt = nil, nil
}

// finished reports whether an item of the loose issues queue is through.
func finished(it aiv1.AutopilotItem) bool {
	return it.State == aiv1.AutopilotItemMerged || it.State == aiv1.AutopilotItemLeft
}

// finishedAt is when a finished item merged or left.
func finishedAt(it aiv1.AutopilotItem) int64 {
	switch {
	case it.State == aiv1.AutopilotItemMerged && it.MergedAt != nil:
		return it.MergedAt.UnixNano()
	case it.LeftAt != nil:
		return it.LeftAt.UnixNano()
	}
	return 0
}

// newestFinished returns the indices of the newest n finished items, newest
// first; of the same time the later item first.
func newestFinished(items []aiv1.AutopilotItem, n int) []int {
	var done []int
	for i, it := range items {
		if finished(it) {
			done = append(done, i)
		}
	}
	slices.Reverse(done)
	slices.SortStableFunc(done, func(x, y int) int { return cmp.Compare(finishedAt(items[y]), finishedAt(items[x])) })
	return done[:min(n, len(done))]
}

// pruneFinished keeps every unfinished item and the newest n finished ones,
// in their order.
func pruneFinished(items []aiv1.AutopilotItem, n int) []aiv1.AutopilotItem {
	keep := map[int]bool{}
	for _, i := range newestFinished(items, n) {
		keep[i] = true
	}
	var out []aiv1.AutopilotItem
	for i, it := range items {
		if !finished(it) || keep[i] {
			out = append(out, it)
		}
	}
	return out
}

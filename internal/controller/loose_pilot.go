package controller

import (
	"context"
	"fmt"
	"slices"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// The loose issues queue has a pilot like a milestone's: a Coordinate task
// tied to the Queue task by spec.queueTask, listening on the queue's
// tracking issue. Its steps are the milestone pilot's, with the Queue task in
// the place of the spec task; its scope is the queue's unfinished items.

// inPilotScope reports whether issue is belongs to the pilot of st: it is in
// the milestone of spec task st, or, for a Queue task, an unfinished item of
// its queue without a milestone.
func inPilotScope(st *aiv1.AgentTask, is gitlab.Issue) bool {
	if st.Spec.Type != aiv1.TaskTypeQueue {
		return is.Milestone != nil && is.Milestone.IID == st.Spec.Milestone.IID
	}
	return is.Milestone == nil && queueHas(st, is.IID)
}

// taskInPilotScope reports whether task t belongs to the pilot of st, as
// inPilotScope does for its issue.
func taskInPilotScope(st, t *aiv1.AgentTask) bool {
	if st.Spec.Type != aiv1.TaskTypeQueue {
		return t.Spec.Milestone != nil && t.Spec.Milestone.IID == st.Spec.Milestone.IID
	}
	return t.Spec.Milestone == nil && t.Spec.Issue != nil && queueHas(st, t.Spec.Issue.IID)
}

// pilotTasks returns the implement tasks the pilot of st acts on by issue
// IID: those of a milestone's issues and the main-red issue (issueTasks), or
// those of a Queue task's unfinished items.
func (r *AgentProjectReconciler) pilotTasks(ctx context.Context, project *aiv1.AgentProject, st *aiv1.AgentTask) (map[int64]*aiv1.AgentTask, error) {
	if st.Spec.Type != aiv1.TaskTypeQueue {
		return r.issueTasks(ctx, project, st.Spec.Milestone.IID)
	}
	var list aiv1.AgentTaskList
	if err := r.List(ctx, &list, client.InNamespace(project.Namespace), client.MatchingLabels{LabelProject: project.Name}); err != nil {
		return nil, err
	}
	out := map[int64]*aiv1.AgentTask{}
	for i := range list.Items {
		if t := &list.Items[i]; t.Spec.Type == aiv1.TaskTypeImplement && taskInPilotScope(st, t) {
			out[t.Spec.Issue.IID] = t
		}
	}
	return out, nil
}

// queuePilotTasks returns the Queue task and its pilot when issue iid is an
// unfinished item of the enabled loose issues queue, nil where there is
// none.
func (r *AgentProjectReconciler) queuePilotTasks(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	iid int64) (qt, coord *aiv1.AgentTask, err error) {
	if !spec.LooseIssues.Enabled {
		return nil, nil, nil
	}
	var list aiv1.AgentTaskList
	if err := r.List(ctx, &list, client.InNamespace(project.Namespace), client.MatchingLabels{LabelProject: project.Name}); err != nil {
		return nil, nil, err
	}
	for i := range list.Items {
		switch t := &list.Items[i]; {
		case t.Spec.Type == aiv1.TaskTypeQueue && t.Name == naming.QueueTask(project.Name):
			qt = t
		case t.Spec.Type == aiv1.TaskTypeCoordinate && t.Spec.QueueTask != "":
			coord = t
		}
	}
	if qt == nil || !queueHas(qt, iid) {
		return nil, nil, nil
	}
	return qt, coord, nil
}

// queueHas reports whether issue iid is a pending, active or held item of
// Queue task qt.
func queueHas(qt *aiv1.AgentTask, iid int64) bool {
	ap := qt.Status.Autopilot
	return ap != nil && slices.ContainsFunc(ap.Items, func(it aiv1.AutopilotItem) bool { return it.IID == iid && !finished(it) })
}

// queueTracking is the tracking issue the queue's pilot listens on: that of
// an enabled queue that is not off, 0 for none.
func queueTracking(spec aiv1.AgentProjectSpec, qt *aiv1.AgentTask) int64 {
	if qt == nil || !spec.LooseIssues.Enabled {
		return 0
	}
	if ap := qt.Status.Autopilot; ap != nil && ap.Phase != aiv1.AutopilotOff {
		return ap.TrackingIssueIID
	}
	return 0
}

// findQueuePilot is the queue's pilot among the poll's tasks, nil if there
// is none.
func (p *projectPoll) findQueuePilot() *aiv1.AgentTask {
	for i := range p.tasks.Items {
		if t := &p.tasks.Items[i]; t.Spec.Type == aiv1.TaskTypeCoordinate && t.Spec.QueueTask != "" {
			return t
		}
	}
	return nil
}

// ensureQueuePilot creates or updates the pilot of the loose issues queue qt:
// no milestone and no rules, the queue's owners, its tracking issue, and
// autopilot on, so an unset pilot.autonomy is managed.
func (r *AgentProjectReconciler) ensureQueuePilot(ctx context.Context, project *aiv1.AgentProject, qt, coord *aiv1.AgentTask) (*aiv1.AgentTask, error) {
	tracking := qt.Status.Autopilot.TrackingIssueIID
	set := func(c *aiv1.AgentTask) {
		c.Spec.QueueTask, c.Spec.Milestone, c.Spec.Autopilot, c.Spec.Rules = qt.Name, nil, true, nil
		c.Spec.Owners, c.Spec.OwnerSubjects = qt.Spec.Owners, qt.Spec.OwnerSubjects
		if c.Spec.Issue == nil || c.Spec.Issue.IID != tracking {
			c.Spec.Issue = &aiv1.IssueRef{IID: tracking}
		}
	}
	if coord == nil {
		var err error
		if coord, err = r.newTask(project, naming.QueuePilotTask(project.Name), aiv1.TaskTypeCoordinate); err != nil {
			return nil, err
		}
		set(coord)
		log.FromContext(ctx).Info("starting the pilot of the loose issues queue", "task", coord.Name, "issue", tracking)
		return coord, r.Create(ctx, coord)
	}
	before := coord.Spec.DeepCopy()
	if set(coord); equalSpec(before, &coord.Spec) {
		return coord, nil
	}
	return coord, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, client.ObjectKeyFromObject(coord), coord); err != nil {
			return err
		}
		set(coord)
		return r.Update(ctx, coord)
	})
}

// syncQueuePilot keeps the pilot of the loose issues queue qt while the
// queue is on and has a tracking issue, and runs its steps as syncPilots
// runs a milestone pilot's: the comments on the tracking issue (notes, as
// syncLoose read them; nil: none read in this poll), the Desk's commands, the
// queue's events, warm, proposals, refinements, accepted actions and
// deliveries. It never hears of
// the default branch's health: that stays with the lane's pilot. Left out of
// keep, the pilot is deleted by the cleanup.
func (r *AgentProjectReconciler) syncQueuePilot(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	qt, coord *aiv1.AgentTask, notes []gitlab.Note, keep map[string]bool, issues map[int64]gitlab.Issue, tasks map[int64]*aiv1.AgentTask) error {
	tracking := queueTracking(spec, qt)
	if tracking == 0 {
		return nil
	}
	fresh := coord == nil
	coord, err := r.ensureQueuePilot(ctx, project, qt, coord)
	if err == nil {
		keep[coord.Name] = true
	}
	// The first read marks the comments so far as history.
	if err == nil && notes != nil {
		err = r.pilotTracking(ctx, gl, project, spec, qt, coord, tracking, notes)
	}
	// A pilot created in this poll hears of the events from the next.
	if err != nil || fresh {
		return wrapQueuePilot(err)
	}
	steps := []func() error{
		func() error { return r.applyPilotChatCommands(ctx, gl, project, spec, qt, coord) },
		func() error {
			stalls := r.stallEvidenceOf(ctx, gl, spec, qt, coord, issues, tasks)
			return r.wakePilot(ctx, coord, pilotEvents(spec, project.Status.MainHealth, qt, issues, tasks, false, stalls, r.now()))
		},
		func() error {
			return r.warmPilot(ctx, coord, qt.Status.Autopilot, spec.Lifecycle.ProblemWarmMax.Duration)
		},
		func() error { _, err := r.postProposals(ctx, gl, project, spec, qt, coord, tracking); return err },
		func() error { return r.consumeRefined(ctx, gl, project, spec, qt, coord) },
		func() error { return r.runAccepted(ctx, gl, project, spec, qt, coord, qt, tracking) },
		func() error { return r.reportDeliveries(ctx, gl, spec, qt, coord, tracking, issues, tasks) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return wrapQueuePilot(err)
		}
	}
	return nil
}

func wrapQueuePilot(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("pilot of the loose issues queue: %w", err)
}

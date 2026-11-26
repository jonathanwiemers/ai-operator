package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// loadQueueIssues reads the issue of every pending, active or held item of
// the loose issues queue into a.issues, a map of its own: the queue is short,
// and an issue closed or moved into a milestone must be seen too. An issue
// that is gone is left out (leaveQueue counts it as closed).
func (a *autopilot) loadQueueIssues(ctx context.Context) error {
	a.issues = map[int64]gitlab.Issue{}
	for _, it := range a.ap.Items {
		if finished(it) {
			continue
		}
		is, err := a.gl.GetIssue(ctx, a.spec.GitLab.Project, it.IID)
		if gitlab.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("getting issue #%d: %w", it.IID, err)
		}
		a.issues[it.IID] = *is
	}
	return nil
}

// leaveQueue takes the items whose issue was closed or got a milestone out of
// the queue, with a note on the tracking issue. An issue finished by hand (its
// merge request merged, or closed as done) is recorded as merged, as pick
// does for a milestone's issues done before autopilot got to them.
func (a *autopilot) leaveQueue(ctx context.Context) error {
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		if finished(*it) {
			continue
		}
		is, ok := a.issues[it.IID]
		reason := "closed"
		switch {
		case ok && is.State == "opened" && is.Milestone == nil:
			continue
		case ok && is.State == "opened":
			reason = fmt.Sprintf("moved into milestone %q", inertLine(is.Milestone.Title, 100))
		case ok:
			mr, done, err := a.doneByHand(ctx, it.IID)
			if err != nil {
				return err
			}
			if done {
				if err := a.merged(ctx, it, mr); err != nil {
					return err
				}
				continue
			}
		}
		// The log first: a failed one is tried again on the next poll.
		if err := a.comment(ctx, fmt.Sprintf("↩️ #%d left the queue: %s.", it.IID, reason)); err != nil {
			return err
		}
		a.unready(it)
		leave(it, reason, &metav1.Time{Time: a.r.now()})
		a.log.Info("issue left the loose issues queue", "issue", it.IID, "reason", reason)
	}
	return nil
}

// pickLoose starts pending items, first taken first started, while fewer
// than maxParallel are active (held ones count). It hands an issue that is
// not in work yet to the agent with the ready label, as pick does. There are
// no dependencies, no milestones ahead and no done: an empty queue is idle.
func (a *autopilot) pickLoose(ctx context.Context) error {
	wf := a.spec.Workflow
	for i := range a.ap.Items {
		if a.running() >= int(a.maxParallel()) || a.ap.FrozenAt != nil {
			return nil
		}
		it := &a.ap.Items[i]
		if it.State != aiv1.AutopilotItemPending {
			continue
		}
		a.log.Info("loose issues queue starts the next issue", "issue", it.IID)
		if !hasAny(a.issues[it.IID], wf.ActiveLabels()...) { // else a human already handed it over
			if _, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, it.IID, gitlab.UpdateIssueOptions{
				AddLabels: []string{wf.ReadyLabel}, RemoveLabels: []string{wf.DraftLabel, wf.DoneLabel},
			}); err != nil {
				return fmt.Errorf("moving issue #%d to %q: %w", it.IID, wf.ReadyLabel, err)
			}
		}
		it.State, it.Hold = aiv1.AutopilotItemActive, nil
	}
	return nil
}

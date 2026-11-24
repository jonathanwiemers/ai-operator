package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/timefmt"
)

// reportDeliveries says where the owner messages the runner delivered to the
// implementers of st's milestone went: one line on the tracking issue, on
// the issue and in status.pilot.acted, then the message leaves
// spec.source.ownerMessages. Only messages still there are reported, so a
// crash between report and drop may repeat a line, never lose one. A
// message still pending after its run ended gets one run request.
func (r *AgentProjectReconciler) reportDeliveries(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st, coord *aiv1.AgentTask,
	tracking int64, issues map[int64]gitlab.Issue, tasks map[int64]*aiv1.AgentTask) error {
	var acted []aiv1.PilotActed
	reported := map[int64][]string{}
	for _, iid := range slices.Sorted(maps.Keys(tasks)) {
		t := tasks[iid]
		if t.Spec.Milestone == nil || t.Spec.Milestone.IID != st.Spec.Milestone.IID {
			continue
		}
		for _, m := range t.Spec.Source.OwnerMessages {
			d := t.Delivery(m.ID)
			if d == nil {
				continue
			}
			where := describeDelivery(iid, *d, t.Status.Run.Calls, runInProgress(t))
			if where == "" {
				continue // its call is not recorded yet
			}
			body := fmt.Sprintf("🧭 Message from `@%s` via the pilot (%s): %s", m.Owner, inertLine(m.Text, 80), where)
			for _, on := range []int64{tracking, iid} {
				if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, on, body); err != nil {
					log.FromContext(ctx).Info("could not report where an owner message went", "issue", on, "error", err.Error())
				}
			}
			line := describeAction(aiv1.PilotAction{Tool: "instruct", IssueIID: iid, Owner: m.Owner, Text: m.Text}, nil) + "; " + where
			acted = append(acted, aiv1.PilotActed{At: metav1.NewTime(r.now()), By: m.Owner, Ran: true, Issues: []int64{iid},
				Summary: cutText(fmt.Sprintf("Message from @%s %s", m.Owner, where), aiv1.MaxPilotActedSummary),
				Lines:   []string{cutText(line, aiv1.MaxPilotActedLine)}})
			reported[iid] = append(reported[iid], m.ID)
		}
		if len(reported[iid]) == 0 {
			if err := r.requestLeftovers(ctx, gl, spec, st, t, iid, issues); err != nil {
				return err
			}
		}
	}
	if len(acted) == 0 {
		return nil
	}
	if err := r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) {
		ps.Acted = append(ps.Acted, acted...)
		ps.Acted = ps.Acted[max(0, len(ps.Acted)-aiv1.MaxPilotActed):]
	}); err != nil {
		return err
	}
	for iid, ids := range reported {
		t := tasks[iid]
		// On a fresh copy, tried again on a conflict: the runner writes the task.
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := r.Get(ctx, client.ObjectKeyFromObject(t), t); err != nil {
				return err
			}
			t.Spec.Source.OwnerMessages = slices.DeleteFunc(t.Spec.Source.OwnerMessages, func(m aiv1.OwnerMessage) bool { return slices.Contains(ids, m.ID) })
			return r.Update(ctx, t)
		}); err != nil {
			return err
		}
	}
	return nil
}

// requestLeftovers requests one run of the open issue of task t when an owner
// message waits that came after its newest run started, and no run is in
// progress or requested: the message goes into that run. The run it
// requests starts after the message, so it is requested once.
func (r *AgentProjectReconciler) requestLeftovers(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st, t *aiv1.AgentTask, iid int64, issues map[int64]gitlab.Issue) error {
	if is, ok := issues[iid]; !ok || is.State != "opened" || !leftover(t) {
		return nil
	}
	requested := false
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, client.ObjectKeyFromObject(t), t); err != nil {
			return err
		}
		if requested = leftover(t); !requested {
			return nil
		}
		return r.requestRun(ctx, gl, spec, t, iid)
	}); err != nil || !requested {
		return err
	}
	pending := t.PendingOwnerMessages()
	log.FromContext(ctx).Info("an owner message waits after its run, requesting the next run", "task", t.Name, "messages", len(pending))
	r.commandRun(ctx, st, t, pilotBy(pending[len(pending)-1].Owner))
	return nil
}

// leftover reports whether t has an owner message pending that came after
// its newest run started, with no run in progress or requested.
func leftover(t *aiv1.AgentTask) bool {
	if runInProgress(t) || t.RunPending() {
		return false
	}
	started, ok := newestRunStart(t)
	if !ok {
		return false
	}
	return slices.ContainsFunc(t.PendingOwnerMessages(), func(m aiv1.OwnerMessage) bool { return started.Before(m.At.Time) })
}

// newestRunStart is when the newest finished run of t started, as far as its
// calls tell: the start of its first call. False when none is recorded.
func newestRunStart(t *aiv1.AgentTask) (time.Time, bool) {
	var start time.Time
	for _, c := range t.Status.Run.Calls {
		if c.Run == int64(t.Status.Run.RunCount) && (start.IsZero() || c.At.Time.Before(start)) {
			start = c.At.Time
		}
	}
	return start, !start.IsZero()
}

// runInProgress reports whether the runner works on a run of t.
func runInProgress(t *aiv1.AgentTask) bool {
	return t.Status.Run.ActiveRunRequest > t.Status.Run.ObservedRunRequest
}

// describeDelivery is where delivery d of an owner message to #iid went,
// with its time in UTC; calls are the task's calls. Empty while the call a
// resumed message went into is not recorded yet and the run goes on.
func describeDelivery(iid int64, d aiv1.DeliveredMessage, calls []aiv1.AgentCall, running bool) string {
	at := timefmt.UTCMinute(d.At.Time)
	switch d.How {
	case aiv1.DeliveryStream:
		return fmt.Sprintf("delivered to #%d's running implementer at %s", iid, at)
	case aiv1.DeliveryNextRun:
		return fmt.Sprintf("delivered with #%d's next run at %s", iid, at)
	}
	var stopped, call *aiv1.AgentCall
	for i := range calls {
		switch calls[i].Seq {
		case d.Call - 1:
			stopped = &calls[i]
		case d.Call:
			call = &calls[i]
		}
	}
	switch {
	case stopped != nil && stopped.StoppedForMessage:
		return fmt.Sprintf("delivered to #%d's implementer, the step restarted with it, at %s", iid, at)
	case call != nil:
		return fmt.Sprintf("delivered with #%d's %s call at %s", iid, call.Kind, at)
	case running:
		return ""
	}
	return fmt.Sprintf("delivered with #%d's next implementer call at %s", iid, at)
}

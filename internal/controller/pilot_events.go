package controller

import (
	"context"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// The pilot also speaks first: the operator wakes it for the events of its
// milestone, once per event key. A new head or a new reason is a new key.

// pilotEvents lists the events of a milestone that hold now: an autopilot
// pause, an issue that needs input or is blocked by infra or quota, and on
// the main-red lane (lane: the autopilot milestone that drives the main-red
// issue) a red or flaky default branch, so the owners get one prompt for it.
func pilotEvents(spec aiv1.AgentProjectSpec, mh *aiv1.MainHealthStatus, st *aiv1.AgentTask, issues map[int64]gitlab.Issue, tasks map[int64]*aiv1.AgentTask, lane bool, now time.Time) []aiv1.PilotEvent {
	var out []aiv1.PilotEvent
	add := func(kind string, iid int64, text string) {
		ev := aiv1.PilotEvent{Key: fmt.Sprintf("%s/%d/", kind, iid), IssueIID: iid, Text: text}
		if t := tasks[iid]; t != nil {
			ev.Key += t.Status.Run.PushedSHA
			if ci := t.Status.CI; ci != nil && (ci.State == aiv1.CIStateFailed || ci.State == aiv1.CIStateBaseFailing) {
				ev.PipelineID = ci.PipelineID
			}
		}
		out = append(out, ev)
	}
	// A pause over an issue and its needs-input label are one event, with
	// the pause's text. A milestone whose planning never ran (it waits for
	// its turn) has nothing to assess and does not stall: its pause wakes
	// nobody, and the owners can still talk to the pilot.
	if ap := st.Status.Autopilot; ap != nil && ap.Phase == aiv1.AutopilotPaused && !specNeverRan(st) {
		kind := "stuck"
		if ap.PausedFor == 0 {
			kind = "pause-" + hashString(ap.PausedReason)
		}
		add(kind, ap.PausedFor, "Autopilot paused: "+ap.PausedReason)
		if ev, ok := stalledEvent(spec, ap, tasks, now); ok {
			out = append(out, ev)
		}
	}
	for iid, t := range tasks {
		is, ok := issues[iid]
		if !ok || is.Milestone == nil || is.Milestone.IID != st.Spec.Milestone.IID {
			continue
		}
		switch b := t.Status.Blocked; {
		case b != nil && (b.Reason == aiv1.BlockInfra || b.Reason == aiv1.BlockQuota):
			add(string(b.Reason), iid, fmt.Sprintf("#%d is blocked (%s): %s", iid, b.Reason, b.Message))
		case is.HasLabel(spec.Workflow.NeedsInputLabel):
			if reason, ok := needsInputReason(iid, t); ok {
				add("stuck", iid, reason)
				break
			}
			add("stuck", iid, fmt.Sprintf("#%d needs input: CI is still red after the fix rounds, the run failed or the agent asked a question", iid))
		}
	}
	switch {
	case mh == nil || !st.Spec.Autopilot || !lane:
	case mh.State == aiv1.MainHealthRed && mh.IssueIID != 0:
		out = append(out, aiv1.PilotEvent{Key: fmt.Sprintf("%s%d", aiv1.MainRedEventPrefix, mh.IssueIID), IssueIID: mh.IssueIID, PipelineID: mh.PipelineID,
			Text: fmt.Sprintf("The default branch is red (#%d): %s", mh.IssueIID, strings.Join(mh.FailedJobs, ", "))})
	case mh.State == aiv1.MainHealthFlaky && len(mh.Flaky) > 0:
		for _, f := range mh.Flaky {
			out = append(out, flakyEvent(mh.PipelineID, f))
		}
	case mh.State == aiv1.MainHealthFlaky:
		out = append(out, aiv1.PilotEvent{Key: fmt.Sprintf("%s%d", aiv1.FlakyEventPrefix, mh.PipelineID), PipelineID: mh.PipelineID,
			Text: "Jobs of the default branch passed only on retry: " + strings.Join(mh.FailedJobs, ", ")})
	}
	slices.SortStableFunc(out, func(a, b aiv1.PilotEvent) int { return strings.Compare(a.Key, b.Key) })
	return slices.CompactFunc(out, func(a, b aiv1.PilotEvent) bool { return a.Key == b.Key })
}

// specNeverRan reports whether spec task st has never finished a planning
// run and has no plan: it waits for its turn, or its first run is pending.
func specNeverRan(st *aiv1.AgentTask) bool {
	return st.Status.Run.ObservedRunRequest == 0 && len(st.Status.Plan) == 0
}

// stalledEvent is the event of a pause (ap) whose state hash has not
// changed for pilot.stuckAfter at now: keyed by the pause's start, so it
// wakes the pilot once per pause; its text has the pause's ways out. An
// owner's pause never stalls: its way out is theirs, a resume.
func stalledEvent(spec aiv1.AgentProjectSpec, ap *aiv1.AutopilotStatus, tasks map[int64]*aiv1.AgentTask, now time.Time) (aiv1.PilotEvent, bool) {
	if ap.PausedAt == nil || ap.PauseKind == aiv1.PauseOwner {
		return aiv1.PilotEvent{}, false
	}
	since := ap.PausedAt.Time
	if c := ap.StateChangedAt; c != nil && c.After(since) {
		since = c.Time
	}
	if now.Sub(since) < spec.Pilot.StuckAfterOf() {
		return aiv1.PilotEvent{}, false
	}
	d := strings.TrimSuffix(now.Sub(ap.PausedAt.Time).Round(time.Minute).String(), "0s")
	exits := (&autopilot{spec: spec, tasks: tasks}).exitLines(ap.PauseKind, ap.PausedFor)
	return aiv1.PilotEvent{Key: fmt.Sprintf("%s%d", aiv1.StalledEventPrefix, ap.PausedAt.Unix()), IssueIID: ap.PausedFor,
		Text: fmt.Sprintf("Autopilot has been paused for %s and nothing changed: %s\n\nWays out:\n%s", d, ap.PausedReason, exits)}, true
}

// flakyEvent is the event of a job that passed on retry: its flaky issue,
// the failing tests and the end of the failed attempt's log, cut from the
// front so the text fits. The pipeline is green, so it has none.
func flakyEvent(pipeline int64, f aiv1.FlakyJobReport) aiv1.PilotEvent {
	// The first paragraph, the assessment's headline, quotes nothing of
	// the log.
	text := fmt.Sprintf("`%s` of the default branch passed only on retry (flaky issue #%d).", f.Job, f.IssueIID)
	if len(f.Tests) > 0 {
		text += "\n\nFailing tests in the failed attempt's log: " + strings.Join(f.Tests, ", ") + "."
	}
	text += "\n\nEnd of the failed attempt's log:\n\n```text\n"
	tail := f.LogTail
	if n := maxPilotMessage - len(text) - 10; len(tail) > n {
		tail = strings.ToValidUTF8(tail[len(tail)-max(n, 0):], "")
	}
	return aiv1.PilotEvent{Key: fmt.Sprintf("%s%d/%d", aiv1.FlakyEventPrefix, pipeline, f.IssueIID), IssueIID: f.IssueIID, Text: text + tail + "\n```"}
}

// maxPilotEvents bounds the event keys a pilot remembers after their
// events ended; the keys of those that hold are all kept.
const maxPilotEvents = 100

// wakePilot hands the events the pilot has not had yet to it, with a run
// request. It remembers every key it handed over, also after the event
// ended: an event that comes back with the same key (a label toggled, a
// pause resumed and set again) wakes nobody, so nobody can run up pods by
// flapping a label.
func (r *AgentProjectReconciler) wakePilot(ctx context.Context, coord *aiv1.AgentTask, events []aiv1.PilotEvent) error {
	// Messages may have been handed to it in this poll.
	if err := r.Get(ctx, client.ObjectKeyFromObject(coord), coord); err != nil {
		return err
	}
	var had []string
	if coord.Status.Pilot != nil {
		had = coord.Status.Pilot.Events
	}
	src, observed := &coord.Spec.Source, coord.Status.Run.ObservedRunRequest
	var keys []string
	var fresh []aiv1.PilotEvent
	for _, ev := range events {
		keys = append(keys, ev.Key)
		// Also those still in the spec: the status patch below may have
		// failed after the update.
		if !slices.Contains(had, ev.Key) && !slices.ContainsFunc(src.PilotEvents, func(p aiv1.PilotEvent) bool { return p.Key == ev.Key }) {
			ev.Text = textutil.Truncate(ev.Text, maxPilotMessage)
			ev.Request = coord.Spec.RunRequest + 1
			fresh = append(fresh, ev)
		}
	}
	for _, p := range src.PilotEvents {
		if !slices.Contains(keys, p.Key) {
			keys = append(keys, p.Key)
		}
	}
	ended := 0
	for _, k := range had {
		if ended < maxPilotEvents && !slices.Contains(keys, k) {
			keys, ended = append(keys, k), ended+1
		}
	}
	if len(fresh) > 0 {
		coord.Spec.RunRequest++
		// Assessed events go once the status has their keys; the others
		// stay.
		src.PilotEvents = append(slices.DeleteFunc(src.PilotEvents, func(e aiv1.PilotEvent) bool {
			return e.Request <= observed && slices.Contains(had, e.Key)
		}), fresh...)
		log.FromContext(ctx).Info("waking the pilot", "task", coord.Name, "events", len(fresh))
		if err := r.Update(ctx, coord); err != nil {
			return err
		}
	}
	if slices.Equal(keys, had) {
		return nil
	}
	return r.patchPilot(ctx, coord, func(ps *aiv1.PilotStatus) { ps.Events = keys })
}

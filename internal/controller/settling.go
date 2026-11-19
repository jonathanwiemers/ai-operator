package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// autopilotSettling reports whether st's autopilot is done and only waits for
// its retrospective's follow-ups: phase Done, the retrospective posted, its
// follow-ups not settled yet, no re-plan pending.
func autopilotSettling(st *aiv1.AgentTask, now time.Time) bool {
	ap, ps := st.Status.Autopilot, st.Status.Pilot
	return st.Spec.Autopilot && ap != nil && ap.Phase == aiv1.AutopilotDone && !st.RunPending() &&
		ps != nil && ps.Retrospective != nil && !ps.Retrospective.FollowUpsSettled(now)
}

// settle is the sync of a settling milestone: it reads the tracking issue's
// notes and runs only what they can still change (the pilot, the follow-ups,
// release records not ended). It returns false, with the notes kept for
// commands, while a command is pending: the full sync applies it with the
// issues loaded. The issues are listed only to render a changed tracking
// issue.
func (a *autopilot) settle(ctx context.Context) (bool, error) {
	if ok, err := a.trackingNotes(ctx); !ok || a.commandPending() {
		return !ok, err
	}
	before, retro := a.ap.DeepCopy(), a.st.Status.Pilot.Retrospective.DeepCopy()
	coord := a.pilot(ctx, a.notes)
	err := a.followUps(ctx, a.notes)
	if err == nil {
		err = a.pilotReleases(ctx, coord)
	}
	if err == nil && slices.ContainsFunc(a.ap.Releases, func(r aiv1.ReleaseRecord) bool { return !r.State.Ended() }) {
		err = a.releases(ctx)
	}
	if err != nil || equality.Semantic.DeepEqual(before, a.ap) && equality.Semantic.DeepEqual(retro, a.st.Status.Pilot.Retrospective) {
		return true, err
	}
	if err := a.loadIssues(ctx); err != nil {
		return true, err
	}
	return true, a.updateTracking(ctx)
}

// trackingNotes lists the tracking issue's notes into a.notes, once per
// poll. It reports false when there are none to read: no tracking issue,
// it is gone (trackingGone), or listing failed.
func (a *autopilot) trackingNotes(ctx context.Context) (bool, error) {
	if a.notes != nil || a.ap.TrackingIssueIID == 0 {
		return a.notes != nil, nil
	}
	notes, err := a.gl.ListIssueNotes(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID)
	if a.trackingGone(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("listing notes of the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	a.notes = append([]gitlab.Note{}, notes...)
	return true, nil
}

// commandPending reports whether a tracking-issue command, an accepted pilot
// command or a Desk command waits to be applied.
func (a *autopilot) commandPending() bool {
	src := &a.st.Spec.Source
	return slices.ContainsFunc(a.notes, func(n gitlab.Note) bool {
		return n.ID > a.ap.LastCommandNoteID && IsCommand(n, a.project.Status.BotUserID, a.spec.Workflow.CommandPrefix)
	}) || slices.ContainsFunc(a.st.Status.Pilot.Commands, func(c aiv1.PilotCommand) bool { return c.Seq > a.ap.LastPilotCommand }) ||
		slices.ContainsFunc(src.ChatMessages, func(m aiv1.ChatMessage) bool { return m.Kind == "command" && m.ID > src.LastChatCommandID })
}

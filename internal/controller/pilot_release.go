package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// maxPilotReleaseWhy is how long the reason of a pilot's release record is.
const maxPilotReleaseWhy = 200

// pilotDropped prefixes the reason of a record the pilot dropped.
const pilotDropped = "dropped by the pilot: "

// pilotReleases validates and carries out the pilot's release decisions
// (status.pilot.releaseDecisions of the milestone's Coordinate task) newer
// than LastPilotRelease, once each, see docs/usage.md#releases. One that is
// not applied gets one comment on the tracking issue; the watermark
// advances either way. A failed comment is tried again on the next poll.
func (a *autopilot) pilotReleases(ctx context.Context, coord *aiv1.AgentTask) error {
	if coord == nil || coord.Status.Pilot == nil {
		return nil
	}
	for _, d := range coord.Status.Pilot.ReleaseDecisions {
		if d.Seq <= a.ap.LastPilotRelease {
			continue
		}
		if answer := a.pilotRelease(d); answer != "" {
			if err := a.comment(ctx, answer); err != nil {
				return err
			}
		}
		a.ap.LastPilotRelease = d.Seq
	}
	return nil
}

// pilotRelease applies the pilot's decision d to the milestone's records
// and returns the comment for a decision it did not apply, "" for one it
// did. Valid is a decision on an issue of the queue, with a reason, that
// does not touch the final release:
//
//	release true   adds {pilot, after the issue, why}: Planned while the
//	               issue is not merged, else Waiting for its merge commit;
//	               nothing while a record after the issue has not ended or
//	               released it ("already decided")
//	release false  drops the Planned and Waiting records after the issue
//	               that are not Final; Running ones are left alone
func (a *autopilot) pilotRelease(d aiv1.PilotReleaseDecision) string {
	why := oneLine(d.Why, maxPilotReleaseWhy)
	what := fmt.Sprintf("the pilot's release after #%d", d.IssueIID)
	if !d.Release {
		what = fmt.Sprintf("the pilot's drop of the release after #%d", d.IssueIID)
	}
	if why != "" {
		what += " (" + untrusted(why) + ")"
	}
	refused := func(reason string) string {
		a.log.Info("pilot release decision refused", "seq", d.Seq, "issue", d.IssueIID, "release", d.Release, "reason", reason)
		return sentence("🛑 Refused "+what+": "+reason) + " Nothing changed."
	}
	answered := func(reason string) string {
		a.log.Info("pilot release decision not needed", "seq", d.Seq, "issue", d.IssueIID, "release", d.Release, "reason", reason)
		return sentence("ℹ️ Nothing to do for " + what + ": " + reason)
	}
	it := a.item(d.IssueIID)
	switch {
	case it == nil:
		return refused(fmt.Sprintf("#%d is not an issue of this milestone", d.IssueIID))
	case why == "":
		return refused("it gives no reason")
	}
	now := metav1.NewTime(a.r.now())
	if d.Release {
		for _, o := range a.ap.Releases {
			if o.AfterIssue == d.IssueIID && (!o.State.Ended() || o.State == aiv1.ReleaseReleased || o.State == aiv1.ReleaseAlreadyReleased) {
				return answered(fmt.Sprintf("already decided (%s release, %s)", o.By, o.State))
			}
		}
		rec := aiv1.ReleaseRecord{ID: nextReleaseID(a.ap.Releases), By: aiv1.ReleaseByPilot, AfterIssue: d.IssueIID,
			Why: why, State: aiv1.ReleasePlanned, DecidedAt: now}
		if it.State == aiv1.AutopilotItemMerged && it.MergedSHA != "" {
			rec.State, rec.SHA = aiv1.ReleaseWaiting, it.MergedSHA
		}
		a.ap.Releases = append(a.ap.Releases, rec)
		a.log.Info("pilot release added", "release", rec.ID, "issue", d.IssueIID, "state", rec.State)
		return ""
	}
	dropped, final, running := 0, false, false
	for i := range a.ap.Releases {
		o := &a.ap.Releases[i]
		switch {
		case o.AfterIssue != d.IssueIID:
		case o.Final:
			final = final || !o.State.Ended()
		case o.State == aiv1.ReleasePlanned || o.State == aiv1.ReleaseWaiting:
			o.State, o.Reason, o.EndedAt = aiv1.ReleaseDropped, pilotDropped+why, &now
			dropped++
			a.log.Info("pilot release dropped", "release", o.ID, "issue", d.IssueIID)
		case o.State == aiv1.ReleaseRunning:
			running = true
		}
	}
	switch {
	case dropped > 0:
		return ""
	case final:
		return refused("the release after it is the milestone's final release, which is the operator's")
	case running:
		return answered("the release after it already runs")
	}
	return answered(fmt.Sprintf("no planned release follows #%d", d.IssueIID))
}

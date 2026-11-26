package controller

import (
	"context"
	"fmt"
	"slices"

	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// pause stops the queue over issue iid (0: none) and says why and the ways
// out, once.
func (a *autopilot) pause(ctx context.Context, kind aiv1.PauseKind, iid int64, reason string) error {
	// Above a limit of 1, a problem of one queue item holds it; a milestone
	// pause already over it goes on as one. In the loose issues queue every
	// problem of an item holds only that item.
	if it := a.item(iid); iid != 0 && it != nil && it != a.ap.MainRedFix && it.State == aiv1.AutopilotItemActive && (kind.ItemKind() || a.loose) && a.holdsItems() &&
		(a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor != iid) {
		return a.hold(ctx, it, kind, reason)
	}
	// A pause over the landing item frees the lane, once it holds (after
	// the comment): the next ready item lands.
	freeLane := func() {
		if it := a.item(iid); iid != 0 && it != nil && iid == a.ap.Landing {
			a.unready(it)
		}
	}
	if a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedReason == reason {
		a.ap.PausedFor = iid
		freeLane()
		return nil
	}
	a.log.Info("autopilot paused", "reason", reason)
	// Comment first: if it fails, the pause is tried again on the next poll.
	// Defuse the reason (it can quote job names and titles); the owners'
	// mention is appended after, so it stays a real ping.
	if err := a.comment(ctx, fmt.Sprintf("⏸️ Autopilot paused: %s.\n\nWays out:\n%s", notify.Defuse(reason), a.exitLines(kind, iid, false))+a.mention(aiv1.NotifyAutopilotPaused)); err != nil {
		return err
	}
	a.startPause(kind, iid)
	a.ap.PausedReason, a.ap.PauseKind = reason, kind
	a.ap.PausedFor, a.ap.ResumeWhenReady = iid, false
	freeLane()
	return nil
}

// holdsItems: issue problems hold the issue rather than pause the milestone,
// above a limit of 1 (at 1 a hold would free the only slot). The loose issues
// queue always holds: its issues are unrelated, and a held one keeps its slot.
func (a *autopilot) holdsItems() bool { return a.loose || a.maxParallel() > 1 }

// hold holds it for a problem of kind: it is not driven, leaves the landing
// lane and frees its slot, the other issues go on. It says why and the ways
// out once per reason; the comment goes first, a failed one is tried again.
func (a *autopilot) hold(ctx context.Context, it *aiv1.AutopilotItem, kind aiv1.PauseKind, reason string) error {
	if it.Hold != nil && it.Hold.Reason == reason {
		return nil
	}
	a.log.Info("autopilot holds an issue", "issue", it.IID, "reason", reason)
	if err := a.comment(ctx, fmt.Sprintf("⏸️ #%d is held: %s. The other issues go on.\n\nWays out:\n%s", it.IID, notify.Defuse(reason), a.exitLines(kind, it.IID, true))+a.mention(aiv1.NotifyAutopilotPaused)); err != nil {
		return err
	}
	h := aiv1.ItemHold{Kind: kind, Reason: reason, Since: metav1.Time{Time: a.r.now()}}
	if old := it.Hold; old != nil { // held anew over the same item: as startPause
		h.RunBy, h.RunHead = old.RunBy, old.RunHead
		if old.Kind == kind {
			h.Since = old.Since
		}
	} else if t := a.tasks[it.IID]; t != nil {
		h.RunHead = t.Status.Run.PushedSHA
	}
	it.Hold = &h
	a.unready(it)
	return nil
}

// unhold ends it's hold as res says, with why (resumeHolds): it lifts the
// item's infra block, says "#N goes on" on the tracking issue and drops the
// hold. A failed comment keeps the hold for the next poll.
func (a *autopilot) unhold(ctx context.Context, it *aiv1.AutopilotItem, res resumption, why string) error {
	if err := a.unblockItem(ctx, it); err != nil {
		return err
	}
	if err := a.comment(ctx, goesOnLine(it.IID, res, why, a.spec.Workflow.CommandPrefix)); err != nil {
		return err
	}
	a.log.Info("a held issue goes on", "issue", it.IID, "why", why, "via", res.via)
	it.Hold = nil
	return nil
}

// exitLines renders the ways out of a pause of kind over issue iid
// (aiv1.PauseExits) as a list, with the project's command prefix and labels.
// A command after a label or a GitLab action goes on the tracking issue.
// For a hold of iid (held), `resume` names the issue: it lifts that hold.
func (a *autopilot) exitLines(kind aiv1.PauseKind, iid int64, held bool) string {
	wf := a.spec.Workflow
	issue, ref, head := "the issue", "#N", "<sha>"
	if iid != 0 {
		issue, ref = fmt.Sprintf("#%d", iid), fmt.Sprintf("#%d", iid)
		if t := a.tasks[iid]; t != nil && t.Status.Run.PushedSHA != "" {
			head = short(t.Status.Run.PushedSHA)
		}
	}
	args := map[string]string{"skip": " " + ref, "unskip": " " + ref, "startAnyway": " " + ref, "rebase": " " + ref, "mergeBase": " " + ref, "merge": " " + ref + " " + head}
	if held && iid != 0 {
		args["resume"] = " " + ref
	}
	cmd := func(word string, target aiv1.ExitTarget) string {
		where := "here"
		if target == aiv1.ExitTargetImplement {
			where = "on " + issue
		}
		return fmt.Sprintf("`%s %s%s` %s", wf.CommandPrefix, word, args[word], where)
	}
	var b strings.Builder
	for _, e := range aiv1.PauseExits(kind) {
		var how string
		switch {
		case e.Auto:
			how = "By itself, once " + e.When
		case e.GitLab:
			how = "In GitLab, " + e.When
		case e.Prompt:
			how = fmt.Sprintf("Answer the pilot's prompt (or ask for one with `%s …`) %s", wf.PilotPrefix, e.When)
		case e.Label != "":
			how = fmt.Sprintf("Label %s `%s` %s", issue, wf.RoleLabel(e.Label), e.When)
		default:
			how = cmd(e.Command, e.Target)
			if e.When != "" {
				how += ", " + e.When
			}
		}
		if !e.Auto && e.Command != "" && (e.GitLab || e.Label != "") {
			how += ", then " + cmd(e.Command, aiv1.ExitTargetSpec)
		}
		fmt.Fprintf(&b, "- %s: %s.\n", strings.TrimSpace(how), e.Does)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// lift ends a pause: autopilot runs again. Every resume goes through here;
// by is who ended it and via how (status.autopilot.resumedBy and resumedVia,
// for the history's autopilot.resumed event).
func (a *autopilot) lift(by aiv1.PauseResolver, via aiv1.PauseResumeVia) {
	a.ap.Phase, a.ap.PausedReason, a.ap.PauseKind = aiv1.AutopilotRunning, "", ""
	a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
	a.ap.PausedAt, a.ap.ResumedBy, a.ap.ResumedVia = nil, by, via
	a.ap.PauseRunBy, a.ap.PauseRunHead = "", ""
}

// resumed says on the tracking issue that autopilot continues, why, who
// ended the pause and how, then ends it. The comment goes first: if it
// fails, the pause stays and the next poll tries again.
func (a *autopilot) resumed(ctx context.Context, res resumption, why string) error {
	if err := a.comment(ctx, resumeLine(res, why, a.spec.Workflow.CommandPrefix)); err != nil {
		return err
	}
	a.lift(res.by, res.via)
	return nil
}

// resumeAll is `resume` without an issue, by someone: it lifts the
// milestone's pause and every hold, with one comment. The gate checks each
// issue again on the next poll and holds it again if the problem is still
// there.
func (a *autopilot) resumeAll(ctx context.Context, by string) error {
	var held []int64
	for i := range a.ap.Items {
		if it := &a.ap.Items[i]; it.State == aiv1.AutopilotItemActive && it.Hold != nil {
			if err := a.unblockItem(ctx, it); err != nil {
				return err
			}
			held = append(held, it.IID)
		}
	}
	res := byCommand(by)
	switch {
	case a.ap.Phase == aiv1.AutopilotPaused:
		if a.landingPause() {
			a.retryLandings(ctx, by) // one more rebase for each
		}
		if a.ap.PauseKind == aiv1.PauseRelease {
			a.retryFinalRelease(by)
		}
		if err := a.unblockActive(ctx); err != nil {
			return err
		}
		why := "resumed"
		if len(held) > 0 {
			why += "; " + goOn(held)
		}
		if err := a.resumed(ctx, res, why); err != nil {
			return err
		}
	case len(held) > 0:
		if err := a.comment(ctx, fmt.Sprintf("▶️ %s: resumed (%s).", goOn(held), resumeEnded(res, a.spec.Workflow.CommandPrefix))); err != nil {
			return err
		}
	}
	for i := range a.ap.Items {
		if it := &a.ap.Items[i]; slices.Contains(held, it.IID) {
			it.Hold = nil
		}
	}
	if len(held) > 0 {
		a.log.Info("held issues go on", "issues", held, "by", by)
	}
	return nil
}

// goOn says that the issues iids go on: "#1 goes on", "#1, #2 go on".
func goOn(iids []int64) string {
	if len(iids) == 1 {
		return issueList(iids) + " goes on"
	}
	return issueList(iids) + " go on"
}

// startPause pauses the queue for kind over issue iid (0: none) and clears
// how the last pause ended. A new pause, also one of another kind while
// paused, starts its clock (PausedAt). A pause over another issue than
// before forgets who asked for a run during it (pauseRunBy) and starts
// from that issue's pushed head.
func (a *autopilot) startPause(kind aiv1.PauseKind, iid int64) {
	a.ap.ResumedBy, a.ap.ResumedVia = "", ""
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor != iid {
		a.ap.PauseRunBy, a.ap.PauseRunHead = "", ""
		if t := a.tasks[iid]; iid != 0 && t != nil {
			a.ap.PauseRunHead = t.Status.Run.PushedSHA
		}
	}
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedAt == nil || a.ap.PauseKind != kind {
		a.ap.PausedAt = &metav1.Time{Time: a.r.now()}
	}
	a.ap.Phase = aiv1.AutopilotPaused
}

// askedRun records that the autopilot itself asked for a run of the paused
// issue it for head (pauseRunBy), if it is paused for it.
func (a *autopilot) askedRun(it *aiv1.AutopilotItem, head string) {
	notePauseRun(a.ap, it.IID, aiv1.PauseRunByAutopilot, head)
}

// stamp records the queue's state hash (stateHash) and when it changed; a
// pause from before PausedAt existed starts its clock now.
func (a *autopilot) stamp() {
	now := &metav1.Time{Time: a.r.now()}
	switch {
	case a.ap.Phase != aiv1.AutopilotPaused:
		a.ap.PausedAt = nil
	case a.ap.PausedAt == nil:
		a.ap.PausedAt = now
	}
	if h := a.stateHash(); h != a.ap.StateHash {
		a.ap.StateHash, a.ap.StateChangedAt = h, now
	}
}

// stateHash hashes what a stuck pause waits on to change: the items (also
// the main-red fix) with their MR, rebase, startAnyway, hold, head and CI, main
// health, the phase and the pause reason. Spend, the tracking issue and
// command marks are left out: they change without moving the queue.
func (a *autopilot) stateHash() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%d\n", a.ap.Phase, a.ap.PausedReason, a.ap.Landing)
	if mh := a.project.Status.MainHealth; mh != nil {
		fmt.Fprintf(&b, "main %s %s\n", mh.State, mh.SHA)
	}
	items := a.ap.Items
	if a.ap.MainRedFix != nil {
		items = append(slices.Clone(items), *a.ap.MainRedFix)
	}
	for _, it := range items {
		fmt.Fprintf(&b, "%d %s !%d %s %t %t", it.IID, it.State, it.MergeRequestIID, it.RebasedFrom, it.StartAnyway, it.ReadyAt != nil)
		if h := it.Hold; h != nil {
			fmt.Fprintf(&b, " hold %s %q", h.Kind, h.Reason)
		}
		if t := a.tasks[it.IID]; t != nil {
			fmt.Fprintf(&b, " %s", t.Status.Run.PushedSHA)
			if ci := t.Status.CI; ci != nil {
				fmt.Fprintf(&b, " %s %d", ci.State, ci.PipelineID)
			}
		}
		b.WriteString("\n")
	}
	return hashString(b.String())
}

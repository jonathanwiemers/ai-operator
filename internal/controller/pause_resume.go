package controller

import (
	"context"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// resumption is how a pause ends: who (by) and how (via), for the resume
// comment and the status (resumedBy, resumedVia).
type resumption struct {
	by  aiv1.PauseResolver
	via aiv1.PauseResumeVia
	// who is the command's by for an owner, the paused issue (#N) for a
	// later run.
	who string
	// runBy is who started the later run of a takeover (pauseRunBy).
	runBy string
}

// pilotSelf is the by of a command the pilot runs on its own (managed
// autonomy); an owner's answer via the pilot names the owner.
const pilotSelf = "the pilot"

// resolverOf is who ends a pause with a command by someone: the pilot on
// its own, otherwise an owner.
func resolverOf(by string) aiv1.PauseResolver {
	if by == pilotSelf {
		return aiv1.ResolvedByPilot
	}
	return aiv1.ResolvedByOwner
}

// byOperator is an automatic exit: the cause went away.
var byOperator = resumption{by: aiv1.ResolvedByOperator, via: aiv1.ResumedViaCauseGone}

// byCommand is the end of a pause by a command of by: an owner's, or the
// pilot's on its own.
func byCommand(by string) resumption {
	return resumption{by: resolverOf(by), via: aiv1.ResumedViaCommand, who: by}
}

// resumeLine is the "▶️ Autopilot continues" comment for a pause ended as
// res says: who ended it and how, for a later run also how that run came
// about (prefix is the command prefix of a takeover's comment). Names are
// defused: they name a person but ping nobody.
func resumeLine(res resumption, why, prefix string) string {
	ended := "ended by the operator: the cause went away"
	switch by, who := res.by, res.who; {
	case by == aiv1.ResolvedByOwner && who != "":
		ended = "ended by " + notify.Defuse(who)
	case by == aiv1.ResolvedByOwner:
		ended = "ended by an owner"
	case by == aiv1.ResolvedByPilot:
		ended = "ended by the pilot"
	case by == aiv1.ResolvedByLaterRun:
		ended = "ended by a later run of " + notify.Defuse(who) + laterRunHow(res, prefix)
	case by == aiv1.ResolvedBySelfHealed:
		ended = "ended by the operator: it rebased the merge request"
	case res.via == aiv1.ResumedViaMergedByHand:
		ended = "ended by hand: the merge request was merged"
	case res.via == aiv1.ResumedViaClosedByHand:
		ended = "ended by hand: the issue was closed"
	}
	return fmt.Sprintf("▶️ Autopilot continues: %s (%s).", why, ended)
}

// laterRunHow says how the later run that ended a pause came about, "" when
// that is not known (nobody asked for it, or an owner's merge override let
// it through).
func laterRunHow(res resumption, prefix string) string {
	switch res.via {
	case aiv1.ResumedViaReReview:
		return ", a review-only run the autopilot started"
	case aiv1.ResumedViaPush:
		return ", which reviewed a head someone else pushed"
	case aiv1.ResumedViaTakeover:
		switch who := notify.Defuse(res.runBy); {
		case res.runBy == "":
			return ", which an owner's command started"
		case strings.HasPrefix(res.runBy, "@") && !strings.Contains(res.runBy, " "):
			return fmt.Sprintf(", which %s's `%s` comment started", who, prefix)
		default:
			return ", which " + who + " started"
		}
	}
	return ""
}

// laterRunVia is how a later run ended the pause in ap by passing the gate
// on head: the autopilot's review-only run on the head it asked for
// (reReview), a run an owner's or the pilot's command started (takeover),
// or a run that took a head nobody asked a run for over (push). With no
// run asked for and the head the pause started on, the cause went away (a
// pipeline retried by hand), as it does for a pause from before pauseRunBy
// was recorded.
func laterRunVia(ap *aiv1.AutopilotStatus, head string) aiv1.PauseResumeVia {
	switch by := ap.PauseRunBy; {
	case by == aiv1.PauseRunByAutopilot && head == ap.PauseRunHead:
		return aiv1.ResumedViaReReview
	case by != "" && by != aiv1.PauseRunByAutopilot:
		return aiv1.ResumedViaTakeover
	case head != ap.PauseRunHead && (ap.PauseRunHead != "" || by != ""):
		return aiv1.ResumedViaPush
	}
	return aiv1.ResumedViaCauseGone
}

// notePauseRun records in ap that by asked for a run of issue iid, for
// head, if the autopilot is paused for that issue. It reports whether ap
// changed.
func notePauseRun(ap *aiv1.AutopilotStatus, iid int64, by, head string) bool {
	if ap == nil || ap.Phase != aiv1.AutopilotPaused || iid == 0 || ap.PausedFor != iid || by == "" ||
		ap.PauseRunBy == by && ap.PauseRunHead == head {
		return false
	}
	ap.PauseRunBy, ap.PauseRunHead = by, head
	return true
}

// commandRun records on the spec task st that by's command (an owner's
// comment on the issue, a pilot action) asked for a run of the issue task,
// if the autopilot is paused for it: a later run that ends the pause was a
// takeover. A failed write is logged; the pause then ends as a push.
func (r *AgentProjectReconciler) commandRun(ctx context.Context, st, task *aiv1.AgentTask, by string) {
	if st == nil || task == nil || task.Spec.Issue == nil {
		return
	}
	orig := st.DeepCopy()
	if !notePauseRun(st.Status.Autopilot, task.Spec.Issue.IID, by, task.Status.Run.PushedSHA) {
		return
	}
	if err := r.Status().Patch(ctx, st, client.MergeFrom(orig)); err != nil {
		*st = *orig
		log.FromContext(ctx).Info("could not record who started the run of the paused issue", "task", st.Name,
			"issue", task.Spec.Issue.IID, "by", by, "error", err.Error())
	}
}

// pilotBy is the by of a command from the pilot: the owner who accepted its
// proposal (a username), or the pilot on its own (managed autonomy).
func pilotBy(owner string) string {
	if owner == "" {
		return pilotSelf
	}
	return "@" + owner + " via the pilot"
}

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// ensureTracking creates the tracking issue on the first poll.
func (a *autopilot) ensureTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID != 0 {
		is, ok := a.issues[a.ap.TrackingIssueIID]
		if !ok {
			// Not in the milestone any more: moved, or deleted.
			got, err := a.gl.GetIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID)
			if a.trackingGone(err) {
				return a.ensureTracking(ctx)
			}
			if err != nil {
				return fmt.Errorf("getting the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
			}
			is = *got
		}
		if is.State != "opened" && a.ap.Phase != aiv1.AutopilotDone {
			// Re-planned or switched back on after it was closed.
			_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{StateEvent: "reopen"})
			if a.trackingGone(err) {
				return a.ensureTracking(ctx)
			}
			if err != nil {
				return fmt.Errorf("reopening the tracking issue #%d: %w", is.IID, err)
			}
		}
		return nil
	}
	is, err := a.gl.CreateIssue(ctx, a.spec.GitLab.Project, gitlab.CreateIssueOptions{
		Title:       "🤖 Autopilot: " + a.st.Spec.Milestone.Title,
		Description: a.renderTracking(),
		Labels:      a.spec.Workflow.GeneratedLabel,
		MilestoneID: a.st.Spec.Milestone.ID,
	})
	if err != nil {
		return fmt.Errorf("creating the tracking issue: %w", err)
	}
	a.log.Info("autopilot tracking issue created", "issue", is.IID)
	a.ap.TrackingIssueIID = is.IID
	a.ap.TrackingHash = hashString(a.renderTracking())
	return nil
}

func (a *autopilot) updateTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	body := a.renderTracking()
	if hash := hashString(body); hash != a.ap.TrackingHash {
		_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, gitlab.UpdateIssueOptions{Description: body})
		if a.trackingGone(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("updating the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
		}
		a.ap.TrackingHash = hash
	}
	return nil
}

func (a *autopilot) closeTracking(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	if err := a.updateTracking(ctx); err != nil {
		return err
	}
	_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, gitlab.UpdateIssueOptions{StateEvent: "close"})
	if a.trackingGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("closing the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	return nil
}

// mention returns the line that mentions the milestone's owners, if the
// event mentions them.
func (a *autopilot) mention(e aiv1.NotifyEvent) string {
	if !a.spec.Notify.Wants(e) {
		return ""
	}
	return notify.Mention(a.st.Spec.Owners)
}

func (a *autopilot) comment(ctx context.Context, body string) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil
	}
	_, err := a.gl.CreateIssueNote(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, "🤖 "+body)
	if a.trackingGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("commenting on the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
	}
	return nil
}

// trackingGone reports whether err says the tracking issue no longer exists.
// It is then forgotten, and a new one is created on the next poll.
func (a *autopilot) trackingGone(err error) bool {
	if a.ap.TrackingIssueIID == 0 || !gitlab.IsNotFound(err) {
		return false
	}
	a.log.Info("autopilot tracking issue is gone, a new one is created", "issue", a.ap.TrackingIssueIID)
	a.ap.TrackingIssueIID, a.ap.TrackingHash = 0, ""
	return true
}

func (a *autopilot) save(ctx context.Context) error {
	a.stamp()
	if equality.Semantic.DeepEqual(a.st.Status.Autopilot, a.ap) {
		return nil
	}
	orig := a.st.DeepCopy()
	a.st.Status.Autopilot = a.ap
	return a.r.writeFailed(ctx, a.st, "autopilot.save", a.r.Status().Patch(ctx, a.st, client.MergeFrom(orig)))
}

// renderTracking renders the tracking issue's description.
func (a *autopilot) renderTracking() string {
	var b strings.Builder
	prefix := a.spec.Workflow.CommandPrefix
	fmt.Fprintf(&b, "**Autopilot** for milestone **%s**: ", a.st.Spec.Milestone.Title)
	switch a.ap.Phase {
	case aiv1.AutopilotPaused:
		next := fmt.Sprintf("Comment `%s resume` to continue", prefix)
		if a.ap.PauseKind == aiv1.PauseEmptyPlan {
			next = "Change the milestone description to plan it again"
		}
		fmt.Fprintf(&b, "⏸️ paused, %s. %s.\n", notify.Defuse(a.ap.PausedReason), next)
	case aiv1.AutopilotDone:
		b.WriteString("✅ done.\n")
	case aiv1.AutopilotOff:
		b.WriteString("⏹️ off (the marker was removed from the milestone).\n")
	default:
		switch rec := a.finalRecord(); {
		case len(a.st.Status.Plan) > 0 && rec != nil && !rec.State.Ended() && a.queueThrough():
			fmt.Fprintf(&b, "▶️ running, every issue is through; done once a release contains the last merge %s.\n", short(rec.SHA))
		case len(a.st.Status.Plan) > 0 && len(a.ap.CloseWaitsForCompanions) > 0:
			fmt.Fprintf(&b, "▶️ running, every issue is through; done once the companions %s have landed (merged or closed).\n",
				strings.Join(a.ap.CloseWaitsForCompanions, ", "))
		case a.holding:
			fmt.Fprintf(&b, "⏳ waiting for its turn: %s go first.\n", milestoneList(a.ahead))
		case len(a.st.Status.Plan) > 0:
			b.WriteString("▶️ running.\n")
		case a.waitingForTurn():
			// Not planned until the older milestones are through (decision 9).
			ahead := slices.Sorted(slices.Values(a.st.Spec.QueuedBehind))
			if len(ahead) == 0 {
				b.WriteString("⏳ waiting for its turn, planning starts on the next poll.\n")
				break
			}
			fmt.Fprintf(&b, "⏳ waiting for its turn: %s go first. Comment `%s move top` to plan it now (owners).\n", milestoneList(ahead), prefix)
		case a.st.RunPending():
			b.WriteString("📝 planning, the planning agent is writing the issues.\n")
		default:
			b.WriteString("⚠️ not planned, planning has not succeeded yet. Nothing starts until the milestone is planned.\n")
		}
	}
	if a.ap.MaxParallel > a.spec.MaxConcurrentRuns {
		fmt.Fprintf(&b, "⚠️ Up to %d issues may run at the same time, but the project allows %d agent runs at a time (`maxConcurrentRuns`): further runs queue.\n",
			a.ap.MaxParallel, a.spec.MaxConcurrentRuns)
	}
	var held []string
	for _, it := range a.ap.Items {
		if it.State == aiv1.AutopilotItemActive && it.Hold != nil {
			held = append(held, fmt.Sprintf("- #%d %s: %s (`%s resume #%d`)\n", it.IID, a.issues[it.IID].Title, notify.Defuse(it.Hold.Reason), prefix, it.IID))
		}
	}
	if len(held) > 0 {
		b.WriteString("\nHeld:\n" + strings.Join(held, ""))
	}
	limit := a.ap.MaxParallel // the effective limit, as of this poll
	parallel := limit > 1
	how := "one at a time, in dependency order. It merges each merge request when its pipeline is green and its review is clean, then starts the next issue."
	if parallel {
		how = fmt.Sprintf("up to %d at a time (`%s parallel <n>` in the milestone description changes it), in dependency order, and merges them "+
			"one after another, each merge request when its pipeline is green and its review is clean. An issue starts once the issues it depends on are merged.",
			limit, prefix)
	}
	b.WriteString("\nThe operator works through the planned issues " + how + " The agents take their defaults instead of " +
		"asking; every decision is listed below and can be changed with `" + prefix + " revisit <id> <what you want instead>` on its issue.\n\n")
	start := ""
	if h, what := a.startHint(); h != "" {
		start = fmt.Sprintf("`%s` (owners: %s), ", h, what)
	}
	fmt.Fprintf(&b, "Commands, as comments here: %s`%s pause`, `%s resume [#<issue>]`, `%s skip #<issue>`, `%s parallel <n>` (owners).\n\n### Issues\n\n", start, prefix, prefix, prefix, prefix)
	if len(a.st.Status.Plan) == 0 && len(a.ap.Items) == 0 {
		b.WriteString("_Listed once the milestone is planned._\n")
	}
	for _, it := range a.ap.Items {
		title := a.issues[it.IID].Title
		mr := ""
		if it.MergeRequestIID != 0 {
			mr = fmt.Sprintf(" !%d", it.MergeRequestIID)
		}
		switch it.State {
		case aiv1.AutopilotItemMerged:
			fmt.Fprintf(&b, "- [x] #%d %s: merged%s (`%s`)\n", it.IID, title, mr, short(it.MergedSHA))
		case aiv1.AutopilotItemSkipped:
			fmt.Fprintf(&b, "- [ ] ~~#%d %s~~: skipped\n", it.IID, title)
		case aiv1.AutopilotItemActive:
			lane := "**in progress**"
			switch {
			case !parallel:
			case it.Hold != nil:
				lane = fmt.Sprintf("**held** (%s)", it.Hold.Kind)
			case a.ap.Landing == it.IID:
				lane = "**landing**"
			case it.ReadyAt != nil:
				lane = "ready to land"
			}
			fmt.Fprintf(&b, "- [ ] #%d %s: %s%s (%s)\n", it.IID, title, lane, mr, a.labelOf(it.IID))
		default:
			waits, met := "", true
			if deps := a.deps(it.IID); len(deps) > 0 {
				waits = ", after " + issueList(deps)
				for _, d := range deps {
					if dep := a.item(d); dep != nil && dep.State != aiv1.AutopilotItemMerged {
						met = false
					}
				}
			}
			if h := a.holds[it.IID]; h != nil {
				waits += ", " + h.waits()
			} else if parallel && met && it.State == aiv1.AutopilotItemPending && a.running() >= int(limit) {
				waits += ", waits for a free slot"
			}
			fmt.Fprintf(&b, "- [ ] #%d %s: pending%s\n", it.IID, title, waits)
		}
	}
	var decisions []string
	for _, it := range a.ap.Items {
		for _, d := range it.Decisions {
			line := fmt.Sprintf("[%s](%s) %s (#%d)", d.ID, d.URL, d.Title, it.IID)
			if d.Superseded {
				line = "~~" + line + "~~ superseded"
			}
			decisions = append(decisions, "- "+line)
		}
	}
	b.WriteString(rulesSection(a.st))
	b.WriteString("\n### Decisions\n\n")
	if a.r != nil {
		if u := desklink.Milestone(a.r.DeskURL, a.st.Namespace, a.st.Spec.ProjectRef, a.st.Spec.Milestone.IID); u != "" {
			fmt.Fprintf(&b, "[All decisions on the Desk](%s?panel=decisions)\n\n", u)
		}
	}
	if len(decisions) == 0 {
		b.WriteString("_Listed here once an issue is merged; until then they are on the issue._\n")
	} else {
		b.WriteString(strings.Join(decisions, "\n") + "\n")
	}
	b.WriteString("\n" + marker.TrackingIssue + "\n")
	return b.String()
}

func (a *autopilot) labelOf(iid int64) string {
	wf := a.spec.Workflow
	is := a.issues[iid]
	for _, l := range []string{wf.ReadyLabel, wf.WorkingLabel, wf.NeedsInputLabel, wf.ReviewLabel} {
		if is.HasLabel(l) {
			return l
		}
	}
	return "queued"
}

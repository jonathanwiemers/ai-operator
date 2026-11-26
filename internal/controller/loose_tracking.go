package controller

import (
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/timefmt"
)

// renderLoose renders the loose issues queue's tracking issue: what the
// queue is, then its items by state. It shows issues as #N only, so no
// untrusted title reaches the description, and depends on nothing but the
// status, the tasks and the labels: its hash decides the update.
func (a *autopilot) renderLoose() string {
	var b strings.Builder
	prefix := a.spec.Workflow.CommandPrefix
	b.WriteString("**Loose issues queue**: ")
	switch a.ap.Phase {
	case aiv1.AutopilotOff:
		b.WriteString("⏹️ off (`looseIssues.enabled` is false).\n")
	case aiv1.AutopilotPaused:
		fmt.Fprintf(&b, "⏸️ paused, %s.\n", notify.Defuse(a.ap.PausedReason))
	default:
		b.WriteString("▶️ running.\n")
	}
	if a.ap.FrozenAt != nil && a.ap.Phase != aiv1.AutopilotOff {
		var issue int64
		if a.project != nil && a.project.Status.MainHealth != nil {
			issue = a.project.Status.MainHealth.IssueIID
		}
		b.WriteString("\n🧊 " + frozenLine(issue) + "\n")
	}
	if line := roundLine(a.spec.LooseIssues.Pilot, a.ap.Round); line != "" && a.ap.Phase != aiv1.AutopilotOff {
		b.WriteString("\n🧭 " + line + "\n")
	}
	owners := "none"
	if names := notify.Usernames(a.st.Spec.Owners); len(names) > 0 {
		owners = "`" + strings.Join(names, "`, `") + "`"
	}
	fmt.Fprintf(&b, "\nThe operator works through the open issues without a milestone that an owner takes, up to %d at a time, "+
		"first taken, first started. It merges each merge request when its pipeline is green and its review is clean. "+
		"A problem holds only its own issue.\n\nOwners: %s. Take an issue with `%s take #<issue>` as a comment here (owners).\n",
		a.ap.MaxParallel, owners, prefix)

	var pending, active, held []string
	for _, it := range a.ap.Items {
		switch {
		case it.State == aiv1.AutopilotItemPending:
			pending = append(pending, fmt.Sprintf("%d. #%d\n", len(pending)+1, it.IID))
		case it.State == aiv1.AutopilotItemActive && it.Hold != nil:
			held = append(held, fmt.Sprintf("- #%d: %s (`%s resume #%d`)\n", it.IID, notify.Defuse(it.Hold.Reason), prefix, it.IID))
		case it.State == aiv1.AutopilotItemActive:
			active = append(active, fmt.Sprintf("- #%d%s: %s\n", it.IID, mrRef(it), a.stageOf(it.IID)))
		}
	}
	var merged, left []string
	for _, i := range newestFinished(a.ap.Items, maxLooseFinished) {
		it := a.ap.Items[i]
		if it.State == aiv1.AutopilotItemMerged {
			merged = append(merged, fmt.Sprintf("- [x] #%d: merged%s\n", it.IID, mrRef(it)))
		} else {
			left = append(left, fmt.Sprintf("- #%d: %s\n", it.IID, notify.Defuse(it.LeftReason)))
		}
	}
	if len(a.ap.Items) == 0 {
		b.WriteString("\n_No issues in the queue yet._\n")
	}
	for _, s := range []struct {
		title string
		lines []string
	}{{"Pending", pending}, {"Active", active}, {"Held", held}, {"Merged", merged}, {"Left", left}} {
		if len(s.lines) > 0 {
			b.WriteString("\n### " + s.title + "\n\n" + strings.Join(s.lines, ""))
		}
	}
	b.WriteString("\n" + marker.TrackingIssue + "\n")
	return b.String()
}

// roundLine is the line of the pilot's round rd under the limits of p, ""
// without a round: "Round 3 · started 2026-11-26 09:12 UTC · 2 of 5 issues ·
// $1.40". GitLab knows no viewer's time zone, so it is UTC.
func roundLine(p aiv1.LooseIssuesPilotSpec, rd *aiv1.LooseRound) string {
	if rd == nil {
		return ""
	}
	issues := fmt.Sprintf("%d of %d issues", rd.Finished, p.Issues())
	if p.Issues() == 0 {
		issues = fmt.Sprintf("%d issues, no issue limit", rd.Finished)
	}
	parts := []string{fmt.Sprintf("Round %d", rd.Number), "started " + timefmt.UTCMinute(rd.StartedAt.Time), issues}
	if p.Duration() == 0 {
		parts = append(parts, "no time limit")
	}
	return strings.Join(append(parts, "$"+aiv1.ShowCost(aiv1.CostOf(rd.CostUSD))), " · ")
}

// mrRef is " !<iid>" for an item with a merge request, else "".
func mrRef(it aiv1.AutopilotItem) string {
	if it.MergeRequestIID == 0 {
		return ""
	}
	return fmt.Sprintf(" !%d", it.MergeRequestIID)
}

// stageOf is an active queue item's stage: its agent label, or, when the
// poll did not list its issue, its implement task's phase.
func (a *autopilot) stageOf(iid int64) string {
	if _, ok := a.issues[iid]; !ok {
		if t := a.tasks[iid]; t != nil && t.Status.Phase != "" {
			return strings.ToLower(string(t.Status.Phase))
		}
	}
	return a.labelOf(iid)
}

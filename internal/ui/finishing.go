package ui

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// FinishingEntry is a done autopilot milestone still open on GitLab and what
// holds it, as closeMilestone sees it; followUps: see the Retrospective tab.
type FinishingEntry struct {
	IID          int64            `json:"iid"`
	Title        string           `json:"title"`
	Task         string           `json:"task"`
	Replanning   bool             `json:"replanning"`
	RetroWriting bool             `json:"retroWriting"`
	FollowUps    bool             `json:"followUps"`
	OpenIssues   []FinishingIssue `json:"openIssues"`
	Companions   []string         `json:"companions"`
}

// FinishingIssue is an open issue a done milestone waits for.
type FinishingIssue struct {
	IID    int64  `json:"iid"`
	Title  string `json:"title,omitempty"`
	WebURL string `json:"webURL"`
}

// finishing lists the done autopilot milestones whose spec task still
// exists, oldest first; web: the project's GitLab URL.
func finishing(tasks []aiv1.AgentTask, web string, now time.Time) []FinishingEntry {
	out := []FinishingEntry{}
	for i := range tasks {
		st := &tasks[i]
		if st.Spec.Type != aiv1.TaskTypeSpec || !st.Spec.Autopilot || st.Spec.Milestone == nil || st.Status.Autopilot == nil || st.Status.Autopilot.Phase != aiv1.AutopilotDone {
			continue
		}
		ap, iid, retro := st.Status.Autopilot, st.Spec.Milestone.IID, cmp.Or(st.Status.Pilot, &aiv1.PilotStatus{}).Retrospective
		_, coord := milestoneTasks(tasks, iid)
		e := FinishingEntry{IID: iid, Title: st.Spec.Milestone.Title, Task: st.Name, Replanning: st.RunPending(),
			RetroWriting: retro == nil && coord != nil, FollowUps: retro != nil && !retro.FollowUpsSettled(now),
			OpenIssues: []FinishingIssue{}, Companions: append([]string{}, ap.CloseWaitsForCompanions...)}
		for _, n := range ap.CloseWaitsFor {
			is := FinishingIssue{IID: n, WebURL: fmt.Sprintf("%s/-/issues/%d", web, n)}
			for _, t := range tasks {
				if t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Issue != nil && t.Spec.Issue.IID == n {
					is.Title, is.WebURL = t.Spec.Issue.Title, cmp.Or(t.Spec.Issue.WebURL, is.WebURL)
				}
			}
			e.OpenIssues = append(e.OpenIssues, is)
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b FinishingEntry) int { return cmp.Compare(a.IID, b.IID) })
	return out
}

package ui

import (
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// CompanionChip is what the Desk shows of a companion merge request
// (docs/specs/cross-repo-changes.spec.md, decision 12). Closed and blocked
// companions keep the closest chip and say why in the view's reason.
type CompanionChip string

const (
	// CompanionChipDraft: the home merge request is not merged yet.
	CompanionChipDraft CompanionChip = "draft"
	// CompanionChipWaitingRelease: the home change is merged; the companion
	// waits for its release, a rebase or its pipeline.
	CompanionChipWaitingRelease CompanionChip = "waiting for release"
	// CompanionChipReady: it can be merged (by autopilot or the owners).
	CompanionChipReady CompanionChip = "ready"
	// CompanionChipMerging: autopilot merges it and follows the pipeline.
	CompanionChipMerging CompanionChip = "merging"
	// CompanionChipMerged: it was merged.
	CompanionChipMerged CompanionChip = "merged"
)

// CompanionView is the companion merge request of an issue: its state
// before the home merge (from the task's run, State empty) or while it
// lands after it (the project's status.companions entry).
type CompanionView struct {
	// HomeIssueIID is the issue the companion belongs to.
	HomeIssueIID int64  `json:"homeIssueIID"`
	Project      string `json:"project"`
	MRIID        int64  `json:"mrIID,omitempty"`
	MRURL        string `json:"mrURL,omitempty"`
	// State is the landing state, empty before the home merge.
	State aiv1.LandingState `json:"state,omitempty"`
	Chip  CompanionChip     `json:"chip"`
	// Reason says why it waits, is blocked or was closed (operator text).
	Reason string `json:"reason,omitempty"`
	// Release is the release that made the home change live.
	Release string       `json:"release,omitempty"`
	ReadyAt *metav1.Time `json:"readyAt,omitempty"`
	// AutoMerge: autopilot may merge it once it is ready.
	AutoMerge bool `json:"autoMerge,omitempty"`
}

// companionChip maps a landing entry to its chip, nil (before the home
// merge) to draft. Blocked and closed take the chip of the step they
// stopped at: merging once autopilot's merge started, ready once the
// companion was ready, else waiting for release.
func companionChip(c *aiv1.CompanionStatus) CompanionChip {
	if c == nil {
		return CompanionChipDraft
	}
	switch c.State {
	case aiv1.LandingReady:
		return CompanionChipReady
	case aiv1.LandingMerging:
		return CompanionChipMerging
	case aiv1.LandingMerged:
		return CompanionChipMerged
	case aiv1.LandingClosed, aiv1.LandingBlocked:
		switch {
		case c.MergeStartedAt != nil:
			return CompanionChipMerging
		case c.ReadyAt != nil:
			return CompanionChipReady
		}
	}
	return CompanionChipWaitingRelease
}

// companionEntryView is the view of a landing entry.
func companionEntryView(c *aiv1.CompanionStatus) CompanionView {
	v := CompanionView{HomeIssueIID: c.HomeIssueIID, Project: c.Project, MRIID: c.MRIID, MRURL: c.MRURL, State: c.State,
		Chip: companionChip(c), Reason: c.Reason, Release: c.Release, ReadyAt: c.ReadyAt, AutoMerge: c.AutoMerge}
	if v.Reason == "" {
		switch c.State {
		case aiv1.LandingClosed:
			v.Reason = "closed without a merge"
		case aiv1.LandingBlocked:
			v.Reason = "blocked: a human must act"
		}
	}
	return v
}

// sameCompanion reports whether the landing entry is the merge request of
// run (same project and merge request).
func sameCompanion(c *aiv1.CompanionStatus, run *aiv1.CompanionRun) bool {
	return aiv1.NormalizeProject(c.Project) == aiv1.NormalizeProject(run.Project) && c.MRIID == run.MRIID
}

// taskCompanion is the companion of t: the project's landing entry for the
// task's companion merge request (or, without one in the run, the newest
// entry of the issue), else the run's draft. nil without a companion.
func taskCompanion(t *aiv1.AgentTask, p *aiv1.AgentProject) *CompanionView {
	if t.Spec.Type != aiv1.TaskTypeImplement || t.Spec.Issue == nil {
		return nil
	}
	run := t.Status.Run.Companion
	var entry *aiv1.CompanionStatus
	if p != nil {
		for i := range p.Status.Companions {
			c := &p.Status.Companions[i]
			if c.HomeIssueIID != t.Spec.Issue.IID {
				continue
			}
			if run != nil && sameCompanion(c, run) {
				entry = c
				break
			}
			if run == nil {
				entry = c // the newest entry of the issue wins
			}
		}
	}
	if entry != nil {
		v := companionEntryView(entry)
		if v.MRURL == "" && run != nil {
			v.MRURL = run.MRURL
		}
		return &v
	}
	if run == nil {
		return nil
	}
	return &CompanionView{HomeIssueIID: t.Spec.Issue.IID, Project: run.Project, MRIID: run.MRIID, MRURL: run.MRURL,
		Chip: CompanionChipDraft, AutoMerge: t.Spec.Companion != nil && t.Spec.Companion.AutoMerge}
}

// milestoneCompanions lists the companions of the milestone with the IID:
// those of its implement tasks and the landing entries of the milestone
// whose task is gone, by home issue.
func milestoneCompanions(p *aiv1.AgentProject, tasks []aiv1.AgentTask, iid int64) []CompanionView {
	out := []CompanionView{}
	seen := map[string]bool{}
	key := func(v CompanionView) string {
		return fmt.Sprintf("%d/%s!%d", v.HomeIssueIID, aiv1.NormalizeProject(v.Project), v.MRIID)
	}
	issues := map[int64]bool{}
	for i := range tasks {
		t := &tasks[i]
		if t.Spec.Milestone == nil || t.Spec.Milestone.IID != iid || t.Spec.Type != aiv1.TaskTypeImplement || t.Spec.Issue == nil {
			continue
		}
		issues[t.Spec.Issue.IID] = true
		if v := taskCompanion(t, p); v != nil && !seen[key(*v)] {
			seen[key(*v)] = true
			out = append(out, *v)
		}
	}
	for i := range p.Status.Companions {
		c := &p.Status.Companions[i]
		if c.MilestoneIID != iid || issues[c.HomeIssueIID] {
			continue
		}
		if v := companionEntryView(c); !seen[key(v)] {
			seen[key(v)] = true
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].HomeIssueIID < out[j].HomeIssueIID })
	return out
}

// companionRef names a companion merge request: "group/infra!12".
func companionRef(project string, mrIID int64) string {
	if mrIID == 0 {
		return project
	}
	return fmt.Sprintf("%s!%d", project, mrIID)
}

// issueCompanions maps the issues of p that have a companion repository to
// its project: the landing entry's, else the run's, else the task's
// spec.companion (the issue opted in, its calls edit both trees).
func issueCompanions(p *aiv1.AgentProject, tasks []aiv1.AgentTask) map[int64]string {
	out := map[int64]string{}
	for i := range tasks {
		t := &tasks[i]
		if t.Spec.Type != aiv1.TaskTypeImplement || t.Spec.Issue == nil {
			continue
		}
		switch {
		case t.Status.Run.Companion != nil && t.Status.Run.Companion.Project != "":
			out[t.Spec.Issue.IID] = t.Status.Run.Companion.Project
		case t.Spec.Companion != nil && t.Spec.Companion.Project != "":
			out[t.Spec.Issue.IID] = t.Spec.Companion.Project
		}
	}
	if p != nil {
		for _, c := range p.Status.Companions {
			if c.Project != "" {
				out[c.HomeIssueIID] = c.Project
			}
		}
	}
	return out
}

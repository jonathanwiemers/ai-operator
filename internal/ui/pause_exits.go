package ui

import (
	"fmt"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// ExitStatus is the live state of an automatic exit of a pause.
type ExitStatus string

const (
	// ExitStatusMet: the condition holds now; the operator lifts the pause on its
	// next poll.
	ExitStatusMet ExitStatus = "met"
	// ExitStatusInProgress: something that may meet it is under way (a run
	// pending, a rebase in flight).
	ExitStatusInProgress ExitStatus = "inProgress"
	// ExitStatusWaiting: the condition does not hold yet.
	ExitStatusWaiting ExitStatus = "waiting"
	// ExitStatusUnknown: the view cannot tell from what it has.
	ExitStatusUnknown ExitStatus = "unknown"
)

// ExitView is one way out of the pause (aiv1.PauseExits) with its live
// state: an automatic exit has a Status and maybe a Detail ("main is red at
// abc1234"); a remedy is Allowed when the viewer may send it as a button
// (merge with the merge grant, the other Desk commands with chat). Labels,
// GitLab actions, prompts and commands the Desk cannot send are never
// buttons. Detail is plain text.
type ExitView struct {
	aiv1.PauseExit `tstype:",extends"`
	Status         ExitStatus `json:"status,omitempty"`
	Detail         string     `json:"detail,omitempty"`
	Allowed        bool       `json:"allowed,omitempty"`
}

// deskCommands are the remedy commands the Desk sends (chatcmd), with
// whether they need the merge grant rather than chat.
var deskCommands = map[string]bool{"resume": false, "skip": false, "retry": false, "review": false, "merge": true}

// exitViews lists the ways out of ap's pause with their live state, nil
// while it is not paused.
func exitViews(ap *aiv1.AutopilotStatus, tasks []aiv1.AgentTask, project *aiv1.AgentProject, perms MilestonePermissions) []ExitView {
	if ap == nil || ap.Phase != aiv1.AutopilotPaused {
		return nil
	}
	exits := aiv1.PauseExits(ap.PauseKind)
	views := make([]ExitView, 0, len(exits))
	for _, e := range exits {
		v := ExitView{PauseExit: e}
		switch {
		case e.Auto:
			v.Status, v.Detail = exitStatus(e.ID, ap, tasks, project)
		case e.Command != "" && e.Label == "" && !e.GitLab && !e.Prompt:
			if merge, ok := deskCommands[e.Command]; ok {
				v.Allowed = perms.Chat && (!merge || perms.Merge)
			}
		}
		views = append(views, v)
	}
	return views
}

// exitStatus is the live state of the automatic exit id.
func exitStatus(id string, ap *aiv1.AutopilotStatus, tasks []aiv1.AgentTask, project *aiv1.AgentProject) (ExitStatus, string) {
	var item *aiv1.AutopilotItem
	for i := range ap.Items {
		if ap.PausedFor != 0 && ap.Items[i].IID == ap.PausedFor {
			item = &ap.Items[i]
		}
	}
	var mh *aiv1.MainHealthStatus
	if project != nil {
		mh = project.Status.MainHealth
	}
	switch id {
	case "laterRunPasses":
		if ap.PausedFor == 0 {
			return ExitStatusUnknown, ""
		}
		if t := issueTask(tasks, ap.PausedFor); t != nil && t.RunPending() {
			return ExitStatusInProgress, fmt.Sprintf("run of #%d pending", ap.PausedFor)
		}
		return ExitStatusWaiting, ""
	case "mainGreen":
		if mh == nil || mh.State == "" {
			return ExitStatusUnknown, "the default branch's health is not known"
		}
		if mh.State == aiv1.MainHealthGreen {
			return ExitStatusMet, "main is green" + at(mh.SHA)
		}
		return ExitStatusWaiting, fmt.Sprintf("main is %s%s", mh.State, at(mh.SHA))
	case "mainGreenRebase":
		return mainGreenRebase(item, issueTask(tasks, ap.PausedFor), mh)
	case "budgetRaised":
		return budgetRaised(ap, item)
	// replanned: a pending re-plan lifts the pause on the next poll, so
	// while paused nothing is under way.
	case "somethingCanStart", "replanned":
		return ExitStatusWaiting, ""
	}
	return ExitStatusUnknown, ""
}

// mainGreenRebase: the active MR's CI fails with the default branch
// (BaseFailing), and the operator rebases it once the branch is green at a
// commit it has not rebased onto yet (healBaseFailing).
func mainGreenRebase(item *aiv1.AutopilotItem, task *aiv1.AgentTask, mh *aiv1.MainHealthStatus) (ExitStatus, string) {
	switch {
	case item == nil:
		return ExitStatusUnknown, ""
	case item.RebasedFrom != "":
		return ExitStatusInProgress, fmt.Sprintf("rebase of !%d in flight", item.MergeRequestIID)
	case mh == nil || mh.State == "":
		return ExitStatusUnknown, "the default branch's health is not known"
	case mh.State != aiv1.MainHealthGreen:
		return ExitStatusWaiting, fmt.Sprintf("main is %s%s", mh.State, at(mh.SHA))
	case mh.SHA != "" && mh.SHA == item.HealedOn:
		return ExitStatusInProgress, fmt.Sprintf("rebased onto %s, the new pipeline decides", short(mh.SHA))
	case task != nil && (task.Status.CI == nil || task.Status.CI.State != aiv1.CIStateBaseFailing):
		return ExitStatusWaiting, "the failing jobs pass on main"
	}
	return ExitStatusMet, "main is green" + at(mh.SHA)
}

// budgetRaised: the milestone's budget and the paused issue's own are both
// above their spend (or gone).
func budgetRaised(ap *aiv1.AutopilotStatus, item *aiv1.AutopilotItem) (ExitStatus, string) {
	budgets := []struct {
		of string
		b  *aiv1.BudgetStatus
	}{{"the milestone", ap.Budget}}
	if item != nil {
		budgets = append(budgets, struct {
			of string
			b  *aiv1.BudgetStatus
		}{fmt.Sprintf("#%d", item.IID), item.Budget})
	}
	for _, x := range budgets {
		if x.b == nil {
			continue
		}
		if x.b.SpentUSD == "" {
			return ExitStatusUnknown, "the spend of " + x.of + " cannot be read"
		}
		if aiv1.BudgetLeft(x.b.LimitUSD, x.b.SpentUSD, 0) <= 0 {
			return ExitStatusWaiting, fmt.Sprintf("%s spent %s of %s USD", x.of, x.b.SpentUSD, x.b.LimitUSD)
		}
	}
	return ExitStatusMet, ""
}

// issueTask is the implement task of the issue, nil without one.
func issueTask(tasks []aiv1.AgentTask, iid int64) *aiv1.AgentTask {
	for i := range tasks {
		if t := &tasks[i]; iid != 0 && t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Issue != nil && t.Spec.Issue.IID == iid {
			return t
		}
	}
	return nil
}

// at is " at <short sha>", "" without one.
func at(sha string) string {
	if sha == "" {
		return ""
	}
	return " at " + short(sha)
}

// short is a commit's short SHA.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

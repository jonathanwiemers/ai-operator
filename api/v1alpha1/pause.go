package v1alpha1

import "slices"

// PauseExit is one way out of an autopilot pause: an automatic exit (a
// condition the operator lifts the pause on by itself) or an owner's remedy
// (a command, a label or an action in GitLab, and what it does). The table
// (PauseExits) is the one source of the operator's pause comment, the
// pilot's state and the Desk's pause card. It holds no project setup:
// callers resolve the command prefix and the label names (WorkflowSpec).
type PauseExit struct {
	// ID is stable and camelCase, e.g. laterRunPasses or skip.
	ID string `json:"id"`
	// Auto: the operator lifts the pause by itself once When holds.
	Auto bool `json:"auto,omitempty"`
	// When is the condition of an automatic exit, or what a remedy needs
	// first (for a GitLab action: the action).
	When string `json:"when,omitempty"`
	// Command is the /agent word of a remedy (resume, skip, unskip,
	// startAnyway, merge, rebase, mergeBase, retry, review); after a label or
	// a GitLab action it is the command that follows, on the tracking issue.
	Command string `json:"command,omitempty"`
	// Target is where a remedy's command or label goes: spec (the tracking
	// issue, the milestone) or implement (the paused issue).
	Target ExitTarget `json:"target,omitempty"`
	// Label is the workflow label role a remedy sets (done, ready),
	// resolved with WorkflowSpec.RoleLabel.
	Label LabelRole `json:"label,omitempty"`
	// GitLab: the remedy is an action in GitLab (When says which), not a
	// command or a label.
	GitLab bool `json:"gitlab,omitempty"`
	// Prompt: the remedy is an owner's answer to the pilot's prompt, whose
	// tools When names (the owner may ask the pilot for one with /pilot).
	Prompt bool `json:"prompt,omitempty"`
	// Does says what happens then.
	Does string `json:"does"`
}

// ExitTarget is where a remedy goes.
type ExitTarget string

const (
	// ExitTargetSpec: the tracking issue (or the milestone).
	ExitTargetSpec ExitTarget = "spec"
	// ExitTargetImplement: the paused issue.
	ExitTargetImplement ExitTarget = "implement"
)

// LabelRole is a workflow label by its role.
type LabelRole string

const (
	LabelRoleDone  LabelRole = "done"
	LabelRoleReady LabelRole = "ready"
)

// RoleLabel is the configured name of a workflow label role, "" for an
// unknown role.
func (w WorkflowSpec) RoleLabel(role LabelRole) string {
	switch role {
	case LabelRoleDone:
		return w.DoneLabel
	case LabelRoleReady:
		return w.ReadyLabel
	}
	return ""
}

// PauseKinds lists every PauseKind (the enum of its marker; a test
// compares them).
var PauseKinds = []PauseKind{
	PauseOwner, PauseCycle, PauseStuck, PauseNeedsInput, PauseRunFailed, PauseFindings, PauseReviewErrors,
	PauseNotReviewed, PauseCIRed, PauseCICanceled, PauseMergeRequest, PauseMainRed, PauseConfig, PauseBudget, PauseCompanion,
	PauseEmptyPlan,
}

// The exits several kinds share.
var (
	exitLaterRunPasses = PauseExit{ID: "laterRunPasses", Auto: true, When: "a later run makes the issue pass the merge gate (reviewed and green)",
		Does: "autopilot continues with the merge"}
	exitMergedByHand = PauseExit{ID: "mergedByHand", Auto: true, When: "the issue's merge request is merged by hand",
		Does: "autopilot counts the issue as merged and goes on with the next one"}
	exitClosed = PauseExit{ID: "closed", Auto: true, When: "the issue is closed",
		Does: "autopilot counts it as merged if it was done by hand (a merged MR, or the done label), else as skipped, and goes on"}
	exitRetry = PauseExit{ID: "retry", Command: "retry", Target: ExitTargetImplement,
		Does: "the agent runs again on the issue; a run that passes the gate ends the pause"}
	exitSkip = PauseExit{ID: "skip", Command: "skip", Target: ExitTargetSpec,
		Does: "the issue is skipped and autopilot goes on; issues that depend on it wait until it is done by hand"}
	exitReview = PauseExit{ID: "review", Command: "review", Target: ExitTargetImplement,
		Does: "the reviewers check the pushed head again"}
	exitMergeAnyway = PauseExit{ID: "mergeAnyway", Command: "merge", Target: ExitTargetSpec,
		Does: "owners only: autopilot merges this head without a full review (nor of its companion); CI, the merge request's state and approvals still apply, also the companion's"}
	exitResume = PauseExit{ID: "resume", Command: "resume", Target: ExitTargetSpec,
		Does: "autopilot continues"}
	exitRebase = PauseExit{ID: "rebase", Command: "rebase", Target: ExitTargetSpec,
		Does: "GitLab rebases the issue's merge request onto the default branch and the pause ends; the review holds if the change is unchanged, the new pipeline decides"}
	exitMergeBase = PauseExit{ID: "mergeBase", Command: "mergeBase", Target: ExitTargetSpec,
		Does: "the agent merges the default branch into the issue's merge request and the pause ends; the new head is reviewed and checked by CI like any push"}
)

// pauseRunExits are the exits of a pause over the active issue's run, review
// or CI (ResumeWhenReady), plus the kind's own remedies.
func pauseRunExits(remedies ...PauseExit) []PauseExit {
	return append([]PauseExit{exitLaterRunPasses, exitMergedByHand, exitClosed, exitRetry, exitSkip}, remedies...)
}

var pauseExits = map[PauseKind][]PauseExit{
	PauseOwner: {exitResume},
	PauseCycle: {
		{ID: "changePlan", GitLab: true, When: "change the milestone description so the issues no longer depend on each other in a cycle", Command: "resume",
			Does: "the milestone is planned again and autopilot continues with the new plan"},
		withDoes(exitSkip, "the issue is skipped, which breaks the cycle of the issues after it"),
		withDoes(exitResume, "autopilot checks the plan again"),
	},
	PauseStuck: {
		{ID: "somethingCanStart", Auto: true, When: "an issue can start (a skipped dependency closed as done, a link removed, an unskip or a startAnyway)",
			Does: "autopilot continues with it"},
		{ID: "closeAsDone", Label: LabelRoleDone, Target: ExitTargetImplement, When: "and close it (or merge it by hand)",
			Does: "the skipped issue counts as merged, so the issues that wait for it can start"},
		{ID: "removeLink", GitLab: true, When: `remove the "blocked by" link of the waiting issue`,
			Does: "the issue no longer waits for the linked one"},
		{ID: "unskip", Command: "unskip", Target: ExitTargetSpec,
			Does: "the skipped issue goes back to pending and autopilot picks it once it can start"},
		{ID: "startAnyway", Command: "startAnyway", Target: ExitTargetSpec,
			Does: "the waiting issue may start although an issue it depends on was skipped; the tracking issue says so"},
		{ID: "pilotPrompt", Prompt: true, When: "with unskip, markDone (the skipped issue is closed as done, with a reason) or startAnyway, e.g. when another milestone replaces the skipped issue",
			Does: "the answer runs the tool, and autopilot continues once an issue can start"},
		withDoes(exitSkip, "the waiting issue is skipped and autopilot goes on with the others"),
		withDoes(exitResume, "autopilot looks for an issue that can start again"),
	},
	PauseNeedsInput: pauseRunExits(
		PauseExit{ID: "answered", Label: LabelRoleReady, Target: ExitTargetImplement, When: "after answering its questions",
			Does: "a new run starts with the answers"}),
	PauseRunFailed:    pauseRunExits(),
	PauseFindings:     pauseRunExits(exitMergeAnyway),
	PauseReviewErrors: pauseRunExits(exitReview, exitMergeAnyway),
	PauseNotReviewed:  pauseRunExits(exitReview, exitMergeAnyway),
	PauseCIRed: append([]PauseExit{
		{ID: "mainGreenRebase", Auto: true, When: "the failing jobs also fail on the default branch, and it is green again at a commit the MR is not based on",
			Does: "autopilot rebases the merge request once, and the new pipeline decides"},
	}, pauseRunExits(exitRebase, exitMergeBase)...),
	PauseCICanceled: pauseRunExits(),
	PauseMergeRequest: {
		exitMergedByHand,
		exitClosed,
		{ID: "fixInGitLab", GitLab: true, When: "fix the merge request (approve it, resolve its discussions or conflicts, mark it ready, let the bot merge)", Command: "resume",
			Does: "the merge gate checks it again"},
		exitRebase,
		exitMergeBase,
		exitSkip,
	},
	PauseMainRed: {
		{ID: "mainGreen", Auto: true, When: "the default branch turns green", Does: "the issue's block is lifted and the merge gate runs again"},
		{ID: "freezeOff", Auto: true, When: "the merge freeze is switched off (`mainHealth.freezeAutopilot`)", Does: "the issue's block is lifted and the merge gate runs again"},
		{ID: "healthStale", Auto: true, When: "the default branch's health is no longer watched or checked", Does: "the issue's block is lifted and the merge gate runs again"},
		exitMergedByHand,
		exitClosed,
	},
	PauseConfig: {
		exitMergedByHand,
		exitClosed,
		{ID: "resume", Command: "resume", Target: ExitTargetSpec, When: "after fixing the setup (the issue's workflow label, `agents.review.reviewers`, a pushed merge request)",
			Does: "the merge gate checks the issue again"},
		exitSkip,
	},
	PauseBudget: {
		{ID: "budgetRaised", Auto: true, When: "the budget is raised above the spend or removed (`budget <usd>` in the milestone description, or on the issue for its own budget)",
			Does: "autopilot continues"},
	},
	// The companion's merge request, before the home merge (the merge gate)
	// and while it lands after it.
	PauseCompanion: {
		{ID: "companionMergeable", Auto: true, When: "the companion merge request can be merged again, or was rebased by hand",
			Does: "the merge gate, or the companion's landing, checks it again and autopilot continues"},
		{ID: "companionByHand", Auto: true, When: "the companion merge request is merged by hand (or closed by hand once the home change is merged)",
			Does: "it is recorded and autopilot goes on"},
		{ID: "fixCompanion", GitLab: true, When: "fix the companion merge request (resolve its conflicts or discussions, approve it)", Command: "resume",
			Does: "the merge gate, or the companion's landing, checks it again"},
		withDoes(exitResume, "the merge gate, or the companion's landing, checks the companion again"),
		withDoes(exitSkip, "the issue is skipped and autopilot goes on without it; its companion merge request is left to the owners"),
	},
	// A planning run that succeeded with no issues. A re-plan ends it; an
	// empty one pauses again.
	PauseEmptyPlan: {
		{ID: "replanned", Auto: true, When: "the milestone is planned again and the plan has issues",
			Does: "autopilot starts with the new plan"},
		{ID: "changeSpec", GitLab: true, When: "change the milestone description so the planning agent plans issues",
			Does: "the milestone is planned again"},
	},
}

func withDoes(e PauseExit, does string) PauseExit {
	e.Does = does
	return e
}

// PauseExits returns the ways out of a pause of kind, automatic exits first.
// The slice is a copy.
func PauseExits(kind PauseKind) []PauseExit {
	return slices.Clone(pauseExits[kind])
}

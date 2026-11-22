package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PlannedIssue is one issue of a milestone plan.
type PlannedIssue struct {
	// Key is the planner's stable identifier of the issue.
	Key   string `json:"key"`
	IID   int64  `json:"iid"`
	Title string `json:"title,omitempty"`
	// DependsOn are the IIDs of planned issues that must be merged first.
	// +optional
	DependsOn []int64 `json:"dependsOn,omitempty"`
	// Linked are the dependencies the operator linked as GitLab issue
	// links; only these are unlinked when the plan drops them.
	// +optional
	Linked []int64 `json:"linked,omitempty"`
	// Release is the planner's decision to release the default branch after
	// this issue is merged.
	// +optional
	Release bool `json:"release,omitempty"`
	// ReleaseWhy is the planner's one-line reason for Release.
	// +optional
	ReleaseWhy string `json:"releaseWhy,omitempty"`
}

// AutopilotPhase is the state of an autopilot milestone.
// +kubebuilder:validation:Enum=Running;Paused;Done;Off
type AutopilotPhase string

const (
	// AutopilotRunning: the operator works through the queue.
	AutopilotRunning AutopilotPhase = "Running"
	// AutopilotPaused: something needs a human; "/agent resume" continues.
	AutopilotPaused AutopilotPhase = "Paused"
	// AutopilotDone: every planned issue is merged or skipped.
	AutopilotDone AutopilotPhase = "Done"
	// AutopilotOff: the marker was removed from the milestone.
	AutopilotOff AutopilotPhase = "Off"
)

// MergeOverride lets the merge gate skip its review checks for one head:
// failed or missing reviews and blocking findings. CI, the MR state and
// approvals still apply.
type MergeOverride struct {
	IssueIID int64 `json:"issueIID"`
	// SHA is the head it was given for.
	SHA string `json:"sha"`
	// By is who decided it.
	By string      `json:"by"`
	At metav1.Time `json:"at"`
}

// ReviewErrorKind is why one reviewer failed.
// +kubebuilder:validation:Enum=usageLimit;transient;other
type ReviewErrorKind string

const (
	// ReviewErrorUsageLimit: the reviewer's engine hit its usage limit.
	ReviewErrorUsageLimit ReviewErrorKind = "usageLimit"
	// ReviewErrorTransient: an outage of the reviewer's service.
	ReviewErrorTransient ReviewErrorKind = "transient"
	// ReviewErrorOther: any other failure.
	ReviewErrorOther ReviewErrorKind = "other"
)

// ReviewError is one reviewer's failure in the last review.
type ReviewError struct {
	Reviewer ReviewerKind    `json:"reviewer"`
	Kind     ReviewErrorKind `json:"kind"`
	// Engine is the limited engine (kind usageLimit).
	// +optional
	Engine Engine `json:"engine,omitempty"`
	// ResetAt is when the limit resets, if the engine said so.
	// +optional
	ResetAt *metav1.Time `json:"resetAt,omitempty"`
}

// PauseKind is why autopilot paused.
// +kubebuilder:validation:Enum=owner;cycle;stuck;needsInput;runFailed;findings;reviewErrors;notReviewed;ciRed;ciCanceled;mergeRequest;mainRed;config;budget;companion;emptyPlan;release;stalled
type PauseKind string

const (
	// PauseOwner: the pause command.
	PauseOwner PauseKind = "owner"
	// PauseCycle: the plan has a dependency cycle.
	PauseCycle PauseKind = "cycle"
	// PauseStuck: no queued issue can start.
	PauseStuck PauseKind = "stuck"
	// PauseNeedsInput: the active issue has the needs-input label.
	PauseNeedsInput PauseKind = "needsInput"
	// PauseRunFailed: the active issue's last run did not succeed.
	PauseRunFailed PauseKind = "runFailed"
	// PauseFindings: blocking review findings remain.
	PauseFindings PauseKind = "findings"
	// PauseReviewErrors: reviewers failed (status.run.reviewErrorKinds).
	PauseReviewErrors PauseKind = "reviewErrors"
	// PauseNotReviewed: the head, or the companion head, was pushed after the
	// last review.
	PauseNotReviewed PauseKind = "notReviewed"
	// PauseCIRed: CI of the MR is red.
	PauseCIRed PauseKind = "ciRed"
	// PauseCICanceled: the MR's pipeline was canceled.
	PauseCICanceled PauseKind = "ciCanceled"
	// PauseMergeRequest: the MR cannot be merged (state, draft, approvals,
	// discussions, conflicts, a moved head, GitLab refused).
	PauseMergeRequest PauseKind = "mergeRequest"
	// PauseMainRed: the default branch is red (merge freeze).
	PauseMainRed PauseKind = "mainRed"
	// PauseConfig: the project or issue setup does not allow a merge (no
	// workflow label, no reviewers, no pushed MR).
	PauseConfig PauseKind = "config"
	// PauseBudget: the milestone spent its budget (spec.budgetUSD).
	PauseBudget PauseKind = "budget"
	// PauseCompanion: the companion's merge request cannot go on (closed, a
	// conflict a rebase did not fix, another blocking merge status, a moved
	// head). A red companion pipeline pauses with ciRed.
	PauseCompanion PauseKind = "companion"
	// PauseEmptyPlan: the planning run succeeded but planned no issues.
	PauseEmptyPlan PauseKind = "emptyPlan"
	// PauseRelease: the final release failed twice.
	PauseRelease PauseKind = "release"
	// PauseStalled: the active issue's run is stalled and the pilot's
	// restarts did not help or the interruption cap is reached.
	PauseStalled PauseKind = "stalled"
)

// PauseResolver is who ended a pause (status.autopilot.resumedBy, the
// autopilot.resumed history event).
// +kubebuilder:validation:Enum=operator;pilot;owner;selfHealed;laterRun
type PauseResolver string

const (
	// ResolvedByOperator: an automatic exit (the cause went away).
	ResolvedByOperator PauseResolver = "operator"
	// ResolvedByPilot: the pilot's own action (managed autonomy).
	ResolvedByPilot PauseResolver = "pilot"
	// ResolvedByOwner: an owner's command or answered prompt.
	ResolvedByOwner PauseResolver = "owner"
	// ResolvedBySelfHealed: the operator rebased a BaseFailing MR onto a
	// green default branch.
	ResolvedBySelfHealed PauseResolver = "selfHealed"
	// ResolvedByLaterRun: a later run of the paused issue made it pass the
	// merge gate.
	ResolvedByLaterRun PauseResolver = "laterRun"
)

// Known reports whether r is one of the PauseResolver values.
func (r PauseResolver) Known() bool {
	switch r {
	case ResolvedByOperator, ResolvedByPilot, ResolvedByOwner, ResolvedBySelfHealed, ResolvedByLaterRun:
		return true
	}
	return false
}

// PauseResumeVia is how a pause ended (status.autopilot.resumedVia, the
// autopilot.resumed history event), next to who ended it (PauseResolver).
// +kubebuilder:validation:Enum=reReview;takeover;push;mergedByHand;closedByHand;command;causeGone;selfHealed
type PauseResumeVia string

const (
	// ResumedViaReReview: a review-only run the autopilot asked for (e.g.
	// after a rebase) passed the merge gate on the head it was asked for.
	ResumedViaReReview PauseResumeVia = "reReview"
	// ResumedViaTakeover: a run an owner's (or the pilot's) command started
	// made the issue pass the merge gate.
	ResumedViaTakeover PauseResumeVia = "takeover"
	// ResumedViaPush: a human pushed, and a later run reviewed that head.
	ResumedViaPush PauseResumeVia = "push"
	// ResumedViaMergedByHand: the merge request was merged by hand.
	ResumedViaMergedByHand PauseResumeVia = "mergedByHand"
	// ResumedViaClosedByHand: the issue was closed by hand.
	ResumedViaClosedByHand PauseResumeVia = "closedByHand"
	// ResumedViaCommand: `resume` or another owner or pilot command
	// (`merge`, `rebase`, `mergeBase`, `endMilestone`, turning autopilot
	// off).
	ResumedViaCommand PauseResumeVia = "command"
	// ResumedViaCauseGone: another automatic exit, the cause went away
	// (green default branch, raised budget, a companion that can go on).
	ResumedViaCauseGone PauseResumeVia = "causeGone"
	// ResumedViaSelfHealed: the operator rebased a BaseFailing MR onto a
	// green default branch.
	ResumedViaSelfHealed PauseResumeVia = "selfHealed"
)

// PauseRunByAutopilot is status.autopilot.pauseRunBy for a run the
// autopilot itself asked for. A person's command records "@name", the
// pilot "the pilot", so it cannot be mistaken for one.
const PauseRunByAutopilot = "autopilot"

// AutopilotItemState is the state of one issue in the queue.
// +kubebuilder:validation:Enum=Pending;Active;Merged;Skipped
type AutopilotItemState string

const (
	AutopilotItemPending AutopilotItemState = "Pending"
	AutopilotItemActive  AutopilotItemState = "Active"
	AutopilotItemMerged  AutopilotItemState = "Merged"
	AutopilotItemSkipped AutopilotItemState = "Skipped"
)

// AutopilotStatus is the queue of an autopilot milestone.
type AutopilotStatus struct {
	Phase AutopilotPhase `json:"phase"`

	// MaxParallel is the effective limit of how many issues of this
	// milestone may run at the same time, as of the last poll.
	// +optional
	MaxParallel int32 `json:"maxParallel,omitempty"`

	// Landing is the IID of the item that lands (rebase, CI, merge); at most
	// one per milestone, 0: none.
	// +optional
	Landing int64 `json:"landing,omitempty"`

	// TrackingIssueIID is the bot's tracking issue of the milestone.
	// +optional
	TrackingIssueIID int64 `json:"trackingIssueIID,omitempty"`

	// Reason why the milestone is paused.
	// +optional
	PausedReason string `json:"pausedReason,omitempty"`

	// PauseKind is the cause of the pause, for the Desk's pause card. Empty
	// for a pause from before it was recorded.
	// +optional
	PauseKind PauseKind `json:"pauseKind,omitempty"`

	// PausedFor is the active issue the pause is about. Autopilot continues
	// by itself once that issue is merged or closed by hand, and, with
	// ResumeWhenReady, once it passes the merge gate again (a later run
	// fixed it).
	// +optional
	PausedFor int64 `json:"pausedFor,omitempty"`

	// ResumeWhenReady: the pause is over PausedFor's run, review or CI.
	// +optional
	ResumeWhenReady bool `json:"resumeWhenReady,omitempty"`

	// MergeOverride is an owner's decision to merge one head of an issue
	// without a full review (the `merge` command). A new head voids it.
	// +optional
	MergeOverride *MergeOverride `json:"mergeOverride,omitempty"`

	// StartRequestedBy is the owner who set a run-next mark with the
	// `start` command.
	//
	// Deprecated: the project's status.milestoneOrder ranks milestones. The
	// operator no longer writes this field and reads it only to seed that
	// order.
	// +optional
	StartRequestedBy string `json:"startRequestedBy,omitempty"`

	// StartRequestedAt is the time of the run-next mark (set with
	// StartRequestedBy).
	//
	// Deprecated: read only to seed the project's status.milestoneOrder.
	// +optional
	StartRequestedAt *metav1.Time `json:"startRequestedAt,omitempty"`

	// Items in queue order: dependencies first, ties in plan order.
	// +optional
	Items []AutopilotItem `json:"items,omitempty"`

	// MainRedFix is the main-red issue (mainHealth.autoFix) this milestone
	// drives through the merge gate while the default branch is red.
	// +optional
	MainRedFix *AutopilotItem `json:"mainRedFix,omitempty"`

	// LastCommandNoteID is the newest command on the tracking issue the
	// operator has handled.
	// +optional
	LastCommandNoteID int64 `json:"lastCommandNoteID,omitempty"`

	// LastPilotCommand is the newest queue command from the pilot handled
	// (status.pilot.commands).
	// +optional
	LastPilotCommand int64 `json:"lastPilotCommand,omitempty"`

	// LastPilotRelease is the Seq of the newest release decision of the
	// pilot handled (the Coordinate task's status.pilot.releaseDecisions),
	// applied or refused.
	// +optional
	LastPilotRelease int64 `json:"lastPilotRelease,omitempty"`

	// TrackingHash is the hash of the tracking issue's last rendered
	// description, so it is only edited when it changes.
	// +optional
	TrackingHash string `json:"trackingHash,omitempty"`
	// CloseWaitsFor are the open issues a done milestone was last reported
	// to wait for before autopilot closes it.
	// +optional
	CloseWaitsFor []int64 `json:"closeWaitsFor,omitempty"`
	// CloseWaitsForCompanions are the companion merge requests of the
	// milestone ("<project>!<iid>") that had not landed (merged or closed)
	// when last reported: while there are any, autopilot is not done and does
	// not close the milestone.
	// +optional
	CloseWaitsForCompanions []string `json:"closeWaitsForCompanions,omitempty"`

	// Budget is the milestone's budget and its spend, refreshed every poll
	// while the milestone has a budget line.
	// +optional
	Budget *BudgetStatus `json:"budget,omitempty"`

	// StateHash is a hash of the queue's state (items, CI states, MR
	// heads, main health, phase and pause reason); StateChangedAt is when
	// it last changed. A pause it has not changed in for pilot.stuckAfter
	// is assessed by the pilot.
	// +optional
	StateHash string `json:"stateHash,omitempty"`
	// +optional
	StateChangedAt *metav1.Time `json:"stateChangedAt,omitempty"`

	// PausedAt is when the current pause started; unset while running.
	// +optional
	PausedAt *metav1.Time `json:"pausedAt,omitempty"`

	// ResumedBy is who ended the last pause; cleared by the next pause.
	// +optional
	ResumedBy PauseResolver `json:"resumedBy,omitempty"`

	// ResumedVia is how the last pause ended; set and cleared with
	// ResumedBy. Empty for a pause that ended before it was recorded.
	// +optional
	ResumedVia PauseResumeVia `json:"resumedVia,omitempty"`

	// PauseRunBy is who asked for the newest run of PausedFor during the
	// pause: PauseRunByAutopilot for the autopilot's own (a review-only run
	// after a rebase, a re-review), else the person ("@alice") or the pilot
	// whose command started it. A later run that ends the pause tells
	// resumedVia from it. Cleared when the pause ends or moves to another
	// issue.
	// +optional
	PauseRunBy string `json:"pauseRunBy,omitempty"`

	// PauseRunHead is the pushed head of PausedFor when the pause started,
	// then the head the newest run during the pause was asked for: a later
	// run passing on another head took over someone's push.
	// +optional
	PauseRunHead string `json:"pauseRunHead,omitempty"`

	// RepoNotices are the milestone's "/agent repo" lines (as written) the
	// operator has named on the tracking issue as ignored, so each is named
	// once. A line that is fixed or removed leaves the list.
	// +optional
	RepoNotices []string `json:"repoNotices,omitempty"`

	// Releases are the milestone's release decisions, oldest first (at most
	// MaxMilestoneReleases).
	// +optional
	Releases []ReleaseRecord `json:"releases,omitempty"`

	// SettleSyncedAt is when the operator last synced the milestone while it
	// was settling: done, waiting only for its retrospective's follow-ups.
	// +optional
	SettleSyncedAt *metav1.Time `json:"settleSyncedAt,omitempty"`
}

// BudgetStatus is the budget of an autopilot milestone and what it spent,
// from the history database (agent_calls).
type BudgetStatus struct {
	// LimitUSD is the budget (spec.budgetUSD).
	LimitUSD string `json:"limitUSD"`
	// SpentUSD is the milestone's spend; empty while it cannot be read
	// (no database, or the query failed).
	// +optional
	SpentUSD string `json:"spentUSD,omitempty"`
	// WarnedAt is when the owners were told that 80 % of the budget is
	// spent.
	// +optional
	WarnedAt *metav1.Time `json:"warnedAt,omitempty"`
}

// LineCount is a number of added and deleted lines.
type LineCount struct {
	Added   int64 `json:"added"`
	Deleted int64 `json:"deleted"`
}

// MergedLines are the lines a merge request merged, split by the category of
// each file's path: generated, tests, docs, and production for the rest.
type MergedLines struct {
	Production LineCount `json:"production"`
	Tests      LineCount `json:"tests"`
	Docs       LineCount `json:"docs"`
	Generated  LineCount `json:"generated"`
	// Partial is set when GitLab returned some file's diff without its
	// content (collapsed or too large); those lines are not counted.
	// +optional
	Partial bool `json:"partial,omitempty"`
}

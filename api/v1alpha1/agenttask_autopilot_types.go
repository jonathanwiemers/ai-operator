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

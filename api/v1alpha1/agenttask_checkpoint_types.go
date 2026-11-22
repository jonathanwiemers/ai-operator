package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CompanionRun is what the runner pushed and opened in the companion
// project (docs/specs/cross-repo-changes.spec.md, decision 6).
type CompanionRun struct {
	// Project is the companion's full path (spec.companion.project).
	Project string `json:"project"`
	// MRIID is the companion's draft merge request; MRURL its web URL.
	// +optional
	MRIID int64 `json:"mrIID,omitempty"`
	// +optional
	MRURL string `json:"mrURL,omitempty"`
	// Branch is the companion branch (spec.companion.branch).
	// +optional
	Branch string `json:"branch,omitempty"`
	// PushedSHA is the companion branch head the last push left.
	// +optional
	PushedSHA string `json:"pushedSHA,omitempty"`
	// ReviewedSHA is the companion head the last review saw.
	// +optional
	ReviewedSHA string `json:"reviewedSHA,omitempty"`
}

// CheckpointStatus is a checkpoint commit the runner pushed after an
// implementer call was cut off.
type CheckpointStatus struct {
	// SHA is the checkpoint commit.
	SHA string `json:"sha"`

	// Branch it was pushed to.
	Branch string `json:"branch"`

	// Reason the call was cut off: usage-limit, engine-error, timeout,
	// budget-stop, container-restart, pod-lost, oom-kill, or run-failed (the
	// run failed after its call, e.g. its answer did not decode).
	Reason string `json:"reason"`

	// Run is the run (status.run.runCount after it) and Step the
	// implementer call of the run that was cut off, from 1.
	// +optional
	Run int64 `json:"run,omitempty"`
	// +optional
	Step int32 `json:"step,omitempty"`

	// Files, Added and Deleted are the checkpoint's diff stat.
	// +optional
	Files int32 `json:"files,omitempty"`
	// +optional
	Added int64 `json:"added,omitempty"`
	// +optional
	Deleted int64 `json:"deleted,omitempty"`

	// Skipped are the files left out (too big, or new files that look like
	// secrets); they stayed in the workspace.
	// +optional
	// +listType=atomic
	Skipped []string `json:"skipped,omitempty"`

	// At is when the checkpoint was pushed.
	At metav1.Time `json:"at"`
}

// RunCheckpoint is a pushed checkpoint no agent call carries
// (RunStatus.RunCheckpoints).
type RunCheckpoint struct {
	// Reason the work was cut off: budget-stop, pod-lost, oom-kill,
	// container-restart or run-failed.
	Reason string `json:"reason"`

	// SHA is the checkpoint commit.
	SHA string `json:"sha"`

	// Run is the run whose work it saved and Step its implementer call,
	// from 1.
	// +optional
	Run int64 `json:"run,omitempty"`
	// +optional
	Step int32 `json:"step,omitempty"`

	// Files, Added and Deleted are the checkpoint's diff stat.
	// +optional
	Files int32 `json:"files,omitempty"`
	// +optional
	Added int64 `json:"added,omitempty"`
	// +optional
	Deleted int64 `json:"deleted,omitempty"`

	// At is when the checkpoint was pushed.
	At metav1.Time `json:"at"`
}

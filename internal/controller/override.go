package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

const notOwnerMerge = "Only the milestone's owners may merge without a full review."

// reviewPauses are the pauses a merge override ends.
var reviewPauses = []aiv1.PauseKind{aiv1.PauseFindings, aiv1.PauseReviewErrors, aiv1.PauseNotReviewed}

// mergeAnyway records an owner's decision to merge the active issue's head
// without a full review ("#N sha", the head they looked at): the merge gate
// skips its review checks for that head, CI, the MR state and approvals
// still apply. A pause over its review ends, so the gate runs again on the
// next poll. Applying it again changes nothing, so a retried poll is safe.
func (a *autopilot) mergeAnyway(ctx context.Context, arg, by string) (string, bool, error) {
	prefix := a.spec.Workflow.CommandPrefix
	f := append(strings.Fields(arg), "", "")
	iid, _ := strconv.ParseInt(strings.TrimPrefix(f[0], "#"), 10, 64)
	it := a.item(iid)
	if it == nil || it.State != aiv1.AutopilotItemActive {
		return fmt.Sprintf("`merge` needs the active issue and its head, e.g. `%s merge #12 5a0e3c1`.", prefix), false, nil
	}
	var run aiv1.RunStatus
	if task := a.tasks[iid]; task != nil {
		run = task.Status.Run
	}
	head := run.PushedSHA
	switch want := strings.ToLower(f[1]); {
	case head == "" || it.MergeRequestIID == 0:
		return fmt.Sprintf("#%d has no pushed merge request yet.", iid), false, nil
	case want == "":
		return fmt.Sprintf("`merge` needs the head you looked at: `%s merge #%d %s` for the current head of !%d.", prefix, iid, short(head), it.MergeRequestIID), false, nil
	case len(want) < 7 || !strings.HasPrefix(head, want):
		return fmt.Sprintf("The head of !%d is %s, not %s: nothing changed. Look at the new head first.", it.MergeRequestIID, short(head), notify.Defuse(f[1])), false, nil
	}
	if !a.overridden(it, head, run) {
		// Recorded on GitLab first: if it fails, the command is tried again.
		if err := a.comment(ctx, fmt.Sprintf("⚠️ %s lets autopilot merge !%d at %s without a full review. CI, the merge request's state and approvals still apply; a new push or review voids this.",
			notify.Defuse(by), it.MergeRequestIID, short(head))); err != nil {
			return "", false, err
		}
		a.ap.MergeOverride = &aiv1.MergeOverride{IssueIID: iid, SHA: head, By: by, At: metav1.Time{Time: a.r.now()}}
	}
	if a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedFor == iid && slices.Contains(reviewPauses, a.ap.PauseKind) {
		if err := a.unblockActive(ctx); err != nil {
			return "", false, err
		}
		a.ap.Phase, a.ap.PausedReason, a.ap.PauseKind = aiv1.AutopilotRunning, "", ""
		a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
	}
	return fmt.Sprintf("!%d at %s may merge without a full review.", it.MergeRequestIID, short(head)), true, nil
}

// overridden reports whether an owner let sha of it merge without a full
// review, and no run (a re-review) finished on it since.
func (a *autopilot) overridden(it *aiv1.AutopilotItem, sha string, run aiv1.RunStatus) bool {
	ov := a.ap.MergeOverride
	return ov != nil && sha != "" && ov.IssueIID == it.IID && ov.SHA == sha &&
		(run.LastActivityTime == nil || !run.LastActivityTime.After(ov.At.Time))
}

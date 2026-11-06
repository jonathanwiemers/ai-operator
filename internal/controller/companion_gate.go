package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// The home merge gate's checks of the companion's merge request
// (docs/specs/cross-repo-changes.spec.md, decision 7). Its CI and review
// are checked with the home ones (gateCheck); here it must be open and
// mergeable. It stays a draft until it lands, so draft_status counts as
// mergeable. need_rebase or a conflict gets one rebase per companion head
// (spec.source.companionRebase); anything else that blocks it pauses with
// kind companion.

// companionState is where the companion's merge request stands for the
// home merge.
type companionState int

const (
	// companionOK: none, merged, or open at the pushed head and mergeable
	// (draft_status counts).
	companionOK companionState = iota
	// companionWait: GitLab is checking it, or a rebase is under way.
	companionWait
	// companionRebase: it needs a rebase, and none was asked for its head.
	companionRebase
	// companionRebased: its head moved on from a rebased one (by the
	// autopilot's rebase, or by hand after it): the new head is taken over.
	companionRebased
	// companionBlocked: a human must act; the reason says why.
	companionBlocked
)

type companionVerdict struct {
	state  companionState
	reason string
	mr     *gitlab.MergeRequest
}

// companionCheck reads the task's companion merge request and says where
// it stands. It changes nothing, so autoResume asks it too.
func (a *autopilot) companionCheck(ctx context.Context, task *aiv1.AgentTask) (companionVerdict, error) {
	c := task.Status.Run.Companion
	if c == nil || c.PushedSHA == "" {
		return companionVerdict{state: companionOK}, nil
	}
	iid := task.Spec.Issue.IID
	blocked := func(format string, args ...any) (companionVerdict, error) {
		return companionVerdict{state: companionBlocked, reason: fmt.Sprintf(format, args...)}, nil
	}
	if c.MRIID == 0 {
		return blocked("the companion change of #%d in %s has no merge request (opening it failed); a run of #%d opens it", iid, c.Project, iid)
	}
	ref := companionRef(c)
	mr, err := a.gl.GetMergeRequest(ctx, c.Project, c.MRIID)
	switch {
	case gitlab.IsNotFound(err):
		return blocked("the companion merge request %s of #%d is gone", ref, iid)
	case err != nil:
		return companionVerdict{}, fmt.Errorf("getting the companion merge request %s: %w", ref, err)
	}
	v := companionVerdict{mr: mr}
	rb := task.Spec.Source.CompanionRebase
	rebased := rb != nil && rb.From == c.PushedSHA
	switch {
	case mr.State == "merged":
		a.log.Info("the companion merge request was merged by hand", "issue", iid, "companion", ref)
		v.state = companionOK
		return v, nil
	case mr.State != "opened":
		return blocked("the companion merge request %s of #%d was %s without a merge; reopen it, or have a run of #%d drop the companion change", ref, iid, mr.State, iid)
	case mr.RebaseInProgress:
		v.state = companionWait
		return v, nil
	case mr.SHA != c.PushedSHA && rebased:
		v.state = companionRebased
		return v, nil
	case mr.SHA != c.PushedSHA:
		return blocked("the head of the companion merge request %s (%s) is not the commit the agent pushed (%s); someone pushed to its branch. Comment `%s …` on #%d so the agent takes it over",
			ref, short(mr.SHA), short(c.PushedSHA), a.spec.Workflow.CommandPrefix, iid)
	case rebased && mr.MergeError != "":
		return blocked("the rebase of the companion merge request %s failed: %s", ref, mr.MergeError)
	}
	status := mr.DetailedMergeStatus
	if status == "draft_status" && mr.HasConflicts {
		status = "conflict" // a draft shows no other status
	}
	switch status {
	case "mergeable", "draft_status":
		v.state = companionOK
	case "checking", "unchecked", "preparing", "approvals_syncing", "ci_still_running", "ci_must_pass":
		v.state = companionWait
	case "need_rebase", "conflict":
		switch {
		case !rebased:
			v.state = companionRebase
		case status == "need_rebase" && rb.At != nil && a.r.now().Sub(rb.At.Time) < rebaseGrace:
			v.state = companionWait // GitLab refreshes the MR after the rebase
		case status == "conflict":
			return blocked("the companion merge request %s has merge conflicts with its default branch, and a rebase did not resolve them; resolve them on its branch", ref)
		default:
			return blocked("the companion merge request %s still needs a rebase after one", ref)
		}
	case "not_approved":
		return blocked("the companion merge request %s needs an approval", ref)
	case "discussions_not_resolved":
		return blocked("the companion merge request %s has unresolved discussions", ref)
	default:
		return blocked("GitLab reports the companion merge request %s as %q", ref, mr.DetailedMergeStatus)
	}
	return v, nil
}

// companionGate is the home merge gate's check of the companion's merge
// request; pass reports whether the home merge may go on.
func (a *autopilot) companionGate(ctx context.Context, it *aiv1.AutopilotItem, task *aiv1.AgentTask) (pass bool, err error) {
	v, err := a.companionCheck(ctx, task)
	if err != nil {
		return false, err
	}
	switch v.state {
	case companionOK:
		return true, nil
	case companionWait:
		return false, nil
	case companionRebase:
		return false, a.rebaseCompanion(ctx, it, task, v.mr)
	case companionRebased:
		return false, a.companionRebased(ctx, task, v.mr)
	}
	return false, a.pauseActive(ctx, it, aiv1.PauseCompanion, v.reason)
}

// rebaseCompanion asks GitLab to rebase the companion's merge request, once
// per head. Its change is hashed first, so the review can hold on the
// rebased head; the rebase is recorded before it is asked for, so its new
// head is never taken for someone's push.
func (a *autopilot) rebaseCompanion(ctx context.Context, it *aiv1.AutopilotItem, task *aiv1.AgentTask, mr *gitlab.MergeRequest) error {
	c := task.Status.Run.Companion
	diff, err := a.diffHash(ctx, c.Project, mr.IID)
	if err != nil {
		return err
	}
	task.Spec.Source.CompanionRebase = &aiv1.CompanionRebase{From: mr.SHA, Diff: diff, At: &metav1.Time{Time: a.r.now()}}
	if err := a.r.Update(ctx, task); err != nil {
		return err
	}
	if err := a.gl.RebaseMergeRequest(ctx, c.Project, mr.IID); err != nil {
		return a.pauseActive(ctx, it, aiv1.PauseCompanion, fmt.Sprintf("GitLab could not rebase the companion merge request %s (%v)", companionRef(c), err))
	}
	a.log.Info("autopilot rebases the companion merge request", "issue", it.IID, "companion", companionRef(c), "from", mr.SHA)
	return nil
}

// companionRebased takes over the companion's rebased head as its pushed
// one. The review holds if the change hashes as it did before the rebase;
// the CI loop follows the new head's pipeline (gateCheck hands it back).
func (a *autopilot) companionRebased(ctx context.Context, task *aiv1.AgentTask, mr *gitlab.MergeRequest) error {
	c := *task.Status.Run.Companion
	diff, err := a.diffHash(ctx, c.Project, mr.IID)
	if err != nil {
		return err
	}
	holds := diff != "" && diff == task.Spec.Source.CompanionRebase.Diff
	orig := task.DeepCopy()
	if holds && c.ReviewedSHA == c.PushedSHA {
		c.ReviewedSHA = mr.SHA
	}
	from := c.PushedSHA
	c.PushedSHA = mr.SHA
	task.Status.Run.Companion = &c
	if task.Status.CI != nil {
		task.Status.CI.Companion = nil
	}
	if err := a.r.Status().Patch(ctx, task, client.MergeFrom(orig)); err != nil {
		return err
	}
	a.log.Info("autopilot took over the rebased companion head", "issue", task.Spec.Issue.IID, "companion", companionRef(&c),
		"from", from, "to", mr.SHA, "reviewHolds", holds)
	return nil
}

// homeCIReset is the CI status after the home head changed under the
// autopilot (a rebase, a pipeline it started): the loop follows home again,
// the companion's status stays.
func homeCIReset(ci *aiv1.CIStatus, sha string) *aiv1.CIStatus {
	if ci == nil || ci.Companion == nil {
		return nil
	}
	return &aiv1.CIStatus{SHA: sha, State: aiv1.CIStatePending, Companion: ci.Companion.DeepCopy()}
}

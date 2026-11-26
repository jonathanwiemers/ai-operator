package ui

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// PlanMilestoneView is the milestone created from a plan's draft, with what
// became of the references earlier drafts' milestones make to it.
type PlanMilestoneView struct {
	IID    int64  `json:"iid"`
	WebURL string `json:"webURL"`
	// FilledIn has one entry per earlier milestone that named this draft by
	// its quoted title; empty when none did.
	FilledIn []PlanRefFillView `json:"filledIn"`
}

// PlanRefFillView is what became of one earlier draft's reference to the
// created draft: Key and IID are the earlier draft and its milestone. State
// is "filled" (its quoted title became the %IID) or "skipped", with Reason.
type PlanRefFillView struct {
	Key    int64  `json:"key"`
	IID    int64  `json:"iid"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// The states of a PlanRefFillView.
const (
	refFilled  = "filled"
	refSkipped = "skipped"
)

// maxRefReason is the most bytes of a GitLab error a skip reports.
const maxRefReason = 300

// refFill is a handled pending ref: the earlier draft, its recorded text and
// the outcome.
type refFill struct {
	view PlanRefFillView
	text string
}

// fillPendingRefs writes %iid, the milestone of pl's just created draft key,
// in place of the quoted title each created draft of pl recorded for key,
// in list order, with the owner's client gl: only while that milestone's
// Spec task waits for its turn (spec.runRequest 0, or no task yet) and its
// description still holds the text. It writes an audit line per entry, then
// drops the handled entries from the plan and puts the %iid into the stored
// text of the filled drafts. Nothing here fails the create.
func (s *Server) fillPendingRefs(ctx context.Context, gl gitlab.API, ap *aiv1.AgentProject, pl *aiv1.AgentPlan,
	tok *GitLabToken, user string, key, iid int64) []PlanRefFillView {
	fills := []refFill{}
	for _, d := range pl.Status.Drafts {
		i := slices.IndexFunc(d.PendingRefs, func(p aiv1.PlanDraftRef) bool { return p.Key == key })
		if d.CreatedMilestoneIID <= 0 || i < 0 {
			continue
		}
		text := d.PendingRefs[i].Text
		f := refFill{view: PlanRefFillView{Key: d.Key, IID: d.CreatedMilestoneIID, State: refSkipped}, text: text}
		if reason := s.fillRef(ctx, gl, ap, d.CreatedMilestoneIID, text, "%"+strconv.FormatInt(iid, 10)); reason != "" {
			f.view.Reason = textutil.Truncate(textutil.Redact(reason, tok.Access, tok.Refresh), maxRefReason)
		} else {
			f.view.State = refFilled
		}
		s.log.Info("audit", "action", "plan.ref.fill", "user", user, "gitlabUser", tok.Username, "namespace", pl.Namespace,
			"name", pl.Name, "project", ap.Name, "key", d.Key, "milestone", d.CreatedMilestoneIID, "ref", key, "iid", iid,
			"state", f.view.State, "reason", f.view.Reason)
		fills = append(fills, f)
	}
	if len(fills) > 0 {
		if err := s.dropPendingRefs(ctx, pl, key, iid, fills); err != nil {
			s.log.Error(err, "cannot drop the plan's pending refs", "namespace", pl.Namespace, "name", pl.Name, "key", key)
		}
	}
	views := make([]PlanRefFillView, len(fills))
	for i, f := range fills {
		views[i] = f.view
	}
	return views
}

// fillRef replaces each text in the description of milestone %milestone
// with ref, unless its Spec task has started planning or the text is gone:
// "" when it did, else why not.
func (s *Server) fillRef(ctx context.Context, gl gitlab.API, ap *aiv1.AgentProject, milestone int64, text, ref string) string {
	// Read the tasks right before the edit: planning may start any moment.
	tasks, err := s.projectTasks(ctx, ap.Namespace, ap.Name)
	if err != nil {
		return err.Error()
	}
	if slices.ContainsFunc(tasks, func(t aiv1.AgentTask) bool {
		return t.Spec.Type == aiv1.TaskTypeSpec && t.Spec.Milestone != nil && t.Spec.Milestone.IID == milestone && t.Spec.RunRequest > 0
	}) {
		return "already planning"
	}
	m, err := gl.GetMilestoneByIID(ctx, ap.Spec.GitLab.Project, milestone)
	switch {
	case err != nil:
		return err.Error()
	case !strings.Contains(m.Description, text):
		return "text changed in GitLab"
	}
	if _, err := gl.UpdateMilestoneDescription(ctx, ap.Spec.GitLab.Project, m.ID, strings.ReplaceAll(m.Description, text, ref)); err != nil {
		return err.Error()
	}
	return ""
}

// dropPendingRefs removes the pending refs to key from pl's drafts and, in
// the stored text of each filled draft, replaces its recorded text with %iid:
// one status patch under optimistic locking, read again on a conflict.
func (s *Server) dropPendingRefs(ctx context.Context, pl *aiv1.AgentPlan, key, iid int64, fills []refFill) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := s.writer.Get(ctx, client.ObjectKeyFromObject(pl), pl); err != nil {
			return err
		}
		base := pl.DeepCopy()
		st := &pl.Status
		st.MoveDrafts()
		for _, f := range fills {
			if i := st.DraftIndex(f.view.Key); i >= 0 && f.view.State == refFilled {
				st.Drafts[i].Draft = strings.ReplaceAll(st.Drafts[i].Draft, f.text, "%"+strconv.FormatInt(iid, 10))
			}
		}
		dropDraftRefs(st.Drafts, key)
		return s.writer.Status().Patch(ctx, pl, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

// dropDraftRefs removes every pending ref to key from drafts.
func dropDraftRefs(drafts []aiv1.PlanDraft, key int64) {
	for i := range drafts {
		drafts[i].PendingRefs = slices.DeleteFunc(drafts[i].PendingRefs, func(p aiv1.PlanDraftRef) bool { return p.Key == key })
		if len(drafts[i].PendingRefs) == 0 {
			drafts[i].PendingRefs = nil
		}
	}
}

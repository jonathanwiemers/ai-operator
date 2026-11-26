package ui

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// draftCreating marks a draft whose milestone the UI server is creating.
const draftCreating = -1

// createPlanMilestone creates the milestone of the plan's draft {key, title,
// description, spec, autopilot} with the owner's GitLab token: the owner's
// text with each %draft-N of a created draft replaced by its %IID and, in the
// description only, each one of a later draft by its quoted title, which the
// draft records as its PendingRefs; then the chosen markers and the owners
// lines. Key 0 is the first draft without a milestone, a new one on
// a plan without drafts. Drafts are created in list order only. The draft is
// claimed first (its IID set to draftCreating under optimistic locking), so
// one request wins; a GitLab failure restores it. The plan's phase becomes
// Created with its last draft. Then the earlier milestones that name the
// draft by its quoted title get its %IID (fillPendingRefs).
func (s *Server) createPlanMilestone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key         int64  `json:"key"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Spec        bool   `json:"spec"`
		Autopilot   bool   `json:"autopilot"`
	}
	pl, ok := s.ownPlan(w, r, 8*maxFollowUpDescription, &req) // JSON escapes a byte in up to 6
	if !ok || !milestoneText(w, &req.Title, &req.Description) {
		return
	}
	base := pl.DeepCopy()
	st := &pl.Status
	st.MoveDrafts()
	i := st.DraftIndex(req.Key)
	if req.Key == 0 {
		i = planDraftIndex(st.Drafts)
	}
	added := i < 0 && req.Key == 0
	if added {
		st.Drafts, i = append(st.Drafts, aiv1.PlanDraft{Key: st.NextDraftKey()}), len(st.Drafts)
	}
	switch {
	case planDraftsLocked(base) != "":
		writeError(w, http.StatusConflict, planDraftsLocked(base))
		return
	case i < 0:
		notFound(w)
		return
	case draftCreateRefusal(st.Drafts, i) != "":
		writeError(w, http.StatusConflict, draftCreateRefusal(st.Drafts, i))
		return
	}
	key := st.Drafts[i].Key
	title, refs, _ := resolveDraftRefs(req.Title, st.Drafts, i, false)
	if len(refs) > 0 {
		writeErrorResponse(w, http.StatusUnprocessableEntity, ErrorResponse{Error: unresolvedDraftRefs(refs), Field: "title"})
		return
	}
	description, refs, pending := resolveDraftRefs(req.Description, st.Drafts, i, true)
	switch {
	case len(refs) > 0:
		writeErrorResponse(w, http.StatusUnprocessableEntity, ErrorResponse{Error: unresolvedDraftRefs(refs), Field: "description"})
		return
	case len(pending) > maxPendingRefs:
		writeErrorResponse(w, http.StatusUnprocessableEntity, ErrorResponse{Field: "description",
			Error: fmt.Sprintf("the description refers to %d later drafts; refer to at most %d", len(pending), maxPendingRefs)})
		return
	}
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	gl, tok, ok := s.writingGitLab(w, r, ap)
	if !ok {
		return
	}
	st.Drafts[i].CreatedMilestoneIID = draftCreating
	switch err := s.writer.Status().Patch(r.Context(), pl, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); {
	case apierrors.IsConflict(err):
		writeError(w, http.StatusConflict, "the plan changed meanwhile; reload it and try again")
		return
	case err != nil:
		s.internalError(w, err)
		return
	}
	// The claim is ours now: settle it even when the client goes away.
	ctx, user := context.WithoutCancel(r.Context()), principalOf(r).Username
	owners, subjects := planMilestoneOwners(ap, tasks, user)
	desc := withHeader(stripOperatorLines(description, ap.Spec.Workflow.WithDefaults().CommandPrefix), milestoneHeader(ap.Spec,
		milestoneHeaderOpts{Spec: req.Spec, Autopilot: req.Autopilot, Owners: owners, Subjects: subjects}))
	m, err := gl.CreateMilestone(ctx, ap.Spec.GitLab.Project, gitlab.CreateMilestoneOptions{Title: title, Description: desc})
	if err != nil {
		if err := s.settleDraft(ctx, pl, key, func(i int) {
			st.Drafts[i].CreatedMilestoneIID = 0
			if added { // the draft was added for the request: remove it, and give its key out again
				st.Drafts = slices.Delete(st.Drafts, i, i+1)
				if st.DraftSeq == key {
					st.DraftSeq--
				}
			}
		}); err != nil {
			s.log.Error(err, "cannot restore the plan's draft", "namespace", pl.Namespace, "name", pl.Name, "key", key)
		}
		s.writeCreateMilestoneError(w, err, tok, "creating a milestone from a plan failed",
			"user", user, "gitlabUser", tok.Username, "namespace", pl.Namespace, "name", pl.Name, "project", ap.Name, "key", key)
		return
	}
	url := milestoneURL(ap, m)
	if err := s.settleDraft(ctx, pl, key, func(i int) {
		d := &st.Drafts[i]
		d.CreatedMilestoneIID, d.CreatedMilestoneURL, d.Title = m.IID, url, cmp.Or(m.Title, title)
		d.Draft, d.Revision, d.By, d.PendingRefs = description, d.Revision+1, "owner", pending
		if st.CreatedMilestoneIID == 0 { // the first created draft names the plan
			st.Title, st.CreatedMilestoneIID = d.Title, m.IID
		}
		if pl.AllDraftsCreated() {
			st.Phase = aiv1.AgentPlanCreated
		}
	}); err != nil {
		s.log.Error(err, "cannot record the plan's milestone", "namespace", pl.Namespace, "name", pl.Name, "key", key, "milestone", m.IID)
	}
	s.log.Info("audit", "action", "plan.milestone", "user", user, "gitlabUser", tok.Username,
		"namespace", pl.Namespace, "name", pl.Name, "project", ap.Name, "key", key, "created", m.IID)
	filled := s.fillPendingRefs(ctx, gl, ap, pl, tok, user, key, m.IID)
	writeJSON(w, http.StatusCreated, PlanMilestoneView{IID: m.IID, WebURL: url, FilledIn: filled})
}

// settleDraft reads pl again and, while its draft key is still claimed,
// applies change to the draft's index in pl.Status.Drafts and patches the
// status under optimistic locking, reading again on a conflict.
func (s *Server) settleDraft(ctx context.Context, pl *aiv1.AgentPlan, key int64, change func(i int)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := s.writer.Get(ctx, client.ObjectKeyFromObject(pl), pl); err != nil {
			return err
		}
		base := pl.DeepCopy()
		pl.Status.MoveDrafts()
		i := pl.Status.DraftIndex(key)
		if i < 0 || pl.Status.Drafts[i].CreatedMilestoneIID != draftCreating {
			return nil
		}
		change(i)
		return s.writer.Status().Patch(ctx, pl, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

// draftCreateRefusal is why the draft at i of drafts may not be created now:
// it has its milestone or is being created, or an earlier draft has none
// yet; "" when it may.
func draftCreateRefusal(drafts []aiv1.PlanDraft, i int) string {
	d := drafts[i]
	switch first := slices.IndexFunc(drafts, func(d aiv1.PlanDraft) bool { return d.CreatedMilestoneIID <= 0 }); {
	case d.CreatedMilestoneIID > 0:
		return fmt.Sprintf("draft %d is already created as %%%d", d.Key, d.CreatedMilestoneIID)
	case d.CreatedMilestoneIID < 0:
		return fmt.Sprintf("draft %d is being created", d.Key)
	case first != i:
		return fmt.Sprintf("create draft %d first", drafts[first].Key)
	}
	return ""
}

// draftRef is a reference to a draft of the plan: %draft-N, N its key.
var draftRef = regexp.MustCompile(`%draft-([0-9]+)\b`)

// maxPendingRefs is the most later drafts one draft's description may refer
// to (PlanDraft.PendingRefs).
const maxPendingRefs = 5

// resolveDraftRefs resolves each %draft-N in text, the text of the draft at
// self of drafts: a draft with its milestone becomes the milestone's %IID.
// With forward, a later draft without a milestone and with a title becomes
// that title in straight double quotes, returned among the pending refs once
// per key in text order. It also returns the references it cannot replace,
// once each in text order: an unknown key, self, a draft being created, an
// earlier draft without a milestone, and a later one without forward or
// without a title.
func resolveDraftRefs(text string, drafts []aiv1.PlanDraft, self int, forward bool) (string, []string, []aiv1.PlanDraftRef) {
	var refs []string
	var pending []aiv1.PlanDraftRef
	out := draftRef.ReplaceAllStringFunc(text, func(ref string) string {
		key, err := strconv.ParseInt(draftRef.FindStringSubmatch(ref)[1], 10, 64)
		i := slices.IndexFunc(drafts, func(d aiv1.PlanDraft) bool { return d.Key == key })
		switch {
		case err != nil || i < 0 || i == self:
		case drafts[i].CreatedMilestoneIID > 0:
			return "%" + strconv.FormatInt(drafts[i].CreatedMilestoneIID, 10)
		case forward && i > self && drafts[i].CreatedMilestoneIID == 0 && strings.TrimSpace(drafts[i].Title) != "":
			quoted := `"` + strings.TrimSpace(drafts[i].Title) + `"`
			if !slices.ContainsFunc(pending, func(p aiv1.PlanDraftRef) bool { return p.Key == key }) {
				pending = append(pending, aiv1.PlanDraftRef{Key: key, Text: quoted})
			}
			return quoted
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
		return ref
	})
	return out, refs, pending
}

// unresolvedDraftRefs is the 422 message naming the references refs.
func unresolvedDraftRefs(refs []string) string {
	if len(refs) == 1 {
		return refs[0] + " is not created yet; create it first or remove the reference"
	}
	return strings.Join(refs, ", ") + " are not created yet; create them first or remove the references"
}

// planMilestoneOwners are the owners and owner-subjects of a milestone user
// creates from a plan: those of the project's live Spec tasks (owners else
// its notify users), and user among the subjects.
func planMilestoneOwners(ap *aiv1.AgentProject, tasks []aiv1.AgentTask, user string) (owners, subjects []string) {
	for _, t := range tasks {
		if t.Spec.Type == aiv1.TaskTypeSpec && t.DeletionTimestamp == nil {
			owners, subjects = append(owners, t.Spec.Owners...), append(subjects, t.Spec.OwnerSubjects...)
		}
	}
	if len(owners) == 0 {
		owners = slices.Clone(ap.Spec.Notify.Users)
	}
	subjects = append(subjects, user)
	slices.Sort(owners)
	slices.Sort(subjects)
	return slices.Compact(owners), slices.Compact(subjects)
}

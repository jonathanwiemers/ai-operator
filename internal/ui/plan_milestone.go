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
// text with each %draft-N replaced by its %IID, then the chosen markers and
// the owners lines. Key 0 is the first draft without a milestone, a new one on
// a plan without drafts. Drafts are created in list order only. The draft is
// claimed first (its IID set to draftCreating under optimistic locking), so
// one request wins; a GitLab failure restores it. The plan's phase becomes
// Created with its last draft.
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
	// The title has no line break, so it is the text's first line.
	text, refs := resolveDraftRefs(req.Title+"\n"+req.Description, st.Drafts, key)
	if len(refs) > 0 {
		writeErrorResponse(w, http.StatusUnprocessableEntity, ErrorResponse{Error: unresolvedDraftRefs(refs), Field: "description"})
		return
	}
	title, description, _ := strings.Cut(text, "\n")
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
		d.Draft, d.Revision, d.By = description, d.Revision+1, "owner"
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
	writeJSON(w, http.StatusCreated, FollowUpMilestoneView{IID: m.IID, WebURL: url})
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

// resolveDraftRefs replaces each %draft-N in text whose draft has its
// milestone with the milestone's %IID. It returns the text and the
// references it cannot replace, once each in text order: a draft without a
// milestone (or being created), an unknown key and self, the draft the text
// is for.
func resolveDraftRefs(text string, drafts []aiv1.PlanDraft, self int64) (string, []string) {
	var refs []string
	out := draftRef.ReplaceAllStringFunc(text, func(ref string) string {
		key, err := strconv.ParseInt(draftRef.FindStringSubmatch(ref)[1], 10, 64)
		if i := slices.IndexFunc(drafts, func(d aiv1.PlanDraft) bool { return d.Key == key }); err == nil && key != self && i >= 0 && drafts[i].CreatedMilestoneIID > 0 {
			return "%" + strconv.FormatInt(drafts[i].CreatedMilestoneIID, 10)
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
		return ref
	})
	return out, refs
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

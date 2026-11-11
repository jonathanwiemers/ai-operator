package ui

import (
	"net/http"
	"slices"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// dismissBody is the reply in the retrospective's thread that dismisses
// its remaining follow-ups (command.RetroNone).
const dismissBody = "create none"

// dismissable reports whether the remaining follow-ups of the milestone of
// the spec task st can still be dismissed: autopilot runs it (so the
// operator reads its tracking issue), its retrospective is posted, none
// dismissed them yet and one is open.
func dismissable(st *aiv1.AgentTask) bool {
	if !st.Spec.Autopilot || st.Status.Autopilot == nil || st.Status.Pilot == nil {
		return false
	}
	r := st.Status.Pilot.Retrospective
	return r != nil && r.NoteID != 0 && r.DismissedAt == nil && openFollowUps(liveRetrospective("", nil, st))
}

// dismissFollowUps dismisses the remaining follow-ups of an open
// milestone's retrospective: it replies "create none" in the
// retrospective's thread on the confirmed tracking issue, with the user's
// GitLab token. Only the milestone's owner subjects with the steer grant
// may. The operator settles them on its next poll (202); the UI writes
// nothing.
func (s *Server) dismissFollowUps(w http.ResponseWriter, r *http.Request) {
	if !s.mayPost(w, r) {
		return
	}
	ap, tasks, iid, ok := s.loadMilestoneIID(w, r)
	if !ok {
		return
	}
	st, _ := milestoneTasks(tasks, iid)
	p := principalOf(r)
	switch {
	case st == nil:
		writeError(w, http.StatusConflict, "the milestone is closed")
		return
	case !slices.Contains(st.Spec.OwnerSubjects, p.Username):
		writeError(w, http.StatusForbidden, "only the milestone's owners may dismiss its follow-ups")
		return
	case !dismissable(st):
		writeError(w, http.StatusConflict, "the milestone has no open follow-ups to dismiss")
		return
	}
	retro := st.Status.Pilot.Retrospective
	_, gl, tok, part, ok := s.trackingIssue(w, r)
	if !ok {
		return
	}
	// The operator takes "create none" only from the owners it pinned.
	if tok.UserID != 0 && !slices.Contains(retro.OwnerIDs, tok.UserID) {
		writeError(w, http.StatusForbidden, "your GitLab account is not among the milestone's owners")
		return
	}
	if ok, _ := s.notes.allow(p.Username, time.Now()); !ok {
		writeError(w, http.StatusTooManyRequests, "too many requests, try again in a minute")
		return
	}
	project := ap.Spec.GitLab.Project
	failed := func(step string, err error) {
		code := gitlab.StatusCode(err)
		s.log.Info("dismissing follow-ups failed", "step", step, "user", p.Username, "gitlabUser", tok.Username,
			"project", ap.Namespace+"/"+ap.Name, "milestone", iid, "status", code)
		switch code {
		case http.StatusUnauthorized:
			s.gitlabRejected(w, tok)
		case http.StatusForbidden, http.StatusNotFound:
			writeError(w, http.StatusForbidden, "GitLab did not let you reply to the retrospective")
		default:
			writeError(w, http.StatusBadGateway, "GitLab did not take the reply")
		}
	}
	threads, err := gl.ListIssueDiscussions(r.Context(), project, part.IID)
	if err != nil {
		failed("list", err)
		return
	}
	i := slices.IndexFunc(threads, func(d gitlab.Discussion) bool { return len(d.Notes) > 0 && d.Notes[0].ID == retro.NoteID })
	if i < 0 {
		writeError(w, http.StatusConflict, "the retrospective's comment is gone from the tracking issue")
		return
	}
	n, err := gl.ReplyIssueDiscussion(r.Context(), project, part.IID, threads[i].ID, dismissBody)
	if err != nil {
		failed("reply", err)
		return
	}
	s.log.Info("audit", "action", "milestone.dismiss", "user", p.Username, "gitlabUser", tok.Username,
		"namespace", ap.Namespace, "project", ap.Name, "milestone", iid, "note", n.ID)
	writeJSON(w, http.StatusAccepted, NoteView{ID: n.ID, Author: tok.Username, Body: n.Body, CreatedAt: n.CreatedAt})
}

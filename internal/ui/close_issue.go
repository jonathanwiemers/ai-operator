package ui

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// closeHoldingIssue closes an issue that holds a done autopilot milestone
// open (its CloseWaitsFor) with the user's GitLab token: a note saying who
// and why first, then the close. Only the milestone's owner subjects with
// the steer grant may; the operator closes the milestone on its next poll.
func (s *Server) closeHoldingIssue(w http.ResponseWriter, r *http.Request) {
	if !s.mayPost(w, r) {
		return
	}
	ap, tasks, iid, ok := s.loadMilestoneIID(w, r)
	if !ok {
		return
	}
	issue, err := strconv.ParseInt(r.PathValue("issue"), 10, 64)
	st, _ := milestoneTasks(tasks, iid)
	if err != nil || st == nil {
		notFound(w)
		return
	}
	if !st.Spec.Autopilot || st.Status.Autopilot == nil || st.Status.Autopilot.Phase != aiv1.AutopilotDone {
		writeError(w, http.StatusConflict, "the milestone is not done")
		return
	}
	if !slices.Contains(st.Status.Autopilot.CloseWaitsFor, issue) {
		writeError(w, http.StatusConflict, "the issue does not hold the milestone")
		return
	}
	p := principalOf(r)
	if !slices.Contains(st.Spec.OwnerSubjects, p.Username) {
		writeError(w, http.StatusForbidden, "only the milestone's owners may close its issues")
		return
	}
	gl, tok, ok := s.writingGitLab(w, r, ap)
	if !ok {
		return
	}
	// The note first: a failed close retried posts it twice at worst.
	// The shared tokenUser token writes every note as one user: name the login.
	by := "@" + tok.Username
	if tok.Fixed {
		by = p.Username
	}
	note, project := fmt.Sprintf("Closed from the Desk by %s to finish %%%d.", by, iid), ap.Spec.GitLab.Project
	if _, err = gl.CreateIssueNote(r.Context(), project, issue, note); err == nil {
		_, err = gl.UpdateIssue(r.Context(), project, issue, gitlab.UpdateIssueOptions{StateEvent: "close"})
	}
	if err != nil {
		code := gitlab.StatusCode(err)
		s.log.Info("closing an issue failed", "user", p.Username, "gitlabUser", tok.Username,
			"project", ap.Namespace+"/"+ap.Name, "milestone", iid, "issue", issue, "status", code)
		switch code {
		case http.StatusUnauthorized:
			s.gitlabRejected(w, tok)
		case http.StatusForbidden, http.StatusNotFound:
			writeError(w, http.StatusForbidden, "GitLab did not let you close the issue")
		default:
			writeError(w, http.StatusBadGateway, "GitLab did not close the issue")
		}
		return
	}
	s.log.Info("audit", "action", "issue.close", "user", p.Username, "gitlabUser", tok.Username,
		"namespace", ap.Namespace, "project", ap.Name, "milestone", iid, "issue", issue)
	writeJSON(w, http.StatusOK, FinishingIssue{IID: issue, WebURL: fmt.Sprintf("%s/-/issues/%d", gitLabRef(ap).WebURL, issue)})
}

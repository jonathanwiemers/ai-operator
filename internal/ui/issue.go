package ui

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// IssueView is an issue of a milestone, its row of the milestone's view:
// from the milestone's live tasks, with the name of its implement task if
// there is one (whose view is the task's), or Stored, from the snapshot
// once the tasks are gone. Its stream is GET /api/v1/stream with project,
// milestone and issue. Titles are untrusted text.
type IssueView struct {
	Namespace         string `json:"namespace"`
	Project           string `json:"project"`
	MilestoneIID      int64  `json:"milestoneIID"`
	MilestoneTitle    string `json:"milestoneTitle"`
	Stored            bool   `json:"stored,omitempty"`
	WebURL            string `json:"webURL"`
	MilestoneIssueRow `tstype:",extends"`
}

// issue returns an issue of a milestone: from the live tasks, else from
// the snapshot. An issue not in the milestone is not found.
func (s *Server) issue(w http.ResponseWriter, r *http.Request) {
	ap, tasks, iid, ok := s.loadMilestoneIID(w, r)
	if !ok {
		return
	}
	issue, err := strconv.ParseInt(r.PathValue("issue"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	v := IssueView{Namespace: ap.Namespace, Project: ap.Name, MilestoneIID: iid,
		WebURL: fmt.Sprintf("%s/-/issues/%d", gitLabRef(ap).WebURL, issue)}
	pk, now := store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}, time.Now()
	db := s.milestoneDetailStore()
	if st, _ := milestoneTasks(tasks, iid); st != nil {
		byIssue := map[int64]string{}
		for i := range tasks {
			if t := &tasks[i]; t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Milestone != nil && t.Spec.Milestone.IID == iid && t.Spec.Issue != nil {
				byIssue[t.Spec.Issue.IID] = t.Name
			}
		}
		found := false
		for _, row := range issueRows(st, byIssue, now) {
			if row.IID == issue {
				v.MilestoneIssueRow, found = row, true
			}
		}
		if !found {
			notFound(w)
			return
		}
		v.MilestoneTitle = st.Spec.Milestone.Title
		if db != nil {
			f, err := db.IssueFigures(r.Context(), pk, iid, issue, now)
			switch {
			case err == nil:
				v.Figures = issueFiguresView(f)
			case !errors.Is(err, store.ErrNotFound):
				s.log.Error(err, "cannot read the issue's figures", "namespace", ap.Namespace, "project", ap.Name, "milestone", iid, "issue", issue)
			}
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	if db == nil {
		notFound(w)
		return
	}
	d, err := db.Milestone(r.Context(), pk, iid, now)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	for _, row := range storedMilestoneView(ap, d).Issues {
		if row.IID == issue {
			v.MilestoneIssueRow, v.MilestoneTitle, v.Stored = row, d.Title, true
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	notFound(w)
}

package ui

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// maxSearchResults caps GET /api/v1/search.
const maxSearchResults = 20

// SearchResult is a task of an issue (and its merge request) that matched a
// search; the web opens it at /projects/:namespace/:project/tasks/:task.
type SearchResult struct {
	Namespace       string        `json:"namespace"`
	Project         string        `json:"project"`
	Task            string        `json:"task"`
	Type            aiv1.TaskType `json:"type"`
	Title           string        `json:"title"`
	IssueIID        int64         `json:"issueIID"`
	MergeRequestIID int64         `json:"mergeRequestIID,omitempty"`
	CreatedAt       time.Time     `json:"createdAt"`
}

// SearchResponse is GET /api/v1/search: at most 20 results, newest first.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

// search finds the tasks with an issue the user may see by "#<issue iid>",
// "!<merge request iid>" or a case-insensitive part of the issue title.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		badRequest(w, "q must have at least 2 characters")
		return
	}
	var tasks aiv1.AgentTaskList
	if err := s.reader.List(r.Context(), &tasks); err != nil {
		s.internalError(w, err)
		return
	}
	match := func(t *aiv1.AgentTask) bool {
		return strings.Contains(strings.ToLower(t.Spec.Issue.Title), strings.ToLower(q))
	}
	if n, err := strconv.ParseInt(q[1:], 10, 64); err == nil && q[0] == '#' {
		match = func(t *aiv1.AgentTask) bool { return t.Spec.Issue.IID == n }
	} else if err == nil && q[0] == '!' {
		match = func(t *aiv1.AgentTask) bool { return t.Status.Run.MergeRequestIID == n }
	}
	p := principalOf(r)
	out := SearchResponse{Results: []SearchResult{}}
	for i := range tasks.Items {
		t := &tasks.Items[i]
		if t.Spec.Issue == nil || !s.authz.CanView(p, t.Namespace, t.Spec.ProjectRef) || !match(t) {
			continue
		}
		out.Results = append(out.Results, SearchResult{Namespace: t.Namespace, Project: t.Spec.ProjectRef, Task: t.Name,
			Type: t.Spec.Type, Title: t.Spec.Issue.Title, IssueIID: t.Spec.Issue.IID,
			MergeRequestIID: t.Status.Run.MergeRequestIID, CreatedAt: t.CreationTimestamp.Time})
	}
	slices.SortFunc(out.Results, func(a, b SearchResult) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Namespace+"/"+a.Task, b.Namespace+"/"+b.Task)
	})
	if len(out.Results) > maxSearchResults {
		out.Results = out.Results[:maxSearchResults]
	}
	writeJSON(w, http.StatusOK, out)
}

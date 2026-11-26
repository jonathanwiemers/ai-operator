package ui

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// looseIssuePages caps the issues without a milestone the view reads: 5
// pages of 100, the newest.
const looseIssuePages = 5

// LooseIssues is GET .../loose-issues: the project's loose issues queue
// and the open issues without a milestone an owner may take into it.
// Titles are data, rendered as text.
type LooseIssues struct {
	// Queue is nil while the project has no Queue task.
	Queue *LooseQueue `json:"queue" tstype:"LooseQueue | null"`
	// Items are the queue's issues, in queue order.
	Items []LooseItem `json:"items"`
	// Issues are the open issues without a milestone, newest first, at most
	// 500.
	Issues []LooseIssue `json:"issues"`
	// Take: the caller may take and drop issues (Desk command messages on
	// QueueTask): one of OwnerSubjects, with chat.
	Take bool `json:"take"`
	// OwnerSubjects are the Desk logins who may act on the queue.
	OwnerSubjects []string `json:"ownerSubjects"`
	// QueueTask is the Queue task's name, the chat route's target.
	QueueTask string `json:"queueTask,omitempty"`
}

// LooseQueue is the state of the loose issues queue.
type LooseQueue struct {
	Phase        aiv1.AutopilotPhase `json:"phase"`
	PausedReason string              `json:"pausedReason,omitempty"`
	MaxParallel  int32               `json:"maxParallel"`
	// FrozenAt is when the freeze began: the default branch is red with
	// mainHealth.freezeAutopilot, nothing starts or merges. Unset: not frozen.
	FrozenAt *metav1.Time `json:"frozenAt,omitempty"`
}

// LooseItem is one issue of the queue. HeadSHA is the implement task's
// pushed head, what "/agent merge #N <sha>" names.
type LooseItem struct {
	IID             int64                   `json:"iid"`
	Title           string                  `json:"title"`
	WebURL          string                  `json:"webURL"`
	State           aiv1.AutopilotItemState `json:"state"`
	HoldReason      string                  `json:"holdReason,omitempty"`
	LeftReason      string                  `json:"leftReason,omitempty"`
	MergeRequestIID int64                   `json:"mergeRequestIID,omitempty"`
	MergeRequestURL string                  `json:"mergeRequestURL,omitempty"`
	MergedAt        *metav1.Time            `json:"mergedAt,omitempty"`
	Task            string                  `json:"task,omitempty"`
	HeadSHA         string                  `json:"headSHA,omitempty"`
}

// LooseIssue is an open GitLab issue without a milestone. InQueue: it is a
// pending, active or held item of the queue.
type LooseIssue struct {
	IID       int64      `json:"iid"`
	Title     string     `json:"title"`
	Labels    []string   `json:"labels"`
	Author    string     `json:"author"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	WebURL    string     `json:"webURL"`
	InQueue   bool       `json:"inQueue"`
}

// looseIssues returns the project's loose issues view. With the queue off
// it reads nothing from GitLab; the issues are read with the user's token.
func (s *Server) looseIssues(w http.ResponseWriter, r *http.Request) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	out := LooseIssues{Items: []LooseItem{}, Issues: []LooseIssue{}, OwnerSubjects: []string{}}
	if !ap.Spec.LooseIssues.Enabled {
		writeJSON(w, http.StatusOK, out)
		return
	}
	var queue *aiv1.AgentTask
	for i := range tasks {
		if tasks[i].Spec.Type == aiv1.TaskTypeQueue {
			queue = &tasks[i]
		}
	}
	gl, tok, ok := s.userGitLab(w, r, ap)
	if !ok {
		return
	}
	issues, err := gl.ListIssues(r.Context(), ap.Spec.GitLab.Project,
		gitlab.ListIssuesOptions{NoMilestone: true, NewestFirst: true, MaxPages: looseIssuePages})
	if err != nil {
		if gitlab.StatusCode(err) == http.StatusUnauthorized {
			s.gitlabRejected(w, tok)
			return
		}
		s.log.Error(err, "cannot list the issues without a milestone", "namespace", ap.Namespace, "project", ap.Name)
		writeError(w, http.StatusBadGateway, "GitLab did not return the issues")
		return
	}
	if len(issues) > looseIssuePages*100 {
		issues = issues[:looseIssuePages*100]
	}
	inQueue := map[int64]bool{}
	if queue != nil {
		p := principalOf(r)
		out.QueueTask = queue.Name
		out.OwnerSubjects = append(out.OwnerSubjects, queue.Spec.OwnerSubjects...)
		out.Take = slices.Contains(queue.Spec.OwnerSubjects, p.Username) &&
			s.cfg.ChatEnabled() && s.authz.CanChat(p, ap.Namespace, ap.Name)
		out.Queue = &LooseQueue{Phase: aiv1.AutopilotRunning, MaxParallel: ap.Spec.LooseIssues.Parallel()}
		if st := queue.Status.Autopilot; st != nil {
			if st.Phase != "" {
				out.Queue.Phase = st.Phase
			}
			out.Queue.PausedReason, out.Queue.FrozenAt = st.PausedReason, st.FrozenAt
			out.Items = looseItems(ap, tasks, st.Items, issues)
			for _, it := range st.Items {
				inQueue[it.IID] = it.State == aiv1.AutopilotItemPending || it.State == aiv1.AutopilotItemActive || it.Hold != nil
			}
		}
	}
	for _, is := range issues {
		labels := is.Labels
		if labels == nil {
			labels = []string{}
		}
		out.Issues = append(out.Issues, LooseIssue{IID: is.IID, Title: is.Title, Labels: labels, Author: is.Author.Username,
			UpdatedAt: is.UpdatedAt, WebURL: is.WebURL, InQueue: inQueue[is.IID]})
	}
	writeJSON(w, http.StatusOK, out)
}

// looseItems are the views of the queue's items. A title comes from the
// issue list, else from the item's implement task.
func looseItems(ap *aiv1.AgentProject, tasks []aiv1.AgentTask, items []aiv1.AutopilotItem, issues []gitlab.Issue) []LooseItem {
	titles := map[int64]string{}
	for _, is := range issues {
		titles[is.IID] = is.Title
	}
	web := projectWeb(ap)
	out := make([]LooseItem, 0, len(items))
	for _, it := range items {
		v := LooseItem{IID: it.IID, Title: titles[it.IID], WebURL: fmt.Sprintf("%sissues/%d", web, it.IID), State: it.State,
			LeftReason: it.LeftReason, MergeRequestIID: it.MergeRequestIID, MergeRequestURL: it.MergeRequestURL, MergedAt: it.MergedAt}
		if it.Hold != nil {
			v.HoldReason = it.Hold.Reason
		}
		if t := issueTask(tasks, it.IID); t != nil {
			v.Task, v.HeadSHA = t.Name, t.Status.Run.PushedSHA
			if v.Title == "" {
				v.Title = t.Spec.Issue.Title
			}
		}
		out = append(out, v)
	}
	return out
}

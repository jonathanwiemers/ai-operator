package ui

import (
	"context"
	"fmt"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// pushEventWindow is how far back history events are push candidates.
const pushEventWindow = 15 * time.Minute

// pushEventKinds are the history kinds that may notify (pushEventCategory).
var pushEventKinds = []string{"autopilot", "milestone.started", "release.ended"}

// pushCandidate is something that may be pushed: an Inbox item or a history
// event, with its notification key and category.
type pushCandidate struct {
	Key, Category string
	Item          *InboxItem
	Event         *StreamEvent
}

// pushEventCategory is the category of an event that notifies, empty for
// none, as the Desk's eventCategory: an autopilot event only when Done, a
// release end only when released or failed.
func pushEventCategory(e StreamEvent) string {
	switch e.Kind {
	case "milestone.started":
		return "milestoneStarted"
	case "autopilot":
		if e.Data["phase"] == "Done" {
			return "milestoneDone"
		}
	case "release.ended":
		if st := e.Data["state"]; st == "Released" || st == "Failed" {
			return "release"
		}
	}
	return ""
}

// viewableInbox lists the projects p may view, by namespace/name, and all
// tasks: what inboxOf takes.
func (s *Server) viewableInbox(ctx context.Context, p Principal) (map[string]*aiv1.AgentProject, []aiv1.AgentTask, error) {
	var projects aiv1.AgentProjectList
	if err := s.reader.List(ctx, &projects); err != nil {
		return nil, nil, err
	}
	var tasks aiv1.AgentTaskList
	if err := s.reader.List(ctx, &tasks); err != nil {
		return nil, nil, err
	}
	visible := map[string]*aiv1.AgentProject{}
	for i := range projects.Items {
		ap := &projects.Items[i]
		if s.authz.CanView(p, ap.Namespace, ap.Name) {
			visible[ap.Namespace+"/"+ap.Name] = ap
		}
	}
	return visible, tasks.Items, nil
}

// pushCandidates are what may be pushed to p now: the Inbox items with a
// category and the notifying events of the last pushEventWindow, of the
// projects p may view.
func (s *Server) pushCandidates(ctx context.Context, st pushStore, p Principal, now time.Time) ([]pushCandidate, error) {
	visible, tasks, err := s.viewableInbox(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []pushCandidate
	for _, it := range inboxOf(visible, tasks, now) {
		if it.Notify != "" {
			out = append(out, pushCandidate{Key: it.Key, Category: it.Notify, Item: &it})
		}
	}
	keys := make([]store.ProjectKey, 0, len(visible))
	for _, ap := range visible {
		keys = append(keys, store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name})
	}
	events, err := st.Stream(ctx, store.StreamQuery{Projects: keys, Kinds: pushEventKinds,
		After: &store.Cursor{At: now.Add(-pushEventWindow)}, Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("read push events: %w", err)
	}
	for _, e := range events {
		ev := streamEvent(e, false)
		if c := pushEventCategory(ev); c != "" {
			out = append(out, pushCandidate{Key: fmt.Sprintf("event/%d", ev.ID), Category: c, Event: &ev})
		}
	}
	return out, nil
}

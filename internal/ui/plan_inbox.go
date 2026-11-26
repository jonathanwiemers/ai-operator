package ui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// viewablePlans lists all AgentPlans when the chat login is on, nil without
// it: plans need the plan chat.
func (s *Server) viewablePlans(ctx context.Context) ([]aiv1.AgentPlan, error) {
	if !s.cfg.ChatEnabled() {
		return nil, nil
	}
	var plans aiv1.AgentPlanList
	if err := s.reader.List(ctx, &plans); err != nil {
		return nil, err
	}
	return plans.Items, nil
}

// planInboxOf are the ready plans of the projects username may view (by
// namespace/name): a plan whose last finished turn answered username's
// message, until the next message, the created milestones or the deletion.
// A plan from before answeredFor gives none. Newest turn first.
func planInboxOf(projects map[string]*aiv1.AgentProject, plans []aiv1.AgentPlan, username string) []InboxItem {
	var items []InboxItem
	for i := range plans {
		pl := &plans[i]
		st := pl.Status
		if projects[pl.Namespace+"/"+pl.Spec.ProjectRef] == nil || username == "" || st.AnsweredFor != username ||
			len(pl.Spec.ChatMessages) > 0 || st.Chat == nil || st.Chat.LastTurnTime == nil {
			continue
		}
		switch st.Phase {
		case aiv1.AgentPlanWorking, aiv1.AgentPlanCreated, aiv1.AgentPlanFailed:
			continue
		}
		rep, ok := turnReply(st.Chat.Replies)
		if !ok {
			continue
		}
		reason := "The turn did not finish: " + rep.Error
		if rep.Error == "" {
			reason = firstLine(rep.Text)
		}
		at := st.Chat.LastTurnTime.Time
		items = append(items, InboxItem{Namespace: pl.Namespace, Project: pl.Spec.ProjectRef, Plan: pl.Name,
			Kind: "plan", Title: cmp.Or(st.Title, pl.Spec.Title, "Untitled plan"),
			Reason: truncateRunes(reason, inboxTitleMax), At: &at, Notify: "plan",
			Key: fmt.Sprintf("%s/%s/plan/%s/%d", pl.Namespace, pl.Spec.ProjectRef, pl.Name, rep.MessageID)})
	}
	slices.SortFunc(items, func(a, b InboxItem) int {
		return cmp.Or(b.At.Compare(*a.At), strings.Compare(a.Key, b.Key))
	})
	return items
}

// turnReply is the newest reply with text or an error: the last turn's, not
// an interrupt's own empty reply.
func turnReply(replies []aiv1.ChatReply) (aiv1.ChatReply, bool) {
	for i := len(replies) - 1; i >= 0; i-- {
		if replies[i].Text != "" || replies[i].Error != "" {
			return replies[i], true
		}
	}
	return aiv1.ChatReply{}, false
}

// firstLine is s's first non-empty line, trimmed.
func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

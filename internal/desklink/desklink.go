// Package desklink builds links to the Desk (the web UI). The routes match
// web/src/main.ts. Every helper returns "" when the Desk's URL is not known.
package desklink

import (
	"net/url"
	"strconv"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func project(base, ns, project string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return ""
	}
	return base + "/projects/" + url.PathEscape(ns) + "/" + url.PathEscape(project)
}

// Milestone is the Desk page of a milestone.
func Milestone(base, ns, proj string, iid int64) string {
	p := project(base, ns, proj)
	if p == "" {
		return ""
	}
	return p + "/milestones/" + strconv.FormatInt(iid, 10)
}

// Task is the Desk page of an AgentTask.
func Task(base, ns, proj, task string) string {
	p := project(base, ns, proj)
	if p == "" {
		return ""
	}
	return p + "/tasks/" + url.PathEscape(task)
}

// Prompt links to a prompt held by a task: the milestone page for the tasks of
// a milestone (Spec, Coordinate), the task page for an Implement task. The
// page scrolls to the prompt's card.
func Prompt(base string, holder *aiv1.AgentTask, promptID string) string {
	if holder == nil {
		return ""
	}
	var link string
	switch {
	case holder.Spec.Type == aiv1.TaskTypeImplement:
		link = Task(base, holder.Namespace, holder.Spec.ProjectRef, holder.Name)
	case holder.Spec.Milestone != nil:
		link = Milestone(base, holder.Namespace, holder.Spec.ProjectRef, holder.Spec.Milestone.IID)
	}
	if link == "" {
		return ""
	}
	return link + "#prompt-" + url.PathEscape(promptID)
}

// Markdown is the link text for comments: a link to the Desk, or plain text
// when there is no URL.
func Markdown(u string) string {
	if u == "" {
		return "in the Desk"
	}
	return "[Answer in the Desk](" + u + ")"
}

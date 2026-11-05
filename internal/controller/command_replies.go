package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// No silent ignore: an "/agent" or "/pilot" comment the operator does not
// act on gets one reply naming the reason. This covers the issues the
// operator watches without an agent label: the open issues with the
// generated label (ci::* ones too) and the queue of autopilot milestones.
// Every full sync scans them, and a webhook delivery starts one.

// syncCommandReplies scans the watched issues updated since the last scan
// and answers their commands. started is the poll's start, the next scan's
// window. Errors are logged; a transient one keeps the window, so the next
// poll tries again.
func (r *AgentProjectReconciler) syncCommandReplies(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	autopilots map[int64]*aiv1.AgentTask, started time.Time) {
	if !spec.Workflow.Takes(aiv1.InputGitLab) {
		return
	}
	cr := project.Status.CommandReplies
	if cr == nil || cr.Since == nil {
		// Start watching now; comments written before are history.
		project.Status.CommandReplies = &aiv1.CommandRepliesStatus{Since: &metav1.Time{Time: started}}
		return
	}
	logger := log.FromContext(ctx)
	after := cr.Since.Add(-revisitClockSkew)
	keep := false
	failed := func(what string, err error) {
		logger.Info("could not answer the commands on unlabelled issues", "step", what, "error", err.Error())
		keep = keep || gitlab.IsTransient(err)
	}
	watched := map[int64]gitlab.Issue{}
	list, err := gl.ListIssues(ctx, spec.GitLab.Project, gitlab.ListIssuesOptions{Labels: []string{spec.Workflow.GeneratedLabel}, UpdatedAfter: &after})
	if err != nil {
		failed("listing the generated issues", err)
	}
	for _, is := range list {
		watched[is.IID] = is
	}
	for _, st := range autopilots {
		if autopilotDone(st) || st.Spec.Milestone == nil {
			continue
		}
		list, err := gl.ListIssues(ctx, spec.GitLab.Project, gitlab.ListIssuesOptions{Milestone: st.Spec.Milestone.Title, UpdatedAfter: &after})
		if err != nil {
			failed("listing the issues of milestone "+st.Spec.Milestone.Title, err)
			continue
		}
		for _, is := range list {
			if onAutopilot(st, is.IID) {
				watched[is.IID] = is
			}
		}
		if ap := st.Status.Autopilot; ap != nil {
			delete(watched, ap.TrackingIssueIID)
		}
	}
	r.replyUnhandled(ctx, gl, project, spec, watched, failed)
	if !keep {
		project.Status.CommandReplies.Since = &metav1.Time{Time: started}
	}
}

// replyUnhandled answers the commands on the watched issues without an
// agent label that are newer than the window and not answered yet.
func (r *AgentProjectReconciler) replyUnhandled(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	watched map[int64]gitlab.Issue, failed func(string, error)) {
	cr := project.Status.CommandReplies
	after := cr.Since.Add(-revisitClockSkew)
	bot := project.Status.BotUserID
	mention := ""
	if *spec.Pilot.Mention {
		mention = project.Status.BotUsername
	}
	wf := spec.Workflow
	iids := make([]int64, 0, len(watched))
	for iid := range watched {
		iids = append(iids, iid)
	}
	slices.Sort(iids)
	for _, iid := range iids {
		is := watched[iid]
		// Tracking issues take their own commands.
		if hasAny(is, wf.ActiveLabels()...) || is.Author.ID == bot && marker.IsTrackingIssue(is.Description) {
			continue
		}
		notes, err := gl.ListIssueNotes(ctx, spec.GitLab.Project, iid)
		if err != nil {
			failed(fmt.Sprintf("listing the comments of #%d", iid), err)
			continue
		}
		for _, n := range notes {
			if n.System || n.Author.ID == bot || n.CreatedAt == nil || !n.CreatedAt.After(after) || slices.Contains(cr.Replied, n.ID) {
				continue
			}
			_, pilot := command.Pilot(n.Body, wf.PilotPrefix, mention)
			if !pilot && !IsCommand(n, bot, wf.CommandPrefix) {
				continue
			}
			text := fmt.Sprintf("🤖 Nothing happens yet: #%d has no `%s` label. Add it to hand #%d to the agent.", iid, wf.ReadyLabel, iid)
			if command.IsRoutingOnly(n.Body, wf.CommandPrefix) {
				text = fmt.Sprintf("🤖 Nothing happens yet: #%d has no `%s` label. This is applied once it has.", iid, wf.ReadyLabel)
			}
			if err := replyOnce(ctx, promptNote{gl: gl, project: spec.GitLab.Project, iid: iid}, bot, n.ID, text); err != nil {
				failed(fmt.Sprintf("replying on #%d", iid), err)
				if gitlab.IsTransient(err) {
					break // the window stays: tried again
				}
				// Refused for good (a locked issue): not tried again.
			}
			cr.Replied = append(cr.Replied, n.ID)
			if len(cr.Replied) > aiv1.MaxCommandReplies {
				cr.Replied = slices.Clone(cr.Replied[len(cr.Replied)-aiv1.MaxCommandReplies:])
			}
		}
	}
}

// replyToMarker ends the bot's reply to a comment.
func replyToMarker(noteID int64) string {
	return fmt.Sprintf("<!-- ai-operator:reply-to=%d -->", noteID)
}

// replyOnce answers the comment noteID with text in its thread, unless the
// bot answered it already: its reply carries the comment's marker, which
// survives a lost status.
func replyOnce(ctx context.Context, to promptNote, bot, noteID int64, text string) error {
	m := replyToMarker(noteID)
	return to.reply(ctx, bot, noteID, text+"\n\n"+m, m)
}

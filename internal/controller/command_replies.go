package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
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
	cr := repliesStatus(project)
	if cr.Since == nil {
		// Start watching now; comments written before are history.
		cr.Since = &metav1.Time{Time: started}
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
	r.replyUnhandled(ctx, gl, project, spec, watched, autopilots, failed)
	if !keep {
		project.Status.CommandReplies.Since = &metav1.Time{Time: started}
	}
}

// replyUnhandled answers the commands on the watched issues without an
// agent label that are newer than the window and not answered yet;
// autopilots are the spec tasks of the autopilot milestones, by IID.
func (r *AgentProjectReconciler) replyUnhandled(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	watched map[int64]gitlab.Issue, autopilots map[int64]*aiv1.AgentTask, failed func(string, error)) {
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
			switch {
			case !wf.Takes(aiv1.InputGitLab):
				var st *aiv1.AgentTask
				if is.Milestone != nil {
					st = autopilots[is.Milestone.IID]
				}
				text = toDesk(r.deskLink(spec, iid, trackingIssue(st, nil), nil, st)) +
					fmt.Sprintf("\n\nTo hand #%d to the agent, add the `%s` label.", iid, wf.ReadyLabel)
			case command.IsRoutingOnly(n.Body, wf.CommandPrefix):
				text = fmt.Sprintf("🤖 Nothing happens yet: #%d has no `%s` label. This is applied once it has.", iid, wf.ReadyLabel)
			}
			if !r.mayReply(ctx, project, n) {
				// Over the cap: logged, and not tried again.
			} else if posted, err := replyOnce(ctx, promptNote{gl: gl, project: spec.GitLab.Project, iid: iid}, bot, n.ID, text); err != nil {
				failed(fmt.Sprintf("replying on #%d", iid), err)
				if gitlab.IsTransient(err) {
					break // the window stays: tried again
				}
				// Refused for good (a locked issue): not tried again.
			} else if posted {
				r.countReply(project, n.Author.ID)
			}
			cr.Replied = append(cr.Replied, n.ID)
			if len(cr.Replied) > aiv1.MaxCommandReplies {
				cr.Replied = slices.Clone(cr.Replied[len(cr.Replied)-aiv1.MaxCommandReplies:])
			}
		}
	}
}

// toDesk is the reply to an "/agent" or "/pilot" comment of a project that
// takes no GitLab input (workflow.inputs): link is where to go instead.
func toDesk(link string) string {
	if link == "" {
		return "🤖 Nothing happens: this project takes instructions in the Desk, not in comments (`workflow.inputs`)."
	}
	return "🤖 Nothing happens: this project takes instructions in the Desk: " + link
}

// deskLink is where a comment on issue iid that is not acted on sends its
// author: the newest open prompt posted on the issue, else the Desk page of
// its Implement task, else that of its milestone (st). tracking is the
// milestone's tracking issue. "": no Desk URL, or neither task.
func (r *AgentProjectReconciler) deskLink(spec aiv1.AgentProjectSpec, iid, tracking int64, task, st *aiv1.AgentTask) string {
	for _, h := range []*aiv1.AgentTask{task, st} {
		if h == nil {
			continue
		}
		for _, p := range slices.Backward(h.Status.Prompts) {
			if n := promptNoteOf(nil, spec, h, p, tracking); !p.State.Closed() && n.iid == iid && !n.mr {
				return desklink.Prompt(r.DeskURL, h, p.ID)
			}
		}
	}
	switch {
	case task != nil:
		return desklink.Task(r.DeskURL, task.Namespace, task.Spec.ProjectRef, task.Name)
	case st != nil && st.Spec.Milestone != nil:
		return desklink.Milestone(r.DeskURL, st.Namespace, st.Spec.ProjectRef, st.Spec.Milestone.IID)
	}
	return ""
}

// replyToMarker ends the bot's reply to a comment.
func replyToMarker(noteID int64) string {
	return fmt.Sprintf("<!-- ai-operator:reply-to=%d -->", noteID)
}

// replyOnce answers the comment noteID with text in its thread, unless the
// bot answered it already: its reply carries the comment's marker, which
// survives a lost status.
func replyOnce(ctx context.Context, to promptNote, bot, noteID int64, text string) (posted bool, err error) {
	m := replyToMarker(noteID)
	return to.reply(ctx, bot, noteID, text+"\n\n"+m, m)
}

// maxRepliesPerDay caps the replies to one author's comments a day. The
// comments beyond it are logged, not answered: the only commands the
// operator ignores silently, so a spammer cannot make the bot spam.
const maxRepliesPerDay = 10

// repliesStatus is project's reply bookkeeping, created when missing.
func repliesStatus(project *aiv1.AgentProject) *aiv1.CommandRepliesStatus {
	if project.Status.CommandReplies == nil {
		project.Status.CommandReplies = &aiv1.CommandRepliesStatus{}
	}
	return project.Status.CommandReplies
}

// mayReply reports whether n's author is under the day's cap of replies,
// and logs n if not.
func (r *AgentProjectReconciler) mayReply(ctx context.Context, project *aiv1.AgentProject, n gitlab.Note) bool {
	c, ok := repliesStatus(project).Authors[strconv.FormatInt(n.Author.ID, 10)]
	if !ok || r.now().Sub(c.Since.Time) >= rejectEvery || c.Count < maxRepliesPerDay {
		return true
	}
	log.FromContext(ctx).Info("not answering a comment: its author had the day's replies",
		"note", n.ID, "author", n.Author.Username, "replies", c.Count)
	return false
}

// countReply counts a reply to author's comment, and forgets the counts of
// past days.
func (r *AgentProjectReconciler) countReply(project *aiv1.AgentProject, author int64) {
	cr, now := repliesStatus(project), r.now()
	for k, c := range cr.Authors {
		if now.Sub(c.Since.Time) >= rejectEvery {
			delete(cr.Authors, k)
		}
	}
	if cr.Authors == nil {
		cr.Authors = map[string]aiv1.CommandReplyCount{}
	}
	key := strconv.FormatInt(author, 10)
	c, ok := cr.Authors[key]
	if !ok {
		c.Since = metav1.NewTime(now)
	}
	c.Count++
	cr.Authors[key] = c
}

// replyPerNote answers the comment n with body in its thread, once, up to
// the cap of its author's replies a day.
func (r *AgentProjectReconciler) replyPerNote(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	n pilotNote, body string) error {
	if !r.mayReply(ctx, project, n.Note) {
		return nil
	}
	to := promptNote{gl: gl, project: spec.GitLab.Project, iid: n.issue}
	if n.mr != 0 {
		to.iid, to.mr = n.mr, true
	}
	posted, err := replyOnce(ctx, to, project.Status.BotUserID, n.ID, body)
	if posted {
		r.countReply(project, n.Author.ID)
	}
	return err
}

// rejectNote is replyPerNote for a comment that is not taken: a failed
// reply is logged, not tried again.
func (r *AgentProjectReconciler) rejectNote(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	n pilotNote, body string) {
	if err := r.replyPerNote(ctx, gl, project, spec, n, body); err != nil {
		log.FromContext(ctx).Info("could not answer a comment", "note", n.ID, "error", err.Error())
	}
}

package controller

import (
	"context"
	"errors"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/writefail"
)

// The pilot of a milestone is its Coordinate task. The operator reads the
// comments addressed to it ("/pilot …" or a mention of the bot at the
// start) on the tracking issue and on the issues and merge requests of the
// milestone's tasks, lets only the owners through and hands their messages
// to the task, whose runner answers them (internal/runner/pilot.go).

// pilotNote is a comment addressed to the pilot, and where it was written.
type pilotNote struct {
	gitlab.Note
	msg       string
	issue, mr int64
}

// rejectEvery is the day maxRepliesPerDay counts in.
const rejectEvery = 24 * time.Hour

// maxPilotMessage bounds a message handed to the pilot.
const maxPilotMessage = 4000

// pilotNotes picks the comments addressed to the pilot that are newer than
// since. The bot's own comments and system notes never are.
func pilotNotes(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, notes []gitlab.Note, since, issue, mr int64) []pilotNote {
	bot := ""
	if *spec.Pilot.Mention {
		bot = project.Status.BotUsername
	}
	var out []pilotNote
	for _, n := range notes {
		if n.ID <= since || n.System || n.Author.ID == project.Status.BotUserID {
			continue
		}
		if msg, ok := command.Pilot(n.Body, spec.Workflow.PilotPrefix, bot); ok && msg != "" {
			out = append(out, pilotNote{n, msg, issue, mr})
		}
	}
	return out
}

func newestNote(notes ...[]gitlab.Note) int64 {
	var id int64
	for _, list := range notes {
		for _, n := range list {
			id = max(id, n.ID)
		}
	}
	return id
}

// pilotIssueNotes hands the comments to the pilot that were written on an
// issue with a task or its merge request since the last poll that read
// them. Comments from before the task (or this feature) are history. A
// failure is logged and tried again the next time the notes are read. A
// project without GitLab input hands nothing over: it returns the comments,
// to be answered with the Desk.
func (r *AgentProjectReconciler) pilotIssueNotes(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	is gitlab.Issue, mr *gitlab.MergeRequest, task *aiv1.AgentTask, issueNotes, mrNotes []gitlab.Note) []pilotNote {
	src := &task.Spec.Source
	since := src.LastPilotNoteID
	src.LastPilotNoteID = max(since, newestNote(issueNotes, mrNotes), 1)
	if since == 0 {
		return nil
	}
	notes := pilotNotes(project, spec, issueNotes, since, is.IID, 0)
	if mr != nil {
		notes = append(notes, pilotNotes(project, spec, mrNotes, since, 0, mr.IID)...)
	}
	if !spec.Workflow.Takes(aiv1.InputGitLab) {
		return notes
	}
	// Option letters in the threads of prompts on the issue.
	letters := len(letterReplies(issueNotes, since, project.Status.BotUserID)) > 0
	if len(notes) == 0 && !letters {
		return nil
	}
	st, coord, err := r.milestoneTasks(ctx, project, is.Milestone)
	if err == nil && letters {
		err = r.promptThreadAnswers(ctx, gl, project, spec, st, coord, is.IID, trackingIssue(st, coord), since, issueNotes)
	}
	if err == nil && len(notes) > 0 {
		err = r.routePilot(ctx, gl, project, spec, st, coord, task.Spec.Owners, notes)
	}
	if err != nil {
		log.FromContext(ctx).Info("could not hand comments to the pilot", "issue", is.IID, "error", err.Error())
		src.LastPilotNoteID = since
	}
	return nil
}

// pilot hands the comments on the tracking issue of an autopilot milestone
// to its pilot.
func (a *autopilot) pilot(ctx context.Context, notes []gitlab.Note) {
	_, coord, err := a.r.milestoneTasks(ctx, a.project, &gitlab.Milestone{IID: a.st.Spec.Milestone.IID})
	if err == nil {
		err = a.r.pilotTracking(ctx, a.gl, a.project, a.spec, a.st, coord, a.ap.TrackingIssueIID, notes)
	}
	if err != nil {
		a.log.Info("could not hand comments to the pilot", "error", err.Error())
	}
}

// pilotTracking hands the comments on a milestone's tracking issue to its
// pilot. Comments from before the pilot listened there are history. A
// project without GitLab input answers them with the Desk, and reads no
// answers in threads.
func (r *AgentProjectReconciler) pilotTracking(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, tracking int64, notes []gitlab.Note) error {
	var since int64
	if st.Status.Pilot != nil {
		since = st.Status.Pilot.LastNoteID
	}
	switch {
	case since == 0:
	case !spec.Workflow.Takes(aiv1.InputGitLab):
		if ns := pilotNotes(project, spec, notes, since, tracking, 0); len(ns) > 0 {
			link := r.deskLink(spec, tracking, tracking, nil, st)
			for _, n := range ns {
				r.rejectNote(ctx, gl, project, spec, n, toDesk(link))
			}
		}
	default:
		if coord == nil && trackingIssue(st, nil) != 0 {
			return nil // created at the end of this poll
		}
		if err := r.routePilot(ctx, gl, project, spec, st, coord, st.Spec.Owners, pilotNotes(project, spec, notes, since, tracking, 0)); err != nil {
			return err
		}
		if err := r.threadAnswers(ctx, gl, project, spec, st, coord, tracking, since, notes); err != nil {
			return err
		}
		if err := r.promptThreadAnswers(ctx, gl, project, spec, st, coord, tracking, tracking, since, notes); err != nil {
			return err
		}
	}
	latest := max(since, newestNote(notes), 1)
	if latest == since {
		return nil
	}
	return r.patchPilot(ctx, st, func(ps *aiv1.PilotStatus) { ps.LastNoteID = latest })
}

// milestoneTasks returns the spec task and the Coordinate task of a
// milestone, nil where there is none.
func (r *AgentProjectReconciler) milestoneTasks(ctx context.Context, project *aiv1.AgentProject, m *gitlab.Milestone) (st, coord *aiv1.AgentTask, err error) {
	if m == nil {
		return nil, nil, nil
	}
	var list aiv1.AgentTaskList
	if err := r.List(ctx, &list, client.InNamespace(project.Namespace),
		client.MatchingLabels{LabelProject: project.Name, LabelMilestoneIID: strconv.FormatInt(m.IID, 10)}); err != nil {
		return nil, nil, err
	}
	for i := range list.Items {
		switch t := &list.Items[i]; t.Spec.Type {
		case aiv1.TaskTypeSpec:
			st = t
		case aiv1.TaskTypeCoordinate:
			coord = t
		}
	}
	return st, coord, nil
}

// routePilot hands the owners' messages to the pilot and answers the others
// itself: no planned milestone, no owner, no pilot yet, or "/pilot
// coordinate", which starts one. Others get one reply per comment.
func (r *AgentProjectReconciler) routePilot(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, owners []string, notes []pilotNote) error {
	prefix := spec.Workflow.PilotPrefix
	var ids map[int64]bool
	var msgs []aiv1.PilotMessage
	for _, n := range notes {
		if ids == nil {
			var err error
			if ids, err = ownerIDs(ctx, gl, owners); err != nil {
				return err
			}
		}
		owner := ids[n.Author.ID]
		coordinate := strings.EqualFold(n.msg, "coordinate")
		yes, id, answer := command.PilotAnswer(n.msg)
		iid, text, tell := command.PilotTell(n.msg)
		stop, goOn := strings.EqualFold(n.msg, "stop"), strings.EqualFold(n.msg, "go")
		list, forgetID := command.PilotRules(n.msg)
		forget := forgetID != ""
		var reply string
		var err error
		done := autopilotDone(st)
		switch tracking := trackingIssue(st, coord); {
		case st == nil:
			reply = "There is no pilot here: this issue is not in a planned milestone."
		case done:
			reply = "There is no pilot here: the autopilot of this milestone is done."
		// A milestone without owners gets answers only, which is all the
		// pilot does yet.
		case !owner && len(owners) > 0:
			reply = "Only the owners of this milestone can instruct me."
		case coordinate && tracking != 0:
			reply = fmt.Sprintf("The pilot of this milestone listens on #%d.", tracking)
		case coordinate && st.Spec.Autopilot:
			reply = "The pilot of this milestone starts with the autopilot's tracking issue."
		case coordinate && len(owners) == 0:
			reply = fmt.Sprintf("A pilot needs owners: add a line `%s owners @you` to the milestone description.", spec.Workflow.CommandPrefix)
		case coordinate:
			if coord, err = r.startPilot(ctx, gl, project, spec, st); err != nil {
				return err
			}
			reply = fmt.Sprintf("The pilot of this milestone listens on #%d now: comment `%s …` there or here.", coord.Spec.Issue.IID, prefix)
		case coord == nil:
			reply = fmt.Sprintf("This milestone has no pilot yet: comment `%s coordinate` to start one.", prefix)
		case (answer || tell || stop || goOn || forget) && !owner:
			reply = "This milestone has no owners, so the pilot takes no actions."
		case stop || goOn:
			reply, err = r.switchAutonomy(ctx, spec, coord, n.Author.Username, stop)
		case answer:
			reply, err = r.answerProposal(ctx, project, spec, st, coord, n.Author.Username, n.Author.ID, n.issue, id, yes, false)
		case tell:
			reply, err = r.proposeTell(ctx, gl, project, spec, st, coord, n, iid, text)
		case list:
			reply = listRules(spec, st)
		case forget:
			reply, err = r.proposeForget(ctx, gl, project, spec, st, coord, forgetID)
		case slices.ContainsFunc(coord.Spec.Source.PilotMessages, func(m aiv1.PilotMessage) bool { return m.NoteID == n.ID }):
			continue
		default:
			msgs = append(msgs, aiv1.PilotMessage{NoteID: n.ID, IssueIID: n.issue, MergeRequestIID: n.mr, Author: n.Author.Username,
				Body: textutil.Truncate(n.msg, maxPilotMessage)})
			continue
		}
		if err != nil {
			return err
		}
		switch {
		case reply == "":
		case owner:
			pilotReply(ctx, gl, spec, n, reply)
		default:
			// Once per comment, capped: others can comment a lot.
			r.rejectNote(ctx, gl, project, spec, n, "🧭 "+reply)
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	// Answered messages go; the new ones are answered by the next run.
	coord.Spec.RunRequest++
	for i := range msgs {
		msgs[i].Request = coord.Spec.RunRequest
	}
	pending := slices.DeleteFunc(coord.Spec.Source.PilotMessages, func(m aiv1.PilotMessage) bool { return m.Request <= coord.Status.Run.ObservedRunRequest })
	coord.Spec.Source.PilotMessages = append(pending, msgs...)
	log.FromContext(ctx).Info("messages for the pilot", "task", coord.Name, "messages", len(msgs))
	return r.Update(ctx, coord)
}

// trackingIssue is the issue the pilot of a milestone listens on: the
// autopilot's tracking issue, else the one "/pilot coordinate" opened.
// A milestone whose autopilot is done has no pilot.
func trackingIssue(st, coord *aiv1.AgentTask) int64 {
	if st == nil || autopilotDone(st) {
		return 0
	}
	if ap := st.Status.Autopilot; ap != nil && ap.Phase != aiv1.AutopilotOff && ap.TrackingIssueIID != 0 {
		return ap.TrackingIssueIID
	}
	if coord != nil && !coord.Spec.Autopilot && coord.Spec.Issue != nil {
		return coord.Spec.Issue.IID
	}
	return 0
}

func autopilotDone(st *aiv1.AgentTask) bool {
	return st != nil && st.Status.Autopilot != nil && st.Status.Autopilot.Phase == aiv1.AutopilotDone
}

// startPilot opens a tracking issue for a milestone without autopilot and
// creates its pilot.
func (r *AgentProjectReconciler) startPilot(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask) (*aiv1.AgentTask, error) {
	m := st.Spec.Milestone
	is, err := gl.CreateIssue(ctx, spec.GitLab.Project, gitlab.CreateIssueOptions{
		Title:       "🧭 Pilot: " + m.Title,
		Description: pilotIssueBody(spec, st),
		Labels:      spec.Workflow.GeneratedLabel,
		MilestoneID: m.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("creating the pilot's tracking issue: %w", err)
	}
	// The new issue has no comments yet.
	if err := r.patchPilot(ctx, st, func(ps *aiv1.PilotStatus) { ps.LastNoteID = 1 }); err != nil {
		return nil, err
	}
	return r.ensurePilot(ctx, project, st, nil, &aiv1.IssueRef{ID: is.ID, IID: is.IID, Title: is.Title, WebURL: is.WebURL}, false)
}

// ensurePilot creates or updates the Coordinate task of a milestone.
func (r *AgentProjectReconciler) ensurePilot(ctx context.Context, project *aiv1.AgentProject, st, coord *aiv1.AgentTask, issue *aiv1.IssueRef, autopilot bool) (*aiv1.AgentTask, error) {
	m := st.Spec.Milestone
	if coord == nil {
		var err error
		if coord, err = r.newTask(project, naming.CoordinateTask(project.Name, m.IID, m.Title), aiv1.TaskTypeCoordinate); err != nil {
			return nil, err
		}
		coord.Labels[LabelMilestoneIID] = strconv.FormatInt(m.IID, 10)
		coord.Spec.Milestone, coord.Spec.Issue, coord.Spec.Autopilot = m, issue, autopilot
		coord.Spec.Owners, coord.Spec.OwnerSubjects, coord.Spec.Rules = st.Spec.Owners, st.Spec.OwnerSubjects, st.Spec.Rules
		log.FromContext(ctx).Info("starting the pilot of a milestone", "milestone", m.Title, "task", coord.Name, "issue", issue.IID)
		return coord, r.Create(ctx, coord)
	}
	set := func(c *aiv1.AgentTask) {
		c.Spec.Milestone, c.Spec.Autopilot = m, autopilot
		c.Spec.Owners, c.Spec.OwnerSubjects, c.Spec.Rules = st.Spec.Owners, st.Spec.OwnerSubjects, st.Spec.Rules
		if issue != nil {
			c.Spec.Issue = issue
		}
	}
	before := coord.Spec.DeepCopy()
	if set(coord); equalSpec(before, &coord.Spec) {
		return coord, nil
	}
	// On a fresh copy: an earlier step of the poll may have changed the
	// task (a message, a /pilot stop), e.g. when the rules change with it.
	return coord, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, client.ObjectKeyFromObject(coord), coord); err != nil {
			return err
		}
		set(coord)
		return r.Update(ctx, coord)
	})
}

// syncPilots keeps the pilot of every planned milestone that has a tracking
// issue, reads the tracking issues "/pilot coordinate" opened (the
// autopilot's are read by autopilot) and wakes the pilots for the events of
// their milestones, those of the default branch's health only for lane's.
// Pilots left out of keep are deleted.
func (r *AgentProjectReconciler) syncPilots(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	specTasks []*aiv1.AgentTask, lane *aiv1.AgentTask, pilots map[int64]*aiv1.AgentTask, keep map[string]bool, issues map[int64]gitlab.Issue, tasks map[int64]*aiv1.AgentTask) error {
	var errs []error
	for _, st := range specTasks {
		coord := pilots[st.Spec.Milestone.IID]
		tracking := trackingIssue(st, coord)
		if tracking == 0 {
			// No pilot, or its autopilot is done: a pilot left is deleted
			// below, after its retrospective.
			if coord != nil && coord.Spec.Autopilot && autopilotDone(st) {
				wait, err := r.retrospective(ctx, st, coord)
				if wait {
					keep[coord.Name] = true
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("retrospective of milestone %q: %w", st.Spec.Milestone.Title, err))
				}
			}
			continue
		}
		if coord != nil {
			keep[coord.Name] = true
		}
		var err error
		if coord == nil || coord.Spec.Autopilot || coord.Spec.Issue == nil || coord.Spec.Issue.IID != tracking {
			if coord != nil && !coord.Spec.Autopilot && coord.Spec.Issue != nil {
				// Autopilot took over: the issue /pilot coordinate opened goes.
				err = closePilotIssue(ctx, gl, spec, coord.Spec.Issue.IID, tracking)
			}
			// Follows the autopilot's tracking issue, also a recreated one.
			if err == nil {
				coord, err = r.ensurePilot(ctx, project, st, coord, &aiv1.IssueRef{IID: tracking}, true)
			}
		} else if _, err = r.ensurePilot(ctx, project, st, coord, nil, false); err == nil {
			var notes []gitlab.Note
			var is *gitlab.Issue
			is, err = gl.GetIssue(ctx, spec.GitLab.Project, tracking)
			if err == nil && is.State == "opened" {
				if err = syncPilotIssue(ctx, gl, spec, st, is); err == nil {
					notes, err = gl.ListIssueNotes(ctx, spec.GitLab.Project, tracking)
				}
			}
			switch {
			case gitlab.IsNotFound(err) || err == nil && is.State != "opened":
				delete(keep, coord.Name) // the tracking issue is closed or gone, and the pilot with it
				err = nil
			case err == nil:
				err = r.pilotTracking(ctx, gl, project, spec, st, coord, tracking, notes)
			}
		}
		// A pilot created in this poll hears of the events from the next.
		if err == nil && coord != nil && keep[coord.Name] {
			// Desk accept/reject before runAccepted, like GitLab yes/no in
			// pilotTracking above.
			if err = r.applyPilotChatCommands(ctx, gl, project, spec, st, coord); err == nil {
				err = r.wakePilot(ctx, coord, pilotEvents(spec, project.Status.MainHealth, st, issues, tasks, st == lane, r.now()))
			}
			if err == nil {
				_, err = r.postProposals(ctx, gl, project, spec, st, coord, tracking)
			}
			if err == nil {
				err = r.consumeRefined(ctx, gl, project, spec, st, coord)
			}
			if err == nil {
				err = r.runAccepted(ctx, gl, project, spec, st, coord, tracking)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("pilot of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
	}
	return errors.Join(errs...)
}

// retrospective has the pilot of a finished autopilot milestone write the
// milestone's retrospective, once, and reports whether to keep the pilot:
// until its run for it is over and the result is on the spec task, where it
// outlives the pilot. A run that ended without one counts as failed and is
// not tried again.
func (r *AgentProjectReconciler) retrospective(ctx context.Context, st, coord *aiv1.AgentTask) (bool, error) {
	if st.Status.Pilot != nil && st.Status.Pilot.Retrospective != nil {
		return false, nil
	}
	request := coord.Spec.Source.Retrospective
	if request == 0 {
		coord.Spec.RunRequest++
		coord.Spec.Source.Retrospective = coord.Spec.RunRequest
		coord.Spec.Source.RetroSpend = r.retroSpend(ctx, coord)
		log.FromContext(ctx).Info("asking the pilot for the retrospective", "task", coord.Name, "request", coord.Spec.RunRequest,
			"spendFromDB", coord.Spec.Source.RetroSpend != nil)
		return true, r.Update(ctx, coord)
	}
	var retro *aiv1.PilotRetrospective
	if ps := coord.Status.Pilot; ps != nil && ps.Retrospective != nil {
		retro = ps.Retrospective.DeepCopy()
	}
	if retro == nil {
		if coord.Status.Run.ObservedRunRequest < request {
			return true, nil // the run is not over
		}
		retro = &aiv1.PilotRetrospective{Failed: textutil.Truncate("the run ended without one: "+coord.Status.Run.Summary, 500)}
	}
	if err := r.patchPilot(ctx, st, func(ps *aiv1.PilotStatus) { ps.Retrospective = retro }); err != nil {
		return true, err
	}
	return false, nil
}

// closePilotIssue closes the tracking issue "/pilot coordinate" opened,
// once the pilot listens on the autopilot's.
func closePilotIssue(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, iid, tracking int64) error {
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, iid, fmt.Sprintf("🧭 The pilot moved to #%d, the autopilot's tracking issue.", tracking)); err != nil && !gitlab.IsNotFound(err) {
		return fmt.Errorf("commenting on the pilot's tracking issue #%d: %w", iid, err)
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, iid, gitlab.UpdateIssueOptions{StateEvent: "close"}); err != nil && !gitlab.IsNotFound(err) {
		return fmt.Errorf("closing the pilot's tracking issue #%d: %w", iid, err)
	}
	return nil
}

// patchPilot changes the pilot status of a spec or Coordinate task.
func (r *AgentProjectReconciler) patchPilot(ctx context.Context, st *aiv1.AgentTask, mutate func(*aiv1.PilotStatus)) error {
	orig := st.DeepCopy()
	if st.Status.Pilot == nil {
		st.Status.Pilot = &aiv1.PilotStatus{}
	}
	mutate(st.Status.Pilot)
	return r.writeFailed(ctx, st, "patchPilot", r.Status().Patch(ctx, st, client.MergeFrom(orig)))
}

// writeFailed reports a status write of the operator that the API server
// rejected (writefail) and returns err.
func (r *AgentProjectReconciler) writeFailed(ctx context.Context, task *aiv1.AgentTask, source string, err error) error {
	if !writefail.Rejected(err) {
		return err
	}
	var project aiv1.AgentProject
	var gl gitlab.API
	gerr := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.ProjectRef}, &project)
	spec := project.Spec.WithDefaults()
	if gerr == nil {
		gl, gerr = r.gitlabClient(ctx, &project, spec)
	}
	if gerr != nil {
		log.FromContext(ctx).Error(gerr, "no GitLab client for the rejected status write", "source", source)
	}
	if rerr := writefail.Report(ctx, r.Client, gl, spec.GitLab.Project, task, writefail.Tracking(task), "operator/"+source, err); rerr != nil {
		log.FromContext(ctx).Error(rerr, "reporting a rejected status write", "source", source)
	}
	return err
}

// pilotReply answers a comment where it was written. A failed answer is not
// tried again: the comment is handled.
func pilotReply(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, n pilotNote, text string) {
	noteReply(ctx, gl, spec, n, "🧭 "+text)
}

// noteReply is pilotReply without the pilot's sign.
func noteReply(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, n pilotNote, body string) {
	var err error
	if n.mr != 0 {
		_, err = gl.CreateMergeRequestNote(ctx, spec.GitLab.Project, n.mr, body)
	} else {
		_, err = gl.CreateIssueNote(ctx, spec.GitLab.Project, n.issue, body)
	}
	if err != nil {
		log.FromContext(ctx).Info("could not answer a comment", "note", n.ID, "error", err.Error())
	}
}

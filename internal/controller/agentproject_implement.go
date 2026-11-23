package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// syncImplementTask makes sure an issue handed to the agent has a task and
// turns "moved to ready" and "/agent" comments into run requests. It returns
// the task's merge request too, if it has one. st is the spec task of the
// autopilot whose queue has the issue (nil: none): a command's run of the
// issue it is paused for is recorded there (commandRun).
func (r *AgentProjectReconciler) syncImplementTask(ctx context.Context, gl gitlab.API, gate *commandGate, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	is gitlab.Issue, task *aiv1.AgentTask, autopilot bool, pause string, budgetStop, urgent bool, ms issueMilestone, owners, ownerSubjects []string, st *aiv1.AgentTask) (*aiv1.AgentTask, *gitlab.MergeRequest, error) {
	wf := spec.Workflow
	glProject := spec.GitLab.Project
	created := false
	if task == nil {
		var err error
		task, err = r.newTask(project, naming.ImplementTask(project.Name, is.IID, is.Title), aiv1.TaskTypeImplement)
		if err != nil {
			return nil, nil, err
		}
		task.Labels[LabelIssueIID] = strconv.FormatInt(is.IID, 10)
		created = true
	}
	before := task.Spec.DeepCopy()
	task.Spec.Issue = &aiv1.IssueRef{ID: is.ID, IID: is.IID, Title: is.Title, WebURL: is.WebURL}
	if is.Milestone != nil {
		task.Spec.Milestone = &aiv1.MilestoneRef{ID: is.Milestone.ID, IID: is.Milestone.IID, Title: is.Milestone.Title}
	}
	task.Spec.Autopilot = autopilot
	task.Spec.Owners, task.Spec.OwnerSubjects = owners, ownerSubjects
	task.Spec.Source.AutopilotPause = pause
	task.Spec.BudgetStop = budgetStop
	// The main-red fix goes ahead in the run queue; the lane is cleared on
	// the poll after the fix is over.
	task.Spec.QueueLane = ""
	if urgent {
		task.Spec.QueueLane = aiv1.QueueLaneUrgent
	}

	ready := is.HasLabel(wf.ReadyLabel)
	src := &task.Spec.Source

	// Only look at comments when something changed since the last poll.
	issueChanged := is.UpdatedAt == nil || src.IssueUpdatedAt == nil || is.UpdatedAt.After(src.IssueUpdatedAt.Time)
	var mr *gitlab.MergeRequest
	if iid := task.Status.Run.MergeRequestIID; iid != 0 {
		var err error
		if mr, err = gl.GetMergeRequest(ctx, glProject, iid); err != nil && !gitlab.IsNotFound(err) {
			return nil, nil, fmt.Errorf("getting merge request !%d: %w", iid, err)
		}
	}
	mrChanged := mr != nil && (mr.UpdatedAt == nil || src.MergeRequestUpdatedAt == nil || mr.UpdatedAt.After(src.MergeRequestUpdatedAt.Time))

	command, review := false, false
	commandBy := "" // the author of the newest command, "@name"
	var reply *routingReply
	var denied []pilotNote
	if ready || issueChanged || mrChanged {
		var latest int64
		var issueNotes, mrNotes []gitlab.Note
		var err error
		latest, issueNotes, mrNotes, denied, err = commandNotes(ctx, gl, gate, glProject, is.IID, mr, owners)
		if err != nil {
			return nil, nil, err
		}
		// Commands that do not count request, route and review nothing.
		// Those written since the notes were last read are answered once
		// the task is saved.
		cmdIssue, cmdMR := withoutNotes(issueNotes, denied), withoutNotes(mrNotes, denied)
		denied = slices.DeleteFunc(denied, func(n pilotNote) bool { return src.LastPilotNoteID == 0 || n.ID <= src.LastPilotNoteID })
		// "/agent use" and "/agent effort" become labels at once, "/agent
		// budget" the task's budget, also the ones written before the task
		// existed; the answer follows once the task is saved.
		if reply, err = r.applyRouting(ctx, gl, spec, &is, cmdIssue, cmdMR, &task.Spec, project.Status.BotUserID); err != nil {
			return nil, nil, err
		}
		denied = append(denied, r.pilotIssueNotes(ctx, gl, project, spec, is, mr, task, issueNotes, mrNotes)...)
		if created {
			// Comments written before the task existed are context, not commands.
			src.LastCommandNoteID = latest
		} else if latest > src.LastCommandNoteID {
			review = reviewOnly(slices.Concat(cmdIssue, cmdMR), src.LastCommandNoteID, project.Status.BotUserID, wf.CommandPrefix)
			commandBy = commandAuthor(slices.Concat(cmdIssue, cmdMR), latest)
			src.LastCommandNoteID = latest
			command = true
		}
	}
	if is.UpdatedAt != nil {
		src.IssueUpdatedAt = &metav1.Time{Time: *is.UpdatedAt}
	}
	if mr != nil && mr.UpdatedAt != nil {
		src.MergeRequestUpdatedAt = &metav1.Time{Time: *mr.UpdatedAt}
	}

	// The merge request of an issue in review conflicts with the default
	// branch: the next run merges it in and the agent resolves the
	// conflicts, once per head.
	conflict := !created && *wf.ResolveConflicts && mr != nil && mr.State == "opened" && (mr.HasConflicts || mr.DetailedMergeStatus == "conflict") && mr.SHA != "" &&
		is.HasLabel(wf.ReviewLabel) && !task.RunPending() && src.MergeBaseFor != mr.SHA
	if conflict {
		log.FromContext(ctx).Info("merge request conflicts with the default branch, asking the agent to merge it", "issue", is.IID, "mr", mr.IID, "sha", mr.SHA)
		src.MergeBaseFor = mr.SHA
	}

	// "/agent review": the next run reviews the agent's pushed head again.
	// A head someone else pushed is taken over by a normal run instead.
	if review && mr != nil && mr.SHA != "" && mr.SHA == task.Status.Run.PushedSHA {
		if err := r.askReview(ctx, task, task.Status.Run.ReviewKey()); err != nil {
			return nil, nil, err
		}
	}

	// Desk chat commands (kind "command") are applied on every sync, not
	// only when the issue or MR changed: they arrive via the dispatcher.
	chatRun, err := r.applyChatCommands(ctx, gl, spec, &is, task, mr, project.Status.BotUserID)
	if err != nil {
		return nil, nil, err
	}

	// The companion repository, from the labels as the commands left them.
	// It requests no run by itself.
	companionNotice, err := r.syncCompanion(ctx, gl, gate, spec, is, task, ms, owners)
	if err != nil {
		return nil, nil, err
	}

	requested := ready || command || conflict || chatRun
	if requested {
		task.Spec.RunRequest++
		r.setChatContext(ctx, spec, task)
	}

	switch {
	case created:
		log.FromContext(ctx).Info("issue handed to agent", "issue", is.IID, "task", task.Name)
		if err := r.Create(ctx, task); err != nil {
			return nil, nil, err
		}
	case !equalSpec(before, &task.Spec):
		if err := r.Update(ctx, task); err != nil {
			return nil, nil, err
		}
	}

	if requested && command && commandBy != "" {
		// An owner's comment took the paused issue over (status.autopilot.pauseRunBy).
		r.commandRun(ctx, st, task, commandBy)
	}

	if reply != nil {
		// A failed answer is not tried again: the labels are set.
		var err error
		if reply.onMR && mr != nil {
			_, err = gl.CreateMergeRequestNote(ctx, glProject, mr.IID, reply.body)
		} else {
			_, err = gl.CreateIssueNote(ctx, glProject, is.IID, reply.body)
		}
		if err != nil {
			log.FromContext(ctx).Info("could not answer the model and effort commands", "issue", is.IID, "error", err.Error())
		}
	}
	if companionNotice != "" {
		// Not tried again: the notice is recorded (source.companionNotice).
		if _, err := gl.CreateIssueNote(ctx, glProject, is.IID, companionNotice); err != nil {
			log.FromContext(ctx).Info("could not tell the issue its companion label is ignored", "issue", is.IID, "error", err.Error())
		}
	}
	link := ""
	if gate.desk && len(denied) > 0 {
		st, _, err := r.milestoneTasks(ctx, project, is.Milestone)
		if err != nil {
			log.FromContext(ctx).Info("replying without the milestone's prompts", "issue", is.IID, "error", err.Error())
		}
		link = r.deskLink(spec, is.IID, trackingIssue(st, nil), task, st)
	}
	for _, n := range denied {
		gate.deny(ctx, n, owners, link)
	}

	// Acknowledge in GitLab only after the run request is persisted. If this
	// fails the next poll sees the ready label again and requests one more
	// run, which the runner simply coalesces.
	if requested {
		opts := gitlab.UpdateIssueOptions{
			AddLabels:    []string{wf.WorkingLabel},
			RemoveLabels: []string{wf.ReadyLabel, wf.NeedsInputLabel, wf.ReviewLabel, wf.BlockedLabel},
		}
		// Assigning the bot makes GitLab's milestone page list the issue as
		// ongoing. assignee_ids replaces all assignees, so an issue that is
		// already assigned (to a human or to the bot) is left alone.
		if *wf.AssignBot && len(is.Assignees) == 0 && project.Status.BotUserID != 0 {
			opts.AssigneeIDs = []int64{project.Status.BotUserID}
		}
		if _, err := gl.UpdateIssue(ctx, glProject, is.IID, opts); err != nil {
			return nil, nil, fmt.Errorf("moving issue #%d to %q: %w", is.IID, wf.WorkingLabel, err)
		}
	}
	return task, mr, nil
}

func equalSpec(a, b *aiv1.AgentTaskSpec) bool {
	return equality.Semantic.DeepEqual(a, b)
}

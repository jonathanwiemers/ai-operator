package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// syncAutopilot drives an autopilot milestone, one step per poll:
//
//	no active issue         -> move the next issue whose dependencies are merged to ready
//	active issue in review  -> merge gate: pipeline green, review clean, MR mergeable -> merge
//	MR needs a rebase       -> ask GitLab to rebase, then treat the new head as a push
//	merged                  -> record it, close the issue if needed, next issue
//	needs a human           -> pause with a comment on the tracking issue
//
// The operator only calls the GitLab API here; no model decides whether to
// merge. State lives in the spec task's status.autopilot, the tracking issue
// shows it.
func (r *AgentProjectReconciler) syncAutopilot(ctx context.Context, gl gitlab.API, gate *commandGate, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, pilot *aiv1.AgentTask, lane bool, ahead []int64, peers map[int64]*aiv1.AgentTask) error {
	switch {
	case st.Spec.Milestone == nil:
		return nil
	case !st.Spec.Autopilot && (st.Status.Autopilot == nil || st.Status.Autopilot.Phase == aiv1.AutopilotOff):
		// Never on, or already off: Desk verbs sent meanwhile get an answer.
		return r.refuseChatCommands(ctx, st, "The autopilot is off for this milestone.")
	}
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
	if st.Status.Autopilot != nil {
		ap = st.Status.Autopilot.DeepCopy()
	}
	a := &autopilot{r: r, gl: gl, gate: gate, project: project, spec: spec, st: st, ap: ap, tasks: tasks, pilotTask: pilot, lane: lane, ahead: ahead, peers: peers,
		log: log.FromContext(ctx).WithValues("milestone", st.Spec.Milestone.Title)}
	err := a.sync(ctx)
	// Save what was done even if a later step failed: its GitLab side
	// effects (a created tracking issue, a requested rebase) have happened.
	if serr := a.save(ctx); err == nil {
		err = serr
	}
	// Desk answers after the status: the verbs are idempotent, so a restart
	// between the two writes applies a verb again and answers once. Re-get
	// so a status patch response cannot wipe the Spec fields.
	if err == nil && a.chatMark > 0 {
		var fresh aiv1.AgentTask
		if err = r.Get(ctx, client.ObjectKeyFromObject(st), &fresh); err == nil {
			// Appended to the fresh list: chatreply may have dropped
			// answered ones meanwhile.
			fresh.Spec.Source.ChatAnswers = append(fresh.Spec.Source.ChatAnswers, a.chatAnswers...)
			fresh.Spec.Source.LastChatCommandID = a.chatMark
			if n := len(fresh.Spec.Source.ChatAnswers); n > maxChatAnswers {
				fresh.Spec.Source.ChatAnswers = append([]aiv1.ChatReply(nil), fresh.Spec.Source.ChatAnswers[n-maxChatAnswers:]...)
			}
			err = r.Update(ctx, &fresh)
			if err == nil {
				st.Spec.Source.ChatAnswers = fresh.Spec.Source.ChatAnswers
				st.Spec.Source.LastChatCommandID = fresh.Spec.Source.LastChatCommandID
			}
		}
	}
	if err == nil && !st.Spec.Autopilot {
		// Turned off by this sync: verbs that came too late are answered.
		err = r.refuseChatCommands(ctx, st, "The autopilot is off for this milestone.")
	}
	return err
}

// refuseChatCommands answers the Desk commands of task newer than its
// LastChatCommandID with text, without applying them, so they do not stay
// unanswered on a task that no longer handles them.
func (r *AgentProjectReconciler) refuseChatCommands(ctx context.Context, task *aiv1.AgentTask, text string) error {
	src := &task.Spec.Source
	var answers []aiv1.ChatReply
	var restart *corev1.Pod
	mark := src.LastChatCommandID
	for _, m := range src.ChatMessages {
		if m.Kind == "command" && m.ID > src.LastChatCommandID {
			reply := text
			// A restart works without the autopilot too, once per save.
			if cmd, err := chatcmd.Parse(m.Text); err == nil && cmd.RestartCmd() && restart == nil {
				if reply, restart, err = r.restartUntracked(ctx, task, m.Author); err != nil {
					return err
				}
			}
			answers = append(answers, aiv1.ChatReply{MessageID: m.ID, Text: reply, At: metav1.Now()})
			mark = max(mark, m.ID)
		}
	}
	if len(answers) == 0 {
		return nil
	}
	src.ChatAnswers = append(src.ChatAnswers, answers...)
	src.LastChatCommandID = mark
	if n := len(src.ChatAnswers); n > maxChatAnswers {
		src.ChatAnswers = append([]aiv1.ChatReply(nil), src.ChatAnswers[n-maxChatAnswers:]...)
	}
	if err := r.Update(ctx, task); err != nil || restart == nil {
		return err
	}
	return r.deletePod(ctx, restart)
}

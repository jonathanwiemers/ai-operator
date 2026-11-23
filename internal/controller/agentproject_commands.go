package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// commandNotes returns the ID of the newest command on the issue or its
// merge request that asks for a run (a comment that only changes the model
// or effort does not), the notes, and the commands that do not count
// (workflow.commandsFrom).
func commandNotes(ctx context.Context, gl gitlab.API, gate *commandGate, glProject string, issueIID int64, mr *gitlab.MergeRequest,
	owners []string) (int64, []gitlab.Note, []gitlab.Note, []pilotNote, error) {
	notes, err := gl.ListIssueNotes(ctx, glProject, issueIID)
	if err != nil {
		return 0, nil, nil, nil, fmt.Errorf("listing notes of issue #%d: %w", issueIID, err)
	}
	var mrNotes []gitlab.Note
	if mr != nil {
		if mrNotes, err = gl.ListMergeRequestNotes(ctx, glProject, mr.IID); err != nil {
			return 0, nil, nil, nil, fmt.Errorf("listing notes of merge request !%d: %w", mr.IID, err)
		}
	}
	prefix := gate.spec.Workflow.CommandPrefix
	var latest int64
	var denied []pilotNote
	for i, list := range [][]gitlab.Note{notes, mrNotes} {
		kept, others, err := gate.split(ctx, list, owners)
		if err != nil {
			return 0, nil, nil, nil, err
		}
		for _, n := range kept {
			if IsCommand(n, gate.botID, prefix) && !command.IsRoutingOnly(n.Body, prefix) && n.ID > latest {
				latest = n.ID
			}
		}
		for _, n := range others {
			if i == 0 {
				denied = append(denied, pilotNote{Note: n, issue: issueIID})
			} else {
				denied = append(denied, pilotNote{Note: n, mr: mr.IID})
			}
		}
	}
	return latest, notes, mrNotes, denied, nil
}

// withoutNotes returns notes without those in drop.
func withoutNotes(notes []gitlab.Note, drop []pilotNote) []gitlab.Note {
	return slices.DeleteFunc(slices.Clone(notes), func(n gitlab.Note) bool {
		return slices.ContainsFunc(drop, func(d pilotNote) bool { return d.ID == n.ID })
	})
}

// commandAuthor is the author of the note id among notes, "@name", "" if it
// is not there.
func commandAuthor(notes []gitlab.Note, id int64) string {
	for _, n := range notes {
		if n.ID == id && n.Author.Username != "" {
			return "@" + n.Author.Username
		}
	}
	return ""
}

// reviewOnly reports whether all commands newer than since are "/agent
// review".
func reviewOnly(notes []gitlab.Note, since, botID int64, prefix string) bool {
	for _, n := range notes {
		if n.ID > since && IsCommand(n, botID, prefix) && !command.IsRoutingOnly(n.Body, prefix) && !command.IsReview(n.Body, prefix) {
			return false
		}
	}
	return true
}

// routingReply is the operator's answer to model and effort commands, and
// where it goes.
type routingReply struct {
	body string
	onMR bool
}

const maxChatAnswers = 20

// applyChatCommands applies Desk chat commands newer than
// src.LastChatCommandID (Implement tasks). It records answers in
// src.ChatAnswers and advances the mark in the same write as a RunRequest
// bump. Returns whether a run was requested (retry, a successful review, or
// the answer to the last open question of the agent).
func (r *AgentProjectReconciler) applyChatCommands(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec,
	is *gitlab.Issue, task *aiv1.AgentTask, mr *gitlab.MergeRequest, bot int64) (bool, error) {
	src := &task.Spec.Source
	var pending []aiv1.ChatMessage
	for _, m := range src.ChatMessages {
		if m.Kind == "command" && m.ID > src.LastChatCommandID {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return false, nil
	}
	slices.SortFunc(pending, func(a, b aiv1.ChatMessage) int { return cmp.Compare(a.ID, b.ID) })
	logger := log.FromContext(ctx)
	now := metav1.Now()
	run := false
	answer := func(id int64, text string) {
		src.ChatAnswers = append(src.ChatAnswers, aiv1.ChatReply{MessageID: id, Text: text, At: now})
		src.LastChatCommandID = id
	}
	for _, m := range pending {
		cmd, err := chatcmd.Parse(m.Text)
		if err != nil {
			answer(m.ID, err.Error())
			logger.Info("chat command skipped", "id", m.ID, "author", m.Author, "error", err.Error())
			continue
		}
		if cmd.PromptCmd() {
			// The agent's questions: the owners' answers go to its session.
			if !slices.Contains(task.Spec.OwnerSubjects, m.Author) {
				answer(m.ID, "Only the milestone's owners can answer prompts.")
				logger.Info("chat command refused", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "reason", "not an owner subject")
				continue
			}
			who := m.Author
			if m.AuthorName != "" {
				who = m.AuthorName
			}
			logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "prompt", cmd.Prompt)
			reply, due := answerQuestion(task, who, cmd)
			run = run || due
			answer(m.ID, reply)
			continue
		}
		if cmd.RestartCmd() {
			if !slices.Contains(task.Spec.OwnerSubjects, m.Author) {
				answer(m.ID, notOwnerRestart)
				logger.Info("chat command refused", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "reason", "not an owner subject")
				continue
			}
			// Keyed by the command: applied again after a lost update of the
			// task, it restarts nothing twice.
			key := fmt.Sprintf("chat/%s/%d", task.Name, m.ID)
			text, err := r.restartRun(ctx, gl, spec, task, is.IID, cmp.Or(m.AuthorName, m.Author)+" via the Desk", key, bot)
			if err != nil {
				return false, err
			}
			answer(m.ID, text)
			continue
		}
		if !cmd.ImplementOnly() {
			answer(m.ID, "not supported on this task yet")
			logger.Info("chat command not supported yet", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author)
			continue
		}
		logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author)
		switch cmd.Cmd {
		case "retry":
			run = true
			answer(m.ID, "🤖 A new run is requested.")
		case "review":
			text := "There is no pushed head of the agent to review."
			if mr != nil && mr.SHA != "" && mr.SHA == task.Status.Run.PushedSHA {
				if err := r.askReview(ctx, task, task.Status.Run.ReviewKey()); err != nil {
					return false, err
				}
				run = true
				text = "🤖 The reviewers will run again on the pushed head."
			}
			answer(m.ID, text)
		case "use", "effort", "budget":
			rt, ok := cmd.Routing()
			if !ok {
				answer(m.ID, "not a routing command")
				continue
			}
			ch := command.Apply(spec, r.crossRepoTargets(spec), is.Labels, []command.Routing{rt})
			if len(ch.Add)+len(ch.Remove) > 0 {
				if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{AddLabels: ch.Add, RemoveLabels: ch.Remove}); err != nil {
					return false, fmt.Errorf("setting the model and effort labels of issue #%d: %w", is.IID, err)
				}
				is.Labels = append(slices.DeleteFunc(is.Labels, func(l string) bool { return slices.Contains(ch.Remove, l) }), ch.Add...)
			}
			if ch.Budget != nil {
				task.Spec.BudgetUSD = *ch.Budget
			}
			answer(m.ID, ch.Reply())
		}
	}
	if n := len(src.ChatAnswers); n > maxChatAnswers {
		src.ChatAnswers = append([]aiv1.ChatReply(nil), src.ChatAnswers[n-maxChatAnswers:]...)
	}
	return run, nil
}

// applyRouting turns the "/agent use", "/agent effort", "/agent budget" and
// "/agent repo" comments of humans that are newer than the last applied one
// into the issue's model, effort and companion labels and the task's budget
// (ts). It returns the answer, to be posted once the task is saved, where
// the last command was written.
func (r *AgentProjectReconciler) applyRouting(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is *gitlab.Issue,
	issueNotes, mrNotes []gitlab.Note, ts *aiv1.AgentTaskSpec, botID int64) (*routingReply, error) {
	prefix := spec.Workflow.CommandPrefix
	src := &ts.Source
	since := src.LastRoutingNoteID
	if since == 0 {
		// A new task (also one made again for the same issue): commands the
		// operator answered before are not applied again.
		for _, n := range append(slices.Clone(issueNotes), mrNotes...) {
			if botID != 0 && n.Author.ID == botID && strings.Contains(n.Body, marker.RoutingReply) {
				since = max(since, n.ID)
			}
		}
	}
	type note struct {
		gitlab.Note
		onMR bool
	}
	var found []note
	for i, list := range [][]gitlab.Note{issueNotes, mrNotes} {
		for _, n := range list {
			if n.ID <= since || !IsCommand(n, botID, prefix) {
				continue
			}
			if _, ok := command.ParseRouting(n.Body, prefix); ok {
				found = append(found, note{n, i == 1})
			}
		}
	}
	if len(found) == 0 {
		src.LastRoutingNoteID = since
		return nil, nil
	}
	slices.SortFunc(found, func(a, b note) int { return cmp.Compare(a.ID, b.ID) })
	cmds := make([]command.Routing, len(found))
	for i, n := range found {
		cmds[i], _ = command.ParseRouting(n.Body, prefix)
	}
	ch := command.Apply(spec, r.crossRepoTargets(spec), is.Labels, cmds)
	if len(ch.Add)+len(ch.Remove) > 0 {
		if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{AddLabels: ch.Add, RemoveLabels: ch.Remove}); err != nil {
			return nil, fmt.Errorf("setting the model and effort labels of issue #%d: %w", is.IID, err)
		}
		is.Labels = append(slices.DeleteFunc(is.Labels, func(l string) bool { return slices.Contains(ch.Remove, l) }), ch.Add...)
	}
	if ch.Budget != nil {
		ts.BudgetUSD = *ch.Budget
	}
	log.FromContext(ctx).Info("model, effort, budget and repo commands applied", "issue", is.IID, "add", ch.Add, "remove", ch.Remove, "budgetUSD", ts.BudgetUSD)
	last := found[len(found)-1]
	src.LastRoutingNoteID = last.ID
	return &routingReply{body: ch.Reply(), onMR: last.onMR}, nil
}

// IsCommand reports whether a note is a human "/agent ..." command.
func IsCommand(n gitlab.Note, botID int64, prefix string) bool {
	if n.System || n.Author.ID == botID || prefix == "" {
		return false
	}
	body := strings.TrimSpace(n.Body)
	if !strings.HasPrefix(body, prefix) {
		return false
	}
	// "/agent" must be a word on its own: "/agents are cool" is not a command.
	rest := body[len(prefix):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\n' || rest[0] == '\t' || rest[0] == ':'
}

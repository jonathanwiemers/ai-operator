package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// commentBoxRefusal answers a comment with command lines the Desk does not
// send itself: posted with the bot's token, the operator would ignore them.
const commentBoxRefusal = "Commands from the Desk comment box are posted as the bot and ignored. Use the buttons or comment in GitLab as yourself."

// NoteCommandsResponse answers a comment whose command lines went to their
// tasks as Desk chat commands instead of to GitLab: the words, in order.
type NoteCommandsResponse struct {
	Routed []string `json:"routed"`
}

// noteCommand is a command line of a comment as the Desk chat command the
// buttons send: to task, with owners holding the merge grant.
type noteCommand struct {
	task   *aiv1.AgentTask
	cmd    chatcmd.Command
	owners []string
}

// noteRoute says where a Desk command word goes, or why it cannot be sent.
type noteRoute func(tasks []aiv1.AgentTask, word string) (noteCommand, string)

// postsAsBot reports whether tok is a fixed token of ap's bot, whose
// command lines the operator ignores.
func postsAsBot(ap *aiv1.AgentProject, tok *GitLabToken) bool {
	st := ap.Status
	return tok.Fixed && ((st.BotUserID != 0 && tok.UserID == st.BotUserID) || (st.BotUsername != "" && tok.Username == st.BotUsername))
}

// noteCommands reads a trimmed comment's "<prefix> <word>" lines: the
// Desk command words of lines that are exactly that, whether any line
// starts with the prefix, and whether every non-blank line is a Desk
// command line.
func noteCommands(body, prefix string) (words []string, commands, only bool) {
	only = true
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rest, ok := strings.CutPrefix(line, prefix)
		if prefix == "" || !ok || (rest != "" && !strings.ContainsAny(rest[:1], " \t:")) {
			only = false
			continue
		}
		commands = true
		f := strings.Fields(strings.TrimPrefix(rest, ":"))
		if len(f) != 1 {
			only = false
			continue
		}
		word := strings.ToLower(f[0])
		if _, desk := deskCommands[word]; !desk {
			only = false
			continue
		}
		words = append(words, word)
	}
	return words, commands, only
}

// routeNoteCommands handles a comment that would go out with ap's bot
// token and holds command lines: Desk commands go to their tasks (route)
// as chat commands, with the grants of the buttons, and nothing is posted;
// any other command line is refused. False when the comment is to be
// posted as it is.
func (s *Server) routeNoteCommands(w http.ResponseWriter, r *http.Request, ap *aiv1.AgentProject, tok *GitLabToken, body string, route noteRoute) bool {
	if !postsAsBot(ap, tok) {
		return false
	}
	words, commands, only := noteCommands(body, ap.Spec.Workflow.WithDefaults().CommandPrefix)
	if !commands {
		return false
	}
	if !only || !s.cfg.ChatEnabled() {
		badRequest(w, commentBoxRefusal)
		return true
	}
	if !s.granted(w, r, s.authz.CanChat, "chat") {
		return true
	}
	tasks, err := s.projectTasks(r.Context(), ap.Namespace, ap.Name)
	if err != nil {
		s.internalError(w, err)
		return true
	}
	p := principalOf(r)
	cmds := make([]noteCommand, 0, len(words))
	for _, word := range words {
		c, problem := route(tasks, word)
		if problem != "" {
			writeError(w, http.StatusConflict, problem)
			return true
		}
		if deskCommands[word] && !slices.Contains(c.owners, p.Username) {
			writeError(w, http.StatusForbidden, "no access to "+word)
			return true
		}
		cmds = append(cmds, c)
	}
	for _, c := range cmds {
		b, err := json.Marshal(c.cmd)
		if err != nil {
			s.internalError(w, err)
			return true
		}
		id, _, ok := s.insertChat(w, r, store.ChatMessage{Namespace: ap.Namespace, Project: ap.Name, Task: c.task.Name, Kind: "command", Body: string(b)})
		if !ok {
			return true
		}
		s.log.Info("audit", "action", "note.command", "user", p.Username, "namespace", ap.Namespace, "project", ap.Name,
			"task", c.task.Name, "cmd", c.cmd.Cmd, "id", id)
	}
	writeJSON(w, http.StatusOK, NoteCommandsResponse{Routed: words})
	return true
}

// taskNoteRoute routes the comment box of task: retry, review and restart
// go to task itself, the autopilot words to its milestone's spec task for
// its issue.
func taskNoteRoute(task *aiv1.AgentTask) noteRoute {
	return func(tasks []aiv1.AgentTask, word string) (noteCommand, string) {
		switch word {
		case "retry", "review", "restart":
			return noteCommand{task: task, cmd: chatcmd.Command{Cmd: word}, owners: task.Spec.OwnerSubjects}, ""
		}
		var st *aiv1.AgentTask
		if task.Spec.Milestone != nil {
			st, _ = milestoneTasks(tasks, task.Spec.Milestone.IID)
		}
		if st == nil || st.Status.Autopilot == nil || task.Spec.Issue == nil {
			return noteCommand{}, word + " needs an issue on an autopilot milestone"
		}
		iid := task.Spec.Issue.IID
		held := slices.ContainsFunc(st.Status.Autopilot.Items, func(it aiv1.AutopilotItem) bool { return it.IID == iid && it.Hold != nil })
		return autopilotNoteCommand(tasks, st, word, iid, held)
	}
}

// milestoneNoteRoute routes the comment box of the milestone with the spec
// task of the IID as its pause card does: for the paused issue, retry,
// review and restart to its implement task, the autopilot words to the
// spec task.
func milestoneNoteRoute(iid int64) noteRoute {
	return func(tasks []aiv1.AgentTask, word string) (noteCommand, string) {
		st, _ := milestoneTasks(tasks, iid)
		if st == nil || st.Status.Autopilot == nil {
			return noteCommand{}, word + " needs an autopilot milestone"
		}
		paused := int64(0)
		if ap := st.Status.Autopilot; ap.Phase == aiv1.AutopilotPaused {
			paused = ap.PausedFor
		}
		switch word {
		case "retry", "review", "restart":
			impl := issueTask(tasks, paused)
			if impl == nil {
				return noteCommand{}, word + " needs a paused issue with a task"
			}
			return noteCommand{task: impl, cmd: chatcmd.Command{Cmd: word}, owners: st.Spec.OwnerSubjects}, ""
		case "resume":
			return noteCommand{task: st, cmd: chatcmd.Command{Cmd: word}, owners: st.Spec.OwnerSubjects}, ""
		}
		if paused == 0 {
			return noteCommand{}, word + " needs a paused issue"
		}
		return autopilotNoteCommand(tasks, st, word, paused, false)
	}
}

// autopilotNoteCommand is the autopilot word (resume, skip, merge) for the
// issue iid on the spec task st, as the pause card builds it: resume of a
// held issue names it, merge names the issue's pushed head.
func autopilotNoteCommand(tasks []aiv1.AgentTask, st *aiv1.AgentTask, word string, iid int64, held bool) (noteCommand, string) {
	c := noteCommand{task: st, cmd: chatcmd.Command{Cmd: word}, owners: st.Spec.OwnerSubjects}
	switch word {
	case "resume":
		if held {
			c.cmd.Issue = iid
		}
	case "skip":
		c.cmd.Issue = iid
	case "merge":
		t := issueTask(tasks, iid)
		if t == nil || t.Status.Run.PushedSHA == "" {
			return noteCommand{}, fmt.Sprintf("merge needs a pushed head of #%d", iid)
		}
		c.cmd.Issue, c.cmd.SHA = iid, t.Status.Run.PushedSHA
	}
	return c, ""
}

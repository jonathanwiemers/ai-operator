package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// commands handles "/agent pause|resume|skip #N" on the tracking issue and
// answers any other command once, in its thread; then the pilot's accepted
// queue commands and its release decisions.
func (a *autopilot) commands(ctx context.Context) error {
	if ok, err := a.trackingNotes(ctx); !ok {
		return err
	}
	notes := a.notes
	coord := a.pilot(ctx, notes)
	if err := a.followUps(ctx, notes); err != nil {
		return err
	}
	prefix := a.spec.Workflow.CommandPrefix
	for _, n := range notes {
		if n.ID <= a.ap.LastCommandNoteID || !IsCommand(n, a.project.Status.BotUserID, prefix) {
			continue
		}
		// Every comment gets one reply in its thread (replyPerNote).
		// Someone else's command is answered and handled, so it is not
		// answered again.
		tn := pilotNote{Note: n, issue: a.ap.TrackingIssueIID}
		if ok, err := a.gate.counts(ctx, n, a.st.Spec.Owners); err != nil {
			return err
		} else if !ok {
			a.gate.deny(ctx, tn, a.st.Spec.Owners, a.r.deskLink(a.spec, tn.issue, tn.issue, nil, a.st))
			a.ap.LastCommandNoteID = n.ID
			continue
		}
		rest := strings.TrimPrefix(strings.TrimSpace(n.Body), prefix)
		// Padded, so a bare command and a bare "skip" have their fields.
		fields := append(strings.Fields(strings.TrimLeft(rest, ": \t")), "", "")
		word := strings.ToLower(strings.TrimSuffix(fields[0], ":"))
		if word == "revisit" {
			// syncRevisits handles (and answers) the well-formed ones.
			if m := marker.Revisit.FindStringSubmatch(n.Body); m != nil && m[1] == prefix {
				a.ap.LastCommandNoteID = n.ID
				continue
			}
			word = ""
		}
		arg := fields[1]
		if word == "norelease" {
			word = "no-release"
		}
		if word == "release" || word == "no-release" {
			// The rest of the comment after the word: an optional reason
			// (no-release: "#N reason").
			body := strings.TrimLeft(rest, ": \t")
			arg = strings.TrimSpace(strings.TrimPrefix(body, fields[0]))
		}
		if word == "merge" || word == "start" || word == "move" || word == "endmilestone" || word == "release" || word == "no-release" || word == "parallel" {
			// Owners only, also where anyone may give commands.
			if ok, err := a.gate.isOwner(ctx, n.Author.ID, a.st.Spec.Owners); err != nil {
				return err
			} else if !ok {
				reply := map[string]string{"merge": notOwnerMerge, "start": notOwnerStart, "move": notOwnerMove, "endmilestone": notOwnerEnd,
					"release": notOwnerRelease, "no-release": notOwnerRelease, "parallel": notOwnerParallel}[word]
				a.r.rejectNote(ctx, a.gl, a.project, a.spec, tn, "🤖 "+reply)
				a.ap.LastCommandNoteID = n.ID
				continue
			}
		}
		if word == "endmilestone" {
			// Says itself what it did, on the tracking issue.
			if err := a.endMilestone(ctx, "@"+n.Author.Username); err != nil {
				return err
			}
			a.ap.LastCommandNoteID = n.ID
			continue
		}
		if word == "merge" {
			arg += " " + fields[2]
		}
		// Tried again on the next poll when it fails. A move is keyed by its
		// note, so a replay posts nothing twice.
		by := "@" + n.Author.Username
		var reply string
		var applied bool
		var err error
		if isMoveWord(word, arg) {
			reply, applied, err = a.moveWords(ctx, word, fields[1:], by, fmt.Sprintf("note/%d", n.ID))
		} else {
			reply, applied, err = a.command(ctx, word, arg, by)
		}
		if err != nil {
			return err
		}
		if !applied {
			// Usage and unknown commands; the reply's marker keeps a retry
			// from answering twice.
			err := a.r.replyPerNote(ctx, a.gl, a.project, a.spec, tn, "🤖 "+reply)
			if err != nil && !a.trackingGone(err) {
				return fmt.Errorf("answering a command on the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
			}
		}
		a.ap.LastCommandNoteID = n.ID
	}
	// The same commands from proposals an owner accepted; revisits are
	// syncRevisits'.
	if ps := a.st.Status.Pilot; ps != nil {
		for _, c := range ps.Commands {
			if c.Seq <= a.ap.LastPilotCommand {
				continue
			}
			by := pilotBy(c.Owner)
			if c.Action.Tool == "endMilestone" {
				if err := a.endMilestone(ctx, by); err != nil {
					return err
				}
			} else if c.Action.Tool != "revisit" {
				arg := fmt.Sprintf("#%d", c.Action.IssueIID)
				if c.Action.Tool == "resume" {
					arg = "" // the pilot's resume is the milestone's, as before
				}
				reply, applied, err := a.command(ctx, c.Action.Tool, arg, by)
				if err != nil {
					return err
				}
				if !applied {
					if err := a.comment(ctx, reply); err != nil {
						return err
					}
				}
			}
			a.ap.LastPilotCommand = c.Seq
		}
	}
	return a.pilotReleases(ctx, coord)
}

// chatCommands applies Desk pause/resume/skip on the Spec task newer than
// LastChatCommandID. Answers are kept on a until after the status save.
func (a *autopilot) chatCommands(ctx context.Context) error {
	src := &a.st.Spec.Source
	var pending []aiv1.ChatMessage
	for _, m := range src.ChatMessages {
		if m.Kind == "command" && m.ID > src.LastChatCommandID {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	slices.SortFunc(pending, func(x, y aiv1.ChatMessage) int { return cmp.Compare(x.ID, y.ID) })
	now := metav1.Now()
	var answers []aiv1.ChatReply
	mark := src.LastChatCommandID
	answer := func(id int64, text string) {
		answers = append(answers, aiv1.ChatReply{MessageID: id, Text: text, At: now})
		mark = id
	}
	for _, m := range pending {
		cmd, err := chatcmd.Parse(m.Text)
		if err != nil {
			answer(m.ID, err.Error())
			a.log.Info("chat command skipped", "id", m.ID, "author", m.Author, "error", err.Error())
			continue
		}
		if !cmd.Autopilot() && !cmd.RestartCmd() {
			answer(m.ID, "not supported on this task yet")
			continue
		}
		who := m.Author
		if m.AuthorName != "" {
			who = m.AuthorName
		}
		by := who + " via the Desk"
		// Owners only, like the tracking issue's words.
		if refusal := map[string]string{"merge": notOwnerMerge, "start": notOwnerStart, "unstart": notOwnerStart, "move": notOwnerMove,
			"endmilestone": notOwnerEnd, "restart": notOwnerRestart, "parallel": notOwnerParallel}[cmd.Cmd]; refusal != "" && !slices.Contains(a.st.Spec.OwnerSubjects, m.Author) {
			answer(m.ID, refusal)
			a.log.Info("chat command refused", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "reason", "not an owner subject")
			continue
		}
		a.log.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author)
		if cmd.RestartCmd() {
			// Keyed by the command: applied again after a lost answer, it
			// finds its note and restarts nothing twice.
			key := fmt.Sprintf("chat/%s/%d", a.st.Name, m.ID)
			reply, err := a.r.restartRun(ctx, a.gl, a.spec, a.st, a.ap.TrackingIssueIID, by, key, a.project.Status.BotUserID)
			if err != nil {
				return err
			}
			answer(m.ID, reply)
			continue
		}
		if mv, ok := cmd.Move(); ok || cmd.Cmd == "start" {
			// start is move top. Keyed like a restart: a replay posts
			// nothing twice.
			if !ok {
				mv = chatcmd.Move{To: "top"}
			}
			reply, _, err := a.move(ctx, mv, by, fmt.Sprintf("chat/%s/%d", a.st.Name, m.ID))
			if err != nil {
				return err
			}
			answer(m.ID, reply)
			continue
		}
		word, arg := cmd.Cmd, ""
		switch word {
		case "skip":
			arg = fmt.Sprintf("#%d", cmd.Issue)
		case "resume":
			if cmd.Issue != 0 {
				arg = fmt.Sprintf("#%d", cmd.Issue)
			}
		case "merge":
			arg = fmt.Sprintf("#%d %s", cmd.Issue, cmd.SHA)
		case "parallel":
			arg = strconv.Itoa(int(cmd.Parallel))
		case "unstart":
			word, arg = "start", "off"
		case "endmilestone":
			// endMilestone says on the tracking issue what it did.
			reply := "The milestone ends here; the tracking issue says what was skipped."
			if len(a.ap.Items) == 0 {
				reply = "Nothing to end: nothing is planned in this milestone."
			}
			if err := a.endMilestone(ctx, by); err != nil {
				return err
			}
			answer(m.ID, reply)
			continue
		}
		reply, _, err := a.command(ctx, word, arg, by)
		if err != nil {
			return err
		}
		answer(m.ID, reply)
	}
	a.chatMark, a.chatAnswers = mark, answers
	return nil
}

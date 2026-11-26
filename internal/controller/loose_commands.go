package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// notOwnerQueue answers a command on the loose issues queue by someone who
// is not one of its owners: every queue command is owner-only, as an
// owner's take is the only gate into the queue.
const notOwnerQueue = "Only the owners of the loose issues queue can give it commands."

// notOnQueue answers the tracking-issue words the queue does not take.
const notOnQueue = "not on the loose issues queue"

// looseCommands handles the owners' "/agent take" and "/agent drop" on the
// queue's tracking issue newer than LastCommandNoteID, and answers any other
// command once, in its thread. The mark moves only past a command that was
// applied, so a failed one is tried again on the next poll.
func (a *autopilot) looseCommands(ctx context.Context) error {
	if ok, err := a.trackingNotes(ctx); !ok {
		return err
	}
	prefix := a.spec.Workflow.CommandPrefix
	for _, n := range a.notes {
		if n.ID <= a.ap.LastCommandNoteID || !IsCommand(n, a.project.Status.BotUserID, prefix) {
			continue
		}
		tn := pilotNote{Note: n, issue: a.ap.TrackingIssueIID}
		if a.gate.desk {
			a.gate.deny(ctx, tn, a.st.Spec.Owners, a.r.deskLink(a.spec, tn.issue, tn.issue, nil, a.st))
			a.ap.LastCommandNoteID = n.ID
			continue
		}
		if ok, err := a.gate.isOwner(ctx, n.Author.ID, a.st.Spec.Owners); err != nil {
			return err
		} else if !ok {
			a.r.rejectNote(ctx, a.gl, a.project, a.spec, tn, "🤖 "+notOwnerQueue)
			a.ap.LastCommandNoteID = n.ID
			continue
		}
		rest := strings.TrimLeft(strings.TrimPrefix(strings.TrimSpace(n.Body), prefix), ": \t")
		fields := append(strings.Fields(rest), "")
		word := strings.ToLower(strings.TrimSuffix(fields[0], ":"))
		at := a.r.now()
		if n.CreatedAt != nil {
			at = *n.CreatedAt
		}
		reply, logged, err := a.looseCommand(ctx, word, fields[1:], "@"+n.Author.Username, at)
		if err != nil {
			return err
		}
		if !logged {
			// The reply's marker keeps a retry from answering twice.
			err := a.r.replyPerNote(ctx, a.gl, a.project, a.spec, tn, "🤖 "+reply)
			if err != nil && !a.trackingGone(err) {
				return fmt.Errorf("answering a command on the tracking issue #%d: %w", a.ap.TrackingIssueIID, err)
			}
		}
		a.ap.LastCommandNoteID = n.ID
	}
	return nil
}

// looseChatCommands applies the Desk commands on the Queue task newer than
// LastChatCommandID, from its owner subjects only. The answers are kept on a
// until after the status save (writeChatAnswers).
func (a *autopilot) looseChatCommands(ctx context.Context) error {
	src := &a.st.Spec.Source
	var pending []aiv1.ChatMessage
	for _, m := range src.ChatMessages {
		if m.Kind == "command" && m.ID > src.LastChatCommandID {
			pending = append(pending, m)
		}
	}
	slices.SortFunc(pending, func(x, y aiv1.ChatMessage) int { return cmp.Compare(x.ID, y.ID) })
	now := metav1.Now()
	for _, m := range pending {
		reply, err := a.looseChatCommand(ctx, m)
		if err != nil {
			return err
		}
		a.chatAnswers = append(a.chatAnswers, aiv1.ChatReply{MessageID: m.ID, Text: reply, At: now})
		a.chatMark = m.ID
	}
	return nil
}

// looseChatCommand applies one Desk command m and returns its answer.
func (a *autopilot) looseChatCommand(ctx context.Context, m aiv1.ChatMessage) (string, error) {
	cmd, err := chatcmd.Parse(m.Text)
	switch {
	case err != nil:
		return err.Error(), nil
	case !slices.Contains(a.st.Spec.OwnerSubjects, m.Author):
		a.log.Info("chat command refused", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "reason", "not an owner subject")
		return notOwnerQueue, nil
	case !cmd.Queue():
		return fmt.Sprintf("%s is %s.", cmd.Cmd, notOnQueue), nil
	}
	who := m.Author
	if m.AuthorName != "" {
		who = m.AuthorName
	}
	// The tracking issue's words, so both sources share one dispatch.
	var args []string
	for _, iid := range cmd.Issues { // take
		args = append(args, fmt.Sprintf("#%d", iid))
	}
	if cmd.Issue != 0 {
		args = append(args, fmt.Sprintf("#%d", cmd.Issue))
	}
	if cmd.SHA != "" { // merge
		args = append(args, cmd.SHA)
	}
	at := m.At.Time
	if at.IsZero() {
		at = a.r.now()
	}
	a.log.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author)
	reply, _, err := a.looseCommand(ctx, cmd.Cmd, args, who+" via the Desk", at)
	return reply, err
}

// looseCommand applies one command of the loose issues queue, word with its
// args, by someone, given at. It returns the answer and whether the tracking
// issue says already what was done (logged), so the comment needs no reply.
// The steering commands join here.
func (a *autopilot) looseCommand(ctx context.Context, word string, args []string, by string, at time.Time) (reply string, logged bool, err error) {
	switch word {
	case "take":
		iids, err := chatcmd.ParseTake(args)
		if err != nil {
			return fmt.Sprintf("%s. Write `%s take #N [#M …]`.", err, a.spec.Workflow.CommandPrefix), false, nil
		}
		reply, err := a.take(ctx, iids, by, at)
		return reply, false, err
	case "drop":
		return a.drop(ctx, args, by)
	case "pause", "resume", "merge", "rebase", "mergebase":
		// As on a milestone's tracking issue: the issue, and merge's sha.
		args = append(args, "", "")
		arg := args[0]
		if word == "merge" {
			arg += " " + args[1]
		}
		reply, applied, err := a.command(ctx, word, arg, by)
		switch {
		case err != nil || !applied:
		case word == "pause":
			reply = "The loose issues queue is paused."
		case word == "resume" && arg == "":
			reply = "The loose issues queue goes on."
		}
		return reply, applied, err
	default:
		return fmt.Sprintf("`%s` is %s.", word, notOnQueue), false, nil
	}
}

// take puts iids into the queue in their order, by someone, given at: one
// answer naming the issues taken and each one refused with its reason. An
// issue the same command took already (a retry after a failure) counts as
// taken and is not commented on again.
func (a *autopilot) take(ctx context.Context, iids []int64, by string, at time.Time) (string, error) {
	var taken, refused []string
	for _, iid := range iids {
		why, err := a.takeOne(ctx, iid, by, at)
		if err != nil {
			return "", err
		}
		if why != "" {
			refused = append(refused, why)
		} else {
			taken = append(taken, fmt.Sprintf("#%d", iid))
		}
	}
	var parts []string
	if len(taken) > 0 {
		parts = append(parts, "Taken into the loose issues queue: "+strings.Join(taken, ", ")+".")
	}
	if len(refused) > 0 {
		parts = append(parts, "Not taken: "+strings.Join(refused, "; ")+".")
	}
	return strings.Join(parts, " "), nil
}

// takeOne takes issue iid, or returns why not.
func (a *autopilot) takeOne(ctx context.Context, iid int64, by string, at time.Time) (string, error) {
	if it := a.queued(iid); it != nil {
		if it.TakenBy == by && it.TakenAt != nil && it.TakenAt.Unix() == at.Unix() {
			return "", nil
		}
		return fmt.Sprintf("#%d is in the queue already", iid), nil
	}
	is, err := a.gl.GetIssue(ctx, a.spec.GitLab.Project, iid)
	if gitlab.IsNotFound(err) {
		return fmt.Sprintf("#%d is closed", iid), nil
	}
	if err != nil {
		return "", fmt.Errorf("getting issue #%d: %w", iid, err)
	}
	wf := a.spec.Workflow
	mh := a.project.Status.MainHealth
	switch {
	case is.State != "opened":
		return fmt.Sprintf("#%d is closed", iid), nil
	case is.Milestone != nil:
		return fmt.Sprintf("#%d is in milestone %q", iid, inertLine(is.Milestone.Title, 100)), nil
	case mh != nil && (mh.IssueIID == iid || mh.AutoFixIssueIID == iid):
		return fmt.Sprintf("#%d is the main-red issue", iid), nil
	case is.HasLabel(wf.GeneratedLabel) && marker.IsTrackingIssue(is.Description):
		return fmt.Sprintf("#%d is a tracking issue of the operator", iid), nil
	}
	it := aiv1.AutopilotItem{IID: iid, State: aiv1.AutopilotItemPending, TakenBy: by, TakenAt: &metav1.Time{Time: at}}
	where := fmt.Sprintf("position %d", 1+len(slices.DeleteFunc(slices.Clone(a.ap.Items), func(x aiv1.AutopilotItem) bool {
		return x.State != aiv1.AutopilotItemPending
	})))
	if hasAny(*is, wf.ActiveLabels()...) {
		it.State, where = aiv1.AutopilotItemActive, "active"
	}
	// The comment first: a failed one is tried again with the command.
	if _, err := a.gl.CreateIssueNote(ctx, a.spec.GitLab.Project, iid, fmt.Sprintf("🛠️ Taken into the loose issues queue by %s (%s).", by, where)); err != nil {
		return "", fmt.Errorf("commenting on issue #%d: %w", iid, err)
	}
	// Taken again after it merged or left: a new item, at the end.
	a.ap.Items = append(slices.DeleteFunc(a.ap.Items, func(x aiv1.AutopilotItem) bool { return x.IID == iid }), it)
	if a.issues != nil {
		a.issues[iid] = *is // read before the poll's leave and step
	}
	a.log.Info("issue taken into the loose issues queue", "issue", iid, "by", by, "state", it.State)
	return "", nil
}

// queued is the queue's pending, active or held item of iid, nil if none.
func (a *autopilot) queued(iid int64) *aiv1.AutopilotItem {
	for i := range a.ap.Items {
		if it := &a.ap.Items[i]; it.IID == iid && (it.State == aiv1.AutopilotItemPending || it.State == aiv1.AutopilotItemActive) {
			return it
		}
	}
	return nil
}

// drop takes the issue in args off the queue, by someone, and logs it on
// the tracking issue. An active or held one continues in the manual flow:
// its labels stay, its run ends and a human merges it.
func (a *autopilot) drop(ctx context.Context, args []string, by string) (string, bool, error) {
	var it *aiv1.AutopilotItem
	if words := slices.DeleteFunc(slices.Clone(args), func(w string) bool { return w == "" }); len(words) == 1 {
		if iid, err := strconv.ParseInt(strings.TrimPrefix(words[0], "#"), 10, 64); err == nil {
			it = a.queued(iid)
		}
	}
	if it == nil {
		return fmt.Sprintf("`drop` needs an issue that is pending, active or held in the queue, e.g. `%s drop #12`.", a.spec.Workflow.CommandPrefix), false, nil
	}
	reason := "dropped by " + by
	if it.State == aiv1.AutopilotItemActive {
		reason += "; continues in the manual flow"
	}
	// The log first: a failed one is tried again with the command.
	if err := a.comment(ctx, fmt.Sprintf("↩️ #%d left the queue: %s.", it.IID, reason)); err != nil {
		return "", false, err
	}
	if a.ap.Landing == it.IID {
		a.ap.Landing = 0
	}
	leave(it, reason, &metav1.Time{Time: a.r.now()})
	a.log.Info("issue dropped from the loose issues queue", "issue", it.IID, "by", by)
	return fmt.Sprintf("#%d left the queue: %s.", it.IID, reason), true, nil
}

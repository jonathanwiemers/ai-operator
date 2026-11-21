package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/milestoneorder"
)

// noMarks answers `start off` and the Desk's unstart (decision 14).
const noMarks = "There are no run-next marks any more; use `move`."

// isMoveWord reports whether a tracking-issue command changes the run order:
// `move …`, and `start`, which is `move top` (all but `start off`).
func isMoveWord(word, arg string) bool {
	return word == "move" || word == "start" && !strings.EqualFold(arg, "off")
}

// moveWords applies the tracking issue's `move …` or `start` words (args
// after the word) by someone; key names the note.
func (a *autopilot) moveWords(ctx context.Context, word string, args []string, by, key string) (string, bool, error) {
	mv := chatcmd.Move{To: "top"}
	if word == "move" {
		var err error
		if mv, err = chatcmd.ParseMove(args); err != nil {
			return fmt.Sprintf("%s, e.g. `%s move before %%6`.", err, a.spec.Workflow.CommandPrefix), false, nil
		}
	}
	return a.move(ctx, mv, by, key)
}

// move puts this milestone where mv says in the project's run order
// (status.milestoneOrder), by an owner (the callers check). It refuses a
// done milestone and a target that is this milestone or not in the order,
// and answers "Already …" when nothing would change. Otherwise it posts one
// note on the tracking issue first, then rewrites the order: the next sync
// ranks by it. key names the command; the note records it, so a replay
// after a lost write finds the note, posts nothing and applies the order.
// The answer of an applied move starts with "Moved" (the Desk reads it).
func (a *autopilot) move(ctx context.Context, mv chatcmd.Move, by, key string) (string, bool, error) {
	self := a.st.Spec.Milestone.IID
	if a.ap.Phase == aiv1.AutopilotDone {
		return "This milestone is done; it has no place in the run order.", false, nil
	}
	old := slices.Clone(a.project.Status.MilestoneOrder)
	if !slices.Contains(old, self) {
		old = append(old, self)
	}
	if n := cmp.Or(mv.Before, mv.After); n == self {
		return "A milestone cannot move before or after itself.", false, nil
	} else if n != 0 && !slices.Contains(old, n) {
		refusal, err := a.notInOrder(ctx, n)
		return refusal, false, err
	}
	next := reorder(old, self, mv)
	what, icon, already := "to the top", "⏫", "at the top of"
	switch {
	case mv.Before > 0:
		what = fmt.Sprintf("before %%%d", mv.Before)
		already = what + " in"
	case mv.After > 0:
		what = fmt.Sprintf("after %%%d", mv.After)
		already = what + " in"
	case mv.To == "bottom":
		what, icon, already = "to the bottom", "⏬", "at the bottom of"
	}
	tag := ""
	if key != "" {
		tag = fmt.Sprintf("<!-- ai-operator:move=%s -->", key)
	}
	noted, err := a.movedBefore(ctx, tag)
	if err != nil {
		return "", false, err
	}
	if !noted && slices.Equal(old, next) {
		return "Already " + already + " the run order; nothing changed.", false, nil
	}
	held := ""
	if r := a.runningMilestone(old); r != 0 && slices.Index(old, self) > slices.Index(old, r) && slices.Index(next, self) < slices.Index(next, r) {
		held = fmt.Sprintf(" %%%d's active issues finish first; it starts no new issues until this milestone is done.", r)
	}
	if !noted {
		body := fmt.Sprintf("%s Moved %s (by %s).%s", icon, what, by, held)
		if tag != "" {
			body += "\n\n" + tag
		}
		// Comment first: if it fails, the command is applied again on the
		// next poll.
		if err := a.comment(ctx, body); err != nil {
			return "", false, err
		}
	}
	a.log.Info("autopilot milestone moved in the run order", "move", mv.String(), "order", next, "by", by)
	a.project.Status.MilestoneOrder = next
	return "Moved " + what + "." + held, true, nil
}

// reorder returns order with self moved where mv says; a before or after
// target is in order.
func reorder(order []int64, self int64, mv chatcmd.Move) []int64 {
	rest := slices.DeleteFunc(slices.Clone(order), func(o int64) bool { return o == self })
	switch {
	case mv.To == "top":
		return append([]int64{self}, rest...)
	case mv.To == "bottom":
		return append(rest, self)
	}
	i := slices.Index(rest, cmp.Or(mv.Before, mv.After))
	if mv.After > 0 {
		i++
	}
	return slices.Insert(rest, i, self)
}

// runningMilestone returns the running milestone of the run order: the first
// one that has started (milestoneorder.Entry.Started), 0 when none has.
func (a *autopilot) runningMilestone(order []int64) int64 {
	for _, iid := range order {
		e := milestoneorder.EntryOf(iid, a.peers[iid])
		if iid == a.st.Spec.Milestone.IID {
			e.Started = len(a.ap.Items) > 0 && a.ap.Phase != aiv1.AutopilotDone
		}
		if e.Started {
			return iid
		}
	}
	return 0
}

// notInOrder is the refusal of a move before or after milestone n, which is
// not in the run order: done, closed, unknown or not on autopilot.
func (a *autopilot) notInOrder(ctx context.Context, n int64) (string, error) {
	if t := a.peers[n]; t != nil && t.Status.Autopilot != nil && t.Status.Autopilot.Phase == aiv1.AutopilotDone {
		return fmt.Sprintf("%%%d is done; it is no longer in the run order.", n), nil
	}
	m, err := a.gl.GetMilestoneByIID(ctx, a.spec.GitLab.Project, n)
	switch {
	case gitlab.IsNotFound(err):
		return fmt.Sprintf("%%%d is not a milestone of this project.", n), nil
	case err != nil:
		return "", fmt.Errorf("reading milestone %%%d: %w", n, err)
	case m.State == "closed":
		return fmt.Sprintf("%%%d is closed; it is no longer in the run order.", n), nil
	}
	return fmt.Sprintf("%%%d is not an open autopilot milestone of this project.", n), nil
}

// movedBefore reports whether the bot's note of the move tag is on the
// tracking issue already: the move was applied and its order write lost.
func (a *autopilot) movedBefore(ctx context.Context, tag string) (bool, error) {
	bot := a.project.Status.BotUserID
	if tag == "" || bot == 0 {
		return false, nil
	}
	if _, err := a.trackingNotes(ctx); err != nil {
		return false, err
	}
	return slices.ContainsFunc(a.notes, func(n gitlab.Note) bool {
		return n.Author.ID == bot && strings.Contains(n.Body, tag)
	}), nil
}

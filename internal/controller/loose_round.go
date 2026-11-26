package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// The loose issues queue's pilot works in rounds, status.autopilot.round on
// the Queue task. A round ends after looseIssues.pilot.roundIssues finished
// queue issues or roundHours, never in the middle of a pilot call: the
// operator asks the pilot for the round's note (spec.source.roundNote),
// posts it on the tracking issue and starts the next round. The runner runs
// a new round in a fresh session, with the note, and counts its budget from
// the round's StartCostUSD.

// syncRound starts, counts, ends and resets the round of the queue's pilot
// coord on a.ap, saved with the queue's status. Without a pilot it waits.
func (a *autopilot) syncRound(ctx context.Context, coord *aiv1.AgentTask) error {
	if coord == nil {
		return nil
	}
	// Its runs and messages may have moved since the poll listed it.
	if err := a.r.Get(ctx, client.ObjectKeyFromObject(coord), coord); err != nil {
		return err
	}
	// Whole seconds, as the API stores them.
	now, cost := a.r.now().UTC().Truncate(time.Second), coord.Status.Run.CostUSD
	if a.ap.Round == nil {
		a.ap.Round = &aiv1.LooseRound{Number: 1, StartedAt: metav1.NewTime(now), StartCostUSD: cost}
	}
	rd, src, observed := a.ap.Round, &coord.Spec.Source, coord.Status.Run.ObservedRunRequest
	rd.Finished = finishedSince(a.ap.Items, rd.StartedAt.Time)
	rd.CostUSD = aiv1.FormatCost(max(0, aiv1.CostOf(cost)-aiv1.CostOf(rd.StartCostUSD)))
	switch {
	case rd.NoteRequest == 0 && src.RoundNote != 0:
		// Asked for before, and the status write was lost.
		rd.NoteRequest = src.RoundNote
	case rd.NoteRequest == 0 && roundDue(a.spec.LooseIssues.Pilot, rd, now) && observed >= coord.Spec.RunRequest:
		// No call runs: one call for the note.
		coord.Spec.RunRequest++
		src.RoundNote = coord.Spec.RunRequest
		if err := a.r.Update(ctx, coord); err != nil {
			return fmt.Errorf("asking the pilot for the note of round %d: %w", rd.Number, err)
		}
		a.log.Info("round of the queue's pilot ends, asking for its note", "round", rd.Number, "request", src.RoundNote)
		rd.NoteRequest = src.RoundNote
		return nil
	case rd.NoteRequest != 0 && observed >= rd.NoteRequest:
		return a.nextRound(ctx, coord, now)
	}
	return a.copyRound(ctx, coord)
}

// roundDue reports whether round rd reached a limit of p; a limit of 0 is
// off.
func roundDue(p aiv1.LooseIssuesPilotSpec, rd *aiv1.LooseRound, now time.Time) bool {
	n, d := p.Issues(), p.Duration()
	return n > 0 && rd.Finished >= n || d > 0 && now.Sub(rd.StartedAt.Time) >= d
}

// finishedSince counts the items that merged or left at or after start.
func finishedSince(items []aiv1.AutopilotItem, start time.Time) int32 {
	var n int32
	for _, it := range items {
		if finished(it) && finishedAt(it) >= start.UnixNano() {
			n++
		}
	}
	return n
}

// nextRound posts the note of the round that ended, the pilot's or the
// facts, and starts the next round. It waits while the tracking issue
// cannot be read.
func (a *autopilot) nextRound(ctx context.Context, coord *aiv1.AgentTask, now time.Time) error {
	rd := a.ap.Round
	var note string
	if ps := coord.Status.Pilot; ps != nil && ps.RoundNote != nil && ps.RoundNote.Request == rd.NoteRequest {
		note = strings.TrimSpace(ps.RoundNote.Note)
	}
	if note == "" {
		note = a.roundFacts(rd)
	}
	// Room for the ellipsis within the cap.
	note = textutil.Truncate(note, aiv1.MaxRoundNote-1)
	if posted, err := a.postRoundNote(ctx, rd.Number, note); !posted {
		return err
	}
	next := &aiv1.LooseRound{Number: rd.Number + 1, StartedAt: metav1.NewTime(now), StartCostUSD: coord.Status.Run.CostUSD, Note: note}
	coord.Spec.Source.RoundNote = 0
	coord.Spec.Source.PilotRound = pilotRoundOf(next)
	if err := a.r.Update(ctx, coord); err != nil {
		return fmt.Errorf("starting round %d of the queue's pilot: %w", next.Number, err)
	}
	a.log.Info("round of the queue's pilot started", "round", next.Number)
	a.ap.Round = next
	return nil
}

// roundFacts is the note of round rd when the pilot wrote none: what merged,
// left and is held, and what the pilot spent.
func (a *autopilot) roundFacts(rd *aiv1.LooseRound) string {
	start := rd.StartedAt.UnixNano()
	var merged, left, held []string
	for _, it := range a.ap.Items {
		switch {
		case it.State == aiv1.AutopilotItemMerged && finishedAt(it) >= start:
			merged = append(merged, fmt.Sprintf("#%d", it.IID))
		case it.State == aiv1.AutopilotItemLeft && finishedAt(it) >= start:
			left = append(left, fmt.Sprintf("#%d (%s)", it.IID, it.LeftReason))
		case it.State == aiv1.AutopilotItemActive && it.Hold != nil:
			held = append(held, fmt.Sprintf("#%d (%s)", it.IID, it.Hold.Reason))
		}
	}
	lines := []string{"The pilot wrote no note, so these are the facts of the round."}
	for _, l := range []struct {
		name  string
		items []string
	}{{"Merged", merged}, {"Left", left}, {"Held", held}} {
		if len(l.items) > 0 {
			lines = append(lines, l.name+": "+strings.Join(l.items, "; ")+".")
		}
	}
	return strings.Join(append(lines, "Spent $"+aiv1.ShowCost(aiv1.CostOf(rd.CostUSD))+"."), "\n")
}

// roundNoteMarker tags the comment with the note of round n, so a retry
// after a lost status write does not post it again.
func roundNoteMarker(n int32) string {
	return fmt.Sprintf("<!-- ai-operator:round-note=%d -->", n)
}

// postRoundNote posts the note of round n and the start of the next one on
// the tracking issue, once. It reports whether the comment is there.
func (a *autopilot) postRoundNote(ctx context.Context, n int32, note string) (bool, error) {
	if ok, err := a.trackingNotes(ctx); !ok {
		return false, err
	}
	tag, bot := roundNoteMarker(n), a.project.Status.BotUserID
	if slices.ContainsFunc(a.notes, func(x gitlab.Note) bool { return x.Author.ID == bot && strings.Contains(x.Body, tag) }) {
		return true, nil
	}
	body := fmt.Sprintf("🧭 **Round %d note**\n\n%s\n\nRound %d started.\n\n%s", n, notify.Inert(note), n+1, tag)
	_, err := a.gl.CreateIssueNote(ctx, a.spec.GitLab.Project, a.ap.TrackingIssueIID, body)
	if a.trackingGone(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("posting the note of round %d on the tracking issue #%d: %w", n, a.ap.TrackingIssueIID, err)
	}
	return true, nil
}

// copyRound copies the round into the pilot's spec.source.pilotRound, for
// the runner.
func (a *autopilot) copyRound(ctx context.Context, coord *aiv1.AgentTask) error {
	want := pilotRoundOf(a.ap.Round)
	if equality.Semantic.DeepEqual(coord.Spec.Source.PilotRound, want) {
		return nil
	}
	coord.Spec.Source.PilotRound = want
	return a.r.Update(ctx, coord)
}

func pilotRoundOf(rd *aiv1.LooseRound) *aiv1.PilotRound {
	return &aiv1.PilotRound{Number: rd.Number, StartedAt: rd.StartedAt, Note: rd.Note, StartCostUSD: rd.StartCostUSD}
}

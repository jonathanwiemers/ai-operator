package history

import (
	"fmt"
	"slices"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/problem"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// The kinds of the problem thread: a problem opens or resolves
// (store.KindProblemOpened, store.KindProblemResolved write desk_problems
// too), an issue is held or released, the pilot acted on its own or ran an
// owner's yes.
const (
	KindProblemOpened   = store.KindProblemOpened
	KindProblemResolved = store.KindProblemResolved
	KindHoldSet         = "hold.set"
	KindHoldCleared     = "hold.cleared"
	KindPilotActed      = "pilot.acted"
)

// problems returns the problem.opened and problem.resolved events of a spec
// task's change: an issue (or 0, the milestone) that enters problem.Open
// opens, one that leaves it resolves. Keys hold the resource version of the
// change, so a replayed update is no new event.
func problems(t *aiv1.AgentTask, old, cur *aiv1.AgentTask, now time.Time) []store.Event {
	if t.Spec.Type != aiv1.TaskTypeSpec || t.Spec.Milestone == nil {
		return nil
	}
	was, is := problem.Open(old), problem.Open(cur)
	var out []store.Event
	emit := func(iid int64, kind, summary string, data map[string]any) {
		e := base(t, now)
		e.Key = taskKey(t, fmt.Sprintf("problem/%d/%s/%s", iid, kind, t.ResourceVersion))
		e.Kind, e.IssueIID, e.Summary, e.Data = kind, iid, textutil.Truncate(summary, maxText), data
		out = append(out, e)
	}
	for _, iid := range is {
		if !slices.Contains(was, iid) {
			cause := problem.Why(cur, iid)
			emit(iid, KindProblemOpened, "Stuck: "+cause, map[string]any{"cause": cause})
		}
	}
	for _, iid := range was {
		if !slices.Contains(is, iid) {
			by := resolvedBy(old, cur, iid)
			emit(iid, KindProblemResolved, "Unstuck, resolved by "+by, map[string]any{"resolvedBy": by})
		}
	}
	return out
}

// resolvedBy says who resolved iid's problem in the change from old to cur:
// the owner who answered its question, who resumed the pause, or the
// operator (the cause went away).
func resolvedBy(old, cur *aiv1.AgentTask, iid int64) string {
	was := map[string]bool{}
	for _, p := range old.Status.Prompts {
		was[p.ID] = p.State == aiv1.PromptOpen
	}
	for _, p := range cur.Status.Prompts {
		if was[p.ID] && p.State == aiv1.PromptAnswered && p.Source.IssueIID == iid && p.Answer != nil && p.Answer.By != "" {
			return p.Answer.By
		}
	}
	if ap := cur.Status.Autopilot; ap != nil && ap.Phase != aiv1.AutopilotPaused && ap.ResumedBy != "" &&
		aiv1.ProblemIssue(old.Status.Autopilot) == iid {
		return string(ap.ResumedBy)
	}
	return string(aiv1.ResolvedByOperator)
}

// holds returns hold.set and hold.cleared events of the autopilot's items:
// a hold that starts (or restarts: a new since) and one that ends. A hold
// also ends when its item leaves the items or becomes merged or skipped.
// Both carry the hold's start as since (RFC 3339); keys hold the item, the
// start and the kind, so a replayed update is no new event.
func holds(t *aiv1.AgentTask, old, cur *aiv1.AutopilotStatus, now time.Time) []store.Event {
	was := map[int64]*aiv1.ItemHold{}
	if old != nil {
		for _, it := range old.Items {
			if !itemEnded(it.State) {
				was[it.IID] = it.Hold
			}
		}
	}
	var items []aiv1.AutopilotItem
	if cur != nil {
		items = cur.Items
	}
	var out []store.Event
	emit := func(iid int64, kind string, h *aiv1.ItemHold, summary string) *store.Event {
		e := base(t, now)
		e.Key = taskKey(t, fmt.Sprintf("hold/%d/%d/%s", iid, h.Since.Unix(), kind))
		e.Kind, e.IssueIID, e.Summary = kind, iid, textutil.Truncate(summary, maxText)
		e.Data = map[string]any{"holdKind": string(h.Kind), "reason": textutil.Truncate(h.Reason, maxText),
			"since": h.Since.UTC().Format(time.RFC3339)}
		out = append(out, e)
		return &out[len(out)-1]
	}
	end := func(iid int64, h *aiv1.ItemHold) {
		emit(iid, KindHoldCleared, h, fmt.Sprintf("Hold on #%d cleared (%s)", iid, h.Kind))
	}
	seen := map[int64]bool{}
	for _, it := range items {
		seen[it.IID] = true
		prev := was[it.IID]
		h := it.Hold
		if itemEnded(it.State) {
			h = nil
		}
		if prev != nil && (h == nil || !prev.Since.Equal(&h.Since)) {
			end(it.IID, prev)
		}
		if h != nil && (prev == nil || !prev.Since.Equal(&h.Since)) {
			e := emit(it.IID, KindHoldSet, h, fmt.Sprintf("Held #%d (%s): %s", it.IID, h.Kind, h.Reason))
			if !h.Since.IsZero() {
				e.At = h.Since.Time
			}
		}
	}
	if old != nil {
		for _, it := range old.Items {
			if h := was[it.IID]; h != nil && !seen[it.IID] {
				end(it.IID, h)
			}
		}
	}
	return out
}

// itemEnded is whether an autopilot item is done: merged or skipped.
func itemEnded(s aiv1.AutopilotItemState) bool {
	return s == aiv1.AutopilotItemMerged || s == aiv1.AutopilotItemSkipped
}

// acted returns a pilot.acted event per issue of each new status.pilot.acted
// entry (one for the milestone when it names none), so every problem's
// thread shows what the pilot did about it.
func acted(t *aiv1.AgentTask, old, cur *aiv1.PilotStatus, now time.Time) []store.Event {
	if cur == nil {
		return nil
	}
	seen := map[string]bool{}
	if old != nil {
		for _, a := range old.Acted {
			seen[actedID(a)] = true
		}
	}
	var out []store.Event
	for _, a := range cur.Acted {
		if seen[actedID(a)] {
			continue
		}
		issues := a.Issues
		if len(issues) == 0 {
			issues = []int64{0}
		}
		who := "Pilot"
		if a.By != "" {
			who = a.By + " (via pilot)"
		}
		summary := fmt.Sprintf("%s: %s", who, a.Summary)
		if !a.Ran {
			summary += " (did not run)"
		}
		for _, iid := range slices.Compact(slices.Sorted(slices.Values(issues))) {
			e := base(t, now)
			e.Key = taskKey(t, fmt.Sprintf("acted/%s/%d", actedID(a), iid))
			e.Kind, e.IssueIID, e.Actor = KindPilotActed, iid, a.By
			if !a.At.IsZero() {
				e.At = a.At.Time
			}
			e.Summary = textutil.Truncate(summary, maxText)
			e.Data = map[string]any{"why": a.Why, "lines": a.Lines, "ran": a.Ran, "by": a.By}
			out = append(out, e)
		}
	}
	return out
}

func actedID(a aiv1.PilotActed) string {
	return fmt.Sprintf("%d-%x", a.At.UnixNano(), fnv32(a.Summary))
}

func fnv32(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

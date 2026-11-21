// Package milestoneorder ranks the open autopilot milestones of a project in
// the order they run: milestones with a run-next mark first (earliest mark
// first), then the unmarked ones by age (lowest iid first). The controller
// (queuedAhead) and the web UI use it, so they cannot disagree. RunOrder is
// the queue position the plan agent and the Desk show.
package milestoneorder

import (
	"cmp"
	"slices"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Entry is one autopilot milestone of a project.
type Entry struct {
	IID      int64
	MarkedAt *time.Time // the run-next mark; nil: unmarked
	Done     bool
	// Started: the autopilot has items and is not done, so the milestone
	// is planned or running.
	Started bool
}

// EntryOf builds the entry of milestone iid from its spec task (nil: none
// yet, unmarked and not done). A mark needs both StartRequestedBy and
// StartRequestedAt: a StartRequestedBy from before marks had a time only
// lets the milestone plan ahead of its turn.
func EntryOf(iid int64, st *aiv1.AgentTask) Entry {
	e := Entry{IID: iid}
	if st == nil || st.Status.Autopilot == nil {
		return e
	}
	ap := st.Status.Autopilot
	e.Done = ap.Phase == aiv1.AutopilotDone
	e.Started = len(ap.Items) > 0 && !e.Done
	if ap.StartRequestedBy != "" && ap.StartRequestedAt != nil {
		e.MarkedAt = &ap.StartRequestedAt.Time
	}
	return e
}

// key is an entry's place before the iid: marked (0, mark time) before
// unmarked (1).
func key(e Entry) (int, time.Time) {
	if e.MarkedAt == nil {
		return 1, time.Time{}
	}
	return 0, *e.MarkedAt
}

// sorted returns the entries in run order, done ones included where they
// would rank.
func sorted(entries []Entry) []Entry {
	s := slices.Clone(entries)
	slices.SortFunc(s, func(a, b Entry) int {
		ga, ta := key(a)
		gb, tb := key(b)
		return cmp.Or(cmp.Compare(ga, gb), ta.Compare(tb), cmp.Compare(a.IID, b.IID))
	})
	return s
}

// Rank returns the iids of the entries that are not done, in run order. The
// first one is the milestone that may start new issues.
func Rank(entries []Entry) []int64 {
	var iids []int64
	for _, e := range sorted(entries) {
		if !e.Done {
			iids = append(iids, e.IID)
		}
	}
	return iids
}

// Ahead returns, for every entry, the iids of the entries that are not done
// and rank before it, in run order. A done entry holds back nothing; its own
// list is computed from where it would rank, like any other.
func Ahead(entries []Entry) map[int64][]int64 {
	ahead := make(map[int64][]int64, len(entries))
	var open []int64
	for _, e := range sorted(entries) {
		ahead[e.IID] = slices.Clone(open)
		if !e.Done {
			open = append(open, e.IID)
		}
	}
	return ahead
}

// RunOrder returns the iids of the entries that are not done in the order
// they will run, the queue position: started milestones first (lowest iid
// first), then the marked ones (earliest mark first), then the rest by
// lowest iid. Unlike Rank, a started milestone stays ahead of a newer mark:
// its issues are already under way.
func RunOrder(entries []Entry) []int64 {
	var started, rest []int64
	for _, e := range sorted(entries) {
		switch {
		case e.Started && !e.Done:
			started = append(started, e.IID)
		case !e.Done:
			rest = append(rest, e.IID) // marks first, then by iid
		}
	}
	slices.Sort(started)
	return append(started, rest...)
}

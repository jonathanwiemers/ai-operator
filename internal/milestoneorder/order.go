// Package milestoneorder ranks the open autopilot milestones of a project in
// the order they run. The order is stored on the project
// (AgentProjectStatus.MilestoneOrder): listed milestones run in list order,
// open ones missing from the list follow by age (lowest iid first). Seed
// builds the first stored order from the run-next marks of before, so
// nothing moves on upgrade; Normalize keeps the list equal to the open
// milestones. The controller (queuedAhead), the web UI and the runner use
// it, so they cannot disagree. RunOrder is the queue position the plan agent
// and the Desk show.
package milestoneorder

import (
	"cmp"
	"slices"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Entry is one autopilot milestone of a project.
type Entry struct {
	IID int64
	// MarkedAt is the deprecated run-next mark; only Seed reads it. nil:
	// unmarked.
	MarkedAt *time.Time
	Done     bool
	// Started: the autopilot has items and is not done, so the milestone
	// is planned or running.
	Started bool
}

// EntryOf builds the entry of milestone iid from its spec task (nil: none
// yet, unmarked and not done). A mark needs both StartRequestedBy and
// StartRequestedAt: a StartRequestedBy from before marks had a time was no
// mark.
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

// Seed returns the open entries' iids in the order the run-next marks gave
// them: marked first (earliest mark first, ties by iid), then the unmarked
// ones by iid. It is the first stored order of a project, and the only
// place that still reads marks.
func Seed(entries []Entry) []int64 {
	s := slices.Clone(entries)
	key := func(e Entry) (int, time.Time) {
		if e.MarkedAt == nil {
			return 1, time.Time{}
		}
		return 0, *e.MarkedAt
	}
	slices.SortFunc(s, func(a, b Entry) int {
		ga, ta := key(a)
		gb, tb := key(b)
		return cmp.Or(cmp.Compare(ga, gb), ta.Compare(tb), cmp.Compare(a.IID, b.IID))
	})
	iids := []int64{}
	for _, e := range s {
		if !e.Done {
			iids = append(iids, e.IID)
		}
	}
	return iids
}

// Normalize returns the stored order to keep: an empty order is seeded;
// otherwise iids that are not open entries (done, closed, off autopilot) or
// listed twice are dropped and open entries missing from it are appended by
// iid. The result lists every open entry exactly once.
func Normalize(order []int64, entries []Entry) []int64 {
	if len(order) == 0 {
		return Seed(entries)
	}
	out := []int64{}
	for _, e := range sorted(order, entries) {
		if !e.Done {
			out = append(out, e.IID)
		}
	}
	return out
}

// sorted returns the entries in run order: those in order in list order,
// then the rest by iid, done ones included where they rank. An empty order
// is the seed.
func sorted(order []int64, entries []Entry) []Entry {
	if len(order) == 0 {
		order = Seed(entries)
	}
	pos := make(map[int64]int, len(order))
	for i, iid := range order {
		if _, ok := pos[iid]; !ok {
			pos[iid] = i
		}
	}
	place := func(e Entry) (int, int) {
		if i, ok := pos[e.IID]; ok {
			return 0, i
		}
		return 1, 0
	}
	s := slices.Clone(entries)
	slices.SortStableFunc(s, func(a, b Entry) int {
		ga, ia := place(a)
		gb, ib := place(b)
		return cmp.Or(cmp.Compare(ga, gb), cmp.Compare(ia, ib), cmp.Compare(a.IID, b.IID))
	})
	return s
}

// Rank returns the iids of the entries that are not done, in run order
// (stored order, then unlisted by iid). The first one is the milestone that
// may start new issues.
func Rank(order []int64, entries []Entry) []int64 {
	var iids []int64
	for _, e := range sorted(order, entries) {
		if !e.Done {
			iids = append(iids, e.IID)
		}
	}
	return iids
}

// Ahead returns, for every entry, the iids of the entries that are not done
// and rank before it, in run order. A done entry holds back nothing; its own
// list is computed from where it would rank, like any other.
func Ahead(order []int64, entries []Entry) map[int64][]int64 {
	ahead := make(map[int64][]int64, len(entries))
	var open []int64
	for _, e := range sorted(order, entries) {
		ahead[e.IID] = slices.Clone(open)
		if !e.Done {
			open = append(open, e.IID)
		}
	}
	return ahead
}

// RunOrder returns the iids of the entries that are not done in the order
// they will run, the queue position: started milestones first, then the
// rest, both in run order. Unlike Rank, a started milestone stays ahead of
// one moved before it: its issues are already under way.
func RunOrder(order []int64, entries []Entry) []int64 {
	started, rest := []int64{}, []int64{}
	for _, e := range sorted(order, entries) {
		switch {
		case e.Started && !e.Done:
			started = append(started, e.IID)
		case !e.Done:
			rest = append(rest, e.IID)
		}
	}
	return append(started, rest...)
}

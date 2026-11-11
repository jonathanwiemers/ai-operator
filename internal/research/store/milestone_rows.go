package store

import "time"

// The milestone types the UI API's views embed (web/src/generated/store.ts;
// tygo reads this file only, milestones.go has types the web never gets).
// Fields tagged json:"-" are the store's alone: each view sends them in its
// own shape or not at all.

// MilestoneRow is one milestone of the list with its progress and spend.
// Merged counts the issues merged, skipped or (backfilled) closed, Open the
// others.
// FollowUpsOpen are the leftovers still open. Spend sums the milestone's
// calls as SpendTotals does: without calls, CostUSD is unknown and every
// sum is 0, never a cost of $0. The UI's row sends the project, state,
// progress and cost its own way.
type MilestoneRow struct {
	Project       ProjectKey  `json:"-"`
	IID           int64       `json:"iid"`
	Title         string      `json:"title"`
	WebURL        string      `json:"webURL"`
	State         string      `json:"-"`
	Phase         string      `json:"phase,omitempty"`
	StartedAt     time.Time   `json:"startedAt,omitzero"`
	DoneAt        time.Time   `json:"doneAt,omitzero"`
	TrackingIID   int64       `json:"-"`
	Issues        int         `json:"-"`
	Merged        int         `json:"-"`
	Open          int         `json:"-"`
	FollowUpsOpen int         `json:"followUpsOpen"`
	Spend         SpendTotals `json:"-"`
}

// IssueFigures are one issue of a milestone with its figures. FirstRun is
// its first run.started event, nil if it never ran; MergedAt its latest
// merged event, nil if none. WallSec runs from FirstRun to MergedAt, or to
// now while it is not merged, 0 if it never ran. AgentMS sums its calls'
// durations, PausedSec the pauses of the milestone whose item is the issue
// (open ones up to now). Spend sums its calls as MilestoneRow does: without
// calls the cost is unknown. The UI sends the issue's fields in its issue
// row, the agent time in seconds and the spend as a cost chip.
type IssueFigures struct {
	IID       int64       `json:"-"`
	Title     string      `json:"-"`
	State     string      `json:"-"`
	MRIID     int64       `json:"-"`
	MRURL     string      `json:"-"`
	Position  int         `json:"-"`
	FirstRun  *time.Time  `json:"-"`
	MergedAt  *time.Time  `json:"-"`
	WallSec   int64       `json:"wallSec"`
	AgentMS   int64       `json:"-"`
	PausedSec int64       `json:"pausedSec"`
	Spend     SpendTotals `json:"-"`
}

// FollowUpRow is a stored leftover of a milestone's retrospective, see
// FollowUpSnapshot. Title and Body are untrusted markdown without HTML.
type FollowUpRow struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	State     string `json:"state" tstype:"'open' | 'issue' | 'milestone' | 'dismissed'"`
	TargetIID int64  `json:"targetIID,omitempty"`
}

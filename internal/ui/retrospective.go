package ui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// RetrospectiveView is a milestone's retrospective, live from task status
// or stored. Text, follow-up titles and bodies are untrusted markdown
// without HTML. NoteURL is its comment on the tracking issue; Failed says
// why there is none (live only).
type RetrospectiveView struct {
	Text      string         `json:"text"`
	PostedAt  *metav1.Time   `json:"postedAt,omitempty"`
	NoteURL   string         `json:"noteURL,omitempty"`
	Failed    string         `json:"failed,omitempty"`
	FollowUps []FollowUpView `json:"followUps"`
	// CostDrivers are the milestone's top issues and roles by cost, absent
	// without the history database.
	CostDrivers []CostDriverView `json:"costDrivers,omitempty"`
	// Draft is the pilot's draft of a follow-up milestone, absent while
	// none was asked for or the pilot is gone.
	Draft *FollowUpDraftView `json:"draft,omitempty"`
}

// FollowUpDraftView is the pilot's draft of a follow-up milestone from the
// follow-ups Numbers. Title and Description are untrusted markdown without
// HTML until an owner confirms them. Pending: the pilot is writing it, with
// no title or description yet. Error says why there is none.
type FollowUpDraftView struct {
	Numbers     []int        `json:"numbers"`
	Pending     bool         `json:"pending,omitempty"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	At          *metav1.Time `json:"at,omitempty"`
	Error       string       `json:"error,omitempty"`
}

// followUpDraftView is the draft of the pilot coord: the one asked for while
// it is written, else the last one written; nil without either.
func followUpDraftView(coord *aiv1.AgentTask) *FollowUpDraftView {
	if coord == nil {
		return nil
	}
	req := coord.Spec.Source.FollowUpDraft
	var d *aiv1.PilotFollowUpDraft
	if ps := coord.Status.Pilot; ps != nil {
		d = ps.FollowUpDraft
	}
	switch {
	case req != nil && (d == nil || d.Request < req.Request):
		return &FollowUpDraftView{Numbers: slices.Clone(req.Numbers), Pending: true}
	case d != nil:
		at := d.At
		return &FollowUpDraftView{Numbers: slices.Clone(d.Numbers), Title: d.Title, Description: d.Description, At: &at, Error: d.Error}
	}
	return nil
}

// openFollowUps reports whether a follow-up of r is still open.
func openFollowUps(r *RetrospectiveView) bool {
	return r != nil && slices.ContainsFunc(r.FollowUps, func(f FollowUpView) bool { return f.State == store.FollowUpOpen })
}

// FollowUpView is a leftover of a retrospective and what came of it:
// TargetURL is the issue or milestone it became.
type FollowUpView struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	State     string `json:"state" tstype:"'open' | 'issue' | 'milestone' | 'dismissed'"`
	TargetIID int64  `json:"targetIID,omitempty"`
	TargetURL string `json:"targetURL,omitempty"`
}

// CostDriverView is an issue (label "#42") or a role of the milestone's
// calls, with its cost ("" when unknown) and tokens.
type CostDriverView struct {
	Label     string `json:"label"`
	Kind      string `json:"kind" tstype:"'issue' | 'role'"`
	CostUSD   string `json:"costUSD"`
	CostKnown bool   `json:"costKnown"`
	Tokens    int64  `json:"tokens"`
}

// maxCostDrivers bounds RetrospectiveView.CostDrivers.
const maxCostDrivers = 5

// followUpView is follow-up n with its target's link under the project's
// web URL.
func followUpView(web string, f store.FollowUpRow) FollowUpView {
	v := FollowUpView{Number: f.Number, Title: f.Title, Body: f.Body, State: f.State, TargetIID: f.TargetIID}
	switch {
	case f.TargetIID == 0:
	case f.State == store.FollowUpIssue:
		v.TargetURL = fmt.Sprintf("%s/-/issues/%d", web, f.TargetIID)
	case f.State == store.FollowUpMilestone:
		v.TargetURL = fmt.Sprintf("%s/-/milestones/%d", web, f.TargetIID)
	}
	return v
}

// noteURL is the comment id on the tracking issue, "" without either.
func noteURL(tr *TrackingIssueRef, id int64) string {
	if tr == nil || id == 0 {
		return ""
	}
	return fmt.Sprintf("%s#note_%d", tr.WebURL, id)
}

// liveRetrospective is the retrospective in the status of the spec task,
// else of its pilot (before the copy), nil without one.
func liveRetrospective(web string, tr *TrackingIssueRef, tasks ...*aiv1.AgentTask) *RetrospectiveView {
	for _, t := range tasks {
		if t == nil || t.Status.Pilot == nil || t.Status.Pilot.Retrospective == nil {
			continue
		}
		r := t.Status.Pilot.Retrospective
		v := &RetrospectiveView{Text: r.Text, PostedAt: r.PostedAt, NoteURL: noteURL(tr, r.NoteID), Failed: r.Failed, FollowUps: []FollowUpView{}}
		for i, f := range r.FollowUps {
			row := store.FollowUpRow{Number: i + 1, Title: f.Title, Body: f.Body, State: store.FollowUpOpen}
			if iid := r.Created[strconv.Itoa(i+1)]; iid != 0 {
				row.State, row.TargetIID = store.FollowUpIssue, iid
			} else if iid := r.Taken[strconv.Itoa(i+1)]; iid != 0 {
				row.State, row.TargetIID = store.FollowUpMilestone, iid
			} else if r.DismissedAt != nil {
				row.State = store.FollowUpDismissed
			}
			v.FollowUps = append(v.FollowUps, followUpView(web, row))
		}
		return v
	}
	return nil
}

// storedRetrospective is the snapshot's retrospective, nil without one.
func storedRetrospective(web string, tr *TrackingIssueRef, d store.MilestoneDetail) *RetrospectiveView {
	if d.RetroText == "" && d.RetroNoteID == 0 && len(d.FollowUps) == 0 {
		return nil
	}
	v := &RetrospectiveView{Text: d.RetroText, NoteURL: noteURL(tr, d.RetroNoteID), FollowUps: []FollowUpView{}}
	if !d.RetroPostedAt.IsZero() {
		v.PostedAt = &metav1.Time{Time: d.RetroPostedAt}
	}
	for _, f := range d.FollowUps {
		v.FollowUps = append(v.FollowUps, followUpView(web, f))
	}
	return v
}

// costDrivers are the top issues and roles of sp by cost; those of unknown
// cost follow by tokens. Roles span issues, so the two kinds overlap.
func costDrivers(sp store.Spend) []CostDriverView {
	type driver struct {
		CostDriverView
		usd float64
	}
	var all []driver
	add := func(label, kind string, t store.SpendTotals) {
		all = append(all, driver{CostDriverView{Label: label, Kind: kind, CostUSD: t.CostUSD.String(),
			CostKnown: t.CostUSD.Known, Tokens: t.Tokens()}, t.CostUSD.USD})
	}
	for _, c := range sp.Children {
		if c.Level == store.SpendIssue {
			add(c.Label, "issue", c.Totals)
		}
	}
	for _, b := range sp.ByRole {
		add(b.Key, "role", b.Totals)
	}
	slices.SortStableFunc(all, func(a, b driver) int {
		if a.CostKnown != b.CostKnown {
			if a.CostKnown {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.usd, a.usd), cmp.Compare(b.Tokens, a.Tokens))
	})
	out := []CostDriverView{}
	for _, d := range all[:min(len(all), maxCostDrivers)] {
		out = append(out, d.CostDriverView)
	}
	return out
}

// spendReader reads a scope's spend breakdown (the history store).
type spendReader interface {
	Spend(ctx context.Context, sc store.SpendScope) (store.Spend, error)
}

// retroSpendStore is the reader of cost drivers, nil without the history
// database.
func (s *Server) retroSpendStore() spendReader {
	if s.retroSpend != nil {
		return s.retroSpend
	}
	if st := s.spendStore(); st != nil {
		return st
	}
	return nil
}

// addCostDrivers sets the cost drivers of the milestone's retrospective r
// in one call; they stay absent without r, the database or when it fails
// (logged).
func (s *Server) addCostDrivers(ctx context.Context, ap *aiv1.AgentProject, iid int64, r *RetrospectiveView) {
	st := s.retroSpendStore()
	if r == nil || st == nil {
		return
	}
	sp, err := st.Spend(ctx, store.SpendScope{Namespace: ap.Namespace, Project: ap.Name, MilestoneIID: iid})
	if err != nil {
		s.log.Error(err, "cannot read the milestone's cost drivers", "namespace", ap.Namespace, "project", ap.Name, "milestone", iid)
		return
	}
	r.CostDrivers = costDrivers(sp)
}

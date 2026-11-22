package ui

import (
	"net/http"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// PauseBucketView is the milestone pauses that started in one bucket (its
// local start date, YYYY-MM-DD): their number and paused time, in total and
// by kind. A bucket without pauses has zeros and an empty byKind.
type PauseBucketView struct {
	Bucket    string                          `json:"bucket"`
	Count     int                             `json:"count"`
	PausedSec int64                           `json:"pausedSec"`
	ByKind    map[string]store.PauseKindTotal `json:"byKind" tstype:"{ [kind: string]: store.PauseKindTotal }"`
}

// PauseProjectView is the pauses of one project (namespace/name) in the
// range.
type PauseProjectView struct {
	Namespace string `json:"namespace"`
	Project   string `json:"project"`
	Count     int    `json:"count"`
	PausedSec int64  `json:"pausedSec"`
}

// LongPauseView is one of the longest pauses of the range. Seconds is its
// full length, up to now while Open. DeskURL is the milestone's Pilot tab on
// the Desk, a path of the Desk's routes.
type LongPauseView struct {
	Namespace    string    `json:"namespace"`
	Project      string    `json:"project"`
	MilestoneIID int64     `json:"milestoneIID"`
	Kind         string    `json:"kind" tstype:"aiv1.PauseKind | 'unknown'"`
	Reason       string    `json:"reason,omitempty"`
	Seconds      int64     `json:"seconds"`
	Since        time.Time `json:"since"`
	Open         bool      `json:"open"`
	DeskURL      string    `json:"deskURL"`
}

// MetricsPausesView is the answer of GET /api/v1/metrics/pauses: the
// milestone pauses of the viewer's projects (or ?project=) that started in
// the range of local dates in TZ, From to To inclusive. A pause counts in
// the bucket where it started, with its full length; an open one up to now.
// Buckets has one entry per bucket start of the range. Totals' mean time to
// resume is of the resumed pauses; OpenNow counts those still open. ByProject
// is longest paused first, Longest the ten longest pauses. Truncated: more
// than 20000 pause events since From, only the newest were read. The kind
// and engine filters do not apply.
type MetricsPausesView struct {
	Range     string             `json:"range" tstype:"'7d' | '30d' | '90d' | 'month' | 'lastMonth' | '12m' | 'custom'"`
	From      string             `json:"from"`
	To        string             `json:"to"`
	Bucket    string             `json:"bucket" tstype:"'day' | 'week' | 'month'"`
	TZ        string             `json:"tz"`
	Buckets   []PauseBucketView  `json:"buckets"`
	Totals    store.PauseTotals  `json:"totals"`
	OpenNow   int                `json:"openNow"`
	ByProject []PauseProjectView `json:"byProject"`
	Longest   []LongPauseView    `json:"longest"`
	Truncated bool               `json:"truncated"`
}

// metricsPauses answers the Metrics page's Pauses section over the viewer's
// projects, for the parameters of costs (group, kind and engine are read and
// checked, not used).
func (s *Server) metricsPauses(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	p, _, _, st, ok := s.costRequest(w, r, now)
	if !ok {
		return
	}
	// Pauses are no calls: only the range and the projects apply.
	q := store.CostQuery{Projects: p.q.Projects, From: p.q.From, To: p.q.To, Loc: p.q.Loc}
	pr, err := st.PausesInRange(r.Context(), q, p.bucket, now)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metricsPausesView(p, pr))
}

// metricsPausesView turns the store's pauses of p's range into the view.
func metricsPausesView(p costParams, pr store.PauseRange) MetricsPausesView {
	v := MetricsPausesView{
		Range: p.rng, From: p.first.Format(time.DateOnly), To: p.last.Format(time.DateOnly), Bucket: p.bucket, TZ: p.q.Loc.String(),
		Buckets: []PauseBucketView{}, Totals: pr.Totals, OpenNow: pr.OpenNow, ByProject: []PauseProjectView{},
		Longest: []LongPauseView{}, Truncated: pr.Truncated,
	}
	byBucket := map[string]store.PauseBucket{}
	for _, b := range pr.Buckets {
		byBucket[b.Bucket] = b
	}
	for _, start := range bucketStarts(p.first, p.last, p.bucket) {
		b := PauseBucketView{Bucket: start, ByKind: map[string]store.PauseKindTotal{}}
		if got, ok := byBucket[start]; ok {
			b.Count, b.PausedSec, b.ByKind = got.Count, got.PausedSec, got.ByKind
		}
		v.Buckets = append(v.Buckets, b)
	}
	for _, pp := range pr.ByProject {
		v.ByProject = append(v.ByProject, PauseProjectView{Namespace: pp.Namespace, Project: pp.Project, Count: pp.Count, PausedSec: pp.PausedSec})
	}
	for _, l := range pr.Longest {
		v.Longest = append(v.Longest, LongPauseView{
			Namespace: l.Namespace, Project: l.Project, MilestoneIID: l.MilestoneIID, Kind: l.Kind, Reason: l.Reason,
			Seconds: l.Seconds, Since: l.Since, Open: l.Until == nil,
			DeskURL: desklink.MilestonePath(l.Namespace, l.Project, l.MilestoneIID) + "?tab=pilot",
		})
	}
	return v
}

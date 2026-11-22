package ui

import (
	"net/http"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// FlowBucketView is the issues merged in one bucket (its local start date,
// YYYY-MM-DD). A bucket without merges has 0.
type FlowBucketView struct {
	Bucket string `json:"bucket"`
	Merged int    `json:"merged"`
}

// FlowTotalsView is the figures of the issues merged in the range. The
// median wall time (first run start to merge) is of the wallIssues with a
// run start, the median agent time (the sum of the issue's call durations)
// of the agentIssues with calls; a median of no issues is 0. The review and
// fix calls are means per merged issue. An issue is measured whole: its
// runs and calls before the range count.
type FlowTotalsView struct {
	MergedIssues        int     `json:"mergedIssues"`
	WallIssues          int     `json:"wallIssues"`
	MedianWallSec       int64   `json:"medianWallSec"`
	AgentIssues         int     `json:"agentIssues"`
	MedianAgentSec      int64   `json:"medianAgentSec"`
	ReviewCallsPerIssue float64 `json:"reviewCallsPerIssue"`
	FixCallsPerIssue    float64 `json:"fixCallsPerIssue"`
}

// MetricsFlowView is the answer of GET /api/v1/metrics/flow: the issues of
// the viewer's projects (or ?project=) merged in the range of local dates
// in TZ, From to To inclusive, each once, at its latest merge in the range.
// Buckets has one entry per bucket start of the range, by merge time. The
// kind and engine filters do not apply, and research calls never count.
type MetricsFlowView struct {
	Range   string           `json:"range" tstype:"'7d' | '30d' | '90d' | 'month' | 'lastMonth' | '12m' | 'custom'"`
	From    string           `json:"from"`
	To      string           `json:"to"`
	Bucket  string           `json:"bucket" tstype:"'day' | 'week' | 'month'"`
	TZ      string           `json:"tz"`
	Buckets []FlowBucketView `json:"buckets"`
	Totals  FlowTotalsView   `json:"totals"`
}

// metricsFlow answers the Metrics page's Flow section over the viewer's
// projects, for the parameters of costs (group, kind and engine are read and
// checked, not used).
func (s *Server) metricsFlow(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	p, _, _, st, ok := s.costRequest(w, r, now)
	if !ok {
		return
	}
	// A merged issue counts whole: only the range and the projects apply.
	q := store.CostQuery{Projects: p.q.Projects, From: p.q.From, To: p.q.To, Loc: p.q.Loc}
	fr, err := st.FlowInRange(r.Context(), q, p.bucket, now)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metricsFlowView(p, fr))
}

// metricsFlowView turns the store's flow of p's range into the view.
func metricsFlowView(p costParams, fr store.FlowRange) MetricsFlowView {
	t := fr.Totals
	v := MetricsFlowView{
		Range: p.rng, From: p.first.Format(time.DateOnly), To: p.last.Format(time.DateOnly), Bucket: p.bucket, TZ: p.q.Loc.String(),
		Buckets: []FlowBucketView{},
		Totals: FlowTotalsView{MergedIssues: t.MergedIssues, WallIssues: t.WallIssues, MedianWallSec: t.MedianWallSec,
			AgentIssues: t.AgentIssues, MedianAgentSec: t.MedianAgentSec,
			ReviewCallsPerIssue: t.ReviewCallsPerIssue, FixCallsPerIssue: t.FixCallsPerIssue},
	}
	merged := map[string]int{}
	for _, b := range fr.Buckets {
		merged[b.Bucket] = b.Merged
	}
	for _, start := range bucketStarts(p.first, p.last, p.bucket) {
		v.Buckets = append(v.Buckets, FlowBucketView{Bucket: start, Merged: merged[start]})
	}
	return v
}

package ui

import (
	"net/http"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// CodeBucketView is the lines of one bucket (its local start date,
// YYYY-MM-DD): written is the calls' lines by call time, merged the lines of
// the merged and companion.merged events by event time, by category, partial
// when some part came without a count. A bucket without lines has zeros.
type CodeBucketView struct {
	Bucket  string            `json:"bucket"`
	Written store.LineCount   `json:"written"`
	Merged  store.MergedLines `json:"merged"`
}

// CodeTotalsView is the lines of the range. Written and merged sum the
// buckets. Issues is the issues with a merged event in the range, each whole
// (its companions and calls at any time): merged and measured count them,
// lines sums the measured ones, costPer100USD is their calls' cost per 100
// merged production lines, left out when unknown or without production
// lines. Cost is those calls' totals, for the cost's notes.
type CodeTotalsView struct {
	Written store.LineCount        `json:"written"`
	Merged  store.MergedLines      `json:"merged"`
	Issues  store.MergedLineTotals `json:"issues"`
	Cost    store.SpendTotals      `json:"cost"`
}

// CodeProjectView is the lines of one project; a companion's lines count
// under its home project.
type CodeProjectView struct {
	Namespace string         `json:"namespace"`
	Project   string         `json:"project"`
	Totals    CodeTotalsView `json:"totals"`
}

// MetricsCodeView is the answer of GET /api/v1/metrics/code: the lines of
// the viewer's projects (or ?project=) in the range of local dates in TZ,
// From to To inclusive. Buckets has one entry per bucket start of the range.
// ByProject lists the projects with figures, by namespace and name, and is
// empty with ?project=. The kind and engine filters do not apply, and
// research calls never count.
type MetricsCodeView struct {
	Range     string            `json:"range" tstype:"'7d' | '30d' | '90d' | 'month' | 'lastMonth' | '12m' | 'custom'"`
	From      string            `json:"from"`
	To        string            `json:"to"`
	Bucket    string            `json:"bucket" tstype:"'day' | 'week' | 'month'"`
	TZ        string            `json:"tz"`
	Buckets   []CodeBucketView  `json:"buckets"`
	Totals    CodeTotalsView    `json:"totals"`
	ByProject []CodeProjectView `json:"byProject"`
}

// metricsCode answers the Metrics page's Code section over the viewer's
// projects, for the parameters of costs (group, kind and engine are read and
// checked, not used).
func (s *Server) metricsCode(w http.ResponseWriter, r *http.Request) {
	p, _, _, st, ok := s.costRequest(w, r, time.Now())
	if !ok {
		return
	}
	// Lines are no cost: only the range and the projects apply.
	q := store.CostQuery{Projects: p.q.Projects, From: p.q.From, To: p.q.To, Loc: p.q.Loc}
	cr, err := st.CodeInRange(r.Context(), q, p.bucket)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metricsCodeView(p, cr, r.URL.Query().Get("project") == ""))
}

// metricsCodeView turns the store's lines of p's range into the view, with
// the per-project rows when perProject.
func metricsCodeView(p costParams, cr store.CodeRange, perProject bool) MetricsCodeView {
	v := MetricsCodeView{
		Range: p.rng, From: p.first.Format(time.DateOnly), To: p.last.Format(time.DateOnly), Bucket: p.bucket, TZ: p.q.Loc.String(),
		Buckets: []CodeBucketView{}, Totals: codeTotalsView(cr.Totals), ByProject: []CodeProjectView{},
	}
	buckets := map[string]store.CodeBucket{}
	for _, b := range cr.Buckets {
		buckets[b.Bucket] = b
	}
	for _, start := range bucketStarts(p.first, p.last, p.bucket) {
		b := buckets[start]
		v.Buckets = append(v.Buckets, CodeBucketView{Bucket: start, Written: b.Written, Merged: b.Merged})
	}
	if perProject {
		for _, cp := range cr.ByProject {
			v.ByProject = append(v.ByProject, CodeProjectView{Namespace: cp.Namespace, Project: cp.Project, Totals: codeTotalsView(cp.CodeTotals)})
		}
	}
	return v
}

func codeTotalsView(t store.CodeTotals) CodeTotalsView {
	return CodeTotalsView{Written: t.Written, Merged: t.Merged, Issues: t.Issues, Cost: t.Cost}
}

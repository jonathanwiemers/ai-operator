package ui

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// CostPointView is the totals of one bucket (its local start date,
// YYYY-MM-DD) and group key: namespace/name of a project, a kind, model or
// engine, "" for legacy runs ("before calls") and in the month series.
// Other is the sum of the keys beyond the largest ones, key "" then.
type CostPointView struct {
	Bucket string            `json:"bucket"`
	Key    string            `json:"key"`
	Other  bool              `json:"other"`
	Totals store.SpendTotals `json:"totals"`
}

// CostsMonthView is the This month chart's days, independent of the range:
// this calendar month up to today (day Today of the month) and all of last
// month, by day, one series, with the page's filters.
type CostsMonthView struct {
	Today    int             `json:"today"`
	Current  []CostPointView `json:"current"`
	Previous []CostPointView `json:"previous"`
}

// CostsView is the answer of GET /api/v1/costs: what the viewer's projects
// cost in a range of local dates in TZ, From to To inclusive (YYYY-MM-DD).
// Earliest is the oldest From the history retention allows. Buckets lists
// every bucket start in the range; Series has a point per bucket and key
// with calls only. Previous is the period of equal length before From.
// ResearchSince is the first research call still stored, absent without
// one or when the viewer may not see research.
type CostsView struct {
	Range                  string               `json:"range" tstype:"'7d' | '30d' | '90d' | 'month' | 'lastMonth' | '12m' | 'custom'"`
	From                   string               `json:"from"`
	To                     string               `json:"to"`
	Bucket                 string               `json:"bucket" tstype:"'day' | 'week' | 'month'"`
	TZ                     string               `json:"tz"`
	Group                  string               `json:"group" tstype:"'project' | 'kind' | 'model' | 'engine'"`
	Earliest               string               `json:"earliest"`
	Buckets                []string             `json:"buckets"`
	Series                 []CostPointView      `json:"series"`
	Totals                 store.SpendTotals    `json:"totals"`
	Previous               store.SpendTotals    `json:"previous"`
	CacheReadShare         float64              `json:"cacheReadShare"`
	PreviousCacheReadShare float64              `json:"previousCacheReadShare"`
	StepLines              StepLinesView        `json:"stepLines"`
	ByProject              []SpendBreakdownView `json:"byProject"`
	ByKind                 []SpendBreakdownView `json:"byKind"`
	ByModel                []SpendBreakdownView `json:"byModel"`
	ByEngine               []SpendBreakdownView `json:"byEngine"`
	Month                  CostsMonthView       `json:"month"`
	ResearchSince          *time.Time           `json:"researchSince,omitempty"`
}

// costRanges are the named ranges of ?range=, costGroups the groupings of
// ?group=, the first of each the default.
var (
	costRanges = []string{"30d", "7d", "90d", "month", "lastMonth", "12m"}
	costGroups = []string{"project", "kind", "model", "engine"}
	callKinds  = []string{aiv1.CallKindSpec, aiv1.CallKindImplement, aiv1.CallKindReview, aiv1.CallKindFix, aiv1.CallKindCI,
		aiv1.CallKindConflict, aiv1.CallKindDescribe, aiv1.CallKindPilot, aiv1.CallKindRetro, aiv1.CallKindFollowUpDraft,
		aiv1.CallKindChat, aiv1.CallKindResearch}
	engines = []string{string(aiv1.EngineClaude), string(aiv1.EngineCursor)}
)

// maxSeriesKeys bounds the series' keys: the others fold into one point per
// bucket.
const maxSeriesKeys = 20

// costReader reads the Costs page's figures: the history store, a fake in
// tests.
type costReader interface {
	CostSeries(ctx context.Context, q store.CostQuery, bucket, group string) ([]store.CostPoint, error)
	CostBreakdowns(ctx context.Context, q store.CostQuery) (store.CostBreakdowns, error)
}

// costStore is the reader of the costs, nil without the history database.
func (s *Server) costStore() costReader {
	if s.costReads != nil {
		return s.costReads
	}
	if st := s.research.Load(); st != nil {
		return st
	}
	return nil
}

// costParams is a costs request: the range of local dates first to last
// (midnights in q.Loc) and its bucket, the group and the store query over
// [first, last+1 day).
type costParams struct {
	rng                          string
	first, last, today, earliest time.Time
	bucket, group                string
	q                            store.CostQuery
}

// errCostProject: ?project= names a project the viewer may not see.
var errCostProject = errors.New("project not visible")

// costParamError is a bad parameter, answered with 400.
type costParamError string

func (e costParamError) Error() string { return string(e) }

// parseCostParams reads the costs parameters of r over the visible projects
// (namespace/name) at now: range or from and to, group, project, kind,
// engine and tz. The viewer's calls of kind research count only with
// research read access. A project not in visible is errCostProject, any
// other bad value a costParamError.
func (s *Server) parseCostParams(r *http.Request, visible map[string]*aiv1.AgentProject, now time.Time) (costParams, error) {
	q := r.URL.Query()
	loc := time.UTC
	if tz := q.Get("tz"); tz != "" {
		var err error
		if loc, err = time.LoadLocation(tz); err != nil || tz == "Local" {
			return costParams{}, costParamError("unknown time zone")
		}
	}
	p := costParams{today: localDay(now, loc), group: cmp.Or(q.Get("group"), costGroups[0])}
	p.earliest = p.today.AddDate(0, 0, -int(s.historyRetention/(24*time.Hour)))
	from, to := q.Get("from"), q.Get("to")
	if from != "" || to != "" {
		if q.Get("range") != "" {
			return costParams{}, costParamError("give range or from and to, not both")
		}
		if from == "" || to == "" {
			return costParams{}, costParamError("from and to are both required")
		}
		var err1, err2 error
		p.first, err1 = time.ParseInLocation(time.DateOnly, from, loc)
		p.last, err2 = time.ParseInLocation(time.DateOnly, to, loc)
		switch {
		case err1 != nil || err2 != nil:
			return costParams{}, costParamError("from and to must be dates, YYYY-MM-DD")
		case p.first.After(p.last):
			return costParams{}, costParamError("from is after to")
		case p.last.After(p.today):
			return costParams{}, costParamError("to is after today")
		case p.first.Before(p.earliest):
			return costParams{}, costParamError("from is before " + p.earliest.Format(time.DateOnly) + ", the history retention")
		}
		p.rng = "custom"
	} else {
		p.rng = cmp.Or(q.Get("range"), costRanges[0])
		var ok bool
		if p.first, p.last, ok = namedRange(p.rng, p.today); !ok {
			return costParams{}, costParamError("range must be one of " + strings.Join(costRanges, ", "))
		}
		// A named range keeps to the history retention: older calls are gone.
		if p.first.Before(p.earliest) {
			p.first = p.earliest
		}
		if p.first.After(p.last) {
			return costParams{}, costParamError("the range is older than the history retention")
		}
	}
	if !slices.Contains(costGroups, p.group) {
		return costParams{}, costParamError("group must be one of " + strings.Join(costGroups, ", "))
	}
	p.q = store.CostQuery{From: p.first, To: p.last.AddDate(0, 0, 1), Loc: loc, Kind: q.Get("kind"), Engine: q.Get("engine")}
	if p.q.Kind != "" && !slices.Contains(callKinds, p.q.Kind) {
		return costParams{}, costParamError("unknown kind")
	}
	if p.q.Engine != "" && !slices.Contains(engines, p.q.Engine) {
		return costParams{}, costParamError("engine must be one of " + strings.Join(engines, ", "))
	}
	if read, _ := s.authz.ResearchAccess(principalOf(r)); !read {
		p.q.ExcludeKinds = []string{aiv1.CallKindResearch}
	}
	if ref := q.Get("project"); ref != "" {
		ap := visible[ref]
		if ap == nil {
			return costParams{}, errCostProject
		}
		p.q.Projects = []store.ProjectKey{{Namespace: ap.Namespace, Name: ap.Name}}
	} else {
		for _, ref := range slices.Sorted(maps.Keys(visible)) {
			p.q.Projects = append(p.q.Projects, store.ProjectKey{Namespace: visible[ref].Namespace, Name: visible[ref].Name})
		}
	}
	p.bucket = spanBucket(dayNumber(p.last) - dayNumber(p.first) + 1)
	return p, nil
}

// localDay is the midnight of t's date in loc.
func localDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// firstOfMonth is the midnight of the first of t's month, in t's location.
func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

// namedRange is the first and last date of a named range ending today;
// 12m starts on the first of the month 11 months back.
func namedRange(rng string, today time.Time) (first, last time.Time, ok bool) {
	switch rng {
	case "7d":
		return today.AddDate(0, 0, -6), today, true
	case "30d":
		return today.AddDate(0, 0, -29), today, true
	case "90d":
		return today.AddDate(0, 0, -89), today, true
	case "month":
		return firstOfMonth(today), today, true
	case "lastMonth":
		this := firstOfMonth(today)
		return this.AddDate(0, -1, 0), this.AddDate(0, 0, -1), true
	case "12m":
		return firstOfMonth(today).AddDate(0, -11, 0), today, true
	}
	return time.Time{}, time.Time{}, false
}

// dayNumber counts t's date in days since 1970-01-01, independent of the
// zone's offsets.
func dayNumber(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// spanBucket is the bucket of a range of days: day up to 90 days, week up
// to 180, month above.
func spanBucket(days int) string {
	switch {
	case days <= 90:
		return "day"
	case days <= 180:
		return "week"
	}
	return "month"
}

// bucketStarts lists the start of every bucket from first's to last's, as
// the store truncates: weeks start on Monday, months on the first.
func bucketStarts(first, last time.Time, bucket string) []string {
	y, m, d := first.Date()
	b := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	step := func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
	switch bucket {
	case "week":
		b = b.AddDate(0, 0, -(int(b.Weekday())+6)%7)
		step = func(t time.Time) time.Time { return t.AddDate(0, 0, 7) }
	case "month":
		b = time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
		step = func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }
	}
	y, m, d = last.Date()
	end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	out := []string{}
	for ; !b.After(end); b = step(b) {
		out = append(out, b.Format(time.DateOnly))
	}
	return out
}

// foldSeries keeps the keep keys of points with the largest known cost (then
// by key) and sums the others into one Other point per bucket, after the
// bucket's kept points. Points come ordered by bucket.
func foldSeries(points []store.CostPoint, keep int) []CostPointView {
	cost := map[string]float64{}
	for _, p := range points {
		known := 0.0
		if p.Totals.CostUSD.Known {
			known = p.Totals.CostUSD.USD
		}
		cost[p.Key] += known
	}
	keys := slices.SortedFunc(maps.Keys(cost), func(a, b string) int {
		return cmp.Or(cmp.Compare(cost[b], cost[a]), cmp.Compare(a, b))
	})
	kept := map[string]bool{}
	for _, k := range keys[:min(keep, len(keys))] {
		kept[k] = true
	}
	out := make([]CostPointView, 0, len(points))
	var other []CostPointView
	for _, p := range points {
		if kept[p.Key] {
			out = append(out, CostPointView{Bucket: p.Bucket, Key: p.Key, Totals: p.Totals})
		} else if n := len(other); n > 0 && other[n-1].Bucket == p.Bucket {
			other[n-1].Totals = other[n-1].Totals.Add(p.Totals)
		} else {
			other = append(other, CostPointView{Bucket: p.Bucket, Other: true, Totals: p.Totals})
		}
	}
	// Stable: within a bucket the kept points stay first, in key order.
	out = append(out, other...)
	slices.SortStableFunc(out, func(a, b CostPointView) int { return cmp.Compare(a.Bucket, b.Bucket) })
	return out
}

// costs answers the Costs page: the series of the chosen grouping, the
// totals and breakdowns of the range and the previous period, and the
// month's days, over the viewer's projects (or ?project=).
func (s *Server) costs(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Research.Enabled() {
		notFound(w)
		return
	}
	visible, _, ok := s.visibleProjects(w, r)
	if !ok {
		return
	}
	p, err := s.parseCostParams(r, visible, time.Now())
	if errors.Is(err, errCostProject) {
		notFound(w)
		return
	} else if err != nil {
		badRequest(w, err.Error())
		return
	}
	st := s.costStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "the history database is not connected yet")
		return
	}
	v, err := costsView(r.Context(), st, p)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// costsView reads p's figures from st.
func costsView(ctx context.Context, st costReader, p costParams) (CostsView, error) {
	points, err := st.CostSeries(ctx, p.q, p.bucket, p.group)
	if err != nil {
		return CostsView{}, err
	}
	b, err := st.CostBreakdowns(ctx, p.q)
	if err != nil {
		return CostsView{}, err
	}
	// The month's days in one query: the first of last month to today.
	this := firstOfMonth(p.today)
	mq := p.q
	mq.From, mq.To = this.AddDate(0, -1, 0), p.today.AddDate(0, 0, 1)
	days, err := st.CostSeries(ctx, mq, "day", "")
	if err != nil {
		return CostsView{}, err
	}
	month := CostsMonthView{Today: p.today.Day(), Current: []CostPointView{}, Previous: []CostPointView{}}
	cut := this.Format(time.DateOnly)
	for _, d := range days {
		pv := CostPointView{Bucket: d.Bucket, Key: d.Key, Totals: d.Totals}
		if d.Bucket < cut {
			month.Previous = append(month.Previous, pv)
		} else {
			month.Current = append(month.Current, pv)
		}
	}
	return CostsView{
		Range: p.rng, From: p.first.Format(time.DateOnly), To: p.last.Format(time.DateOnly), Bucket: p.bucket,
		TZ: p.q.Loc.String(), Group: p.group, Earliest: p.earliest.Format(time.DateOnly),
		Buckets: bucketStarts(p.first, p.last, p.bucket), Series: foldSeries(points, maxSeriesKeys),
		Totals: b.Totals, Previous: b.Previous, CacheReadShare: b.CacheReadShare, PreviousCacheReadShare: b.PreviousCacheReadShare,
		StepLines: StepLinesView(b.StepLines), ByProject: breakdownViews(b.ByProject), ByKind: breakdownViews(b.ByKind),
		ByModel: breakdownViews(b.ByModel), ByEngine: breakdownViews(b.ByEngine), Month: month, ResearchSince: b.ResearchSince,
	}, nil
}

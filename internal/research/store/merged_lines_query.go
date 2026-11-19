package store

import (
	"context"
	"fmt"
	"strings"
)

// lineCategories are MergedLines's categories in the order of the line
// columns, see lineDest.
var lineCategories = []string{"production", "tests", "docs", "generated"}

// lineColumns are the names of the line columns of issue_lines: per
// category its added and deleted lines.
var lineColumns = func() []string {
	var cols []string
	for _, c := range lineCategories {
		cols = append(cols, c+"_added", c+"_deleted")
	}
	return cols
}()

// issueLinesCTE is the common table expression issue_lines: the merged lines
// of each issue (namespace, project, milestone_iid, issue_iid) whose merged
// or companion.merged events, not expired, match where (a condition on the
// columns of history_events). Append it to a WITH list. Its columns:
//   - merged_at: the issue's latest merged event, NULL if it has none (only
//     a companion merged);
//   - measured: its merged event carries lines;
//   - the line columns (lineColumns): the sums of data.lines over both kinds,
//     companions included;
//   - partial: some part is partial, or a companion merged without lines;
//   - companions: the companion events as a JSON array of CompanionLines,
//     by project and MR.
//
// Only a measured issue's sums mean anything: an unmeasured one is never 0
// lines.
func issueLinesCTE(where string) string {
	return `issue_lines AS (
		SELECT namespace, project, milestone_iid, issue_iid,
			max(at) FILTER (WHERE kind = 'merged') AS merged_at,
			coalesce(bool_or(kind = 'merged' AND data ? 'lines'), false) AS measured,
			` + eventLineSums + ` AS partial,
			coalesce(jsonb_agg(jsonb_build_object('project', coalesce(data->>'companion', ''), 'mrIID', coalesce(mr_iid, 0),
				'lines', data->'lines') ORDER BY data->>'companion', mr_iid) FILTER (WHERE kind = 'companion.merged'), '[]'::jsonb)
				AS companions
		FROM history_events
		WHERE kind IN ('merged', 'companion.merged') AND issue_iid IS NOT NULL AND expires_at > now() AND ` + where + `
		GROUP BY 1, 2, 3, 4
	) `
}

// eventLineSums aggregate merged and companion.merged rows of
// history_events into the line columns (lineColumns, by their names) and
// partial (unnamed, last): the sums of data.lines, 0 where it has none, and
// whether some part is partial or a companion merged without lines.
var eventLineSums = func() string {
	var sums strings.Builder
	for _, c := range lineCategories {
		for _, side := range []string{"added", "deleted"} {
			fmt.Fprintf(&sums, "coalesce(sum((data #>> '{lines,%s,%s}')::bigint), 0)::bigint AS %s_%s, ", c, side, c, side)
		}
	}
	return sums.String() + `coalesce(bool_or(coalesce((data #>> '{lines,partial}')::boolean, false)
		OR (kind = 'companion.merged' AND NOT data ? 'lines')), false)`
}()

// lineDest are the destinations of the line columns and partial, in
// order.
func lineDest(l *MergedLines) []any {
	return []any{&l.Production.Added, &l.Production.Deleted, &l.Tests.Added, &l.Tests.Deleted,
		&l.Docs.Added, &l.Docs.Deleted, &l.Generated.Added, &l.Generated.Deleted, &l.Partial}
}

// issueLineColumns are issue_lines' measured, line and partial columns of
// the row alias l, 0 and false where it has no row; see lineDest.
func issueLineColumns(l string) string {
	cols := []string{"coalesce(" + l + ".measured, false)"}
	for _, c := range lineColumns {
		cols = append(cols, "coalesce("+l+"."+c+", 0)")
	}
	return strings.Join(append(cols, "coalesce("+l+".partial, false)"), ", ")
}

// mergedTotalsColumns aggregate issue_lines rows into a MergedLineTotals
// without its cost, see scanMergedTotals: the merged and measured issues,
// the measured ones' sums and whether any of them is partial.
var mergedTotalsColumns = func() string {
	cols := []string{"count(*) FILTER (WHERE merged_at IS NOT NULL)", "count(*) FILTER (WHERE measured)"}
	for _, c := range lineColumns {
		cols = append(cols, "coalesce(sum("+c+") FILTER (WHERE measured), 0)::bigint")
	}
	return strings.Join(append(cols, "coalesce(bool_or(partial) FILTER (WHERE measured), false)"), ", ")
}()

// mergedTotalsNames name the columns of mergedTotalsColumns.
var mergedTotalsNames = append(append([]string{"merged", "measured"}, lineColumns...), "partial")

// coalescedTotals are the mergedTotalsColumns of the row alias l by their
// names, 0 and false where it has no row.
func coalescedTotals(l string) string {
	cols := make([]string, 0, len(mergedTotalsNames))
	for _, c := range mergedTotalsNames {
		def := "0"
		if c == "partial" {
			def = "false"
		}
		cols = append(cols, "coalesce("+l+"."+c+", "+def+")")
	}
	return strings.Join(cols, ", ")
}

// mergedTotalsDest are the destinations of mergedTotalsColumns.
func mergedTotalsDest(t *MergedLineTotals) []any {
	return append([]any{&t.Merged, &t.Measured}, lineDest(&t.Lines)...)
}

// orNil is t, nil when no issue merged.
func (t MergedLineTotals) orNil() *MergedLineTotals {
	if t.Merged == 0 {
		return nil
	}
	return &t
}

// costPer100 sets t's CostPer100USD from cost, the totals of its measured
// issues' calls: unknown when they have no calls, no known cost or no
// production lines.
func (t *MergedLineTotals) costPer100(cost SpendTotals) {
	lines := t.Lines.Production.Added + t.Lines.Production.Deleted
	if lines == 0 || !cost.CostUSD.Known || cost.Calls == 0 && !cost.LegacyCostUSD.Known {
		t.CostPer100USD = Money{}
		return
	}
	t.CostPer100USD = USD(cost.CostUSD.USD * 100 / float64(lines))
}

// mergedLines returns the merged line totals of sc's merged issues with
// the cost per 100 production lines, nil when none merged or sc is a task
// or run scope. with and args are spendQuery's for sc.
func (s *Store) mergedLines(ctx context.Context, sc SpendScope, with string, args []any) (*MergedLineTotals, error) {
	if sc.Task != "" {
		return nil, nil
	}
	// The same condition with the same placeholders as with's.
	where := sc.where(new([]any))
	var t MergedLineTotals
	cost, err := scanTotals(s.pool.QueryRow(ctx, with+`, `+issueLinesCTE(where)+`
		SELECT l.*, c.* FROM (SELECT `+mergedTotalsColumns+` FROM issue_lines) l CROSS JOIN (
			SELECT `+spendTotals+` FROM spend
			WHERE (namespace, project, issue_iid) IN (SELECT namespace, project, issue_iid FROM issue_lines WHERE measured)
		) c`, args...), mergedTotalsDest(&t)...)
	if err != nil {
		return nil, err
	}
	t.costPer100(cost)
	return t.orNil(), nil
}

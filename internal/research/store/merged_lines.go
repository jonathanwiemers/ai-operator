package store

// The merged lines the store reads from the history (web/src/generated/store.ts;
// tygo reads this file, the queries are in merged_lines_query.go). A merged
// event carries the home merge request's lines in data.lines, a
// companion.merged event the companion's, both in the JSON of the CRD's
// MergedLines.

// LineCount is a number of added and deleted lines.
type LineCount struct {
	Added   int64 `json:"added"`
	Deleted int64 `json:"deleted"`
}

// MergedLines are the lines merge requests merged, by the category of each
// file's path. Partial is set when some file's diff came without content or
// a companion merged without a count: the sums are a lower bound.
type MergedLines struct {
	Production LineCount `json:"production"`
	Tests      LineCount `json:"tests"`
	Docs       LineCount `json:"docs"`
	Generated  LineCount `json:"generated"`
	Partial    bool      `json:"partial,omitempty"`
}

// CompanionLines is a companion merge request's share of an issue's merged
// lines: the companion project's path, its MR and its lines, nil when the
// companion merged without a count.
type CompanionLines struct {
	Project string       `json:"project"`
	MRIID   int64        `json:"mrIID"`
	Lines   *MergedLines `json:"lines,omitempty"`
}

// MergedLineTotals sums the merged lines of a scope's merged issues (those
// with a merged event). Merged counts them, Measured those whose merged
// event has lines; Lines sums the measured ones only, companions included,
// and is partial when any of them is. An unmeasured issue adds nothing,
// never 0 lines. CostPer100USD is the cost of the measured issues' calls
// per 100 merged production lines (added plus deleted), unknown (left out)
// when their cost is unknown, they have no production lines, or the view
// does not compute it (milestone rows).
type MergedLineTotals struct {
	Lines         MergedLines `json:"lines"`
	Merged        int64       `json:"merged"`
	Measured      int64       `json:"measured"`
	CostPer100USD Money       `json:"costPer100USD,omitzero"`
}

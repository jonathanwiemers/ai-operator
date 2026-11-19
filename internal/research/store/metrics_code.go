package store

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Code over a range: the lines of many projects, for the Desk's Metrics
// page. Written lines are the calls' lines_added and lines_deleted, by call
// time, not split by category. Merged lines are the data.lines of the merged
// and companion.merged events, each by its own event time, so a companion
// that merged after its home MR counts when it merged. The cost per 100
// merged production lines is per issue instead: the issues whose merged event
// is in the range count whole, with all their calls and companions at any
// time. Companion events carry the home project, so their lines count under
// it. Research calls never count; q's kinds and engine do not apply.

// CodeBucket is the lines of one bucket: Bucket is its local start date,
// YYYY-MM-DD.
type CodeBucket struct {
	Bucket  string
	Written LineCount
	// Merged is the lines of the bucket's merged and companion.merged events,
	// partial when one of them is or a companion merged without a count. A
	// merged event without lines (not measured) adds nothing.
	Merged MergedLines
}

// CodeTotals is the lines of a range, of all q's projects or one of them.
// Written and Merged sum the buckets. Issues is the issues with a merged
// event in the range, measured whole (see MergedLineTotals), with the cost
// per 100 merged production lines; Cost is the totals of their measured
// issues' calls at any time, for the cost's notes (unknown and unmetered
// calls, token basis).
type CodeTotals struct {
	Written LineCount
	Merged  MergedLines
	Issues  MergedLineTotals
	Cost    SpendTotals
}

// CodeProject is the lines of one project.
type CodeProject struct {
	Namespace, Project string
	CodeTotals
}

// CodeRange is the lines of a range: Buckets holds the buckets with lines,
// by start; ByProject the projects with written lines, merged lines or
// merged issues, by namespace and name.
type CodeRange struct {
	Buckets   []CodeBucket
	Totals    CodeTotals
	ByProject []CodeProject
}

// codeSQL is CodeInRange's statements in the order it reads them:
//   - the written lines per project and bucket, on agent_calls_project_at_idx;
//   - the merged lines per project and bucket, on history_events_at_idx (the
//     project filter is no condition history_events_project_idx could
//     serve, as in pausesInRangeSQL);
//   - per issue merged in the range (its merged events there found on
//     history_events_at_idx) its merged lines (issue_lines, all its events
//     on history_events_issue_idx) and the totals of all its calls (spend).
func codeSQL(q CostQuery, bucket string) []costStmt {
	nss, names := split(q.Projects)
	refs := make([]string, 0, len(q.Projects))
	for _, p := range q.Projects {
		refs = append(refs, p.Namespace+"/"+p.Name)
	}
	loc := cmp.Or(q.Loc, time.UTC).String()
	written := `SELECT namespace, project, to_char(date_trunc($5::text, at AT TIME ZONE $6::text), 'YYYY-MM-DD'),
			coalesce(sum(lines_added), 0)::bigint, coalesce(sum(lines_deleted), 0)::bigint
		FROM agent_calls
		WHERE (namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[])) AND at >= $3 AND at < $4
			AND expires_at > now() AND kind <> 'research' AND (lines_added <> 0 OR lines_deleted <> 0)
		GROUP BY 1, 2, 3`
	merged := `SELECT namespace, project, to_char(date_trunc($4::text, at AT TIME ZONE $5::text), 'YYYY-MM-DD'), ` + eventLineSums + `
		FROM history_events
		WHERE at >= $1 AND at < $2 AND kind IN ('merged', 'companion.merged') AND issue_iid > 0
			AND namespace || '/' || project = ANY($3::text[]) AND expires_at + interval '0 seconds' > now()
		GROUP BY 1, 2, 3`
	// The issues merged in the range, as a condition on the columns
	// namespace, project and issue_iid for spendWith and issueLinesCTE.
	inRange := `(namespace, project, issue_iid) IN (SELECT h.namespace, h.project, h.issue_iid FROM history_events h
		WHERE h.at >= $1 AND h.at < $2 AND h.kind = 'merged' AND h.issue_iid > 0
			AND h.namespace || '/' || h.project = ANY($3::text[]) AND h.expires_at + interval '0 seconds' > now())`
	issues := spendWith(inRange) + `, ` + issueLinesCTE(inRange) + `
		SELECT l.namespace, l.project, l.issue_iid, ` + issueLineColumns("l") + `, ` + spendTotals + `
		FROM issue_lines l LEFT JOIN spend s
			ON s.namespace = l.namespace AND s.project = l.project AND s.issue_iid = l.issue_iid AND s.kind <> 'research'
		GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13
		ORDER BY 1, 2, 3`
	return []costStmt{
		{written, []any{nss, names, q.From, q.To, bucket, loc}},
		{merged, []any{q.From, q.To, refs, bucket, loc}},
		{issues, []any{q.From, q.To, refs}},
	}
}

// CodeInRange returns the written and merged lines of q's projects in
// [q.From, q.To) per bucket (day, week or month, in q.Loc, as CostSeries
// buckets), in total and per project, with the cost per 100 merged
// production lines of the issues merged in the range. q's kinds and engine
// do not apply, research calls never count. No projects give zeros and
// empty slices.
func (s *Store) CodeInRange(ctx context.Context, q CostQuery, bucket string) (CodeRange, error) {
	if !costBuckets[bucket] {
		return CodeRange{}, fmt.Errorf("code: unknown bucket %q", bucket)
	}
	b := newCodeBuilder()
	if len(q.Projects) == 0 {
		return b.done(), nil
	}
	batch := &pgx.Batch{}
	for _, st := range codeSQL(q, bucket) {
		batch.Queue(st.sql, st.args...)
	}
	br := s.pool.SendBatch(ctx, batch)
	err := func() error {
		var ns, project, key string
		var written LineCount
		rows, _ := br.Query()
		if _, err := pgx.ForEachRow(rows, []any{&ns, &project, &key, &written.Added, &written.Deleted}, func() error {
			b.written(ns, project, key, written)
			return nil
		}); err != nil {
			return err
		}
		var lines MergedLines
		rows, _ = br.Query()
		if _, err := pgx.ForEachRow(rows, append([]any{&ns, &project, &key}, lineDest(&lines)...), func() error {
			b.merged(ns, project, key, lines)
			return nil
		}); err != nil {
			return err
		}
		rows, _ = br.Query()
		defer rows.Close()
		for rows.Next() {
			var issue int64
			var measured bool
			var l MergedLines
			cost, err := scanTotals(rows, append([]any{&ns, &project, &issue, &measured}, lineDest(&l)...)...)
			if err != nil {
				return err
			}
			b.issue(ns, project, measured, l, cost)
		}
		return rows.Err()
	}()
	if cerr := br.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return CodeRange{}, err
	}
	return b.done(), nil
}

// codeBuilder sums CodeInRange's rows into a CodeRange.
type codeBuilder struct {
	r        CodeRange
	buckets  map[string]int
	projects map[[2]string]int
}

func newCodeBuilder() *codeBuilder {
	return &codeBuilder{r: CodeRange{Buckets: []CodeBucket{}, ByProject: []CodeProject{}},
		buckets: map[string]int{}, projects: map[[2]string]int{}}
}

// bucket is the bucket starting at key, added when new.
func (b *codeBuilder) bucket(key string) *CodeBucket {
	i, ok := b.buckets[key]
	if !ok {
		i = len(b.r.Buckets)
		b.buckets[key] = i
		b.r.Buckets = append(b.r.Buckets, CodeBucket{Bucket: key})
	}
	return &b.r.Buckets[i]
}

// project is the totals of namespace/name, added when new.
func (b *codeBuilder) project(ns, name string) *CodeTotals {
	k := [2]string{ns, name}
	i, ok := b.projects[k]
	if !ok {
		i = len(b.r.ByProject)
		b.projects[k] = i
		b.r.ByProject = append(b.r.ByProject, CodeProject{Namespace: ns, Project: name})
	}
	return &b.r.ByProject[i].CodeTotals
}

// written adds a project's written lines in the bucket key.
func (b *codeBuilder) written(ns, project, key string, l LineCount) {
	bk := b.bucket(key)
	bk.Written = bk.Written.plus(l)
	for _, t := range []*CodeTotals{&b.r.Totals, b.project(ns, project)} {
		t.Written = t.Written.plus(l)
	}
}

// merged adds a project's merged lines in the bucket key.
func (b *codeBuilder) merged(ns, project, key string, l MergedLines) {
	bk := b.bucket(key)
	bk.Merged = bk.Merged.plus(l)
	for _, t := range []*CodeTotals{&b.r.Totals, b.project(ns, project)} {
		t.Merged = t.Merged.plus(l)
	}
}

// issue adds an issue merged in the range: its lines and cost count only
// when it is measured.
func (b *codeBuilder) issue(ns, project string, measured bool, l MergedLines, cost SpendTotals) {
	for _, t := range []*CodeTotals{&b.r.Totals, b.project(ns, project)} {
		t.Issues.Merged++
		if measured {
			t.Issues.Measured++
			t.Issues.Lines = t.Issues.Lines.plus(l)
			t.Cost = t.Cost.Add(cost)
		}
	}
}

// done sorts the buckets and projects and sets the costs per 100 lines.
func (b *codeBuilder) done() CodeRange {
	r := b.r
	slices.SortFunc(r.Buckets, func(x, y CodeBucket) int { return cmp.Compare(x.Bucket, y.Bucket) })
	slices.SortFunc(r.ByProject, func(x, y CodeProject) int {
		return cmp.Or(cmp.Compare(x.Namespace, y.Namespace), cmp.Compare(x.Project, y.Project))
	})
	r.Totals.Issues.costPer100(r.Totals.Cost)
	for i := range r.ByProject {
		t := &r.ByProject[i].CodeTotals
		t.Issues.costPer100(t.Cost)
	}
	return r
}

// plus is the sum of c and d.
func (c LineCount) plus(d LineCount) LineCount {
	return LineCount{Added: c.Added + d.Added, Deleted: c.Deleted + d.Deleted}
}

// plus is the sum of l and u, partial when either is.
func (l MergedLines) plus(u MergedLines) MergedLines {
	return MergedLines{Production: l.Production.plus(u.Production), Tests: l.Tests.plus(u.Tests), Docs: l.Docs.plus(u.Docs),
		Generated: l.Generated.plus(u.Generated), Partial: l.Partial || u.Partial}
}

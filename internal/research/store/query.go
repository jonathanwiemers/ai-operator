package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/types"
)

// The queries of the UI. They only read: the UI connects as the reader,
// which has SELECT only.

// ErrNotFound is returned for a run or result that does not exist.
var ErrNotFound = errors.New("not found")

// ResultRow is a stored result as the UI reads it. Its blocks were validated
// before they were stored; the UI validates them again before it sends them.
type ResultRow struct {
	RunID, Title, Summary string
	Blocks                json.RawMessage
	CreatedAt, ExpiresAt  time.Time
	// NewURLHashes are the URL hashes of the items first seen in this run:
	// in no earlier run of the research that is still kept.
	NewURLHashes [][]byte
}

// Hit is a search result.
type Hit struct {
	RunID, Namespace, Research string
	Title, Summary             string
	CreatedAt                  time.Time
}

const runColumns = `id::text, namespace, research, started_at, finished_at, status, engine, model, effort,
	coalesce(cost_usd, 0)::float8, choice_reason, error, warnings, expires_at`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.Namespace, &r.Research, &r.StartedAt, &r.FinishedAt, &r.Status, &r.Engine, &r.Model,
		&r.Effort, &r.CostUSD, &r.ChoiceReason, &r.Error, &r.Warnings, &r.ExpiresAt)
	return r, err
}

// ListRuns returns at most limit runs of the research namespace/name that
// started before before, newest first.
func (s *Store) ListRuns(ctx context.Context, namespace, research string, before time.Time, limit int) ([]Run, error) {
	rows, _ := s.pool.Query(ctx, `SELECT `+runColumns+` FROM research_runs
		WHERE namespace = $1 AND research = $2 AND started_at < $3 ORDER BY started_at DESC, id LIMIT $4`,
		namespace, research, before, limit)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Run, error) { return scanRun(row) })
}

// GetRun returns the run id, ErrNotFound if there is none (or id is no UUID).
func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return Run{}, ErrNotFound
	}
	r, err := scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM research_runs WHERE id = $1`, u.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

// resultQuery selects ResultRows; an item is new if its hash was first seen
// at or after the start of the result's run.
const resultQuery = `SELECT res.run_id::text, res.title, res.summary, res.blocks, res.created_at, res.expires_at,
	array(SELECT i.url_hash FROM research_items i WHERE i.run_id = res.run_id AND i.first_seen >= r.started_at)
	FROM research_results res JOIN research_runs r ON r.id = res.run_id `

func scanResult(row pgx.Row) (ResultRow, error) {
	var res ResultRow
	err := row.Scan(&res.RunID, &res.Title, &res.Summary, &res.Blocks, &res.CreatedAt, &res.ExpiresAt, &res.NewURLHashes)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResultRow{}, ErrNotFound
	}
	return res, err
}

// GetResult returns the result of run runID, ErrNotFound if it has none.
func (s *Store) GetResult(ctx context.Context, runID string) (ResultRow, error) {
	u, err := uuid.Parse(runID)
	if err != nil {
		return ResultRow{}, ErrNotFound
	}
	return scanResult(s.pool.QueryRow(ctx, resultQuery+`WHERE res.run_id = $1`, u.String()))
}

// LatestResult returns the newest result of the research namespace/name,
// ErrNotFound if it has none.
func (s *Store) LatestResult(ctx context.Context, namespace, research string) (ResultRow, error) {
	return scanResult(s.pool.QueryRow(ctx, resultQuery+`WHERE r.namespace = $1 AND r.research = $2
		ORDER BY res.created_at DESC, r.started_at DESC LIMIT 1`, namespace, research))
}

// Search returns at most limit results of the researches in allowed that
// match q (web search syntax: words, "phrases", or, -not), best match
// first, then newest.
func (s *Store) Search(ctx context.Context, q string, allowed []types.NamespacedName, limit int) ([]Hit, error) {
	if len(allowed) == 0 {
		return []Hit{}, nil
	}
	namespaces, names := make([]string, len(allowed)), make([]string, len(allowed))
	for i, a := range allowed {
		namespaces[i], names[i] = a.Namespace, a.Name
	}
	rows, _ := s.pool.Query(ctx, `SELECT r.id::text, r.namespace, r.research, res.title, res.summary, res.created_at
		FROM research_results res JOIN research_runs r ON r.id = res.run_id, websearch_to_tsquery('simple', $1) q
		WHERE res.search @@ q AND (r.namespace, r.research) IN (SELECT * FROM unnest($2::text[], $3::text[]))
		ORDER BY ts_rank(res.search, q) DESC, res.created_at DESC LIMIT $4`, q, namespaces, names, limit)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Hit, error) {
		var h Hit
		err := row.Scan(&h.RunID, &h.Namespace, &h.Research, &h.Title, &h.Summary, &h.CreatedAt)
		return h, err
	})
}

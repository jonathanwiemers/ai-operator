package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
)

// Statuses of a run. Every status but running is final.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusDegraded  = "degraded"
	StatusFailed    = "failed"
	StatusLimited   = "limited"
)

// Run is one run of an AgentResearch. ID is a UUID.
type Run struct {
	ID, Namespace, Research string
	StartedAt               time.Time
	FinishedAt              *time.Time
	Status, Engine, Model   string
	Effort, ChoiceReason    string
	Error                   string
	CostUSD                 float64
	Warnings                []string
	ExpiresAt               time.Time
}

// StartRun inserts r as running; Status, FinishedAt and CostUSD are ignored.
// A run that exists already, running or finished, is left as it is.
func (s *Store) StartRun(ctx context.Context, r Run) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO research_runs (id, namespace, research, started_at, status,
		engine, model, effort, choice_reason, error, warnings, expires_at)
		VALUES ($1, $2, $3, $4, 'running', $5, $6, $7, $8, $9, coalesce($10::text[], '{}'), $11)
		ON CONFLICT (id) DO NOTHING`,
		r.ID, r.Namespace, r.Research, r.StartedAt, r.Engine, r.Model, r.Effort, r.ChoiceReason, r.Error, r.Warnings, r.ExpiresAt)
	return err
}

// FinishRun records the outcome of a run that is still running and appends
// warnings to its own. The first finish wins: a finished or unknown run is
// left unchanged, so the controller and the runner cannot overwrite each
// other.
func (s *Store) FinishRun(ctx context.Context, id, status string, cost float64, errMsg string, warnings []string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE research_runs SET status = $2, cost_usd = $3, error = $4,
		warnings = warnings || coalesce($5::text[], '{}'), finished_at = $6
		WHERE id = $1 AND status = 'running'`, id, status, cost, errMsg, warnings, at)
	return err
}

// FinishRunOrInsert finishes r like FinishRun, or inserts it finished if the
// runner never started it (its pod died first). r.FinishedAt is required.
func (s *Store) FinishRunOrInsert(ctx context.Context, r Run) error {
	if r.FinishedAt == nil {
		return errors.New("finish run: finished time is required")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO research_runs AS old (id, namespace, research, started_at, finished_at,
		status, engine, model, effort, cost_usd, choice_reason, error, warnings, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, coalesce($13::text[], '{}'), $14)
		ON CONFLICT (id) DO UPDATE SET status = excluded.status, finished_at = excluded.finished_at,
			cost_usd = excluded.cost_usd, error = excluded.error, warnings = old.warnings || excluded.warnings
		WHERE old.status = 'running'`,
		r.ID, r.Namespace, r.Research, r.StartedAt, r.FinishedAt, r.Status, r.Engine, r.Model, r.Effort,
		r.CostUSD, r.ChoiceReason, r.Error, r.Warnings, r.ExpiresAt)
	return err
}

// SaveResult stores the validated result of run runID, expiring with the run,
// and one item row per distinct URL hash. An item's first_seen is the
// earliest first_seen of its hash in the research's earlier runs, else now.
// Result and items are written in one transaction.
func (s *Store) SaveResult(ctx context.Context, runID string, r blocks.Result, now time.Time) error {
	if r.Blocks == nil {
		r.Blocks = []blocks.Block{}
	}
	data, err := json.Marshal(r.Blocks)
	if err != nil {
		return err
	}
	// The first item of a hash gives its topic.
	var hashes [][]byte
	var topics []string
	seen := map[string]bool{}
	for _, b := range r.Blocks {
		if b.Items == nil {
			continue
		}
		for _, it := range b.Items.Items {
			if h := blocks.URLHash(it.URL); !seen[string(h)] {
				seen[string(h)] = true
				hashes, topics = append(hashes, h), append(topics, it.Topic)
			}
		}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO research_results (run_id, created_at, expires_at, title, summary, blocks)
			SELECT id, $2, expires_at, $3, $4, $5 FROM research_runs WHERE id = $1`, runID, now, r.Title, r.Summary, data)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("save result: run %s does not exist", runID)
		}
		_, err = tx.Exec(ctx, `INSERT INTO research_items (run_id, url_hash, topic, first_seen)
			SELECT cur.id, h.hash, h.topic, coalesce((SELECT min(i.first_seen) FROM research_items i
				JOIN research_runs r ON r.id = i.run_id
				WHERE i.url_hash = h.hash AND r.namespace = cur.namespace AND r.research = cur.research), $4)
			FROM research_runs cur, unnest($2::bytea[], $3::text[]) AS h(hash, topic)
			WHERE cur.id = $1`, runID, hashes, topics, now)
		return err
	})
}

// PreviousResult returns at most limit item URLs of the latest result of
// the research namespace/name, without duplicates, and that run's start
// time. Without a result it returns none and a zero time.
func (s *Store) PreviousResult(ctx context.Context, namespace, research string, limit int) ([]string, time.Time, error) {
	rows, _ := s.pool.Query(ctx, `SELECT res.blocks, r.started_at FROM research_results res JOIN research_runs r ON r.id = res.run_id
		WHERE r.namespace = $1 AND r.research = $2 ORDER BY res.created_at DESC, r.started_at DESC LIMIT 1`,
		namespace, research)
	latest, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[struct {
		Blocks    []blocks.Block
		StartedAt time.Time
	}])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	urls := blocks.ItemURLs(blocks.Result{Blocks: latest.Blocks})
	return urls[:min(len(urls), max(limit, 0))], latest.StartedAt, nil
}

// PreviousItemURLs returns at most limit item URLs of the latest result of
// the research namespace/name, without duplicates, so the next run can be
// told what it reported already. Without a result it returns none.
func (s *Store) PreviousItemURLs(ctx context.Context, namespace, research string, limit int) ([]string, error) {
	urls, _, err := s.PreviousResult(ctx, namespace, research, limit)
	return urls, err
}

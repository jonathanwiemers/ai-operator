package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Agent calls: one row per engine call of the factory, written by the
// operator (the owner) and read by the UI (the reader).

// Call is one engine call. Zero IIDs are stored as NULL. Key makes writes
// idempotent.
type Call struct {
	ID              int64
	Key             string
	At              time.Time
	Namespace       string
	Project         string
	MilestoneIID    int64
	IssueIID        int64
	MergeRequestIID int64
	Task            string
	Run             int64
	Seq             int64
	Kind            string
	Role            string
	Engine          string
	Model           string
	Effort          string
	Resumed         bool
	FallbackFrom    string

	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	// CostUSD is nil when the cost is unknown.
	CostUSD *float64
	// CostSource is reported, estimated or unknown.
	CostSource string
	Turns      int32
	DurationMS int64

	Models        []CallModel
	SubagentCalls int32
	// ReadGateBlocks is how many tool calls the read gate denied.
	ReadGateBlocks int32
	// ContextSeries is the context size of each main-thread turn;
	// SubagentTurns are indexes into it after which a subagent ran.
	ContextSeries []int64
	SubagentTurns []int32

	ExpiresAt time.Time
}

// CallModel is one model's share of a call, an element of the models column.
type CallModel struct {
	Model            string `json:"model"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	// CostUSD is nil when the cost is unknown.
	CostUSD *float64 `json:"costUSD,omitempty"`
}

// InsertCalls writes calls, skipping those whose key is already stored, and
// returns how many it wrote.
func (s *Store) InsertCalls(ctx context.Context, calls []Call) (int64, error) {
	if len(calls) == 0 {
		return 0, nil
	}
	b := &pgx.Batch{}
	for _, c := range calls {
		models, err := jsonArray(c.Models)
		if err != nil {
			return 0, err
		}
		series, err := jsonArray(c.ContextSeries)
		if err != nil {
			return 0, err
		}
		turns, err := jsonArray(c.SubagentTurns)
		if err != nil {
			return 0, err
		}
		b.Queue(`INSERT INTO agent_calls
			(key, at, namespace, project, milestone_iid, issue_iid, mr_iid, task, run, seq,
			 kind, role, engine, model, effort, resumed, fallback_from,
			 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, cost_source,
			 turns, duration_ms, models, subagent_calls, context_series, subagent_turns, expires_at, read_gate_blocks)
			VALUES ($1, $2, $3, $4, nullif($5, 0), nullif($6, 0), nullif($7, 0), $8, $9, $10,
			 $11, $12, $13, $14, $15, $16, $17,
			 $18, $19, $20, $21, $22, $23,
			 $24, $25, $26, $27, $28, $29, $30, $31)
			ON CONFLICT (key) DO NOTHING`,
			c.Key, c.At, c.Namespace, c.Project, c.MilestoneIID, c.IssueIID, c.MergeRequestIID, c.Task, c.Run, c.Seq,
			c.Kind, c.Role, c.Engine, c.Model, c.Effort, c.Resumed, c.FallbackFrom,
			c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheWriteTokens, c.CostUSD, c.CostSource,
			c.Turns, c.DurationMS, models, c.SubagentCalls, series, turns, c.ExpiresAt, c.ReadGateBlocks)
	}
	var n int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		br := tx.SendBatch(ctx, b)
		for range calls {
			tag, err := br.Exec()
			if err != nil {
				_ = br.Close()
				return err
			}
			n += tag.RowsAffected()
		}
		return br.Close()
	})
	return n, err
}

// jsonArray marshals s for a jsonb array column: nil is [].
func jsonArray[T any](s []T) ([]byte, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(s)
}

// DeleteExpiredCalls deletes the calls that expired before now and returns
// how many. It runs as the owner.
func (s *Store) DeleteExpiredCalls(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `DELETE FROM agent_calls WHERE id IN (
			SELECT id FROM agent_calls WHERE expires_at < $1 ORDER BY expires_at LIMIT $2)`, now, deleteBatch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < deleteBatch {
			return total, nil
		}
	}
}

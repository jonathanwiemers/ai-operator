package store

import (
	"context"
	"time"
)

// deleteBatch bounds the rows one retention statement deletes, so a large
// backlog does not hold long locks.
const deleteBatch = 1000

// DeleteExpired deletes the runs that expired before now, with their results
// and items (they cascade), and returns how many runs it deleted. It runs as
// the owner: readers and writers may not delete.
func (s *Store) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `DELETE FROM research_runs WHERE id IN (
			SELECT id FROM research_runs WHERE expires_at < $1 ORDER BY expires_at LIMIT $2)`, now, deleteBatch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < deleteBatch {
			return total, nil
		}
	}
}

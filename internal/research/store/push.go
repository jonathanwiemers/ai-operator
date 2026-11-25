package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// The push tables (push_keys, push_subscriptions, push_sent) hold the Desk's
// Web Push key, the devices' subscriptions and the ledger of what was pushed
// to each. The UI server writes them as the reader login. Errors of the
// functions below never carry an endpoint, a subscription key or the
// private key: Postgres messages name constraints, not values, and the
// wrapping names the operation only.

// PushExpiry is how long a subscription lives without a refresh, and how
// long a ledger row of push_sent is kept.
const PushExpiry = 30 * 24 * time.Hour

// pushLock is the pg_advisory_lock key of the push sender, so that only one
// UI replica sends at a time. It differs from migrateLock.
const pushLock int64 = 0x61692d70757368 // "ai-push"

// PushKey is the VAPID key pair: Public is an uncompressed P-256 point (65
// bytes), Private its 32-byte scalar.
type PushKey struct {
	Public    []byte
	Private   []byte
	CreatedAt time.Time
}

// PushKey returns the stored key pair, and false when there is none.
func (s *Store) PushKey(ctx context.Context) (PushKey, bool, error) {
	var k PushKey
	err := s.pool.QueryRow(ctx, "SELECT public_key, private_key, created_at FROM push_keys").
		Scan(&k.Public, &k.Private, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PushKey{}, false, nil
	}
	if err != nil {
		return PushKey{}, false, fmt.Errorf("read push key: %w", err)
	}
	return k, true, nil
}

// EnsurePushKey stores the key pair unless one is stored, and returns the
// stored one: replicas that race on their first start end with the same.
func (s *Store) EnsurePushKey(ctx context.Context, public, private []byte) (PushKey, error) {
	if _, err := s.pool.Exec(ctx, `INSERT INTO push_keys (public_key, private_key) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, public, private); err != nil {
		return PushKey{}, fmt.Errorf("store push key: %w", err)
	}
	k, ok, err := s.PushKey(ctx)
	if err == nil && !ok {
		err = errors.New("push key was deleted while it was stored")
	}
	return k, err
}

// PushSubscription is a device's push subscription with the principal
// (Username, Roles) of its last refresh and the categories picked on it.
type PushSubscription struct {
	ID          int64
	Endpoint    string
	P256dh      string
	Auth        string
	Username    string
	Roles       []string
	Categories  []string
	CreatedAt   time.Time
	RefreshedAt time.Time
	Failures    int
}

// UpsertPushSubscription stores sub by its endpoint and returns its id and
// whether the row is new. It sets the keys, the principal and refreshed_at
// to now. An existing row keeps its categories with keepCategories and
// takes sub.Categories otherwise; a new row always takes sub.Categories.
func (s *Store) UpsertPushSubscription(ctx context.Context, sub PushSubscription, keepCategories bool, now time.Time) (id int64, created bool, err error) {
	err = s.pool.QueryRow(ctx, `INSERT INTO push_subscriptions
			(endpoint, p256dh, auth, username, roles, categories, created_at, refreshed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (endpoint) DO UPDATE SET
			p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, username = EXCLUDED.username,
			roles = EXCLUDED.roles, refreshed_at = EXCLUDED.refreshed_at,
			categories = CASE WHEN $8::boolean THEN push_subscriptions.categories ELSE EXCLUDED.categories END
		RETURNING id, (xmax = 0)`,
		sub.Endpoint, sub.P256dh, sub.Auth, sub.Username, nonNil(sub.Roles), nonNil(sub.Categories), now, keepCategories,
	).Scan(&id, &created)
	if err != nil {
		return 0, false, fmt.Errorf("upsert push subscription: %w", err)
	}
	return id, created, nil
}

// DeletePushSubscription deletes the subscription of endpoint if it belongs
// to username, and reports whether it did.
func (s *Store) DeletePushSubscription(ctx context.Context, endpoint, username string) (bool, error) {
	tag, err := s.pool.Exec(ctx, "DELETE FROM push_subscriptions WHERE endpoint = $1 AND username = $2", endpoint, username)
	if err != nil {
		return false, fmt.Errorf("delete push subscription: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeletePushSubscriptionID deletes the subscription id and its ledger.
func (s *Store) DeletePushSubscriptionID(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM push_subscriptions WHERE id = $1", id); err != nil {
		return fmt.Errorf("delete push subscription %d: %w", id, err)
	}
	return nil
}

// PushSubscriptions returns every subscription, by id.
func (s *Store) PushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, _ := s.pool.Query(ctx, `SELECT id, endpoint, p256dh, auth, username, roles, categories,
		created_at, refreshed_at, failures FROM push_subscriptions ORDER BY id`)
	subs, err := pgx.CollectRows(rows, pgx.RowToStructByName[PushSubscription])
	if err != nil {
		return nil, fmt.Errorf("read push subscriptions: %w", err)
	}
	return subs, nil
}

// ClaimPush records keys in the ledger of subscription subID, sent at now,
// and returns the ones it recorded (sorted): the keys not claimed before.
// The sender claims a key before it sends it; subscribing seeds the ledger
// with what is current, so only newer keys are pushed.
func (s *Store) ClaimPush(ctx context.Context, subID int64, keys []string, now time.Time) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	rows, _ := s.pool.Query(ctx, `INSERT INTO push_sent (subscription_id, key, sent_at)
		SELECT $1, k, $3 FROM (SELECT DISTINCT unnest($2::text[]) AS k) AS ks
		ON CONFLICT DO NOTHING RETURNING key`, subID, keys, now)
	claimed, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("claim pushes of subscription %d: %w", subID, err)
	}
	slices.Sort(claimed)
	return claimed, nil
}

// UnclaimPush deletes key from the ledger of subscription subID, after its
// send failed, so the next cycle sends it again.
func (s *Store) UnclaimPush(ctx context.Context, subID int64, key string) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM push_sent WHERE subscription_id = $1 AND key = $2", subID, key); err != nil {
		return fmt.Errorf("unclaim push of subscription %d: %w", subID, err)
	}
	return nil
}

// ForgetPush deletes the ledger rows of subscription subID that are neither
// event keys (event/…) nor in keep, the keys of its current Inbox: an item
// that leaves the Inbox and comes back is pushed again.
func (s *Store) ForgetPush(ctx context.Context, subID int64, keep []string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM push_sent
		WHERE subscription_id = $1 AND NOT starts_with(key, 'event/') AND NOT key = ANY($2::text[])`,
		subID, nonNil(keep)); err != nil {
		return fmt.Errorf("forget pushes of subscription %d: %w", subID, err)
	}
	return nil
}

// PushFailed counts a failed send to subscription id and deletes the
// subscription when it reaches max failures in a row; deleted reports that.
func (s *Store) PushFailed(ctx context.Context, id int64, max int) (deleted bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var failures int
		err := tx.QueryRow(ctx, "UPDATE push_subscriptions SET failures = failures + 1 WHERE id = $1 RETURNING failures", id).
			Scan(&failures)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && failures < max {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "DELETE FROM push_subscriptions WHERE id = $1", id)
		deleted = err == nil
		return err
	})
	if err != nil {
		return false, fmt.Errorf("count push failure of subscription %d: %w", id, err)
	}
	return deleted, nil
}

// PushSucceeded resets the failures of subscription id.
func (s *Store) PushSucceeded(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, "UPDATE push_subscriptions SET failures = 0 WHERE id = $1 AND failures <> 0", id); err != nil {
		return fmt.Errorf("reset push failures of subscription %d: %w", id, err)
	}
	return nil
}

// DeleteExpiredPush deletes the subscriptions not refreshed and the ledger
// rows not sent within PushExpiry before now, and returns how many of each
// (ledger rows of deleted subscriptions not counted).
func (s *Store) DeleteExpiredPush(ctx context.Context, now time.Time) (subs, sent int64, err error) {
	cut := now.Add(-PushExpiry)
	tag, err := s.pool.Exec(ctx, "DELETE FROM push_subscriptions WHERE refreshed_at < $1", cut)
	if err != nil {
		return 0, 0, fmt.Errorf("delete expired push subscriptions: %w", err)
	}
	subs = tag.RowsAffected()
	if tag, err = s.pool.Exec(ctx, "DELETE FROM push_sent WHERE sent_at < $1", cut); err != nil {
		return subs, 0, fmt.Errorf("delete expired push ledger: %w", err)
	}
	return subs, tag.RowsAffected(), nil
}

// TryPushLock takes the sender's advisory lock on a connection of the pool,
// without waiting. With ok, unlock releases the lock and the connection;
// without, the connection is released at once and unlock is nil.
func (s *Store) TryPushLock(ctx context.Context) (unlock func(), ok bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("push lock: %w", err)
	}
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", pushLock).Scan(&ok); err != nil || !ok {
		conn.Release()
		if err != nil {
			return nil, false, fmt.Errorf("push lock: %w", err)
		}
		return nil, false, nil
	}
	return sync.OnceFunc(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		// A lock left behind by a failed unlock ends with the connection.
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", pushLock); err != nil {
			conn.Conn().Close(ctx) // #nosec G104 -- best effort after a failed unlock: closing ends the session's advisory lock
		}
		conn.Release()
	}), true, nil
}

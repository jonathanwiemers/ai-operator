package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Chat of a task: messages written by the UI (chat writer) and by the
// operator (owner), read by the UI (reader).

// ChatMessage is one row of chat_messages.
type ChatMessage struct {
	ID         int64
	Namespace  string
	Project    string
	Task       string
	Author     string
	AuthorName string
	Role       string
	Kind       string
	Body       string
	ReplyTo    int64 // 0 when unset
	ClientKey  string
	State      string
	Reason     string
	At         time.Time
	ExpiresAt  time.Time
}

const chatColumns = `id, namespace, project, task, author, author_name, role, kind, body,
	coalesce(reply_to, 0), coalesce(client_key, ''), state, reason, at, expires_at`

func scanChat(row pgx.CollectableRow) (ChatMessage, error) {
	var m ChatMessage
	err := row.Scan(&m.ID, &m.Namespace, &m.Project, &m.Task, &m.Author, &m.AuthorName,
		&m.Role, &m.Kind, &m.Body, &m.ReplyTo, &m.ClientKey, &m.State, &m.Reason, &m.At, &m.ExpiresAt)
	return m, err
}

// InsertUserMessage writes a user message in state queued. The same
// (author, client_key) again returns the existing id with created false.
// An empty ClientKey always inserts a new row (NULL in the unique key).
func (s *Store) InsertUserMessage(ctx context.Context, m ChatMessage) (id int64, created bool, err error) {
	kind := m.Kind
	if kind == "" {
		kind = "message"
	}
	var key any
	if m.ClientKey != "" {
		key = m.ClientKey
	}
	err = s.pool.QueryRow(ctx, `INSERT INTO chat_messages
		(namespace, project, task, author, author_name, role, kind, body, client_key, state, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'user', $6, $7, $8, 'queued', $9)
		ON CONFLICT (author, client_key) DO NOTHING
		RETURNING id`,
		m.Namespace, m.Project, m.Task, m.Author, m.AuthorName, kind, m.Body, key, m.ExpiresAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(ctx, `SELECT id FROM chat_messages WHERE author = $1 AND client_key = $2`,
			m.Author, key).Scan(&id)
		return id, false, err
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// QueuedMessages returns messages in state queued, oldest first, up to limit.
func (s *Store) QueuedMessages(ctx context.Context, limit int) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, _ := s.pool.Query(ctx, `SELECT `+chatColumns+` FROM chat_messages
		WHERE state = 'queued' ORDER BY id LIMIT $1`, limit)
	return pgx.CollectRows(rows, scanChat)
}

// SetMessageState sets state and reason on the given ids, except on
// answered ones: a reply can land before the operator marks its message
// delivered (a re-dispatch after a crash), and answered must stay.
func (s *Store) SetMessageState(ctx context.Context, ids []int64, state, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE chat_messages SET state = $1, reason = $2
		WHERE id = ANY($3) AND state <> 'answered'`,
		state, reason, ids)
	return err
}

// InsertReply writes an agent or system reply with reply_to set. A second
// reply to the same message is a no-op (created false). On insert it marks
// the replied message answered in the same transaction.
func (s *Store) InsertReply(ctx context.Context, m ChatMessage) (created bool, err error) {
	kind, state := m.Kind, m.State
	if kind == "" {
		kind = "message"
	}
	if state == "" {
		state = "answered"
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO chat_messages
			(namespace, project, task, author, author_name, role, kind, body, reply_to, state, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (reply_to) DO NOTHING`,
			m.Namespace, m.Project, m.Task, m.Author, m.AuthorName, m.Role, kind, m.Body,
			m.ReplyTo, state, m.ExpiresAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		created = true
		_, err = tx.Exec(ctx, `UPDATE chat_messages SET state = 'answered' WHERE id = $1`, m.ReplyTo)
		return err
	})
	return created, err
}

// ChatStream returns chat messages of the given projects, newest first, for
// the UI stream. before/after are (at, id) like Stream. tasks and roles,
// when set, limit to those task names (a milestone's tasks) and roles.
func (s *Store) ChatStream(ctx context.Context, projects []ProjectKey, tasks, roles []string, before, after *Cursor, limit int) ([]ChatMessage, error) {
	if len(projects) == 0 {
		return nil, nil
	}
	nss, names := split(projects)
	args := []any{nss, names}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	where := `(namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[]))`
	if len(tasks) > 0 {
		where += " AND task = ANY(" + arg(tasks) + "::text[])"
	}
	if len(roles) > 0 {
		where += " AND role = ANY(" + arg(roles) + "::text[])"
	}
	order := "DESC"
	switch {
	case after != nil:
		where += " AND (at, id) > (" + arg(after.At) + ", " + arg(after.ID) + "::bigint)"
		order = "ASC"
	case before != nil && before.ID == 0:
		where += " AND at < " + arg(before.At)
	case before != nil:
		where += " AND (at, id) < (" + arg(before.At) + ", " + arg(before.ID) + "::bigint)"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, _ := s.pool.Query(ctx, `SELECT `+chatColumns+` FROM chat_messages WHERE `+where+
		` ORDER BY at `+order+`, id `+order+` LIMIT `+arg(limit), args...)
	msgs, err := pgx.CollectRows(rows, scanChat)
	if err != nil {
		return nil, err
	}
	if order == "ASC" {
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	return msgs, nil
}

// ChatMessages returns the messages of a task, newest first. before > 0
// pages older than that id; after > 0 pages newer (the oldest of them when
// there are more than limit), still returned newest first.
func (s *Store) ChatMessages(ctx context.Context, namespace, project, task string, before, after int64, limit int) ([]ChatMessage, error) {
	args := []any{namespace, project, task}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	where := `namespace = $1 AND project = $2 AND task = $3`
	order := "DESC"
	switch {
	case after > 0:
		where += " AND id > " + arg(after) + "::bigint"
		order = "ASC"
	case before > 0:
		where += " AND id < " + arg(before) + "::bigint"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, _ := s.pool.Query(ctx, `SELECT `+chatColumns+` FROM chat_messages WHERE `+where+
		` ORDER BY id `+order+` LIMIT `+arg(limit), args...)
	msgs, err := pgx.CollectRows(rows, scanChat)
	if err != nil {
		return nil, err
	}
	if order == "ASC" {
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	return msgs, nil
}

// ListenChat LISTENs on channel chat on a dedicated connection and calls fn
// with each notified id until ctx ends or the connection fails. The caller
// falls back to polling and retries on error. The connection is closed, not
// returned to the pool, so no pooled connection keeps listening.
func (s *Store) ListenChat(ctx context.Context, fn func(id int64)) error {
	pc, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := pc.Hijack()
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN chat"); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(n.Payload, 10, 64)
		if err != nil {
			continue
		}
		fn(id)
	}
}

// DeleteExpiredChat deletes chat messages that expired before now and
// returns how many. It runs as the owner.
func (s *Store) DeleteExpiredChat(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `DELETE FROM chat_messages WHERE id IN (
			SELECT id FROM chat_messages WHERE expires_at < $1 ORDER BY expires_at LIMIT $2)`, now, deleteBatch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < deleteBatch {
			return total, nil
		}
	}
}

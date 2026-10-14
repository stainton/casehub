package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Postgres keeps the task record as JSONB with the columns it is filtered on beside it; progress events and
// blobs are rows of their own, so appending an event never rewrites the record and a listing never reads a blob.
type Postgres struct{ db *sql.DB }

func NewPostgres(ctx context.Context, db *sql.DB) (*Postgres, error) {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS casehub_tasks (id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '', handled BOOLEAN NOT NULL DEFAULT FALSE, created_at TIMESTAMPTZ NOT NULL, finished_at TIMESTAMPTZ, record JSONB NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS casehub_tasks_kind_created ON casehub_tasks (kind, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS casehub_tasks_subject ON casehub_tasks (subject) WHERE subject <> ''`,
		`CREATE TABLE IF NOT EXISTS casehub_task_events (task_id TEXT NOT NULL REFERENCES casehub_tasks(id) ON DELETE CASCADE, id BIGINT NOT NULL, payload JSONB NOT NULL, PRIMARY KEY (task_id, id))`,
		`CREATE TABLE IF NOT EXISTS casehub_task_blobs (task_id TEXT NOT NULL REFERENCES casehub_tasks(id) ON DELETE CASCADE, name TEXT NOT NULL, data BYTEA NOT NULL, PRIMARY KEY (task_id, name))`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return nil, err
		}
	}
	return &Postgres{db: db}, nil
}

func (p *Postgres) SaveTask(ctx context.Context, t Task) error {
	record, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `INSERT INTO casehub_tasks (id, kind, status, subject, handled, created_at, finished_at, record)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, subject=EXCLUDED.subject, handled=EXCLUDED.handled, finished_at=EXCLUDED.finished_at, record=EXCLUDED.record`,
		t.ID, t.Kind, t.Status, t.Subject, t.Handled, t.CreatedAt, t.FinishedAt, record)
	return err
}
func (p *Postgres) GetTask(ctx context.Context, id string) (Task, error) {
	var b []byte
	err := p.db.QueryRowContext(ctx, `SELECT record FROM casehub_tasks WHERE id=$1`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	var t Task
	return t, json.Unmarshal(b, &t)
}
func (p *Postgres) ListTasks(ctx context.Context, f Filter) ([]Task, error) {
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if len(f.Kinds) > 0 {
		where = append(where, "kind = ANY("+arg(f.Kinds)+")")
	}
	if len(f.Statuses) > 0 {
		where = append(where, "status = ANY("+arg(f.Statuses)+")")
	}
	if f.Subject != "" {
		where = append(where, "subject = "+arg(f.Subject))
	}
	if f.Active {
		where = append(where, "NOT handled")
	}
	query := `SELECT record - 'result' FROM casehub_tasks`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC"
	if f.Limit > 0 {
		query += " LIMIT " + arg(f.Limit)
	}
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var t Task
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (p *Postgres) DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := p.db.ExecContext(ctx, `DELETE FROM casehub_tasks WHERE finished_at IS NOT NULL AND finished_at < $1 AND status = ANY($2)`,
		cutoff, []string{Succeeded, Failed, Cancelled})
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
func (p *Postgres) AppendEvent(ctx context.Context, taskID string, id int64, payload json.RawMessage, keep int) error {
	if _, err := p.db.ExecContext(ctx, `INSERT INTO casehub_task_events (task_id, id, payload) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, taskID, id, []byte(payload)); err != nil {
		return err
	}
	if keep > 0 && id > int64(keep) {
		_, err := p.db.ExecContext(ctx, `DELETE FROM casehub_task_events WHERE task_id=$1 AND id <= $2`, taskID, id-int64(keep))
		return err
	}
	return nil
}
func (p *Postgres) Events(ctx context.Context, taskID string, after int64) ([]json.RawMessage, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT payload FROM casehub_task_events WHERE task_id=$1 AND id > $2 ORDER BY id`, taskID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (p *Postgres) PutBlob(ctx context.Context, taskID, name string, data []byte) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO casehub_task_blobs (task_id, name, data) VALUES ($1,$2,$3) ON CONFLICT (task_id, name) DO UPDATE SET data=EXCLUDED.data`, taskID, name, data)
	return err
}
func (p *Postgres) GetBlob(ctx context.Context, taskID, name string) ([]byte, error) {
	var data []byte
	err := p.db.QueryRowContext(ctx, `SELECT data FROM casehub_task_blobs WHERE task_id=$1 AND name=$2`, taskID, name).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return data, err
}

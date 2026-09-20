package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"casehub/internal/core"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Memory struct {
	mu    sync.RWMutex
	state *core.State
}

func NewMemory() *Memory { return &Memory{} }
func clone(s core.State) (core.State, error) {
	b, e := json.Marshal(s)
	if e != nil {
		return core.State{}, e
	}
	var out core.State
	e = json.Unmarshal(b, &out)
	return out, e
}
func (m *Memory) Load(context.Context) (core.State, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state == nil {
		return core.State{}, core.ErrNotFound
	}
	return clone(*m.state)
}
func (m *Memory) Save(_ context.Context, s core.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, e := clone(s)
	if e == nil {
		m.state = &c
	}
	return e
}

type File struct {
	path string
	mu   sync.Mutex
}

func NewFile(path string) *File { return &File{path: path} }
func (f *File) Load(context.Context) (core.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, e := os.ReadFile(f.path)
	if errors.Is(e, os.ErrNotExist) {
		return core.State{}, core.ErrNotFound
	}
	if e != nil {
		return core.State{}, e
	}
	var s core.State
	e = json.Unmarshal(b, &s)
	return s, e
}
func (f *File) Save(_ context.Context, s core.State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := os.MkdirAll(filepath.Dir(f.path), 0755); e != nil {
		return e
	}
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return e
	}
	tmp := f.path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, f.path)
}

type Postgres struct{ db *sql.DB }

func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		return nil, e
	}
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	_, e = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS casehub_state (id SMALLINT PRIMARY KEY CHECK(id=1), payload JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Postgres{db: db}, nil
}
func (p *Postgres) Close() error { return p.db.Close() }
func (p *Postgres) Load(ctx context.Context) (core.State, error) {
	var b []byte
	e := p.db.QueryRowContext(ctx, "SELECT payload FROM casehub_state WHERE id=1").Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return core.State{}, core.ErrNotFound
	}
	if e != nil {
		return core.State{}, e
	}
	var s core.State
	e = json.Unmarshal(b, &s)
	return s, e
}
func (p *Postgres) Save(ctx context.Context, s core.State) error {
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	_, e = p.db.ExecContext(ctx, `INSERT INTO casehub_state(id,payload) VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET payload=EXCLUDED.payload,updated_at=NOW()`, b)
	return e
}

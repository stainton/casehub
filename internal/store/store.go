package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"casehub/internal/core"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Memory struct {
	mu     sync.RWMutex
	state  *core.State
	agents map[string]core.AgentConfig
}

func NewMemory() *Memory { return &Memory{agents: map[string]core.AgentConfig{}} }
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

func (m *Memory) AgentConfig(_ context.Context, id string) (core.AgentConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.agents[id]
	if !ok {
		return core.AgentConfig{}, core.ErrNotFound
	}
	return cfg, nil
}
func (m *Memory) UpdateAgentConfig(_ context.Context, id string, mutate func(core.AgentConfig) (core.AgentConfig, error)) (core.AgentConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next, e := mutate(m.agents[id])
	if e != nil {
		return core.AgentConfig{}, e
	}
	m.agents[id] = next
	return next, nil
}

type File struct {
	path     string
	mu       sync.Mutex
	agentsMu sync.Mutex
}

func NewFile(path string) *File { return &File{path: path} }

// Agent configuration is a table of its own in PostgreSQL; the file store keeps it in its own
// document beside the state file (data/casehub.json → data/casehub-agents.json) for the same reason:
// saving a configuration must not rewrite every case, folder and record.
func agentsPath(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + "-agents" + ext
}
func (f *File) loadAgents() (map[string]core.AgentConfig, error) {
	agents := map[string]core.AgentConfig{}
	b, e := os.ReadFile(agentsPath(f.path))
	if errors.Is(e, os.ErrNotExist) {
		return agents, nil
	}
	if e != nil {
		return nil, e
	}
	return agents, json.Unmarshal(b, &agents)
}
func (f *File) AgentConfig(_ context.Context, id string) (core.AgentConfig, error) {
	f.agentsMu.Lock()
	defer f.agentsMu.Unlock()
	agents, e := f.loadAgents()
	if e != nil {
		return core.AgentConfig{}, e
	}
	cfg, ok := agents[id]
	if !ok {
		return core.AgentConfig{}, core.ErrNotFound
	}
	return cfg, nil
}
func (f *File) UpdateAgentConfig(_ context.Context, id string, mutate func(core.AgentConfig) (core.AgentConfig, error)) (core.AgentConfig, error) {
	f.agentsMu.Lock()
	defer f.agentsMu.Unlock()
	agents, e := f.loadAgents()
	if e != nil {
		return core.AgentConfig{}, e
	}
	next, e := mutate(agents[id])
	if e != nil {
		return core.AgentConfig{}, e
	}
	agents[id] = next
	if e = writeJSONFile(agentsPath(f.path), agents); e != nil {
		return core.AgentConfig{}, e
	}
	return next, nil
}
func writeJSONFile(path string, v any) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
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
	return writeJSONFile(f.path, s)
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
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS casehub_state (id SMALLINT PRIMARY KEY CHECK(id=1), payload JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		// One row per agent: its business defaults, its setting.json text and the revision agents
		// compare against. Separate from casehub_state so saving a configuration does not rewrite the
		// whole state document, and so a configuration survives every agent redeploy.
		`CREATE TABLE IF NOT EXISTS casehub_agent_settings (agent_id TEXT PRIMARY KEY, defaults JSONB NOT NULL DEFAULT '{}'::jsonb, settings TEXT NOT NULL DEFAULT '', revision BIGINT NOT NULL DEFAULT 0, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	} {
		if _, e = db.ExecContext(ctx, ddl); e != nil {
			db.Close()
			return nil, e
		}
	}
	return &Postgres{db: db}, nil
}
func scanAgent(row interface{ Scan(...any) error }) (core.AgentConfig, error) {
	var cfg core.AgentConfig
	var defaults []byte
	if e := row.Scan(&defaults, &cfg.Settings, &cfg.Revision, &cfg.UpdatedAt); e != nil {
		return core.AgentConfig{}, e
	}
	return cfg, json.Unmarshal(defaults, &cfg.Defaults)
}
func (p *Postgres) AgentConfig(ctx context.Context, id string) (core.AgentConfig, error) {
	cfg, e := scanAgent(p.db.QueryRowContext(ctx, `SELECT defaults, settings, revision, updated_at FROM casehub_agent_settings WHERE agent_id=$1`, id))
	if errors.Is(e, sql.ErrNoRows) {
		return core.AgentConfig{}, core.ErrNotFound
	}
	return cfg, e
}

// The row is locked for the whole mutation, so two devices saving at once are ordered rather than
// interleaved and the revision check inside mutate decides which one is refused.
func (p *Postgres) UpdateAgentConfig(ctx context.Context, id string, mutate func(core.AgentConfig) (core.AgentConfig, error)) (core.AgentConfig, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return core.AgentConfig{}, e
	}
	defer func() { _ = tx.Rollback() }()
	if _, e = tx.ExecContext(ctx, `INSERT INTO casehub_agent_settings(agent_id) VALUES($1) ON CONFLICT DO NOTHING`, id); e != nil {
		return core.AgentConfig{}, e
	}
	current, e := scanAgent(tx.QueryRowContext(ctx, `SELECT defaults, settings, revision, updated_at FROM casehub_agent_settings WHERE agent_id=$1 FOR UPDATE`, id))
	if e != nil {
		return core.AgentConfig{}, e
	}
	next, e := mutate(current)
	if e != nil {
		return core.AgentConfig{}, e
	}
	defaults, e := json.Marshal(next.Defaults)
	if e != nil {
		return core.AgentConfig{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE casehub_agent_settings SET defaults=$2, settings=$3, revision=$4, updated_at=NOW() WHERE agent_id=$1`,
		id, defaults, next.Settings, next.Revision); e != nil {
		return core.AgentConfig{}, e
	}
	if e = tx.Commit(); e != nil {
		return core.AgentConfig{}, e
	}
	return next, nil
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

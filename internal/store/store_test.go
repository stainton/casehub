package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"casehub/internal/core"
	"casehub/internal/store"
)

func TestFileRepositoryPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	repo := store.NewFile(path)
	want := core.Seed()
	if err := repo.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.MainRevision != want.MainRevision || len(got.Cases) != len(want.Cases) {
		t.Fatalf("state mismatch")
	}
}

// Agent configuration is stored apart from the state document and behaves the same in every store:
// absent until written, then read back exactly, including the revision agents compare against.
func TestAgentConfigurationIsStoredPerAgent(t *testing.T) {
	repos := map[string]core.Repository{
		"memory": store.NewMemory(),
		"file":   store.NewFile(filepath.Join(t.TempDir(), "state.json")),
	}
	for name, repo := range repos {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := repo.AgentConfig(ctx, "playwright"); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("unconfigured agent = %v, want ErrNotFound", err)
			}
			want := core.AgentConfig{Defaults: core.AgentSettings{BaseURL: "https://target.test", TimeoutMinutes: 20},
				Settings: `{"model":"stored"}`, Revision: 3}
			if _, err := repo.UpdateAgentConfig(ctx, "playwright", func(core.AgentConfig) (core.AgentConfig, error) { return want, nil }); err != nil {
				t.Fatal(err)
			}
			got, err := repo.AgentConfig(ctx, "playwright")
			if err != nil || got.Settings != want.Settings || got.Revision != want.Revision || got.Defaults != want.Defaults {
				t.Fatalf("stored %+v, read back %+v (%v)", want, got, err)
			}
			// A refused mutation stores nothing, and one agent's row never touches another's.
			refused := errors.New("refused")
			if _, err = repo.UpdateAgentConfig(ctx, "playwright", func(core.AgentConfig) (core.AgentConfig, error) {
				return core.AgentConfig{Settings: "{}"}, refused
			}); !errors.Is(err, refused) {
				t.Fatalf("mutate error = %v", err)
			}
			if got, _ = repo.AgentConfig(ctx, "playwright"); got.Settings != want.Settings {
				t.Fatal("refused mutation was stored")
			}
			if _, err = repo.AgentConfig(ctx, "generator"); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("generator inherited a configuration: %v", err)
			}
		})
	}
	// The state document and the agent document are independent: saving state keeps the configuration.
	path := filepath.Join(t.TempDir(), "state.json")
	repo := store.NewFile(path)
	if _, err := repo.UpdateAgentConfig(context.Background(), "playwright", func(core.AgentConfig) (core.AgentConfig, error) {
		return core.AgentConfig{Settings: "{}", Revision: 1}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), core.Seed()); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.AgentConfig(context.Background(), "playwright"); err != nil || got.Revision != 1 {
		t.Fatalf("saving state dropped the agent configuration: %+v %v", got, err)
	}
}

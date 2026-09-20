package store_test

import (
	"bytes"
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

// Assets behave the same in every store: absent until saved, then read back exactly (metadata and
// bytes both), filterable by type, and gone (not merely emptied) after delete.
func TestAssetsAreStoredPerBackend(t *testing.T) {
	repos := map[string]core.Repository{
		"memory": store.NewMemory(),
		"file":   store.NewFile(filepath.Join(t.TempDir(), "state.json")),
	}
	for name, repo := range repos {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := repo.GetAssetMeta(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("missing asset meta = %v, want ErrNotFound", err)
			}
			if _, err := repo.GetAsset(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("missing asset = %v, want ErrNotFound", err)
			}
			image := core.Asset{AssetMeta: core.AssetMeta{ID: "a1", Type: core.AssetImage, Name: "logo.png",
				MimeType: "image/png", Size: 3, SHA256: "x"}, Data: []byte{1, 2, 3}}
			video := core.Asset{AssetMeta: core.AssetMeta{ID: "a2", Type: core.AssetVideo, Name: "clip.mp4",
				MimeType: "video/mp4", Size: 2, SHA256: "y"}, Data: []byte{9, 9}}
			if err := repo.SaveAsset(ctx, image); err != nil {
				t.Fatal(err)
			}
			if err := repo.SaveAsset(ctx, video); err != nil {
				t.Fatal(err)
			}
			meta, err := repo.GetAssetMeta(ctx, "a1")
			if err != nil || meta.Name != "logo.png" || meta.Type != core.AssetImage {
				t.Fatalf("stored meta mismatch: %+v (%v)", meta, err)
			}
			full, err := repo.GetAsset(ctx, "a1")
			if err != nil || !bytes.Equal(full.Data, image.Data) {
				t.Fatalf("stored bytes mismatch: %+v (%v)", full, err)
			}
			all, err := repo.ListAssets(ctx, "")
			if err != nil || len(all) != 2 {
				t.Fatalf("list all = %+v (%v)", all, err)
			}
			onlyVideo, err := repo.ListAssets(ctx, core.AssetVideo)
			if err != nil || len(onlyVideo) != 1 || onlyVideo[0].ID != "a2" {
				t.Fatalf("list by type = %+v (%v)", onlyVideo, err)
			}
			if err = repo.DeleteAsset(ctx, "a1"); err != nil {
				t.Fatal(err)
			}
			if _, err = repo.GetAssetMeta(ctx, "a1"); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("deleted asset still readable: %v", err)
			}
			if err = repo.DeleteAsset(ctx, "a1"); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("deleting twice = %v, want ErrNotFound", err)
			}
		})
	}
}

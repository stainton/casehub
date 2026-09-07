package store_test

import (
	"context"
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

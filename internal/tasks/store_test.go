package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// conformance is what the dispatcher relies on from every store.
func conformance(t *testing.T, s Store, reopen func() Store) {
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour).UTC()
	done := Task{ID: NewID(), Kind: "planner", Status: Failed, CreatedAt: old, FinishedAt: &old, Subject: "req:1", CheckpointSHA: "abc"}
	open := Task{ID: NewID(), Kind: "generator", Status: Running, CreatedAt: time.Now().UTC(), Meta: json.RawMessage(`{"caseIDs":["C1"]}`)}
	handled := Task{ID: NewID(), Kind: "generator", Status: Succeeded, CreatedAt: time.Now().Add(-time.Minute).UTC(), FinishedAt: &old, Handled: true, Result: json.RawMessage(`{"x":1}`)}
	for _, task := range []Task{done, open, handled} {
		if err := s.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	for i := int64(1); i <= 5; i++ {
		if err := s.AppendEvent(ctx, open.ID, i, json.RawMessage(`{"id":`+strconv.FormatInt(i, 10)+`}`), 3); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutBlob(ctx, done.ID, BlobCheckpoint, []byte("session")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBlob(ctx, done.ID, BlobCheckpoint, []byte("session v2")); err != nil {
		t.Fatal("a blob may be replaced: ", err)
	}
	if reopen != nil {
		s = reopen() // a new process
	}
	got, err := s.GetTask(ctx, handled.ID)
	if err != nil || !sameJSON(got.Result, `{"x":1}`) || !got.Handled || len(got.Meta) != 0 && string(got.Meta) != "null" {
		t.Fatalf("task round trip: %+v %v", got, err)
	}
	if _, err := s.GetTask(ctx, NewID()); err != ErrNotFound {
		t.Fatalf("missing task: %v", err)
	}
	if events, _ := s.Events(ctx, open.ID, 3); len(events) != 2 || !sameJSON(events[0], `{"id":4}`) {
		t.Fatalf("events after 3 = %s", events)
	}
	if events, _ := s.Events(ctx, open.ID, 0); len(events) != 3 {
		t.Fatalf("only the last keep events are kept, got %d", len(events))
	}
	if blob, _ := s.GetBlob(ctx, done.ID, BlobCheckpoint); string(blob) != "session v2" {
		t.Fatalf("blob = %q", blob)
	}
	list, _ := s.ListTasks(ctx, Filter{Kinds: []string{"generator"}})
	if len(list) != 2 || list[0].ID != open.ID || list[1].Result != nil {
		t.Fatalf("listings filter by kind, newest first, without results: %+v", list)
	}
	if list, _ := s.ListTasks(ctx, Filter{Kinds: []string{"generator"}, Active: true}); len(list) != 1 || list[0].ID != open.ID {
		t.Fatal("active leaves handled tasks out")
	}
	if list, _ := s.ListTasks(ctx, Filter{Subject: "req:1"}); len(list) != 1 || list[0].CheckpointSHA != "abc" {
		t.Fatal("subject filter")
	}
	if list, _ := s.ListTasks(ctx, Filter{Statuses: []string{Queued, Running}}); len(list) != 1 {
		t.Fatal("status filter")
	}
	if list, _ := s.ListTasks(ctx, Filter{Limit: 1}); len(list) != 1 {
		t.Fatal("limit")
	}
	if n, _ := s.DeleteFinishedBefore(ctx, time.Now().Add(-time.Hour)); n != 2 {
		t.Fatalf("old finished tasks are pruned, unfinished ones never: %d", n)
	}
	if _, err := s.GetBlob(ctx, done.ID, BlobCheckpoint); err != ErrNotFound {
		t.Fatal("a pruned task's blobs go with it")
	}
	if _, err := s.GetTask(ctx, open.ID); err != nil {
		t.Fatal("the open task stays")
	}
}

// PostgreSQL normalises JSONB text; what matters is the same value.
func sameJSON(raw json.RawMessage, want string) bool {
	var a, b any
	return json.Unmarshal(raw, &a) == nil && json.Unmarshal([]byte(want), &b) == nil && reflect.DeepEqual(a, b)
}

func TestMemoryStore(t *testing.T) { conformance(t, NewMemory(), nil) }

func TestFileStore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	conformance(t, s, func() Store {
		s, err := NewFile(dir)
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
	if _, err := s.GetBlob(context.Background(), NewID(), "../escape"); err != ErrNotFound {
		t.Fatal("blob names are checked")
	}
}

// Set CASEHUB_TEST_DATABASE_URL to a disposable database to include PostgreSQL; its task tables are dropped first.
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("CASEHUB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CASEHUB_TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, table := range []string{"casehub_task_blobs", "casehub_task_events", "casehub_tasks"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewPostgres(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	conformance(t, s, func() Store {
		s, err := NewPostgres(ctx, db) // creating the tables again is a no-op
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

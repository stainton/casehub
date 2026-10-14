package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type event struct {
	ID      int64           `json:"id"`
	Payload json.RawMessage `json:"payload"`
}
type record struct {
	Task   Task    `json:"task"`
	Events []event `json:"events"`
}

// Local keeps tasks in memory and, with a directory, also as one JSON file per task (blobs beside it), which
// is what the file-backed CaseHub store uses. Everything is loaded at start: retention keeps the set small.
type Local struct {
	mu      sync.Mutex
	dir     string
	records map[string]*record
	blobs   map[string]map[string][]byte // memory mode only
}

func NewMemory() *Local {
	return &Local{records: map[string]*record{}, blobs: map[string]map[string][]byte{}}
}

var fileID = regexp.MustCompile(`^[0-9a-f-]{36}$`)

func NewFile(dir string) (*Local, error) {
	l := &Local{dir: dir, records: map[string]*record{}}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || !fileID.MatchString(strings.TrimSuffix(name, ".json")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var r record
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		l.records[r.Task.ID] = &r
	}
	return l, nil
}

func (l *Local) persist(r *record) error {
	if l.dir == "" {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	file := filepath.Join(l.dir, r.Task.ID+".json")
	if err := os.WriteFile(file+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(file+".tmp", file)
}

func (l *Local) SaveTask(_ context.Context, t Task) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.records[t.ID]
	if r == nil {
		r = &record{}
		l.records[t.ID] = r
	}
	r.Task = t
	return l.persist(r)
}
func (l *Local) GetTask(_ context.Context, id string) (Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.records[id]
	if r == nil {
		return Task{}, ErrNotFound
	}
	return r.Task, nil
}
func (l *Local) ListTasks(_ context.Context, f Filter) ([]Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Task
	for _, r := range l.records {
		if f.Match(r.Task) {
			t := r.Task
			t.Result = nil
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}
func (l *Local) DeleteFinishedBefore(_ context.Context, cutoff time.Time) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for id, r := range l.records {
		if !Terminal(r.Task.Status) || r.Task.FinishedAt == nil || !r.Task.FinishedAt.Before(cutoff) {
			continue
		}
		delete(l.records, id)
		delete(l.blobs, id)
		if l.dir != "" {
			matches, _ := filepath.Glob(filepath.Join(l.dir, id+".*"))
			for _, file := range matches {
				if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
					return n, err
				}
			}
		}
		n++
	}
	return n, nil
}
func (l *Local) AppendEvent(_ context.Context, taskID string, id int64, payload json.RawMessage, keep int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.records[taskID]
	if r == nil {
		return ErrNotFound
	}
	r.Events = append(r.Events, event{ID: id, Payload: payload})
	if keep > 0 && len(r.Events) > keep {
		r.Events = append([]event(nil), r.Events[len(r.Events)-keep:]...)
	}
	return l.persist(r)
}
func (l *Local) Events(_ context.Context, taskID string, after int64) ([]json.RawMessage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.records[taskID]
	if r == nil {
		return nil, ErrNotFound
	}
	var out []json.RawMessage
	for _, e := range r.Events {
		if e.ID > after {
			out = append(out, e.Payload)
		}
	}
	return out, nil
}

var blobName = regexp.MustCompile(`^[a-z]+$`)

func (l *Local) PutBlob(_ context.Context, taskID, name string, data []byte) error {
	if !blobName.MatchString(name) {
		return errors.New("invalid blob name")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.records[taskID] == nil {
		return ErrNotFound
	}
	if l.dir == "" {
		if l.blobs[taskID] == nil {
			l.blobs[taskID] = map[string][]byte{}
		}
		l.blobs[taskID][name] = append([]byte(nil), data...)
		return nil
	}
	file := filepath.Join(l.dir, taskID+"."+name+".bin")
	if err := os.WriteFile(file+".tmp", data, 0o600); err != nil {
		return err
	}
	return os.Rename(file+".tmp", file)
}
func (l *Local) GetBlob(_ context.Context, taskID, name string) ([]byte, error) {
	if !blobName.MatchString(name) {
		return nil, ErrNotFound
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dir == "" {
		data, ok := l.blobs[taskID][name]
		if !ok {
			return nil, ErrNotFound
		}
		return append([]byte(nil), data...), nil
	}
	data, err := os.ReadFile(filepath.Join(l.dir, taskID+"."+name+".bin"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

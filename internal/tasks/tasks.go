// Package tasks is CaseHub's record of the work it hands to auto-test nodes (planner, generator, healer,
// executor). The nodes are stateless executors: they keep a task only while running it, so this record —
// status, progress events, result, the planner session to continue and a failed run's diagnostics — is the
// one that survives node restarts, scale-in and a browser that was closed while the task ran.
//
// What a task was submitted with is deliberately not part of it: requests carry test accounts, passwords and
// storageState, so the dispatcher keeps them in memory only until the task is done (see dispatch).
package tasks

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("task not found")

// Statuses mirror the nodes' own job statuses, so the frontend reads a CaseHub task exactly like a node job.
const (
	Queued    = "queued"
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"
	Cancelled = "cancelled"
)

func Terminal(status string) bool {
	return status == Succeeded || status == Failed || status == Cancelled
}

// Blob names stored beside a task.
const (
	BlobCheckpoint  = "checkpoint"
	BlobDiagnostics = "diagnostics"
	// BlobRequest is a queued task's request, kept only until a node has it (see dispatch.keepRequest).
	BlobRequest = "request"
)

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Task struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Stage   string `json:"stage"`
	Subject string `json:"subject,omitempty"`
	// Meta is the frontend's own bookkeeping for the task (which cases, which group, what was saved), opaque here.
	Meta        json.RawMessage `json:"meta,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	FinishedAt  *time.Time      `json:"finishedAt,omitempty"`
	LastEventID int64           `json:"lastEventId"`
	Error       *ErrorDetail    `json:"error,omitempty"`
	TimeoutMs   int64           `json:"timeoutMs,omitempty"`
	// Where the task runs or ran, and how far its progress has been copied from there.
	Node        string `json:"node,omitempty"`
	NodeJobID   string `json:"nodeJobId,omitempty"`
	NodeEventID int64  `json:"nodeEventId,omitempty"`
	Attempts    int    `json:"attempts,omitempty"`
	// A failed planner task can be continued: from its session archive (CheckpointSHA, on any node) or, for a
	// node too old to export one, from the session still on that node (LegacyContinue: Node + NodeJobID).
	CheckpointSHA  string     `json:"checkpointSha,omitempty"`
	LegacyContinue bool       `json:"legacyContinue,omitempty"`
	ContinuedAt    *time.Time `json:"continuedAt,omitempty"`
	Diagnostics    bool       `json:"diagnostics,omitempty"`
	// Handled marks a finished task the frontend has dealt with (imported, saved or dismissed).
	Handled bool            `json:"handled,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (t Task) Continuable() bool {
	return t.Status == Failed && t.ContinuedAt == nil && (t.CheckpointSHA != "" || t.LegacyContinue)
}

// Summary is the public shape, the same fields a node returns for its own job plus what only CaseHub knows.
func (t Task) Summary() map[string]any {
	out := map[string]any{"id": t.ID, "kind": t.Kind, "status": t.Status, "stage": t.Stage,
		"createdAt": t.CreatedAt, "updatedAt": t.UpdatedAt, "lastEventId": t.LastEventID}
	if t.StartedAt != nil {
		out["startedAt"] = t.StartedAt
	}
	if t.FinishedAt != nil {
		out["finishedAt"] = t.FinishedAt
	}
	if t.Error != nil {
		out["error"] = t.Error
	}
	if t.TimeoutMs > 0 {
		out["timeoutMs"] = t.TimeoutMs
	}
	if t.Continuable() {
		out["continuable"] = true
	}
	if t.ContinuedAt != nil {
		out["continuedAt"] = t.ContinuedAt
	}
	if t.Diagnostics {
		out["diagnostics"] = true
	}
	if t.Subject != "" {
		out["subject"] = t.Subject
	}
	if len(t.Meta) > 0 && string(t.Meta) != "null" {
		out["meta"] = t.Meta
	}
	if t.Handled {
		out["handled"] = true
	}
	if t.Attempts > 1 {
		out["attempts"] = t.Attempts
	}
	return out
}

type Filter struct {
	Kinds    []string
	Subject  string
	Active   bool // only unfinished tasks, and finished ones the frontend has not handled yet
	Statuses []string
	Limit    int
}

func (f Filter) Match(t Task) bool {
	if len(f.Kinds) > 0 && !contains(f.Kinds, t.Kind) {
		return false
	}
	if f.Subject != "" && t.Subject != f.Subject {
		return false
	}
	if len(f.Statuses) > 0 && !contains(f.Statuses, t.Status) {
		return false
	}
	return !f.Active || !t.Handled
}
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Store persists tasks. The dispatcher is the only writer (CaseHub runs one replica), so no store has to
// arbitrate concurrent updates of the same task.
type Store interface {
	SaveTask(ctx context.Context, t Task) error
	GetTask(ctx context.Context, id string) (Task, error)
	// ListTasks returns matching tasks newest first, without their results.
	ListTasks(ctx context.Context, f Filter) ([]Task, error)
	// DeleteFinishedBefore removes finished tasks (and their events and blobs) older than cutoff.
	DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error)
	// AppendEvent stores one progress event and drops all but the last keep events of the task.
	AppendEvent(ctx context.Context, taskID string, id int64, event json.RawMessage, keep int) error
	Events(ctx context.Context, taskID string, after int64) ([]json.RawMessage, error)
	PutBlob(ctx context.Context, taskID, name string, data []byte) error
	GetBlob(ctx context.Context, taskID, name string) ([]byte, error)
}

// NewID returns a random UUID v4, the id format nodes use and the frontend expects.
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

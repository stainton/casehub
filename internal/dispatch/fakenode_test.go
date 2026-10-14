package dispatch_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNode is an auto-test node with the HTTP contract of server/shared/http.mjs: capacity, admission with
// ?queue=false, SSE progress, result, release, checkpoint, diagnostics and asset pushes. legacy turns it into
// a node from before step 1: no capacity, no release, no checkpoint (it still keeps sessions to continue).
type fakeNode struct {
	t           *testing.T
	srv         *httptest.Server
	kind        string
	legacy      bool
	mu          sync.Mutex
	concurrency int
	jobs        map[string]*fakeJob
	assets      map[string][]byte
	submitted   []map[string]any
	released    []string
	cancelled   []string
	reject      func(body map[string]any) (int, string)
	changed     chan struct{}
}

type fakeJob struct {
	id, status, stage string
	started           bool
	events            []map[string]any
	continuable       bool
	diagnostics       bool
	err               map[string]string
	result            json.RawMessage
}

func newFakeNode(t *testing.T, kind string, concurrency int) *fakeNode {
	n := &fakeNode{t: t, kind: kind, concurrency: concurrency, jobs: map[string]*fakeJob{}, assets: map[string][]byte{}, changed: make(chan struct{})}
	n.srv = httptest.NewServer(http.HandlerFunc(n.serve))
	t.Cleanup(n.srv.Close)
	return n
}

func (n *fakeNode) URL() string { return n.srv.URL }

// notify wakes every waiting event stream (n.mu held).
func (n *fakeNode) notify() { close(n.changed); n.changed = make(chan struct{}) }

func (n *fakeNode) busy() int {
	count := 0
	for _, j := range n.jobs {
		if j.status == "queued" || j.status == "running" {
			count++
		}
	}
	return count
}

func (j *fakeJob) summary(kind string) map[string]any {
	out := map[string]any{"id": j.id, "kind": kind, "status": j.status, "stage": j.stage, "lastEventId": len(j.events)}
	if j.started {
		out["startedAt"] = time.Now().UTC().Format(time.RFC3339)
	}
	if j.continuable && j.status == "failed" {
		out["continuable"] = true
	}
	if j.diagnostics && j.status != "running" && j.status != "queued" {
		out["diagnostics"] = true
	}
	if j.err != nil {
		out["error"] = j.err
	}
	return out
}

func (j *fakeJob) emit(status, stage, message string, extra map[string]any) {
	j.status, j.stage = status, stage
	e := map[string]any{"id": len(j.events) + 1, "jobId": j.id, "status": status, "stage": stage, "message": message}
	for k, v := range extra {
		e[k] = v
	}
	j.events = append(j.events, e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func notFound(w http.ResponseWriter) {
	writeJSON(w, 404, map[string]any{"error": map[string]string{"code": "NOT_FOUND", "message": "Route not found"}})
}

func (n *fakeNode) serve(w http.ResponseWriter, r *http.Request) {
	base := "/v1/" + n.kind
	path := strings.TrimPrefix(r.URL.Path, base)
	switch {
	case path == "/capacity" && !n.legacy:
		n.mu.Lock()
		free := n.concurrency - n.busy()
		n.mu.Unlock()
		if free < 0 {
			free = 0
		}
		writeJSON(w, 200, map[string]any{"kind": n.kind, "concurrency": n.concurrency, "available": free, "accepting": true})
	case strings.HasPrefix(path, "/assets/"):
		sha := strings.TrimPrefix(path, "/assets/")
		n.mu.Lock()
		defer n.mu.Unlock()
		if r.Method == http.MethodHead {
			if _, ok := n.assets[sha]; ok {
				w.WriteHeader(200)
			} else {
				w.WriteHeader(404)
			}
			return
		}
		data, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != sha {
			writeJSON(w, 400, map[string]any{"error": map[string]string{"code": "INVALID_ASSET", "message": "hash mismatch"}})
			return
		}
		n.assets[sha] = data
		writeJSON(w, 201, map[string]any{"sha256": sha})
	case path == "/jobs" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.reject != nil {
			if status, code := n.reject(body); status != 0 {
				writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": "rejected: " + code}})
				return
			}
		}
		if !n.legacy && r.URL.Query().Get("queue") == "false" && n.busy() >= n.concurrency {
			writeJSON(w, 429, map[string]any{"error": map[string]string{"code": "BUSY", "message": "no idle slot"}})
			return
		}
		n.submitted = append(n.submitted, body)
		j := &fakeJob{id: fmt.Sprintf("00000000-0000-4000-8000-%012d", len(n.submitted)+len(n.jobs)*1000)}
		j.emit("queued", "queued", "accepted", nil)
		n.jobs[j.id] = j
		n.notify()
		writeJSON(w, 202, j.summary(n.kind))
	case strings.HasPrefix(path, "/jobs/"):
		parts := strings.Split(strings.TrimPrefix(path, "/jobs/"), "/")
		n.mu.Lock()
		j := n.jobs[parts[0]]
		n.mu.Unlock()
		if j == nil {
			writeJSON(w, 404, map[string]any{"error": map[string]string{"code": "JOB_NOT_FOUND", "message": "gone"}})
			return
		}
		resource := ""
		if len(parts) > 1 {
			resource = parts[1]
		}
		n.job(w, r, j, resource)
	default:
		notFound(w)
	}
}

func (n *fakeNode) job(w http.ResponseWriter, r *http.Request, j *fakeJob, resource string) {
	switch {
	case resource == "" && r.Method == http.MethodDelete:
		n.mu.Lock()
		n.cancelled = append(n.cancelled, j.id)
		if j.status == "queued" || j.status == "running" {
			j.emit("cancelled", "cancelled", "cancelled", nil)
			j.err = map[string]string{"code": "JOB_CANCELLED", "message": "cancelled"}
			n.notify()
		}
		s := j.summary(n.kind)
		n.mu.Unlock()
		writeJSON(w, 202, s)
	case resource == "":
		n.mu.Lock()
		s := j.summary(n.kind)
		n.mu.Unlock()
		writeJSON(w, 200, s)
	case resource == "result":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(j.result)
	case resource == "release" && !n.legacy:
		n.mu.Lock()
		n.released = append(n.released, j.id)
		delete(n.jobs, j.id)
		n.mu.Unlock()
		writeJSON(w, 200, map[string]any{"id": j.id, "released": true})
	case resource == "checkpoint" && !n.legacy:
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("session-of-" + j.id))
	case resource == "diagnostics" && !n.legacy:
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("diagnostics-of-" + j.id))
	case resource == "events":
		n.events(w, r, j)
	default:
		notFound(w)
	}
}

func (n *fakeNode) events(w http.ResponseWriter, r *http.Request, j *fakeJob) {
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	n.mu.Lock()
	snapshot, _ := json.Marshal(j.summary(n.kind))
	n.mu.Unlock()
	fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snapshot)
	flush()
	for {
		n.mu.Lock()
		events := j.events[min(after, len(j.events)):]
		terminal := j.status != "queued" && j.status != "running"
		changed := n.changed
		n.mu.Unlock()
		for _, e := range events {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %v\nevent: progress\ndata: %s\n\n", e["id"], data)
			after++
		}
		flush()
		if terminal {
			return
		}
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		}
	}
}

// ---- driving the fake from a test ----

func (n *fakeNode) waitJobs(count int) []*fakeJob {
	n.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n.mu.Lock()
		if len(n.submitted) >= count {
			var out []*fakeJob
			for _, j := range n.jobs {
				out = append(out, j)
			}
			n.mu.Unlock()
			return out
		}
		n.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	n.t.Fatalf("node %s never received %d task(s)", n.URL(), count)
	return nil
}
func (n *fakeNode) only() *fakeJob {
	jobs := n.waitJobs(1)
	n.mu.Lock()
	defer n.mu.Unlock()
	var latest *fakeJob
	for _, j := range jobs {
		if latest == nil || j.id > latest.id {
			latest = j
		}
	}
	return latest
}
func (n *fakeNode) start(j *fakeJob) {
	n.mu.Lock()
	defer n.mu.Unlock()
	j.started = true
	j.emit("running", "exploring", "looking at the page", map[string]any{"tool": "browser_snapshot"})
	n.notify()
}
func (n *fakeNode) succeed(j *fakeJob, result string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	j.result = json.RawMessage(result)
	j.emit("succeeded", "completed", "done", map[string]any{"casesGenerated": 3})
	n.notify()
}
func (n *fakeNode) fail(j *fakeJob, code string, continuable, diagnostics bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	j.continuable, j.diagnostics = continuable, diagnostics
	j.err = map[string]string{"code": code, "message": "failed with " + code}
	j.emit("failed", "failed", j.err["message"], nil)
	n.notify()
}
func (n *fakeNode) occupy() *fakeJob {
	n.mu.Lock()
	defer n.mu.Unlock()
	j := &fakeJob{id: fmt.Sprintf("ffffffff-0000-4000-8000-%012d", len(n.jobs))}
	j.emit("running", "exploring", "someone else's task", nil)
	n.jobs[j.id] = j
	return j
}
func (n *fakeNode) free(j *fakeJob) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.jobs, j.id)
}
func (n *fakeNode) submissions() []map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]map[string]any(nil), n.submitted...)
}
func (n *fakeNode) releasedIDs() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.released...)
}

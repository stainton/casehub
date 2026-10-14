package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"casehub/internal/tasks"
)

const maxSubmitBytes = 8 << 20

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, err error) {
	var h *HTTPError
	if errors.As(err, &h) {
		writeJSON(w, h.Status, map[string]any{"error": map[string]string{"code": h.Code, "message": h.Message}})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "INTERNAL_ERROR", "message": err.Error()}})
}

var jobRoute = regexp.MustCompile(`^/([0-9a-f-]{36})(?:/(result|events|diagnostics))?$`)

// Jobs serves /api/<kind>/jobs and below with the same contract as a node's /v1/<kind>/jobs — the frontend
// talks to CaseHub's tasks exactly as it talked to the node's jobs — plus PATCH for the frontend's own
// bookkeeping (meta, handled) and the diagnostics download.
func (d *Dispatcher) Jobs(kind string) http.Handler {
	prefix := "/api/" + kind + "/jobs"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if rest == "" || rest == "/" {
			if r.Method != http.MethodPost {
				writeError(w, &HTTPError{http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed"})
				return
			}
			d.submit(w, r, kind)
			return
		}
		m := jobRoute.FindStringSubmatch(rest)
		if m == nil {
			writeError(w, &HTTPError{http.StatusNotFound, "NOT_FOUND", "Route not found"})
			return
		}
		id, resource := m[1], m[2]
		switch {
		case resource == "" && r.Method == http.MethodGet:
			t, err := d.Get(r.Context(), kind, id)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, t.Summary())
		case resource == "" && r.Method == http.MethodDelete:
			t, err := d.Cancel(r.Context(), kind, id)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusAccepted, t.Summary())
		case resource == "" && r.Method == http.MethodPatch:
			var in struct {
				Meta    json.RawMessage `json:"meta"`
				Handled *bool           `json:"handled"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&in) != nil || len(in.Meta) > 64*1024 {
				writeError(w, &HTTPError{http.StatusBadRequest, "INVALID_REQUEST", "Body must be {meta?, handled?} with meta ≤ 64 KiB"})
				return
			}
			t, err := d.Update(r.Context(), kind, id, in.Meta, in.Handled)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, t.Summary())
		case r.Method != http.MethodGet:
			writeError(w, &HTTPError{http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed"})
		case resource == "result":
			t, err := d.Get(r.Context(), kind, id)
			if err != nil {
				writeError(w, err)
				return
			}
			if t.Status != tasks.Succeeded || t.Result == nil {
				writeError(w, &HTTPError{http.StatusConflict, "RESULT_UNAVAILABLE", "Result unavailable while job is " + t.Status})
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(t.Result)
		case resource == "diagnostics":
			t, err := d.Get(r.Context(), kind, id)
			if err != nil {
				writeError(w, err)
				return
			}
			data, err := d.store.GetBlob(r.Context(), t.ID, tasks.BlobDiagnostics)
			if err != nil {
				writeError(w, &HTTPError{http.StatusNotFound, "DIAGNOSTICS_UNAVAILABLE", "This task kept no diagnostics"})
				return
			}
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s-diagnostics.tar.gz"`, kind, id))
			_, _ = w.Write(data)
		default:
			d.events(w, r, kind, id)
		}
	})
}

func (d *Dispatcher) submit(w http.ResponseWriter, r *http.Request, kind string) {
	if !strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]), "application/json") {
		writeError(w, &HTTPError{http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSubmitBytes))
	if err != nil {
		writeError(w, &HTTPError{http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "Request body is too large"})
		return
	}
	t, err := d.Submit(r.Context(), kind, body)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", "/api/"+kind+"/jobs/"+t.ID)
	writeJSON(w, http.StatusAccepted, t.Summary())
}

// events streams a task's progress the way a node does: a snapshot first, then the stored events after the
// cursor (Last-Event-ID or ?after=N), then live ones until the task ends, with a heartbeat every 15 seconds.
func (d *Dispatcher) events(w http.ResponseWriter, r *http.Request, kind, id string) {
	t, err := d.Get(r.Context(), kind, id)
	if err != nil {
		writeError(w, err)
		return
	}
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("after")
	}
	if raw == "" {
		raw = "0"
	}
	after, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || after < 0 || after > t.LastEventID {
		writeError(w, &HTTPError{http.StatusBadRequest, "INVALID_CURSOR", "Event cursor must be a nonnegative integer no greater than lastEventId"})
		return
	}
	// Subscribe before reading what is stored, so nothing falls between the replay and the live events.
	ch, unsubscribe, snapshot, live := d.subscribe(id, t)
	defer unsubscribe()
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(event string, id int64, data []byte) {
		if id > 0 {
			fmt.Fprintf(w, "id: %d\n", id)
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	summary, _ := json.Marshal(snapshot.Summary())
	send("snapshot", 0, summary)
	stored, err := d.store.Events(r.Context(), id, after)
	if err != nil {
		return
	}
	sent := after
	for i, payload := range stored {
		var head struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(payload, &head)
		if i == 0 && head.ID > after+1 {
			reset, _ := json.Marshal(map[string]any{"firstAvailableEventId": head.ID, "message": "Earlier progress events have expired"})
			send("reset", 0, reset)
		}
		send("progress", head.ID, payload)
		sent = head.ID
	}
	if !live {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case f, ok := <-ch:
			if !ok {
				return // ended, or this reader fell behind and reconnects from its cursor
			}
			if f.id > sent {
				send(f.event, f.id, f.data)
				sent = f.id
			}
		}
	}
}

// subscribe registers for a live task's events; live is false (and ch nil) for a finished one.
func (d *Dispatcher) subscribe(id string, fallback tasks.Task) (chan frame, func(), tasks.Task, bool) {
	d.mu.Lock()
	l := d.live[id]
	d.mu.Unlock()
	if l == nil {
		return nil, func() {}, fallback, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return nil, func() {}, l.task, false
	}
	ch := make(chan frame, 256)
	l.subs[ch] = struct{}{}
	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if _, ok := l.subs[ch]; ok {
			delete(l.subs, ch)
			close(ch)
		}
	}, l.task, true
}

// Tasks serves GET /api/tasks?kind=a,b&subject=…&active=1&limit=N: the frontend's way back to its tasks from
// any browser, instead of remembering job ids locally.
func (d *Dispatcher) Tasks() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, &HTTPError{http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed"})
			return
		}
		q := r.URL.Query()
		f := tasks.Filter{Subject: q.Get("subject"), Active: q.Get("active") == "1" || q.Get("active") == "true", Limit: 200}
		if kinds := q.Get("kind"); kinds != "" {
			f.Kinds = strings.Split(kinds, ",")
		}
		if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n < f.Limit {
			f.Limit = n
		}
		list, err := d.List(r.Context(), f)
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(list))
		for _, t := range list {
			out = append(out, d.current(t).Summary())
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
	})
}

// current prefers the in-memory state of a live task over its last stored copy.
func (d *Dispatcher) current(t tasks.Task) tasks.Task {
	d.mu.Lock()
	l := d.live[t.ID]
	d.mu.Unlock()
	if l == nil {
		return t
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.task
}

// Metrics serves the queue in Prometheus text format: what an autoscaler (KEDA, HPA external metrics) reads
// to size each node pool. Nothing reads it in a plain process deployment, and nothing depends on it.
func (d *Dispatcher) Metrics() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		load := d.Load()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintln(w, "# HELP casehub_tasks Unfinished tasks by kind: waiting for a node (queued) or handed to one (running).")
		fmt.Fprintln(w, "# TYPE casehub_tasks gauge")
		for _, kind := range d.Kinds() {
			fmt.Fprintf(w, "casehub_tasks{kind=%q,state=\"queued\"} %d\n", kind, load[kind][0])
			fmt.Fprintf(w, "casehub_tasks{kind=%q,state=\"running\"} %d\n", kind, load[kind][1])
		}
		fmt.Fprintln(w, "# HELP casehub_nodes Nodes currently in each pool (static list, or what its DNS name resolves to).")
		fmt.Fprintln(w, "# TYPE casehub_nodes gauge")
		for _, kind := range d.Kinds() {
			fmt.Fprintf(w, "casehub_nodes{kind=%q} %d\n", kind, d.Nodes(kind))
		}
	})
}

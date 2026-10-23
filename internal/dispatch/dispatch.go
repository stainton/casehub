package dispatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"casehub/internal/tasks"
	"casehub/internal/upstream"
)

// Kind is one kind of task and the nodes that run it. Name is also the route segment on both sides:
// /api/<name>/jobs here, /v1/<name>/jobs on a node.
type Kind struct {
	Name     string
	Pool     *Pool
	Settings upstream.Settings
	Assets   *upstream.Assets
	// Continuable kinds (the planner) keep an interrupted run's session so a person can continue it.
	Continuable bool
}

type Config struct {
	QueueTimeout  time.Duration // a task no node took within this fails with NO_NODE_AVAILABLE
	Retention     time.Duration // finished tasks (and their blobs) are deleted after this
	NodeLostAfter time.Duration // a node unreachable this long has lost its task
	MaxAttempts   int           // a task interrupted by its node is run again at most this many times in all
	KeepEvents    int
	Client        *http.Client
	Logf          func(format string, args ...any)
}

func (c *Config) defaults() {
	if c.QueueTimeout <= 0 {
		c.QueueTimeout = 30 * time.Minute
	}
	if c.Retention <= 0 {
		c.Retention = 7 * 24 * time.Hour
	}
	if c.NodeLostAfter <= 0 {
		c.NodeLostAfter = 2 * time.Minute
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.KeepEvents <= 0 {
		c.KeepEvents = 500
	}
	if c.Client == nil {
		c.Client = &http.Client{}
	}
	if c.Logf == nil {
		c.Logf = log.Printf
	}
}

// request is what a task was submitted with. It carries test credentials, so it lives in memory — except while the
// task waits for a node, when it is also kept in the store (keepRequest) so a restart does not lose the queue;
// it is wiped as soon as a node has it. A task whose request is gone cannot be queued again.
type request struct {
	body     map[string]json.RawMessage
	affinity string
	// A planner continuation: the session archive to push (checkpointSHA from task source), or, for a node too
	// old to export one, the node still holding it and its job there.
	source       string
	checkpoint   string
	legacyNode   string
	legacyNodeID string
}

type frame struct {
	event string
	id    int64
	data  []byte
}

// live is an unfinished task. Its mutex orders everything that changes it: the scheduler, the goroutine
// copying its progress from a node, and a person cancelling it.
type live struct {
	mu          sync.Mutex
	task        tasks.Task
	req         *request
	kind        *Kind
	subs        map[chan frame]struct{}
	queuedSince time.Time
	waiting     string // why it is still queued, announced once per change
	dispatching bool
	cancel      bool // cancelled while being handed to a node
	nodeFinal   json.RawMessage
	done        bool
	requestKept bool // the request is in the store (it waits for a node)
}

type Dispatcher struct {
	store tasks.Store
	kinds map[string]*Kind
	cfg   Config

	// mu guards the maps below. Lock order: a task's own mutex may be held while taking mu, never the reverse
	// (snapshot the live tasks under mu, release it, then lock each task).
	mu       sync.Mutex
	live     map[string]*live
	affinity map[string]string
	wake     chan struct{}
	ctx      context.Context
	stop     context.CancelFunc
	wg       sync.WaitGroup
}

func New(store tasks.Store, kinds []*Kind, cfg Config) *Dispatcher {
	cfg.defaults()
	d := &Dispatcher{store: store, kinds: map[string]*Kind{}, cfg: cfg, live: map[string]*live{},
		affinity: map[string]string{}, wake: make(chan struct{}, 1)}
	for _, k := range kinds {
		if k != nil && k.Pool != nil {
			d.kinds[k.Name] = k
		}
	}
	d.ctx, d.stop = context.WithCancel(context.Background())
	return d
}

func (d *Dispatcher) Enabled(kind string) bool { return d.kinds[kind] != nil }

// Start picks up where a previous CaseHub left off and runs the scheduler. A task already on a node is
// followed there again; one that was still waiting lost its request with the old process and fails.
func (d *Dispatcher) Start(ctx context.Context) error {
	open, err := d.store.ListTasks(ctx, tasks.Filter{Statuses: []string{tasks.Queued, tasks.Running}})
	if err != nil {
		return err
	}
	for _, summary := range open {
		t, err := d.store.GetTask(ctx, summary.ID)
		if err != nil {
			return err
		}
		k := d.kinds[t.Kind]
		l := &live{task: t, kind: k, subs: map[chan frame]struct{}{}, queuedSince: time.Now()}
		d.mu.Lock()
		d.live[t.ID] = l
		d.mu.Unlock()
		switch {
		case k == nil:
			d.finish(l, tasks.Failed, &tasks.ErrorDetail{Code: "SERVICE_DISABLED", Message: "该任务对应的服务已不再配置，任务无法继续"}, nil)
		case t.Node != "" && t.NodeJobID != "":
			d.follow(l)
		case t.Node == "" && d.restoreRequest(ctx, l):
			// It was still waiting for a node: back in the queue with its request, as if nothing happened.
		default:
			d.finish(l, tasks.Failed, &tasks.ErrorDetail{Code: "CASEHUB_RESTARTED", Message: "CaseHub 重启时任务尚未交给执行节点，请重新提交"}, nil)
		}
	}
	d.wg.Add(2)
	d.poke() // tasks restored into the queue go out as soon as a node has room
	go d.schedule()
	go d.prune()
	return nil
}

func (d *Dispatcher) Close() {
	d.stop()
	d.wg.Wait()
}

func (d *Dispatcher) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// ---- submission ---------------------------------------------------------------------------------------

// HTTPError is a refusal with the status and code the frontend sees.
type HTTPError struct {
	Status        int
	Code, Message string
}

func (e *HTTPError) Error() string { return e.Message }

// Submit records a task and tries to hand it to an idle node at once, so a request a node rejects (invalid
// input, an asset that cannot be pushed) is refused here exactly as before; otherwise the task waits in the
// queue. body is the frontend's request; the CaseHub-only field casehubTask carries subject and meta.
func (d *Dispatcher) Submit(ctx context.Context, kindName string, body []byte) (tasks.Task, error) {
	k := d.kinds[kindName]
	if k == nil {
		return tasks.Task{}, &HTTPError{http.StatusServiceUnavailable, upstream.ErrorCode(kindName) + "_DISABLED", kindName + " 服务未配置"}
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || payload == nil {
		return tasks.Task{}, &HTTPError{http.StatusBadRequest, "INVALID_JSON", "Request body must be a JSON object"}
	}
	now := time.Now().UTC()
	t := tasks.Task{ID: tasks.NewID(), Kind: k.Name, Status: tasks.Queued, Stage: "queued", CreatedAt: now, UpdatedAt: now}
	if raw, ok := payload["casehubTask"]; ok {
		delete(payload, "casehubTask")
		var extra struct {
			Subject string          `json:"subject"`
			Meta    json.RawMessage `json:"meta"`
		}
		if json.Unmarshal(raw, &extra) != nil || len(extra.Subject) > 200 || len(extra.Meta) > 64*1024 {
			return tasks.Task{}, &HTTPError{http.StatusBadRequest, "INVALID_REQUEST", "casehubTask must be {subject (≤200 chars), meta (≤64 KiB)}"}
		}
		t.Subject, t.Meta = extra.Subject, extra.Meta
	}
	req := &request{body: payload, affinity: affinityKey(payload)}
	var source *tasks.Task
	if raw, ok := payload["continueFrom"]; ok && k.Continuable {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return tasks.Task{}, &HTTPError{http.StatusBadRequest, "INVALID_REQUEST", "continueFrom must be the id of the task to continue"}
		}
		s, err := d.claimContinuation(ctx, k.Name, id)
		if err != nil {
			return tasks.Task{}, err
		}
		source = &s
		delete(payload, "continueFrom")
		req.source, req.checkpoint = s.ID, s.CheckpointSHA
		if s.CheckpointSHA == "" {
			req.legacyNode, req.legacyNodeID = s.Node, s.NodeJobID
		}
	}
	l := &live{task: t, req: req, kind: k, subs: map[chan frame]struct{}{}, queuedSince: now, dispatching: true}
	out := d.attempt(ctx, l)
	if out.rejected != nil {
		if source != nil {
			d.unclaim(ctx, source.ID)
		}
		return tasks.Task{}, out.rejected
	}
	if err := d.store.SaveTask(ctx, l.task); err != nil {
		return tasks.Task{}, err
	}
	d.mu.Lock()
	d.live[t.ID] = l
	d.mu.Unlock()
	if out.node == "" { // no node took it yet: it has to survive a restart while it waits
		d.keepRequest(ctx, l)
	}
	l.mu.Lock()
	d.event(l, map[string]any{"stage": "queued", "message": "任务已提交"})
	l.dispatching = false
	l.mu.Unlock()
	d.settle(l, out)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.task, nil
}

func (d *Dispatcher) claimContinuation(ctx context.Context, kind, id string) (tasks.Task, error) {
	refuse := &HTTPError{http.StatusConflict, "NOT_CONTINUABLE", "This task cannot be continued; start a new one"}
	d.mu.Lock()
	defer d.mu.Unlock()
	s, err := d.store.GetTask(ctx, id)
	if err != nil || s.Kind != kind || !s.Continuable() {
		return tasks.Task{}, refuse
	}
	at := time.Now().UTC()
	s.ContinuedAt = &at
	return s, d.store.SaveTask(ctx, s)
}
func (d *Dispatcher) unclaim(ctx context.Context, id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, err := d.store.GetTask(ctx, id); err == nil {
		s.ContinuedAt = nil
		_ = d.store.SaveTask(ctx, s)
	}
}

// One login per target origin and test account: a node that already logged in for it can skip the login,
// so the scheduler prefers that node when it is idle. Only a hint; the key holds no credential.
func affinityKey(payload map[string]json.RawMessage) string {
	var target struct {
		BaseURL string `json:"baseUrl"`
	}
	var context struct {
		TestData struct {
			Username string `json:"username"`
		} `json:"testData"`
	}
	_ = json.Unmarshal(payload["target"], &target)
	_ = json.Unmarshal(payload["context"], &context)
	u, err := url.Parse(target.BaseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(u.Scheme + "://" + u.Host + "|" + context.TestData.Username))
	return hex.EncodeToString(sum[:8])
}

// ---- handing a task to a node ---------------------------------------------------------------------------

type outcome struct {
	node     string
	job      nodeJob
	rejected *HTTPError
	waiting  string // why nothing took it: every node busy, or none reachable
}

type nodeJob struct {
	ID          string             `json:"id"`
	Status      string             `json:"status"`
	Stage       string             `json:"stage"`
	StartedAt   string             `json:"startedAt"`
	TimeoutMs   int64              `json:"timeoutMs"`
	Continuable bool               `json:"continuable"`
	Diagnostics bool               `json:"diagnostics"`
	Error       *tasks.ErrorDetail `json:"error"`
}

func (d *Dispatcher) nodeURL(node string, k *Kind, rest string) string {
	return node + "/v1/" + k.Name + rest
}

// attempt offers the task to each node in turn (the one holding a reusable login first) and stops at the first
// that accepts it, or at a rejection of the request itself. Called without l.mu held: pushing assets is slow.
func (d *Dispatcher) attempt(ctx context.Context, l *live) outcome {
	k, req := l.kind, l.req
	var nodes []string
	if req.legacyNode != "" {
		nodes = []string{req.legacyNode}
	} else {
		nodes = k.Pool.Nodes(ctx)
		if hint := d.affinityNode(req.affinity); hint != "" {
			for i, n := range nodes {
				if n == hint {
					nodes = append(append([]string{n}, nodes[:i]...), nodes[i+1:]...)
					break
				}
			}
		}
	}
	if len(nodes) == 0 {
		return outcome{waiting: "当前没有可用的执行节点，等待节点上线"}
	}
	busy := false
	for _, node := range nodes {
		if free, known := d.capacity(ctx, k, node); known && free < 1 {
			busy = true
			continue
		}
		body := make(map[string]json.RawMessage, len(req.body)+1)
		for key, v := range req.body {
			body[key] = v
		}
		if err := upstream.Prepare(ctx, d.cfg.Client, node, k.Name, body, k.Settings, k.Assets); err != nil {
			if upstream.IsNodeError(err) {
				continue
			}
			return outcome{rejected: prepareError(k.Name, err)}
		}
		switch {
		case req.checkpoint != "":
			archive, err := d.store.GetBlob(ctx, req.source, tasks.BlobCheckpoint)
			if err != nil {
				return outcome{rejected: &HTTPError{http.StatusConflict, "NOT_CONTINUABLE", "The saved session of that task is gone; start a new one"}}
			}
			if err := upstream.PushBlob(ctx, d.cfg.Client, node, k.Name, req.checkpoint, int64(len(archive)), bytes.NewReader(archive), "checkpoint"); err != nil {
				continue
			}
			body["continueFrom"], _ = json.Marshal(map[string]string{"checkpoint": req.checkpoint})
		case req.legacyNodeID != "":
			body["continueFrom"], _ = json.Marshal(req.legacyNodeID)
		}
		encoded, _ := json.Marshal(body)
		status, response, err := d.call(ctx, http.MethodPost, d.nodeURL(node, k, "/jobs?queue=false"), encoded, 60*time.Second)
		if err != nil || status >= 500 && status != http.StatusServiceUnavailable {
			continue
		}
		switch {
		case status == http.StatusAccepted:
			var job nodeJob
			if json.Unmarshal(response, &job) != nil || job.ID == "" {
				continue
			}
			return outcome{node: node, job: job}
		case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable:
			busy = true
		default:
			return outcome{rejected: nodeRejection(status, response)}
		}
	}
	if busy {
		return outcome{waiting: "所有执行节点都在忙，排队等待空闲节点"}
	}
	return outcome{waiting: "暂时联系不上执行节点，等待重试"}
}

func prepareError(service string, err error) *HTTPError {
	w := &bufferWriter{header: http.Header{}}
	upstream.WriteError(w, service, err)
	return nodeRejection(w.status, w.body.Bytes())
}

type bufferWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *bufferWriter) Header() http.Header         { return w.header }
func (w *bufferWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *bufferWriter) WriteHeader(status int)      { w.status = status }

func nodeRejection(status int, body []byte) *HTTPError {
	var out struct {
		Error tasks.ErrorDetail `json:"error"`
	}
	if json.Unmarshal(body, &out) != nil || out.Error.Code == "" {
		return &HTTPError{status, "NODE_REJECTED", fmt.Sprintf("执行节点拒绝了该任务（HTTP %d）", status)}
	}
	return &HTTPError{status, out.Error.Code, out.Error.Message}
}

// capacity asks a node for its free slots. known is false for a node that does not report them (an older
// auto-test): it is simply offered the task.
func (d *Dispatcher) capacity(ctx context.Context, k *Kind, node string) (free int, known bool) {
	status, body, err := d.call(ctx, http.MethodGet, d.nodeURL(node, k, "/capacity"), nil, 3*time.Second)
	if err != nil || status != http.StatusOK {
		return 0, false
	}
	var c struct {
		Available int  `json:"available"`
		Accepting bool `json:"accepting"`
	}
	if json.Unmarshal(body, &c) != nil {
		return 0, false
	}
	if !c.Accepting {
		return 0, true
	}
	return c.Available, true
}

func (d *Dispatcher) call(ctx context.Context, method, u string, body []byte, timeout time.Duration) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	r, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.cfg.Client.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, data, err
}

func (d *Dispatcher) affinityNode(key string) string {
	if key == "" {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.affinity[key]
}
func (d *Dispatcher) remember(key, node string) {
	if key == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.affinity) > 1000 {
		d.affinity = map[string]string{}
	}
	d.affinity[key] = node
}

// settle applies what an attempt achieved. Called without l.mu held.
func (d *Dispatcher) settle(l *live, out outcome) {
	l.mu.Lock()
	switch {
	case l.done:
		l.mu.Unlock()
		if out.node != "" { // cancelled meanwhile: the node must not keep running it
			_, _, _ = d.call(d.ctx, http.MethodDelete, d.nodeURL(out.node, l.kind, "/jobs/"+out.job.ID), nil, 10*time.Second)
		}
		return
	case out.rejected != nil:
		d.finishLocked(l, tasks.Failed, &tasks.ErrorDetail{Code: out.rejected.Code, Message: out.rejected.Message}, nil)
		l.mu.Unlock()
		return
	case out.node == "":
		if time.Since(l.queuedSince) > d.cfg.QueueTimeout {
			d.finishLocked(l, tasks.Failed, &tasks.ErrorDetail{Code: "NO_NODE_AVAILABLE",
				Message: fmt.Sprintf("%s 内没有执行节点接收该任务，请检查节点状态后重新提交", d.cfg.QueueTimeout)}, nil)
		} else if l.waiting != out.waiting {
			l.waiting = out.waiting
			d.event(l, map[string]any{"stage": "queued", "message": out.waiting})
		}
		l.mu.Unlock()
		return
	}
	t := &l.task
	t.Node, t.NodeJobID, t.NodeEventID = out.node, out.job.ID, 0
	t.Attempts++
	t.Status = tasks.Running
	if out.job.Status == tasks.Queued {
		t.Status = tasks.Queued
	}
	l.waiting = ""
	d.remember(l.req.affinity, out.node)
	d.event(l, map[string]any{"stage": "dispatched", "message": "已交给执行节点 " + out.node})
	cancel := l.cancel
	l.mu.Unlock()
	d.dropRequest(l)
	if cancel {
		_, _, _ = d.call(d.ctx, http.MethodDelete, d.nodeURL(out.node, l.kind, "/jobs/"+out.job.ID), nil, 10*time.Second)
	}
	d.follow(l)
}

// ---- the scheduler --------------------------------------------------------------------------------------

func (d *Dispatcher) schedule() {
	defer d.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.wake:
		case <-ticker.C:
		}
		d.dispatchQueued()
	}
}

// dispatchQueued hands out waiting tasks oldest first. Once one task of a kind finds no node, later tasks of
// that kind wait too (they would find none either), except continuations bound to one particular node.
func (d *Dispatcher) snapshot() []*live {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*live, 0, len(d.live))
	for _, l := range d.live {
		out = append(out, l)
	}
	return out
}

func (d *Dispatcher) dispatchQueued() {
	var queued []*live
	for _, l := range d.snapshot() {
		l.mu.Lock()
		if l.task.Status == tasks.Queued && l.task.Node == "" && !l.dispatching && !l.done {
			queued = append(queued, l)
		}
		l.mu.Unlock()
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].task.CreatedAt.Before(queued[j].task.CreatedAt) })
	blocked := map[string]bool{}
	for _, l := range queued {
		if d.ctx.Err() != nil {
			return
		}
		pinned := l.req != nil && l.req.legacyNode != ""
		if blocked[l.kind.Name] && !pinned {
			if time.Since(l.queuedSince) > d.cfg.QueueTimeout {
				d.settle(l, outcome{waiting: "timeout"})
			}
			continue
		}
		l.mu.Lock()
		if l.done {
			l.mu.Unlock()
			continue
		}
		if l.req == nil {
			d.finishLocked(l, tasks.Failed, &tasks.ErrorDetail{Code: "CASEHUB_RESTARTED", Message: "任务需要重新派发，但 CaseHub 重启后已没有它的请求内容，请重新提交"}, nil)
			l.mu.Unlock()
			continue
		}
		l.dispatching = true
		l.mu.Unlock()
		ctx, cancel := context.WithTimeout(d.ctx, 5*time.Minute)
		out := d.attempt(ctx, l)
		cancel()
		l.mu.Lock()
		l.dispatching = false
		l.mu.Unlock()
		if out.node == "" && out.rejected == nil && !pinned {
			blocked[l.kind.Name] = true
		}
		d.settle(l, out)
	}
}

func (d *Dispatcher) prune() {
	defer d.wg.Done()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if n, err := d.store.DeleteFinishedBefore(d.ctx, time.Now().Add(-d.cfg.Retention)); err != nil {
			d.cfg.Logf("dispatch: pruning finished tasks failed: %v", err)
		} else if n > 0 {
			d.cfg.Logf("dispatch: pruned %d finished tasks", n)
		}
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ---- events, finishing -----------------------------------------------------------------------------------

// event records one progress event for the task (l.mu held): its own numbering, whichever nodes it ran on.
func (d *Dispatcher) event(l *live, fields map[string]any) {
	t := &l.task
	t.LastEventID++
	now := time.Now().UTC()
	t.UpdatedAt = now
	if stage, ok := fields["stage"].(string); ok {
		t.Stage = stage
	}
	fields["id"], fields["jobId"], fields["status"], fields["createdAt"] = t.LastEventID, t.ID, t.Status, now
	payload, _ := json.Marshal(fields)
	ctx := context.Background()
	if err := d.store.SaveTask(ctx, *t); err != nil {
		d.cfg.Logf("dispatch: saving task %s failed: %v", t.ID, err)
	}
	if err := d.store.AppendEvent(ctx, t.ID, t.LastEventID, payload, d.cfg.KeepEvents); err != nil {
		d.cfg.Logf("dispatch: saving an event of task %s failed: %v", t.ID, err)
	}
	for ch := range l.subs {
		select {
		case ch <- frame{event: "progress", id: t.LastEventID, data: payload}:
		default: // a slow reader reconnects with Last-Event-ID instead of holding memory here
			delete(l.subs, ch)
			close(ch)
		}
	}
}

func (d *Dispatcher) finish(l *live, status string, detail *tasks.ErrorDetail, final json.RawMessage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d.finishLocked(l, status, detail, final)
}

// finishLocked ends the task (l.mu held). final is the node's own terminal event when there is one, so fields
// a workflow adds to it (cases generated, passed) reach the frontend unchanged.
func (d *Dispatcher) finishLocked(l *live, status string, detail *tasks.ErrorDetail, final json.RawMessage) {
	if l.done {
		return
	}
	t := &l.task
	now := time.Now().UTC()
	t.Status, t.FinishedAt, t.Error = status, &now, detail
	fields := map[string]any{}
	if final != nil {
		_ = json.Unmarshal(final, &fields)
		delete(fields, "jobId")
	}
	if _, ok := fields["stage"]; !ok || final == nil {
		fields["stage"] = map[string]string{tasks.Succeeded: "completed", tasks.Failed: "failed", tasks.Cancelled: "cancelled"}[status]
	}
	if detail != nil && (final == nil || status != tasks.Succeeded) {
		fields["message"] = detail.Message
	}
	d.event(l, fields)
	l.done = true
	for ch := range l.subs {
		close(ch)
	}
	l.subs = map[chan frame]struct{}{}
	l.req = nil // the credentials it carried are no longer needed
	d.dropRequestLocked(l)
	d.mu.Lock()
	delete(d.live, t.ID)
	d.mu.Unlock()
	d.poke()
}

// requeue puts an interrupted task back in the queue (l.mu held), or fails it when it cannot run again.
func (d *Dispatcher) requeueLocked(l *live, why string) {
	t := &l.task
	if l.req == nil || t.Attempts >= d.cfg.MaxAttempts {
		reason := "任务被执行节点中断，且已达到重试上限"
		if l.req == nil {
			reason = "任务被执行节点中断；CaseHub 重启后已没有它的请求内容，请重新提交"
		}
		d.finishLocked(l, tasks.Failed, &tasks.ErrorDetail{Code: "NODE_LOST", Message: reason + "（" + why + "）"}, nil)
		return
	}
	t.Status, t.Node, t.NodeJobID, t.NodeEventID, t.StartedAt = tasks.Queued, "", "", 0, nil
	d.keepRequestLocked(l)
	l.queuedSince, l.waiting, l.nodeFinal = time.Now(), "", nil
	d.event(l, map[string]any{"stage": "queued", "message": why + "，任务已重新排队"})
	d.poke()
}

// ---- cancelling ------------------------------------------------------------------------------------------

func (d *Dispatcher) Cancel(ctx context.Context, kind, id string) (tasks.Task, error) {
	d.mu.Lock()
	l := d.live[id]
	d.mu.Unlock()
	if l == nil {
		t, err := d.Get(ctx, kind, id)
		return t, err
	}
	l.mu.Lock()
	if l.task.Kind != kind {
		l.mu.Unlock()
		return tasks.Task{}, errNotFound
	}
	if l.done {
		defer l.mu.Unlock()
		return l.task, nil
	}
	node, nodeJob := l.task.Node, l.task.NodeJobID
	if node == "" {
		if l.dispatching {
			l.cancel = true
		}
		d.finishLocked(l, tasks.Cancelled, &tasks.ErrorDetail{Code: "JOB_CANCELLED", Message: "任务已取消"}, nil)
		defer l.mu.Unlock()
		return l.task, nil
	}
	d.event(l, map[string]any{"stage": "cancelling", "message": "正在停止执行节点上的任务"})
	l.mu.Unlock()
	status, _, err := d.call(ctx, http.MethodDelete, d.nodeURL(node, l.kind, "/jobs/"+nodeJob), nil, 10*time.Second)
	if err != nil || status == http.StatusNotFound {
		// The node is gone or forgot the job: nothing is running there any more.
		d.finish(l, tasks.Cancelled, &tasks.ErrorDetail{Code: "JOB_CANCELLED", Message: "任务已取消"}, nil)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.task, nil
}

// ---- reading ---------------------------------------------------------------------------------------------

var errNotFound = &HTTPError{http.StatusNotFound, "JOB_NOT_FOUND", "Job not found or expired"}

func (d *Dispatcher) Get(ctx context.Context, kind, id string) (tasks.Task, error) {
	d.mu.Lock()
	l := d.live[id]
	d.mu.Unlock()
	if l != nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.task.Kind == kind {
			return l.task, nil
		}
		return tasks.Task{}, errNotFound
	}
	t, err := d.store.GetTask(ctx, id)
	if errors.Is(err, tasks.ErrNotFound) || err == nil && t.Kind != kind {
		return tasks.Task{}, errNotFound
	}
	return t, err
}

// Update changes what the frontend keeps on a task: its meta and whether it has been dealt with.
func (d *Dispatcher) Update(ctx context.Context, kind, id string, meta json.RawMessage, handled *bool) (tasks.Task, error) {
	d.mu.Lock()
	l := d.live[id]
	d.mu.Unlock()
	apply := func(t *tasks.Task) {
		if meta != nil {
			t.Meta = meta
		}
		if handled != nil {
			t.Handled = *handled
		}
	}
	if l != nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.task.Kind != kind {
			return tasks.Task{}, errNotFound
		}
		apply(&l.task)
		return l.task, d.store.SaveTask(ctx, l.task)
	}
	d.mu.Lock() // finished tasks are written by nobody else but Update; serialise those
	defer d.mu.Unlock()
	t, err := d.store.GetTask(ctx, id)
	if errors.Is(err, tasks.ErrNotFound) || err == nil && t.Kind != kind {
		return tasks.Task{}, errNotFound
	}
	if err != nil {
		return tasks.Task{}, err
	}
	apply(&t)
	return t, d.store.SaveTask(ctx, t)
}

func (d *Dispatcher) List(ctx context.Context, f tasks.Filter) ([]tasks.Task, error) {
	known := f.Kinds[:0:0]
	for _, k := range f.Kinds {
		if d.kinds[k] != nil {
			known = append(known, k)
		}
	}
	if len(f.Kinds) > 0 && len(known) == 0 {
		return nil, nil
	}
	f.Kinds = known
	return d.store.ListTasks(ctx, f)
}

// Load is what the metrics endpoint reports per kind: tasks waiting for a node and tasks on one.
func (d *Dispatcher) Load() map[string][2]int {
	out := map[string][2]int{}
	for name := range d.kinds {
		out[name] = [2]int{}
	}
	for _, l := range d.snapshot() {
		l.mu.Lock()
		counts := out[l.task.Kind]
		if l.task.Node == "" {
			counts[0]++
		} else {
			counts[1]++
		}
		out[l.task.Kind] = counts
		l.mu.Unlock()
	}
	return out
}

func (d *Dispatcher) Kinds() []string {
	var out []string
	for name := range d.kinds {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (d *Dispatcher) Nodes(kind string) int {
	if k := d.kinds[kind]; k != nil {
		return len(k.Pool.Nodes(d.ctx))
	}
	return 0
}

func describe(k *Kind) string { return k.Name + "=" + k.Pool.Describe() }

func (d *Dispatcher) String() string {
	var parts []string
	for _, name := range d.Kinds() {
		parts = append(parts, describe(d.kinds[name]))
	}
	return strings.Join(parts, " ")
}

// ---- queued requests survive a restart ----------------------------------------------------------------------

// pendingRequest is the stored form of a request that is waiting for a node.
type pendingRequest struct {
	Body         map[string]json.RawMessage `json:"body"`
	Source       string                     `json:"source,omitempty"`
	Checkpoint   string                     `json:"checkpoint,omitempty"`
	LegacyNode   string                     `json:"legacyNode,omitempty"`
	LegacyNodeID string                     `json:"legacyNodeId,omitempty"`
}

// keepRequest stores a queued task's request so that a CaseHub restart puts the task back in the queue instead of
// failing it. The request holds the test account the user typed, so it is only stored while the task waits and is
// wiped once a node has it (dropRequest) or the task ends.
func (d *Dispatcher) keepRequest(ctx context.Context, l *live) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d.storeRequest(ctx, l)
}

func (d *Dispatcher) keepRequestLocked(l *live) { d.storeRequest(context.Background(), l) }

func (d *Dispatcher) storeRequest(ctx context.Context, l *live) {
	if l.req == nil {
		return
	}
	data, err := json.Marshal(pendingRequest{Body: l.req.body, Source: l.req.source, Checkpoint: l.req.checkpoint,
		LegacyNode: l.req.legacyNode, LegacyNodeID: l.req.legacyNodeID})
	if err == nil {
		err = d.store.PutBlob(ctx, l.task.ID, tasks.BlobRequest, data)
	}
	if err != nil {
		d.cfg.Logf("dispatch: keeping the request of task %s failed: %v", l.task.ID, err)
		return
	}
	l.requestKept = true
}

// dropRequest wipes the stored request once a node has the task.
func (d *Dispatcher) dropRequest(l *live) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d.dropRequestLocked(l)
}

func (d *Dispatcher) dropRequestLocked(l *live) {
	if !l.requestKept {
		return
	}
	l.requestKept = false
	if err := d.store.PutBlob(context.Background(), l.task.ID, tasks.BlobRequest, nil); err != nil {
		d.cfg.Logf("dispatch: wiping the stored request of task %s failed: %v", l.task.ID, err)
	}
}

// restoreRequest brings back a queued task's request after a restart; false when it was not kept.
func (d *Dispatcher) restoreRequest(ctx context.Context, l *live) bool {
	data, err := d.store.GetBlob(ctx, l.task.ID, tasks.BlobRequest)
	if err != nil || len(data) == 0 {
		return false
	}
	var p pendingRequest
	if json.Unmarshal(data, &p) != nil || p.Body == nil {
		return false
	}
	l.req = &request{body: p.Body, affinity: affinityKey(p.Body), source: p.Source, checkpoint: p.Checkpoint,
		legacyNode: p.LegacyNode, legacyNodeID: p.LegacyNodeID}
	l.requestKept = true
	l.task.Status = tasks.Queued
	l.waiting = ""
	return true
}

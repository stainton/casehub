package dispatch

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"casehub/internal/tasks"
)

// follow copies a task's progress from the node running it until the task ends there (or the node is lost).
func (d *Dispatcher) follow(l *live) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.run(l)
	}()
}

func (d *Dispatcher) run(l *live) {
	var unreachableSince time.Time
	backoff := time.Second
	for d.ctx.Err() == nil {
		l.mu.Lock()
		if l.done || l.task.Node == "" {
			l.mu.Unlock()
			return
		}
		node, jobID, cursor := l.task.Node, l.task.NodeJobID, l.task.NodeEventID
		l.mu.Unlock()
		if job := d.stream(l, node, jobID, cursor); job != nil {
			d.complete(l, node, jobID, *job)
			return
		}
		if d.ctx.Err() != nil {
			return
		}
		// The stream ended without the task ending: ask the node directly.
		status, body, err := d.call(d.ctx, http.MethodGet, d.nodeURL(node, l.kind, "/jobs/"+jobID), nil, 10*time.Second)
		switch {
		case err == nil && status == http.StatusOK:
			unreachableSince, backoff = time.Time{}, time.Second
			var job nodeJob
			if json.Unmarshal(body, &job) == nil && tasks.Terminal(job.Status) {
				d.complete(l, node, jobID, job)
				return
			}
		case err == nil && status == http.StatusNotFound:
			d.lost(l, "执行节点已不再持有该任务（节点可能已重启）")
			return
		default:
			if unreachableSince.IsZero() {
				unreachableSince = time.Now()
			}
			if time.Since(unreachableSince) > d.cfg.NodeLostAfter {
				d.lost(l, "执行节点长时间无法联系")
				return
			}
		}
		select {
		case <-d.ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

// stream reads the node's progress events from cursor on. It returns the node's job once the task has ended
// there, or nil when the stream broke off first. A stream silent for longer than the node's heartbeat is
// treated as broken.
func (d *Dispatcher) stream(l *live, node, jobID string, cursor int64) *nodeJob {
	ctx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	watchdog := time.AfterFunc(45*time.Second, cancel)
	defer watchdog.Stop()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, d.nodeURL(node, l.kind, "/jobs/"+jobID+"/events"), nil)
	if err != nil {
		return nil
	}
	if cursor > 0 {
		r.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
	}
	resp, err := d.cfg.Client.Do(r)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	var kind string
	var data strings.Builder
	for scanner.Scan() {
		watchdog.Reset(45 * time.Second)
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "":
			payload := data.String()
			data.Reset()
			if job, ended := d.copyFrame(l, node, jobID, kind, payload); ended {
				return job
			}
			kind = ""
		}
	}
	return nil
}

// copyFrame applies one frame from the node. ended reports that the task is over there; job is then the
// node's final summary (nil if it could not be read, and the caller asks again).
func (d *Dispatcher) copyFrame(l *live, node, jobID, kind, payload string) (*nodeJob, bool) {
	switch kind {
	case "snapshot":
		var job nodeJob
		if json.Unmarshal([]byte(payload), &job) != nil {
			return nil, false
		}
		l.mu.Lock()
		d.noteRunning(l, job.Status, job.TimeoutMs)
		l.mu.Unlock()
		if tasks.Terminal(job.Status) {
			return &job, true
		}
	case "progress":
		var fields map[string]any
		if json.Unmarshal([]byte(payload), &fields) != nil {
			return nil, false
		}
		id, _ := fields["id"].(float64)
		status, _ := fields["status"].(string)
		l.mu.Lock()
		if l.done {
			l.mu.Unlock()
			return nil, true
		}
		l.task.NodeEventID = int64(id)
		if tasks.Terminal(status) {
			// CaseHub announces the end itself once it has collected the result: the node's last event is kept
			// for its workflow-specific fields.
			l.nodeFinal = json.RawMessage(payload)
			l.mu.Unlock()
			_, body, err := d.call(d.ctx, http.MethodGet, d.nodeURL(node, l.kind, "/jobs/"+jobID), nil, 10*time.Second)
			var job nodeJob
			if err != nil || json.Unmarshal(body, &job) != nil || !tasks.Terminal(job.Status) {
				return nil, true
			}
			return &job, true
		}
		d.noteRunning(l, status, 0)
		for _, key := range []string{"id", "jobId", "status", "createdAt"} {
			delete(fields, key)
		}
		d.event(l, fields)
		l.mu.Unlock()
	}
	return nil, false
}

func (d *Dispatcher) noteRunning(l *live, status string, timeoutMs int64) {
	t := &l.task
	if timeoutMs > 0 {
		t.TimeoutMs = timeoutMs
	}
	if status == tasks.Running && t.Status == tasks.Queued {
		t.Status = tasks.Running
	}
	if status == tasks.Running && t.StartedAt == nil {
		now := time.Now().UTC()
		t.StartedAt = &now
	}
}

// complete collects what a finished node job leaves behind, then releases it on the node.
func (d *Dispatcher) complete(l *live, node, jobID string, job nodeJob) {
	base := d.nodeURL(node, l.kind, "/jobs/"+jobID)
	release := true
	defer func() {
		if release {
			_, _, _ = d.call(d.ctx, http.MethodPost, base+"/release", nil, 10*time.Second)
		}
	}()
	l.mu.Lock()
	final := l.nodeFinal
	l.mu.Unlock()
	switch job.Status {
	case tasks.Succeeded:
		var result []byte
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			var status int
			if status, result, err = d.call(d.ctx, http.MethodGet, base+"/result", nil, 2*time.Minute); err == nil && status == http.StatusOK {
				break
			}
			if err == nil {
				err = errors.New("HTTP " + strconv.Itoa(status))
			}
			time.Sleep(2 * time.Second)
		}
		if err != nil {
			release = false // the node keeps it until its own retention ends; someone may still fetch it there
			d.finish(l, tasks.Failed, &tasks.ErrorDetail{Code: "RESULT_UNAVAILABLE", Message: "任务已完成，但无法从执行节点取回结果：" + err.Error()}, nil)
			return
		}
		l.mu.Lock()
		l.task.Result = result
		d.finishLocked(l, tasks.Succeeded, nil, final)
		l.mu.Unlock()
	case tasks.Failed:
		code := ""
		if job.Error != nil {
			code = job.Error.Code
		}
		if code == "NODE_SHUTDOWN" && (job.StartedAt == "" || !(l.kind.Continuable && job.Continuable)) {
			// The node stopped (scale-in, redeploy) before it finished: run the task again elsewhere. A planner
			// session it kept stays with the person instead, who continues it below.
			l.mu.Lock()
			d.requeueLocked(l, "执行节点停机")
			l.mu.Unlock()
			return
		}
		var checkpoint string
		legacy := false
		if l.kind.Continuable && job.Continuable {
			status, body, err := d.call(d.ctx, http.MethodGet, base+"/checkpoint", nil, 5*time.Minute)
			switch {
			case err == nil && status == http.StatusOK:
				sum := sha256.Sum256(body)
				checkpoint = hex.EncodeToString(sum[:])
				if err := d.store.PutBlob(d.ctx, l.task.ID, tasks.BlobCheckpoint, body); err != nil {
					d.cfg.Logf("dispatch: saving the session of task %s failed: %v", l.task.ID, err)
					checkpoint = ""
				}
			case err == nil && status == http.StatusNotFound && nodeRejection(status, body).Code == "NOT_FOUND":
				// A node from before checkpoints: the session can only be continued on that node.
				legacy, release = true, false
			}
		}
		diagnostics := false
		if job.Diagnostics {
			if status, body, err := d.call(d.ctx, http.MethodGet, base+"/diagnostics", nil, 5*time.Minute); err == nil && status == http.StatusOK {
				diagnostics = d.store.PutBlob(d.ctx, l.task.ID, tasks.BlobDiagnostics, body) == nil
			}
		}
		detail := job.Error
		if detail == nil {
			detail = &tasks.ErrorDetail{Code: strings.ToUpper(l.kind.Name) + "_FAILED", Message: "任务执行失败"}
		}
		l.mu.Lock()
		l.task.CheckpointSHA, l.task.LegacyContinue, l.task.Diagnostics = checkpoint, legacy, diagnostics
		d.finishLocked(l, tasks.Failed, detail, final)
		l.mu.Unlock()
	default:
		d.finish(l, tasks.Cancelled, &tasks.ErrorDetail{Code: "JOB_CANCELLED", Message: "任务已取消"}, final)
	}
}

// lost handles a node that no longer has the task (restarted, deleted, unreachable for too long).
func (d *Dispatcher) lost(l *live, why string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.done:
	case l.task.StartedAt == nil:
		d.requeueLocked(l, why)
	case l.kind.Continuable:
		d.finishLocked(l, tasks.Failed, &tasks.ErrorDetail{Code: "NODE_LOST",
			Message: why + "，中断前的探索会话没能保存下来，请重新开始"}, nil)
	default:
		d.requeueLocked(l, why)
	}
}

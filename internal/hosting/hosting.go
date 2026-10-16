// Package hosting runs AI 托管 on the server: once a person hands a requirement over, CaseHub itself estimates
// it, submits the design task, files the result into the review area, passes it and moves it into the test
// version's case tree — no browser has to stay open. Each step is one core action, so a step either happens
// whole or not at all; the run picks up at its recorded stage after a CaseHub restart.
package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"casehub/internal/core"
	"casehub/internal/tasks"
)

// Planner is what a hosting needs from the planner service: the synchronous estimate and the task queue.
type Planner interface {
	Estimate(ctx context.Context, body []byte) ([]byte, error)
	Submit(ctx context.Context, body []byte) (tasks.Task, error)
	Get(ctx context.Context, id string) (tasks.Task, error)
	Cancel(ctx context.Context, id string) error
	MarkHandled(ctx context.Context, id string)
}

// Request is what the frontend sends to start (or redo the design of) a hosting. Estimate and Job are the
// planner's own request bodies, composed by the frontend exactly as for a manual design (requirements,
// referenced-requirement context, exploration notes, assets, test account, time limit); the runner fills in
// the estimated caseCount and the requirement code, and its own casehubTask. They live in memory only: the
// test account they carry is never stored.
type Request struct {
	Estimate json.RawMessage `json:"estimate"`
	Job      json.RawMessage `json:"job"`
	// Continue (on a retry of a failed design) continues the failed task's session instead of starting over.
	Continue bool `json:"continue"`
}

type Runner struct {
	svc     *core.Service
	planner Planner
	Poll    time.Duration
	Logf    func(string, ...any)

	mu      sync.Mutex
	running map[string]bool
	ctx     context.Context
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

func New(svc *core.Service, planner Planner) *Runner {
	ctx, stop := context.WithCancel(context.Background())
	return &Runner{svc: svc, planner: planner, Poll: 3 * time.Second, Logf: log.Printf, running: map[string]bool{}, ctx: ctx, stop: stop}
}

// Start resumes every unfinished hosting. One still estimating, or designing without a submitted task, lost
// its request with the old process and stops; everything later needs no request and carries on.
func (r *Runner) Start(ctx context.Context) error {
	st, err := r.svc.State(ctx)
	if err != nil {
		return err
	}
	for _, h := range st.Hostings {
		if core.HostingActive(h) {
			r.launch(h.ID, nil)
		}
	}
	return nil
}

func (r *Runner) Close() {
	r.stop()
	r.wg.Wait()
}

// Begin records a new hosting for a requirement and starts running it.
func (r *Runner) Begin(ctx context.Context, h core.Hosting, req Request, author string) (core.Hosting, error) {
	if len(req.Estimate) == 0 || len(req.Job) == 0 {
		return core.Hosting{}, errors.New("缺少评估或设计请求")
	}
	out, err := r.svc.Apply(ctx, core.Action{Type: "startHosting", Hosting: &h, Author: author})
	if err != nil {
		return core.Hosting{}, err
	}
	created := findDoc(out.State, h.DocID)
	r.launch(created.ID, &req)
	return created, nil
}

// Retry restarts a stopped hosting at the step it stopped in. A stop in estimating or designing needs req.
func (r *Runner) Retry(ctx context.Context, id string, req *Request) (core.Hosting, error) {
	h, err := r.hosting(ctx, id)
	if err != nil {
		return core.Hosting{}, err
	}
	// Redoing the estimate needs both requests; redoing (or continuing) the design keeps the estimate.
	needEstimate := h.FailedAt == core.HostingEstimating || h.FailedAt == ""
	if (needEstimate || h.FailedAt == core.HostingDesigning) && (req == nil || len(req.Job) == 0 || needEstimate && len(req.Estimate) == 0) {
		return core.Hosting{}, errors.New("重新设计需要重新填写托管参数")
	}
	if req != nil && req.Continue && (h.FailedAt != core.HostingDesigning || !h.Continuable) {
		return core.Hosting{}, errors.New("这次设计已经无法继续，请重新托管")
	}
	failedTask := h.PlannerTaskID
	if _, err := r.svc.Apply(ctx, core.Action{Type: "hostingRetry", HostingID: id, Author: core.HostingAuthor}); err != nil {
		return core.Hosting{}, err
	}
	if req != nil && req.Continue {
		req.Job = withField(req.Job, "continueFrom", failedTask)
	}
	r.launch(id, req)
	return r.hosting(ctx, id)
}

// Abandon drops a hosting, cancelling its design task first when one is running. Whatever it already put in
// the review area stays there for a person to handle.
func (r *Runner) Abandon(ctx context.Context, id string) error {
	h, err := r.hosting(ctx, id)
	if err != nil {
		return err
	}
	if h.PlannerTaskID != "" {
		if t, err := r.planner.Get(ctx, h.PlannerTaskID); err == nil && !tasks.Terminal(t.Status) {
			_ = r.planner.Cancel(ctx, h.PlannerTaskID)
		}
		r.planner.MarkHandled(ctx, h.PlannerTaskID)
	}
	_, err = r.svc.Apply(ctx, core.Action{Type: "deleteHosting", HostingID: id, Force: true})
	return err
}

func (r *Runner) launch(id string, req *Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[id] {
		return
	}
	r.running[id] = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() { r.mu.Lock(); delete(r.running, id); r.mu.Unlock() }()
		r.run(id, req)
	}()
}

func findDoc(st core.State, docID string) core.Hosting {
	for _, h := range st.Hostings {
		if h.DocID == docID {
			return h
		}
	}
	return core.Hosting{}
}

func (r *Runner) hosting(ctx context.Context, id string) (core.Hosting, error) {
	st, err := r.svc.State(ctx)
	if err != nil {
		return core.Hosting{}, err
	}
	for _, h := range st.Hostings {
		if h.ID == id {
			return h, nil
		}
	}
	return core.Hosting{}, errNotFound
}

var errNotFound = errors.New("托管不存在")

// ErrTaskGone is what Planner.Get returns for a task the queue no longer has.
var ErrTaskGone = errors.New("task gone")

func (r *Runner) apply(a core.Action) error {
	if a.Author == "" {
		a.Author = core.HostingAuthor
	}
	_, err := r.svc.Apply(r.ctx, a)
	return err
}

func (r *Runner) halt(id, failedAt, msg string, continuable bool) {
	if err := r.apply(core.Action{Type: "hostingStop", HostingID: id, Note: msg, Hosting: &core.Hosting{FailedAt: failedAt, Continuable: continuable}}); err != nil {
		r.Logf("hosting %s: stopping failed: %v", id, err)
	}
}

// run advances one hosting until it is done, stops, or CaseHub shuts down.
func (r *Runner) run(id string, req *Request) {
	for r.ctx.Err() == nil {
		h, err := r.hosting(r.ctx, id)
		if err != nil {
			return // abandoned
		}
		switch h.Stage {
		case core.HostingEstimating:
			if req == nil {
				r.halt(id, h.Stage, "CaseHub 重启时托管还在评估，请重新托管", false)
				return
			}
			if !r.estimate(h, req) {
				return
			}
		case core.HostingDesigning:
			if h.PlannerTaskID == "" {
				if req == nil {
					r.halt(id, h.Stage, "CaseHub 重启时设计任务还没提交，请重新托管", false)
					return
				}
				if !r.submit(h, req) {
					return
				}
				req = nil // the design task holds it now; nothing later needs the credentials
				continue
			}
			if !r.await(h) {
				return
			}
		case core.HostingReviewing:
			if err := r.apply(core.Action{Type: "hostingReview", HostingID: id}); err != nil {
				r.halt(id, h.Stage, err.Error(), false)
				return
			}
		case core.HostingArchiving:
			if err := r.apply(core.Action{Type: "hostingArchive", HostingID: id}); err != nil {
				r.halt(id, h.Stage, err.Error(), false)
				return
			}
			r.planner.MarkHandled(r.ctx, h.PlannerTaskID)
		default:
			return
		}
	}
}

// estimate asks the planner how many cases the requirement needs and adopts its answer; a failed estimate
// falls back to the default count, like the manual form.
func (r *Runner) estimate(h core.Hosting, req *Request) bool {
	const defaultCount = 10
	count, code, rationale, note := defaultCount, "", "", ""
	ctx, cancel := context.WithTimeout(r.ctx, 135*time.Second)
	body, err := r.planner.Estimate(ctx, req.Estimate)
	cancel()
	var out struct {
		SuggestedCaseCount float64 `json:"suggestedCaseCount"`
		Rationale          string  `json:"rationale"`
		RequirementCodes   []struct {
			Requirement, Code string
		} `json:"requirementCodes"`
	}
	if err == nil {
		err = json.Unmarshal(body, &out)
	}
	if err != nil {
		note = fmt.Sprintf("评估失败（%v），按默认 %d 条继续。", err, defaultCount)
	} else {
		if n := int(out.SuggestedCaseCount + 0.5); n >= 1 {
			count = min(n, 500)
		}
		rationale = out.Rationale
		for _, c := range out.RequirementCodes {
			if c.Requirement == h.DocID {
				code = strings.ToUpper(strings.TrimSpace(c.Code))
			}
		}
	}
	if err := r.apply(core.Action{Type: "hostingDesigning", HostingID: h.ID, Hosting: &core.Hosting{CaseCount: count, Code: code, Rationale: rationale, Note: note}}); err != nil {
		r.halt(h.ID, core.HostingEstimating, err.Error(), false)
		return false
	}
	return true
}

func (r *Runner) submit(h core.Hosting, req *Request) bool {
	body := req.Job
	if !req.Continue {
		body = withField(body, "caseCount", h.CaseCount)
	}
	body = withCode(body, h.DocID, h.Code)
	body = withField(body, "casehubTask", map[string]any{"subject": "hosting:" + h.DocID, "meta": map[string]string{"hosting": h.ID}})
	t, err := r.planner.Submit(r.ctx, body)
	if err != nil {
		r.halt(h.ID, core.HostingDesigning, "提交设计任务失败："+err.Error(), false)
		return false
	}
	if err := r.apply(core.Action{Type: "hostingSubmitted", HostingID: h.ID, TaskID: t.ID}); err != nil {
		_ = r.planner.Cancel(r.ctx, t.ID)
		r.halt(h.ID, core.HostingDesigning, err.Error(), false)
		return false
	}
	return true
}

// await waits for the design task and files its result; false when the hosting stopped or CaseHub is closing.
func (r *Runner) await(h core.Hosting) bool {
	for {
		t, err := r.planner.Get(r.ctx, h.PlannerTaskID)
		switch {
		case r.ctx.Err() != nil:
			return false
		case errors.Is(err, ErrTaskGone):
			r.halt(h.ID, core.HostingDesigning, "这次托管的设计任务已不存在", false)
			return false
		case err == nil && t.Status == tasks.Succeeded:
			var result core.PlannerResult
			if err := json.Unmarshal(t.Result, &result); err != nil {
				r.halt(h.ID, core.HostingImporting, "读取设计结果失败："+err.Error(), false)
				return false
			}
			if err := r.apply(core.Action{Type: "hostingImport", HostingID: h.ID, Planner: &result}); err != nil {
				r.halt(h.ID, core.HostingImporting, err.Error(), false)
				return false
			}
			return true
		case err == nil && tasks.Terminal(t.Status):
			msg := "设计任务已取消"
			if t.Error != nil {
				msg = t.Error.Code + "：" + t.Error.Message
			}
			r.halt(h.ID, core.HostingDesigning, msg, t.Continuable())
			return false
		}
		// Still running, or the task store hiccupped: look again shortly.
		select {
		case <-r.ctx.Done():
			return false
		case <-time.After(r.Poll):
		}
	}
}

// withField sets one top-level field of a JSON object body.
func withField(body json.RawMessage, key string, value any) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil || m == nil {
		m = map[string]json.RawMessage{}
	}
	v, _ := json.Marshal(value)
	m[key] = v
	out, _ := json.Marshal(m)
	return out
}

// withCode gives the hosted requirement its code in the design request (it may only just have been set).
func withCode(body json.RawMessage, docID, code string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	var reqs []map[string]any
	if json.Unmarshal(m["requirements"], &reqs) != nil {
		return body
	}
	for _, q := range reqs {
		if q["id"] == docID {
			q["code"] = code
		}
	}
	return withField(body, "requirements", reqs)
}

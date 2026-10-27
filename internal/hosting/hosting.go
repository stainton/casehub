// Package hosting runs AI 托管 on the server: once a person hands a requirement over, CaseHub itself estimates
// it, submits the design task, files the result into the review area, passes it and moves it into the test
// version's case tree, then (when asked) generates a script per case and runs each script once, recording the
// results — no browser has to stay open. Cases handed over from 用例管理 skip the design part and start at the
// script generation. Each step is one core action, so a step either happens whole or not
// at all; the run picks up at its recorded stage after a CaseHub restart.
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

// Services is what a hosting needs from the auto-test services: the planner's synchronous estimate, and the
// task queue (kinds planner, generator, executor).
type Services interface {
	Estimate(ctx context.Context, body []byte) ([]byte, error)
	Enabled(kind string) bool
	Submit(ctx context.Context, kind string, body []byte) (tasks.Task, error)
	Get(ctx context.Context, kind, id string) (tasks.Task, error)
	Cancel(ctx context.Context, kind, id string) error
	MarkHandled(ctx context.Context, kind, id string)
}

// Request is what the frontend sends to start (or redo part of) a hosting. Estimate, Job and Generate are the
// services' own request bodies, composed by the frontend exactly as for a manual design / generation
// (requirements, referenced-requirement context, exploration notes, assets, test account, time limits); the
// runner fills in the estimated caseCount, the requirement code, the cases to script and its own casehubTask.
// They live in memory only: the test account they carry is never stored.
type Request struct {
	Estimate json.RawMessage `json:"estimate"`
	Job      json.RawMessage `json:"job"`
	// Generate is the generator request without its cases (target, context, caseTimeoutMs).
	Generate json.RawMessage `json:"generate"`
	// Continue (on a retry of a failed design) continues the failed task's session instead of starting over.
	Continue bool `json:"continue"`
}

type Runner struct {
	svc  *core.Service
	srv  Services
	Poll time.Duration
	Logf func(string, ...any)

	mu      sync.Mutex
	running map[string]*slot
	// reqs holds each hosting's latest request until the last task that needs it (the generation, or the
	// design when no scripts are wanted) has it — across stops and retries, never on disk.
	reqs map[string]*Request
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
}

func New(svc *core.Service, srv Services) *Runner {
	ctx, stop := context.WithCancel(context.Background())
	return &Runner{svc: svc, srv: srv, Poll: 3 * time.Second, Logf: log.Printf, running: map[string]*slot{}, reqs: map[string]*Request{}, ctx: ctx, stop: stop}
}

// Start resumes every unfinished hosting. A stage that still needs the request (estimating, submitting the
// design or the generation) lost it with the old process and stops; everything else carries on.
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

// Begin records a new hosting for a requirement (or for cases in a test version) and starts running it.
func (r *Runner) Begin(ctx context.Context, h core.Hosting, req Request, author string) (core.Hosting, error) {
	fromCases := h.Source == core.HostingFromCases
	if !fromCases && !r.srv.Enabled("planner") {
		return core.Hosting{}, errors.New("AI 设计服务（planner）未配置，不能托管需求")
	}
	if !fromCases && (len(req.Estimate) == 0 || len(req.Job) == 0) {
		return core.Hosting{}, errors.New("缺少评估或设计请求")
	}
	h.Generate = h.Generate || h.Execute || fromCases
	if h.Generate && !r.srv.Enabled("generator") {
		return core.Hosting{}, errors.New("脚本生成服务（generator）未配置，不能托管脚本生成")
	}
	if h.Execute && !r.srv.Enabled("executor") {
		return core.Hosting{}, errors.New("自动化执行服务（executor）未配置，不能托管自动化执行")
	}
	if h.Generate && len(req.Generate) == 0 {
		return core.Hosting{}, errors.New("缺少脚本生成请求")
	}
	out, err := r.svc.Apply(ctx, core.Action{Type: "startHosting", Hosting: &h, Author: author})
	if err != nil {
		return core.Hosting{}, err
	}
	created := out.State.Hostings[len(out.State.Hostings)-1] // startHosting appends the new one
	r.launch(created.ID, &req)
	return created, nil
}

// Retry restarts a stopped hosting at the step it stopped in. What it needs again depends on that step:
// redoing the estimate or the design needs the planner requests, submitting a new generation the generator
// request (they carry the test account, which is never stored); re-filing a result or re-running scripts
// needs nothing.
func (r *Runner) Retry(ctx context.Context, id string, req *Request) (core.Hosting, error) {
	h, err := r.hosting(ctx, id)
	if err != nil {
		return core.Hosting{}, err
	}
	if h.Stage != core.HostingStopped {
		return core.Hosting{}, errors.New("只有已停止的托管可以重试")
	}
	needEstimate := h.Source != core.HostingFromCases && (h.FailedAt == core.HostingEstimating || h.FailedAt == "")
	if (needEstimate || h.FailedAt == core.HostingDesigning) && (req == nil || len(req.Job) == 0 || needEstimate && len(req.Estimate) == 0) {
		return core.Hosting{}, errors.New("重新设计需要重新填写托管参数")
	}
	if req != nil && req.Continue && (h.FailedAt != core.HostingDesigning || !h.Continuable) {
		return core.Hosting{}, errors.New("这次设计已经无法继续，请重新托管")
	}
	// A finished generator run is only saved again; any other generation stop submits a new one.
	resubmitGeneration := false
	continueGeneration := false
	if h.FailedAt == core.HostingGenerating || h.Source == core.HostingFromCases && h.FailedAt == "" {
		t, err := r.srv.Get(ctx, "generator", h.GenTaskID)
		resubmitGeneration = h.GenTaskID == "" || err != nil || t.Status != tasks.Succeeded
		continueGeneration = err == nil && t.Continuable() && (h.GenCaseID != "" || len(h.CaseIDs) == 1)
		if resubmitGeneration && (req == nil || len(req.Generate) == 0) {
			return core.Hosting{}, errors.New("重新生成脚本需要重新填写托管参数")
		}
	}
	failedTask := h.PlannerTaskID
	if _, err := r.svc.Apply(ctx, core.Action{Type: "hostingRetry", HostingID: id, Force: resubmitGeneration, Author: core.HostingAuthor}); err != nil {
		return core.Hosting{}, err
	}
	if req != nil && req.Continue {
		req.Job = withField(req.Job, "continueFrom", failedTask)
	}
	if continueGeneration {
		req.Generate = withField(req.Generate, "continueFrom", h.GenTaskID)
	}
	r.launch(id, req)
	return r.hosting(ctx, id)
}

// Abandon drops a hosting, cancelling whatever task of it is still running. What it already filed (review
// area, case tree, scripts, records) stays for a person to handle.
func (r *Runner) Abandon(ctx context.Context, id string) error {
	h, err := r.hosting(ctx, id)
	if err != nil {
		return err
	}
	drop := func(kind, taskID string) {
		if taskID == "" {
			return
		}
		if t, err := r.srv.Get(ctx, kind, taskID); err == nil && !tasks.Terminal(t.Status) {
			_ = r.srv.Cancel(ctx, kind, taskID)
		}
		r.srv.MarkHandled(ctx, kind, taskID)
	}
	drop("planner", h.PlannerTaskID)
	drop("generator", h.GenTaskID)
	for _, taskID := range h.ExecTasks {
		drop("executor", taskID)
	}
	r.forget(id, true)
	_, err = r.svc.Apply(ctx, core.Action{Type: "deleteHosting", HostingID: id, Force: true})
	return err
}

// slot is a hosting's goroutine. A launch while it still runs (a retry racing its winding down) is handed to
// it: it runs the hosting once more instead of the launch being lost.
type slot struct{ again bool }

// launch runs a hosting; req, when given, is merged over the request kept for it (a retry may bring only
// the part it needs, e.g. just the generator request).
func (r *Runner) launch(id string, req *Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req != nil {
		kept := r.reqs[id]
		if kept == nil {
			kept = &Request{}
		}
		merged := *kept
		if len(req.Estimate) > 0 {
			merged.Estimate = req.Estimate
		}
		if len(req.Job) > 0 {
			merged.Job = req.Job
		}
		if len(req.Generate) > 0 {
			merged.Generate = req.Generate
		}
		merged.Continue = req.Continue
		r.reqs[id] = &merged
	}
	if s := r.running[id]; s != nil {
		s.again = true
		return
	}
	s := &slot{}
	r.running[id] = s
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			r.run(id)
			r.mu.Lock()
			if !s.again || r.ctx.Err() != nil {
				delete(r.running, id)
				r.mu.Unlock()
				return
			}
			s.again = false
			r.mu.Unlock()
		}
	}()
}

func (r *Runner) request(id string) *Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs[id]
}

// forget drops what the request holds that is no longer needed: the planner parts once the design is
// submitted, everything after the last generation is submitted (or when no generation will follow).
func (r *Runner) forget(id string, all bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q := r.reqs[id]; q != nil {
		if all || len(q.Generate) == 0 {
			delete(r.reqs, id)
			return
		}
		q.Estimate, q.Job, q.Continue = nil, nil, false
	}
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

// ErrTaskGone is what Services.Get returns for a task the queue no longer has.
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

func (r *Runner) wait() bool {
	select {
	case <-r.ctx.Done():
		return false
	case <-time.After(r.Poll):
		return true
	}
}

func (r *Runner) subject(h core.Hosting) map[string]any {
	// Not req:<id>: the manual design / generation views must not pick a hosting's tasks up and import them again.
	key := h.DocID
	if key == "" {
		key = h.ID
	}
	return map[string]any{"subject": "hosting:" + key, "meta": map[string]string{"hosting": h.ID}}
}

// run advances one hosting until it is done, stops, or CaseHub shuts down.
func (r *Runner) run(id string) {
	for r.ctx.Err() == nil {
		h, err := r.hosting(r.ctx, id)
		if err != nil {
			r.forget(id, true)
			return // abandoned
		}
		req := r.request(id)
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
				if req == nil || len(req.Job) == 0 {
					r.halt(id, h.Stage, "CaseHub 重启时设计任务还没提交，请重新托管", false)
					return
				}
				if !r.submitDesign(h, req) {
					return
				}
				r.forget(id, !h.Generate)
				continue
			}
			if !r.awaitDesign(h) {
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
			r.srv.MarkHandled(r.ctx, "planner", h.PlannerTaskID)
		case core.HostingGenerating:
			if h.GenTaskID == "" {
				if req == nil || len(req.Generate) == 0 {
					r.halt(id, h.Stage, "CaseHub 重启后需要重新填写测试账号才能生成脚本，请点「重新填写并生成」", false)
					return
				}
				if !r.submitGeneration(h, req) {
					return
				}
				r.forget(id, !h.SaveScriptsIndividually) // individual mode needs the request for later cases
				continue
			}
			if !r.awaitGeneration(h) {
				return
			}
		case core.HostingRunning:
			if !r.runScripts(h) {
				return
			}
		default:
			if h.Stage == core.HostingDone {
				r.forget(id, true)
			}
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
	body, err := r.srv.Estimate(ctx, req.Estimate)
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

func (r *Runner) submitDesign(h core.Hosting, req *Request) bool {
	body := req.Job
	if !req.Continue {
		body = withField(body, "caseCount", h.CaseCount)
	}
	body = withCode(body, h.DocID, h.Code)
	body = withField(body, "casehubTask", r.subject(h))
	t, err := r.srv.Submit(r.ctx, "planner", body)
	if err != nil {
		r.halt(h.ID, core.HostingDesigning, "提交设计任务失败："+err.Error(), false)
		return false
	}
	if err := r.apply(core.Action{Type: "hostingSubmitted", HostingID: h.ID, TaskID: t.ID}); err != nil {
		_ = r.srv.Cancel(r.ctx, "planner", t.ID)
		r.halt(h.ID, core.HostingDesigning, err.Error(), false)
		return false
	}
	return true
}

// await polls one task until it ends. It returns the task, or false when the hosting stopped (gone) or
// CaseHub is closing.
func (r *Runner) await(h core.Hosting, kind, taskID, stage, what string) (tasks.Task, bool) {
	for {
		t, err := r.srv.Get(r.ctx, kind, taskID)
		switch {
		case r.ctx.Err() != nil:
			return t, false
		case errors.Is(err, ErrTaskGone):
			r.halt(h.ID, stage, "这次托管的"+what+"任务已不存在", false)
			return t, false
		case err == nil && tasks.Terminal(t.Status):
			return t, true
		}
		// Still running, or the task store hiccupped: look again shortly.
		if !r.wait() {
			return t, false
		}
	}
}

func failure(t tasks.Task, what string) string {
	if t.Error != nil {
		return t.Error.Code + "：" + t.Error.Message
	}
	return what + "任务已取消"
}

// awaitDesign waits for the design task and files its result.
func (r *Runner) awaitDesign(h core.Hosting) bool {
	t, ok := r.await(h, "planner", h.PlannerTaskID, core.HostingDesigning, "设计")
	if !ok {
		return false
	}
	if t.Status != tasks.Succeeded {
		r.halt(h.ID, core.HostingDesigning, failure(t, "设计"), t.Continuable())
		return false
	}
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
}

// submitGeneration asks the generator for one script per hosted case, sending the cases as they now are in
// the test version together with their requirements (and exploration notes) — what the automation page sends.
func (r *Runner) submitGeneration(h core.Hosting, req *Request) bool {
	st, err := r.svc.State(r.ctx)
	if err != nil {
		return r.wait() // try again
	}
	var cases []map[string]any
	var requirements []any
	seen := map[string]bool{}
	for _, id := range h.CaseIDs {
		if h.SaveScriptsIndividually && h.ScriptJobs[id] != "" {
			continue
		}
		for _, c := range st.Cases {
			if c.VersionID != h.VersionID || c.ID != id || strings.TrimSpace(c.Steps) == "" {
				continue
			}
			item := map[string]any{"id": c.ID, "title": c.Title, "priority": c.Priority, "precondition": c.Preconditions, "steps": c.Steps, "expects": c.Expected}
			if doc := core.HostingDoc(&st, h, c.ID); doc != nil {
				item["requirement"] = doc.ID
				if !seen[doc.ID] {
					seen[doc.ID] = true
					requirement := map[string]any{"id": doc.ID, "title": doc.Title, "content": doc.Content}
					if strings.TrimSpace(doc.ExplorationNotes) != "" {
						requirement["explorationNotes"] = doc.ExplorationNotes
					}
					requirements = append(requirements, requirement)
				}
			}
			cases = append(cases, item)
			if h.SaveScriptsIndividually {
				break
			}
		}
		if h.SaveScriptsIndividually && len(cases) > 0 {
			break
		}
	}
	if len(cases) == 0 {
		r.halt(h.ID, core.HostingGenerating, "托管的用例都没有执行步骤（或已被删除），无法生成脚本", false)
		return false
	}
	body := withField(req.Generate, "cases", cases)
	if len(requirements) > 0 {
		body = withField(body, "requirements", requirements)
	}
	body = withField(body, "casehubTask", r.subject(h))
	t, err := r.srv.Submit(r.ctx, "generator", body)
	if err != nil {
		r.halt(h.ID, core.HostingGenerating, "提交脚本生成任务失败："+err.Error(), false)
		return false
	}
	caseID := ""
	if h.SaveScriptsIndividually {
		caseID = cases[0]["id"].(string)
	}
	if err := r.apply(core.Action{Type: "hostingGenSubmitted", HostingID: h.ID, TaskID: t.ID, CaseID: caseID}); err != nil {
		_ = r.srv.Cancel(r.ctx, "generator", t.ID)
		r.halt(h.ID, core.HostingGenerating, err.Error(), false)
		return false
	}
	return true
}

func (r *Runner) awaitGeneration(h core.Hosting) bool {
	t, ok := r.await(h, "generator", h.GenTaskID, core.HostingGenerating, "脚本生成")
	if !ok {
		return false
	}
	if t.Status != tasks.Succeeded {
		r.halt(h.ID, core.HostingGenerating, failure(t, "脚本生成"), false)
		return false
	}
	var result core.GeneratorResult
	if err := json.Unmarshal(t.Result, &result); err != nil {
		r.halt(h.ID, core.HostingGenerating, "读取脚本生成结果失败："+err.Error(), false)
		return false
	}
	if err := r.apply(core.Action{Type: "hostingScripts", HostingID: h.ID, Generator: &result}); err != nil {
		r.halt(h.ID, core.HostingGenerating, err.Error(), false)
		return false
	}
	r.srv.MarkHandled(r.ctx, "generator", h.GenTaskID)
	return true
}

// runScripts submits one executor task per generated script that has none yet, then records each run as it
// ends: a test record with the run's Markdown report (steps and screenshots) for a pass or fail, or an error
// note when the run itself failed. It returns once every script has an outcome.
func (r *Runner) runScripts(h core.Hosting) bool {
	st, err := r.svc.State(r.ctx)
	if err != nil {
		return r.wait()
	}
	runnable := core.HostingRunnable(st, h)
	if len(runnable) == 0 { // every script was deleted or regenerated by hand meanwhile: nothing left to run
		if err := r.apply(core.Action{Type: "hostingRecord", HostingID: h.ID}); err != nil {
			r.halt(h.ID, core.HostingRunning, err.Error(), false)
		}
		return false
	}
	for _, sc := range runnable {
		if h.ExecTasks[sc.CaseID] != "" || h.Results[sc.CaseID] != "" {
			continue
		}
		body, _ := json.Marshal(map[string]any{"code": sc.Code, "fileName": sc.FileName, "target": map[string]string{"baseUrl": h.BaseURL}, "title": sc.Title, "casehubTask": r.subject(h)})
		t, err := r.srv.Submit(r.ctx, "executor", body)
		if err != nil {
			r.halt(h.ID, core.HostingRunning, "提交执行任务失败："+err.Error(), false)
			return false
		}
		if err := r.apply(core.Action{Type: "hostingExecSubmitted", HostingID: h.ID, CaseID: sc.CaseID, TaskID: t.ID}); err != nil {
			_ = r.srv.Cancel(r.ctx, "executor", t.ID)
			r.halt(h.ID, core.HostingRunning, err.Error(), false)
			return false
		}
	}
	for {
		h, err = r.hosting(r.ctx, h.ID)
		if err != nil || h.Stage != core.HostingRunning {
			return err == nil // done (or stopped / abandoned meanwhile)
		}
		for _, sc := range runnable {
			taskID := h.ExecTasks[sc.CaseID]
			if taskID == "" && h.Results[sc.CaseID] == "" {
				return true // cleared by a retry: back to submitting
			}
			if taskID == "" || h.Results[sc.CaseID] != "" {
				continue
			}
			t, err := r.srv.Get(r.ctx, "executor", taskID)
			if err != nil && !errors.Is(err, ErrTaskGone) || err == nil && !tasks.Terminal(t.Status) {
				continue
			}
			outcome := core.Action{Type: "hostingRecord", HostingID: h.ID, CaseID: sc.CaseID, Result: "error"}
			if err == nil && t.Status == tasks.Succeeded {
				var res struct {
					Passed   bool   `json:"passed"`
					Markdown string `json:"markdown"`
				}
				if json.Unmarshal(t.Result, &res) == nil {
					outcome.Result = map[bool]string{true: "passed", false: "failed"}[res.Passed]
					outcome.Note = fmt.Sprintf("> AI 托管自动执行的脚本 r%d（%s）\n\n%s", sc.Revision, sc.FileName, res.Markdown)
				}
			}
			if err := r.apply(outcome); err != nil {
				r.Logf("hosting %s: recording %s failed: %v", h.ID, sc.CaseID, err)
				continue
			}
			r.srv.MarkHandled(r.ctx, "executor", taskID)
		}
		if !r.wait() {
			return false
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

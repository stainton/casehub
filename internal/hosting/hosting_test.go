package hosting_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"casehub/internal/core"
	"casehub/internal/hosting"
	"casehub/internal/store"
	"casehub/internal/tasks"
)

// fakePlanner answers the estimate at once and holds each submitted task (of any kind) until the test
// finishes it. submitted lists the planner tasks' bodies; sent lists every kind's.
type fakePlanner struct {
	mu        sync.Mutex
	tasks     map[string]*tasks.Task
	submitted []map[string]any
	sent      map[string][]map[string]any
	bodies    map[string]map[string]any // task ID -> body
	handled   map[string]bool
	code      string
	disabled  map[string]bool
}

func newFake(code string) *fakePlanner {
	return &fakePlanner{tasks: map[string]*tasks.Task{}, sent: map[string][]map[string]any{}, bodies: map[string]map[string]any{}, handled: map[string]bool{}, disabled: map[string]bool{}, code: code}
}

func (f *fakePlanner) Estimate(_ context.Context, body []byte) ([]byte, error) {
	var in struct{ Requirements []struct{ ID string } }
	_ = json.Unmarshal(body, &in)
	return json.Marshal(map[string]any{"suggestedCaseCount": 3, "rationale": "主流程", "requirementCodes": []map[string]string{{"requirement": in.Requirements[0].ID, "code": f.code}}})
}
func (f *fakePlanner) Enabled(kind string) bool { return !f.disabled[kind] }
func (f *fakePlanner) Submit(_ context.Context, kind string, body []byte) (tasks.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if kind == "planner" {
		f.submitted = append(f.submitted, m)
	}
	f.sent[kind] = append(f.sent[kind], m)
	t := &tasks.Task{ID: tasks.NewID(), Kind: kind, Status: tasks.Running}
	f.tasks[t.ID] = t
	f.bodies[t.ID] = m
	return *t, nil
}
func (f *fakePlanner) Get(_ context.Context, kind, id string) (tasks.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tasks[id]
	if t == nil || t.Kind != kind {
		return tasks.Task{}, hosting.ErrTaskGone
	}
	return *t, nil
}
func (f *fakePlanner) Cancel(_ context.Context, _, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t := f.tasks[id]; t != nil && !tasks.Terminal(t.Status) {
		t.Status = tasks.Cancelled
	}
	return nil
}
func (f *fakePlanner) MarkHandled(_ context.Context, _, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handled[id] = true
}

// finish ends the latest running planner task: with the result when result is set, else as a failure.
func (f *fakePlanner) finish(t *testing.T, result any, continuable bool) string {
	return f.finishKind(t, "planner", result, continuable)
}
func (f *fakePlanner) finishKind(t *testing.T, kind string, result any, continuable bool) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		var last *tasks.Task
		for _, x := range f.tasks {
			if x.Kind == kind && x.Status == tasks.Running {
				last = x
			}
		}
		if last != nil {
			if result != nil {
				last.Status, last.Result = tasks.Succeeded, mustJSON(result)
			} else {
				last.Status, last.Error = tasks.Failed, &tasks.ErrorDetail{Code: "JOB_TIMEOUT", Message: "超时"}
				if continuable {
					last.CheckpointSHA = "abc"
				}
			}
			f.mu.Unlock()
			return last.ID
		}
		f.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("no running %s task", kind)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// finishRuns waits for want executor tasks, then ends every running one by its script's title: "fail" fails
// the assertion, "error" fails the run itself, anything else passes.
func (f *fakePlanner) finishRuns(t *testing.T, want int, outcome map[string]string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		n := len(f.sent["executor"])
		f.mu.Unlock()
		if n >= want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d executor tasks submitted, want %d", n, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, x := range f.tasks {
		if x.Kind != "executor" || x.Status != tasks.Running {
			continue
		}
		switch outcome[f.bodies[id]["title"].(string)] {
		case "error":
			x.Status, x.Error = tasks.Failed, &tasks.ErrorDetail{Code: "BROWSER_CRASHED", Message: "浏览器崩溃"}
		case "fail":
			x.Status, x.Result = tasks.Succeeded, mustJSON(map[string]any{"passed": false, "markdown": "## 步骤\n断言失败"})
		default:
			x.Status, x.Result = tasks.Succeeded, mustJSON(map[string]any{"passed": true, "markdown": "## 步骤\n全部通过"})
		}
	}
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func designResult(code string) map[string]any {
	c := func(mod, n, name string) map[string]any {
		return map[string]any{"request": "", "name": name, "case_id": "TC-" + code + "-" + mod + "-FUNC-00" + n, "priority": "P1", "precondition": "已有账号", "steps": "1. 打开登录页", "expects": "1. 显示登录表单"}
	}
	return map[string]any{
		"modules":     []map[string]string{{"code": "AUTH", "name": "登录认证"}, {"code": "PWD", "name": "找回密码"}},
		"cases":       []any{c("AUTH", "1", "正确密码登录成功"), c("AUTH", "2", "错误密码登录失败"), c("PWD", "1", "邮箱找回密码")},
		"issues":      []map[string]string{{"risk": "high", "scenario": "错误密码", "symptom": "没有提示"}},
		"limitations": []map[string]string{{"risk": "low", "summary": "短信未验证"}},
	}
}

type fixture struct {
	svc        *core.Service
	planner    *fakePlanner
	runner     *hosting.Runner
	docID, vid string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	svc := core.NewService(store.NewMemory())
	ctx := context.Background()
	must := func(a core.Action) core.State {
		out, err := svc.Apply(ctx, a)
		if err != nil {
			t.Fatalf("%s: %v", a.Type, err)
		}
		return out.State
	}
	st := must(core.Action{Type: "createReqDoc", FolderID: "req-root", Title: "登录", Content: "登录成功跳转首页"})
	f := &fixture{svc: svc, planner: newFake("LOGIN")}
	for _, d := range st.ReqDocs {
		if d.Title == "登录" {
			f.docID = d.ID
		}
	}
	st = must(core.Action{Type: "createVersion", Name: "迭代 1", Blank: true})
	for _, v := range st.Versions {
		if v.Name == "迭代 1" {
			f.vid = v.ID
		}
	}
	f.start()
	return f
}

func (f *fixture) start() {
	f.runner = hosting.New(f.svc, f.planner)
	f.runner.Poll = 5 * time.Millisecond
	f.runner.Logf = func(string, ...any) {}
}

func (f *fixture) request() hosting.Request {
	return hosting.Request{
		Estimate: mustJSON(map[string]any{"requirements": []map[string]string{{"id": f.docID, "title": "登录"}}}),
		Job:      mustJSON(map[string]any{"requirements": []map[string]string{{"id": f.docID, "title": "登录"}}, "target": map[string]string{"baseUrl": "http://sut"}}),
	}
}

func (f *fixture) hosting(t *testing.T) core.Hosting {
	st, err := f.svc.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range st.Hostings {
		if h.DocID == f.docID {
			return h
		}
	}
	t.Fatal("no hosting")
	return core.Hosting{}
}

func (f *fixture) waitStage(t *testing.T, stage string) core.Hosting {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h := f.hosting(t)
		if h.Stage == stage {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("stage = %s (%s), want %s", h.Stage, h.Error, stage)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fixture) waitSubmitted(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.planner.mu.Lock()
		got := len(f.planner.submitted)
		f.planner.mu.Unlock()
		h := f.hosting(t)
		if got >= n && h.PlannerTaskID != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("submitted %d tasks, want %d (stage %s %s)", got, n, h.Stage, h.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// filed returns the titles of the cases under 全部用例 / product / version / requirement in the version.
func (f *fixture) filed(t *testing.T, product, version, req string) []string {
	st, _ := f.svc.State(context.Background())
	find := func(parent, name string) string {
		for _, x := range st.Folders {
			if x.VersionID == f.vid && x.ParentID == parent && x.Name == name {
				return x.ID
			}
		}
		return ""
	}
	top := find(find(find("root", product), version), req)
	if top == "" {
		t.Fatalf("folder %s/%s/%s missing", product, version, req)
	}
	under := map[string]bool{top: true}
	for changed := true; changed; {
		changed = false
		for _, x := range st.Folders {
			if x.VersionID == f.vid && under[x.ParentID] && !under[x.ID] {
				under[x.ID], changed = true, true
			}
		}
	}
	var titles []string
	for _, c := range st.Cases {
		if c.VersionID == f.vid && under[c.FolderID] {
			titles = append(titles, c.Title)
		}
	}
	return titles
}

func TestHostingRunsFromEstimateToCaseTreeWithoutABrowser(t *testing.T) {
	f := setup(t)
	defer f.runner.Close()
	h, err := f.runner.Begin(context.Background(), core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "TaskLite", ProductVersion: "v2.0"}, f.request(), "alice")
	if err != nil || h.Stage != core.HostingEstimating {
		t.Fatalf("begin: %+v %v", h, err)
	}
	f.waitSubmitted(t, 1)
	job := f.planner.submitted[0]
	if job["caseCount"] != float64(3) {
		t.Fatalf("estimate not adopted: caseCount=%v", job["caseCount"])
	}
	if !strings.Contains(string(mustJSON(job["requirements"])), `"code":"LOGIN"`) {
		t.Fatalf("code not sent: %v", job["requirements"])
	}
	if sub := job["casehubTask"].(map[string]any)["subject"]; sub != "hosting:"+f.docID {
		t.Fatalf("hosted task subject = %v; manual-design restore must not pick it up", sub)
	}
	taskID := f.planner.finish(t, designResult("LOGIN"), false)
	h = f.waitStage(t, core.HostingDone)

	if got := f.filed(t, "TaskLite", "v2.0", "登录"); len(got) != 3 {
		t.Fatalf("filed cases = %v", got)
	}
	st, _ := f.svc.State(context.Background())
	if len(st.PendingCases) != 0 {
		t.Fatalf("review area still holds %d cases", len(st.PendingCases))
	}
	for _, d := range st.ReqDocs {
		if d.ID == f.docID && d.Code != "LOGIN" {
			t.Fatalf("code not saved: %q", d.Code)
		}
	}
	if len(st.Issues) != 1 || !strings.HasPrefix(st.Issues[0].Title, "[高]") {
		t.Fatalf("defects not reported: %+v", st.Issues)
	}
	if len(h.CaseIDs) != 3 || len(h.Limitations) != 1 || h.ReqFolder != "登录" {
		t.Fatalf("hosting record: %+v", h)
	}
	for _, c := range st.Cases {
		if c.VersionID == f.vid && !strings.HasPrefix(c.ID, "TC-LOGIN-") {
			t.Fatalf("case numbered by folder code expected, got %s", c.ID)
		}
	}
	if !f.planner.handled[taskID] {
		t.Fatal("the design task is not marked handled")
	}
}

func TestHostingResumesAfterRestartAndRetriesAStoppedStep(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	// A case already filed where the hosting will file: archiving must stop on the name clash.
	out, _ := f.svc.Apply(ctx, core.Action{Type: "createFolder", VersionID: f.vid, ParentID: "root", Name: "TaskLite"})
	var prod string
	for _, x := range out.State.Folders {
		if x.VersionID == f.vid && x.Name == "TaskLite" {
			prod = x.ID
		}
	}
	if _, err := f.runner.Begin(ctx, core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "TaskLite", ProductVersion: "v2.0"}, f.request(), ""); err != nil {
		t.Fatal(err)
	}
	f.waitSubmitted(t, 1)
	// CaseHub restarts while the design task runs: the new runner follows the same task.
	f.runner.Close()
	f.start()
	if err := f.runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer f.runner.Close()
	// Put the clashing case in place before the task finishes.
	out, _ = f.svc.Apply(ctx, core.Action{Type: "createFolder", VersionID: f.vid, ParentID: prod, Name: "v2.0"})
	var ver string
	for _, x := range out.State.Folders {
		if x.VersionID == f.vid && x.ParentID == prod {
			ver = x.ID
		}
	}
	out, _ = f.svc.Apply(ctx, core.Action{Type: "createFolder", VersionID: f.vid, ParentID: ver, Name: "登录"})
	var req string
	for _, x := range out.State.Folders {
		if x.VersionID == f.vid && x.ParentID == ver {
			req = x.ID
		}
	}
	out, _ = f.svc.Apply(ctx, core.Action{Type: "createFolder", VersionID: f.vid, ParentID: req, Name: "登录认证"})
	var mod string
	for _, x := range out.State.Folders {
		if x.VersionID == f.vid && x.ParentID == req {
			mod = x.ID
		}
	}
	out, _ = f.svc.Apply(ctx, core.Action{Type: "createFolder", VersionID: f.vid, ParentID: mod, Name: "功能测试"})
	var cat string
	for _, x := range out.State.Folders {
		if x.VersionID == f.vid && x.ParentID == mod {
			cat = x.ID
		}
	}
	out, err := f.svc.Apply(ctx, core.Action{Type: "createCase", VersionID: f.vid, FolderID: cat, Title: "正确密码登录成功"})
	if err != nil {
		t.Fatal(err)
	}
	var clash string
	for _, c := range out.State.Cases {
		if c.VersionID == f.vid && c.Title == "正确密码登录成功" {
			clash = c.ID
		}
	}
	f.planner.finish(t, designResult("LOGIN"), false)
	h := f.waitStage(t, core.HostingStopped)
	if h.FailedAt != core.HostingArchiving || !strings.Contains(h.Error, "同名用例") {
		t.Fatalf("stopped at %s: %s", h.FailedAt, h.Error)
	}
	st, _ := f.svc.State(ctx)
	passed := 0
	for _, c := range st.PendingCases {
		if c.Review == "passed" && c.ReviewedBy == core.HostingAuthor {
			passed++
		}
	}
	if passed != 3 {
		t.Fatalf("%d cases passed and waiting in the review area, want 3", passed)
	}
	if _, err := f.runner.Retry(ctx, h.ID, nil); err != nil {
		t.Fatalf("retry without the conflict resolved should still start: %v", err)
	}
	f.waitStage(t, core.HostingStopped)
	if _, err := f.svc.Apply(ctx, core.Action{Type: "deleteCases", VersionID: f.vid, CaseIDs: []string{clash}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.Retry(ctx, h.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.waitStage(t, core.HostingDone)
	if got := f.filed(t, "TaskLite", "v2.0", "登录"); len(got) != 3 {
		t.Fatalf("filed = %v", got)
	}
	if len(f.planner.submitted) != 1 {
		t.Fatalf("retrying archiving must not redesign: %d submissions", len(f.planner.submitted))
	}
}

func TestHostingContinuesAFailedDesign(t *testing.T) {
	f := setup(t)
	defer f.runner.Close()
	ctx := context.Background()
	h, _ := f.runner.Begin(ctx, core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "P", ProductVersion: "1.0"}, f.request(), "")
	f.waitSubmitted(t, 1)
	failed := f.planner.finish(t, nil, true)
	h = f.waitStage(t, core.HostingStopped)
	if h.FailedAt != core.HostingDesigning || !h.Continuable {
		t.Fatalf("design failure: %+v", h)
	}
	if _, err := f.runner.Retry(ctx, h.ID, nil); err == nil {
		t.Fatal("redoing a design needs the request (credentials are never stored)")
	}
	req := f.request()
	req.Estimate, req.Continue = nil, true
	if _, err := f.runner.Retry(ctx, h.ID, &req); err != nil {
		t.Fatal(err)
	}
	f.waitSubmitted(t, 2)
	if f.planner.submitted[1]["continueFrom"] != failed {
		t.Fatalf("continueFrom = %v, want %s", f.planner.submitted[1]["continueFrom"], failed)
	}
	f.planner.finish(t, designResult("LOGIN"), false)
	f.waitStage(t, core.HostingDone)

	// A second hosting of the same requirement replaces the finished record; while it runs it cannot be deleted
	// as a plain action, only abandoned through the runner.
	if _, err := f.runner.Begin(ctx, core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "P", ProductVersion: "1.0"}, f.request(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.Begin(ctx, core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "P", ProductVersion: "1.0"}, f.request(), ""); err == nil {
		t.Fatal("one active hosting per requirement")
	}
	f.waitSubmitted(t, 3)
	h = f.hosting(t)
	if _, err := f.svc.Apply(ctx, core.Action{Type: "deleteHosting", HostingID: h.ID}); err == nil {
		t.Fatal("an active hosting cannot be deleted as a plain action")
	}
	if err := f.runner.Abandon(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if t2, _ := f.planner.Get(ctx, "planner", h.PlannerTaskID); t2.Status != tasks.Cancelled {
		t.Fatalf("abandoning must cancel the running design task: %s", t2.Status)
	}
	st, _ := f.svc.State(ctx)
	if len(st.Hostings) != 0 {
		t.Fatalf("hosting not removed: %+v", st.Hostings)
	}
}

func (f *fixture) fullRequest() hosting.Request {
	req := f.request()
	req.Generate = mustJSON(map[string]any{"target": map[string]string{"baseUrl": "http://sut"}, "context": map[string]any{"testData": map[string]string{"username": "u", "password": "pw"}}, "caseTimeoutMs": 600000})
	return req
}

func generatorResult(status map[string]string) map[string]any {
	var scripts []map[string]any
	for _, id := range []string{"TC-LOGIN-AUTH-FUNC-001", "TC-LOGIN-AUTH-FUNC-002", "TC-LOGIN-PWD-FUNC-001"} {
		sc := map[string]any{"caseId": id, "fileName": id + ".spec.ts", "language": "typescript", "status": "generated", "code": "test('x', async () => {})", "summary": "ok"}
		switch status[id] {
		case "blocked":
			sc["status"], sc["code"], sc["missingInputs"] = "blocked", "", []string{"短信验证码"}
		case "deviation":
			sc["deviations"] = []map[string]string{{"risk": "high", "summary": "错误密码没有提示"}}
		}
		scripts = append(scripts, sc)
	}
	return map[string]any{"scripts": scripts, "generated": 2, "blocked": 1, "explorationRecords": map[string]string{}}
}

func (f *fixture) records(t *testing.T) map[string]core.Record {
	st, _ := f.svc.State(context.Background())
	out := map[string]core.Record{}
	for _, r := range st.Records {
		if r.VersionID == f.vid {
			out[r.CaseID] = r
		}
	}
	return out
}

func TestHostingGeneratesScriptsAndRunsThem(t *testing.T) {
	f := setup(t)
	defer f.runner.Close()
	ctx := context.Background()
	h, err := f.runner.Begin(ctx, core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "TaskLite", ProductVersion: "v2.0", Generate: true, Execute: true, BaseURL: "http://sut"}, f.fullRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	f.waitSubmitted(t, 1)
	f.planner.finish(t, designResult("LOGIN"), false)
	f.planner.finishKind(t, "generator", generatorResult(map[string]string{"TC-LOGIN-AUTH-FUNC-002": "deviation", "TC-LOGIN-PWD-FUNC-001": "blocked"}), false)

	f.planner.mu.Lock()
	gen := f.planner.sent["generator"][0]
	f.planner.mu.Unlock()
	if cases := gen["cases"].([]any); len(cases) != 3 || cases[0].(map[string]any)["requirement"] != f.docID {
		t.Fatalf("generator cases: %v", gen["cases"])
	}
	if !strings.Contains(string(mustJSON(gen["context"])), `"password":"pw"`) || gen["requirements"] == nil {
		t.Fatalf("generator request lost the account or the requirement: %v", gen)
	}
	// Only the two generated scripts run; the blocked one does not.
	f.planner.finishRuns(t, 2, map[string]string{"错误密码登录失败": "fail"})
	h = f.waitStage(t, core.HostingDone)
	if h.Generated != 2 || h.Blocked != 1 || h.Results["TC-LOGIN-AUTH-FUNC-001"] != "passed" || h.Results["TC-LOGIN-AUTH-FUNC-002"] != "failed" {
		t.Fatalf("hosting: generated %d blocked %d results %v", h.Generated, h.Blocked, h.Results)
	}
	recs := f.records(t)
	if len(recs) != 2 || recs["TC-LOGIN-AUTH-FUNC-001"].Result != "passed" || !recs["TC-LOGIN-AUTH-FUNC-002"].Submitted ||
		!strings.Contains(recs["TC-LOGIN-AUTH-FUNC-001"].Note, "AI 托管自动执行的脚本 r1") {
		t.Fatalf("records: %+v", recs)
	}
	st, _ := f.svc.State(ctx)
	scripts := 0
	for _, sc := range st.Scripts {
		if sc.VersionID == f.vid {
			scripts++
		}
	}
	defect := false
	for _, i := range st.Issues {
		defect = defect || strings.Contains(i.Title, "错误密码没有提示")
	}
	if scripts != 3 || !defect {
		t.Fatalf("%d scripts saved (want 3, blocked included); deviation reported: %v", scripts, defect)
	}
	if body := f.planner.sent["executor"][0]; body["target"].(map[string]any)["baseUrl"] != "http://sut" || body["code"] == "" {
		t.Fatalf("executor request: %v", body)
	}
}

func TestHostingRerunsOnlyScriptsWhoseRunFailed(t *testing.T) {
	f := setup(t)
	defer f.runner.Close()
	ctx := context.Background()
	h, _ := f.runner.Begin(ctx, core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "P", ProductVersion: "1", Execute: true, BaseURL: "http://sut"}, f.fullRequest(), "")
	f.waitSubmitted(t, 1)
	f.planner.finish(t, designResult("LOGIN"), false)
	f.planner.finishKind(t, "generator", generatorResult(nil), false)
	f.planner.finishRuns(t, 3, map[string]string{"邮箱找回密码": "error"})
	h = f.waitStage(t, core.HostingStopped)
	if h.FailedAt != core.HostingRunning || !strings.Contains(h.Error, "1 条用例") || len(f.records(t)) != 2 {
		t.Fatalf("a run that itself failed must stop the hosting with the others recorded: %+v", h)
	}
	if _, err := f.runner.Retry(ctx, h.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.planner.finishRuns(t, 4, nil)
	h = f.waitStage(t, core.HostingDone)
	if len(f.planner.sent["executor"]) != 4 || len(f.records(t)) != 3 {
		t.Fatalf("retry must re-run just the errored script: %d runs, %d records", len(f.planner.sent["executor"]), len(f.records(t)))
	}
}

func TestHostingAsksForTheAccountAgainWhenARestartLostIt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.runner.Begin(ctx, core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "P", ProductVersion: "1", Generate: true, BaseURL: "http://sut"}, f.fullRequest(), "")
	f.waitSubmitted(t, 1)
	f.runner.Close() // CaseHub restarts during the design: the generator request (with the account) is gone
	f.start()
	defer f.runner.Close()
	if err := f.runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	f.planner.finish(t, designResult("LOGIN"), false)
	h := f.waitStage(t, core.HostingStopped)
	if h.FailedAt != core.HostingGenerating || len(f.filed(t, "P", "1", "登录")) != 3 {
		t.Fatalf("the cases must be filed and the hosting wait at generating: %+v", h)
	}
	if _, err := f.runner.Retry(ctx, h.ID, nil); err == nil {
		t.Fatal("a new generation needs the generator request again")
	}
	req := f.fullRequest()
	if _, err := f.runner.Retry(ctx, h.ID, &hosting.Request{Generate: req.Generate}); err != nil {
		t.Fatal(err)
	}
	f.planner.finishKind(t, "generator", generatorResult(nil), false)
	h = f.waitStage(t, core.HostingDone)
	if h.Generated != 3 || len(f.planner.submitted) != 1 {
		t.Fatalf("generated %d, %d designs", h.Generated, len(f.planner.submitted))
	}
}

func TestHostingRefusesStagesWhoseServiceIsMissing(t *testing.T) {
	f := setup(t)
	defer f.runner.Close()
	f.planner.disabled["executor"] = true
	if _, err := f.runner.Begin(context.Background(), core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "P", ProductVersion: "1", Execute: true, BaseURL: "http://sut"}, f.fullRequest(), ""); err == nil || !strings.Contains(err.Error(), "executor") {
		t.Fatalf("err = %v", err)
	}
}

// A stop before the generation (here: a name clash while filing) keeps the account in memory, so retrying
// the stopped step needs nothing re-entered.
func TestHostingKeepsTheAccountAcrossARetryBeforeGeneration(t *testing.T) {
	f := setup(t)
	defer f.runner.Close()
	ctx := context.Background()
	h, _ := f.runner.Begin(ctx, core.Hosting{DocID: f.docID, VersionID: f.vid, Product: "P", ProductVersion: "1", Generate: true, BaseURL: "http://sut"}, f.fullRequest(), "")
	f.waitSubmitted(t, 1)
	if _, err := f.svc.Apply(ctx, core.Action{Type: "archiveVersion", VersionID: f.vid}); err != nil {
		t.Fatal(err)
	}
	f.planner.finish(t, designResult("LOGIN"), false)
	h = f.waitStage(t, core.HostingStopped)
	if h.FailedAt != core.HostingArchiving {
		t.Fatalf("stopped at %s: %s", h.FailedAt, h.Error)
	}
	if _, err := f.svc.Apply(ctx, core.Action{Type: "unarchiveVersion", VersionID: f.vid}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.Retry(ctx, h.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.planner.finishKind(t, "generator", generatorResult(nil), false)
	f.waitStage(t, core.HostingDone)
	f.planner.mu.Lock()
	defer f.planner.mu.Unlock()
	if !strings.Contains(string(mustJSON(f.planner.sent["generator"][0]["context"])), `"password":"pw"`) {
		t.Fatal("the generator request lost the account")
	}
}

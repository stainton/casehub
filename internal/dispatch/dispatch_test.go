package dispatch_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"casehub/internal/dispatch"
	"casehub/internal/tasks"
)

func pool(t *testing.T, nodes ...*fakeNode) *dispatch.Pool {
	var urls []string
	for _, n := range nodes {
		urls = append(urls, n.URL())
	}
	p, err := dispatch.ParsePool(strings.Join(urls, ","))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func start(t *testing.T, store tasks.Store, kinds ...*dispatch.Kind) *dispatch.Dispatcher {
	t.Helper()
	d := dispatch.New(store, kinds, dispatch.Config{NodeLostAfter: 300 * time.Millisecond, Logf: t.Logf})
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func status(t *testing.T, d *dispatch.Dispatcher, kind, id string) tasks.Task {
	t.Helper()
	task, err := d.Get(context.Background(), kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func waitStatus(t *testing.T, d *dispatch.Dispatcher, kind, id, want string) tasks.Task {
	t.Helper()
	var last tasks.Task
	waitFor(t, "task "+id+" to be "+want, func() bool { last = status(t, d, kind, id); return last.Status == want })
	return last
}

const request = `{"target":{"baseUrl":"https://app.example.test/login"},"context":{"testData":{"username":"qa","password":"secret"}},"requirements":[{"id":"REQ-1"}]}`

func TestHandsATaskToAnIdleNodeAndCollectsItsResult(t *testing.T) {
	busy, idle := newFakeNode(t, "generator", 1), newFakeNode(t, "generator", 1)
	busy.occupy()
	store := tasks.NewMemory()
	d := start(t, store, &dispatch.Kind{Name: "generator", Pool: pool(t, busy, idle)})
	task, err := d.Submit(context.Background(), "generator", []byte(request))
	if err != nil {
		t.Fatal(err)
	}
	job := idle.only()
	if len(busy.submissions()) != 0 {
		t.Fatal("the busy node must not be offered the task")
	}
	idle.start(job)
	waitStatus(t, d, "generator", task.ID, tasks.Running)
	idle.succeed(job, `{"scripts":[{"caseId":"TC-1"}],"generated":1}`)
	done := waitStatus(t, d, "generator", task.ID, tasks.Succeeded)
	if string(done.Result) != `{"scripts":[{"caseId":"TC-1"}],"generated":1}` {
		t.Fatalf("result = %s", done.Result)
	}
	waitFor(t, "the node job to be released", func() bool { return len(idle.releasedIDs()) == 1 })
	events, _ := store.Events(context.Background(), task.ID, 0)
	text := ""
	for _, e := range events {
		text += string(e) + "\n"
	}
	if !strings.Contains(text, `"tool":"browser_snapshot"`) || !strings.Contains(text, `"casesGenerated":3`) || strings.Count(text, `"status":"succeeded"`) != 1 {
		t.Fatalf("progress must be copied from the node, with one final event carrying its fields:\n%s", text)
	}
	if strings.Contains(text, "secret") {
		t.Fatal("no event may carry the request's credentials")
	}
}

func TestQueuesWhileEveryNodeIsBusyAndDispatchesWhenOneFrees(t *testing.T) {
	node := newFakeNode(t, "planner", 1)
	other := node.occupy()
	d := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "planner", Pool: pool(t, node)})
	task, err := d.Submit(context.Background(), "planner", []byte(request))
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != tasks.Queued || task.Stage != "queued" {
		t.Fatalf("task = %s/%s, want queued", task.Status, task.Stage)
	}
	if got := d.Load()["planner"]; got != [2]int{1, 0} {
		t.Fatalf("load = %v, want one queued", got)
	}
	node.free(other)
	job := node.only()
	node.start(job)
	waitStatus(t, d, "planner", task.ID, tasks.Running)
	if got := d.Load()["planner"]; got != [2]int{0, 1} {
		t.Fatalf("load = %v, want one running", got)
	}
}

func TestARequestTheNodeRejectsIsRefusedAtOnce(t *testing.T) {
	node := newFakeNode(t, "planner", 1)
	node.reject = func(map[string]any) (int, string) { return 400, "INVALID_REQUEST" }
	store := tasks.NewMemory()
	d := start(t, store, &dispatch.Kind{Name: "planner", Pool: pool(t, node)})
	_, err := d.Submit(context.Background(), "planner", []byte(request))
	var refusal *dispatch.HTTPError
	if !errors.As(err, &refusal) || refusal.Status != 400 || refusal.Code != "INVALID_REQUEST" {
		t.Fatalf("err = %v, want the node's 400", err)
	}
	if list, _ := store.ListTasks(context.Background(), tasks.Filter{}); len(list) != 0 {
		t.Fatal("a refused request must leave no task behind")
	}
}

func TestAPlannerSessionContinuesOnAnotherNodeFromItsCheckpoint(t *testing.T) {
	first, second := newFakeNode(t, "planner", 1), newFakeNode(t, "planner", 1)
	store := tasks.NewMemory()
	d := start(t, store, &dispatch.Kind{Name: "planner", Pool: pool(t, first), Continuable: true})
	task, _ := d.Submit(context.Background(), "planner", []byte(request))
	job := first.only()
	first.start(job)
	first.fail(job, "JOB_TIMEOUT", true, false)
	failed := waitStatus(t, d, "planner", task.ID, tasks.Failed)
	if !failed.Continuable() || failed.Summary()["continuable"] != true || failed.Error.Code != "JOB_TIMEOUT" {
		t.Fatalf("a timed-out planner task must be continuable: %+v", failed.Summary())
	}
	archive := []byte("session-of-" + job.id)
	sum := sha256.Sum256(archive)
	if failed.CheckpointSHA != hex.EncodeToString(sum[:]) {
		t.Fatal("the session archive must be kept by its hash")
	}
	waitFor(t, "release", func() bool { return len(first.releasedIDs()) == 1 })

	// The first node is gone; another one continues the session.
	d2 := start(t, store, &dispatch.Kind{Name: "planner", Pool: pool(t, second), Continuable: true})
	continued, err := d2.Submit(context.Background(), "planner", []byte(`{"continueFrom":"`+task.ID+`","target":{"baseUrl":"https://app.example.test"}}`))
	if err != nil {
		t.Fatal(err)
	}
	sent := second.only()
	_ = sent
	body := second.submissions()[0]
	if cf, _ := body["continueFrom"].(map[string]any); cf["checkpoint"] != failed.CheckpointSHA {
		t.Fatalf("continueFrom = %v, want the pushed checkpoint", body["continueFrom"])
	}
	if string(second.assets[failed.CheckpointSHA]) != string(archive) {
		t.Fatal("the archive must be pushed to the node before the task names it")
	}
	if source := status(t, d2, "planner", task.ID); source.Continuable() || source.ContinuedAt == nil {
		t.Fatal("a session is continued once")
	}
	if _, err := d2.Submit(context.Background(), "planner", []byte(`{"continueFrom":"`+task.ID+`"}`)); err == nil {
		t.Fatal("continuing twice must be refused")
	}
	_ = continued
}

func TestASessionOnAnOldNodeIsContinuedOnThatNode(t *testing.T) {
	old, other := newFakeNode(t, "planner", 1), newFakeNode(t, "planner", 5)
	old.legacy = true
	d := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "planner", Pool: pool(t, old), Continuable: true})
	task, _ := d.Submit(context.Background(), "planner", []byte(request))
	job := old.only()
	old.start(job)
	old.fail(job, "JOB_TIMEOUT", true, false)
	failed := waitStatus(t, d, "planner", task.ID, tasks.Failed)
	if !failed.LegacyContinue || failed.CheckpointSHA != "" || !failed.Continuable() {
		t.Fatalf("an old node's session stays there: %+v", failed)
	}
	d2 := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "planner", Pool: pool(t, other, old), Continuable: true})
	_ = d2
	if _, err := d.Submit(context.Background(), "planner", []byte(`{"continueFrom":"`+task.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	old.waitJobs(2)
	if got := old.submissions()[1]["continueFrom"]; got != job.id {
		t.Fatalf("continueFrom = %v, want the node's own job id %s", got, job.id)
	}
}

func TestANodeShuttingDownHandsWorkBackToTheQueue(t *testing.T) {
	first, second := newFakeNode(t, "generator", 1), newFakeNode(t, "generator", 1)
	second.occupy()
	d := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "generator", Pool: pool(t, first, second)})
	task, _ := d.Submit(context.Background(), "generator", []byte(request))
	job := first.only()
	first.start(job)
	first.mu.Lock()
	first.concurrency = 0 // a draining node takes nothing new
	first.mu.Unlock()
	first.fail(job, "NODE_SHUTDOWN", false, false)
	waitFor(t, "the task to be queued again", func() bool {
		s := status(t, d, "generator", task.ID)
		return s.Status == tasks.Queued && s.Node == ""
	})
	second.mu.Lock()
	second.jobs = map[string]*fakeJob{}
	second.mu.Unlock()
	again := second.only()
	second.start(again)
	second.succeed(again, `{"generated":1}`)
	if done := waitStatus(t, d, "generator", task.ID, tasks.Succeeded); done.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", done.Attempts)
	}
}

func TestAPlannerInterruptedByShutdownStaysWithThePerson(t *testing.T) {
	node := newFakeNode(t, "planner", 1)
	d := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "planner", Pool: pool(t, node), Continuable: true})
	task, _ := d.Submit(context.Background(), "planner", []byte(request))
	job := node.only()
	node.start(job)
	node.fail(job, "NODE_SHUTDOWN", true, false)
	failed := waitStatus(t, d, "planner", task.ID, tasks.Failed)
	if !failed.Continuable() || failed.Error.Code != "NODE_SHUTDOWN" {
		t.Fatalf("an interrupted exploration is continued by choice, not rerun: %+v", failed.Summary())
	}
}

func TestALostNodeRerunsAGeneratorTaskAndEndsAPlannerOne(t *testing.T) {
	lost, spare := newFakeNode(t, "generator", 1), newFakeNode(t, "generator", 1)
	spare.occupy()
	d := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "generator", Pool: pool(t, lost, spare)})
	task, _ := d.Submit(context.Background(), "generator", []byte(request))
	job := lost.only()
	lost.start(job)
	waitStatus(t, d, "generator", task.ID, tasks.Running)
	lost.mu.Lock()
	lost.jobs = map[string]*fakeJob{} // the node restarted: it no longer knows the job
	lost.concurrency = 0
	lost.mu.Unlock()
	lost.srv.CloseClientConnections()
	spare.mu.Lock()
	spare.jobs = map[string]*fakeJob{}
	spare.mu.Unlock()
	waitFor(t, "the task to move to the spare node", func() bool { return len(spare.submissions()) == 1 })

	planner := newFakeNode(t, "planner", 1)
	dp := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "planner", Pool: pool(t, planner), Continuable: true})
	ptask, _ := dp.Submit(context.Background(), "planner", []byte(request))
	pjob := planner.only()
	planner.start(pjob)
	waitStatus(t, dp, "planner", ptask.ID, tasks.Running)
	planner.srv.CloseClientConnections()
	planner.srv.Close()
	if failed := waitStatus(t, dp, "planner", ptask.ID, tasks.Failed); failed.Error.Code != "NODE_LOST" || failed.Continuable() {
		t.Fatalf("a planner whose node vanished has no session to continue: %+v", failed.Summary())
	}
}

func TestCancellingAQueuedOrRunningTask(t *testing.T) {
	node := newFakeNode(t, "executor", 1)
	other := node.occupy()
	d := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "executor", Pool: pool(t, node)})
	queued, _ := d.Submit(context.Background(), "executor", []byte(request))
	if c, _ := d.Cancel(context.Background(), "executor", queued.ID); c.Status != tasks.Cancelled {
		t.Fatalf("a queued task is cancelled at once, got %s", c.Status)
	}
	node.free(other)
	running, _ := d.Submit(context.Background(), "executor", []byte(request))
	job := node.only()
	node.start(job)
	waitStatus(t, d, "executor", running.ID, tasks.Running)
	if _, err := d.Cancel(context.Background(), "executor", running.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, d, "executor", running.ID, tasks.Cancelled)
	if len(node.cancelled) != 1 {
		t.Fatal("the node must be told to stop")
	}
}

func TestARestartedCaseHubPicksUpTasksAlreadyOnANode(t *testing.T) {
	node := newFakeNode(t, "generator", 1)
	store := tasks.NewMemory()
	kind := func() *dispatch.Kind { return &dispatch.Kind{Name: "generator", Pool: pool(t, node)} }
	first := dispatch.New(store, []*dispatch.Kind{kind()}, dispatch.Config{Logf: t.Logf})
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	running, _ := first.Submit(context.Background(), "generator", []byte(request))
	job := node.only()
	node.start(job)
	waitStatus(t, first, "generator", running.ID, tasks.Running)
	waiting, _ := first.Submit(context.Background(), "generator", []byte(request)) // node busy: stays queued
	first.Close()

	second := start(t, store, kind())
	if lost := status(t, second, "generator", waiting.ID); lost.Status != tasks.Failed || lost.Error.Code != "CASEHUB_RESTARTED" {
		t.Fatalf("a task still waiting lost its request with the old process: %+v", lost.Summary())
	}
	node.succeed(job, `{"generated":1}`)
	waitStatus(t, second, "generator", running.ID, tasks.Succeeded)
}

func TestTheHTTPContractMatchesANodes(t *testing.T) {
	node := newFakeNode(t, "planner", 1)
	d := start(t, tasks.NewMemory(), &dispatch.Kind{Name: "planner", Pool: pool(t, node), Continuable: true})
	mux := http.NewServeMux()
	mux.Handle("/api/planner/jobs", d.Jobs("planner"))
	mux.Handle("/api/planner/jobs/", d.Jobs("planner"))
	mux.Handle("GET /api/tasks", d.Tasks())
	mux.Handle("GET /metrics", d.Metrics())
	mux.Handle("GET /api/queue", d.Queue())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	post := func(body string, contentType string) *http.Response {
		resp, err := http.Post(srv.URL+"/api/planner/jobs", contentType, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := post(request, "text/plain"); resp.StatusCode != 415 {
		t.Fatalf("status = %d, want 415", resp.StatusCode)
	}
	resp := post(`{"casehubTask":{"subject":"req:42","meta":{"doc":"42"}},`+request[1:], "application/json")
	if resp.StatusCode != 202 || !strings.HasPrefix(resp.Header.Get("Location"), "/api/planner/jobs/") {
		t.Fatalf("submit: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	var job map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&job)
	id := job["id"].(string)
	if _, leaked := node.submissions()[0]["casehubTask"]; leaked {
		t.Fatal("casehubTask is CaseHub's own field and never reaches a node")
	}
	nodeJob := node.only()
	node.start(nodeJob)
	node.succeed(nodeJob, `{"cases":[]}`)
	waitStatus(t, d, "planner", id, tasks.Succeeded)
	get := func(path string) (int, string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("/api/planner/jobs/" + id + "/result"); code != 200 || body != `{"cases":[]}` {
		t.Fatalf("result: %d %s", code, body)
	}
	code, stream := get("/api/planner/jobs/" + id + "/events")
	scanner := bufio.NewScanner(strings.NewReader(stream))
	var kinds []string
	for scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, "event: ") {
			kinds = append(kinds, strings.TrimPrefix(line, "event: "))
		}
	}
	if code != 200 || kinds[0] != "snapshot" || kinds[len(kinds)-1] != "progress" || !strings.Contains(stream, `"status":"succeeded"`) {
		t.Fatalf("events: %d %v\n%s", code, kinds, stream)
	}
	if code, _ := get("/api/planner/jobs/" + id + "/events?after=999"); code != 400 {
		t.Fatalf("a cursor beyond lastEventId is a 400, got %d", code)
	}
	if code, body := get("/api/tasks?kind=planner&subject=req:42&active=1"); code != 200 || !strings.Contains(body, `"meta":{"doc":"42"}`) {
		t.Fatalf("listing: %d %s", code, body)
	}
	patch, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/planner/jobs/"+id, strings.NewReader(`{"handled":true}`))
	if resp, err := http.DefaultClient.Do(patch); err != nil || resp.StatusCode != 200 {
		t.Fatalf("patch: %v %v", err, resp)
	}
	if _, body := get("/api/tasks?kind=planner&subject=req:42&active=1"); strings.Contains(body, id) {
		t.Fatal("a handled task leaves the active listing")
	}
	if code, _ := get("/api/planner/jobs/" + id + "/diagnostics"); code != 404 {
		t.Fatalf("diagnostics of a task that kept none: %d", code)
	}
	if code, _ := get("/api/generator/jobs/" + id); code != 404 {
		t.Fatal("unregistered kinds do not answer")
	}
	if _, body := get("/api/queue"); body != `{"planner":{"demand":0,"nodes":1,"queued":0,"running":0}}`+"\n" {
		t.Fatalf("queue: %s", body)
	}
	if _, body := get("/metrics"); !strings.Contains(body, `casehub_tasks{kind="planner",state="queued"} 0`) || !strings.Contains(body, `casehub_nodes{kind="planner"} 1`) {
		t.Fatalf("metrics:\n%s", body)
	}
}

func TestFailedRunDiagnosticsAreKept(t *testing.T) {
	node := newFakeNode(t, "healer", 1)
	store := tasks.NewMemory()
	d := start(t, store, &dispatch.Kind{Name: "healer", Pool: pool(t, node)})
	task, _ := d.Submit(context.Background(), "healer", []byte(request))
	job := node.only()
	node.start(job)
	node.fail(job, "HEALER_FAILED", false, true)
	failed := waitStatus(t, d, "healer", task.ID, tasks.Failed)
	data, err := store.GetBlob(context.Background(), task.ID, tasks.BlobDiagnostics)
	if !failed.Diagnostics || err != nil || string(data) != "diagnostics-of-"+job.id {
		t.Fatalf("diagnostics: %v %q %v", failed.Diagnostics, data, err)
	}
}

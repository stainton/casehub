package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"casehub/internal/core"
	"casehub/internal/store"
	"casehub/internal/upstream"
)

func TestServiceURLs(t *testing.T) {
	for _, tc := range []struct {
		name               string
		vars               map[string]string
		planner, generator string
	}{
		{"local defaults", nil, "http://localhost:4501", "http://localhost:4502"},
		{"separate pods", map[string]string{"CASEHUB_PLANNER_URL": "http://planner:4501", "CASEHUB_GENERATOR_URL": "http://generator:4502"}, "http://planner:4501", "http://generator:4502"},
		{"combined automation", map[string]string{"CASEHUB_AUTOMATION_URL": "http://automation:4501"}, "http://automation:4501", "http://automation:4501"},
		{"per-service URL wins over automation", map[string]string{"CASEHUB_AUTOMATION_URL": "http://automation:4501", "CASEHUB_GENERATOR_URL": "http://127.0.0.1:4599"}, "http://automation:4501", "http://127.0.0.1:4599"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planner, generator := serviceURLs(func(key string) string { return tc.vars[key] })
			if planner != tc.planner || generator != tc.generator {
				t.Fatalf("serviceURLs() = %q, %q; want %q, %q", planner, generator, tc.planner, tc.generator)
			}
		})
	}
}

func TestStorageKind(t *testing.T) {
	for _, tc := range []struct{ name, kind, url, want string }{
		{"default local", "", "", "file"},
		{"database service", "", "postgres://casehub:password@postgres.database.svc.cluster.local:5432/casehub", "postgres"},
		{"explicit memory", "memory", "postgres://db/casehub", "memory"},
		{"explicit file", "file", "postgres://db/casehub", "file"},
		{"explicit postgres", "postgres", "postgres://db/casehub", "postgres"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := storageKind(tc.kind, tc.url); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Both upstream services are optional; with neither configured the app still serves
// its own pages and API, and each status endpoint reports its own service disabled.
func disabledServices() []service {
	planner, plannerEnabled := upstream.New("", "planner", nil, nil)
	generator, generatorEnabled := upstream.New("", "generator", nil, nil)
	executor, executorEnabled := upstream.New("", "executor", nil, nil)
	generalAgent, generalAgentEnabled := upstream.New("", "general-agent", nil, nil)
	return []service{{name: "planner", proxy: planner, enabled: plannerEnabled},
		{name: "generator", proxy: generator, enabled: generatorEnabled},
		{name: "executor", proxy: executor, enabled: executorEnabled},
		{name: "general-agent", proxy: generalAgent, enabled: generalAgentEnabled}}
}

func TestUpstreamServiceStatusIsReportedPerService(t *testing.T) {
	handler := routes(&api{service: core.NewService(store.NewMemory())}, disabledServices()...)
	for _, name := range []string{"planner", "generator", "executor", "general-agent"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/"+name+"/status", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
			t.Fatalf("%s status: %d %s", name, w.Code, w.Body.String())
		}
	}
}

func TestFrontendAssetsAndMarkdownRecord(t *testing.T) {
	handler := routes(&api{service: core.NewService(store.NewMemory())}, disabledServices()...)
	for _, path := range []string{"/", "/web/api.html", "/web/api.js", "/web/vendor/toastui-editor-all.min.js", "/web/vendor/toastui-editor.min.css", "/web/vendor/toastui-editor-dark.min.css", "/web/vendor/zh-cn.js"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("asset %s: status %d", path, w.Code)
		}
	}
	apply := func(a core.Action) core.Result {
		t.Helper()
		body, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatalf("%s: %s", a.Type, w.Body.String())
		}
		var out core.Result
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := apply(core.Action{Type: "createVersion", Name: "Markdown regression"})
	vid := out.State.Versions[len(out.State.Versions)-1].ID
	note := "## 执行环境\n\n**通过**\n\n- 登录成功\n\n```text\n原始日志 <test>\n```"
	out = apply(core.Action{Type: "submitRecord", VersionID: vid, CaseID: "CASE-0001", Result: "passed", Note: note})
	r := out.State.Records[0]
	if r.Note != note || !r.Submitted || r.Result != "passed" {
		t.Fatalf("record was not preserved: %+v", r)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	if !strings.Contains(w.Body.String(), "执行环境") {
		t.Fatal("saved Markdown missing from state")
	}
}

func TestCrossOriginRequests(t *testing.T) {
	handler := routes(&api{service: core.NewService(store.NewMemory())}, disabledServices()...)
	for _, method := range []string{http.MethodOptions, http.MethodGet} {
		r := httptest.NewRequest(method, "/api/state", nil)
		r.Header.Set("Origin", "http://localhost:4501")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatal("cross-origin access was not enabled")
		}
		want := http.StatusOK
		if method == http.MethodOptions {
			want = http.StatusNoContent
		}
		if w.Code != want {
			t.Fatalf("%s returned %d, want %d", method, w.Code, want)
		}
	}
}

func TestRecordImagesAreExternalizedAndLoadedLazily(t *testing.T) {
	handler := routes(&api{service: core.NewService(store.NewMemory())}, disabledServices()...)
	createBody, err := json.Marshal(core.Action{Type: "createVersion", Name: "lazy-image"})
	if err != nil {
		t.Fatal(err)
	}
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(createBody)))
	if created.Code != http.StatusOK {
		t.Fatalf("create version: %d %s", created.Code, created.Body.String())
	}
	var createdOut core.Result
	if err := json.Unmarshal(created.Body.Bytes(), &createdOut); err != nil {
		t.Fatal(err)
	}
	vid := createdOut.State.Versions[len(createdOut.State.Versions)-1].ID
	body, err := json.Marshal(core.Action{Type: "submitRecord", VersionID: vid, CaseID: "CASE-0001", Result: "passed", Note: "![证据](data:image/png;base64,aGVsbG8=)"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("action: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "aGVsbG8=") || !strings.Contains(w.Body.String(), "/api/record-images/") {
		t.Fatalf("action response retained inline image: %s", w.Body.String())
	}
	state := httptest.NewRecorder()
	handler.ServeHTTP(state, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	if strings.Contains(state.Body.String(), "aGVsbG8=") || !strings.Contains(state.Body.String(), "/api/record-images/") {
		t.Fatalf("state retained inline image: %s", state.Body.String())
	}
	var out core.State
	if err := json.Unmarshal(state.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(out.Records[0].Note, "![证据](/api/record-images/"), ")")
	image := httptest.NewRecorder()
	handler.ServeHTTP(image, httptest.NewRequest(http.MethodGet, "/api/record-images/"+id, nil))
	if image.Code != http.StatusOK || image.Header().Get("Content-Type") != "image/png" || image.Body.String() != "hello" {
		t.Fatalf("lazy image: %d %s %q", image.Code, image.Header().Get("Content-Type"), image.Body.String())
	}
}

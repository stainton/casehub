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

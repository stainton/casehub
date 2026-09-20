package main

import (
	"casehub/internal/core"
	"casehub/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentSettingsPersistAndValidate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	handler := routes(&api{service: core.NewService(store.NewFile(file))})
	send := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	saved := send("PUT", "/api/agent-settings/playwright", `{"baseUrl":"https://target.test","instructions":"覆盖登录","testAccount":"tester","testSecret":"default-password","timeoutMinutes":30}`)
	if saved.Code != 200 {
		t.Fatal(saved.Body.String())
	}
	handler = routes(&api{service: core.NewService(store.NewFile(file))})
	out := send("GET", "/api/agent-settings/playwright", "")
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"testSecret":"default-password"`) || !strings.Contains(out.Body.String(), `"timeoutMinutes":30`) {
		t.Fatal(out.Body.String())
	}
	for _, body := range []string{`{"timeoutMinutes":0}`, `{"timeoutMinutes":241}`, `{"timeoutMinutes":15,"baseUrl":"file:///tmp/key"}`} {
		if send("PUT", "/api/agent-settings/playwright", body).Code != 400 {
			t.Fatal("invalid settings accepted")
		}
	}
	if send("GET", "/api/agent-settings/unknown", "").Code != 404 {
		t.Fatal("unknown agent accepted")
	}
	out = send("PUT", "/api/agent-settings/playwright", `{"timeoutMinutes":15,"testSecret":""}`)
	if out.Code != 200 || strings.Contains(out.Body.String(), "default-password") {
		t.Fatal("clear failed")
	}
}

func TestAgentRuntimeEditsWholeFileAndDetectsConflict(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "setting.json")
	original := `{"model":"original","env":{"ANTHROPIC_API_KEY":"original-key","CUSTOM":"value"},"permissions":{"deny":["Bash"]},"extra":{"future":true}}`
	if err := os.WriteFile(file, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CASEHUB_PLANNER_SETTINGS", file)
	handler := routes(&api{service: core.NewService(store.NewMemory())})
	send := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/api/agent-settings/playwright/runtime", strings.NewReader(body)))
		return w
	}
	get := send("GET", "")
	var snapshot struct {
		Content, Revision string
		Exists            bool
	}
	if err := json.Unmarshal(get.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Content != original || !snapshot.Exists {
		t.Fatal("original file not prefilled")
	}
	changed := strings.Replace(original, "original-key", "new-key", 1)
	body, _ := json.Marshal(map[string]string{"content": changed, "revision": snapshot.Revision})
	if out := send("PUT", string(body)); out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	actual, err := os.ReadFile(file)
	if err != nil || string(actual) != changed {
		t.Fatal("file not updated exactly")
	}
	if send("PUT", string(body)).Code != 409 {
		t.Fatal("stale write accepted")
	}
	for _, content := range []string{`[]`, `null`, `{"env":{"API_KEY":1}}`, `{"model":""}`, `{"env":{"KEY":null}}`, `bad json`} {
		body, _ = json.Marshal(map[string]string{"content": content, "revision": settingsRevision(actual)})
		if send("PUT", string(body)).Code != 400 {
			t.Fatalf("invalid settings accepted: %s", content)
		}
	}
	// A symlink remains a symlink, and saves target the actual shared file.
	link := filepath.Join(dir, "link.json")
	if err = os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	f := newAgentSettingsFile(link)
	r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"content":"{}","revision":"`+settingsRevision(actual)+`"}`))
	r.SetPathValue("id", "playwright")
	w := httptest.NewRecorder()
	f.serveHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err = os.Readlink(link); err != nil {
		t.Fatal("symlink replaced")
	}
	actual, _ = os.ReadFile(file)
	if string(actual) != "{}" {
		t.Fatal("symlink target not updated")
	}
	// Missing files can be created without fabricating existing configuration.
	f = newAgentSettingsFile(filepath.Join(dir, "new", "setting.json"))
	r = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"content":"{}","revision":"`+settingsRevision([]byte("{}"))+`"}`))
	r.SetPathValue("id", "playwright")
	w = httptest.NewRecorder()
	f.serveHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

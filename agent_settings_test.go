package main

import (
	"casehub/internal/core"
	"casehub/internal/store"
	"context"
	"encoding/json"
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
	// Each agent is configured on its own; the generator does not inherit the planner's defaults.
	if out = send("GET", "/api/agent-settings/generator", ""); out.Code != 200 || strings.Contains(out.Body.String(), "target.test") {
		t.Fatal(out.Body.String())
	}
	for _, body := range []string{`{"timeoutMinutes":0}`, `{"timeoutMinutes":241}`, `{"timeoutMinutes":15,"baseUrl":"file:///tmp/key"}`} {
		if send("PUT", "/api/agent-settings/playwright", body).Code != 400 {
			t.Fatal("invalid settings accepted")
		}
	}
	if send("GET", "/api/agent-settings/unknown", "").Code != 404 || send("GET", "/api/agent-settings/unknown/runtime", "").Code != 404 {
		t.Fatal("unknown agent accepted")
	}
	out = send("PUT", "/api/agent-settings/playwright", `{"timeoutMinutes":15,"testSecret":""}`)
	if out.Code != 200 || strings.Contains(out.Body.String(), "default-password") {
		t.Fatal("clear failed")
	}
	// The configuration lives in its own document, not inside the state the frontend loads.
	state, err := os.ReadFile(filepath.Join(filepath.Dir(file), "state-agents.json"))
	if err != nil || !strings.Contains(string(state), `"playwright"`) {
		t.Fatalf("agent configuration not stored separately: %v %s", err, state)
	}
	if body := send("GET", "/api/state", "").Body.String(); strings.Contains(body, "agentSettings") {
		t.Fatal("agent configuration must not travel with the state document")
	}
}

// Business defaults saved before the configuration moved into its own table are carried over.
func TestLegacyStateDefaultsAreAdopted(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	legacy := core.State{AgentSettings: map[string]core.AgentSettings{"playwright": {BaseURL: "https://legacy.test", TimeoutMinutes: 45}}}
	repo := store.NewFile(file)
	if err := repo.Save(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	cfg, err := core.NewService(repo).AgentConfig(context.Background(), "playwright")
	if err != nil || cfg.Defaults.BaseURL != "https://legacy.test" || cfg.Defaults.TimeoutMinutes != 45 {
		t.Fatalf("legacy defaults lost: %+v %v", cfg, err)
	}
	stored, err := repo.AgentConfig(context.Background(), "playwright")
	if err != nil || stored.Defaults != cfg.Defaults {
		t.Fatalf("legacy defaults not written to the agent table: %+v %v", stored, err)
	}
}

func TestAgentRuntimeSettingsAreStoredWithRevisions(t *testing.T) {
	handler := routes(&api{service: core.NewService(store.NewMemory())})
	send := func(method, agent, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/api/agent-settings/"+agent+"/runtime", strings.NewReader(body)))
		return w
	}
	read := func(agent string) (snapshot struct {
		Content, Revision string
		Exists            bool
	}) {
		t.Helper()
		if err := json.Unmarshal(send("GET", agent, "").Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	// Nothing stored yet: the agent keeps the setting.json built into its image.
	if first := read("playwright"); first.Content != "" || first.Exists || first.Revision != "0" {
		t.Fatalf("unexpected initial configuration: %+v", first)
	}
	config := `{"model":"stored","env":{"ANTHROPIC_API_KEY":"key","CUSTOM":"value"},"permissions":{"deny":["Bash"]},"extra":{"future":true}}`
	body, _ := json.Marshal(map[string]string{"content": config, "revision": "0"})
	out := send("PUT", "playwright", string(body))
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	// Stored verbatim, so fields CaseHub knows nothing about survive; the revision moves once.
	saved := read("playwright")
	if saved.Content != config || saved.Revision != "1" || !saved.Exists {
		t.Fatalf("configuration not stored verbatim: %+v", saved)
	}
	if send("PUT", "playwright", string(body)).Code != 409 {
		t.Fatal("stale write accepted")
	}
	// Saving the same text again is not a new revision: agents must not rewrite their file for nothing.
	body, _ = json.Marshal(map[string]string{"content": config, "revision": "1"})
	if out = send("PUT", "playwright", string(body)); out.Code != 200 || read("playwright").Revision != "1" {
		t.Fatalf("identical save bumped the revision: %s", out.Body.String())
	}
	for _, content := range []string{`[]`, `null`, `{"env":{"API_KEY":1}}`, `{"model":""}`, `{"env":{"KEY":null}}`, `bad json`} {
		body, _ = json.Marshal(map[string]string{"content": content, "revision": "1"})
		if send("PUT", "playwright", string(body)).Code != 400 {
			t.Fatalf("invalid settings accepted: %s", content)
		}
	}
	if send("PUT", "playwright", `{"content":"{}","revision":"not-a-number"}`).Code != 400 {
		t.Fatal("invalid revision accepted")
	}
	// The generator is configured separately and starts from nothing.
	if gen := read("generator"); gen.Revision != "0" || gen.Content != "" {
		t.Fatalf("generator inherited the planner configuration: %+v", gen)
	}
	body, _ = json.Marshal(map[string]string{"content": `{"model":"generator-model"}`, "revision": "0"})
	if send("PUT", "generator", string(body)).Code != 200 || read("playwright").Content != config {
		t.Fatal("agents share one row")
	}
}

// What the proxy sends to a service is exactly what the settings editor stored for its agent.
func TestProxySendsTheStoredConfigurationOfItsOwnAgent(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	if _, err := svc.SaveAgentSettings(context.Background(), "generator", `{"model":"generator-model"}`, 0); err != nil {
		t.Fatal(err)
	}
	revision, content, err := agentSettingsOf(svc, "generator")(context.Background())
	if err != nil || revision != "1" || content != `{"model":"generator-model"}` {
		t.Fatalf("generator proxy would send %q/%q (%v)", revision, content, err)
	}
	if revision, content, err = agentSettingsOf(svc, "planner")(context.Background()); err != nil || revision != "0" || content != "" {
		t.Fatalf("planner proxy would send the generator's configuration: %q/%q (%v)", revision, content, err)
	}
	if agentSettingsOf(svc, "healer") != nil {
		t.Fatal("a service with no agent must not inject anything")
	}
}

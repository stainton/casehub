package main

import (
	"casehub/internal/core"
	"casehub/internal/upstream"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Agent ids behind the proxied services. The frontend addresses an agent by the same id, and every
// request CaseHub forwards to a service carries the configuration stored for its agent.
var serviceAgents = map[string]string{"planner": "playwright", "generator": "generator", "general-agent": "general-agent"}

func (a *api) agentConfig(w http.ResponseWriter, r *http.Request) (core.AgentConfig, bool) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("id")
	if !core.KnownAgent(id) {
		jsonOut(w, 404, map[string]string{"error": "未知 agent"})
		return core.AgentConfig{}, false
	}
	cfg, err := a.service.AgentConfig(r.Context(), id)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": "读取 agent 配置失败"})
		return core.AgentConfig{}, false
	}
	return cfg, true
}

// Business defaults: what a new task's form is prefilled with. Saving them does not change the
// revision, so no agent rewrites its setting.json because a default URL changed.
func (a *api) agentSettings(w http.ResponseWriter, r *http.Request) {
	cfg, ok := a.agentConfig(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		jsonOut(w, 200, cfg.Defaults.WithDefaults())
		return
	}
	var in core.AgentSettings
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		jsonOut(w, 400, map[string]string{"error": "无效的业务默认配置"})
		return
	}
	saved, err := a.service.SaveAgentDefaults(r.Context(), r.PathValue("id"), in)
	if err != nil {
		jsonOut(w, 400, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, 200, saved.Defaults)
}

// The agent's own setting.json, stored as text so every field stays editable — including Claude
// options CaseHub knows nothing about. CaseHub is the only copy that matters: agents receive it with
// each request and overwrite their file when the revision moves.
func (a *api) agentRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	cfg, ok := a.agentConfig(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		jsonOut(w, 200, runtimeSettingsOut(cfg))
		return
	}
	var input struct {
		Content  string `json:"content"`
		Revision string `json:"revision"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil {
		jsonOut(w, 400, map[string]string{"error": "无效的配置请求"})
		return
	}
	revision, err := strconv.ParseInt(input.Revision, 10, 64)
	if err != nil {
		jsonOut(w, 400, map[string]string{"error": "无效的配置版本号"})
		return
	}
	saved, err := a.service.SaveAgentSettings(r.Context(), r.PathValue("id"), input.Content, revision)
	switch {
	case errors.Is(err, core.ErrRevisionConflict):
		jsonOut(w, 409, map[string]string{"error": err.Error()})
	case err != nil:
		jsonOut(w, 400, map[string]string{"error": err.Error()})
	default:
		jsonOut(w, 200, runtimeSettingsOut(saved))
	}
}

func runtimeSettingsOut(cfg core.AgentConfig) map[string]any {
	// exists=false means CaseHub has never been given a configuration for this agent: it is still
	// running on the setting.json built into its image, and nothing is sent with its requests.
	return map[string]any{"content": cfg.Settings, "revision": strconv.FormatInt(cfg.Revision, 10),
		"exists": cfg.Settings != "", "updatedAt": cfg.UpdatedAt}
}

// agentSettingsOf gives the proxy the stored configuration of the agent behind one service, so every
// forwarded request carries it. A read failure fails that request: running a task on a configuration
// CaseHub cannot vouch for would defeat the point of storing it here.
func agentSettingsOf(service *core.Service, name string) upstream.Settings {
	id, ok := serviceAgents[name]
	if !ok {
		return nil
	}
	return func(ctx context.Context) (string, string, error) {
		cfg, err := service.AgentConfig(ctx, id)
		if err != nil {
			return "", "", err
		}
		return strconv.FormatInt(cfg.Revision, 10), cfg.Settings, nil
	}
}

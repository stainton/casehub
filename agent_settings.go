package main

import (
	"casehub/internal/core"
	"encoding/json"
	"net/http"
)

func (a *api) agentSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.PathValue("id") != "playwright" {
		jsonOut(w, 404, map[string]string{"error": "未知 agent"})
		return
	}
	if r.Method == http.MethodGet {
		st, err := a.service.State(r.Context())
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": "读取配置失败"})
			return
		}
		jsonOut(w, 200, st.AgentSettings["playwright"].WithDefaults())
		return
	}
	var in core.AgentSettings
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		jsonOut(w, 400, map[string]string{"error": "无效的业务默认配置"})
		return
	}
	out, err := a.service.SaveAgentSettings(r.Context(), "playwright", in)
	if err != nil {
		jsonOut(w, 400, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, 200, out)
}

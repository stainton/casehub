// Package upstream reverse-proxies CaseHub to the auto-test HTTP services: the
// planner behind 需求管理's "AI 设计" drawer and the generator behind 用例管理's
// "脚本生成" drawer. CaseHub does not understand job/SSE semantics for either; it
// forwards requests and streamed progress directly. The two services are separate
// processes and are configured, enabled and reached independently.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Settings reads the configuration CaseHub stores for this service's agent: the setting.json text and
// the revision identifying it. CaseHub sends it with every request that starts work, and the agent
// overwrites its own file when the revision changed — so an agent needs no configuration of its own and
// no shared volume, and a redeployed one is corrected by the next task instead of by hand. An empty
// content means CaseHub has none and the agent keeps what its image shipped.
type Settings func(ctx context.Context) (revision, content string, err error)

// Bodies above this size are forwarded untouched; the services' own limit is smaller.
const maxInjectBytes = 8 << 20

func injectSettings(req *http.Request, settings Settings) error {
	revision, content, err := settings(req.Context())
	if err != nil {
		return err
	}
	if content == "" || req.Body == nil || req.ContentLength > maxInjectBytes {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxInjectBytes+1))
	_ = req.Body.Close()
	if err != nil {
		return err
	}
	var payload map[string]json.RawMessage
	// A body the service will reject anyway (too large, not an object) is forwarded exactly as sent, so
	// the service's own error reaches the caller instead of one invented here.
	if len(body) <= maxInjectBytes && json.Unmarshal(body, &payload) == nil && payload != nil {
		payload["agentSettings"], err = json.Marshal(map[string]string{"revision": revision, "content": content})
		if err != nil {
			return err
		}
		if next, err := json.Marshal(payload); err == nil {
			body = next
		}
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Del("Content-Length")
	return nil
}

// New builds the handler for /api/<service>/, forwarding to <baseURL>/v1/<service>/.
// When baseURL is empty the feature is disabled and every request gets a 503 in
// the same Error shape the services themselves use, so the frontend has one error
// format to handle regardless of which side rejected the request.
func New(baseURL, service string, settings Settings) (http.Handler, bool) {
	code := strings.ToUpper(service)
	disabled := func(w http.ResponseWriter, _ *http.Request) {
		errorJSON(w, http.StatusServiceUnavailable, code+"_DISABLED", disabledMessage(service))
	}
	if strings.TrimSpace(baseURL) == "" {
		return http.HandlerFunc(disabled), false
	}
	target, err := url.Parse(baseURL)
	if err != nil {
		return http.HandlerFunc(disabled), false
	}
	apiPrefix, servicePrefix := "/api/"+service, "/v1/"+service
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			req.URL.Path = servicePrefix + strings.TrimPrefix(req.URL.Path, apiPrefix)
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			errorJSON(w, http.StatusBadGateway, code+"_UNREACHABLE", unreachableMessage(service)+err.Error())
		},
	}
	if settings == nil {
		return rp, true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := injectSettings(r, settings); err != nil {
				errorJSON(w, http.StatusInternalServerError, code+"_SETTINGS_UNAVAILABLE",
					"无法读取 "+service+" 的 agent 配置，请稍后重试："+err.Error())
				return
			}
		}
		rp.ServeHTTP(w, r)
	}), true
}

func errorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func disabledMessage(service string) string {
	if service == "generator" {
		return "脚本生成服务未配置（缺少 CASEHUB_GENERATOR_URL）"
	}
	return "AI 设计服务未配置（缺少 CASEHUB_PLANNER_URL）"
}

func unreachableMessage(service string) string {
	if service == "generator" {
		return "脚本生成服务不可用："
	}
	return "AI 设计服务不可用："
}

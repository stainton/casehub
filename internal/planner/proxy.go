// Package planner reverse-proxies CaseHub's requirement-management "AI 设计"
// drawer to the auto-test planner HTTP service. CaseHub does not understand
// job/SSE semantics here; it only injects the service bearer token so the
// browser never holds it, as the planner service's own README requires.
package planner

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// New builds the handler for /api/planner/. When baseURL is empty the
// feature is disabled and every request gets a 503 in the same Error shape
// the planner service itself uses, so the frontend has one error format to
// handle regardless of which side rejected the request.
func New(baseURL, token string) (http.Handler, bool) {
	if strings.TrimSpace(baseURL) == "" {
		return http.HandlerFunc(disabled), false
	}
	target, err := url.Parse(baseURL)
	if err != nil {
		return http.HandlerFunc(disabled), false
	}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			req.URL.Path = "/v1/planner" + strings.TrimPrefix(req.URL.Path, "/api/planner")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			} else {
				req.Header.Del("Authorization")
			}
		},
		FlushInterval: -1,
		ErrorHandler:  unreachable,
	}
	return rp, true
}

func errorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func disabled(w http.ResponseWriter, r *http.Request) {
	errorJSON(w, http.StatusServiceUnavailable, "PLANNER_DISABLED", "AI 设计服务未配置（缺少 CASEHUB_PLANNER_URL）")
}

func unreachable(w http.ResponseWriter, r *http.Request, err error) {
	errorJSON(w, http.StatusBadGateway, "PLANNER_UNREACHABLE", "AI 设计服务不可用："+err.Error())
}

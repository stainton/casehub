// Package upstream reverse-proxies CaseHub to the auto-test HTTP services: the
// planner behind 需求管理's "AI 设计" drawer and the generator behind 用例管理's
// "脚本生成" drawer. CaseHub does not understand job/SSE semantics for either; it
// forwards requests and streamed progress directly. The two services are separate
// processes and are configured, enabled and reached independently.
package upstream

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// New builds the handler for /api/<service>/, forwarding to <baseURL>/v1/<service>/.
// When baseURL is empty the feature is disabled and every request gets a 503 in
// the same Error shape the services themselves use, so the frontend has one error
// format to handle regardless of which side rejected the request.
func New(baseURL, service string) (http.Handler, bool) {
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
	return rp, true
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

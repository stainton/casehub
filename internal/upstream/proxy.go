// Package upstream reverse-proxies CaseHub to the auto-test HTTP services: the
// planner behind 需求管理's "AI 设计" drawer, the generator behind 用例管理's
// "脚本生成" drawer, and general-agent for shared model work. CaseHub does not understand job/SSE semantics for them; it
// forwards requests and streamed progress directly. The two services are separate
// processes and are configured, enabled and reached independently.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// AssetRef is what a task carries about a file: never the bytes and never an address to fetch them from.
// The bytes are pushed to the agent (see push in New) before the task is forwarded, addressed by SHA256,
// so the agent needs no way to reach CaseHub and no access to its database.
type AssetRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	MimeType string `json:"mimeType"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// Assets is CaseHub's side of that: Lookup resolves the ids the AI 设计 drawer sent (context.assetIds)
// into refs, dropping ids that no longer exist (a deleted-in-the-meantime asset must not block a task
// that still lists it), and Open streams one asset's bytes for the push.
type Assets struct {
	Lookup func(ctx context.Context, ids []string) ([]AssetRef, error)
	Open   func(ctx context.Context, id string) (io.ReadCloser, error)
}

// Bodies above this size are forwarded untouched; the services' own limit is smaller.
const maxInjectBytes = 8 << 20

// invalidAssetIDsError marks context.assetIds being malformed as the caller's fault (400), as opposed
// to every other failure here (a settings/asset lookup that could not run) being ours (500).
type invalidAssetIDsError struct{ error }

// pushError marks the agent being unable to take an asset (down, too old to have the endpoint, out of
// space), reported as a bad gateway rather than a CaseHub fault.
type pushError struct{ error }

// readJSONBody reads and restores the request body, decoding it as a JSON object when possible. ok is
// false for anything this has no business rewriting (no body, too large, not an object) — the service's
// own validation is left to reject that request exactly as sent.
func readJSONBody(r *http.Request) (map[string]json.RawMessage, bool, error) {
	if r.Body == nil || r.ContentLength > maxInjectBytes {
		return nil, false, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInjectBytes+1))
	_ = r.Body.Close()
	if err != nil {
		return nil, false, err
	}
	restore := func() { r.Body = io.NopCloser(bytes.NewReader(body)); r.ContentLength = int64(len(body)) }
	restore()
	if len(body) > maxInjectBytes {
		return nil, false, nil
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || payload == nil {
		return nil, false, nil
	}
	return payload, true, nil
}
func writeJSONBody(r *http.Request, payload map[string]json.RawMessage) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Content-Length")
	return nil
}

// applyAssetIDs replaces context.assetIds (a list of ids the drawer sent) with context.assets (the
// resolved refs above), so the outgoing request only ever carries a field the service's own contract
// knows: assetIds is a CaseHub-only convention and must never reach the agent.
func applyAssetIDs(ctx context.Context, payload map[string]json.RawMessage, assets *Assets, push func(context.Context, Assets, AssetRef) error) error {
	if assets == nil {
		return nil
	}
	raw, ok := payload["context"]
	if !ok {
		return nil
	}
	var context map[string]json.RawMessage
	// Not an object: leave it alone and let the service reject the request as malformed itself.
	if json.Unmarshal(raw, &context) != nil || context == nil {
		return nil
	}
	idsRaw, ok := context["assetIds"]
	if !ok {
		return nil
	}
	var ids []string
	if json.Unmarshal(idsRaw, &ids) != nil {
		return invalidAssetIDsError{errors.New("context.assetIds 必须是字符串数组")}
	}
	delete(context, "assetIds")
	if len(ids) > 0 {
		refs, err := assets.Lookup(ctx, ids)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := push(ctx, *assets, ref); err != nil {
				return pushError{err}
			}
		}
		if len(refs) > 0 {
			encoded, err := json.Marshal(refs)
			if err != nil {
				return err
			}
			context["assets"] = encoded
		}
	}
	encoded, err := json.Marshal(context)
	if err != nil {
		return err
	}
	payload["context"] = encoded
	return nil
}
func applyAgentSettings(ctx context.Context, payload map[string]json.RawMessage, settings Settings) error {
	if settings == nil {
		return nil
	}
	revision, content, err := settings(ctx)
	if err != nil {
		return err
	}
	if content == "" {
		return nil
	}
	encoded, err := json.Marshal(map[string]string{"revision": revision, "content": content})
	if err != nil {
		return err
	}
	payload["agentSettings"] = encoded
	return nil
}

// New builds the handler for /api/<service>/, forwarding to <baseURL>/v1/<service>/.
// When baseURL is empty the feature is disabled and every request gets a 503 in
// the same Error shape the services themselves use, so the frontend has one error
// format to handle regardless of which side rejected the request.
func New(baseURL, service string, settings Settings, assets *Assets) (http.Handler, bool) {
	code := strings.ReplaceAll(strings.ToUpper(service), "-", "_")
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
	// push makes sure the agent holds one asset before a task names it: HEAD asks whether its cache already
	// has that hash (the same file is never sent twice), PUT streams the bytes otherwise.
	client := &http.Client{}
	push := func(ctx context.Context, source Assets, ref AssetRef) error {
		u := *target
		u.Path, u.RawQuery = "/v1/"+service+"/assets/"+ref.SHA256, ""
		head, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(head)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		body, err := source.Open(ctx, ref.ID)
		if err != nil {
			return err
		}
		defer body.Close()
		put, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), body)
		if err != nil {
			return err
		}
		put.ContentLength = ref.Size
		put.Header.Set("Content-Type", "application/octet-stream")
		resp, err = client.Do(put)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return errors.New(ref.Name + "：" + service + " 拒绝接收资产（HTTP " + resp.Status + "），服务可能需要升级")
		}
		return nil
	}
	if settings == nil && assets == nil {
		return rp, true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			payload, ok, err := readJSONBody(r)
			if err != nil {
				errorJSON(w, http.StatusInternalServerError, code+"_REQUEST_UNAVAILABLE", "无法读取请求体："+err.Error())
				return
			}
			if ok {
				if err := applyAssetIDs(r.Context(), payload, assets, push); err != nil {
					var bad invalidAssetIDsError
					var pushFailed pushError
					if errors.As(err, &bad) {
						errorJSON(w, http.StatusBadRequest, code+"_INVALID_REQUEST", err.Error())
					} else if errors.As(err, &pushFailed) {
						errorJSON(w, http.StatusBadGateway, code+"_ASSETS_UNAVAILABLE", unreachableMessage(service)+"无法把资产交给它："+err.Error())
					} else {
						errorJSON(w, http.StatusInternalServerError, code+"_ASSETS_UNAVAILABLE", "无法读取所选资产，请稍后重试："+err.Error())
					}
					return
				}
				if err := applyAgentSettings(r.Context(), payload, settings); err != nil {
					errorJSON(w, http.StatusInternalServerError, code+"_SETTINGS_UNAVAILABLE",
						"无法读取 "+service+" 的 agent 配置，请稍后重试："+err.Error())
					return
				}
				if err := writeJSONBody(r, payload); err != nil {
					errorJSON(w, http.StatusInternalServerError, code+"_REQUEST_UNAVAILABLE", "无法编码请求体："+err.Error())
					return
				}
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
	if service == "general-agent" {
		return "通用 AI 服务未配置（缺少 CASEHUB_GENERAL_AGENT_URL）"
	}
	if service == "executor" {
		return "脚本执行服务未配置（缺少 CASEHUB_EXECUTOR_URL）"
	}
	return "AI 设计服务未配置（缺少 CASEHUB_PLANNER_URL）"
}

func unreachableMessage(service string) string {
	if service == "generator" {
		return "脚本生成服务不可用："
	}
	if service == "general-agent" {
		return "通用 AI 服务不可用："
	}
	if service == "executor" {
		return "脚本执行服务不可用："
	}
	return "AI 设计服务不可用："
}

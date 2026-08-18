package app

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/stainton/casehub/cmd/api/app/store"
	"github.com/stainton/casehub/pkg/api"
	"github.com/stainton/casehub/pkg/model"
)

type HttpServer struct {
	store store.Store
}

// NewAPIMux 构建 case API 路由以及一个 "GET /" 健康检查（用于 pod 的 readinessProbe/livenessProbe）。
// case API 不对外暴露，唯一的调用者是 manager（见 cmd/manager/app/client），
// 通过网络访问这里，而不是直接 import 这个包。
func NewAPIMux(st store.Store) *http.ServeMux {
	mux := http.NewServeMux()
	s := &HttpServer{store: st}
	mux.HandleFunc("GET /", s.healthz)
	mux.HandleFunc(api.RouteCreateCase, s.postCase)
	mux.HandleFunc(api.RouteBatchCreateCases, s.postCasesBatch)
	mux.HandleFunc(api.RouteListCases, s.getCases)
	mux.HandleFunc(api.RouteGetCase, s.getCase)
	mux.HandleFunc(api.RouteGetCaseHistory, s.getCaseHistoryHandler)
	mux.HandleFunc(api.RouteCreateExecution, s.postExecution)
	mux.HandleFunc(api.RouteListExecutions, s.getExecutionsHandler)
	return mux
}

// Serve 没有加 CORS：case API 不对外暴露，唯一的调用者是 manager，服务端到
// 服务端的调用不受同源策略限制，需要 CORS 的是 manager（见 cmd/manager/app/svr.go）。
func Serve(opts *Options, mux *http.ServeMux) error {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(opts.httpPort))
	if err != nil {
		return err
	}
	log.Printf("HTTP server listening on %s", listener.Addr())
	return http.Serve(listener, mux)
}

// healthz 健康检查探针
func (s *HttpServer) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// postCase 处理单用例的创建/编辑，创建默认使用 revision=1
// 编辑时，revision 必须大于当前 revision，且必须是连续的（不能跳过中间的 revision）。
func (s *HttpServer) postCase(w http.ResponseWriter, r *http.Request) {
	var tc model.TestCase
	if err := json.NewDecoder(r.Body).Decode(&tc); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if tc.CaseID == "" {
		http.Error(w, "case_id is required", http.StatusBadRequest)
		return
	}

	if err := s.store.UpsertTestCase(r.Context(), &tc); err != nil {
		http.Error(w, "Failed to save case: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, tc)
}

// postCasesBatch 处理批量导入。每个用例独立进行更新插入（遵循与 postCase 相同的规则）
// 因此一个错误的条目不会阻止其余条目的处理。
func (s *HttpServer) postCasesBatch(w http.ResponseWriter, r *http.Request) {
	var cases []*model.TestCase
	if err := json.NewDecoder(r.Body).Decode(&cases); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	errs := s.store.InsertTestCases(r.Context(), cases)
	results := make([]api.BatchCreateResult, len(cases))
	for i, tc := range cases {
		results[i] = api.BatchCreateResult{Index: i, OK: errs[i] == nil}
		if errs[i] != nil {
			results[i].Error = errs[i].Error()
		} else {
			results[i].Case = tc
		}
	}

	writeJSON(w, http.StatusOK, results)
}

// getCases 筛选获得某些用例的最新版本
// e.g. GET /api/cases?case_name=login&state=active.
func (s *HttpServer) getCases(w http.ResponseWriter, r *http.Request) {
	filters := map[string]string{}
	for _, key := range api.QueryCaseFields {
		if v := r.URL.Query().Get(key); v != "" {
			filters[key] = v
		}
	}

	cases, err := s.store.QueryCases(r.Context(), filters)
	if err != nil {
		http.Error(w, "Failed to query cases: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, cases)
}

// getCase 返回某个用例的最新版本
func (s *HttpServer) getCase(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	tc, err := s.store.GetCaseByUID(r.Context(), uid)
	if err != nil {
		http.Error(w, "Failed to load case: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if tc == nil {
		http.Error(w, "Case not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, tc)
}

// getCaseHistoryHandler 返回一个用例对应的所有编辑历史
func (s *HttpServer) getCaseHistoryHandler(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	history, err := s.store.GetCaseHistory(r.Context(), uid)
	if err != nil {
		http.Error(w, "Failed to load case history: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, history)
}

// postExecution 存储一条测试记录
func (s *HttpServer) postExecution(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	var body api.CreateExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Revision == 0 {
		http.Error(w, "revision is required", http.StatusBadRequest)
		return
	}
	if body.ExecutedBy == "" {
		http.Error(w, "executed_by is required", http.StatusBadRequest)
		return
	}

	te := &model.TestExecution{
		UID:        uid,
		Revision:   body.Revision,
		Content:    body.Content,
		ExecutedBy: body.ExecutedBy,
	}
	if err := s.store.InsertTestExecution(r.Context(), te); err != nil {
		http.Error(w, "Failed to save execution: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, te)
}

// getExecutionsHandler 返回一个用例某个 revision 对应的所有测试记录
func (s *HttpServer) getExecutionsHandler(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get(api.RevisionQueryParam), 10, 64)
	if err != nil {
		http.Error(w, "revision is required", http.StatusBadRequest)
		return
	}

	executions, err := s.store.GetTestExecutions(r.Context(), uid, revision)
	if err != nil {
		http.Error(w, "Failed to load executions: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, executions)
}

// writeJSON 写回 JSON 响应，设置 Content-Type 为 application/json，并写入状态码和响应体。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

package app

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/stainton/casehub/cmd/manager/app/client"
	"github.com/stainton/casehub/cmd/manager/app/store"
	"github.com/stainton/casehub/pkg/api"
	"github.com/stainton/casehub/pkg/model"
)

// rootFolderID 是 designed.md 约定的根目录 ID：FolderID=1，ParentID=0，
// FolderName="基线"，由 store/postgres 在启动时幂等创建。
const rootFolderID = 1

type HttpServer struct {
	store  store.Store
	client *client.Client
}

// NewManagerMux 构建 manager 路由以及一个 "GET /" 健康检查（用于 pod 的 readinessProbe/livenessProbe）。
func NewManagerMux(st store.Store, c *client.Client) *http.ServeMux {
	mux := http.NewServeMux()
	s := &HttpServer{store: st, client: c}
	mux.HandleFunc("GET /", s.healthz)
	registerManagerAPI(mux, s)
	return mux
}

// RegisterManagerAPI 将 manager 路由挂载到 mux 上，而不提供 "/" 处理程序，
// 供希望在根目录提供其他内容的调用者使用，例如 cmd/manager/mock，它在根目录提供前端。
func RegisterManagerAPI(mux *http.ServeMux, st store.Store, c *client.Client) {
	registerManagerAPI(mux, &HttpServer{store: st, client: c})
}

func registerManagerAPI(mux *http.ServeMux, s *HttpServer) {
	mux.HandleFunc(api.ManageAddTestCase, s.addTestCase)
	mux.HandleFunc(api.ManageUpdateTestCase, s.updateTestCase)
	mux.HandleFunc(api.ManageDeleteTestCase, s.deleteTestCase)
	mux.HandleFunc(api.ManageMigrateTestCase, s.migrateTestCase)

	mux.HandleFunc(api.ManageCreateCaseFolder, s.createCaseFolder)
	mux.HandleFunc(api.ManageUpdateCaseFolder, s.updateCaseFolder)
	mux.HandleFunc(api.ManageDeleteCaseFolder, s.deleteCaseFolder)
	mux.HandleFunc(api.ManageMigrateCaseFolder, s.migrateCaseFolder)
	mux.HandleFunc(api.ManageListCaseFolders, s.listCaseFolders)

	// case API 不对外暴露，这几个只读接口原样透传给它，前端只和 manager 打交道。
	mux.HandleFunc(api.ManageListTestCases, s.listTestCases)
	mux.HandleFunc(api.ManageGetTestCase, s.getTestCase)
	mux.HandleFunc(api.ManageGetTestCaseHistory, s.getTestCaseHistory)
	mux.HandleFunc(api.ManageRecordExecution, s.recordExecution)
	mux.HandleFunc(api.ManageListExecutions, s.listExecutions)
}

func Serve(opts *Options, mux *http.ServeMux) error {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(opts.httpPort))
	if err != nil {
		return err
	}
	log.Printf("Manager HTTP server listening on %s", listener.Addr())
	return http.Serve(listener, withCORS(mux))
}

// withCORS 允许托管在不同源的前端调用 manager
// 前端可能和 manager 分开部署，无法确定前端部署在哪里（或是否部署了前端）。
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// healthz 健康检查探针
func (s *HttpServer) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// addTestCase 处理批量新增用例：为每个用例先查 case API 确认 case_id 不重复
// （业务语义上不允许重复），不重复的才创建；创建成功的用例 UID 会被加入
// folder_id 对应目录。单个用例失败不影响其余用例的处理。
func (s *HttpServer) addTestCase(w http.ResponseWriter, r *http.Request) {
	var req model.RequestAddTestCase
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.TestCases) == 0 {
		http.Error(w, "test_cases is required", http.StatusBadRequest)
		return
	}

	folder, err := s.store.GetFolder(r.Context(), req.FolderID)
	if err != nil {
		http.Error(w, "Failed to load folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if folder == nil {
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	}

	results := make([]api.BatchCreateResult, len(req.TestCases))
	var createdUIDs []int64
	for i, tc := range req.TestCases {
		results[i] = api.BatchCreateResult{Index: i}
		if tc.CaseID == "" {
			results[i].Error = "case_id is required"
			continue
		}

		existing, err := s.client.QueryCases(r.Context(), map[string]string{"case_id": tc.CaseID})
		if err != nil {
			results[i].Error = "Failed to check case_id: " + err.Error()
			continue
		}
		if len(existing) > 0 {
			results[i].Error = "case_id already exists: " + tc.CaseID
			continue
		}

		created, err := s.client.UpsertCase(r.Context(), &tc)
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		results[i].OK = true
		results[i].Case = created
		createdUIDs = append(createdUIDs, created.UID)
	}

	if len(createdUIDs) > 0 {
		if err := s.store.AddCaseUIDs(r.Context(), req.FolderID, createdUIDs); err != nil {
			http.Error(w, "Cases created but failed to link to folder: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, results)
}

// updateTestCase 处理批量更新用例：直接调用 case API 的创建/编辑接口
// （case_id 已存在时会被视为编辑，新增一个 revision），并确保用例仍然
// 关联在 folder_id 对应的目录下。
func (s *HttpServer) updateTestCase(w http.ResponseWriter, r *http.Request) {
	var req model.RequestUpdateTestCase
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.TestCases) == 0 {
		http.Error(w, "test_cases is required", http.StatusBadRequest)
		return
	}

	folder, err := s.store.GetFolder(r.Context(), req.FolderID)
	if err != nil {
		http.Error(w, "Failed to load folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if folder == nil {
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	}

	results := make([]api.BatchCreateResult, len(req.TestCases))
	var uids []int64
	for i, tc := range req.TestCases {
		results[i] = api.BatchCreateResult{Index: i}
		if tc.CaseID == "" {
			results[i].Error = "case_id is required"
			continue
		}

		updated, err := s.client.UpsertCase(r.Context(), &tc)
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		results[i].OK = true
		results[i].Case = updated
		uids = append(uids, updated.UID)
	}

	if len(uids) > 0 {
		if err := s.store.AddCaseUIDs(r.Context(), req.FolderID, uids); err != nil {
			http.Error(w, "Cases updated but failed to link to folder: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, results)
}

// deleteTestCase 将用例从 folder_id 对应目录中移除。case API 没有提供删除
// 用例的接口（用例的编辑历史是不可变的），所以这里只是解除目录与用例的关联，
// 用例本身在 case API 中不受影响。
func (s *HttpServer) deleteTestCase(w http.ResponseWriter, r *http.Request) {
	var req model.RequestDeleteTestCase
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.TestCases) == 0 {
		http.Error(w, "test_cases is required", http.StatusBadRequest)
		return
	}

	folder, err := s.store.GetFolder(r.Context(), req.FolderID)
	if err != nil {
		http.Error(w, "Failed to load folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if folder == nil {
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	}

	results := make([]api.BatchCreateResult, len(req.TestCases))
	uids := make([]int64, len(req.TestCases))
	for i := range req.TestCases {
		uids[i] = req.TestCases[i].UID
		results[i] = api.BatchCreateResult{Index: i, OK: true, Case: &req.TestCases[i]}
	}

	if err := s.store.RemoveCaseUIDs(r.Context(), req.FolderID, uids); err != nil {
		http.Error(w, "Failed to unlink cases: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, results)
}

// migrateTestCase 将用例从 source_folder_id 迁移到 target_folder_id：
// is_copy=false 时从原目录解除关联、加入目标目录（移动）；is_copy=true 时
// 只加入目标目录、原目录保持不变（复制的是目录关联，用例数据在 case API 中
// 始终只有一份）。
func (s *HttpServer) migrateTestCase(w http.ResponseWriter, r *http.Request) {
	var req model.RequestMigrateTestCase
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.TestCases) == 0 {
		http.Error(w, "test_cases is required", http.StatusBadRequest)
		return
	}

	source, err := s.store.GetFolder(r.Context(), req.SourceFolderID)
	if err != nil {
		http.Error(w, "Failed to load source folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if source == nil {
		http.Error(w, "Source folder not found", http.StatusNotFound)
		return
	}
	target, err := s.store.GetFolder(r.Context(), req.TargetFolderID)
	if err != nil {
		http.Error(w, "Failed to load target folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if target == nil {
		http.Error(w, "Target folder not found", http.StatusNotFound)
		return
	}

	results := make([]api.BatchCreateResult, len(req.TestCases))
	uids := make([]int64, len(req.TestCases))
	for i := range req.TestCases {
		uids[i] = req.TestCases[i].UID
		results[i] = api.BatchCreateResult{Index: i, OK: true, Case: &req.TestCases[i]}
	}

	if err := s.store.AddCaseUIDs(r.Context(), req.TargetFolderID, uids); err != nil {
		http.Error(w, "Failed to link cases to target folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !req.IsCopy {
		if err := s.store.RemoveCaseUIDs(r.Context(), req.SourceFolderID, uids); err != nil {
			http.Error(w, "Cases linked to target but failed to unlink from source: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, results)
}

// createCaseFolder 在 parent_id 下创建一个新目录；parent_id 必须是一个已存在
// 的目录（根目录的 FolderID 固定是 1，所有新目录最终都挂在它下面）。
func (s *HttpServer) createCaseFolder(w http.ResponseWriter, r *http.Request) {
	var req model.RequestCreateCaseFolder
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.FolderName == "" {
		http.Error(w, "folder_name is required", http.StatusBadRequest)
		return
	}

	parent, err := s.store.GetFolder(r.Context(), req.ParentID)
	if err != nil {
		http.Error(w, "Failed to load parent folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil {
		http.Error(w, "Parent folder not found", http.StatusBadRequest)
		return
	}

	folder, err := s.store.CreateFolder(r.Context(), req.FolderName, req.ParentID)
	if err != nil {
		http.Error(w, "Failed to create folder: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, folder)
}

// updateCaseFolder 修改目录名称。
func (s *HttpServer) updateCaseFolder(w http.ResponseWriter, r *http.Request) {
	var req model.RequestUpdateCaseFolder
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.FolderName == "" {
		http.Error(w, "folder_name is required", http.StatusBadRequest)
		return
	}

	folder, err := s.store.GetFolder(r.Context(), req.FolderID)
	if err != nil {
		http.Error(w, "Failed to load folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if folder == nil {
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	}

	if err := s.store.RenameFolder(r.Context(), req.FolderID, req.FolderName); err != nil {
		http.Error(w, "Failed to rename folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	folder.FolderName = req.FolderName

	writeJSON(w, http.StatusOK, folder)
}

// deleteCaseFolder 删除一个目录：根目录不可删除，且目录必须先清空
// （没有子目录、没有关联的用例），避免子目录或用例失去归属。
func (s *HttpServer) deleteCaseFolder(w http.ResponseWriter, r *http.Request) {
	var req model.RequestDeleteCaseFolder
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.FolderID == rootFolderID {
		http.Error(w, "Root folder cannot be deleted", http.StatusBadRequest)
		return
	}

	folder, err := s.store.GetFolder(r.Context(), req.FolderID)
	if err != nil {
		http.Error(w, "Failed to load folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if folder == nil {
		http.Error(w, "Folder not found", http.StatusNotFound)
		return
	}
	if len(folder.CaseUIDs) > 0 {
		http.Error(w, "Folder is not empty", http.StatusBadRequest)
		return
	}
	hasChildren, err := s.store.HasChildren(r.Context(), req.FolderID)
	if err != nil {
		http.Error(w, "Failed to check sub-folders: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if hasChildren {
		http.Error(w, "Folder has sub-folders", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteFolder(r.Context(), req.FolderID); err != nil {
		http.Error(w, "Failed to delete folder: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, folder)
}

// migrateCaseFolder 将目录挂到新的父目录下：is_copy=false 时是移动（只改父
// 指针，子目录引用的还是被移动目录自身的 ID，整棵子树随之一起移动）；
// is_copy=true 时是复制，只复制该目录自身的名称与 CaseUIDs，不递归复制子目录。
func (s *HttpServer) migrateCaseFolder(w http.ResponseWriter, r *http.Request) {
	var req model.RequestMigrateCaseFolder
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.SourceFolderID == rootFolderID {
		http.Error(w, "Root folder cannot be migrated", http.StatusBadRequest)
		return
	}
	if req.SourceFolderID == req.TargetParentID {
		http.Error(w, "A folder cannot become its own parent", http.StatusBadRequest)
		return
	}

	source, err := s.store.GetFolder(r.Context(), req.SourceFolderID)
	if err != nil {
		http.Error(w, "Failed to load source folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if source == nil {
		http.Error(w, "Source folder not found", http.StatusNotFound)
		return
	}
	target, err := s.store.GetFolder(r.Context(), req.TargetParentID)
	if err != nil {
		http.Error(w, "Failed to load target parent folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if target == nil {
		http.Error(w, "Target parent folder not found", http.StatusNotFound)
		return
	}

	if req.IsCopy {
		copied, err := s.store.CopyFolder(r.Context(), req.SourceFolderID, req.TargetParentID)
		if err != nil {
			http.Error(w, "Failed to copy folder: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, copied)
		return
	}

	if err := s.store.MoveFolder(r.Context(), req.SourceFolderID, req.TargetParentID); err != nil {
		http.Error(w, "Failed to move folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	source.ParentID = req.TargetParentID
	writeJSON(w, http.StatusOK, source)
}

// listCaseFolders 返回全部目录（扁平列表），前端按 parent_id 在本地拼出目录树。
func (s *HttpServer) listCaseFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := s.store.ListFolders(r.Context())
	if err != nil {
		http.Error(w, "Failed to list folders: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, folders)
}

// listTestCases 透传到 case API 的用例查询，和目录无关。
func (s *HttpServer) listTestCases(w http.ResponseWriter, r *http.Request) {
	filters := map[string]string{}
	for _, key := range api.QueryCaseFields {
		if v := r.URL.Query().Get(key); v != "" {
			filters[key] = v
		}
	}

	cases, err := s.client.QueryCases(r.Context(), filters)
	if err != nil {
		http.Error(w, "Failed to query cases: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, cases)
}

// getTestCase 透传到 case API，返回某个用例的最新版本。
func (s *HttpServer) getTestCase(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	tc, err := s.client.GetCase(r.Context(), uid)
	if err != nil {
		http.Error(w, "Failed to load case: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, tc)
}

// getTestCaseHistory 透传到 case API，返回某个用例的全部编辑历史。
func (s *HttpServer) getTestCaseHistory(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	history, err := s.client.GetCaseHistory(r.Context(), uid)
	if err != nil {
		http.Error(w, "Failed to load case history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// recordExecution 透传到 case API，新增一条测试执行记录，和目录无关。
func (s *HttpServer) recordExecution(w http.ResponseWriter, r *http.Request) {
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

	te, err := s.client.CreateExecution(r.Context(), uid, body)
	if err != nil {
		http.Error(w, "Failed to save execution: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, te)
}

// listExecutions 透传到 case API，返回某个用例某个 revision 下的全部测试记录。
func (s *HttpServer) listExecutions(w http.ResponseWriter, r *http.Request) {
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

	executions, err := s.client.ListExecutions(r.Context(), uid, revision)
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

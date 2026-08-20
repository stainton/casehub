package app

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/stainton/casehub/cmd/manager/app/basecase"
	"github.com/stainton/casehub/cmd/manager/app/store"
	"github.com/stainton/casehub/pkg/api"
	"github.com/stainton/casehub/pkg/model"
)

type HttpServer struct {
	store    store.Store
	basecase *basecase.BaseCase
}

// NewManagerMux 构建 manager 路由以及一个 "GET /" 健康检查（用于 pod 的 readinessProbe/livenessProbe）。
func NewManagerMux(st store.Store, bc *basecase.BaseCase) *http.ServeMux {
	mux := http.NewServeMux()
	s := &HttpServer{store: st, basecase: bc}
	mux.HandleFunc("GET /", s.healthz)
	registerManagerAPI(mux, s)
	return mux
}

// RegisterManagerAPI 将 manager 路由挂载到 mux 上，而不提供 "/" 处理程序，
// 供希望在根目录提供其他内容的调用者使用，例如 cmd/manager/mock，它在根目录提供前端。
func RegisterManagerAPI(mux *http.ServeMux, st store.Store, bc *basecase.BaseCase) {
	registerManagerAPI(mux, &HttpServer{store: st, basecase: bc})
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

	// 基线（已合并用例）上的只读接口。
	mux.HandleFunc(api.ManageListTestCases, s.listTestCases)
	mux.HandleFunc(api.ManageGetTestCase, s.getTestCase)
	mux.HandleFunc(api.ManageGetTestCaseHistory, s.getTestCaseHistory)
	mux.HandleFunc(api.ManageListExecutions, s.listExecutions)

	// 版本分支：创建/编辑用例、记录测试、合并进基线。
	mux.HandleFunc(api.ManageCreateVersion, s.createVersion)
	mux.HandleFunc(api.ManageListVersions, s.listVersions)
	mux.HandleFunc(api.ManageMergeVersion, s.mergeVersion)
	mux.HandleFunc(api.ManagePullVersion, s.pullVersion)
	mux.HandleFunc(api.ManageListVersionTestCases, s.listVersionTestCases)
	mux.HandleFunc(api.ManageGetVersionTestCase, s.getVersionTestCase)
	mux.HandleFunc(api.ManageGetVersionTestCaseHistory, s.getVersionTestCaseHistory)
	mux.HandleFunc(api.ManageDeleteVersionTestCaseHistory, s.deleteVersionTestCaseHistory)
	mux.HandleFunc(api.ManageRecordVersionExecution, s.recordVersionExecution)
	mux.HandleFunc(api.ManageListVersionExecutions, s.listVersionExecutions)
	mux.HandleFunc(api.ManageDeleteVersionExecution, s.deleteVersionExecution)
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

// addTestCase 处理批量新增用例：所有用例的创建都发生在 req.Version 这个
// 分支里。每个用例先由基线分配 uid（NewCaseUID 靠 base_cases.case_id 的
// UNIQUE 约束保证不重名，失败就说明 case_id 已存在），再把第一条编辑记录
// 写进分支自己的 history 表；成功创建的用例 UID 会被加入 folder_id 对应
// 目录（目录归属和是否已合并进基线无关）。单个用例失败不影响其余用例。
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
	if !s.basecase.VersionExists(req.Version) {
		http.Error(w, "Version not found: "+req.Version, http.StatusBadRequest)
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
	for i := range req.TestCases {
		tc := &req.TestCases[i]
		results[i] = api.BatchCreateResult{Index: i}
		if tc.CaseID == "" {
			results[i].Error = "case_id is required"
			continue
		}

		uid, err := s.basecase.NewCaseUID(r.Context(), tc.CaseID)
		if err != nil {
			results[i].Error = "Failed to allocate uid (case_id may already exist): " + err.Error()
			continue
		}
		tc.UID = uid

		if err := s.basecase.UpsertVersionCase(r.Context(), req.Version, tc); err != nil {
			results[i].Error = err.Error()
			continue
		}
		results[i].OK = true
		results[i].Case = tc
		createdUIDs = append(createdUIDs, uid)
	}

	if len(createdUIDs) > 0 {
		if err := s.store.AddCaseUIDs(r.Context(), req.FolderID, createdUIDs); err != nil {
			http.Error(w, "Cases created but failed to link to folder: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, results)
}

// updateTestCase 处理批量更新用例：同样只能发生在 req.Version 这个分支里。
// 用例的 uid 优先用请求里带的 tc.UID；没带的话用 tc.CaseID 去基线的
// base_cases 里查（case_id 的身份在 NewCaseUID 时就已登记，和是否合并过
// 无关，所以哪怕这个用例是在同一个未合并分支里刚创建的，也能查到）。
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
	if !s.basecase.VersionExists(req.Version) {
		http.Error(w, "Version not found: "+req.Version, http.StatusBadRequest)
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
	for i := range req.TestCases {
		tc := &req.TestCases[i]
		results[i] = api.BatchCreateResult{Index: i}
		if tc.CaseID == "" {
			results[i].Error = "case_id is required"
			continue
		}

		uid := tc.UID
		if uid == 0 {
			resolved, err := s.basecase.ResolveUID(r.Context(), tc.CaseID)
			if err != nil {
				results[i].Error = "Failed to resolve case_id: " + err.Error()
				continue
			}
			uid = resolved
		}
		tc.UID = uid

		if err := s.basecase.UpsertVersionCase(r.Context(), req.Version, tc); err != nil {
			results[i].Error = err.Error()
			continue
		}
		results[i].OK = true
		results[i].Case = tc
		uids = append(uids, uid)
	}

	if len(uids) > 0 {
		if err := s.store.AddCaseUIDs(r.Context(), req.FolderID, uids); err != nil {
			http.Error(w, "Cases updated but failed to link to folder: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, results)
}

// deleteTestCase 将用例从 folder_id 对应目录中移除。用例的编辑历史是不可变
// 的，这里只是解除目录与用例的关联，用例本身不受影响。
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
// 只加入目标目录、原目录保持不变（复制的是目录关联，用例数据始终只有一份）。
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

// createCaseFolder 在 parent_id 下创建一个新目录。parent_id=0 是特例：不需要
// 一个已存在的父目录，创建的是一个新的顶层（产品级）目录——目录树里可以有
// 多个顶层目录并列存在，比如"产品A""产品B""产品C"，没有哪一个是启动时
// 写死的根。parent_id 非零时必须引用一个已存在的目录。
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

	if req.ParentID != 0 {
		parent, err := s.store.GetFolder(r.Context(), req.ParentID)
		if err != nil {
			http.Error(w, "Failed to load parent folder: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if parent == nil {
			http.Error(w, "Parent folder not found", http.StatusBadRequest)
			return
		}
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

// deleteCaseFolder 删除一个目录：顶层（产品级，ParentID=0）目录不可删除，
// 且目录必须先清空（没有子目录、没有关联的用例），避免子目录或用例失去归属。
func (s *HttpServer) deleteCaseFolder(w http.ResponseWriter, r *http.Request) {
	var req model.RequestDeleteCaseFolder
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
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
	if folder.ParentID == 0 {
		http.Error(w, "Top-level folder cannot be deleted", http.StatusBadRequest)
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
	if source.ParentID == 0 {
		http.Error(w, "Top-level folder cannot be migrated", http.StatusBadRequest)
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

// listTestCases 查询基线上已合并的最新用例，和目录无关。
func (s *HttpServer) listTestCases(w http.ResponseWriter, r *http.Request) {
	filters := map[string]string{}
	for _, key := range api.QueryCaseFields {
		if v := r.URL.Query().Get(key); v != "" {
			filters[key] = v
		}
	}

	cases, err := s.basecase.QueryCases(r.Context(), filters)
	if err != nil {
		http.Error(w, "Failed to query cases: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, cases)
}

// getTestCase 返回基线上某个用例已合并的最新版本。
func (s *HttpServer) getTestCase(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	tc, err := s.basecase.GetCase(r.Context(), uid)
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

// getTestCaseHistory 返回基线上某个用例的全部已合并编辑历史。
func (s *HttpServer) getTestCaseHistory(w http.ResponseWriter, r *http.Request) {
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	history, err := s.basecase.GetCaseHistory(r.Context(), uid)
	if err != nil {
		http.Error(w, "Failed to load case history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// listExecutions 返回基线上某个用例某个已合并 revision 下的全部测试记录。
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

	executions, err := s.basecase.GetExecutions(r.Context(), uid, revision)
	if err != nil {
		http.Error(w, "Failed to load executions: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, executions)
}

// createVersion 创建一个新的版本分支。
func (s *HttpServer) createVersion(w http.ResponseWriter, r *http.Request) {
	var req model.RequestCreateVersion
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Version == "" {
		http.Error(w, "version is required", http.StatusBadRequest)
		return
	}

	if err := s.basecase.NewVersion(r.Context(), req.Version); err != nil {
		if errors.Is(err, basecase.ErrVersionExists) {
			http.Error(w, "Version already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create version: "+err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, req)
}

// listVersions 返回当前存在的全部版本分支名称。
func (s *HttpServer) listVersions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.basecase.ListVersions())
}

// mergeVersion 把 {name} 分支里的用例合并进基线，请求体里的 uids 为空/省略
// 表示合并分支里全部被编辑过的用例。
func (s *HttpServer) mergeVersion(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")

	var req model.RequestMergeVersion
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	result, err := s.basecase.MergeVersion(r.Context(), version, req.UIDs)
	if err != nil {
		if errors.Is(err, basecase.ErrVersionNotFound) {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to merge version: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// pullVersion 把 uids 在基线上已合并的最新内容拉取进 {name} 分支，作为
// 这些用例在分支里的起始本地 revision（不拷贝任何执行记录）。单个用例
// 失败（比如基线上不存在这个 uid）不影响其余用例。
func (s *HttpServer) pullVersion(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")

	var req model.RequestPullVersion
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.UIDs) == 0 {
		http.Error(w, "uids is required", http.StatusBadRequest)
		return
	}
	if !s.basecase.VersionExists(version) {
		http.Error(w, "Version not found", http.StatusNotFound)
		return
	}

	results := make([]api.BatchCreateResult, len(req.UIDs))
	for i, uid := range req.UIDs {
		results[i] = api.BatchCreateResult{Index: i}
		tc, err := s.basecase.PullFromBase(r.Context(), version, uid)
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		results[i].OK = true
		results[i].Case = tc
	}

	writeJSON(w, http.StatusOK, results)
}

// listVersionTestCases 返回 {name} 分支里每个被编辑过的用例的最新一条记录，
// 不管合没合并过；?pending=true 时只返回还没合并过的（供合并选择器用）。
func (s *HttpServer) listVersionTestCases(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")

	var cases []*model.TestCase
	var err error
	if r.URL.Query().Get("pending") == "true" {
		cases, err = s.basecase.ListPendingVersionCases(r.Context(), version)
	} else {
		cases, err = s.basecase.ListVersionCases(r.Context(), version)
	}
	if err != nil {
		if errors.Is(err, basecase.ErrVersionNotFound) {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to list version cases: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, cases)
}

// getVersionTestCase 返回某个用例在 {name} 分支里的最新一条编辑记录。
func (s *HttpServer) getVersionTestCase(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	tc, err := s.basecase.GetVersionCase(r.Context(), version, uid)
	if err != nil {
		if errors.Is(err, basecase.ErrVersionNotFound) {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load case: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if tc == nil {
		http.Error(w, "Case not found in this version", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, tc)
}

// getVersionTestCaseHistory 返回某个用例在 {name} 分支里的全部编辑记录。
func (s *HttpServer) getVersionTestCaseHistory(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}

	history, err := s.basecase.GetVersionCaseHistory(r.Context(), version, uid)
	if err != nil {
		if errors.Is(err, basecase.ErrVersionNotFound) {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load case history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// deleteVersionTestCaseHistory 删除 {name} 分支里某个用例的一条局部编辑
// 记录，用于合并前撤销一次写错的编辑。
func (s *HttpServer) deleteVersionTestCaseHistory(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")
	uid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid case id", http.StatusBadRequest)
		return
	}
	revision, err := strconv.ParseInt(r.PathValue("revision"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid revision", http.StatusBadRequest)
		return
	}

	if err := s.basecase.DeleteVersionHistory(r.Context(), version, uid, revision); err != nil {
		if errors.Is(err, basecase.ErrVersionNotFound) {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to delete history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// recordVersionExecution 在 {name} 分支里新增一条测试执行记录，关联到
// body.revision 这个分支内的局部 revision（不是基线 revision）。
func (s *HttpServer) recordVersionExecution(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")
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

	te := &model.TestExecution{UID: uid, Revision: body.Revision, Content: body.Content, ExecutedBy: body.ExecutedBy}
	if err := s.basecase.RecordVersionExecution(r.Context(), version, te); err != nil {
		if errors.Is(err, basecase.ErrVersionNotFound) {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to save execution: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, te)
}

// listVersionExecutions 返回 {name} 分支里某个用例某个局部 revision 下的
// 全部测试记录。
func (s *HttpServer) listVersionExecutions(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")
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

	executions, err := s.basecase.ListVersionExecutions(r.Context(), version, uid, revision)
	if err != nil {
		if errors.Is(err, basecase.ErrVersionNotFound) {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load executions: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, executions)
}

// deleteVersionExecution 删除 {name} 分支里的一条执行记录。
func (s *HttpServer) deleteVersionExecution(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("name")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid execution id", http.StatusBadRequest)
		return
	}

	if err := s.basecase.DeleteVersionExecution(r.Context(), version, id); err != nil {
		if errors.Is(err, basecase.ErrVersionNotFound) {
			http.Error(w, "Version not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to delete execution: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeJSON 写回 JSON 响应，设置 Content-Type 为 application/json，并写入状态码和响应体。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// Package api 定义 manager HTTP API 的对外契约：路由表（Go 1.22+
// http.ServeMux pattern，"METHOD /path"），以及未被 pkg/model 覆盖的请求/
// 响应结构体。服务端（cmd/manager/app）和任何客户端都应基于本包构建，避免
// 两边各自维护一份、逐渐漂移。见同目录 API.md 获取完整的请求/响应字段说明。
package api

const (
	ManageAddTestCase     = "POST /api/manage/testcase"
	ManageUpdateTestCase  = "PUT /api/manage/testcase"
	ManageDeleteTestCase  = "DELETE /api/manage/testcase"
	ManageMigrateTestCase = "POST /api/manage/testcase/migrate"

	ManageCreateCaseFolder  = "POST /api/manage/casefolder"
	ManageUpdateCaseFolder  = "PUT /api/manage/casefolder"
	ManageDeleteCaseFolder  = "DELETE /api/manage/casefolder"
	ManageMigrateCaseFolder = "POST /api/manage/casefolder/migrate"

	// 基线（base_cases/case_history/test_executions）上已合并用例的只读接口，
	// 和目录无关。
	ManageListTestCases      = "GET /api/manage/testcase"
	ManageGetTestCase        = "GET /api/manage/testcase/{id}"
	ManageGetTestCaseHistory = "GET /api/manage/testcase/{id}/history"
	ManageListExecutions     = "GET /api/manage/testcase/{id}/executions"

	// ManageListCaseFolders 返回全部目录（扁平列表），前端按 parent_id 在本地拼出目录树。
	ManageListCaseFolders = "GET /api/manage/casefolder"

	// 版本分支：所有用例的新增/编辑/测试记录都只能发生在某个分支里（见
	// RequestAddTestCase/RequestUpdateTestCase 的 Version 字段），合并之后
	// 才会出现在上面的只读基线接口里。
	ManageCreateVersion = "POST /api/manage/version"
	ManageListVersions  = "GET /api/manage/version"
	ManageMergeVersion  = "POST /api/manage/version/{name}/merge"
	ManagePullVersion   = "POST /api/manage/version/{name}/pull"

	ManageListVersionTestCases         = "GET /api/manage/version/{name}/testcase"
	ManageGetVersionTestCase           = "GET /api/manage/version/{name}/testcase/{id}"
	ManageGetVersionTestCaseHistory    = "GET /api/manage/version/{name}/testcase/{id}/history"
	ManageDeleteVersionTestCaseHistory = "DELETE /api/manage/version/{name}/testcase/{id}/history/{revision}"
	ManageRecordVersionExecution       = "POST /api/manage/version/{name}/testcase/{id}/executions"
	ManageListVersionExecutions        = "GET /api/manage/version/{name}/testcase/{id}/executions"
	ManageDeleteVersionExecution       = "DELETE /api/manage/version/{name}/testcase/executions/{id}"
)

// QueryCaseFields 列出了 ManageListTestCases 接受的查询参数；服务端原样将其
// 透传给 store 做匹配。
var QueryCaseFields = []string{"case_id", "case_name", "priority", "state", "description"}

// RevisionQueryParam 是 ManageListExecutions/ManageListVersionExecutions 必填的查询参数名。
const RevisionQueryParam = "revision"

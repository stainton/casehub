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

	// case API 不再对外暴露，前端只和 manager 交互，所以 manager 还需要把 case
	// API 的只读接口（查询/详情/历史/测试记录）原样透传出去 —— 这几个和目录
	// 无关，manager 只是转发，不做额外处理。
	ManageListTestCases      = "GET /api/manage/testcase"
	ManageGetTestCase        = "GET /api/manage/testcase/{id}"
	ManageGetTestCaseHistory = "GET /api/manage/testcase/{id}/history"
	ManageRecordExecution    = "POST /api/manage/testcase/{id}/executions"
	ManageListExecutions     = "GET /api/manage/testcase/{id}/executions"

	// ManageListCaseFolders 返回全部目录（扁平列表），前端按 parent_id 在本地拼出目录树。
	ManageListCaseFolders = "GET /api/manage/casefolder"
)

// Package api 定义 case 管理 HTTP API 的对外契约：路由表，以及未被
// pkg/model 覆盖的请求/响应结构体。服务端（cmd/api/internal/app）和任何
// 客户端（例如 manager）都应基于本包构建，以避免两边各自维护一份、逐渐漂移。
package api

// 路由表，格式为 Go 1.22+ http.ServeMux 的 pattern（"METHOD /path"）。
const (
	RouteCreateCase       = "POST /api/cases"
	RouteBatchCreateCases = "POST /api/cases/batch"
	RouteListCases        = "GET /api/cases"
	RouteGetCase          = "GET /api/cases/{id}"
	RouteGetCaseHistory   = "GET /api/cases/{id}/history"
	RouteCreateExecution  = "POST /api/cases/{id}/executions"
	RouteListExecutions   = "GET /api/cases/{id}/executions"
)

// QueryCaseFields 列出了 RouteListCases 接受的查询参数；服务端原样将其
// 透传给 store 做匹配。
var QueryCaseFields = []string{"case_id", "case_name", "priority", "state", "description"}

// RevisionQueryParam 是 RouteListExecutions 必填的查询参数名。
const RevisionQueryParam = "revision"

package api

import "github.com/stainton/casehub/pkg/model"

// BatchCreateResult 是 RouteBatchCreateCases 响应体中的一项，报告请求体中
// 对应下标的用例的处理结果。
type BatchCreateResult struct {
	Index int             `json:"index"`
	OK    bool            `json:"ok"`
	Case  *model.TestCase `json:"case,omitempty"`
	Error string          `json:"error,omitempty"`
}

// CreateExecutionRequest 是 RouteCreateExecution 的请求体。
type CreateExecutionRequest struct {
	Revision   int64  `json:"revision"`
	Content    string `json:"content"`
	ExecutedBy string `json:"executed_by"`
}

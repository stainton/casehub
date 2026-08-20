package api

import "github.com/stainton/casehub/pkg/model"

// BatchCreateResult 是 ManageAddTestCase/ManageUpdateTestCase/ManagePullVersion
// 等批量接口响应体中的一项，报告请求体中对应下标的用例的处理结果。
type BatchCreateResult struct {
	Index int             `json:"index"`
	OK    bool            `json:"ok"`
	Case  *model.TestCase `json:"case,omitempty"`
	Error string          `json:"error,omitempty"`
}

// CreateExecutionRequest 是 ManageRecordVersionExecution 的请求体。
type CreateExecutionRequest struct {
	Revision   int64  `json:"revision"`
	Content    string `json:"content"`
	ExecutedBy string `json:"executed_by"`
}

// MergeConflict 描述 ManageMergeVersion 中一个用例为什么没能合并进基线。
type MergeConflict struct {
	UID    int64  `json:"uid"`
	CaseID string `json:"case_id"`
	Reason string `json:"reason"`
}

// MergeResult 是 ManageMergeVersion 的响应体：请求合并的每个 uid 要么进了
// Merged，要么带着原因进了 Conflicts；分支里根本没有编辑记录的 uid 会被
// 跳过，两边都不会出现。
type MergeResult struct {
	Merged    []int64         `json:"merged"`
	Conflicts []MergeConflict `json:"conflicts,omitempty"`
}

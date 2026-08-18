// Package store defines the persistence abstraction that cmd/api/app/app
// depends on, keeping casehub decoupled from any specific database. It is
// scoped to the tables the case API owns (base_cases, case_history,
// test_executions); other components (e.g. manager) that need their own
// tables should define their own persistence abstraction rather than reuse
// this package — it lives under cmd/api's app tree and isn't importable
// from outside it. store/postgres provides the Postgres implementation;
// alternate backends can add their own package alongside it without touching
// cmd/api/app/app's business logic.
package store

import (
	"context"

	"github.com/stainton/casehub/pkg/model"
)

// Store persists test cases, their edit history, and execution reports.
type Store interface {
	// Close releases the underlying resources (connections, pools, etc).
	Close()

	// UpsertTestCase 新增用例的编辑记录，新增后同时更新指向最新版本的指针。
	UpsertTestCase(ctx context.Context, tc *model.TestCase) error
	// InsertTestCases 批量调用 UpsertTestCase 新增测试用例，返回与入参一一对应的错误切片。
	InsertTestCases(ctx context.Context, cases []*model.TestCase) []error
	// GetCaseByUID 根据 uid 查询单个用例的最新版本，不存在时返回 (nil, nil)。
	GetCaseByUID(ctx context.Context, uid int64) (*model.TestCase, error)
	// GetCaseHistory 根据 uid 查询单个用例的全部版本。
	GetCaseHistory(ctx context.Context, uid int64) ([]*model.TestCase, error)
	// QueryCases 查询最新版本的测试用例，返回符合过滤条件的所有测试用例。
	QueryCases(ctx context.Context, filters map[string]string) ([]*model.TestCase, error)

	// InsertTestExecution 新增一条测试执行记录。
	InsertTestExecution(ctx context.Context, te *model.TestExecution) error
	// GetTestExecutions 查询某个用例(uid+revision)的全部执行记录。
	GetTestExecutions(ctx context.Context, uid, revision int64) ([]*model.TestExecution, error)
}

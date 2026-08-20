package basecase

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/stainton/casehub/pkg/model"
)

// ResolveUID 通过 case_id 查找它在基线上的 uid。case_id 的身份从
// NewCaseUID 分配时起就已经登记在 base_cases 里，和这个用例是否合并过
// 无关，所以这是找一个用例 uid 的权威方式（哪怕它是在某个还没合并的分支
// 里刚创建的）。
func (b *BaseCase) ResolveUID(ctx context.Context, caseID string) (int64, error) {
	var uid int64
	err := b.pool.QueryRow(ctx, SelectUIDByCaseID, caseID).Scan(&uid)
	return uid, err
}

// GetCase 返回基线上某个用例已合并的最新版本，不存在（或还没合并过）时
// 返回 (nil, nil)。
func (b *BaseCase) GetCase(ctx context.Context, uid int64) (*model.TestCase, error) {
	tc, err := scanTestCase(b.pool.QueryRow(ctx, SelectLatestCaseByUID, uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return tc, nil
}

// GetCaseHistory 返回基线上某个用例的全部已合并版本，revision 从新到旧。
func (b *BaseCase) GetCaseHistory(ctx context.Context, uid int64) ([]*model.TestCase, error) {
	rows, err := b.pool.Query(ctx, SelectCaseHistoryByUID, uid)
	if err != nil {
		return nil, err
	}
	return scanTestCases(rows)
}

// queryableCaseFields maps the query params accepted by QueryCases to the
// case_history column they filter on, whether that filter is a partial
// (ILIKE) or exact match, and whether the column is integer-typed (so the
// filter value must be sent as an int, not a string, or pgx/Postgres will
// reject it with "operator does not exist: integer = text").
var queryableCaseFields = map[string]struct {
	column  string
	ilike   bool
	numeric bool
}{
	"case_id":     {column: "ch.case_id"},
	"case_name":   {column: "ch.case_name", ilike: true},
	"priority":    {column: "ch.priority", numeric: true},
	"state":       {column: "ch.state"},
	"description": {column: "ch.description", ilike: true},
}

// QueryCases 查询基线上最新（已合并）版本的测试用例，返回符合过滤条件的
// 全部结果。只处理 queryableCaseFields 中存在的键，未知键会被忽略；过滤值
// 总是作为查询参数传递，不会拼接到 SQL 文本中。
func (b *BaseCase) QueryCases(ctx context.Context, filters map[string]string) ([]*model.TestCase, error) {
	var (
		conditions []string
		args       []any
	)
	for key, value := range filters {
		field, ok := queryableCaseFields[key]
		if !ok || value == "" {
			continue
		}
		if field.numeric {
			n, err := strconv.Atoi(value)
			if err != nil {
				continue
			}
			args = append(args, n)
		} else {
			args = append(args, value)
		}
		if field.ilike {
			conditions = append(conditions, fmt.Sprintf("%s ILIKE '%%' || $%d || '%%'", field.column, len(args)))
		} else {
			conditions = append(conditions, fmt.Sprintf("%s = $%d", field.column, len(args)))
		}
	}

	query := SelectLatestCases
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanTestCases(rows)
}

// GetExecutions 返回基线上某个用例 (uid+revision) 的全部执行记录。
func (b *BaseCase) GetExecutions(ctx context.Context, uid, revision int64) ([]*model.TestExecution, error) {
	rows, err := b.pool.Query(ctx, SelectTestExecutions, uid, revision)
	if err != nil {
		return nil, err
	}
	return scanTestExecutions(rows)
}

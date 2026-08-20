package basecase

import (
	"github.com/jackc/pgx/v5"
	"github.com/stainton/casehub/pkg/model"
)

// scanTestCase 按 caseHistoryColumns/selectLatestCasesColumns 的列顺序把一行
// 扫描到 model.TestCase：两者列顺序完全一致，共用同一个扫描函数。
func scanTestCase(row pgx.Row) (*model.TestCase, error) {
	tc := &model.TestCase{}
	err := row.Scan(
		&tc.ID, &tc.UID, &tc.CaseID, &tc.CaseName, &tc.Priority, &tc.Description,
		&tc.PreCondition, &tc.Steps, &tc.ExpectedResult, &tc.State, &tc.Revision, &tc.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return tc, nil
}

func scanTestCases(rows pgx.Rows) ([]*model.TestCase, error) {
	defer rows.Close()
	var out []*model.TestCase
	for rows.Next() {
		tc, err := scanTestCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

func scanTestExecution(row pgx.Row) (*model.TestExecution, error) {
	te := &model.TestExecution{}
	if err := row.Scan(&te.ID, &te.UID, &te.Revision, &te.Content, &te.ExecutedBy, &te.ExecutedAt); err != nil {
		return nil, err
	}
	return te, nil
}

func scanTestExecutions(rows pgx.Rows) ([]*model.TestExecution, error) {
	defer rows.Close()
	var out []*model.TestExecution
	for rows.Next() {
		te, err := scanTestExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, te)
	}
	return out, rows.Err()
}

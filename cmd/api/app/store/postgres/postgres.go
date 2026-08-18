// Package postgres is the Postgres implementation of store.Store.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stainton/casehub/cmd/api/app/store"
	"github.com/stainton/casehub/pkg/model"
)

var _ store.Store = (*Store)(nil)

// Config holds everything needed to connect to and bootstrap the database.
type Config struct {
	// ConnString connects to DBName directly.
	ConnString string
	// AdminConnString connects to the "postgres" maintenance database, used
	// to create DBName before it exists.
	AdminConnString string
	DBName          string
}

// Store is the Postgres-backed store.Store implementation.
type Store struct {
	pool *pgxpool.Pool
}

// New creates the database if it doesn't exist, opens a connection pool, and
// ensures the schema is in place. The caller owns the returned Store and
// must Close it.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if err := createDatabaseIfNotExists(ctx, cfg); err != nil {
		return nil, err
	}

	pool, err := pgxpool.New(ctx, cfg.ConnString)
	if err != nil {
		return nil, err
	}

	if _, err := pool.Exec(ctx, CreateTableBaseCases); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, CreateTableCaseHistory); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, CreateTableTestExecutions); err != nil {
		pool.Close()
		return nil, err
	}
	// 迁移可能由更早版本创建、还叫 base_case_id 的表；新建的表已经是 uid，这里是空操作。
	if _, err := pool.Exec(ctx, MigrateBaseCaseIDToUID); err != nil {
		pool.Close()
		return nil, err
	}

	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func createDatabaseIfNotExists(ctx context.Context, cfg Config) error {
	adminPool, err := pgxpool.New(ctx, cfg.AdminConnString)
	if err != nil {
		return err
	}
	defer adminPool.Close()

	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, cfg.DBName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = adminPool.Exec(ctx, fmt.Sprintf(CreateDatabase, pgx.Identifier{cfg.DBName}.Sanitize()))
	return err
}

// UpsertTestCase 新增用例的编辑记录，新增后同时更新 base_cases 表的 revision 指针，指向最新的 case_history 记录。
func (s *Store) UpsertTestCase(ctx context.Context, tc *model.TestCase) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var uid int64
	var revision int
	err = tx.QueryRow(ctx, SelectBaseCaseByCaseIDForUpdate, tc.CaseID).Scan(&uid, &revision)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// basecases 中没有包含对应的 case_id，说明这是一个新建的测试用例，revision 应该初始化为 1
		revision = 1
		if err := tx.QueryRow(ctx, InsertBaseCase, tc.CaseID, revision).Scan(&uid); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		oldRevision := revision
		revision++
		if _, err := tx.Exec(ctx, UpdateBaseCaseRevision, revision, uid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, SupersedeActiveCaseHistory,
			model.CaseStateHistory, uid, oldRevision, model.CaseStateActive,
		); err != nil {
			return err
		}
	}

	tc.UID = uid
	tc.Revision = int64(revision)
	if err := tx.QueryRow(ctx, InsertCaseHistory,
		uid, tc.CaseID, tc.CaseName, tc.Priority, tc.Description,
		tc.PreCondition, tc.Steps, tc.ExpectedResult, tc.State, revision,
	).Scan(&tc.ID, &tc.CreatedAt); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// InsertTestCases 批量调用 UpsertTestCase 新增测试用例
func (s *Store) InsertTestCases(ctx context.Context, testCases []*model.TestCase) []error {
	errs := make([]error, len(testCases))
	for i, tc := range testCases {
		errs[i] = s.UpsertTestCase(ctx, tc)
	}
	return errs
}

// scanTestCase 将 pgx.Row 扫描到 model.TestCase 结构体中，并返回该结构体的指针和可能的错误。
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

// scanTestCases 将 pgx.Rows 扫描到 []*model.TestCase 切片中，并返回该切片和可能的错误。
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

// GetCaseByUID 根据 uid 查询单个用例的最新版本
func (s *Store) GetCaseByUID(ctx context.Context, uid int64) (*model.TestCase, error) {
	tc, err := scanTestCase(s.pool.QueryRow(ctx, SelectLatestCaseByUID, uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return tc, nil
}

// GetCaseHistory 根据 uid 查询单个用例的全部版本
func (s *Store) GetCaseHistory(ctx context.Context, uid int64) ([]*model.TestCase, error) {
	rows, err := s.pool.Query(ctx, SelectCaseHistoryByUID, uid)
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

// QueryCases 查询最新版本的测试用例，返回符合过滤条件的所有测试用例。
// 只会处理 queryableCaseFields 中存在的键，未知键会被忽略。
// 过滤值总是作为查询参数传递，而不是拼接到 SQL 文本中。
func (s *Store) QueryCases(ctx context.Context, filters map[string]string) ([]*model.TestCase, error) {
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

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanTestCases(rows)
}

// InsertTestExecution 新增一条测试执行记录（一份 markdown 报告）。
func (s *Store) InsertTestExecution(ctx context.Context, te *model.TestExecution) error {
	return s.pool.QueryRow(ctx, InsertTestExecution,
		te.UID, te.Revision, te.Content, te.ExecutedBy,
	).Scan(&te.ID, &te.ExecutedAt)
}

// scanTestExecution 将 pgx.Row 扫描到 model.TestExecution 结构体中，并返回该结构体的指针和可能的错误。
func scanTestExecution(row pgx.Row) (*model.TestExecution, error) {
	te := &model.TestExecution{}
	err := row.Scan(&te.ID, &te.UID, &te.Revision, &te.Content, &te.ExecutedBy, &te.ExecutedAt)
	if err != nil {
		return nil, err
	}
	return te, nil
}

// GetTestExecutions 查询某个用例(uid+revision)的全部执行记录。
func (s *Store) GetTestExecutions(ctx context.Context, uid, revision int64) ([]*model.TestExecution, error) {
	rows, err := s.pool.Query(ctx, SelectTestExecutions, uid, revision)
	if err != nil {
		return nil, err
	}
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

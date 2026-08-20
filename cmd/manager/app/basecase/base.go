// Package basecase 是用例基线（base_cases/case_history/test_executions）
// 以及各个版本分支物理表的唯一网关：除了这个包导出的方法，代码库中其它任何
// 地方都不允许直接读写这些表——这一点由代码结构保证（表名、SQL 都是包内
// 私有的），而不是运行时检查。
//
// 基线相当于 git 里的 main/master：不接受直接写入。所有用例的新增/编辑/
// 测试记录都发生在某个"版本"分支里（NewVersion/UpsertVersionCase/
// RecordVersionExecution），分支有自己独立的 history_<version>/
// executions_<version> 表；只有 MergeVersion 才会把分支里的编辑追加到基线
// 上。新用例的 UID 始终由基线分配（NewCaseUID），保证跨分支全局唯一。
package basecase

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var versionNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,50}$`)

func validateVersionName(version string) error {
	if !versionNameRe.MatchString(version) {
		return fmt.Errorf("invalid version name %q: must match %s", version, versionNameRe.String())
	}
	return nil
}

// historyTableName / executionsTableName 把一个已校验过的版本名转成安全的
// 表名：先加前缀避免和基线表撞名，再用 pgx.Identifier.Sanitize 转成带引号
// 的标识符（双重防护——调用方在此之前必须已经过 validateVersionName）。
func historyTableName(version string) string {
	return pgx.Identifier{"history_" + version}.Sanitize()
}

func executionsTableName(version string) string {
	return pgx.Identifier{"executions_" + version}.Sanitize()
}

// caseVersion 是一个版本分支在数据库层面的落地。它的 history/executions 表
// 和基线的 case_history/test_executions 结构相同，但 revision 只是分支内部
// 的局部计数器——和 base_cases.revision 无关，直到 MergeVersion 才会被
// 重新编号、追加到基线上（见 merge.go）。
type caseVersion struct {
	name            string
	historyTable    string
	executionsTable string
}

func newCaseVersion(name string) *caseVersion {
	return &caseVersion{
		name:            name,
		historyTable:    historyTableName(name),
		executionsTable: executionsTableName(name),
	}
}

// BaseCase 持有到 casehub 数据库的连接，并在内存中缓存当前存在哪些版本分支。
type BaseCase struct {
	pool     *pgxpool.Pool
	versions map[string]*caseVersion
}

// Config 描述如何连接、创建 basecase 所需的数据库。
type Config struct {
	// ConnString connects to DBName directly.
	ConnString string
	// AdminConnString connects to the "postgres" maintenance database, used
	// to create DBName before it exists.
	AdminConnString string
	DBName          string
}

// New 连接数据库（不存在则创建），创建基线三张表和版本注册表 case_versions，
// 并把已登记的每个版本分支重新加载进内存、重新确保（幂等）它们的物理表
// 存在，让 manager 重启后已有的分支仍然可用。调用方拥有返回的 *BaseCase，
// 用完需要 Close。
func New(ctx context.Context, cfg Config) (*BaseCase, error) {
	if err := createDatabaseIfNotExists(ctx, cfg); err != nil {
		return nil, err
	}

	pool, err := pgxpool.New(ctx, cfg.ConnString)
	if err != nil {
		return nil, err
	}

	b := &BaseCase{pool: pool, versions: map[string]*caseVersion{}}
	if err := b.bootstrap(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return b, nil
}

func (b *BaseCase) Close() {
	b.pool.Close()
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

func (b *BaseCase) bootstrap(ctx context.Context) error {
	for _, ddl := range []string{CreateTableBaseCases, CreateTableCaseHistory, CreateTableTestExecutions, CreateTableCaseVersions} {
		if _, err := b.pool.Exec(ctx, ddl); err != nil {
			return err
		}
	}
	if _, err := b.pool.Exec(ctx, MigrateBaseCaseIDToUID); err != nil {
		return err
	}

	rows, err := b.pool.Query(ctx, SelectAllVersions)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, name := range names {
		if err := b.ensureVersionTables(ctx, name); err != nil {
			return err
		}
		b.versions[name] = newCaseVersion(name)
	}
	return nil
}

func (b *BaseCase) ensureVersionTables(ctx context.Context, version string) error {
	cv := newCaseVersion(version)
	if _, err := b.pool.Exec(ctx, fmt.Sprintf(createVersionHistoryTableTpl, cv.historyTable)); err != nil {
		return err
	}
	if _, err := b.pool.Exec(ctx, fmt.Sprintf(createVersionExecutionsTableTpl, cv.executionsTable)); err != nil {
		return err
	}
	// 给可能是老 schema（建表时还没有 merged/is_pull 列）的既有分支表补列；
	// 新建的表 CREATE TABLE 已经带了这些列，这里是空操作。
	if _, err := b.pool.Exec(ctx, fmt.Sprintf(addVersionHistoryMergedColumnTpl, cv.historyTable)); err != nil {
		return err
	}
	if _, err := b.pool.Exec(ctx, fmt.Sprintf(addVersionHistoryIsPullColumnTpl, cv.historyTable)); err != nil {
		return err
	}
	if _, err := b.pool.Exec(ctx, fmt.Sprintf(addVersionExecutionsMergedColumnTpl, cv.executionsTable)); err != nil {
		return err
	}
	return nil
}

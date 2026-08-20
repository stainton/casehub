package basecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/stainton/casehub/pkg/model"
)

var (
	ErrVersionExists   = errors.New("version already exists")
	ErrVersionNotFound = errors.New("version not found")
)

// NewVersion 创建一个新的版本分支：校验名称、在 case_versions 登记、创建它
// 专属的 history/executions 表。分支创建时是空的——已有的基线用例只有在这个
// 分支里被编辑过之后才会出现在分支的 history 表里；第一次编辑时的局部
// revision 就是"基线当前 revision + 1"，这个不变量是 MergeVersion 冲突
// 检测的基础（见 merge.go）。
func (b *BaseCase) NewVersion(ctx context.Context, version string) error {
	if err := validateVersionName(version); err != nil {
		return err
	}
	if _, exists := b.versions[version]; exists {
		return ErrVersionExists
	}

	if _, err := b.pool.Exec(ctx, InsertVersion, version); err != nil {
		return err
	}
	if err := b.ensureVersionTables(ctx, version); err != nil {
		return err
	}
	b.versions[version] = newCaseVersion(version)
	return nil
}

// ListVersions 返回当前存在的所有版本分支名称。
func (b *BaseCase) ListVersions() []string {
	names := make([]string, 0, len(b.versions))
	for name := range b.versions {
		names = append(names, name)
	}
	return names
}

// VersionExists 判断某个版本分支是否存在。
func (b *BaseCase) VersionExists(version string) bool {
	_, ok := b.versions[version]
	return ok
}

func (b *BaseCase) resolveVersion(version string) (*caseVersion, error) {
	cv, ok := b.versions[version]
	if !ok {
		return nil, ErrVersionNotFound
	}
	return cv, nil
}

// PullFromBase 把 uid 在基线上当前已合并的最新内容原样拷贝进分支，作为
// 分支里这个用例的起始状态——只拷贝 case_history 的内容，不拷贝任何执行
// 记录（分支从零开始积累自己的测试记录）。这份拷贝记在分支里的 revision
// 就是基线当前的 revision（不是 +1）：在被真正修改之前，它和基线上的内容
// 完全一样，不算一次新的编辑，标记 is_pull=TRUE。合并候选判定（见 merge.go
// 的 selectDistinctVersionUIDsTpl/selectVersionHistoryForMergeTpl）会跳过
// is_pull 的行——只要没有真正改过，合并时不会往基线上平白多插一条内容
// 相同的 revision。同一个 revision 重复拉取（比如误触了两次、期间基线没有
// 变化）是幂等的，直接返回已有内容，不会重复插入或报错。uid 在基线上不
// 存在（或还没有被合并过）时返回错误。
func (b *BaseCase) PullFromBase(ctx context.Context, version string, uid int64) (*model.TestCase, error) {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return nil, err
	}

	tc, err := b.GetCase(ctx, uid)
	if err != nil {
		return nil, err
	}
	if tc == nil {
		return nil, fmt.Errorf("case not found on base: uid=%d", uid)
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, fmt.Sprintf(selectVersionHasRevisionTpl, cv.historyTable), tc.UID, tc.Revision).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return tc, nil
	}

	if err := tx.QueryRow(ctx, fmt.Sprintf(insertVersionHistoryTpl, cv.historyTable),
		tc.UID, tc.CaseID, tc.CaseName, tc.Priority, tc.Description,
		tc.PreCondition, tc.Steps, tc.ExpectedResult, tc.State, tc.Revision, true,
	).Scan(&tc.ID, &tc.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return tc, nil
}

// NewCaseUID 在基线的 base_cases 里登记一个新用例、分配它的 uid。case_id 的
// 唯一性由 base_cases.case_id 的 UNIQUE 约束保证，而不是先查后插的两步
// 检查，所以无论这个用例最终从哪个分支合并进来，重名都会在这里就失败，
// 不需要在 MergeVersion 时再单独校验一次。新用例的 revision 从 0 开始：
// 0 表示"还没有被合并过"——SelectLatestCases 靠 revision 关联 case_history，
// revision=0 时天然查不到匹配的 case_history 行，所以合并之前这个用例不会
// 出现在基线的任何查询结果里。
func (b *BaseCase) NewCaseUID(ctx context.Context, caseID string) (int64, error) {
	var uid int64
	err := b.pool.QueryRow(ctx, InsertBaseCase, caseID, 0).Scan(&uid)
	return uid, err
}

// UpsertVersionCase 在指定分支里为 tc.UID 新增一条局部编辑记录。分支内的
// revision 是局部计数器：第一次 touch 这个 uid 时从 base_cases.revision（此
// 时用 FOR UPDATE 读一次，只是为了拿到一个一致的起始编号，事务很快提交，
// 不会长时间持锁）+ 1 开始；同一分支内后续编辑同一个 uid，直接在分支表内
// MAX(revision) + 1。执行结果写回 tc（ID/Revision/CreatedAt）。
func (b *BaseCase) UpsertVersionCase(ctx context.Context, version string, tc *model.TestCase) error {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return err
	}
	if tc.UID == 0 {
		return fmt.Errorf("uid is required")
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var maxRev int
	if err := tx.QueryRow(ctx, fmt.Sprintf(selectVersionMaxRevisionTpl, cv.historyTable), tc.UID).Scan(&maxRev); err != nil {
		return err
	}

	var nextRev int
	if maxRev > 0 {
		nextRev = maxRev + 1
	} else {
		var baseRev int
		if err := tx.QueryRow(ctx, SelectBaseCaseByUIDForUpdate, tc.UID).Scan(&baseRev); err != nil {
			return err
		}
		nextRev = baseRev + 1
	}

	if tc.State == "" {
		tc.State = model.CaseStateDraft
	}
	if err := tx.QueryRow(ctx, fmt.Sprintf(insertVersionHistoryTpl, cv.historyTable),
		tc.UID, tc.CaseID, tc.CaseName, tc.Priority, tc.Description,
		tc.PreCondition, tc.Steps, tc.ExpectedResult, tc.State, nextRev, false,
	).Scan(&tc.ID, &tc.CreatedAt); err != nil {
		return err
	}
	tc.Revision = int64(nextRev)

	return tx.Commit(ctx)
}

// RecordVersionExecution 在指定分支里新增一条测试执行记录，写进这个分支
// 自己的 executions 表，关联到分支内的局部 revision（te.Revision）。
func (b *BaseCase) RecordVersionExecution(ctx context.Context, version string, te *model.TestExecution) error {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return err
	}
	return b.pool.QueryRow(ctx, fmt.Sprintf(insertVersionExecutionTpl, cv.executionsTable),
		te.UID, te.Revision, te.Content, te.ExecutedBy,
	).Scan(&te.ID, &te.ExecutedAt)
}

// GetVersionCase 返回某个用例在分支里的最新一条（局部 revision 最大的）
// 编辑记录；分支里从未 touch 过这个 uid 时返回 (nil, nil)。
func (b *BaseCase) GetVersionCase(ctx context.Context, version string, uid int64) (*model.TestCase, error) {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return nil, err
	}
	tc, err := scanTestCase(b.pool.QueryRow(ctx, fmt.Sprintf(selectVersionCaseLatestTpl, cv.historyTable), uid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return tc, nil
}

// GetVersionCaseHistory 返回某个用例在分支里的全部编辑记录，局部 revision
// 从新到旧。
func (b *BaseCase) GetVersionCaseHistory(ctx context.Context, version string, uid int64) ([]*model.TestCase, error) {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return nil, err
	}
	rows, err := b.pool.Query(ctx, fmt.Sprintf(selectVersionCaseHistoryTpl, cv.historyTable), uid)
	if err != nil {
		return nil, err
	}
	return scanTestCases(rows)
}

// ListVersionCases 返回分支里每个被 touch 过的用例的最新一条记录，不管这条
// 记录合没合并过——分支自己的浏览视图完整保留，合并不会让内容消失。
func (b *BaseCase) ListVersionCases(ctx context.Context, version string) ([]*model.TestCase, error) {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return nil, err
	}
	rows, err := b.pool.Query(ctx, fmt.Sprintf(selectVersionCasesTpl, cv.historyTable))
	if err != nil {
		return nil, err
	}
	return scanTestCases(rows)
}

// ListPendingVersionCases 返回分支里还有东西可以合并的用例最新一条记录——
// 供合并选择器这类"挑哪些去合并"的场景用：内容有真实编辑待合并的、或者
// 测试记录有更新待合并的（哪怕内容本身没变）都算，已经合并干净、既没有
// 内容改动也没有新测试记录的用例不会出现在这份列表里。
func (b *BaseCase) ListPendingVersionCases(ctx context.Context, version string) ([]*model.TestCase, error) {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return nil, err
	}
	rows, err := b.pool.Query(ctx, fmt.Sprintf(selectVersionPendingCasesTpl, cv.historyTable, cv.executionsTable))
	if err != nil {
		return nil, err
	}
	return scanTestCases(rows)
}

// ListVersionExecutions 返回分支里某个用例、某个局部 revision 下的全部
// 执行记录。
func (b *BaseCase) ListVersionExecutions(ctx context.Context, version string, uid, revision int64) ([]*model.TestExecution, error) {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return nil, err
	}
	rows, err := b.pool.Query(ctx, fmt.Sprintf(selectVersionExecutionsTpl, cv.executionsTable), uid, revision)
	if err != nil {
		return nil, err
	}
	return scanTestExecutions(rows)
}

// DeleteVersionHistory 删除分支里某个用例的一条局部编辑记录，用于合并前
// 撤销一次写错的编辑（下一次编辑会基于分支里剩下的最大 revision 继续）。
func (b *BaseCase) DeleteVersionHistory(ctx context.Context, version string, uid, revision int64) error {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return err
	}
	_, err = b.pool.Exec(ctx, fmt.Sprintf(deleteVersionHistoryRowTpl, cv.historyTable), uid, revision)
	return err
}

// DeleteVersionExecution 删除分支里的一条执行记录。
func (b *BaseCase) DeleteVersionExecution(ctx context.Context, version string, id int64) error {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return err
	}
	_, err = b.pool.Exec(ctx, fmt.Sprintf(deleteVersionExecutionRowTpl, cv.executionsTable), id)
	return err
}

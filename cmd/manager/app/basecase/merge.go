package basecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/stainton/casehub/pkg/api"
	"github.com/stainton/casehub/pkg/model"
)

// MergeVersion 把分支里待合并的用例追加到基线上。uids 为空时，合并分支里
// 全部有真实编辑待合并的用例（只拉取过、从没改过的用例不算，见
// selectDistinctVersionUIDsTpl）。
//
// 每个 uid 独立处理、独立提交（一个 uid 的失败/冲突不影响其它 uid）：读出
// 分支里这个用例的所有局部编辑记录（按 revision 升序，只算真正编辑过的，
// 拉取时留下的镜像行不算），如果基线当前 revision >= 分支里最小的那条局部
// 编辑 revision，说明基线在这个分支开始编辑之后已经被别的分支合并过更新的
// 版本——冲突，不合并，记录原因。否则说明基线自这个分支开始编辑以来没有
// 变化：把分支的编辑记录按顺序重新编号为基线revision+1、+2...，连同每条
// 编辑记录在同一局部 revision 下的执行记录（重新编号到相同的新 revision）
// 一起写进基线的 case_history/test_executions，推进 base_cases.revision，
// 并把刚合并的这些行（连同它们之前的拉取镜像行）标记为已合并——分支表此后
// 这部分不再被当成待合并内容，这样连续多次合并不会把已经合并过的旧记录又
// 当成冲突源，但内容仍然留在分支自己的表里，浏览视图看得到。
func (b *BaseCase) MergeVersion(ctx context.Context, version string, uids []int64) (*api.MergeResult, error) {
	cv, err := b.resolveVersion(version)
	if err != nil {
		return nil, err
	}

	if len(uids) == 0 {
		rows, err := b.pool.Query(ctx, fmt.Sprintf(selectDistinctVersionUIDsTpl, cv.historyTable, cv.executionsTable))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var uid int64
			if err := rows.Scan(&uid); err != nil {
				rows.Close()
				return nil, err
			}
			uids = append(uids, uid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	result := &api.MergeResult{}
	for _, uid := range uids {
		merged, conflict, err := b.mergeOne(ctx, cv, uid)
		if err != nil {
			return nil, err
		}
		switch {
		case conflict != nil:
			result.Conflicts = append(result.Conflicts, *conflict)
		case merged:
			result.Merged = append(result.Merged, uid)
		}
	}
	return result, nil
}

// mergeOne 处理单个 uid 的合并，merged=false 且 conflict=nil 表示分支里
// 根本没有这个 uid 的待合并记录（既不是冲突，也没有可合并的内容）——包括
// 分支里压根没 touch 过这个 uid，或者只拉取过、既没编辑过内容也没有新增
// 测试记录的情况。
//
// 内容编辑和测试记录是两件独立的事：内容没有变化（只拉取过、没改过）不该
// 拦住测试记录的合并——mergeUnpulledExecutions 先单独把这部分处理掉，不管
// 下面的内容合并是成功、冲突还是压根没有待合并的内容编辑，都不影响它。
func (b *BaseCase) mergeOne(ctx context.Context, cv *caseVersion, uid int64) (merged bool, conflict *api.MergeConflict, err error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback(ctx)

	var baseRev int
	if err := tx.QueryRow(ctx, SelectBaseCaseByUIDForUpdate, uid).Scan(&baseRev); err != nil {
		return false, nil, err
	}

	execMerged, err := b.mergeUnpulledExecutions(ctx, tx, cv, uid, baseRev)
	if err != nil {
		return false, nil, err
	}

	rows, err := tx.Query(ctx, fmt.Sprintf(selectVersionHistoryForMergeTpl, cv.historyTable), uid)
	if err != nil {
		return false, nil, err
	}
	pending, err := scanTestCases(rows)
	if err != nil {
		return false, nil, err
	}
	if len(pending) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, nil, err
		}
		return execMerged, nil, nil
	}

	minLocalRev := int(pending[0].Revision)
	if baseRev >= minLocalRev {
		if err := tx.Commit(ctx); err != nil {
			return false, nil, err
		}
		return execMerged, &api.MergeConflict{
			UID:    uid,
			CaseID: pending[0].CaseID,
			Reason: fmt.Sprintf(
				"base revision (%d) has moved past this branch's starting point (local revision %d); re-edit from the current base and merge again",
				baseRev, minLocalRev,
			),
		}, nil
	}

	newRev := baseRev
	for _, tc := range pending {
		localRev := tc.Revision
		newRev++

		if _, err := tx.Exec(ctx, InsertCaseHistory,
			uid, tc.CaseID, tc.CaseName, tc.Priority, tc.Description,
			tc.PreCondition, tc.Steps, tc.ExpectedResult, tc.State, newRev,
		); err != nil {
			return false, nil, err
		}

		if _, err := b.mergeExecutionsAtRevision(ctx, tx, cv, uid, localRev, int64(newRev)); err != nil {
			return false, nil, err
		}
	}

	if _, err := tx.Exec(ctx, SupersedeActiveCaseHistory, model.CaseStateHistory, uid, baseRev, model.CaseStateActive); err != nil {
		return false, nil, err
	}
	if _, err := tx.Exec(ctx, UpdateBaseCaseRevision, newRev, uid); err != nil {
		return false, nil, err
	}

	// 标记为已合并而不是删除：分支自己的浏览视图不按 merged 过滤，所以这些
	// 内容会继续留在分支里可见——分支合并之后就和基线断开了，基线后续的
	// 变化不会再同步给它，它只是把当时已经确认的这部分编辑追加进了基线，
	// 自己的这份记录原样保留，不受影响。
	lastLocalRev := pending[len(pending)-1].Revision
	if _, err := tx.Exec(ctx, fmt.Sprintf(markVersionHistoryMergedTpl, cv.historyTable), uid, lastLocalRev); err != nil {
		return false, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, nil, err
	}
	return true, nil, nil
}

// mergeUnpulledExecutions 处理"内容没有变化，但测试记录有更新"的情况：找
// 这个 uid 在分支里那一行内容和基线完全一样的拉取镜像（is_pull），如果它
// 的本地 revision 还等于基线当前 revision（说明基线自拉取以来没有被别的
// 分支改过），就把它名下还没合并过的测试记录直接搬进基线的
// test_executions，挂在基线现有的这个 revision 下——不新建 case_history
// 行，因为内容压根没变，不构成一次新的编辑。如果基线已经往前走了（拉取
// 之后又被别的分支合并过），这些测试记录暂时不合并（留在分支里，等重新
// 拉取最新内容后再说），不报错、也不算冲突——毕竟没有对应的内容合并动作
// 在尝试，谈不上"冲突"。
func (b *BaseCase) mergeUnpulledExecutions(ctx context.Context, tx pgx.Tx, cv *caseVersion, uid int64, baseRev int) (bool, error) {
	var pullRev int
	err := tx.QueryRow(ctx, fmt.Sprintf(selectVersionPullRevisionTpl, cv.historyTable), uid).Scan(&pullRev)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pullRev != baseRev {
		return false, nil
	}
	return b.mergeExecutionsAtRevision(ctx, tx, cv, uid, int64(pullRev), int64(baseRev))
}

// mergeExecutionsAtRevision 把 uid 在分支里 localRev 这个本地 revision 下、
// 还没合并过的测试记录搬进基线 test_executions 的 newBaseRev（内容真正被
// 编辑过时 newBaseRev 是新分配的 revision；内容没变时 newBaseRev 就是
// baseRev 本身，参见 mergeUnpulledExecutions），逐条标记为已合并——按
// revision 精确标记而不是按区间批量标记，因为同一个本地 revision（尤其是
// is_pull 那一行）上可能会持续追加新的测试记录，每次合并只应该搬走这次
// 新增的，不影响下次继续合并这个 revision 上更新的记录。
func (b *BaseCase) mergeExecutionsAtRevision(ctx context.Context, tx pgx.Tx, cv *caseVersion, uid, localRev, newBaseRev int64) (bool, error) {
	rows, err := tx.Query(ctx, fmt.Sprintf(selectVersionExecutionsForRevisionTpl, cv.executionsTable), uid, localRev)
	if err != nil {
		return false, err
	}
	executions, err := scanTestExecutions(rows)
	if err != nil {
		return false, err
	}
	if len(executions) == 0 {
		return false, nil
	}
	for _, te := range executions {
		if _, err := tx.Exec(ctx, InsertTestExecution, uid, newBaseRev, te.Content, te.ExecutedBy); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(markVersionExecutionsMergedTpl, cv.executionsTable), uid, localRev); err != nil {
		return false, err
	}
	return true, nil
}

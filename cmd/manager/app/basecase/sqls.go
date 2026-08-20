package basecase

const (
	CreateDatabase = `CREATE DATABASE %s`

	// base_cases/case_history/test_executions：和 cmd/api 曾经维护的 schema
	// 完全一致——这个包现在是它们唯一的写入方。case_id 的 UNIQUE 约束是
	// NewCaseUID 用来保证新用例不重名的手段，而不是先查后插的两步检查。
	CreateTableBaseCases = `CREATE TABLE IF NOT EXISTS base_cases (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		case_id VARCHAR(255) NOT NULL UNIQUE,
		revision INT NOT NULL
	)`

	// case_history 只在这里被写入（合并时）；分支自己的编辑记录存在各自的
	// history_<version> 表里，见下面的 createVersionHistoryTableTpl。
	CreateTableCaseHistory = `CREATE TABLE IF NOT EXISTS case_history (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		uid BIGINT NOT NULL REFERENCES base_cases(id),
		case_id VARCHAR(255) NOT NULL,
		case_name VARCHAR(255) NOT NULL,
		priority INT NOT NULL,
		description TEXT,
		pre_condition TEXT,
		steps TEXT[],
		expected_result TEXT[],
		state VARCHAR(50) NOT NULL,
		revision INT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		UNIQUE (uid, revision)
	)`

	CreateTableTestExecutions = `CREATE TABLE IF NOT EXISTS test_executions (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		uid BIGINT NOT NULL REFERENCES base_cases(id),
		revision INT NOT NULL,
		content TEXT NOT NULL,
		executed_by VARCHAR(255) NOT NULL,
		executed_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`

	// case_versions 记录当前存在哪些版本分支。manager 重启时用它重新加载
	// versions map，并为每个分支重新确保（幂等）它的物理表存在。
	CreateTableCaseVersions = `CREATE TABLE IF NOT EXISTS case_versions (
		version VARCHAR(64) PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`

	SelectAllVersions = `SELECT version FROM case_versions ORDER BY created_at`
	InsertVersion     = `INSERT INTO case_versions (version) VALUES ($1)`

	// MigrateBaseCaseIDToUID 把 case_history/test_executions 里的旧列名
	// base_case_id 改名成 uid（这两张表以前由 cmd/api 维护，schema 历史沿用
	// 到这里）。对已经是新 schema 或者刚被 CREATE TABLE IF NOT EXISTS 新建
	// 的表都是空操作，可以每次启动都执行。
	MigrateBaseCaseIDToUID = `
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'case_history' AND column_name = 'base_case_id'
			) THEN
				ALTER TABLE case_history RENAME COLUMN base_case_id TO uid;
			END IF;
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'test_executions' AND column_name = 'base_case_id'
			) THEN
				ALTER TABLE test_executions RENAME COLUMN base_case_id TO uid;
			END IF;
		END $$;`

	SelectBaseCaseByUIDForUpdate = `SELECT revision FROM base_cases WHERE id = $1 FOR UPDATE`
	SelectUIDByCaseID            = `SELECT id FROM base_cases WHERE case_id = $1`
	InsertBaseCase               = `INSERT INTO base_cases (case_id, revision) VALUES ($1, $2) RETURNING id`
	UpdateBaseCaseRevision       = `UPDATE base_cases SET revision = $1 WHERE id = $2`

	// SupersedeActiveCaseHistory demotes the revision being replaced from
	// active to history; it is a no-op if that revision isn't active (e.g.
	// draft/deprecated, or revision 0 which never has a matching row), since
	// those aren't "current" states to begin with.
	SupersedeActiveCaseHistory = `UPDATE case_history SET state = $1 WHERE uid = $2 AND revision = $3 AND state = $4`

	InsertCaseHistory = `INSERT INTO case_history
		(uid, case_id, case_name, priority, description, pre_condition, steps, expected_result, state, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at`

	// caseHistoryColumns is the case_history column list, in model.TestCase
	// scan order. Reused verbatim (as %s-templated table) for the per-version
	// history_<version> tables, which share the exact same shape.
	caseHistoryColumns = `id, uid, case_id, case_name, priority, description, pre_condition, steps, expected_result, state, revision, created_at`

	selectLatestCasesColumns = `ch.id, ch.uid, ch.case_id, ch.case_name, ch.priority, ch.description, ch.pre_condition, ch.steps, ch.expected_result, ch.state, ch.revision, ch.created_at`

	// SelectLatestCases joins base_cases to the case_history row matching its
	// current revision pointer, i.e. "the latest merged state of every case".
	// A uid whose base revision is still 0 (never merged) has no matching
	// case_history row and so is naturally excluded.
	SelectLatestCases = `SELECT ` + selectLatestCasesColumns + `
		FROM base_cases bc
		JOIN case_history ch ON ch.uid = bc.id AND ch.revision = bc.revision`

	SelectLatestCaseByUID = SelectLatestCases + ` WHERE bc.id = $1`

	SelectCaseHistoryByUID = `SELECT ` + caseHistoryColumns + `
		FROM case_history WHERE uid = $1 ORDER BY revision DESC`

	InsertTestExecution = `INSERT INTO test_executions
		(uid, revision, content, executed_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, executed_at`

	testExecutionColumns = `id, uid, revision, content, executed_by, executed_at`

	SelectTestExecutions = `SELECT ` + testExecutionColumns + `
		FROM test_executions WHERE uid = $1 AND revision = $2 ORDER BY executed_at DESC`

	// --- per-version tables (history_<version> / executions_<version>) ---
	//
	// %s must always be a table name already produced by historyTableName /
	// executionsTableName (validated + pgx.Identifier-sanitized) — this is
	// the one place in the package where a string is interpolated into SQL
	// text instead of passed as a bind parameter.

	createVersionHistoryTableTpl = `CREATE TABLE IF NOT EXISTS %s (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		uid BIGINT NOT NULL REFERENCES base_cases(id),
		case_id VARCHAR(255) NOT NULL,
		case_name VARCHAR(255) NOT NULL,
		priority INT NOT NULL,
		description TEXT,
		pre_condition TEXT,
		steps TEXT[],
		expected_result TEXT[],
		state VARCHAR(50) NOT NULL,
		revision INT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		merged BOOLEAN NOT NULL DEFAULT FALSE,
		is_pull BOOLEAN NOT NULL DEFAULT FALSE,
		UNIQUE (uid, revision)
	)`

	// addVersionHistoryMergedColumnTpl/addVersionHistoryIsPullColumnTpl 给
	// 已经存在的旧 history_<version> 表补上 merged/is_pull 列（ADD COLUMN IF
	// NOT EXISTS 本身就是幂等的，不需要像 MigrateBaseCaseIDToUID 那样再包一层
	// DO $$ 判断存在性）；ensureVersionTables 每次都会跑一遍，新建的表
	// CREATE TABLE 已经带了这两列，这里是空操作。
	addVersionHistoryMergedColumnTpl = `ALTER TABLE %s ADD COLUMN IF NOT EXISTS merged BOOLEAN NOT NULL DEFAULT FALSE`
	addVersionHistoryIsPullColumnTpl = `ALTER TABLE %s ADD COLUMN IF NOT EXISTS is_pull BOOLEAN NOT NULL DEFAULT FALSE`

	createVersionExecutionsTableTpl = `CREATE TABLE IF NOT EXISTS %s (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		uid BIGINT NOT NULL REFERENCES base_cases(id),
		revision INT NOT NULL,
		content TEXT NOT NULL,
		executed_by VARCHAR(255) NOT NULL,
		executed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		merged BOOLEAN NOT NULL DEFAULT FALSE
	)`

	// addVersionExecutionsMergedColumnTpl 给已存在的旧 executions_<version>
	// 表补上 merged 列，和 addVersionHistoryMergedColumnTpl 一样的道理。
	addVersionExecutionsMergedColumnTpl = `ALTER TABLE %s ADD COLUMN IF NOT EXISTS merged BOOLEAN NOT NULL DEFAULT FALSE`

	// selectVersionMaxRevisionTpl 只看未合并的行——一个 uid 一旦被合并干净
	// （这个分支里它所有的编辑都 merged=TRUE 了），下一次再编辑它就该看作
	// "从当前基线重新开始"，本地编号从 base_cases.revision+1 续上（见
	// UpsertVersionCase），而不是接着已经合并过的旧编号继续加——否则下次
	// MergeVersion 判定冲突时的基准（分支起点局部 revision）就和"这次编辑
	// 是什么时候开始的"对不上了。
	selectVersionMaxRevisionTpl = `SELECT COALESCE(MAX(revision), 0) FROM %s WHERE uid = $1 AND NOT merged`

	insertVersionHistoryTpl = `INSERT INTO %s
		(uid, case_id, case_name, priority, description, pre_condition, steps, expected_result, state, revision, is_pull)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at`

	// selectVersionHasRevisionTpl 供 PullFromBase 判断"这个 uid 在这个 revision
	// 上是不是已经拉取过了"——同一个基线 revision 重复拉取（期间基线没有变化）
	// 是幂等的，不需要再插一行一模一样的镜像。
	selectVersionHasRevisionTpl = `SELECT EXISTS (SELECT 1 FROM %s WHERE uid = $1 AND revision = $2)`

	insertVersionExecutionTpl = `INSERT INTO %s
		(uid, revision, content, executed_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, executed_at`

	// selectVersionCaseHistoryTpl/selectVersionCaseLatestTpl/selectVersionCasesTpl
	// 故意不按 merged 过滤——分支自己的浏览视图（目录树、详情页、历史列表）
	// 展示这个分支曾经有过的全部内容，不管合没合并过：合并只是把内容追加到
	// 基线，不会让分支"断链"后就看不见自己原来的东西。只有合并候选集
	// （selectVersionPendingCasesTpl 及下面 merge.go 用到的两个 Tpl）才需要
	// 排除已经合并过的行。
	selectVersionCaseHistoryTpl = `SELECT ` + caseHistoryColumns + ` FROM %s WHERE uid = $1 ORDER BY revision DESC`
	selectVersionCaseLatestTpl  = `SELECT ` + caseHistoryColumns + ` FROM %s WHERE uid = $1 ORDER BY revision DESC LIMIT 1`
	selectVersionCasesTpl       = `SELECT DISTINCT ON (uid) ` + caseHistoryColumns + ` FROM %s ORDER BY uid, revision DESC`
	selectVersionExecutionsTpl  = `SELECT ` + testExecutionColumns + ` FROM %s WHERE uid = $1 AND revision = $2 ORDER BY executed_at DESC`

	// eligibleVersionUIDsSubqueryTpl 是"这个 uid 在分支里有没有值得合并的东西"
	// 的统一判定，供 selectDistinctVersionUIDsTpl（合并全部）和
	// selectVersionPendingCasesTpl（合并选择器候选列表）共用：满足任一条件即可——
	// (a) 有还没合并过的真实内容编辑（NOT merged AND NOT is_pull），或者
	// (b) 有还没合并过的测试记录，不管这条记录挂在哪个本地 revision 下
	// （哪怕这个 revision 只是拉取时留下的镜像、内容压根没改过——记录测试
	// 结果这件事和用例内容有没有变化完全独立，不该因为内容没变就被拦住，
	// 见 merge.go 的 mergeUnpulledExecutions）。%[1]s=history 表，%[2]s=
	// executions 表。
	eligibleVersionUIDsSubqueryTpl = `
		SELECT uid FROM %[1]s WHERE NOT merged AND NOT is_pull
		UNION
		SELECT uid FROM %[2]s WHERE NOT merged`

	selectDistinctVersionUIDsTpl = `SELECT DISTINCT uid FROM (` + eligibleVersionUIDsSubqueryTpl + `) eligible`

	// selectVersionPendingCasesTpl 返回 eligibleVersionUIDsSubqueryTpl 选中的
	// 每个 uid 在分支里最新的一条记录（不管这条记录本身合没合并过、是不是
	// 纯拉取镜像——只是取来给合并选择器展示 case_id/case_name 用），供
	// "我要挑哪些去合并"的场景用，和上面浏览用的 selectVersionCasesTpl 区分开。
	selectVersionPendingCasesTpl = `SELECT DISTINCT ON (uid) ` + caseHistoryColumns + ` FROM %[1]s
		WHERE uid IN (` + eligibleVersionUIDsSubqueryTpl + `)
		ORDER BY uid, revision DESC`

	deleteVersionHistoryRowTpl   = `DELETE FROM %s WHERE uid = $1 AND revision = $2`
	deleteVersionExecutionRowTpl = `DELETE FROM %s WHERE id = $1`

	// selectVersionHistoryForMergeTpl 排除 is_pull 的行：一个用例如果从基线
	// 拉下来之后内容从没改过，它在分支里的内容跟基线当前的完全一样，合并
	// 它的"内容"不会产生任何变化，不需要在基线上多插一条一模一样的
	// revision，直接当成"没有待合并的内容编辑"跳过（mergeOne 里 pending 为
	// 空的那条路径，参见 merge.go）——但它名下的测试记录仍然可能需要合并，
	// 那部分是 mergeUnpulledExecutions 单独处理的，不受这里排除的影响。
	selectVersionHistoryForMergeTpl = `SELECT ` + caseHistoryColumns + ` FROM %s WHERE uid = $1 AND NOT merged AND NOT is_pull ORDER BY revision ASC`

	// selectVersionExecutionsForRevisionTpl 只看还没合并过的执行记录——合并
	// 是逐条标记的（markVersionExecutionsMergedTpl），不像 history 那样一次
	// 按 revision 区间批量标记，因为同一个本地 revision（尤其是 is_pull 那
	// 一行）上可能会持续追加新的测试记录，每次合并只应该搬走"这次新增的"。
	selectVersionExecutionsForRevisionTpl = `SELECT ` + testExecutionColumns + ` FROM %s WHERE uid = $1 AND revision = $2 AND NOT merged`

	// selectVersionPullRevisionTpl 找这个 uid 在分支里"内容和基线一样、纯
	// 拉取镜像"的那一行的本地 revision，供 mergeUnpulledExecutions 判断它
	// 名下是否还有测试记录没合并——正常情况下一个 uid 在一个分支里最多只有
	// 一行 is_pull，ORDER BY+LIMIT 1 只是防御性的。
	selectVersionPullRevisionTpl = `SELECT revision FROM %s WHERE uid = $1 AND is_pull ORDER BY revision DESC LIMIT 1`

	// markVersionHistoryMergedTpl 合并成功后把刚合并的那些局部编辑标记为
	// merged=TRUE，取代原来的物理删除——这样分支自己的浏览视图（不按 merged
	// 过滤）还能继续看到这些内容，只是它们不再出现在合并候选集里（见上面
	// selectDistinctVersionUIDsTpl/selectVersionHistoryForMergeTpl）。
	markVersionHistoryMergedTpl = `UPDATE %s SET merged = TRUE WHERE uid = $1 AND revision <= $2`

	// markVersionExecutionsMergedTpl 把某个 uid、某个本地 revision 下刚合并
	// 过的测试记录标记为已合并——按具体 revision 精确标记（不是按区间批量），
	// 因为同一个 revision 上可能后续还会追加新记录，新记录要能在下一次合并
	// 时继续被选中。
	markVersionExecutionsMergedTpl = `UPDATE %s SET merged = TRUE WHERE uid = $1 AND revision = $2`
)

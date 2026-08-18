package postgres

const (
	CreateDatabase = `CREATE DATABASE %s`

	// base_cases holds exactly one row per business case_id, pointing at the
	// current revision. It is not versioned itself — case_history is.
	CreateTableBaseCases = `CREATE TABLE IF NOT EXISTS base_cases (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		case_id VARCHAR(255) NOT NULL UNIQUE,
		revision INT NOT NULL
	)`
	// case_history is append-only: every create/edit inserts a new row and
	// never updates an existing one.
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

	SelectBaseCaseByCaseIDForUpdate = `SELECT id, revision FROM base_cases WHERE case_id = $1 FOR UPDATE`
	InsertBaseCase                  = `INSERT INTO base_cases (case_id, revision) VALUES ($1, $2) RETURNING id`
	UpdateBaseCaseRevision          = `UPDATE base_cases SET revision = $1 WHERE id = $2`

	// SupersedeActiveCaseHistory demotes the revision being replaced from
	// active to history; it is a no-op if that revision isn't active (e.g.
	// draft or deprecated), since those aren't "current" states to begin with.
	SupersedeActiveCaseHistory = `UPDATE case_history SET state = $1 WHERE uid = $2 AND revision = $3 AND state = $4`

	InsertCaseHistory = `INSERT INTO case_history
		(uid, case_id, case_name, priority, description, pre_condition, steps, expected_result, state, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at`

	// caseHistoryColumns is the case_history column list, in model.TestCase
	// scan order, for queries that read directly from case_history.
	caseHistoryColumns = `id, uid, case_id, case_name, priority, description, pre_condition, steps, expected_result, state, revision, created_at`

	// selectLatestCasesColumns is the same column list prefixed for the "ch"
	// alias used by SelectLatestCases.
	selectLatestCasesColumns = `ch.id, ch.uid, ch.case_id, ch.case_name, ch.priority, ch.description, ch.pre_condition, ch.steps, ch.expected_result, ch.state, ch.revision, ch.created_at`

	// SelectLatestCases joins base_cases to the case_history row matching its
	// current revision pointer, i.e. "the latest state of every case". Callers
	// append WHERE/params for filtering (see queryCases and SelectLatestCaseByUID).
	SelectLatestCases = `SELECT ` + selectLatestCasesColumns + `
		FROM base_cases bc
		JOIN case_history ch ON ch.uid = bc.id AND ch.revision = bc.revision`

	SelectLatestCaseByUID = SelectLatestCases + ` WHERE bc.id = $1`

	SelectCaseHistoryByUID = `SELECT ` + caseHistoryColumns + `
		FROM case_history WHERE uid = $1 ORDER BY revision DESC`

	// test_executions is append-only: every recorded run is a new row, never
	// updated. content is a full markdown report for the whole case, with any
	// screenshots embedded as base64 data URIs — no separate image storage.
	CreateTableTestExecutions = `CREATE TABLE IF NOT EXISTS test_executions (
		id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		uid BIGINT NOT NULL REFERENCES base_cases(id),
		revision INT NOT NULL,
		content TEXT NOT NULL,
		executed_by VARCHAR(255) NOT NULL,
		executed_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`

	InsertTestExecution = `INSERT INTO test_executions
		(uid, revision, content, executed_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, executed_at`

	testExecutionColumns = `id, uid, revision, content, executed_by, executed_at`

	// SelectTestExecutions returns every run recorded for a case revision,
	// newest first.
	SelectTestExecutions = `SELECT ` + testExecutionColumns + `
		FROM test_executions WHERE uid = $1 AND revision = $2 ORDER BY executed_at DESC`

	// MigrateBaseCaseIDToUID 把 case_history/test_executions 里的旧列名
	// base_case_id 改名成 uid（TestCase.BaseCaseID 重命名为 TestCase.UID 之前的
	// schema）。对已经是新 schema（列已经叫 uid）或者刚被 CREATE TABLE IF NOT
	// EXISTS 新建的表都是空操作，可以每次启动都执行。
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
)

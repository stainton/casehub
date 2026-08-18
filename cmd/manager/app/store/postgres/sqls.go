package postgres

const (
	CreateDatabase = `CREATE DATABASE %s`

	// case_folders 存储用例的目录组织信息。folder_id 不用 IDENTITY 列，而是
	// 从单独的序列（从 2 开始）分配，这样 1 可以稳定留给根目录，不会和自动
	// 分配的 ID 冲突。
	CreateTableCaseFolders = `CREATE TABLE IF NOT EXISTS case_folders (
		folder_id BIGINT PRIMARY KEY,
		folder_name VARCHAR(255) NOT NULL,
		parent_id BIGINT NOT NULL,
		case_uids BIGINT[] NOT NULL DEFAULT '{}'
	)`

	CreateFolderIDSeq = `CREATE SEQUENCE IF NOT EXISTS case_folders_folder_id_seq START WITH 2`

	// InsertRootFolderIfNotExists 幂等创建根目录：FolderID=1，ParentID=0，
	// FolderName="基线"（designed.md 约定）。
	InsertRootFolderIfNotExists = `INSERT INTO case_folders (folder_id, folder_name, parent_id, case_uids)
		VALUES (1, '基线', 0, '{}')
		ON CONFLICT (folder_id) DO NOTHING`

	InsertFolder = `INSERT INTO case_folders (folder_id, folder_name, parent_id, case_uids)
		VALUES (nextval('case_folders_folder_id_seq'), $1, $2, $3)
		RETURNING folder_id`

	folderColumns = `folder_id, folder_name, parent_id, case_uids`

	SelectFolder = `SELECT ` + folderColumns + ` FROM case_folders WHERE folder_id = $1`

	SelectAllFolders = `SELECT ` + folderColumns + ` FROM case_folders ORDER BY folder_id`

	SelectFolderCaseUIDsForUpdate = `SELECT case_uids FROM case_folders WHERE folder_id = $1 FOR UPDATE`

	UpdateFolderName     = `UPDATE case_folders SET folder_name = $1 WHERE folder_id = $2`
	UpdateFolderParent   = `UPDATE case_folders SET parent_id = $1 WHERE folder_id = $2`
	UpdateFolderCaseUIDs = `UPDATE case_folders SET case_uids = $1 WHERE folder_id = $2`

	DeleteFolder = `DELETE FROM case_folders WHERE folder_id = $1`

	CountChildFolders = `SELECT COUNT(*) FROM case_folders WHERE parent_id = $1`
)

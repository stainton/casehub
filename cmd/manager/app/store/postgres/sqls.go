package postgres

const (
	CreateDatabase = `CREATE DATABASE %s`

	// case_folders 存储用例的目录组织信息。folder_id 不用 IDENTITY 列，而是
	// 从单独的序列分配——目录树不预置任何一行，顶层（产品级，parent_id=0）
	// 目录和其它目录一样按需创建，没有哪个 ID 是特殊保留的。
	CreateTableCaseFolders = `CREATE TABLE IF NOT EXISTS case_folders (
		folder_id BIGINT PRIMARY KEY,
		folder_name VARCHAR(255) NOT NULL,
		parent_id BIGINT NOT NULL,
		case_uids BIGINT[] NOT NULL DEFAULT '{}'
	)`

	CreateFolderIDSeq = `CREATE SEQUENCE IF NOT EXISTS case_folders_folder_id_seq START WITH 1`

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

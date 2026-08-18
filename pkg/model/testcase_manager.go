package model

type CasesFloder struct {
	FolderID   int64   `json:"folder_id" gorm:"primaryKey;autoIncrement"`
	FolderName string  `json:"folder_name" gorm:"type:varchar(255);not null"`
	ParentID   int64   `json:"parent_id" gorm:"type:int;not null"`
	CaseUIDs   []int64 `json:"case_uids" gorm:"type:int[]"`
}

type RequestAddTestCase struct {
	FolderID  int64      `json:"folder_id" binding:"required"`
	TestCases []TestCase `json:"test_cases" binding:"required"`
}

type RequestUpdateTestCase struct {
	FolderID  int64      `json:"folder_id" binding:"required"`
	TestCases []TestCase `json:"test_cases" binding:"required"`
}

type RequestDeleteTestCase struct {
	FolderID  int64      `json:"folder_id" binding:"required"`
	TestCases []TestCase `json:"test_cases" binding:"required"`
}

type RequestMigrateTestCase struct {
	SourceFolderID int64      `json:"source_folder_id" binding:"required"`
	TargetFolderID int64      `json:"target_folder_id" binding:"required"`
	TestCases      []TestCase `json:"test_cases" binding:"required"`
	IsCopy         bool       `json:"is_copy" binding:"required"`
}

type RequestCreateCaseFolder struct {
	FolderName string `json:"folder_name" binding:"required"`
	ParentID   int64  `json:"parent_id" binding:"required"`
}

type RequestUpdateCaseFolder struct {
	FolderID   int64  `json:"folder_id" binding:"required"`
	FolderName string `json:"folder_name" binding:"required"`
}

type RequestDeleteCaseFolder struct {
	FolderID int64 `json:"folder_id" binding:"required"`
}

type RequestMigrateCaseFolder struct {
	SourceFolderID int64 `json:"source_folder_id" binding:"required"`
	TargetParentID int64 `json:"target_parent_id" binding:"required"`
	IsCopy         bool  `json:"is_copy" binding:"required"`
}

package model

type CasesFloder struct {
	FolderID   int64   `json:"folder_id" gorm:"primaryKey;autoIncrement"`
	FolderName string  `json:"folder_name" gorm:"type:varchar(255);not null"`
	ParentID   int64   `json:"parent_id" gorm:"type:int;not null"`
	CaseUIDs   []int64 `json:"case_uids" gorm:"type:int[]"`
}

// RequestAddTestCase 新增用例。Version 是必填的分支名——所有用例的编写都
// 只能发生在某个版本分支里，UID 由基线分配，但编辑内容写进这个分支自己的
// history 表，直到 MergeVersion 才会出现在基线上。
type RequestAddTestCase struct {
	FolderID  int64      `json:"folder_id" binding:"required"`
	Version   string     `json:"version" binding:"required"`
	TestCases []TestCase `json:"test_cases" binding:"required"`
}

// RequestUpdateTestCase 编辑已有用例，同样只能在某个版本分支里进行。
type RequestUpdateTestCase struct {
	FolderID  int64      `json:"folder_id" binding:"required"`
	Version   string     `json:"version" binding:"required"`
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

// RequestCreateCaseFolder 新建一个目录。ParentID=0 是特例：不引用任何已存在
// 的目录，创建的是一个新的顶层（产品级）目录，和启动时自动创建的那个顶层
// 目录并列存在——所以这里不是 binding:"required"，0 是合法输入。
type RequestCreateCaseFolder struct {
	FolderName string `json:"folder_name" binding:"required"`
	ParentID   int64  `json:"parent_id"`
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

// RequestCreateVersion 创建一个新的版本分支（类似 git 里开一个新分支）。
type RequestCreateVersion struct {
	Version string `json:"version" binding:"required"`
}

// RequestMergeVersion 把分支里的用例合并进基线；分支名来自路由的 {name}。
// UIDs 为空表示合并分支里全部被编辑过的用例。
type RequestMergeVersion struct {
	UIDs []int64 `json:"uids,omitempty"`
}

// RequestPullVersion 把 UIDs 在基线上当前已合并的内容拉取进分支（分支名来自
// 路由的 {name}），作为这些用例在分支里的起始本地 revision。
type RequestPullVersion struct {
	UIDs []int64 `json:"uids" binding:"required"`
}

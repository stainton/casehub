// Package store defines the persistence abstraction that cmd/manager/app
// depends on for organizing test cases into folders. It owns exactly one
// table, case_folders, and never touches the case baseline tables
// (base_cases, case_history, test_executions) or the per-version branch
// tables — those are owned exclusively by cmd/manager/app/basecase.
package store

import (
	"context"

	"github.com/stainton/casehub/pkg/model"
)

// Store persists case folders and the case UIDs organized under them.
type Store interface {
	// Close releases the underlying resources (connections, pools, etc).
	Close()

	// CreateFolder 在 parentID 下创建一个新目录。
	CreateFolder(ctx context.Context, name string, parentID int64) (*model.CasesFloder, error)
	// GetFolder 查询单个目录，不存在时返回 (nil, nil)。
	GetFolder(ctx context.Context, folderID int64) (*model.CasesFloder, error)
	// ListFolders 返回全部目录（扁平列表），供前端在本地按 parent_id 拼出目录树。
	ListFolders(ctx context.Context) ([]*model.CasesFloder, error)
	// RenameFolder 修改目录名称。
	RenameFolder(ctx context.Context, folderID int64, name string) error
	// DeleteFolder 删除一个目录。
	DeleteFolder(ctx context.Context, folderID int64) error
	// HasChildren 判断目录下是否还有子目录。
	HasChildren(ctx context.Context, folderID int64) (bool, error)
	// MoveFolder 将目录挂到新的父目录下。子目录引用的是被移动目录自身的 ID，
	// 不受影响，所以整棵子树会随之一起移动。
	MoveFolder(ctx context.Context, folderID, targetParentID int64) error
	// CopyFolder 在 targetParentID 下创建一个新目录，复制 folderID 的名称与
	// CaseUIDs（浅拷贝：不递归复制子目录）。folderID 不存在时返回 (nil, nil)。
	CopyFolder(ctx context.Context, folderID, targetParentID int64) (*model.CasesFloder, error)

	// AddCaseUIDs 将 uids 合并进目录的 CaseUIDs（去重）。
	AddCaseUIDs(ctx context.Context, folderID int64, uids []int64) error
	// RemoveCaseUIDs 将 uids 从目录的 CaseUIDs 中移除。
	RemoveCaseUIDs(ctx context.Context, folderID int64, uids []int64) error
}

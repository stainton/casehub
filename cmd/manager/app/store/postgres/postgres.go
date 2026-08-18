// Package postgres is the Postgres implementation of store.Store.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stainton/casehub/cmd/manager/app/store"
	"github.com/stainton/casehub/pkg/model"
)

var _ store.Store = (*Store)(nil)

// Config holds everything needed to connect to and bootstrap the database.
type Config struct {
	// ConnString connects to DBName directly.
	ConnString string
	// AdminConnString connects to the "postgres" maintenance database, used
	// to create DBName before it exists.
	AdminConnString string
	DBName          string
}

// Store is the Postgres-backed store.Store implementation.
type Store struct {
	pool *pgxpool.Pool
}

// New creates the database if it doesn't exist, opens a connection pool, and
// ensures case_folders、其 ID 序列、以及根目录（FolderID=1，ParentID=0，
// FolderName="基线"）都已就位。The caller owns the returned Store and must
// Close it.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if err := createDatabaseIfNotExists(ctx, cfg); err != nil {
		return nil, err
	}

	pool, err := pgxpool.New(ctx, cfg.ConnString)
	if err != nil {
		return nil, err
	}

	if _, err := pool.Exec(ctx, CreateTableCaseFolders); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, CreateFolderIDSeq); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, InsertRootFolderIfNotExists); err != nil {
		pool.Close()
		return nil, err
	}

	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
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

// scanFolder 将 pgx.Row 扫描到 model.CasesFloder 结构体中，并返回该结构体的指针和可能的错误。
func scanFolder(row pgx.Row) (*model.CasesFloder, error) {
	f := &model.CasesFloder{}
	if err := row.Scan(&f.FolderID, &f.FolderName, &f.ParentID, &f.CaseUIDs); err != nil {
		return nil, err
	}
	return f, nil
}

// CreateFolder 在 parentID 下创建一个新目录。
func (s *Store) CreateFolder(ctx context.Context, name string, parentID int64) (*model.CasesFloder, error) {
	f := &model.CasesFloder{FolderName: name, ParentID: parentID, CaseUIDs: []int64{}}
	if err := s.pool.QueryRow(ctx, InsertFolder, name, parentID, f.CaseUIDs).Scan(&f.FolderID); err != nil {
		return nil, err
	}
	return f, nil
}

// GetFolder 查询单个目录，不存在时返回 (nil, nil)。
func (s *Store) GetFolder(ctx context.Context, folderID int64) (*model.CasesFloder, error) {
	f, err := scanFolder(s.pool.QueryRow(ctx, SelectFolder, folderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ListFolders 返回全部目录（扁平列表）。
func (s *Store) ListFolders(ctx context.Context) ([]*model.CasesFloder, error) {
	rows, err := s.pool.Query(ctx, SelectAllFolders)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.CasesFloder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RenameFolder 修改目录名称。
func (s *Store) RenameFolder(ctx context.Context, folderID int64, name string) error {
	_, err := s.pool.Exec(ctx, UpdateFolderName, name, folderID)
	return err
}

// DeleteFolder 删除一个目录。
func (s *Store) DeleteFolder(ctx context.Context, folderID int64) error {
	_, err := s.pool.Exec(ctx, DeleteFolder, folderID)
	return err
}

// HasChildren 判断目录下是否还有子目录。
func (s *Store) HasChildren(ctx context.Context, folderID int64) (bool, error) {
	var count int
	if err := s.pool.QueryRow(ctx, CountChildFolders, folderID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// MoveFolder 将目录挂到新的父目录下。子目录引用的是被移动目录自身的 ID，
// 不受影响，所以整棵子树会随之一起移动。
func (s *Store) MoveFolder(ctx context.Context, folderID, targetParentID int64) error {
	_, err := s.pool.Exec(ctx, UpdateFolderParent, targetParentID, folderID)
	return err
}

// CopyFolder 在 targetParentID 下创建一个新目录，复制 folderID 的名称与
// CaseUIDs（浅拷贝：不递归复制子目录）。folderID 不存在时返回 (nil, nil)。
func (s *Store) CopyFolder(ctx context.Context, folderID, targetParentID int64) (*model.CasesFloder, error) {
	src, err := s.GetFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, nil
	}

	dst := &model.CasesFloder{
		FolderName: src.FolderName,
		ParentID:   targetParentID,
		CaseUIDs:   append([]int64{}, src.CaseUIDs...),
	}
	if err := s.pool.QueryRow(ctx, InsertFolder, dst.FolderName, dst.ParentID, dst.CaseUIDs).Scan(&dst.FolderID); err != nil {
		return nil, err
	}
	return dst, nil
}

// AddCaseUIDs 将 uids 合并进目录的 CaseUIDs（去重）。
func (s *Store) AddCaseUIDs(ctx context.Context, folderID int64, uids []int64) error {
	return s.mutateCaseUIDs(ctx, folderID, func(current []int64) []int64 {
		return mergeUnique(current, uids)
	})
}

// RemoveCaseUIDs 将 uids 从目录的 CaseUIDs 中移除。
func (s *Store) RemoveCaseUIDs(ctx context.Context, folderID int64, uids []int64) error {
	return s.mutateCaseUIDs(ctx, folderID, func(current []int64) []int64 {
		return excludeAll(current, uids)
	})
}

// mutateCaseUIDs 在事务中加锁读出目录当前的 CaseUIDs、应用 mutate、写回，
// 避免并发调用时的丢失更新。
func (s *Store) mutateCaseUIDs(ctx context.Context, folderID int64, mutate func([]int64) []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var current []int64
	if err := tx.QueryRow(ctx, SelectFolderCaseUIDsForUpdate, folderID).Scan(&current); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, UpdateFolderCaseUIDs, mutate(current), folderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mergeUnique(current, add []int64) []int64 {
	seen := make(map[int64]bool, len(current)+len(add))
	out := append([]int64{}, current...)
	for _, v := range current {
		seen[v] = true
	}
	for _, v := range add {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func excludeAll(current, remove []int64) []int64 {
	drop := make(map[int64]bool, len(remove))
	for _, v := range remove {
		drop[v] = true
	}
	out := make([]int64, 0, len(current))
	for _, v := range current {
		if !drop[v] {
			out = append(out, v)
		}
	}
	return out
}

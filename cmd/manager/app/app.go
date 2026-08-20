package app

import (
	"context"
	"time"

	"github.com/stainton/casehub/cmd/manager/app/basecase"
	"github.com/stainton/casehub/cmd/manager/app/store"
	"github.com/stainton/casehub/cmd/manager/app/store/postgres"
)

// Run 是 manager 的独立入口点：管理 case_folders 表（用例的目录组织），以及
// 用例基线和各版本分支（见 basecase 包）——manager 现在是这些表唯一的写入方，
// 不再需要通过 HTTP 调用一个独立的 case API。
func Run(opts *Options) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, bc, err := Bootstrap(ctx, opts)
	if err != nil {
		return err
	}
	defer st.Close()
	defer bc.Close()

	return Serve(opts, NewManagerMux(st, bc))
}

// Bootstrap 连接到 postgres，如果 case_folders 表、用例基线三张表和版本分支
// 注册表尚不存在，则创建它们。调用者拥有返回的 store.Store 和
// *basecase.BaseCase，并且必须都 Close。
func Bootstrap(ctx context.Context, opts *Options) (store.Store, *basecase.BaseCase, error) {
	st, err := postgres.New(ctx, postgres.Config{
		ConnString:      opts.PostgresConnectionString(),
		AdminConnString: opts.PostgresAdminConnectionString(),
		DBName:          opts.dbName,
	})
	if err != nil {
		return nil, nil, err
	}

	bc, err := basecase.New(ctx, basecase.Config{
		ConnString:      opts.PostgresConnectionString(),
		AdminConnString: opts.PostgresAdminConnectionString(),
		DBName:          opts.dbName,
	})
	if err != nil {
		st.Close()
		return nil, nil, err
	}

	return st, bc, nil
}

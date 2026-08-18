package app

import (
	"context"
	"time"

	"github.com/stainton/casehub/cmd/manager/app/client"
	"github.com/stainton/casehub/cmd/manager/app/store"
	"github.com/stainton/casehub/cmd/manager/app/store/postgres"
)

// Run 是 manager 的独立入口点：管理 case_folders 表（用例的目录组织），并通过
// HTTP 调用 case API 来创建/编辑用例本身（见 client 包），不直接访问 case API
// 拥有的数据库表。
func Run(opts *Options) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, err := Bootstrap(ctx, opts)
	if err != nil {
		return err
	}
	defer st.Close()

	return Serve(opts, NewManagerMux(st, NewAPIClient(opts)))
}

// Bootstrap 连接到 postgres，如果数据库、case_folders 表和根目录尚不存在，则创建它们。
// 调用者拥有返回的 store.Store，并且必须关闭它。
func Bootstrap(ctx context.Context, opts *Options) (store.Store, error) {
	return postgres.New(ctx, postgres.Config{
		ConnString:      opts.PostgresConnectionString(),
		AdminConnString: opts.PostgresAdminConnectionString(),
		DBName:          opts.dbName,
	})
}

// NewAPIClient 创建一个访问 case API 的客户端，使用 opts 中配置的 -api-base-url。
// 导出以便其他入口点（例如 mock 的前端+manager 组合服务器）可以重用相同的设置。
func NewAPIClient(opts *Options) *client.Client {
	return client.New(opts.apiBaseURL)
}

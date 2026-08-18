package app

import (
	"context"
	"time"

	"github.com/stainton/casehub/cmd/api/app/store"
	"github.com/stainton/casehub/cmd/api/app/store/postgres"
)

// Run 是独立的后端入口点（参见 cmd/api/main.go）：它仅提供 case API 服务，不涉及任何前端。
func Run(opts *Options) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, err := Bootstrap(ctx, opts)
	if err != nil {
		return err
	}
	defer st.Close()

	return Serve(opts, NewAPIMux(st))
}

// Bootstrap 连接到 postgres，如果数据库和模式尚不存在，则创建它们。
// 调用者拥有返回的 store.Store，并且必须关闭它。
// 导出以便其他入口点（例如 mock 的前端+后端组合服务器）可以重用相同的设置而无需重复。
func Bootstrap(ctx context.Context, opts *Options) (store.Store, error) {
	return postgres.New(ctx, postgres.Config{
		ConnString:      opts.PostgresConnectionString(),
		AdminConnString: opts.PostgresAdminConnectionString(),
		DBName:          opts.dbName,
	})
}

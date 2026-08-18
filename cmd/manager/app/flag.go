package app

import (
	"flag"
	"fmt"
)

type Options struct {
	dbUser     string
	dbPassword string
	dbHost     string
	dbPort     int
	dbName     string
	dbSslMode  string
	httpPort   int
	apiBaseURL string
}

func (o *Options) AddFlags() {
	flag.StringVar(&o.dbUser, "db-user", "appuser", "Database user")
	flag.StringVar(&o.dbPassword, "db-password", "ChangeMe_2026", "Database password")
	flag.StringVar(&o.dbHost, "db-host", "postgres", "Database host")
	flag.IntVar(&o.dbPort, "db-port", 5432, "Database port")
	flag.StringVar(&o.dbName, "db-name", "casehub", "Database name (shared with the case API; manager only ever touches its own case_folders table in it)")
	flag.StringVar(&o.dbSslMode, "db-ssl-mode", "disable", "Database SSL mode (disable, require, verify-ca, verify-full)")
	flag.IntVar(&o.httpPort, "http-port", 8081, "HTTP server port")
	flag.StringVar(&o.apiBaseURL, "api-base-url", "http://localhost:8080", "Base URL of the case API that manager reads/writes test cases through")
	flag.Parse()
}

func (o *Options) PostgresConnectionString() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s", o.dbUser, o.dbPassword, o.dbHost, o.dbPort, o.dbName, o.dbSslMode)
}

// PostgresAdminConnectionString 连接到默认的 "postgres" 维护数据库，用于在 o.dbName 存在之前创建它。
func (o *Options) PostgresAdminConnectionString() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/postgres?sslmode=%s", o.dbUser, o.dbPassword, o.dbHost, o.dbPort, o.dbSslMode)
}

func NewOptions() *Options {
	return &Options{}
}

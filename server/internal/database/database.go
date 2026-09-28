package database

import (
	"database/sql"
	"fmt"

	"knowforge/server/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// mysqlStringSize 无显式 size 的字符串列在 MySQL 上的推导大小。
// 取值超过 2^24 使驱动推导 longtext（4GB），避免文档正文、UI 文案包等长文本
// 被建成 64KB 的 text，也避免对旧库 longtext 列的收缩式迁移；
// SQLite/PostgreSQL 的 text 本身无长度限制，不受此值影响。
const mysqlStringSize = 1<<24 + 1

const (
	TypeSQLite   = "sqlite"
	TypeMySQL    = "mysql"
	TypePostgres = "postgres"
)

// SupportedTypes 安装向导中可选的数据库类型
func SupportedTypes() []string {
	return []string{TypeSQLite, TypeMySQL, TypePostgres}
}

func Supported(t string) bool {
	for _, item := range SupportedTypes() {
		if item == t {
			return true
		}
	}
	return false
}

// mysqlDialector 构造 MySQL 方言；DefaultStringSize 决定无显式 size 字符串列的推导类型，
// 见 mysqlStringSize 注释。
func mysqlDialector(dsn string) gorm.Dialector {
	return mysql.New(mysql.Config{DSN: dsn, DefaultStringSize: mysqlStringSize})
}

func mysqlDSN(cfg config.DatabaseConfig) string {
	if cfg.Port == 0 {
		cfg.Port = 3306
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Name)
}

func postgresDSN(cfg config.DatabaseConfig) string {
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name)
}

// Open 根据配置打开 GORM 连接
func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	switch cfg.Type {
	case TypeSQLite:
		path := cfg.Path
		if path == "" {
			path = "./data/knowforge.db"
		}
		// _txlock=immediate：事务开始即取得写锁，并发写事务按 busy_timeout 排队。默认的 deferred 事务先读后写时
		// 可能无法升级为写锁而立即报 database is locked（busy_timeout 对这种情况不生效），并发兑换、下单等会偶发失败。
		db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"), &gorm.Config{})
		if err != nil {
			// 纯 Go SQLite 驱动把"无法打开文件"(14) 误报为 out of memory，翻译成人话
			return nil, fmt.Errorf("无法打开 SQLite 数据库文件 %s: %w（通常为目录不存在或无写入权限）", path, err)
		}
		return db, nil
	case TypeMySQL:
		return gorm.Open(mysqlDialector(mysqlDSN(cfg)), &gorm.Config{})
	case TypePostgres:
		return gorm.Open(postgres.Open(postgresDSN(cfg)), &gorm.Config{})
	default:
		return nil, fmt.Errorf("不支持的数据库类型: %s", cfg.Type)
	}
}

// Ping 验证数据库连通性
func Ping(gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

// Test 使用给定配置测试数据库连接，返回 *sql.DB 便于调用方关闭
func Test(cfg config.DatabaseConfig) (*sql.DB, error) {
	gdb, err := Open(cfg)
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}

// Package testdb 测试用数据库：按环境变量选择 SQLite（默认）、PostgreSQL 或 MySQL，每个测试一个独立的新库，测试结束后删除。
//
//	KNOWFORGE_TEST_DB=postgres|mysql       不设置时使用 SQLite
//	KNOWFORGE_TEST_DB_HOST / _PORT         默认 127.0.0.1 与各自的默认端口
//	KNOWFORGE_TEST_DB_USER / _PASSWORD     需要有建库、删库权限
//
// CI 用它在三种数据库上运行同一套测试。
package testdb

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"gorm.io/gorm"

	"knowforge/server/internal/config"
	"knowforge/server/internal/database"
)

// Kind 当前测试使用的数据库类型。
func Kind() string {
	switch k := os.Getenv("KNOWFORGE_TEST_DB"); k {
	case database.TypePostgres, database.TypeMySQL:
		return k
	}
	return database.TypeSQLite
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// server 连接数据库服务器（不指定库）所用的配置。
func server(name string) config.DatabaseConfig {
	cfg := config.DatabaseConfig{Type: Kind(), Host: env("KNOWFORGE_TEST_DB_HOST", "127.0.0.1"), Name: name,
		User: env("KNOWFORGE_TEST_DB_USER", "root"), Password: os.Getenv("KNOWFORGE_TEST_DB_PASSWORD")}
	switch cfg.Type {
	case database.TypePostgres:
		cfg.Port, cfg.User = 5432, env("KNOWFORGE_TEST_DB_USER", "postgres")
	case database.TypeMySQL:
		cfg.Port = 3306
	}
	if p, err := strconv.Atoi(os.Getenv("KNOWFORGE_TEST_DB_PORT")); err == nil {
		cfg.Port = p
	}
	return cfg
}

// Config 为测试新建一个空库并返回其配置（SQLite 为临时目录中的文件），测试结束后删除。
func Config(t testing.TB) config.DatabaseConfig {
	t.Helper()
	if Kind() == database.TypeSQLite {
		return config.DatabaseConfig{Type: database.TypeSQLite, Path: filepath.Join(t.TempDir(), "test.db")}
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "kftest_" + hex.EncodeToString(b)
	adminName := "postgres"
	if Kind() == database.TypeMySQL {
		adminName = "mysql"
	}
	admin, err := database.Open(server(adminName))
	if err != nil {
		t.Fatalf("连接测试数据库服务器失败: %v", err)
	}
	create := "CREATE DATABASE " + name
	if Kind() == database.TypeMySQL {
		create += " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
	}
	if err := admin.Exec(create).Error; err != nil {
		t.Fatalf("创建测试库失败: %v", err)
	}
	closeDB(admin)
	t.Cleanup(func() {
		admin, err := database.Open(server(adminName))
		if err != nil {
			return
		}
		defer closeDB(admin)
		if Kind() == database.TypePostgres {
			admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		} else {
			admin.Exec("DROP DATABASE IF EXISTS " + name)
		}
	})
	return server(name)
}

// InstallJSON 安装向导 /setup/install 请求中 database 字段的 JSON（新建的独立测试库）。
func InstallJSON(t testing.TB) string {
	t.Helper()
	if Kind() == database.TypeSQLite {
		return `{"type":"sqlite"}` // 与此前一致：库文件放在测试的数据目录中
	}
	raw, _ := json.Marshal(Config(t))
	return string(raw)
}

// Open 打开一个新的独立测试库（测试结束时关闭连接并删除）。
func Open(t testing.TB) *gorm.DB {
	t.Helper()
	cfg := Config(t)
	db, err := database.Open(cfg)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { closeDB(db) })
	return db
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// Describe 当前测试数据库的说明（日志用）。
func Describe() string {
	cfg := server("")
	if Kind() == database.TypeSQLite {
		return "sqlite"
	}
	return fmt.Sprintf("%s@%s:%d", cfg.Type, cfg.Host, cfg.Port)
}

// InstallMap 与 InstallJSON 相同，供以 map 构造请求体的测试使用。
func InstallMap(t testing.TB) map[string]any {
	t.Helper()
	var out map[string]any
	_ = json.Unmarshal([]byte(InstallJSON(t)), &out)
	return out
}

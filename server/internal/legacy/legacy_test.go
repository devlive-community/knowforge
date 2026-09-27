package legacy_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/glebarez/sqlite"

	"knowforge/server/internal/app"
	"knowforge/server/internal/config"
	"knowforge/server/internal/legacy"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 旧库迁移集成测试：sqlite 模拟旧版 MySQL schema → 迁移 → 校验数据无损与幂等
func TestLegacyMigration(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	// 目标库完成安装（自带 admin）
	raw, _ := json.Marshal(map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "迁移测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	})
	resp, err := http.Post(ts.URL+"/api/v1/setup/install", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}
	resp.Body.Close()

	// ── 构造旧库（旧版 schema 的 sqlite 等价物）──
	legacyDB, err := sql.Open(sqlite.DriverName, filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("打开旧库失败: %v", err)
	}
	defer legacyDB.Close()
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := legacyDB.Exec(q, args...); err != nil {
			t.Fatalf("执行 %s: %v", q, err)
		}
	}
	must(`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE, email TEXT UNIQUE,
		password TEXT, role TEXT, avatar TEXT, last_login_at TIMESTAMP, is_active BOOLEAN,
		bio TEXT, github_url TEXT, created_at TIMESTAMP, updated_at TIMESTAMP)`)
	must(`CREATE TABLE site_configs (id INTEGER PRIMARY KEY AUTOINCREMENT, config_key TEXT UNIQUE,
		config_value TEXT, description TEXT, created_at TIMESTAMP, updated_at TIMESTAMP)`)
	must(`CREATE TABLE user_authentications (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER,
		provider TEXT, provider_id TEXT, provider_username TEXT, provider_email TEXT, access_token TEXT,
		refresh_token TEXT, token_expires_at TIMESTAMP, is_primary BOOLEAN, created_at TIMESTAMP, updated_at TIMESTAMP)`)
	must(`CREATE TABLE books (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT, description TEXT,
		cover_image TEXT, slug TEXT UNIQUE, user_id INTEGER, status TEXT, is_public BOOLEAN,
		view_count INTEGER, order_col TEXT, order_dir TEXT, chapter_prefix TEXT,
		created_at TIMESTAMP, updated_at TIMESTAMP)`)
	must(`CREATE TABLE documents (id INTEGER PRIMARY KEY AUTOINCREMENT, book_id INTEGER, parent_id INTEGER,
		title TEXT, slug TEXT, content TEXT, user_id INTEGER, sort_order INTEGER, status TEXT,
		created_at TIMESTAMP, updated_at TIMESTAMP)`)

	hash, _ := bcrypt.GenerateFromPassword([]byte("legacy-pass"), bcrypt.DefaultCost)
	now := time.Now().UTC().Truncate(time.Second)
	// 旧用户：admin 与目标冲突（跳过），alice/bob 正常迁移；bob 无邮箱
	must(`INSERT INTO users (id, username, email, password, role, avatar, bio, github_url, is_active, created_at, updated_at)
		VALUES (1, 'admin', 'old-admin@test.local', ?, 'admin', '', '老管理员', '', 1, ?, ?)`, string(hash), now, now)
	must(`INSERT INTO users (id, username, email, password, role, avatar, bio, github_url, is_active, created_at, updated_at)
		VALUES (2, 'alice', 'alice@test.local', ?, 'user', '/uploads/a.png', '作家', 'https://github.com/alice', 1, ?, ?)`, string(hash), now, now)
	must(`INSERT INTO users (id, username, email, password, role, is_active, created_at, updated_at)
		VALUES (3, 'bob', NULL, ?, 'user', 1, ?, ?)`, string(hash), now, now)
	// 书籍：覆盖排序规则/章节前缀等新增字段的平移
	must(`INSERT INTO books (id, title, description, cover_image, slug, user_id, status, is_public,
		view_count, order_col, order_dir, chapter_prefix, created_at, updated_at)
		VALUES (1, '旧版之书', '来自旧系统的书', '/uploads/old-cover.png', 'legacy-book', 2, 'published', 1,
		128, 'updated_at', 'asc', '第', ?, ?)`, now, now)
	must(`INSERT INTO books (id, title, slug, user_id, status, is_public, view_count, created_at, updated_at)
		VALUES (2, '没有排序字段的书', 'legacy-book-2', 2, 'draft', 0, 0, ?, ?)`, now, now)
	// 章节：三章成树（第二章挂第一章下）
	must(`INSERT INTO documents (id, book_id, parent_id, title, slug, content, user_id, sort_order, status, created_at, updated_at)
		VALUES (1, 1, NULL, '旧版第一章', 'old-chapter-1', '# 正文一', 2, 0, 'published', ?, ?)`, now, now)
	must(`INSERT INTO documents (id, book_id, parent_id, title, slug, content, user_id, sort_order, status, created_at, updated_at)
		VALUES (2, 1, 1, '旧版子章节', 'old-child', '子内容', 2, 1, 'published', ?, ?)`, now, now)
	must(`INSERT INTO documents (id, book_id, parent_id, title, slug, content, user_id, sort_order, status, created_at, updated_at)
		VALUES (3, 2, NULL, '草稿章节', 'old-draft', '草稿', 2, 0, 'draft', ?, ?)`, now, now)
	must(`INSERT INTO user_authentications (id, user_id, provider, provider_id, provider_username, provider_email, is_primary, created_at, updated_at)
		VALUES (1, 2, 'github', '10001', 'alice-gh', 'alice@test.local', 1, ?, ?)`, now, now)
	must(`INSERT INTO site_configs (config_key, config_value, description) VALUES ('old_notice', '旧站公告', '旧站配置')`)
	must(`INSERT INTO site_configs (config_key, config_value, description) VALUES ('version', '1.0.0', '旧版本')`)

	// ── dry-run：只统计 ──
	dry, err := legacy.RunMigration(a.DB, legacyDB, true)
	if err != nil {
		t.Fatalf("dry-run 失败: %v", err)
	}
	if dry.Users != 2 || dry.UsersSkipped != 1 {
		t.Fatalf("dry-run 用户统计异常: %+v", dry)
	}
	if dry.Books != 2 || dry.Documents != 3 || dry.Auths != 1 {
		t.Fatalf("dry-run 内容统计异常: %+v", dry)
	}
	var userCount int64
	a.DB.Model(&models.User{}).Count(&userCount)
	if userCount != 1 { // dry-run 不写入（只有安装的 admin）
		t.Fatalf("dry-run 不应写入用户: %d", userCount)
	}

	// ── 正式迁移 ──
	summary, err := legacy.RunMigration(a.DB, legacyDB, false)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if summary.Users != 2 || summary.UsersSkipped != 1 {
		t.Fatalf("用户迁移统计异常: %+v", summary)
	}
	if summary.Books != 2 || summary.BooksSkipped != 0 || summary.Documents != 3 {
		t.Fatalf("内容迁移统计异常: %+v", summary)
	}
	if summary.Auths != 1 || summary.SiteConfigs != 1 {
		t.Fatalf("绑定/配置迁移统计异常: %+v", summary)
	}

	// ── 数据校验 ──
	// 用户：密码哈希平移后可直接校验原密码
	var alice models.User
	if err := a.DB.Where("username = ?", "alice").First(&alice).Error; err != nil {
		t.Fatalf("迁移用户缺失: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(alice.Password), []byte("legacy-pass")) != nil {
		t.Fatal("迁移后原密码应可校验通过")
	}
	var bob models.User
	if err := a.DB.Where("username = ?", "bob").First(&bob).Error; err != nil {
		t.Fatalf("无邮箱用户迁移缺失: %v", err)
	}
	// 书籍：新增字段平移
	var book models.Book
	if err := a.DB.Where("slug = ?", "legacy-book").First(&book).Error; err != nil {
		t.Fatalf("迁移书籍缺失: %v", err)
	}
	if book.ViewCount != 128 || book.OrderCol != "updated_at" || book.OrderDir != "asc" || book.ChapterPrefix != "第" {
		t.Fatalf("书籍字段未平移: %+v", book)
	}
	// 章节树：parent_id 已重映射；内容无损
	var child models.Document
	if err := a.DB.Where("slug = ?", "old-child").First(&child).Error; err != nil {
		t.Fatalf("迁移章节缺失: %v", err)
	}
	var parent models.Document
	if err := a.DB.Where("slug = ?", "old-chapter-1").First(&parent).Error; err != nil {
		t.Fatalf("父章节缺失: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Fatalf("子章节 parent_id 未重映射: %v", child.ParentID)
	}
	if child.Content != "子内容" || parent.Content != "# 正文一" {
		t.Fatal("章节正文未无损迁移")
	}
	// 第三方绑定
	var auth models.UserAuthentication
	if err := a.DB.Where("provider = ? AND provider_id = ?", "github", "10001").First(&auth).Error; err != nil {
		t.Fatalf("第三方绑定迁移缺失: %v", err)
	}
	if auth.UserID != alice.ID {
		t.Fatalf("绑定未挂到迁移后的用户: %v vs %v", auth.UserID, alice.ID)
	}
	// 站点配置：自定义键迁入、保留键跳过
	var notice models.SiteConfig
	if err := a.DB.Where("config_key = ?", "old_notice").First(&notice).Error; err != nil {
		t.Fatal("自定义站点配置未迁移")
	}
	var versionCount int64
	a.DB.Model(&models.SiteConfig{}).Where("config_value = ?", "1.0.0").Count(&versionCount)
	if versionCount != 0 {
		t.Fatal("旧 version 不应迁入")
	}

	// ── 幂等：重复执行全部跳过 ──
	again, err := legacy.RunMigration(a.DB, legacyDB, false)
	if err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
	if again.Users != 0 || again.Books != 0 || again.Documents != 0 || again.Auths != 0 || again.SiteConfigs != 0 {
		t.Fatalf("重复迁移应零写入: %+v", again)
	}
	if again.UsersSkipped != 3 || again.BooksSkipped != 2 || again.AuthsSkipped != 1 {
		t.Fatalf("重复迁移跳过统计异常: %+v", again)
	}
}

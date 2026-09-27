package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// 控制台时间线接口集成测试（/admin/activity，user:manage 仅管理员）：
//   - 匿名 401、普通用户 403
//   - 管理员拿到 recent_users / recent_books，均为数组（防 nil→null）
//   - 按 created_at DESC 排序、含书籍作者 user 预加载
//   - 不限可见性：草稿/私有书籍也出现（区别于公开 /explore/latest）
func TestAdminActivity(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	client := &http.Client{Timeout: 10 * 1e9}
	request := func(method, path string, token string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(nil))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	// 安装 + alice 注册
	post := func(path string, body any, token string) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	_, install := post("/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "时间线测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	_, reg := post("/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	aliceToken := reg["data"].(map[string]any)["token"].(string)

	// alice 建两本：一本公开已发布、一本私有草稿（验证时间线不限可见性）
	post("/api/v1/books", map[string]any{"title": "公开之书", "status": "published", "is_public": true}, aliceToken)
	post("/api/v1/books", map[string]any{"title": "私有草稿", "status": "draft", "is_public": false}, aliceToken)

	// 1. 权限拒绝：匿名 401、普通用户 403
	if status, _ := request(http.MethodGet, "/api/v1/admin/activity", ""); status != http.StatusUnauthorized {
		t.Fatalf("匿名访问时间线应 401: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/activity", aliceToken); status != http.StatusForbidden {
		t.Fatalf("普通用户访问时间线应 403: %d", status)
	}

	// 2. 管理员拿到数据，均为数组
	status, body := request(http.MethodGet, "/api/v1/admin/activity", adminToken)
	if status != http.StatusOK {
		t.Fatalf("管理员访问时间线应 200: %d", status)
	}
	data := body["data"].(map[string]any)
	users, okUsers := data["recent_users"].([]any)
	books, okBooks := data["recent_books"].([]any)
	if !okUsers || users == nil {
		t.Fatalf("recent_users 必须为数组（防空列表序列化为 null）: %v", data["recent_users"])
	}
	if !okBooks || books == nil {
		t.Fatalf("recent_books 必须为数组（防空列表序列化为 null）: %v", data["recent_books"])
	}

	// 3. 最近注册：alice 在 admin 之前（DESC，最新在前）
	if len(users) < 2 {
		t.Fatalf("应有至少 2 个最近用户: %d", len(users))
	}
	if users[0].(map[string]any)["username"] != "alice" {
		t.Fatalf("最近注册首位应为 alice: %v", users[0].(map[string]any)["username"])
	}

	// 4. 最近建书：私有草稿也出现（不限可见性），且首位最新
	if len(books) < 2 {
		t.Fatalf("应有至少 2 本最近书籍（含私有草稿）: %d", len(books))
	}
	if books[0].(map[string]any)["title"] != "私有草稿" {
		t.Fatalf("最近建书首位应为「私有草稿」: %v", books[0].(map[string]any)["title"])
	}
	// 作者预加载：user.username == alice
	bookUser := books[0].(map[string]any)["user"]
	if bookUser == nil || bookUser.(map[string]any)["username"] != "alice" {
		t.Fatalf("最近建书应预加载作者 user，且为 alice: %v", bookUser)
	}
}

// 空库时间线：recent_users/recent_books 仍为数组而非 null（仅安装后的管理员）
func TestAdminActivityEmpty(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	client := &http.Client{Timeout: 10 * 1e9}
	post := func(path string, body any) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}
	_, install := post("/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "空时间线站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	})
	adminToken := install["data"].(map[string]any)["token"].(string)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/activity", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/activity: %v", err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	data := payload["data"].(map[string]any)
	// 安装后只有一个管理员，无书籍：recent_books 必须是空数组而非 null
	books, ok := data["recent_books"].([]any)
	if !ok || books == nil {
		t.Fatalf("空库 recent_books 必须为数组而非 null: %v", data["recent_books"])
	}
	if len(books) != 0 {
		t.Fatalf("空库 recent_books 应为空数组: %d", len(books))
	}
}

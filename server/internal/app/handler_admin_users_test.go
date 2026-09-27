package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// fmtUID 将用户 ID 格式化为路径片段
func fmtUID(n uint64) string { return strconv.FormatUint(n, 10) }

// uid 从列表元素取 id
func uid(item any) uint64 {
	return uint64(item.(map[string]any)["id"].(float64))
}

// 用户管理后台集成测试（user:manage，仅管理员）：
//   - 匿名与普通用户访问被拒绝（401/403）
//   - 管理员列表/筛选/关键字检索
//   - 角色变更与状态启停
//   - 边界：操作自身被拒、保留最后一位启用管理员、删除拥有书籍的用户被拒
func TestAdminUsers(t *testing.T) {
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
	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
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

	// 安装取得管理员令牌
	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "用户管理测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	adminID := uint64(install["data"].(map[string]any)["user"].(map[string]any)["id"].(float64))

	register := func(username string) (string, uint64) {
		_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"username": username, "email": username + "@test.local", "password": "secret123",
		}, "")
		data := reg["data"].(map[string]any)
		return data["token"].(string), uint64(data["user"].(map[string]any)["id"].(float64))
	}
	aliceToken, aliceID := register("alice")
	_, bobID := register("bob")
	_, carolID := register("carol") // carol 将被停用，用于状态筛选

	// 1. 权限拒绝：匿名 401、普通用户 403
	if status, _ := request(http.MethodGet, "/api/v1/admin/users", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("匿名访问后台用户列表应 401: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/users", nil, aliceToken); status != http.StatusForbidden {
		t.Fatalf("普通用户访问后台用户列表应 403: %d", status)
	}

	// 2. 管理员列表：items 为数组而非 null，包含 4 个用户
	status, list := request(http.MethodGet, "/api/v1/admin/users", nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("管理员列表应 200: %d %v", status, list)
	}
	data := list["data"].(map[string]any)
	items, ok := data["items"].([]any)
	if !ok || items == nil {
		t.Fatalf("列表 items 必须为数组（防空列表序列化为 null）: %v", data["items"])
	}
	if len(items) != 4 {
		t.Fatalf("应有 4 个用户，实际 %d", len(items))
	}

	// 3. 关键字检索：q=alice 仅命中 1 条
	status, list = request(http.MethodGet, "/api/v1/admin/users?q=alice", nil, adminToken)
	if status != http.StatusOK || list["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("关键字检索应命中 1 条: %v", list["data"].(map[string]any)["total"])
	}

	// 4. 角色筛选 role=admin：仅管理员本人
	status, list = request(http.MethodGet, "/api/v1/admin/users?role=admin", nil, adminToken)
	if status != http.StatusOK || list["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("角色筛选 admin 应命中 1 条: %v", list["data"].(map[string]any)["total"])
	}

	// 5. 停用 carol 后按 status=inactive 筛选命中 1 条
	if status, _ := request(http.MethodPut, "/api/v1/admin/users/"+fmtUID(carolID)+"/status",
		map[string]any{"is_active": false}, adminToken); status != http.StatusOK {
		t.Fatalf("停用 carol 失败")
	}
	status, list = request(http.MethodGet, "/api/v1/admin/users?status=inactive", nil, adminToken)
	if status != http.StatusOK || list["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("停用筛选应命中 1 条: %v", list["data"].(map[string]any)["total"])
	}

	// 5b. 排序 sort=last_login_at_desc：管理员已登录排在最前，三个新注册用户无登录记录排最后
	status, list = request(http.MethodGet, "/api/v1/admin/users?sort=last_login_at_desc", nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("排序查询应 200: %d", status)
	}
	firstItems := list["data"].(map[string]any)["items"].([]any)
	if uid(firstItems[0]) != adminID {
		t.Fatalf("按最近登录排序时首位应为已登录的管理员: %v", uid(firstItems[0]))
	}
	// 末位是从未登录者之一，last_login_at 应为 null
	if last := firstItems[len(firstItems)-1].(map[string]any)["last_login_at"]; last != nil {
		t.Fatalf("从未登录者应排最后且 last_login_at 为 null: %v", last)
	}

	// 5c. 非法 sort 值回退到默认（created_at DESC），不报错
	if status, _ := request(http.MethodGet, "/api/v1/admin/users?sort=evil;DROP", nil, adminToken); status != http.StatusOK {
		t.Fatalf("非法 sort 应回退默认而非报错: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/users?sort=last_login_at_desc&role=admin", nil, adminToken); status != http.StatusOK {
		t.Fatalf("排序可与筛选叠加: %d", status)
	}

	// 6. 角色变更：alice → admin 再降回 user；非法角色被拒
	status, updated := request(http.MethodPut, "/api/v1/admin/users/"+fmtUID(aliceID)+"/role",
		map[string]any{"role": "admin"}, adminToken)
	if status != http.StatusOK || updated["data"].(map[string]any)["role"] != "admin" {
		t.Fatalf("提升 alice 为管理员失败: %d %v", status, updated)
	}
	if status, _ := request(http.MethodPut, "/api/v1/admin/users/"+fmtUID(aliceID)+"/role",
		map[string]any{"role": "user"}, adminToken); status != http.StatusOK {
		t.Fatalf("降回 alice 失败: %d", status)
	}
	if status, _ := request(http.MethodPut, "/api/v1/admin/users/"+fmtUID(aliceID)+"/role",
		map[string]any{"role": "superuser"}, adminToken); status != http.StatusBadRequest {
		t.Fatalf("非法角色应 400: %d", status)
	}

	// 7. 边界：管理员不能操作自身（角色/状态/删除）
	if status, _ := request(http.MethodPut, "/api/v1/admin/users/"+fmtUID(adminID)+"/role",
		map[string]any{"role": "user"}, adminToken); status != http.StatusBadRequest {
		t.Fatalf("降级自身应被拒（保留最后管理员）: %d", status)
	}
	if status, _ := request(http.MethodPut, "/api/v1/admin/users/"+fmtUID(adminID)+"/status",
		map[string]any{"is_active": false}, adminToken); status != http.StatusBadRequest {
		t.Fatalf("停用自身应被拒: %d", status)
	}
	if status, _ := request(http.MethodDelete, "/api/v1/admin/users/"+fmtUID(adminID), nil, adminToken); status != http.StatusBadRequest {
		t.Fatalf("删除自身应被拒: %d", status)
	}

	// 8. 边界：删除不存在的用户 404
	if status, _ := request(http.MethodDelete, "/api/v1/admin/users/9999", nil, adminToken); status != http.StatusNotFound {
		t.Fatalf("删除不存在用户应 404: %d", status)
	}

	// 9. 边界：删除拥有书籍的用户被拒（alice 建一本书后再删她）
	if status, book := request(http.MethodPost, "/api/v1/books",
		map[string]any{"title": "alice 的书", "status": "published"}, aliceToken); status != http.StatusOK {
		t.Fatalf("alice 建书失败（前置条件）: %d %v", status, book)
	}
	if status, _ := request(http.MethodDelete, "/api/v1/admin/users/"+fmtUID(aliceID), nil, adminToken); status != http.StatusBadRequest {
		t.Fatalf("删除拥有书籍的用户应 400: %d", status)
	}

	// 10. 边界：删除无书籍的 bob 成功，删后列表应少一人
	if status, _ := request(http.MethodDelete, "/api/v1/admin/users/"+fmtUID(bobID), nil, adminToken); status != http.StatusOK {
		t.Fatalf("删除 bob 应成功: %d", status)
	}
	_, list = request(http.MethodGet, "/api/v1/admin/users", nil, adminToken)
	if list["data"].(map[string]any)["total"].(float64) != 3 {
		t.Fatalf("删除后应剩 3 个用户: %v", list["data"].(map[string]any)["total"])
	}
}

// TestAdminDeleteUserWithRelatedData 覆盖：用户存在关联数据（如第三方绑定、阅读进度）时仍能成功删除（不因外键失败）。
func TestAdminDeleteUserWithRelatedData(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	client := &http.Client{Timeout: 10e9}
	req := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}

	_, installed := req(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "删除测试"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := installed["data"].(map[string]any)["token"].(string)
	_, reg := req(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "victim", "email": "victim@test.local", "password": "secret123",
	}, "")
	uidVal := uint(reg["data"].(map[string]any)["user"].(map[string]any)["id"].(float64))

	// 关联数据：第三方绑定（外键来源）+ 阅读进度
	a.DB.Exec("INSERT INTO user_authentications (user_id, provider, provider_id) VALUES (?,?,?)", uidVal, "github", "gh-123")
	if err := a.DB.Exec("INSERT INTO reading_progresses (user_id, book_id, doc_id, doc_slug) VALUES (?,?,?,?)", uidVal, 1, 1, "intro").Error; err != nil {
		t.Fatalf("写入阅读进度失败: %v", err)
	}

	status, resp := req(http.MethodDelete, "/api/v1/admin/users/"+fmtUID(uint64(uidVal)), nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("有关联数据的用户应能删除，实际 %d %v", status, resp)
	}
	var authCount, userCount int64
	a.DB.Table("user_authentications").Where("user_id = ?", uidVal).Count(&authCount)
	a.DB.Table("users").Where("id = ?", uidVal).Count(&userCount)
	if authCount != 0 || userCount != 0 {
		t.Fatalf("删除后用户与其绑定应清空，authCount=%d userCount=%d", authCount, userCount)
	}
}

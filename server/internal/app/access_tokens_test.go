package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 个人访问令牌：创建（明文只返回一次）、只读与读写范围、不能访问账号安全接口、不具备管理员权限、
// 只接受请求头、吊销与过期、最近使用记录、数量上限为权益。
func TestPersonalAccessTokens(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	send := func(req *http.Request) (int, map[string]any) {
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	do := func(token, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return send(req)
	}
	data := func(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"令牌"},"admin":{"username":"tk-admin","email":"tk-admin@test.local","password":"secret123"}}`)
	adminJWT := data(installed)["token"].(string)
	u := &models.User{Username: "tk-writer", Email: "tk-writer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	userJWT, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	create := func(jwt, body string) (int, map[string]any) {
		return do(jwt, http.MethodPost, "/api/v1/auth/tokens", body)
	}

	// 校验
	for _, body := range []string{`{"name":"","scope":"read"}`, `{"name":"x","scope":"admin"}`, `{"name":"x","scope":"read","expires_days":3}`} {
		if status, _ := create(userJWT, body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝 %s: %d", body, status)
		}
	}
	status, p := create(userJWT, `{"name":"只读脚本","scope":"read","expires_days":30}`)
	readToken, _ := data(p)["token"].(string)
	if status != http.StatusOK || !strings.HasPrefix(readToken, "kf_pat_") || data(p)["item"].(map[string]any)["prefix"] != readToken[:11] {
		t.Fatalf("创建令牌失败: %d %v", status, p)
	}
	_, p = create(userJWT, `{"name":"发布文档","scope":"write"}`)
	writeToken := data(p)["token"].(string)

	// 只读令牌：可读，不能写
	if status, p := do(readToken, http.MethodGet, "/api/v1/auth/me", ""); status != http.StatusOK || data(p)["username"] != "tk-writer" {
		t.Fatalf("只读令牌应能读取 /auth/me: %d %v", status, p)
	}
	if status, p := do(readToken, http.MethodPost, "/api/v1/books", `{"title":"只读不能建"}`); status != http.StatusForbidden || p["code"] != "TOKEN_FORBIDDEN" {
		t.Fatalf("只读令牌不能写: %d %v", status, p)
	}
	// 读写令牌：可以写普通接口
	if status, p := do(writeToken, http.MethodPost, "/api/v1/books", `{"title":"令牌建的书"}`); status != http.StatusOK {
		t.Fatalf("读写令牌应能建书: %d %v", status, p)
	}
	// 任何令牌都不能访问账号安全接口（含令牌管理）
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/auth/password", `{"old_password":"x","new_password":"y"}`},
		{http.MethodPost, "/api/v1/auth/tokens", `{"name":"套娃","scope":"write"}`},
		{http.MethodGet, "/api/v1/auth/tokens", ""},
		{http.MethodPost, "/api/v1/auth/account/deletion", `{}`},
	} {
		if status, _ := do(writeToken, c.method, c.path, c.body); status != http.StatusForbidden {
			t.Fatalf("令牌不能访问 %s %s: %d", c.method, c.path, status)
		}
	}
	// 只接受请求头：Cookie 中的令牌不生效
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "knowforge_token", Value: readToken})
	if status, _ := send(req); status != http.StatusUnauthorized {
		t.Fatalf("Cookie 中的访问令牌不应生效: %d", status)
	}
	// 最近使用记录
	var used models.PersonalAccessToken
	a.DB.Where("name = ?", "只读脚本").First(&used)
	if used.LastUsedAt == nil || used.LastUsedIP == "" || used.TokenHash == readToken {
		t.Fatalf("应记录最近使用且只保存摘要: %+v", used)
	}

	// 管理员的令牌也不具备管理员权限
	_, p = create(adminJWT, `{"name":"管理员脚本","scope":"write"}`)
	adminToken := data(p)["token"].(string)
	if status, _ := do(adminToken, http.MethodGet, "/api/v1/admin/users", ""); status != http.StatusForbidden {
		t.Fatalf("令牌不应具备管理员权限: %d", status)
	}
	if status, _ := do(adminJWT, http.MethodGet, "/api/v1/admin/users", ""); status != http.StatusOK {
		t.Fatalf("管理员登录令牌仍可访问: %d", status)
	}

	// 列表：含前缀与状态，不含摘要
	_, list := do(userJWT, http.MethodGet, "/api/v1/auth/tokens", "")
	items := data(list)["items"].([]any)
	if len(items) != 2 || data(list)["active"].(float64) != 2 || data(list)["limit"].(float64) != 10 {
		t.Fatalf("令牌列表异常: %v", data(list))
	}
	if _, leaked := items[0].(map[string]any)["token_hash"]; leaked {
		t.Fatal("列表不应返回令牌摘要")
	}
	_, firstPage := do(userJWT, http.MethodGet, "/api/v1/auth/tokens?page=1&page_size=1", "")
	firstPageData := data(firstPage)
	if firstPageData["total"].(float64) != 2 || firstPageData["page"].(float64) != 1 || firstPageData["page_size"].(float64) != 1 || len(firstPageData["items"].([]any)) != 1 {
		t.Fatalf("令牌分页第一页异常: %v", firstPageData)
	}
	_, lastPage := do(userJWT, http.MethodGet, "/api/v1/auth/tokens?page=99&page_size=1", "")
	lastPageData := data(lastPage)
	if lastPageData["page"].(float64) != 2 || len(lastPageData["items"].([]any)) != 1 {
		t.Fatalf("超出范围的页码应归到末页: %v", lastPageData)
	}

	// 吊销后立即失效；过期后失效
	readID := uint(items[1].(map[string]any)["id"].(float64))
	if status, _ := do(userJWT, http.MethodDelete, fmt.Sprintf("/api/v1/auth/tokens/%d", readID), ""); status != http.StatusOK {
		t.Fatalf("吊销失败: %d", status)
	}
	if status, _ := do(readToken, http.MethodGet, "/api/v1/auth/me", ""); status != http.StatusUnauthorized {
		t.Fatalf("吊销后应失效: %d", status)
	}
	a.DB.Model(&models.PersonalAccessToken{}).Where("name = ?", "发布文档").Update("expires_at", time.Now().Add(-time.Minute))
	if status, _ := do(writeToken, http.MethodGet, "/api/v1/auth/me", ""); status != http.StatusUnauthorized {
		t.Fatalf("过期后应失效: %d", status)
	}

	// 数量上限为权益：设为 1 时只能有一个有效令牌；设为 0 时不开放
	do(adminJWT, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"api.tokens_max":1}}`)
	if status, _ := create(userJWT, `{"name":"第一个","scope":"read"}`); status != http.StatusOK {
		t.Fatalf("吊销与过期的不计入上限: %d", status)
	}
	if status, _ := create(userJWT, `{"name":"第二个","scope":"read"}`); status != http.StatusForbidden {
		t.Fatalf("超出上限应拒绝: %d", status)
	}
	do(adminJWT, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"api.tokens_max":0}}`)
	if status, p := create(userJWT, `{"name":"不开放","scope":"read"}`); status != http.StatusForbidden || !strings.Contains(p["message"].(string), "暂不支持") {
		t.Fatalf("上限为 0 时不开放: %d %v", status, p)
	}
}

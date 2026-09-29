package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 自定义权限的访问令牌：只能调用勾选权限对应的接口；未登记权限的登录接口一律拒绝；
// 公开接口缺少对应权限时按游客处理（看不到私有书籍）；全部权限令牌不受限；可选权限不含账号安全类。
func TestAccessTokenCustomScopes(t *testing.T) {
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
	do := func(token, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	data := func(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }
	do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"令牌权限"},"admin":{"username":"sc-admin","email":"sc-admin@test.local","password":"secret123"}}`)
	u := &models.User{Username: "sc-user", Email: "sc-user@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	jwt, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	_, bp := do(jwt, http.MethodPost, "/api/v1/books", `{"title":"私密书","is_public":false}`)
	bookID := uint(data(bp)["id"].(float64))

	// 可选权限：按资源分组，不含 auth:*
	_, pp := do(jwt, http.MethodGet, "/api/v1/auth/tokens/permissions", "")
	groups := data(pp)["groups"].([]any)
	found := map[string]bool{}
	for _, g := range groups {
		gm := g.(map[string]any)
		for _, perm := range gm["permissions"].([]any) {
			found[perm.(string)] = true
		}
		if gm["resource"] == "auth" {
			t.Fatalf("不应提供账号安全类权限: %v", gm)
		}
	}
	if !found["book:read"] || !found["document:create"] || found["user:manage"] {
		t.Fatalf("可选权限异常: %v", found)
	}

	create := func(body string) (int, string, map[string]any) {
		status, p := do(jwt, http.MethodPost, "/api/v1/auth/tokens", body)
		tok, _ := data(p)["token"].(string)
		return status, tok, p
	}
	for _, body := range []string{`{"name":"x","scope":"custom"}`, `{"name":"x","scope":"custom","permissions":["user:manage"]}`, `{"name":"x","scope":"custom","permissions":["auth:oauth"]}`} {
		if status, _, _ := create(body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝 %s: %d", body, status)
		}
	}
	status, docOnly, p := create(`{"name":"写章节","scope":"custom","permissions":["document:create","book:read","document:create"]}`)
	if status != http.StatusOK || fmt.Sprint(data(p)["item"].(map[string]any)["permissions"]) != "[book:read document:create]" || data(p)["item"].(map[string]any)["scope"] != "custom" {
		t.Fatalf("创建自定义令牌失败: %d %v", status, p)
	}

	// 已勾选的权限可用
	if status, p := do(docOnly, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"令牌写的章节"}`); status != http.StatusOK {
		t.Fatalf("有 document:create 应能建章: %d %v", status, p)
	}
	if status, _ := do(docOnly, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), ""); status != http.StatusOK {
		t.Fatalf("有 book:read 应能读私有书: %d", status)
	}
	// 未勾选的权限被拒绝
	if status, p := do(docOnly, http.MethodPost, "/api/v1/books", `{"title":"不能建书"}`); status != http.StatusForbidden || p["code"] != "TOKEN_FORBIDDEN" {
		t.Fatalf("没有 book:create 应拒绝: %d %v", status, p)
	}
	// 未登记权限的登录接口一律拒绝；身份查询接口可用且只返回令牌的权限
	if status, p := do(docOnly, http.MethodGet, "/api/v1/users/me/files", ""); status != http.StatusForbidden || p["code"] != "TOKEN_FORBIDDEN" {
		t.Fatalf("未登记权限的接口应拒绝限定令牌: %d %v", status, p)
	}
	if status, p := do(docOnly, http.MethodGet, "/api/v1/auth/me", ""); status != http.StatusOK || data(p)["username"] != "sc-user" {
		t.Fatalf("限定令牌应能查询身份: %d %v", status, p)
	}
	_, perms := do(docOnly, http.MethodGet, "/api/v1/auth/permissions", "")
	if fmt.Sprint(perms["data"]) != "[book:read document:create]" {
		t.Fatalf("权限列表应只含令牌权限: %v", perms["data"])
	}

	// 公开接口缺少对应权限：按游客处理，看不到私有书
	_, noRead, _ := create(`{"name":"只能通知","scope":"custom","permissions":["notification:read"]}`)
	if status, _ := do(noRead, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookID), ""); status == http.StatusOK {
		t.Fatalf("没有 book:read 不应读到私有书: %d", status)
	}
	if status, _ := do(noRead, http.MethodGet, "/api/v1/site", ""); status != http.StatusOK {
		t.Fatalf("公开接口仍可按游客访问: %d", status)
	}
	if status, _ := do(noRead, http.MethodGet, "/api/v1/notifications", ""); status != http.StatusOK {
		t.Fatalf("有 notification:read 应能读通知: %d", status)
	}

	// 全部权限：不受限（旧的 write 按 all 保存）
	_, all, ap := create(`{"name":"全部","scope":"write"}`)
	if data(ap)["item"].(map[string]any)["scope"] != "all" {
		t.Fatalf("write 应保存为 all: %v", ap)
	}
	if status, _ := do(all, http.MethodPost, "/api/v1/books", `{"title":"全部权限建书"}`); status != http.StatusOK {
		t.Fatalf("全部权限令牌应能建书: %d", status)
	}
	if status, _ := do(all, http.MethodGet, "/api/v1/users/me/files", ""); status != http.StatusOK {
		t.Fatalf("全部权限令牌应能调用未登记权限的接口: %d", status)
	}
}

// 限定书籍的访问令牌：只能操作这本书（读写、协作者、评论管理），其他书籍按游客处理；「我的书籍」只列出这本；
// 不能调用用户级接口（通知等）与新建书籍；创建时只能限定为自己可编辑的书，自定义权限不能包含书籍以外的资源。
func TestAccessTokenBookScope(t *testing.T) {
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
	do := func(token, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	data := func(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"限定书籍"},"admin":{"username":"bs-admin","email":"bs-admin@test.local","password":"secret123"}}`)
	adminJWT := data(installed)["token"].(string)
	u := &models.User{Username: "bs-user", Email: "bs-user@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	jwt, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	book := func(title string) uint {
		_, p := do(jwt, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":%q,"is_public":false}`, title))
		return uint(data(p)["id"].(float64))
	}
	bookA, bookB := book("A 书"), book("B 书")
	_, adminBook := do(adminJWT, http.MethodPost, "/api/v1/books", `{"title":"管理员的书"}`)

	for _, body := range []string{
		fmt.Sprintf(`{"name":"x","scope":"all","book_id":%v}`, data(adminBook)["id"]),
		fmt.Sprintf(`{"name":"x","scope":"custom","permissions":["notification:read"],"book_id":%d}`, bookA),
	} {
		if status, _ := do(jwt, http.MethodPost, "/api/v1/auth/tokens", body); status != http.StatusBadRequest {
			t.Fatalf("应拒绝 %s: %d", body, status)
		}
	}
	_, p := do(jwt, http.MethodPost, "/api/v1/auth/tokens", fmt.Sprintf(`{"name":"A 书发布","scope":"all","book_id":%d}`, bookA))
	token := data(p)["token"].(string)
	if data(p)["item"].(map[string]any)["book"].(map[string]any)["title"] != "A 书" {
		t.Fatalf("令牌应显示限定的书籍: %v", data(p)["item"])
	}

	if status, p := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookA), `{"title":"A 章"}`); status != http.StatusOK {
		t.Fatalf("应能写限定的书: %d %v", status, p)
	}
	if status, _ := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookB), `{"title":"B 章"}`); status == http.StatusOK {
		t.Fatal("不应能写其他书")
	}
	if status, _ := do(token, http.MethodGet, fmt.Sprintf("/api/v1/books/%d", bookB), ""); status == http.StatusOK {
		t.Fatal("其他私有书应按游客处理，读不到")
	}
	if status, _ := do(token, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", bookB), `{"title":"改名"}`); status == http.StatusOK {
		t.Fatal("不应能修改其他书")
	}
	_, list := do(token, http.MethodGet, "/api/v1/books?mine=true", "")
	if items := data(list)["items"].([]any); len(items) != 1 || uint(items[0].(map[string]any)["id"].(float64)) != bookA {
		t.Fatalf("我的书籍应只列出限定的书: %v", items)
	}
	if status, p := do(token, http.MethodPost, "/api/v1/books", `{"title":"新书"}`); status != http.StatusForbidden || p["code"] != "TOKEN_FORBIDDEN" {
		t.Fatalf("限定书籍的令牌不能新建书籍: %d %v", status, p)
	}
	if status, p := do(token, http.MethodGet, "/api/v1/notifications", ""); status != http.StatusForbidden || p["code"] != "TOKEN_FORBIDDEN" {
		t.Fatalf("限定书籍的令牌不能读通知: %d %v", status, p)
	}
	if status, _ := do(token, http.MethodGet, "/api/v1/users/me/files", ""); status != http.StatusForbidden {
		t.Fatalf("未登记权限的接口应拒绝: %d", status)
	}
}

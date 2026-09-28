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

// 名字显示方式：默认优先显示昵称（没有昵称时显示用户名）；用户可改为始终显示用户名；
// 各处返回的用户（本人、书籍作者、个人主页、评论）都带按此计算的 display_name。
func TestNameDisplay(t *testing.T) {
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
	do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"名字"},"admin":{"username":"nd-admin","email":"nd-admin@test.local","password":"secret123"}}`)
	u := &models.User{Username: "nd-writer", Email: "nd-writer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	token, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)

	names := func() []any {
		_, me := do(token, http.MethodGet, "/api/v1/auth/me", "")
		_, profile := do("", http.MethodGet, "/api/v1/users/nd-writer", "")
		_, books := do("", http.MethodGet, "/api/v1/users/nd-writer/books", "")
		book := data(books)["items"].([]any)[0].(map[string]any)
		_, comments := do("", http.MethodGet, fmt.Sprintf("/api/v1/documents/%v/comments", docID(t, a, book)), "")
		comment := comments["data"].([]any)[0].(map[string]any)
		return []any{data(me)["display_name"], data(profile)["display_name"], book["user"].(map[string]any)["display_name"], comment["user"].(map[string]any)["display_name"]}
	}
	_, created := do(token, http.MethodPost, "/api/v1/books", `{"title":"名字书","status":"published","is_public":true}`)
	_, doc := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%v/documents", data(created)["id"]), `{"title":"章","content":"正文","status":"published"}`)
	do(token, http.MethodPost, fmt.Sprintf("/api/v1/documents/%v/comments", data(doc)["id"]), `{"content":"好"}`)

	// 没有昵称：显示用户名
	if got := fmt.Sprint(names()); got != "[nd-writer nd-writer nd-writer nd-writer]" {
		t.Fatalf("没有昵称时应显示用户名: %s", got)
	}
	// 设置昵称：默认显示昵称
	if status, p := do(token, http.MethodPut, "/api/v1/auth/profile", `{"nickname":"写作者"}`); status != http.StatusOK || data(p)["display_name"] != "写作者" {
		t.Fatalf("保存昵称失败: %d %v", status, p)
	}
	if got := fmt.Sprint(names()); got != "[写作者 写作者 写作者 写作者]" {
		t.Fatalf("有昵称时默认显示昵称: %s", got)
	}
	// 改为始终显示用户名
	if status, _ := do(token, http.MethodPut, "/api/v1/auth/profile", `{"name_display":"bad"}`); status != http.StatusBadRequest {
		t.Fatalf("无效的显示方式应拒绝: %d", status)
	}
	if status, p := do(token, http.MethodPut, "/api/v1/auth/profile", `{"name_display":"username"}`); status != http.StatusOK || data(p)["display_name"] != "nd-writer" {
		t.Fatalf("切换显示方式失败: %d %v", status, p)
	}
	if got := fmt.Sprint(names()); got != "[nd-writer nd-writer nd-writer nd-writer]" {
		t.Fatalf("选择显示用户名后各处应显示用户名: %s", got)
	}
}

func docID(t *testing.T, a *App, book map[string]any) uint {
	t.Helper()
	var d models.Document
	a.DB.Where("book_id = ?", uint(book["id"].(float64))).First(&d)
	return d.ID
}

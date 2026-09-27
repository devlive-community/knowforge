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

// 私有书籍数量：新建私有书、公开改私有、导入/复制等新建的私有草稿都受限；新建公开书、私有改公开不受限；只统计本人的私有书。
func TestPrivateBooksEntitlement(t *testing.T) {
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
	do := func(token, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"私有书"},"admin":{"username":"pb-admin","email":"pb-admin@test.local","password":"secret123"}}`)
	adminToken := installed["data"].(map[string]any)["token"].(string)
	u := &models.User{Username: "pb-writer", Email: "pb-writer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	token, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	other := &models.User{Username: "pb-other", Email: "pb-other@test.local", Role: "user", IsActive: true}
	a.DB.Create(other)
	a.DB.Create(&models.Book{Title: "别人的私有书", Slug: "pb-others", UserID: other.ID})

	if status, p := do(adminToken, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"books.private_max":1}}`); status != http.StatusOK {
		t.Fatalf("设置基础值失败: %d %v", status, p)
	}
	create := func(title string, public bool) (int, map[string]any) {
		return do(token, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":%q,"is_public":%t,"status":"published"}`, title, public))
	}
	if status, _ := create("第一本私有", false); status != http.StatusOK {
		t.Fatalf("额度内应能新建私有书: %d", status)
	}
	status, p := create("第二本私有", false)
	if status != http.StatusForbidden || !strings.Contains(p["message"].(string), "私有书籍数量上限（1 本）") {
		t.Fatalf("超出私有书籍上限应 403: %d %v", status, p)
	}
	status, p = create("公开书", true)
	if status != http.StatusOK {
		t.Fatalf("新建公开书不受私有上限限制: %d %v", status, p)
	}
	publicID := uint(p["data"].(map[string]any)["id"].(float64))
	// 公开改私有受限；私有改公开后腾出名额
	if status, p := do(token, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", publicID), `{"is_public":false}`); status != http.StatusForbidden {
		t.Fatalf("公开改私有应受限: %d %v", status, p)
	}
	var private models.Book
	a.DB.Where("user_id = ? AND is_public = ?", u.ID, false).First(&private)
	if status, p := do(token, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", private.ID), `{"is_public":true}`); status != http.StatusOK {
		t.Fatalf("私有改公开应允许: %d %v", status, p)
	}
	if status, p := do(token, http.MethodPut, fmt.Sprintf("/api/v1/books/%d", publicID), `{"is_public":false}`); status != http.StatusOK {
		t.Fatalf("腾出名额后公开改私有应允许: %d %v", status, p)
	}
	// 复制新建的是私有草稿，同样受限
	if status, p := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/copy", private.ID), `{"mode":"metadata"}`); status != http.StatusForbidden {
		t.Fatalf("复制新建私有书应受限: %d %v", status, p)
	}
	if err := a.EnsureBookQuota(u); err == nil || !strings.Contains(err.Error(), "私有书籍") {
		t.Fatalf("导入/采集等新建私有草稿应受限: %v", err)
	}
	// 管理员不受限
	var admin models.User
	a.DB.Where("username = ?", "pb-admin").First(&admin)
	if err := a.EnsureBookQuota(&admin); err != nil {
		t.Fatalf("管理员不受限: %v", err)
	}
}

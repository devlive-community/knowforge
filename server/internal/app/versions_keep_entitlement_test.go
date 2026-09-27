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

// 每章可查看的历史版本数：只列出最近的若干版本（其余仍保存、标出隐藏数），超出范围的版本不能查看或恢复；放宽后重新可见。
func TestVersionsKeepEntitlement(t *testing.T) {
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
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"版本"},"admin":{"username":"vk-admin","email":"vk-admin@test.local","password":"secret123"}}`)
	adminToken := installed["data"].(map[string]any)["token"].(string)
	u := &models.User{Username: "vk-writer", Email: "vk-writer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(u)
	token, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	_, created := do(token, http.MethodPost, "/api/v1/books", `{"title":"版本书"}`)
	bookID := uint(created["data"].(map[string]any)["id"].(float64))
	_, d := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"章节","content":"v0"}`)
	docID := uint(d["data"].(map[string]any)["id"].(float64))
	for i := 1; i <= 4; i++ {
		do(token, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", docID), fmt.Sprintf(`{"content":"v%d","create_revision":true}`, i))
		time.Sleep(5 * time.Millisecond)
	}
	var all []models.DocumentRevision
	a.DB.Where("document_id = ?", docID).Order("created_at DESC, id DESC").Find(&all)
	if len(all) < 4 {
		t.Fatalf("应已有多个版本: %d", len(all))
	}
	list := fmt.Sprintf("/api/v1/documents/%d/revisions", docID)
	if _, p := do(token, http.MethodGet, list, ""); p["data"].(map[string]any)["total"].(float64) != float64(len(all)) || p["data"].(map[string]any)["hidden"].(float64) != 0 {
		t.Fatalf("默认不限: %v", p["data"])
	}

	do(adminToken, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"versions.keep":2}}`)
	_, p := do(token, http.MethodGet, list+"?page_size=50", "")
	data := p["data"].(map[string]any)
	items := data["items"].([]any)
	if data["total"].(float64) != 2 || len(items) != 2 || data["hidden"].(float64) != float64(len(all)-2) || data["keep"].(float64) != 2 ||
		uint(items[0].(map[string]any)["id"].(float64)) != all[0].ID {
		t.Fatalf("只应列出最近 2 个版本: %v", data)
	}
	if _, p := do(token, http.MethodGet, list+"?page=2&page_size=1", ""); len(p["data"].(map[string]any)["items"].([]any)) != 1 {
		t.Fatalf("分页应在保留范围内: %v", p["data"])
	}
	if _, p := do(token, http.MethodGet, list+"?page=3&page_size=1", ""); len(p["data"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatalf("超出保留范围的页应为空: %v", p["data"])
	}
	oldest := all[len(all)-1]
	status, p := do(token, http.MethodGet, fmt.Sprintf("%s/%d", list, oldest.ID), "")
	if status != http.StatusForbidden || !strings.Contains(p["message"].(string), "最近 2 个") {
		t.Fatalf("超出范围的版本不能查看: %d %v", status, p)
	}
	if status, _ := do(token, http.MethodPost, fmt.Sprintf("%s/%d/restore", list, oldest.ID), ""); status != http.StatusForbidden {
		t.Fatalf("超出范围的版本不能恢复: %d", status)
	}
	var count int64
	a.DB.Model(&models.DocumentRevision{}).Where("document_id = ?", docID).Count(&count)
	if count != int64(len(all)) {
		t.Fatalf("隐藏不应删除版本: %d -> %d", len(all), count)
	}
	if status, _ := do(token, http.MethodGet, fmt.Sprintf("%s/%d", list, all[1].ID), ""); status != http.StatusOK {
		t.Fatalf("范围内的版本可查看: %d", status)
	}
	// 放宽后重新可见
	do(adminToken, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"versions.keep":-1}}`)
	if status, _ := do(token, http.MethodGet, fmt.Sprintf("%s/%d", list, oldest.ID), ""); status != http.StatusOK {
		t.Fatalf("放宽后应可查看较早的版本: %d", status)
	}
}

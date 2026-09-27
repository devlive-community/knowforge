package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 个人存储：上传记入用量，超出空间时拒绝；「我的文件」列出文件与被引用次数，删除时同时从存储删除并释放空间；只能删除自己的文件。
func TestUserStorageQuota(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("KNOWFORGE_DATA", dataDir)
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
	upload := func(token string, size int) (int, map[string]any) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, _ := w.CreateFormFile("file", "a.png")
		_, _ = part.Write(bytes.Repeat([]byte{0x89}, size))
		_ = w.Close()
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/upload", &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"存储"},"admin":{"username":"st-admin","email":"st-admin@test.local","password":"secret123"}}`)
	adminToken := installed["data"].(map[string]any)["token"].(string)
	newUser := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		token, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, token
	}
	u, token := newUser("st-writer")
	_, otherToken := newUser("st-other")

	do(adminToken, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"storage.total_mb":1}}`)
	status, p := upload(token, 700<<10)
	if status != http.StatusOK {
		t.Fatalf("空间内上传应成功: %d %v", status, p)
	}
	url := p["data"].(map[string]any)["url"].(string)
	status, p = upload(token, 400<<10)
	if status != http.StatusForbidden || !strings.Contains(p["message"].(string), "个人存储空间不足") {
		t.Fatalf("超出空间应拒绝: %d %v", status, p)
	}

	// 被章节引用
	_, created := do(token, http.MethodPost, "/api/v1/books", `{"title":"存储书"}`)
	bookID := uint(created["data"].(map[string]any)["id"].(float64))
	do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":"章","content":"![](%s)"}`, url))
	_, list := do(token, http.MethodGet, "/api/v1/users/me/files", "")
	data := list["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 || data["used_bytes"].(float64) != float64(700<<10) || data["limit_mb"].(float64) != 1 {
		t.Fatalf("文件列表异常: %v", data)
	}
	item := items[0].(map[string]any)
	file := item["file"].(map[string]any)
	if item["references"].(float64) != 1 || file["source"] != "upload" || file["driver"] != "local" || file["url"] != url {
		t.Fatalf("文件信息异常: %v", item)
	}
	fileID := uint(file["id"].(float64))
	path := filepath.Join(dataDir, "uploads", file["name"].(string))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件应已保存: %v", err)
	}

	if status, _ := do(otherToken, http.MethodDelete, fmt.Sprintf("/api/v1/users/me/files/%d", fileID), ""); status != http.StatusNotFound {
		t.Fatalf("不能删除他人的文件: %d", status)
	}
	status, p = do(token, http.MethodDelete, fmt.Sprintf("/api/v1/users/me/files/%d", fileID), "")
	if status != http.StatusOK || p["data"].(map[string]any)["used_bytes"].(float64) != 0 {
		t.Fatalf("删除失败: %d %v", status, p)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("删除后存储中的文件应不存在: %v", err)
	}
	if status, _ := upload(token, 400<<10); status != http.StatusOK {
		t.Fatalf("释放空间后应可上传: %d", status)
	}
	var files int64
	a.DB.Model(&models.UserFile{}).Where("user_id = ?", u.ID).Count(&files)
	if files != 1 {
		t.Fatalf("文件记录数异常: %d", files)
	}
	// 管理员不受限
	if status, _ := upload(adminToken, 2<<20); status != http.StatusOK {
		t.Fatalf("管理员不受个人存储限制: %d", status)
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 存储迁移：本地 → S3 兼容存储。已有文件（含早期上传、没有文件记录但被引用的）逐个上传到新存储，
// 正文、封面、头像中的旧地址（含带站点域名的绝对地址）改为新地址，文件记录更新，原文件按需删除；进度出现在「我的任务」中。
func TestStorageMigrationLocalToS3(t *testing.T) {
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
	// 假的 S3 兼容存储：记录收到的对象
	var mu sync.Mutex
	objects := map[string][]byte{}
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		objects[r.URL.Path] = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer s3.Close()
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
	upload := func(token, content string) string {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, _ := w.CreateFormFile("file", "a.png")
		_, _ = part.Write([]byte("\x89PNG\r\n\x1a\n" + content))
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
		return p["data"].(map[string]any)["url"].(string)
	}
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"迁移"},"admin":{"username":"mg-admin","email":"mg-admin@test.local","password":"secret123"}}`)
	adminToken := installed["data"].(map[string]any)["token"].(string)
	writer := &models.User{Username: "mg-writer", Email: "mg-writer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(writer)
	token, _ := auth.GenerateToken(a.Config.Secret, writer.ID, writer.Username, writer.Role)
	if a.jobQueue() == nil {
		if err := a.configureJobQueue(); err != nil {
			t.Fatal(err)
		}
	}

	img1 := upload(token, "one")
	img2 := upload(token, "two")
	// 早期上传：只有文件、没有文件记录，但被正文引用
	legacy := "20200101-legacy.png"
	_ = os.WriteFile(filepath.Join(dataDir, "uploads", legacy), []byte("legacy"), 0o644)
	_ = os.WriteFile(filepath.Join(dataDir, "uploads", "20200101-unused.png"), []byte("unused"), 0o644)
	_, created := do(token, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":"迁移书","cover_image":%q}`, img2))
	bookID := uint(created["data"].(map[string]any)["id"].(float64))
	content := fmt.Sprintf("![](%s)\n\n![绝对地址](https://kb.example.com%s)\n\n![早期](/uploads/%s)", img1, img1, legacy)
	_, doc := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":"章","content":%q}`, content))
	docID := uint(doc["data"].(map[string]any)["id"].(float64))
	a.DB.Model(&models.User{}).Where("id = ?", writer.ID).Update("avatar", img1)

	// 本地存储时没有需要迁移的文件
	if status, p := do(adminToken, http.MethodGet, "/api/v1/storage/migration", ""); status != http.StatusOK || p["data"].(map[string]any)["pending"].(float64) != 0 {
		t.Fatalf("本地存储时不应有待迁移文件: %d %v", status, p)
	}
	do(adminToken, http.MethodPut, "/api/v1/storage", fmt.Sprintf(`{"driver":"s3","s3_endpoint":%q,"s3_region":"r1","s3_bucket":"media","s3_access_key":"AK","s3_secret_key":"SK","s3_path_style":true,"s3_public_url":"https://cdn.test"}`, s3.URL))
	_, p := do(adminToken, http.MethodGet, "/api/v1/storage/migration", "")
	if d := p["data"].(map[string]any); d["pending"].(float64) != 3 || d["target_driver"] != "s3" {
		t.Fatalf("应有 3 个待迁移文件（2 个有记录 + 1 个被引用的早期文件；未被引用的不迁移）: %v", d)
	}
	if status, _ := do(token, http.MethodPost, "/api/v1/storage/migration", `{}`); status != http.StatusForbidden {
		t.Fatalf("普通用户不能发起迁移: %d", status)
	}

	sub := a.Notifications.subscribe(1)
	defer a.Notifications.unsubscribe(1, sub)
	status, started := do(adminToken, http.MethodPost, "/api/v1/storage/migration", `{"delete_old":true}`)
	if status != http.StatusAccepted {
		t.Fatalf("发起迁移失败: %d %v", status, started)
	}
	if status, _ := do(adminToken, http.MethodPost, "/api/v1/storage/migration", `{}`); status != http.StatusConflict {
		t.Fatalf("迁移进行中不能重复发起: %d", status)
	}
	if ran, err := a.jobQueue().RunOnce(context.Background()); !ran || err != nil {
		t.Fatalf("迁移任务执行失败: %v %v", ran, err)
	}

	newURL := func(old string) string { return "https://cdn.test/" + strings.TrimPrefix(old, "/uploads/") }
	var d models.Document
	a.DB.First(&d, docID)
	want := fmt.Sprintf("![](%s)\n\n![绝对地址](%s)\n\n![早期](%s)", newURL(img1), newURL(img1), "https://cdn.test/"+legacy)
	if d.Content != want {
		t.Fatalf("正文地址应已改为新存储:\n%s\nwant:\n%s", d.Content, want)
	}
	var book models.Book
	a.DB.First(&book, bookID)
	var me models.User
	a.DB.First(&me, writer.ID)
	if book.CoverImage != newURL(img2) || me.Avatar != newURL(img1) {
		t.Fatalf("封面与头像应已更新: %s %s", book.CoverImage, me.Avatar)
	}
	var files []models.UserFile
	a.DB.Find(&files)
	for _, f := range files {
		if f.Driver != "s3" || !strings.HasPrefix(f.URL, "https://cdn.test/") {
			t.Fatalf("文件记录应已更新: %+v", f)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "uploads", f.Name)); !os.IsNotExist(err) {
			t.Fatalf("原文件应已删除: %s", f.Name)
		}
	}
	if len(objects) != 3 || string(objects["/media/"+legacy]) != "legacy" {
		t.Fatalf("新存储应收到 3 个对象: %v", len(objects))
	}
	if _, err := os.Stat(filepath.Join(dataDir, "uploads", "20200101-unused.png")); err != nil {
		t.Fatal("未被引用的早期文件不迁移、不删除")
	}
	_, p = do(adminToken, http.MethodGet, "/api/v1/storage/migration", "")
	data := p["data"].(map[string]any)
	state := data["state"].(map[string]any)
	if data["pending"].(float64) != 0 || state["status"] != "done" || state["done"].(float64) != 3 || state["failed"].(float64) != 0 {
		t.Fatalf("迁移完成后状态异常: %v", data)
	}
	// 「我的任务」收到进度推送
	sawProgress := false
	for len(sub) > 0 {
		msg := <-sub
		if strings.Contains(msg, `"kind":"storageMigrate"`) && strings.Contains(msg, `"done":3`) {
			sawProgress = true
		}
	}
	if !sawProgress {
		t.Fatal("应推送存储迁移的进度")
	}
}

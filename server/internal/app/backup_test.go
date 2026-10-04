package app

import (
	"archive/zip"
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
	"testing"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/database"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

type backupTestClient struct {
	t      *testing.T
	server *httptest.Server
}

func (h backupTestClient) do(token, method, path, body string) (int, map[string]any) {
	req, _ := http.NewRequest(method, h.server.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	p := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&p)
	return resp.StatusCode, p
}

func backupData(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }

func newBackupTestApp(t *testing.T, dataDir string) (*App, backupTestClient) {
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
	t.Cleanup(server.Close)
	return a, backupTestClient{t: t, server: server}
}

// 备份与恢复：备份包含数据库全部业务数据（含插件表、自引用外键、布尔与时间）与本地上传文件；下载需要短时凭证；
// 在新实例的安装向导中恢复到一个空库后，数据、文件、登录与原有令牌均可用，新建数据不与恢复的 ID 冲突，搜索索引重建。
func TestBackupAndRestore(t *testing.T) {
	a, h := newBackupTestApp(t, t.TempDir())
	_, installed := h.do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"备份站"},"admin":{"username":"bk-admin","email":"bk-admin@test.local","password":"secret123"}}`)
	adminToken := backupData(installed)["token"].(string)
	if a.jobQueue() == nil {
		if err := a.configureJobQueue(); err != nil {
			t.Fatal(err)
		}
	}
	writer := &models.User{Username: "bk-writer", Email: "bk-writer@test.local", Nickname: "写作者", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(writer)
	writerToken, _ := auth.GenerateToken(a.Config.Secret, writer.ID, writer.Username, writer.Role)
	if status, p := h.do(adminToken, http.MethodPost, "/api/v1/admin/plugins/book-lists/install", ""); status != http.StatusOK {
		t.Fatalf("启用书单插件失败: %d %v", status, p)
	}

	// 上传一张图片（本地存储）
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "a.png")
	_, _ = part.Write([]byte("\x89PNG\r\n\x1a\nbackup-image"))
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+writerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&uploaded)
	resp.Body.Close()
	imageURL := backupData(uploaded)["url"].(string)

	_, created := h.do(writerToken, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":"可恢复的书","status":"published","is_public":true,"cover_image":%q}`, imageURL))
	bookID := uint(backupData(created)["id"].(float64))
	_, doc := h.do(writerToken, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章","content":"独一无二的备份正文 zebra-quartz","status":"published"}`)
	docID := uint(backupData(doc)["id"].(float64))
	_, parent := h.do(writerToken, http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", docID), `{"content":"父评论"}`)
	parentID := backupData(parent)["id"]
	h.do(adminToken, http.MethodPost, fmt.Sprintf("/api/v1/documents/%d/comments", docID), fmt.Sprintf(`{"content":"回复","parent_id":%v}`, parentID))
	if n := len(backupData(parent)); n == 0 || parentID == nil {
		t.Fatalf("发表评论失败: %v", parent)
	}
	a.DB.Model(&models.Document{}).Where("id = ?", docID).Update("allow_comments", false) // 布尔 false 也要原样恢复
	_, list := h.do(writerToken, http.MethodPost, "/api/v1/book-lists", fmt.Sprintf(`{"title":"恢复书单","book_id":%d}`, bookID))
	if backupData(list)["id"] == nil {
		t.Fatalf("创建书单失败: %v", list)
	}
	var before struct{ Books, Docs, Comments, Users int64 }
	a.DB.Model(&models.Book{}).Count(&before.Books)
	a.DB.Model(&models.Document{}).Count(&before.Docs)
	a.DB.Model(&models.Comment{}).Count(&before.Comments)
	a.DB.Model(&models.User{}).Count(&before.Users)
	var origDoc models.Document
	a.DB.First(&origDoc, docID)

	// 只有管理员可以备份
	if status, _ := h.do(writerToken, http.MethodGet, "/api/v1/backups", ""); status != http.StatusForbidden {
		t.Fatalf("普通用户不能查看备份: %d", status)
	}
	status, started := h.do(adminToken, http.MethodPost, "/api/v1/backups", `{"include_files":true}`)
	if status != http.StatusAccepted {
		t.Fatalf("发起备份失败: %d %v", status, started)
	}
	if status, _ := h.do(adminToken, http.MethodPost, "/api/v1/backups", `{"include_files":true}`); status != http.StatusConflict {
		t.Fatalf("备份进行中不能重复发起: %d", status)
	}
	if ran, err := a.jobQueue().RunOnce(context.Background()); !ran || err != nil {
		t.Fatalf("备份任务执行失败: %v %v", ran, err)
	}
	_, listed := h.do(adminToken, http.MethodGet, "/api/v1/backups", "")
	items := backupData(listed)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应有 1 个备份: %v", listed)
	}
	b := items[0].(map[string]any)
	if b["status"] != "done" || b["files"].(float64) != 1 || b["rows"].(float64) < 10 || b["size"].(float64) <= 0 {
		t.Fatalf("备份结果不对: %v", b)
	}
	backupID := b["id"]

	// 下载：凭证有效期内无需登录请求头；伪造凭证被拒
	_, ticket := h.do(adminToken, http.MethodPost, fmt.Sprintf("/api/v1/backups/%v/download-ticket", backupID), "")
	url := backupData(ticket)["url"].(string)
	if r, _ := http.Get(h.server.URL + url + "x"); r.StatusCode != http.StatusForbidden {
		t.Fatalf("伪造的下载凭证应被拒绝: %d", r.StatusCode)
	}
	dl, err := http.Get(h.server.URL + url)
	if err != nil || dl.StatusCode != http.StatusOK {
		t.Fatalf("下载备份失败: %v %v", err, dl)
	}
	archive, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["manifest.json"] || !names["db/users.jsonl"] || !names["db/book_list_items.jsonl"] || names["db/background_jobs.jsonl"] || !names["files/uploads/"+strings.TrimPrefix(imageURL, "/uploads/")] {
		t.Fatalf("备份内容不完整: %v", names)
	}

	// —— 在新实例上恢复 ——
	b2Dir := t.TempDir()
	restored, h2 := newBackupTestApp(t, b2Dir)
	var form bytes.Buffer
	fw := multipart.NewWriter(&form)
	_ = fw.WriteField("database", testdb.InstallJSON(t))
	fp, _ := fw.CreateFormFile("file", "backup.zip")
	_, _ = fp.Write(archive)
	_ = fw.Close()
	rreq, _ := http.NewRequest(http.MethodPost, h2.server.URL+"/api/v1/setup/restore", &form)
	rreq.Header.Set("Content-Type", fw.FormDataContentType())
	rresp, err := http.DefaultClient.Do(rreq)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(rresp.Body)
	rresp.Body.Close()
	if !strings.Contains(string(stream), "event: done") {
		t.Fatalf("恢复未完成: %s", stream)
	}
	if !restored.Config.Installed || restored.Config.Secret != a.Config.Secret {
		t.Fatal("恢复后应为已安装状态，并沿用备份中的站点密钥")
	}
	var after struct{ Books, Docs, Comments, Users int64 }
	restored.DB.Model(&models.Book{}).Count(&after.Books)
	restored.DB.Model(&models.Document{}).Count(&after.Docs)
	restored.DB.Model(&models.Comment{}).Count(&after.Comments)
	restored.DB.Model(&models.User{}).Count(&after.Users)
	if after != before {
		t.Fatalf("恢复后数量不一致: %+v vs %+v", after, before)
	}
	var d models.Document
	restored.DB.First(&d, docID)
	if d.Content != origDoc.Content || d.AllowComments == nil || *d.AllowComments || d.Status != "published" || d.CreatedAt.Unix() != origDoc.CreatedAt.Unix() {
		t.Fatalf("章节字段未完整恢复: %+v", d)
	}
	if _, err := os.Stat(filepath.Join(b2Dir, "uploads", strings.TrimPrefix(imageURL, "/uploads/"))); err != nil {
		t.Fatalf("上传文件未恢复: %v", err)
	}
	// 原令牌仍有效、可用原密码登录；插件状态与数据保留
	if status, _ := h2.do(adminToken, http.MethodGet, "/api/v1/backups", ""); status != http.StatusOK {
		t.Fatalf("恢复后原登录令牌应仍有效: %d", status)
	}
	if status, p := h2.do("", http.MethodPost, "/api/v1/auth/login", `{"username":"bk-admin","password":"secret123"}`); status != http.StatusOK {
		t.Fatalf("恢复后应能用原密码登录: %d %v", status, p)
	}
	if status, p := h2.do(writerToken, http.MethodGet, "/api/v1/book-lists/mine", ""); status != http.StatusOK || len(backupData(p)["items"].([]any)) != 1 {
		t.Fatalf("书单插件的数据应恢复: %d %v", status, p)
	}
	// 新建数据不与恢复的 ID 冲突（PostgreSQL 需重置序列）
	if status, p := h2.do(writerToken, http.MethodPost, "/api/v1/books", `{"title":"恢复后新建"}`); status != http.StatusOK || uint(backupData(p)["id"].(float64)) <= bookID {
		t.Fatalf("恢复后新建书籍失败: %d %v", status, p)
	}
	if status, p := h2.do("", http.MethodGet, "/api/v1/search?q=zebra-quartz", ""); status != http.StatusOK || !strings.Contains(fmt.Sprint(p), "第一章") {
		t.Fatalf("恢复后搜索应可用: %d %v", status, p)
	}
	if status, _ := h2.do("", http.MethodPost, "/api/v1/setup/restore", ""); status != http.StatusForbidden {
		t.Fatalf("已安装的实例不能再恢复: %d", status)
	}

	// 目标库已有数据时拒绝恢复
	_, h3 := newBackupTestApp(t, t.TempDir())
	dbJSON, _ := json.Marshal(restored.Config.Database)
	var form3 bytes.Buffer
	fw3 := multipart.NewWriter(&form3)
	_ = fw3.WriteField("database", string(dbJSON))
	fp3, _ := fw3.CreateFormFile("file", "backup.zip")
	_, _ = fp3.Write(archive)
	_ = fw3.Close()
	r3, _ := http.NewRequest(http.MethodPost, h3.server.URL+"/api/v1/setup/restore", &form3)
	r3.Header.Set("Content-Type", fw3.FormDataContentType())
	resp3, err := http.DefaultClient.Do(r3)
	if err != nil {
		t.Fatal(err)
	}
	var p3 map[string]any
	_ = json.NewDecoder(resp3.Body).Decode(&p3)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest || !strings.Contains(fmt.Sprint(p3["message"]), "空数据库") {
		t.Fatalf("目标库已有数据时应拒绝: %d %v", resp3.StatusCode, p3)
	}

	// 删除备份
	if status, _ := h.do(adminToken, http.MethodDelete, fmt.Sprintf("/api/v1/backups/%v", backupID), ""); status != http.StatusOK {
		t.Fatalf("删除备份失败: %d", status)
	}
}

// 备份来自更新的版本或格式时拒绝恢复。
func TestBackupManifestVersionCheck(t *testing.T) {
	write := func(m backupManifest) string {
		p := filepath.Join(t.TempDir(), "b.zip")
		f, _ := os.Create(p)
		zw := zip.NewWriter(f)
		w, _ := zw.Create("manifest.json")
		_ = json.NewEncoder(w).Encode(m)
		_ = zw.Close()
		_ = f.Close()
		return p
	}
	if _, _, err := readBackupManifest(write(backupManifest{Format: 1, AppVersion: "9999.0.0"})); err == nil || !strings.Contains(err.Error(), "9999.0.0") {
		t.Fatalf("更新版本的备份应被拒绝: %v", err)
	}
	if _, _, err := readBackupManifest(write(backupManifest{Format: backupFormat + 1, AppVersion: Version})); err == nil {
		t.Fatal("更新格式的备份应被拒绝")
	}
	zr, m, err := readBackupManifest(write(backupManifest{Format: 1, AppVersion: "2026.0.1", CreatedAt: time.Now()}))
	if err != nil || m.AppVersion != "2026.0.1" {
		t.Fatalf("旧版本的备份应可恢复: %v", err)
	}
	zr.Close()
	if _, _, err := readBackupManifest(filepath.Join(t.TempDir(), "missing.zip")); err == nil {
		t.Fatal("无效文件应报错")
	}
}

// 表按外键依赖排序：被引用的表在前。
func TestBackupTableOrder(t *testing.T) {
	db, err := database.Open(config.DatabaseConfig{Type: database.TypeSQLite, Path: filepath.Join(t.TempDir(), "order.db")})
	if err != nil {
		t.Fatal(err)
	}
	tables, err := backupModelTables(db)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, tb := range tables {
		pos[tb.Name] = i
	}
	for _, pair := range [][2]string{{"users", "books"}, {"users", "comments"}, {"books", "book_collaborators"}, {"membership_plans", "membership_prices"}} {
		if pos[pair[0]] >= pos[pair[1]] {
			t.Fatalf("%s 应排在 %s 之前", pair[0], pair[1])
		}
	}
	for _, excluded := range []string{"background_jobs", "rate_limit_counters", "system_backups"} {
		if _, ok := pos[excluded]; ok {
			t.Fatalf("运行时表 %s 不应备份", excluded)
		}
	}
}

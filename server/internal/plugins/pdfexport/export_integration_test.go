package pdfexport_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// PDF 导出端点由插件注册：权限规则与核心导出一致；未安装 Chromium 运行时给出明确提示。
func TestExportBookPDFRoutingAndGuards(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	do := func(method, path, body, token string) (int, map[string]any) {
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
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}
	_, installed := do(http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"PDF 测试"},"admin":{"username":"pdf-owner","email":"pdf-owner@test.local","password":"secret123"}}`, "")
	token := installed["data"].(map[string]any)["token"].(string)
	var owner models.User
	a.DB.Where("username = ?", "pdf-owner").First(&owner)

	private := models.Book{Title: "私有书", Slug: "pdf-private", UserID: owner.ID, Status: "draft"}
	a.DB.Create(&private)
	path := "/api/v1/books/" + strconv.FormatUint(uint64(private.ID), 10) + "/export/pdf"
	if status, _ := do(http.MethodGet, path, "", ""); status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("游客不应能导出私有书籍，实际 %d", status)
	}
	status, payload := do(http.MethodGet, path, "", token)
	if status != http.StatusBadRequest || !strings.Contains(payload["message"].(string), "PDF 导出插件尚未安装") {
		t.Fatalf("作者导出但未安装运行时应得到明确提示: %d %v", status, payload)
	}
}

// 每月 PDF 导出次数为权益：用完或不含时拒绝（在启动无头浏览器之前）；上月的导出不计入；匿名导出不受此限制。
func TestPDFMonthlyEntitlement(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	do := func(method, path, body, token string) (int, map[string]any) {
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
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}
	_, installed := do(http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"PDF 权益"},"admin":{"username":"pdf-admin","email":"pdf-admin@test.local","password":"secret123"}}`, "")
	adminToken := installed["data"].(map[string]any)["token"].(string)
	// 模拟已安装的运行时：记录指向一个存在的可执行文件（测试二进制本身）
	a.DB.Create(&models.Plugin{Key: "pdf-export", Installed: true, Meta: fmt.Sprintf(`{"chrome_path":%q}`, os.Args[0])})

	reader := models.User{Username: "pdf-reader", Email: "pdf-reader@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(&reader)
	readerToken, _ := auth.GenerateToken(a.Config.Secret, reader.ID, reader.Username, reader.Role)
	book := models.Book{Title: "公开书", Slug: "pdf-public", UserID: reader.ID, Status: "published", IsPublic: true, ExportEnabled: true, GuestExportEnabled: true}
	a.DB.Create(&book)
	path := fmt.Sprintf("/api/v1/books/%d/export/pdf", book.ID)

	if status, p := do(http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"export.pdf_monthly":2}}`, adminToken); status != http.StatusOK {
		t.Fatalf("设置基础值失败: %d %v", status, p)
	}
	// 未用完：通过额度检查，进入下一步（测试环境没有内嵌 Web → 503）
	if status, _ := do(http.MethodGet, path, "", readerToken); status != http.StatusServiceUnavailable {
		t.Fatalf("额度内应通过检查: %d", status)
	}
	record := func(at time.Time) {
		r := models.BookExportRecord{UserID: reader.ID, BookID: book.ID, Format: "pdf", CreatedAt: at}
		a.DB.Create(&r)
	}
	now := time.Now()
	record(now)
	record(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Add(-time.Hour)) // 上月
	if status, _ := do(http.MethodGet, path, "", readerToken); status != http.StatusServiceUnavailable {
		t.Fatalf("上月的导出不应计入: %d", status)
	}
	record(now)
	status, p := do(http.MethodGet, path, "", readerToken)
	if status != http.StatusTooManyRequests || !strings.Contains(p["message"].(string), "本月 PDF 导出次数已用完（2 次）") {
		t.Fatalf("用完后应拒绝: %d %v", status, p)
	}
	if status, _ := do(http.MethodGet, path, "", ""); status != http.StatusServiceUnavailable {
		t.Fatalf("匿名导出不受每月次数限制: %d", status)
	}
	do(http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"export.pdf_monthly":0}}`, adminToken)
	if status, p := do(http.MethodGet, path, "", readerToken); status != http.StatusTooManyRequests || !strings.Contains(p["message"].(string), "不含 PDF 导出") {
		t.Fatalf("不含时应拒绝: %d %v", status, p)
	}
	if status, _ := do(http.MethodGet, path, "", adminToken); status != http.StatusServiceUnavailable {
		t.Fatalf("管理员不受限制: %d", status)
	}
}

package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

func TestBatchExportMyBooks(t *testing.T) {
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
	client := &http.Client{Timeout: 10 * time.Second}

	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}
	download := func(path, token string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("下载 %s: %v", path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}

	_, installed := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "批量导出测试站"},
		"admin":    map[string]any{"username": "alice", "email": "alice@test.local", "password": "secret123"},
	}, "")
	aliceToken := installed["data"].(map[string]any)["token"].(string)

	createBook := func(token, title string) uint {
		_, created := request(http.MethodPost, "/api/v1/books", map[string]any{
			"title": title, "status": "published", "is_public": true,
		}, token)
		id := uint(created["data"].(map[string]any)["id"].(float64))
		request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", id), map[string]any{
			"title": title + " 章节", "content": "正文", "status": "published",
		}, token)
		return id
	}
	book1 := createBook(aliceToken, "第一本书")
	createBook(aliceToken, "第二本书")

	// bob 的书籍不应被 alice 导出。
	_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "bob", "email": "bob@test.local", "password": "secret123",
	}, "")
	bobToken := reg["data"].(map[string]any)["token"].(string)
	bobBook := createBook(bobToken, "Bob 的书")

	// 未登录 → 401。
	if status, _ := download("/api/v1/users/me/export/books", ""); status != http.StatusUnauthorized {
		t.Fatalf("未登录批量导出应 401: %d", status)
	}

	// alice 导出全部自有书籍 → 外层 zip 含两本内层 zip + manifest。
	status, raw := download("/api/v1/users/me/export/books", aliceToken)
	if status != http.StatusOK || len(raw) < 2 || !bytes.Equal(raw[:2], []byte("PK")) {
		t.Fatalf("批量导出应为 zip: %d", status)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("外层 zip 解析失败: %v", err)
	}
	names := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = data
	}
	if _, ok := names["manifest.txt"]; !ok {
		t.Fatalf("应包含 manifest.txt，实际: %v", keysOf(names))
	}
	innerCount := 0
	var innerZip []byte
	for name, data := range names {
		if name == "manifest.txt" {
			continue
		}
		innerCount++
		innerZip = data
	}
	if innerCount != 2 {
		t.Fatalf("应含两本内层 zip，实际 %d: %v", innerCount, keysOf(names))
	}
	// 内层 zip 应是自包含的单本 markdown 包（含 book.md）。
	izr, err := zip.NewReader(bytes.NewReader(innerZip), int64(len(innerZip)))
	if err != nil {
		t.Fatalf("内层 zip 解析失败: %v", err)
	}
	hasBookMD := false
	for _, f := range izr.File {
		if f.Name == "book.md" {
			hasBookMD = true
		}
	}
	if !hasBookMD {
		t.Fatalf("内层 zip 应含 book.md")
	}

	// 显式 ids：只导出 alice 拥有的那本，bob 的被过滤。
	status, raw = download(fmt.Sprintf("/api/v1/users/me/export/books?ids=%d,%d", book1, bobBook), aliceToken)
	if status != http.StatusOK {
		t.Fatalf("按 ids 导出应成功: %d", status)
	}
	zr, _ = zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	inner := 0
	for _, f := range zr.File {
		if f.Name != "manifest.txt" {
			inner++
		}
	}
	if inner != 1 {
		t.Fatalf("alice 用混合 ids 导出应只得 1 本，实际 %d", inner)
	}

	// 仅传 bob 的书 → alice 无可导出 → 403。
	if status, _ := download(fmt.Sprintf("/api/v1/users/me/export/books?ids=%d", bobBook), aliceToken); status != http.StatusForbidden {
		t.Fatalf("导出他人书籍应 403: %d", status)
	}

	// 导出历史：前两次成功的批量导出（全部=2 本 + 混合 ids=1 本）应记录 3 条，仅本人可见。
	status, hist := request(http.MethodGet, "/api/v1/users/me/exports", nil, aliceToken)
	if status != http.StatusOK {
		t.Fatalf("查询导出历史应成功: %d", status)
	}
	data := hist["data"].(map[string]any)
	if int(data["total"].(float64)) != 3 {
		t.Fatalf("alice 导出历史应为 3 条，实际 %v", data["total"])
	}
	items := data["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("导出历史条目应为 3，实际 %d", len(items))
	}
	if first := items[0].(map[string]any); first["format"].(string) != "zip" {
		t.Fatalf("导出格式应为 zip，实际 %v", first["format"])
	}
	// bob 未导出过 → 历史为空（隔离）。
	if _, bobHist := request(http.MethodGet, "/api/v1/users/me/exports", nil, bobToken); int(bobHist["data"].(map[string]any)["total"].(float64)) != 0 {
		t.Fatalf("bob 导出历史应为空")
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

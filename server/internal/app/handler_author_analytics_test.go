package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

func TestAuthorAnalyticsOverview(t *testing.T) {
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

	_, installed := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "作者仪表盘测试站"},
		"admin":    map[string]any{"username": "author", "email": "author@test.local", "password": "secret123"},
	}, "")
	authorToken := installed["data"].(map[string]any)["token"].(string)

	createBook := func(title string) int {
		_, created := request(http.MethodPost, "/api/v1/books", map[string]any{
			"title": title, "status": "published", "is_public": true,
		}, authorToken)
		return int(created["data"].(map[string]any)["id"].(float64))
	}
	createDoc := func(bookID int, title string) map[string]any {
		_, response := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
			"title": title, "content": "content", "status": "published",
		}, authorToken)
		return response["data"].(map[string]any)
	}

	bookA := createBook("Book A")
	bookB := createBook("Book B")
	docA1 := createDoc(bookA, "A-1")
	docA2 := createDoc(bookA, "A-2")
	createDoc(bookB, "B-1")

	// Book A 两次浏览，Book B 一次浏览：验证按书分组的周期浏览量。
	request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/view", bookA), map[string]any{}, "")
	request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/view", bookA), map[string]any{}, "")
	request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/view", bookB), map[string]any{}, "")

	// 一名读者读完 Book A 全部已发布章节。
	_, registered := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "reader", "email": "reader@test.local", "password": "secret123",
	}, "")
	readerToken := registered["data"].(map[string]any)["token"].(string)
	for _, doc := range []map[string]any{docA1, docA2} {
		request(http.MethodPut, fmt.Sprintf("/api/v1/reading-progress/%d", bookA), map[string]any{
			"doc_id": int(doc["id"].(float64)), "doc_slug": doc["slug"].(string), "doc_title": doc["title"].(string),
		}, readerToken)
	}

	status, response := request(http.MethodGet, "/api/v1/users/me/author-analytics?days=7", nil, authorToken)
	if status != http.StatusOK {
		t.Fatalf("作者读取仪表盘失败: %d %v", status, response)
	}
	data := response["data"].(map[string]any)
	if data["total_books"] != float64(2) {
		t.Fatalf("书籍总数错误: %v", data["total_books"])
	}
	if data["total_period_views"] != float64(3) || data["total_lifetime_views"] != float64(3) {
		t.Fatalf("汇总浏览量错误: %v", data)
	}
	books := data["books"].([]any)
	if len(books) != 2 {
		t.Fatalf("应返回两本书: %v", books)
	}
	byTitle := map[string]map[string]any{}
	for _, b := range books {
		row := b.(map[string]any)
		byTitle[row["title"].(string)] = row
	}
	rowA := byTitle["Book A"]
	if rowA["period_views"] != float64(2) {
		t.Fatalf("Book A 周期浏览量错误: %v", rowA["period_views"])
	}
	if rowA["chapters"] != float64(2) {
		t.Fatalf("Book A 已发布章节数错误: %v", rowA["chapters"])
	}
	if rowA["registered_readers"] != float64(1) || rowA["completed_readers"] != float64(1) || rowA["completion_rate"] != float64(100) {
		t.Fatalf("Book A 读者聚合错误: %v", rowA)
	}
	rowB := byTitle["Book B"]
	if rowB["period_views"] != float64(1) || rowB["registered_readers"] != float64(0) {
		t.Fatalf("Book B 指标错误: %v", rowB)
	}

	// 读者本人没有书籍，仪表盘应为空。
	status, readerView := request(http.MethodGet, "/api/v1/users/me/author-analytics?days=7", nil, readerToken)
	if status != http.StatusOK {
		t.Fatalf("读者读取仪表盘应成功: %d %v", status, readerView)
	}
	readerData := readerView["data"].(map[string]any)
	if readerData["total_books"] != float64(0) {
		t.Fatalf("读者不应有书籍: %v", readerData["total_books"])
	}

	// 非法周期应拒绝。
	status, _ = request(http.MethodGet, "/api/v1/users/me/author-analytics?days=15", nil, authorToken)
	if status != http.StatusBadRequest {
		t.Fatalf("非法周期应返回 400，实际 %d", status)
	}
}

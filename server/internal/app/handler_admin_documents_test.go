package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// 后台章节管理：验证跨书籍元数据检索，以及管理员复用既有章节更新/删除权限。
func TestAdminDocuments(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "章节管理测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)

	_, registered := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	aliceToken := registered["data"].(map[string]any)["token"].(string)

	status, createdBook := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Alice 私有知识库", "slug": "alice-private", "status": "draft", "is_public": false,
	}, aliceToken)
	if status != http.StatusOK {
		t.Fatalf("创建书籍失败: %d %v", status, createdBook)
	}
	bookID := uint64(createdBook["data"].(map[string]any)["id"].(float64))
	bookPath := "/api/v1/books/" + strconv.FormatUint(bookID, 10) + "/documents"

	status, parentResult := request(http.MethodPost, bookPath, map[string]any{
		"title": "安装章节", "slug": "install", "status": "draft", "content": "私有正文不应出现在列表",
	}, aliceToken)
	if status != http.StatusOK {
		t.Fatalf("创建父章节失败: %d %v", status, parentResult)
	}
	parentID := uint64(parentResult["data"].(map[string]any)["id"].(float64))

	status, childResult := request(http.MethodPost, bookPath, map[string]any{
		"title": "配置章节", "slug": "config", "status": "published", "parent_id": parentID,
	}, aliceToken)
	if status != http.StatusOK {
		t.Fatalf("创建子章节失败: %d %v", status, childResult)
	}
	childID := uint64(childResult["data"].(map[string]any)["id"].(float64))

	if status, _ := request(http.MethodGet, "/api/v1/admin/documents", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("匿名访问后台章节列表应 401: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/documents", nil, aliceToken); status != http.StatusForbidden {
		t.Fatalf("普通用户访问后台章节列表应 403: %d", status)
	}

	status, list := request(http.MethodGet, "/api/v1/admin/documents", nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("管理员章节列表应 200: %d %v", status, list)
	}
	data := list["data"].(map[string]any)
	items, ok := data["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("后台应返回全部 2 个章节: %v", data["items"])
	}
	for _, rawItem := range items {
		item := rawItem.(map[string]any)
		if _, exists := item["content"]; exists {
			t.Fatalf("后台章节列表不得返回正文: %v", item)
		}
		if item["book_title"] != "Alice 私有知识库" || item["author_username"] != "alice" {
			t.Fatalf("章节应包含书籍与作者元数据: %v", item)
		}
	}

	tests := []struct {
		query string
		want  float64
	}{
		{"book_id=" + strconv.FormatUint(bookID, 10), 2},
		{"status=draft", 1},
		{"q=alice", 2},
		{"q=配置章节", 1},
	}
	for _, test := range tests {
		status, result := request(http.MethodGet, "/api/v1/admin/documents?"+test.query, nil, adminToken)
		if status != http.StatusOK || result["data"].(map[string]any)["total"].(float64) != test.want {
			t.Fatalf("筛选 %s 结果错误: %d %v", test.query, status, result)
		}
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/documents?book_id=bad", nil, adminToken); status != http.StatusBadRequest {
		t.Fatalf("非法书籍 ID 应返回 400: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/documents?sort=evil;DROP", nil, adminToken); status != http.StatusOK {
		t.Fatalf("非法排序应安全回退: %d", status)
	}

	childPath := "/api/v1/documents/" + strconv.FormatUint(childID, 10)
	status, updated := request(http.MethodPut, childPath, map[string]any{
		"status": "archived", "allow_comments": false,
	}, adminToken)
	if status != http.StatusOK || updated["data"].(map[string]any)["status"] != "archived" || updated["data"].(map[string]any)["allow_comments"] != false {
		t.Fatalf("管理员更新章节失败: %d %v", status, updated)
	}

	parentPath := "/api/v1/documents/" + strconv.FormatUint(parentID, 10)
	status, deleted := request(http.MethodDelete, parentPath, nil, adminToken)
	if status != http.StatusOK || deleted["data"].(map[string]any)["count"].(float64) != 2 {
		t.Fatalf("管理员递归删除章节失败: %d %v", status, deleted)
	}
}

package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// 后台书籍管理列表：验证管理员权限、私有内容可见、筛选与安全排序回退。
func TestAdminBooks(t *testing.T) {
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
		"site":     map[string]any{"name": "书籍管理测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)

	_, registered := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "email": "alice@test.local", "password": "secret123",
	}, "")
	aliceToken := registered["data"].(map[string]any)["token"].(string)

	books := []struct {
		token string
		body  map[string]any
	}{
		{adminToken, map[string]any{"title": "管理员私有草稿", "slug": "admin-draft", "status": "draft", "is_public": false}},
		{aliceToken, map[string]any{"title": "Alice 公开书", "slug": "alice-public", "status": "published", "is_public": true}},
		{aliceToken, map[string]any{"title": "Alice 归档书", "slug": "alice-archived", "status": "archived", "is_public": false}},
	}
	for _, book := range books {
		if status, payload := request(http.MethodPost, "/api/v1/books", book.body, book.token); status != http.StatusOK {
			t.Fatalf("创建测试书籍失败: %d %v", status, payload)
		}
	}

	if status, _ := request(http.MethodGet, "/api/v1/admin/books", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("匿名访问后台书籍列表应 401: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/admin/books", nil, aliceToken); status != http.StatusForbidden {
		t.Fatalf("普通用户访问后台书籍列表应 403: %d", status)
	}

	status, list := request(http.MethodGet, "/api/v1/admin/books", nil, adminToken)
	if status != http.StatusOK {
		t.Fatalf("管理员书籍列表应 200: %d %v", status, list)
	}
	data := list["data"].(map[string]any)
	items, ok := data["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("后台应返回全部 3 本书籍: %v", data["items"])
	}
	first := items[0].(map[string]any)
	if _, ok := first["chapter_count"]; !ok {
		t.Fatalf("后台书籍列表必须包含章节数量: %v", first)
	}
	if _, ok := first["user"].(map[string]any); !ok {
		t.Fatalf("后台书籍列表必须包含作者信息: %v", first["user"])
	}

	tests := []struct {
		query string
		want  float64
	}{
		{"q=alice", 2},
		{"status=draft", 1},
		{"visibility=public", 1},
		{"status=archived&visibility=private", 1},
	}
	for _, test := range tests {
		status, result := request(http.MethodGet, "/api/v1/admin/books?"+test.query, nil, adminToken)
		if status != http.StatusOK || result["data"].(map[string]any)["total"].(float64) != test.want {
			t.Fatalf("筛选 %s 结果错误: %d %v", test.query, status, result)
		}
	}

	if status, _ := request(http.MethodGet, "/api/v1/admin/books?sort=evil;DROP", nil, adminToken); status != http.StatusOK {
		t.Fatalf("非法排序应安全回退到默认排序: %d", status)
	}
}

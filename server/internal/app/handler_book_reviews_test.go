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

func TestBookReviews(t *testing.T) {
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
	req := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}

	_, installed := req(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "评价测试"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	authorToken := installed["data"].(map[string]any)["token"].(string)

	_, reg := req(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "reader", "email": "reader@test.local", "password": "secret123",
	}, "")
	readerToken := reg["data"].(map[string]any)["token"].(string)

	_, created := req(http.MethodPost, "/api/v1/books", map[string]any{"title": "被评价的书", "status": "published", "is_public": true}, authorToken)
	bookID := int(created["data"].(map[string]any)["id"].(float64))

	// 作者不能评价自己的书
	if status, _ := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reviews", bookID), map[string]any{"rating": 5}, authorToken); status != http.StatusForbidden {
		t.Fatalf("作者评价自己的书应 403，实际 %d", status)
	}

	// 评分越界应 400
	if status, _ := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reviews", bookID), map[string]any{"rating": 6}, readerToken); status != http.StatusBadRequest {
		t.Fatalf("评分越界应 400，实际 %d", status)
	}

	// reader 首次评价：4 星
	status, first := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reviews", bookID), map[string]any{"rating": 4, "content": "不错"}, readerToken)
	if status != http.StatusOK {
		t.Fatalf("首次评价应成功: %d %v", status, first)
	}
	reviewID := int(first["data"].(map[string]any)["review"].(map[string]any)["id"].(float64))

	// 再次提交视为更新（不新增记录）：改为 2 星
	req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/reviews", bookID), map[string]any{"rating": 2, "content": "改主意了"}, readerToken)

	// 列表：应恰好 1 条，平均分为 2
	_, list := req(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/reviews", bookID), nil, "")
	data := list["data"].(map[string]any)
	if int(data["total"].(float64)) != 1 {
		t.Fatalf("重复提交应更新而非新增，total 应为 1，实际 %v", data["total"])
	}
	if avg := data["summary"].(map[string]any)["average"].(float64); avg != 2 {
		t.Fatalf("平均分应为 2，实际 %v", avg)
	}

	// 作者可删除他人评价
	if status, _ := req(http.MethodDelete, fmt.Sprintf("/api/v1/reviews/%d", reviewID), nil, authorToken); status != http.StatusOK {
		t.Fatalf("作者删除评价应成功，实际 %d", status)
	}
	_, after := req(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/reviews", bookID), nil, "")
	if int(after["data"].(map[string]any)["total"].(float64)) != 0 {
		t.Fatalf("删除后 total 应为 0")
	}
}

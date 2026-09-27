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

// TestDocumentStatusCascadeAndFollowParent 覆盖：子章节状态跟随父章节的创建默认，以及父章节改状态时的级联。
func TestDocumentStatusCascadeAndFollowParent(t *testing.T) {
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
		"site":     map[string]any{"name": "级联测试"},
		"admin":    map[string]any{"username": "author", "email": "author@test.local", "password": "secret123"},
	}, "")
	token := installed["data"].(map[string]any)["token"].(string)

	// 书籍开启「子章节状态跟随父章节」
	_, bookResp := req(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "书", "status": "published", "child_status_follow_parent": true,
	}, token)
	bookID := int(bookResp["data"].(map[string]any)["id"].(float64))
	docID := func(m map[string]any) int { return int(m["data"].(map[string]any)["id"].(float64)) }
	docStatus := func(m map[string]any) string { return m["data"].(map[string]any)["status"].(string) }

	// 父章节：已发布
	_, parent := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "父", "status": "published",
	}, token)
	parentID := docID(parent)

	// 子章节：不指定状态 → 跟随父章节（published）
	_, child := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "子", "parent_id": parentID,
	}, token)
	childID := docID(child)
	if got := docStatus(child); got != "published" {
		t.Fatalf("子章节应跟随父章节为 published，实际 %s", got)
	}

	// 显式指定状态时不被覆盖
	_, child2 := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "子2", "parent_id": parentID, "status": "draft",
	}, token)
	if got := docStatus(child2); got != "draft" {
		t.Fatalf("显式 draft 不应被覆盖，实际 %s", got)
	}

	// 级联：父章节改为 draft 且 cascade_status=true → 子章节一并变 draft
	req(http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", parentID), map[string]any{
		"status": "draft", "cascade_status": true,
	}, token)
	_, reloaded := req(http.MethodGet, fmt.Sprintf("/api/v1/documents/%d", childID), nil, token)
	if got := docStatus(reloaded); got != "draft" {
		t.Fatalf("级联后子章节应为 draft，实际 %s", got)
	}

	// 不级联：父章节改回 published 但 cascade_status 缺省 → 子章节保持 draft
	req(http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", parentID), map[string]any{
		"status": "published",
	}, token)
	_, again := req(http.MethodGet, fmt.Sprintf("/api/v1/documents/%d", childID), nil, token)
	if got := docStatus(again); got != "draft" {
		t.Fatalf("未级联时子章节应保持 draft，实际 %s", got)
	}
}

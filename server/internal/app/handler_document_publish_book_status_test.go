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

// TestPublishChapterPromotesDraftBook 覆盖：发布章节时，草稿书籍自动提升为 in_progress；
// 已处于 in_progress/published/completed/archived 的书籍状态保持不变（只升不降）。
func TestPublishChapterPromotesDraftBook(t *testing.T) {
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
		"site":     map[string]any{"name": "发布联动测试"},
		"admin":    map[string]any{"username": "author", "email": "author@test.local", "password": "secret123"},
	}, "")
	token := installed["data"].(map[string]any)["token"].(string)

	bookID := func(m map[string]any) int { return int(m["data"].(map[string]any)["id"].(float64)) }
	docID := func(m map[string]any) int { return int(m["data"].(map[string]any)["id"].(float64)) }
	bookStatus := func(id int) string {
		_, resp := req(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", id), nil, token)
		return resp["data"].(map[string]any)["status"].(string)
	}
	createDoc := func(book int, title string) int {
		_, resp := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", book), map[string]any{
			"title": title, "status": "draft",
		}, token)
		return docID(resp)
	}

	// 场景 1：草稿书籍发布章节 → 书籍提升为 in_progress
	_, draftBook := req(http.MethodPost, "/api/v1/books", map[string]any{"title": "草稿书"}, token)
	draftBookID := bookID(draftBook)
	if got := bookStatus(draftBookID); got != "draft" {
		t.Fatalf("新建书籍默认应为 draft，实际 %s", got)
	}
	doc1 := createDoc(draftBookID, "章节一")
	req(http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", doc1), map[string]any{
		"status": "published", "create_revision": true, "revision_reason": "publish",
	}, token)
	if got := bookStatus(draftBookID); got != "in_progress" {
		t.Fatalf("发布章节后草稿书应提升为 in_progress，实际 %s", got)
	}

	// 场景 2：已是 in_progress 的书籍发布章节 → 保持 in_progress
	req(http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", doc1), map[string]any{
		"status": "draft",
	}, token)
	doc2 := createDoc(draftBookID, "章节二")
	req(http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", doc2), map[string]any{
		"status": "published", "revision_reason": "publish",
	}, token)
	if got := bookStatus(draftBookID); got != "in_progress" {
		t.Fatalf("in_progress 书籍发布章节后应保持不变，实际 %s", got)
	}

	// 场景 3：published 书籍发布章节 → 不降级也不变动
	_, pubBook := req(http.MethodPost, "/api/v1/books", map[string]any{"title": "已发布书", "status": "published"}, token)
	pubBookID := bookID(pubBook)
	doc3 := createDoc(pubBookID, "章节三")
	req(http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", doc3), map[string]any{
		"status": "published", "revision_reason": "publish",
	}, token)
	if got := bookStatus(pubBookID); got != "published" {
		t.Fatalf("published 书籍发布章节后应保持 published，实际 %s", got)
	}

	// 场景 4：仅保存草稿章节（非发布）不应改变书籍状态
	_, draftBook2 := req(http.MethodPost, "/api/v1/books", map[string]any{"title": "草稿书2"}, token)
	draftBook2ID := bookID(draftBook2)
	doc4 := createDoc(draftBook2ID, "章节四")
	req(http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", doc4), map[string]any{
		"status": "draft", "revision_reason": "save",
	}, token)
	if got := bookStatus(draftBook2ID); got != "draft" {
		t.Fatalf("保存草稿章节不应改变书籍状态，实际 %s", got)
	}
}

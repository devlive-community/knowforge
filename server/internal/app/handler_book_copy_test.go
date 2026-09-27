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

func TestCopyBook(t *testing.T) {
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
		"site":     map[string]any{"name": "复制测试"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	token := installed["data"].(map[string]any)["token"].(string)

	_, created := req(http.MethodPost, "/api/v1/books", map[string]any{"title": "原书", "status": "published", "is_public": true}, token)
	bookID := int(created["data"].(map[string]any)["id"].(float64))
	req(http.MethodPut, fmt.Sprintf("/api/v1/books/%d", bookID), map[string]any{"extra_info": []any{map[string]any{"type": "source", "value": "https://example.com/docs"}}}, token)
	mkDoc := func(title string, parent *int) int {
		body := map[string]any{"title": title, "content": "正文 " + title, "status": "published"}
		if parent != nil {
			body["parent_id"] = *parent
		}
		_, d := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), body, token)
		return int(d["data"].(map[string]any)["id"].(float64))
	}
	a1 := mkDoc("A", nil)
	mkDoc("B", &a1) // A 的子章节
	c1 := mkDoc("C", nil)

	// 整本复制：3 章、保留父子；新书为私有草稿
	status, full := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/copy", bookID), map[string]any{"mode": "full"}, token)
	if status != http.StatusOK {
		t.Fatalf("整本复制应成功: %d %v", status, full)
	}
	fb := full["data"].(map[string]any)
	if fb["copied_documents"] != float64(3) {
		t.Fatalf("整本应复制 3 章，实际 %v", fb["copied_documents"])
	}
	newBook := fb["book"].(map[string]any)
	if newBook["is_public"] != false || newBook["status"] != "draft" {
		t.Fatalf("副本应为私有草稿: %v", newBook)
	}
	if info, _ := newBook["extra_info"].([]any); len(info) != 1 {
		t.Fatalf("副本应保留更多信息: %v", newBook["extra_info"])
	}
	newID := int(newBook["id"].(float64))
	_, tree := req(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents", newID), nil, token)
	top := tree["data"].([]any)
	if len(top) != 2 { // A、C 为顶层，B 在 A 下
		t.Fatalf("整本复制顶层应 2 章，实际 %d", len(top))
	}

	// 自选复制：仅 C、A，且顺序为 C 在前（B 排除，A 复制后无子章节）
	status, custom := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/copy", bookID), map[string]any{"mode": "custom", "doc_ids": []int{c1, a1}}, token)
	if status != http.StatusOK || custom["data"].(map[string]any)["copied_documents"] != float64(2) {
		t.Fatalf("自选复制 2 章失败: %d %v", status, custom)
	}
	cid := int(custom["data"].(map[string]any)["book"].(map[string]any)["id"].(float64))
	_, ctree := req(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents", cid), nil, token)
	ctop := ctree["data"].([]any)
	if len(ctop) != 2 {
		t.Fatalf("自选复制应 2 个顶层章节（B 被排除），实际 %d", len(ctop))
	}
	if ctop[0].(map[string]any)["title"] != "C" {
		t.Fatalf("自选顺序应 C 在前，实际 %v", ctop[0].(map[string]any)["title"])
	}

	// 章节复制到目标书籍：选中 A（含子 B）复制到目标书，保持父子；再单独复制 C
	_, tgt := req(http.MethodPost, "/api/v1/books", map[string]any{"title": "目标书", "status": "draft"}, token)
	targetID := int(tgt["data"].(map[string]any)["id"].(float64))
	status, cp := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents/copy", bookID),
		map[string]any{"target_book_id": targetID, "doc_ids": []int{a1}}, token)
	if status != http.StatusOK || cp["data"].(map[string]any)["copied_documents"] != float64(2) {
		t.Fatalf("复制 A 含子 B 应 2 章: %d %v", status, cp)
	}
	_, ttree := req(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents", targetID), nil, token)
	ttop := ttree["data"].([]any)
	if len(ttop) != 1 || ttop[0].(map[string]any)["title"] != "A" {
		t.Fatalf("目标书顶层应仅 A，实际 %v", ttop)
	}
	achildren, _ := ttop[0].(map[string]any)["children"].([]any)
	if len(achildren) != 1 || achildren[0].(map[string]any)["title"] != "B" {
		t.Fatalf("A 下应保留子章节 B，实际 %v", achildren)
	}
	// 单条复制 C，追加到目标书顶层末尾
	status, cp2 := req(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents/copy", bookID),
		map[string]any{"target_book_id": targetID, "doc_ids": []int{c1}}, token)
	if status != http.StatusOK || cp2["data"].(map[string]any)["copied_documents"] != float64(1) {
		t.Fatalf("单条复制 C 应 1 章: %d %v", status, cp2)
	}
	_, ttree2 := req(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents", targetID), nil, token)
	if len(ttree2["data"].([]any)) != 2 {
		t.Fatalf("目标书顶层应为 A、C 两章，实际 %d", len(ttree2["data"].([]any)))
	}
}

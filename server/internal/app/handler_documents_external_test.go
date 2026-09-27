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

func uintStr(v uint) string { return strconv.FormatUint(uint64(v), 10) }

// 外链章节：保存 external_url 后应能持久化并原样读回（防回归：#17 保存后被还原）。
func TestDocumentExternalURLRoundTrip(t *testing.T) {
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
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	_, installed := req(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "外链章节测试站"},
		"admin":    map[string]any{"username": "alice", "email": "alice@test.local", "password": "secret123"},
	}, "")
	token := installed["data"].(map[string]any)["token"].(string)

	_, created := req(http.MethodPost, "/api/v1/books", map[string]any{"title": "书", "status": "published", "is_public": true}, token)
	bookID := uint(created["data"].(map[string]any)["id"].(float64))

	// 创建外链章节：只给 title + external_url，不给正文。
	status, doc := req(http.MethodPost, "/api/v1/books/"+uintStr(bookID)+"/documents", map[string]any{
		"title": "外链章节", "external_url": "https://example.com/docs", "status": "published",
	}, token)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("创建外链章节失败: %d %v", status, doc)
	}
	docData := doc["data"].(map[string]any)
	docID := uint(docData["id"].(float64))
	if docData["external_url"] != "https://example.com/docs" {
		t.Fatalf("创建响应应含 external_url，实际 %v", docData["external_url"])
	}

	// 读回：external_url 应保留。
	_, got := req(http.MethodGet, "/api/v1/documents/"+uintStr(docID), nil, token)
	if got["data"].(map[string]any)["external_url"] != "https://example.com/docs" {
		t.Fatalf("读回 external_url 丢失: %v", got["data"])
	}

	// 更新地址：应持久化新值。
	req(http.MethodPut, "/api/v1/documents/"+uintStr(docID), map[string]any{"external_url": "https://example.com/new"}, token)
	_, got2 := req(http.MethodGet, "/api/v1/documents/"+uintStr(docID), nil, token)
	if got2["data"].(map[string]any)["external_url"] != "https://example.com/new" {
		t.Fatalf("更新后 external_url 未持久化: %v", got2["data"])
	}

	// 清空地址：转回普通章节。
	req(http.MethodPut, "/api/v1/documents/"+uintStr(docID), map[string]any{"external_url": ""}, token)
	_, got3 := req(http.MethodGet, "/api/v1/documents/"+uintStr(docID), nil, token)
	if v := got3["data"].(map[string]any)["external_url"]; v != "" {
		t.Fatalf("清空后 external_url 应为空，实际 %v", v)
	}

	// 列表/树接口也应带上 external_url（详情/阅读目录渲染外链需要）。
	req(http.MethodPut, "/api/v1/documents/"+uintStr(docID), map[string]any{"external_url": "https://example.com/tree"}, token)
	_, list := req(http.MethodGet, "/api/v1/books/"+uintStr(bookID)+"/documents", nil, token)
	items := list["data"].([]any)
	if len(items) == 0 || items[0].(map[string]any)["external_url"] != "https://example.com/tree" {
		t.Fatalf("目录树接口应返回 external_url，实际 %v", list["data"])
	}
}

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// 搜索接口集成测试：匿名可搜公开内容、空结果必须是数组（防 nil 切片序列化 null）、章节结果带 doc_slug/book_slug
func TestGlobalSearch(t *testing.T) {
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

	client := &http.Client{Timeout: 10 * 1e9}
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

	// 安装并取得管理员令牌
	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "搜索测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	want := map[string]searchBackend{"sqlite": searchBackendSQLite, "mysql": searchBackendMySQL, "postgres": searchBackendPostgres}[testdb.Kind()]
	if a.search != want {
		t.Fatalf("%s 应启用 %q 全文索引，实际为 %q", testdb.Kind(), want, a.search)
	}

	// 公开书籍 + 命中内容的章节
	status, book := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Quantumflux Handbook", "description": "search fixture", "status": "published", "is_public": true,
	}, adminToken)
	if status != 200 {
		t.Fatalf("建书失败: %d %v", status, book)
	}
	bookID := int(book["data"].(map[string]any)["id"].(float64))
	request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), map[string]any{
		"title": "Alpha Chapter", "content": "zebraunicorn 独有关键词正文", "status": "published",
	}, adminToken)

	searchPath := func(path string, token string) map[string]any {
		t.Helper()
		s, payload := request(http.MethodGet, "/api/v1/search"+path, nil, token)
		if s != 200 {
			t.Fatalf("搜索 %q 返回 %d: %v", path, s, payload)
		}
		return payload["data"].(map[string]any)
	}
	search := func(q string) map[string]any {
		t.Helper()
		if q == "" {
			return searchPath("", "")
		}
		return searchPath("?q="+url.QueryEscape(q), "")
	}

	// 无关键词：空结果必须是数组而非 null
	empty := search("")
	if _, ok := empty["books"].([]any); !ok {
		t.Errorf("books 应为 [] 而非 %v", empty["books"])
	}
	if _, ok := empty["documents"].([]any); !ok {
		t.Errorf("documents 应为 [] 而非 %v", empty["documents"])
	}

	// 命中书籍标题：书籍结果非空，章节为空数组
	byTitle := search("Quantumflux")
	books, ok := byTitle["books"].([]any)
	if !ok || len(books) != 1 {
		t.Fatalf("标题搜索应命中 1 本书: %v", byTitle["books"])
	}
	if docs, ok := byTitle["documents"].([]any); !ok || len(docs) != 0 {
		t.Errorf("无章节命中时 documents 应为 []: %v", byTitle["documents"])
	}

	// 命中章节正文：结果带 doc_slug 与 book_slug，书籍为空数组
	byContent := search("zebraunicorn")
	docs, ok := byContent["documents"].([]any)
	if !ok || len(docs) != 1 {
		t.Fatalf("正文搜索应命中 1 章: %v", byContent["documents"])
	}
	doc := docs[0].(map[string]any)
	if doc["doc_slug"] == "" || doc["book_slug"] == "" {
		t.Errorf("章节结果应携带 doc_slug/book_slug: %v", doc)
	}
	if bs, ok := byContent["books"].([]any); !ok || len(bs) != 0 {
		t.Errorf("无书籍命中时 books 应为 []: %v", byContent["books"])
	}
	byChineseSubstring := search("关键词")
	if docs, ok := byChineseSubstring["documents"].([]any); !ok || len(docs) != 1 {
		t.Fatalf("FTS5 trigram 应命中文中子串: %v", byChineseSubstring)
	}

	// 筛选、分类与分页：作者和标签筛选作用于书籍及其章节，分页返回稳定总数。
	_, secondBook := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "Quantumflux Patterns", "description": "second fixture", "status": "published", "is_public": true,
		"tags": []string{"Physics"},
	}, adminToken)
	if secondBook["success"] != true {
		t.Fatalf("创建筛选书籍失败: %v", secondBook)
	}
	pageOne := searchPath("?q=Quantumflux&type=book&page=1&page_size=1", "")
	if pageOne["total"] != float64(2) || len(pageOne["books"].([]any)) != 1 || pageOne["page_size"] != float64(1) {
		t.Fatalf("书籍分页结果不正确: %v", pageOne)
	}
	byAuthor := searchPath("?q=Quantumflux&type=book&author=admin", "")
	if byAuthor["book_total"] != float64(2) {
		t.Fatalf("作者筛选应命中 2 本书: %v", byAuthor)
	}
	byTag := searchPath("?q=Quantumflux&type=book&tag=physics", "")
	if byTag["book_total"] != float64(1) {
		t.Fatalf("标签筛选应命中 1 本书: %v", byTag)
	}
	byDate := searchPath("?q=Quantumflux&type=book&updated_from=2000-01-01&updated_to=2999-12-31", "")
	if byDate["book_total"] != float64(2) {
		t.Fatalf("日期范围筛选应命中 2 本书: %v", byDate)
	}
	beforeCreated := searchPath("?q=Quantumflux&type=book&updated_to=2000-01-01", "")
	if beforeCreated["total"] != float64(0) {
		t.Fatalf("结束日期筛选不应命中书籍: %v", beforeCreated)
	}

	// 短关键词走 LIKE 回退时，百分号和下划线必须按普通字符而非通配符处理。
	request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "50%", "description": "literal marker", "status": "published", "is_public": true,
	}, adminToken)
	literal := searchPath("?q="+url.QueryEscape("50%")+"&type=book", "")
	if literal["book_total"] != float64(1) {
		t.Fatalf("LIKE 特殊字符应按字面量搜索: %v", literal)
	}

	// 索引命中不能绕过可见性：匿名看不到私有草稿，所有者可以搜索到。
	request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "PrivateNeedle", "status": "draft", "is_public": false,
	}, adminToken)
	if hidden := searchPath("?q=PrivateNeedle&type=book", ""); hidden["total"] != float64(0) {
		t.Fatalf("匿名搜索泄露私有书籍: %v", hidden)
	}
	if owned := searchPath("?q=PrivateNeedle&type=book", adminToken); owned["total"] != float64(1) {
		t.Fatalf("所有者应能搜索自己的私有书籍: %v", owned)
	}

	status, invalid := request(http.MethodGet, "/api/v1/search?q=test&type=unknown", nil, "")
	if status != http.StatusBadRequest || invalid["success"] != false {
		t.Fatalf("非法搜索类型应返回 400: %d %v", status, invalid)
	}
}

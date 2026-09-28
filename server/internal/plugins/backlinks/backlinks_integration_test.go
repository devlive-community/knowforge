package backlinks_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 反向链接：[[标题]]、[[slug|文字]]、doc: 链接与跨书链接都计入被引用；目标改名后按新标题匹配；
// 草稿、私有书籍的来源不显示；付费来源不给上下文；作者可查看章节链接关系与失效链接。
func TestBacklinks(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Router())
	t.Cleanup(srv.Close)
	do := func(token, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	data := func(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"链接站"},"admin":{"username":"bl-admin","email":"bl-admin@test.local","password":"secret123"}}`)
	adminToken := data(installed)["token"].(string)

	author := &models.User{Username: "bl-author", Email: "bl-author@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(author)
	token, _ := auth.GenerateToken(a.Config.Secret, author.ID, author.Username, author.Role)
	newBook := func(title string, public bool) (uint, string) {
		_, p := do(token, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":%q,"status":"published","is_public":%v}`, title, public))
		return uint(data(p)["id"].(float64)), data(p)["slug"].(string)
	}
	newDoc := func(bookID uint, title, slug, content, status string) uint {
		status2, p := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":%q,"slug":%q,"content":%q,"status":%q}`, title, slug, content, status))
		if status2 != http.StatusOK {
			t.Fatalf("建章失败: %d %v", status2, p)
		}
		return uint(data(p)["id"].(float64))
	}
	backlinks := func(docID uint) []map[string]any {
		status, p := do("", http.MethodGet, fmt.Sprintf("/api/v1/backlinks/docs/%d", docID), "")
		if status != http.StatusOK {
			t.Fatalf("被引用接口失败: %d %v", status, p)
		}
		out := []map[string]any{}
		for _, it := range data(p)["items"].([]any) {
			out = append(out, it.(map[string]any))
		}
		return out
	}
	titles := func(items []map[string]any) []string {
		out := []string{}
		for _, it := range items {
			out = append(out, it["title"].(string))
		}
		return out
	}

	// 插件未启用时不可用
	if status, _ := do("", http.MethodGet, "/api/v1/backlinks/docs/1", ""); status != http.StatusNotFound {
		t.Fatalf("插件未启用时应不可用: %d", status)
	}
	bookID, bookSlug := newBook("缓存手册", true)
	// 启用前已有的链接：启用时建立索引
	target := newDoc(bookID, "缓存基础", "basics", "缓存是什么", "published")
	early := newDoc(bookID, "早期章节", "early", "先读 [[缓存基础]]。", "published")
	if status, p := do(adminToken, http.MethodPost, "/api/v1/admin/plugins/backlinks/install", ""); status != http.StatusOK {
		t.Fatalf("启用插件失败: %d %v", status, p)
	}
	if got := titles(backlinks(target)); len(got) != 1 || got[0] != "早期章节" {
		t.Fatalf("启用插件应为已有章节建立索引: %v", got)
	}

	newDoc(bookID, "按 slug", "by-slug", "见 [[basics|这里]]，代码里的 `[[缓存基础]]` 不算。", "published")
	newDoc(bookID, "doc 写法", "by-doc", "## 小节\n\n前文很长很长。参考 [基础](doc:basics) 了解更多。\n\n下一段", "published")
	newDoc(bookID, "草稿", "draft", "[[basics]]", "draft")
	newDoc(bookID, "代码示例", "code", "```\n[[缓存基础]]\n```", "published")
	otherID, _ := newBook("另一本书", true)
	newDoc(otherID, "跨书引用", "cross", "[["+bookSlug+"/basics]]", "published")
	privateID, _ := newBook("私密笔记", false)
	newDoc(privateID, "私密引用", "secret", "[["+bookSlug+"/缓存基础]]", "published")

	items := backlinks(target)
	got := titles(items)
	want := []string{"早期章节", "按 slug", "doc 写法", "跨书引用"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("被引用列表异常: %v，期望 %v", got, want)
	}
	byDoc := items[2]["excerpt"].(map[string]any)
	if byDoc["before"] != "前文很长很长。参考 " || byDoc["text"] != "基础" || byDoc["after"] != " 了解更多。" {
		t.Fatalf("上下文摘要异常: %v", byDoc)
	}
	if items[3]["book_title"] != "另一本书" {
		t.Fatalf("跨书引用应带书名: %v", items[3])
	}

	// 目标改名：[[旧标题]] 不再匹配，按 slug 的仍然匹配
	do(token, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", target), `{"title":"缓存入门"}`)
	if got := titles(backlinks(target)); fmt.Sprint(got) != fmt.Sprint([]string{"按 slug", "doc 写法", "跨书引用"}) {
		t.Fatalf("改名后被引用异常: %v", got)
	}
	// 来源修改后重建：早期章节改为链接新标题
	do(token, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", early), `{"content":"先读 [[缓存入门]] 和 [[不存在]]。"}`)
	if got := titles(backlinks(target)); len(got) != 4 || got[0] != "早期章节" {
		t.Fatalf("来源修改后应重建索引: %v", got)
	}

	// 付费来源只给标题，不给上下文
	do(adminToken, http.MethodPost, "/api/v1/admin/plugins/paid-content/install", "")
	do(token, http.MethodPut, fmt.Sprintf("/api/v1/books/%d/paid-settings", bookID), `{"enabled":true,"chapter_price_cents":300,"free_chapters":0,"preview_percent":10}`)
	for _, it := range backlinks(target) {
		if locked := it["book_id"].(float64) == float64(bookID); locked != (it["excerpt"] == nil) {
			t.Fatalf("付费来源不应给出上下文，免费来源应给出: %v", it)
		}
	}

	// 作者：章节链接关系与失效链接；其他人不可查看
	status, p := do(token, http.MethodGet, fmt.Sprintf("/api/v1/backlinks/books/%d/graph", bookID), "")
	if status != http.StatusOK {
		t.Fatalf("链接关系失败: %d %v", status, p)
	}
	g := data(p)
	broken := g["broken"].([]any)
	if g["edges"].(float64) != 4 || len(broken) != 1 || broken[0].(map[string]any)["target"] != "不存在" {
		t.Fatalf("链接关系异常: %v", g)
	}
	for _, n := range g["nodes"].([]any) {
		node := n.(map[string]any)
		if uint(node["id"].(float64)) == target && len(node["incoming"].([]any)) != 4 {
			t.Fatalf("目标章节应有 4 个同书来源（含草稿）: %v", node)
		}
	}
	other := &models.User{Username: "bl-other", Email: "bl-other@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(other)
	otherToken, _ := auth.GenerateToken(a.Config.Secret, other.ID, other.Username, other.Role)
	if status, _ := do(otherToken, http.MethodGet, fmt.Sprintf("/api/v1/backlinks/books/%d/graph", bookID), ""); status != http.StatusForbidden {
		t.Fatalf("非作者不应查看链接关系: %d", status)
	}
}

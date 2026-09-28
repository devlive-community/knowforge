package feeds_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

type feed struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string   `xml:"title"`
			Link        string   `xml:"link"`
			GUID        string   `xml:"guid"`
			PubDate     string   `xml:"pubDate"`
			Description string   `xml:"description"`
			Category    []string `xml:"category"`
		} `xml:"item"`
	} `xml:"channel"`
}

// RSS 订阅：书籍与作者的订阅列出最近发布的章节（草稿与私有书籍不含），摘要为纯文本；付费章节只给试读摘要；插件关闭时不可用。
func TestFeeds(t *testing.T) {
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
	var lastRaw string
	get := func(path string) (int, string, feed) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		lastRaw = string(raw)
		var f feed
		_ = xml.Unmarshal(raw, &f)
		return resp.StatusCode, resp.Header.Get("Content-Type"), f
	}
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"订阅站"},"admin":{"username":"fd-admin","email":"fd-admin@test.local","password":"secret123"}}`)
	adminToken := data(installed)["token"].(string)
	for _, key := range []string{"feeds", "paid-content"} {
		if status, p := do(adminToken, http.MethodPost, "/api/v1/admin/plugins/"+key+"/install", ""); status != http.StatusOK {
			t.Fatalf("启用 %s 失败: %d %v", key, status, p)
		}
	}
	do(adminToken, http.MethodPut, "/api/v1/mail", `{"site_url":"https://kb.example.com/"}`)

	author := &models.User{Username: "fd-author", Nickname: "作者甲", Email: "fd-author@test.local", Role: "user", IsActive: true, EmailVerified: true}
	a.DB.Create(author)
	token, _ := auth.GenerateToken(a.Config.Secret, author.ID, author.Username, author.Role)
	_, book := do(token, http.MethodPost, "/api/v1/books", `{"title":"缓存指南","description":"讲**缓存**","status":"published","is_public":true}`)
	bookID, slug := uint(data(book)["id"].(float64)), data(book)["slug"].(string)
	doc := func(title, content, status string, order int) {
		if status, p := do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":%q,"content":%q,"status":%q,"sort_order":%d}`, title, content, status, order)); status != http.StatusOK {
			t.Fatalf("建章失败: %d %v", status, p)
		}
	}
	doc("第一章", "## 开始\n\n**缓存**可以加速读取，见 [文档](https://x.test)。", "published", 0)
	doc("第二章", "第二章内容", "published", 1)
	doc("草稿章", "未发布", "draft", 2)
	_, private := do(token, http.MethodPost, "/api/v1/books", `{"title":"私密笔记","is_public":false}`)
	do(token, http.MethodPost, fmt.Sprintf("/api/v1/books/%v/documents", data(private)["id"]), `{"title":"私密章","content":"秘密","status":"published"}`)

	// 书籍订阅
	status, ctype, f := get("/api/v1/feeds/books/" + slug + ".xml")
	if status != http.StatusOK || !strings.HasPrefix(ctype, "application/rss+xml") {
		t.Fatalf("书籍订阅应可用: %d %s", status, ctype)
	}
	if f.Channel.Title != "缓存指南 - 订阅站" || len(f.Channel.Items) != 2 ||
		!strings.Contains(lastRaw, "<link>https://kb.example.com/book/detail/"+slug+"</link>") ||
		!strings.Contains(lastRaw, `<atom:link href="https://kb.example.com/api/v1/feeds/books/`+slug+`.xml" rel="self"`) {
		t.Fatalf("订阅内容异常: %+v\n%s", f.Channel, lastRaw)
	}
	if f.Channel.Items[0].Title != "第二章" || f.Channel.Items[1].Title != "第一章" {
		t.Fatalf("应按发布时间从新到旧: %+v", f.Channel.Items)
	}
	first := f.Channel.Items[1]
	if !strings.HasPrefix(first.Link, "https://kb.example.com/book/reader/"+slug+"/") || first.GUID != first.Link || first.PubDate == "" {
		t.Fatalf("条目链接异常: %+v", first)
	}
	if first.Description != "开始 缓存可以加速读取，见 文档。" {
		t.Fatalf("摘要应为纯文本: %q", first.Description)
	}
	if status, _, _ := get(fmt.Sprintf("/api/v1/feeds/books/%v.xml", data(private)["slug"])); status != http.StatusNotFound {
		t.Fatalf("私有书籍不应提供订阅: %d", status)
	}

	// 付费章节只给试读摘要
	doc("付费章", strings.Repeat("开头内容。", 30)+"\n\n结尾秘密：玫瑰花园。", "published", 3)
	do(token, http.MethodPut, fmt.Sprintf("/api/v1/books/%d/paid-settings", bookID), `{"enabled":true,"chapter_price_cents":300,"free_chapters":2,"preview_percent":30}`)
	_, _, f = get("/api/v1/feeds/books/" + slug + ".xml")
	paid := f.Channel.Items[0]
	if paid.Title != "付费章" || strings.Contains(paid.Description, "玫瑰花园") || !strings.Contains(paid.Description, "付费章节") || len(paid.Category) != 1 {
		t.Fatalf("付费章节只应给出试读摘要: %+v", paid)
	}

	// 作者订阅：只含公开书籍，标题带书名
	status, _, f = get("/api/v1/feeds/users/fd-author.xml")
	if status != http.StatusOK || f.Channel.Title != "作者甲 - 订阅站" || len(f.Channel.Items) != 3 || !strings.HasPrefix(f.Channel.Items[0].Title, "缓存指南 · ") {
		t.Fatalf("作者订阅异常: %d %+v", status, f.Channel)
	}
	for _, it := range f.Channel.Items {
		if strings.Contains(it.Title, "私密") {
			t.Fatalf("作者订阅不应包含私有书籍: %+v", it)
		}
	}

	// 插件关闭后不可用
	do(adminToken, http.MethodPost, "/api/v1/admin/plugins/feeds/uninstall", "")
	if status, _, _ := get("/api/v1/feeds/books/" + slug + ".xml"); status != http.StatusNotFound {
		t.Fatalf("插件关闭后应不可用: %d", status)
	}
}

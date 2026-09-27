package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

func TestMarkdownNamingAndOrder(t *testing.T) {
	for in, want := range map[string]string{"01-getting_started.md": "getting started", "2.安装.md": "安装", "README.md": "README", "10": "10"} {
		if got := humanizeName(in); got != want {
			t.Fatalf("humanizeName(%q) = %q，应为 %q", in, got, want)
		}
	}
	if !naturalLess("2-a.md", "10-b.md") || naturalLess("10-b.md", "2-a.md") || !naturalLess("", "a") {
		t.Fatal("自然排序错误")
	}
	title, body, _ := parseMarkdownDoc("x.md", []byte("\ufeff\r\n# 标题\r\n\r\n正文"))
	if title != "标题" || strings.TrimSpace(body) != "正文" {
		t.Fatalf("应取首行一级标题并从正文移除: %q %q", title, body)
	}
	title, _, status := parseMarkdownDoc("y.md", []byte("---\ntitle: 前置标题\nstatus: published\n---\n# 正文标题\n"))
	if title != "前置标题" || status != "published" {
		t.Fatalf("front-matter 优先: %q %q", title, status)
	}
	tree := buildMarkdownTree(map[string][]byte{
		"README.md": []byte("# 首页"), "b/10-z.md": []byte("z"), "b/2-y.md": []byte("y"), "b/index.md": []byte("# 目录 B"), "a.md": []byte("a"),
	})
	var names []string
	for _, c := range tree.children {
		names = append(names, c.title)
	}
	if strings.Join(names, ",") != "首页,a,目录 B" || tree.children[2].children[0].title != "y" || tree.children[2].children[1].title != "z" {
		t.Fatalf("章节树错误: %v / %+v", names, tree.children[2].children)
	}
}

func TestMarkdownImport(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Router())
	defer srv.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	send := func(req *http.Request, token string) (int, map[string]any) {
		t.Helper()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	jsonReq := func(method, path string, body any) *http.Request {
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	_, installed := send(jsonReq(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t), "site": map[string]any{"name": "md"},
		"admin": map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}), "")
	token := installed["data"].(map[string]any)["token"].(string)

	upload := func(path string, fields map[string]string, files map[string][]byte) (int, map[string]any) {
		t.Helper()
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		for name, data := range files {
			fw, _ := mw.CreateFormFile("files", name)
			_, _ = fw.Write(data)
		}
		_ = mw.Close()
		r, _ := http.NewRequest(http.MethodPost, srv.URL+path, body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		return send(r, token)
	}
	zipOf := func(entries map[string]string) []byte {
		buf := &bytes.Buffer{}
		w := zip.NewWriter(buf)
		for name, content := range entries {
			f, _ := w.Create(name)
			_, _ = f.Write([]byte(content))
		}
		_ = w.Close()
		return buf.Bytes()
	}
	png := string(bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 16))

	// 1. Markdown 目录 ZIP → 新书（公共根目录去除、README 作目录正文、图片上传、包内链接改写、隐藏文件忽略）
	pkg := zipOf(map[string]string{
		"docs/README.md":               "# 我的文档\n\n简介 ![logo](img/logo.png)",
		"docs/01-intro.md":             "# 入门\n\n见 [配置](02-config/README.md#x) 与 [外链](https://example.com)",
		"docs/02-config/README.md":     "# 配置\n\n配置说明 <img src=\"../img/logo.png\">",
		"docs/02-config/10-extra.md":   "# 更多",
		"docs/02-config/2-advanced.md": "# 进阶",
		"docs/img/logo.png":            png,
		"docs/.DS_Store":               "x",
		"__MACOSX/docs/._README.md":    "x",
	})
	status, res := upload("/api/v1/import/markdown", map[string]string{"publish": "true"}, map[string][]byte{"docs.zip": pkg})
	if status != http.StatusOK {
		t.Fatalf("Markdown ZIP 导入失败: %d %v", status, res)
	}
	data := res["data"].(map[string]any)
	book := data["book"].(map[string]any)
	if data["imported_doc"].(float64) != 5 {
		t.Fatalf("应导入 5 个章节: %v", data)
	}
	bookID := uint(book["id"].(float64))
	var docs []models.Document
	a.DB.Where("book_id = ?", bookID).Order("parent_id, sort_order").Find(&docs)
	byTitle := map[string]models.Document{}
	for _, d := range docs {
		byTitle[d.Title] = d
		if d.Status != "published" {
			t.Fatalf("publish=true 时章节应发布: %s %s", d.Title, d.Status)
		}
	}
	intro, cfgDoc, adv, extra, home := byTitle["入门"], byTitle["配置"], byTitle["进阶"], byTitle["更多"], byTitle["我的文档"]
	if home.ID == 0 || intro.ID == 0 || cfgDoc.ID == 0 || adv.ParentID == nil || *adv.ParentID != cfgDoc.ID || extra.SortOrder <= adv.SortOrder {
		t.Fatalf("章节层级/排序错误: %+v", byTitle)
	}
	if home.SortOrder != 0 || !strings.Contains(home.Content, "](/uploads/") || strings.Contains(cfgDoc.Content, "../img/logo.png") {
		t.Fatalf("图片应上传并改写: %q / %q", home.Content, cfgDoc.Content)
	}
	if !strings.Contains(intro.Content, "[配置](/book/reader/"+book["slug"].(string)+"/"+cfgDoc.Slug+"#x)") || !strings.Contains(intro.Content, "[外链](https://example.com)") {
		t.Fatalf("包内链接应改写为阅读页链接: %q", intro.Content)
	}

	// 2. 多个 .md 导入已有书籍的指定父章节下（状态按书籍默认规则）
	status, res = upload(fmt.Sprintf("/api/v1/books/%d/documents/import-markdown", bookID), map[string]string{"parent_id": fmt.Sprint(intro.ID)},
		map[string][]byte{"b.md": []byte("# 第二篇"), "a.md": []byte("第一篇正文")})
	if status != http.StatusOK || res["data"].(map[string]any)["imported_doc"].(float64) != 2 {
		t.Fatalf("导入到书籍失败: %d %v", status, res)
	}
	var children []models.Document
	a.DB.Where("parent_id = ?", intro.ID).Order("sort_order").Find(&children)
	if len(children) != 2 || children[0].Title != "a" || children[1].Title != "第二篇" {
		t.Fatalf("应按文件名排序挂到父章节下: %+v", children)
	}
	if status, _ := upload(fmt.Sprintf("/api/v1/books/%d/documents/import-markdown", bookID), map[string]string{"parent_id": "999999"}, map[string][]byte{"c.md": []byte("x")}); status != http.StatusBadRequest {
		t.Fatalf("不存在的父章节应 400，实际 %d", status)
	}
	if status, _ := upload("/api/v1/import/markdown", nil, map[string][]byte{"a.txt": []byte("x")}); status != http.StatusBadRequest {
		t.Fatalf("没有 Markdown 文件应 400，实际 %d", status)
	}

	// 3. 原 ZIP 导入入口：不含 book.md 时按普通 Markdown 目录导入
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "handbook.zip")
	_, _ = fw.Write(zipOf(map[string]string{"guide.md": "# 指南", "faq.md": "# 常见问题"}))
	_ = mw.Close()
	r, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/import", body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	status, res = send(r, token)
	if status != http.StatusOK && status != http.StatusAccepted {
		t.Fatalf("普通 Markdown ZIP 走 /import 失败: %d %v", status, res)
	}
	if status == http.StatusAccepted {
		if ran, runErr := a.Jobs.RunOnce(context.Background()); !ran || runErr != nil {
			t.Fatalf("执行 ZIP 导入任务失败: %v %v", ran, runErr)
		}
	}
	var n int64
	a.DB.Model(&models.Book{}).Where("title = ?", "handbook").Count(&n)
	if n != 1 {
		t.Fatalf("/import 应以 ZIP 文件名建书: %v", res)
	}
}

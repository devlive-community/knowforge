package contentcollect_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/contentcollect"
	"knowforge/server/internal/testdb"
)

// 集成测试：启动完整应用（安装向导 → 插件默认启用），经真实路由与中间件访问采集接口。

type testEnv struct {
	app    *app.App
	db     *gorm.DB
	owner  *models.User
	token  string
	server *httptest.Server
	client *http.Client
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	e := &testEnv{app: a, server: httptest.NewServer(a.Router()), client: &http.Client{Timeout: 10 * time.Second}}
	t.Cleanup(e.server.Close)
	status, installed := e.do(t, http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"采集测试"},"admin":{"username":"import-owner","email":"import-owner@test.local","password":"secret123"}}`, "")
	if status != http.StatusOK {
		t.Fatalf("安装失败: %d %v", status, installed)
	}
	e.token = installed["data"].(map[string]any)["token"].(string)
	e.db = a.DB
	e.owner = &models.User{}
	if err := e.db.Where("username = ?", "import-owner").First(e.owner).Error; err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *testEnv) do(t *testing.T, method, path, body, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	payload := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	return resp.StatusCode, payload
}

// tokenFor 为直接写库创建的用户签发登录令牌。
func (e *testEnv) tokenFor(t *testing.T, u *models.User) string {
	t.Helper()
	token, err := auth.GenerateToken(e.app.Config.Secret, u.ID, u.Username, u.Role)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func page(html string) func(context.Context, *url.URL) (contentcollect.WebPage, error) {
	return func(_ context.Context, target *url.URL) (contentcollect.WebPage, error) {
		return contentcollect.WebPage{HTML: html, FinalURL: target}, nil
	}
}

func TestImportWebAutoFallsBackToBrowser(t *testing.T) {
	e := newTestEnv(t)
	fetchCalled, renderCalled := false, false
	contentcollect.SetWebFetchers(t,
		func(ctx context.Context, target *url.URL) (contentcollect.WebPage, error) {
			fetchCalled = true
			return page(`<html><head><title>页面壳</title></head><body><div id="root"></div><script src="/app.js"></script></body></html>`)(ctx, target)
		},
		func(ctx context.Context, target *url.URL) (contentcollect.WebPage, error) {
			renderCalled = true
			return page(`<html><head><title>动态文章</title><meta name="description" content="动态网页导入测试"></head><body><nav>导航</nav><main><h1>动态文章</h1><p>这是由 JavaScript 渲染出来的文章正文，内容完整并且可以转换为 Markdown。</p><p><a href="/guide">继续阅读指南</a></p></main></body></html>`)(ctx, target)
		})

	status, payload := e.do(t, http.MethodPost, "/api/v1/import/web", `{"url":"https://8.8.8.8/articles/one","render_mode":"auto"}`, e.token)
	if status != http.StatusOK {
		t.Fatalf("网页导入失败: %d %v", status, payload)
	}
	if !fetchCalled || !renderCalled {
		t.Fatalf("自动模式应先静态抓取再回退浏览器: fetch=%v render=%v", fetchCalled, renderCalled)
	}
	if data := payload["data"].(map[string]any); data["render_mode"] != "browser" {
		t.Fatalf("响应未标记浏览器渲染: %v", data)
	}
	var book models.Book
	if err := e.db.Where("user_id = ?", e.owner.ID).First(&book).Error; err != nil {
		t.Fatal(err)
	}
	var doc models.Document
	if err := e.db.Where("book_id = ?", book.ID).First(&doc).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Content, "# 动态文章") || !strings.Contains(doc.Content, "[继续阅读指南](https://8.8.8.8/guide)") || strings.Contains(doc.Content, "导航") {
		t.Fatalf("正文提取或相对链接转换错误: %s", doc.Content)
	}
}

func TestImportWebAutoUsesBrowserWhenStaticFetchFails(t *testing.T) {
	e := newTestEnv(t)
	contentcollect.SetWebFetchers(t,
		func(context.Context, *url.URL) (contentcollect.WebPage, error) {
			return contentcollect.WebPage{}, errors.New("static endpoint denied")
		},
		page(`<html><head><title>浏览器文章</title></head><body><article><h1>浏览器文章</h1><p>静态请求失败后，浏览器仍然成功渲染出了足够长度的正文内容。</p></article></body></html>`))
	status, payload := e.do(t, http.MethodPost, "/api/v1/import/web", `{"url":"https://8.8.8.8/articles/two","render_mode":"auto"}`, e.token)
	if status != http.StatusOK || payload["data"].(map[string]any)["render_mode"] != "browser" {
		t.Fatalf("静态失败后应回退浏览器: status=%d payload=%v", status, payload)
	}
}

func TestImportWebRejectsPrivateAndUnsupportedURLs(t *testing.T) {
	e := newTestEnv(t)
	for _, rawURL := range []string{"http://127.0.0.1/admin", "http://localhost/secret", "file:///etc/passwd"} {
		if status, payload := e.do(t, http.MethodPost, "/api/v1/import/web", `{"url":"`+rawURL+`"}`, e.token); status != http.StatusBadRequest {
			t.Fatalf("危险 URL 应被拒绝: url=%s status=%d body=%v", rawURL, status, payload)
		}
	}
}

func TestImportWebDocumentRemovesPageChromeAndCreatesRevision(t *testing.T) {
	e := newTestEnv(t)
	book := models.Book{Title: "采集测试书", Slug: "collect-test", UserID: e.owner.ID, Status: "draft", IsPublic: false}
	if err := e.db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	parent := models.Document{BookID: book.ID, UserID: e.owner.ID, Title: "资料", Slug: "sources", Status: "draft"}
	if err := e.db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	contentcollect.SetWebFetchers(t, page(`<html><head><title>需要的正文</title></head><body>
			<header>站点标题和登录注册</header><nav>全站导航</nav>
			<main><article class="article-content"><h1>需要的正文</h1>
			<p>这是需要采集到书籍章节里的主要文章内容，应当被完整保留下来。</p>
			<aside>作者推荐</aside><div class="ad-container">广告内容</div>
			<div id="comments">读者评论</div><div class="article-footer">分享与相关推荐</div>
			</article></main><footer>备案信息与版权导航</footer></body></html>`), nil)
	path := "/api/v1/books/" + strconv.FormatUint(uint64(book.ID), 10) + "/documents/import-web"
	body := `{"url":"https://8.8.8.8/posts/clean","render_mode":"static","include_source":true,"parent_id":` + strconv.FormatUint(uint64(parent.ID), 10) + `,"sort_order":3}`
	if status, payload := e.do(t, http.MethodPost, path, body, e.token); status != http.StatusOK {
		t.Fatalf("采集网页章节失败: %d %v", status, payload)
	}

	var doc models.Document
	if err := e.db.Where("book_id = ? AND id <> ?", book.ID, parent.ID).First(&doc).Error; err != nil {
		t.Fatal(err)
	}
	if doc.Status != "draft" || doc.ParentID == nil || *doc.ParentID != parent.ID || doc.SortOrder != 3 {
		t.Fatalf("采集章节结构错误: %+v", doc)
	}
	for _, noise := range []string{"站点标题", "全站导航", "作者推荐", "广告内容", "读者评论", "分享与相关推荐", "备案信息"} {
		if strings.Contains(doc.Content, noise) {
			t.Fatalf("采集正文包含页面噪声 %q: %s", noise, doc.Content)
		}
	}
	if !strings.Contains(doc.Content, "主要文章内容") || !strings.Contains(doc.Content, "[原始网页](https://8.8.8.8/posts/clean)") {
		t.Fatalf("采集正文或来源链接缺失: %s", doc.Content)
	}
	var revision models.DocumentRevision
	if err := e.db.Where("document_id = ? AND reason = ?", doc.ID, "create").First(&revision).Error; err != nil {
		t.Fatalf("采集章节未生成初始版本: %v", err)
	}
	// 采集历史：单章采集记为 chapter 任务
	var jobs int64
	e.db.Model(&contentcollect.CrawlJob{}).Where("book_id = ? AND kind = ?", book.ID, "chapter").Count(&jobs)
	if jobs != 1 {
		t.Fatalf("应记录 1 条单章采集历史，实际 %d", jobs)
	}

	viewer := &models.User{Username: "import-viewer", Email: "import-viewer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	if err := e.db.Create(viewer).Error; err != nil {
		t.Fatal(err)
	}
	if status, _ := e.do(t, http.MethodPost, path, body, e.tokenFor(t, viewer)); status != http.StatusNotFound {
		t.Fatalf("无权用户采集私有书籍时应统一返回 404: %d", status)
	}
}

// 采集的第一级章节应采用书籍「章节默认状态」；子章节在开启跟随时沿用父章节状态。
func TestImportedWebDocumentFollowsDefaultChapterStatus(t *testing.T) {
	e := newTestEnv(t)
	book := models.Book{Title: "默认状态书", Slug: "default-status", UserID: e.owner.ID, Status: "published", DefaultChapterStatus: "published"}
	if err := e.db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	top, err := contentcollect.CreateImportedWebDocument(e.app, &book, e.owner, "第一级", "网页正文内容", nil)
	if err != nil || top.Status != "published" {
		t.Fatalf("第一级采集章节应为书籍默认状态 published: status=%q err=%v", top.Status, err)
	}
	child, err := contentcollect.CreateImportedWebDocument(e.app, &book, e.owner, "子章节", "网页正文内容", &top.ID)
	if err != nil || child.Status != "draft" {
		t.Fatalf("未开启跟随父章节时子章节应为草稿: status=%q err=%v", child.Status, err)
	}
	book.ChildStatusFollowParent = true
	child2, err := contentcollect.CreateImportedWebDocument(e.app, &book, e.owner, "子章节二", "网页正文内容", &top.ID)
	if err != nil || child2.Status != "published" {
		t.Fatalf("开启跟随父章节时子章节应沿用父章节状态: status=%q err=%v", child2.Status, err)
	}
}

// 整站采集执行：按页面清单抓取→建章节，父子层级映射正确，任务状态与计数正确；
// 进行中的任务使书籍列表/详情带「采集中」标记（书籍装饰钩子）。
func TestRunSiteCrawlJob(t *testing.T) {
	e := newTestEnv(t)
	bodies := map[string]string{
		"/overview":          `<main><h1>Overview</h1><p>This is the overview page with enough textual content to be treated as a real article body for markdown conversion.</p></main>`,
		"/overview/concepts": `<main><h1>Concepts</h1><p>Concepts page body with sufficient length to pass content extraction and produce a proper markdown chapter output here.</p></main>`,
		"/guide":             `<main><h1>Guide</h1><p>Guide page body with sufficient length to pass content extraction and produce a proper markdown chapter output for this one.</p></main>`,
	}
	contentcollect.SetWebFetchers(t, func(_ context.Context, target *url.URL) (contentcollect.WebPage, error) {
		return contentcollect.WebPage{HTML: `<html><head><title>` + target.Path + `</title></head><body>` + bodies[target.Path] + `</body></html>`, FinalURL: target}, nil
	}, nil)

	book := models.Book{Title: "采集书", Slug: "crawl-book", UserID: e.owner.ID, Status: "draft"}
	e.db.Create(&book)
	job := contentcollect.CrawlJob{UserID: e.owner.ID, BookID: book.ID, Kind: "site", RootURL: "https://8.8.8.8/", RenderMode: "static", Status: "pending", Total: 3}
	e.db.Create(&job)
	pages := []contentcollect.CrawlPage{
		{JobID: job.ID, URL: "https://8.8.8.8/overview", ParentURL: "", Title: "Overview", Depth: 0, SortOrder: 0, Status: "pending"},
		{JobID: job.ID, URL: "https://8.8.8.8/overview/concepts", ParentURL: "https://8.8.8.8/overview", Title: "Concepts", Depth: 1, SortOrder: 1, Status: "pending"},
		{JobID: job.ID, URL: "https://8.8.8.8/guide", ParentURL: "", Title: "Guide", Depth: 0, SortOrder: 2, Status: "pending"},
	}
	e.db.Create(&pages)

	// 任务未完成前：书籍详情带「采集中」标记
	_, detail := e.do(t, http.MethodGet, "/api/v1/books/"+strconv.FormatUint(uint64(book.ID), 10), "", e.token)
	if detail["data"].(map[string]any)["crawling"] != true {
		t.Fatalf("进行中的采集任务应让书籍带 crawling 标记: %v", detail["data"])
	}

	if err := contentcollect.RunSiteCrawlJob(e.app, job.ID); err != nil {
		t.Fatalf("采集任务执行失败: %v", err)
	}
	var got contentcollect.CrawlJob
	e.db.First(&got, job.ID)
	if got.Status != "succeeded" || got.Success != 3 || got.Failed != 0 {
		t.Fatalf("任务应全部成功，实际 status=%s success=%d failed=%d", got.Status, got.Success, got.Failed)
	}
	var docs []models.Document
	e.db.Where("book_id = ?", book.ID).Order("sort_order ASC").Find(&docs)
	if len(docs) != 3 {
		t.Fatalf("应建 3 个章节，实际 %d", len(docs))
	}
	byTitle := map[string]models.Document{}
	for _, d := range docs {
		byTitle[d.Title] = d
	}
	overview, concepts := byTitle["Overview"], byTitle["Concepts"]
	if concepts.ParentID == nil || *concepts.ParentID != overview.ID {
		t.Fatalf("Concepts 章节父级应为 Overview，实际 %+v", concepts.ParentID)
	}
	if strings.TrimSpace(overview.Content) == "" {
		t.Fatalf("Overview 章节正文不应为空")
	}
	_, list := e.do(t, http.MethodGet, "/api/v1/books?mine=true", "", e.token)
	for _, it := range list["data"].(map[string]any)["items"].([]any) {
		if b := it.(map[string]any); b["crawling"] == true {
			t.Fatalf("任务完成后不应再带 crawling 标记: %v", b)
		}
	}
}

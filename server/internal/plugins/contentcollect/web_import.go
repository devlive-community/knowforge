package contentcollect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"knowforge/server/internal/config"
	"knowforge/server/internal/mdclean"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
	"knowforge/server/internal/safehttp"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/gin-gonic/gin"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"gorm.io/gorm"
)

// 本文件是单页网页采集：抓取（静态/无头浏览器）→ 正文识别 → Markdown 转换，及网页导入成书/章节、编辑器插入正文的接口。
// 由 app 包整体搬入内容采集插件（行为不变）；PDF/ZIP 导入仍在核心。

const (
	webImportMaxHTMLBytes = 12 << 20 // 单个网页 HTML 上限
	webResourceMaxBytes   = 16 << 20 // 单次 HTTP 响应上限
)

// webFetcher / webRenderer 抓取实现，测试可替换；为 nil 时使用默认的静态抓取 / 无头浏览器渲染。
var (
	webFetcher  func(context.Context, *url.URL) (webPage, error)
	webRenderer func(context.Context, *url.URL) (webPage, error)
)

type webPage struct {
	HTML     string
	FinalURL *url.URL
}

type webImportPayload struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	RenderMode    string `json:"render_mode"`    // auto | static | browser
	IncludeSource bool   `json:"include_source"` // 是否在 Markdown 末尾附加「来源：原始网页」链接（默认否）
	ParentID      *uint  `json:"parent_id,omitempty"`
	SortOrder     *int   `json:"sort_order,omitempty"`
	BookID        *uint  `json:"book_id,omitempty"` // 插入正文式采集（/import/web-content）时带上，用于记录到该书的采集历史
}

// withSourceNote 按需在 Markdown 末尾附加来源链接（include 为 false 时原样返回）
func withSourceNote(markdown, sourceURL string, include bool) string {
	md := strings.TrimSpace(markdown)
	if include && sourceURL != "" {
		return md + "\n\n> 来源：[原始网页](" + sourceURL + ")"
	}
	return md
}

type webArticle struct {
	Title       string
	Description string
	Markdown    string
}

// ImportWebBook POST /import/web 抓取静态或 JavaScript 渲染后的网页并建立草稿书籍。
func (cc *behavior) ImportWebBook(c *gin.Context) {
	u := cc.core.CurrentUser(c)
	var req webImportPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		cc.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	// 抓取前先校验书籍数量上限，避免白抓一次
	if err := cc.core.EnsureBookQuota(u); err != nil {
		cc.core.Fail(c, http.StatusForbidden, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	article, page, usedMode, err := cc.collectWebArticle(ctx, req)
	if err != nil {
		cc.failWebImport(c, err)
		return
	}
	if customTitle := strings.TrimSpace(req.Title); customTitle != "" {
		article.Title = truncateText(customTitle, 255)
	}
	chapter := plugincore.ImportedChapter{Title: "正文", Content: withSourceNote(article.Markdown, page.FinalURL.String(), req.IncludeSource)}
	book, err := cc.core.CreateContentImportBook(u, article.Title, article.Description, []plugincore.ImportedChapter{chapter})
	if err != nil {
		cc.core.Fail(c, http.StatusInternalServerError, "创建网页书籍失败: "+err.Error())
		return
	}
	cc.core.OK(c, gin.H{
		"book": book, "imported_doc": 1, "source": "web", "render_mode": usedMode,
		"source_url": page.FinalURL.String(),
		"message":    fmt.Sprintf("导入完成：《%s》已创建为草稿", book.Title),
	})
}

// ImportWebDocument POST /books/:id/documents/import-web 抓取网页并建立草稿章节。
// recordPageCrawl 把一次「单页网页采集」写入采集历史（CrawlJob kind + 一条 CrawlPage）。
// 采集插件禁用或无书籍上下文时为空操作；失败也记录，便于用户在采集历史里看到。
func (cc *behavior) recordPageCrawl(bookID, userID uint, kind, rawURL, title string, docID uint, success bool, errMsg string) {
	if bookID == 0 || !cc.core.PluginEnabled(plugins.KeyContentCollect) {
		return
	}
	now := time.Now()
	// Job 状态沿用整站采集的约定（succeeded/failed）；Page 状态用 success/failed（CrawlPage 约定）。
	jobStatus, pageStatus, ok, failed := "succeeded", "success", 1, 0
	if !success {
		jobStatus, pageStatus, ok, failed = "failed", "failed", 0, 1
	}
	job := CrawlJob{
		UserID: userID, BookID: bookID, Kind: kind, RootURL: truncateText(rawURL, 1024),
		RenderMode: "auto", Status: jobStatus, PageLimit: 1, Total: 1, Success: ok, Failed: failed,
		LastError: errMsg, StartedAt: &now, FinishedAt: &now,
	}
	if cc.core.Gorm().Create(&job).Error != nil {
		return
	}
	cc.core.Gorm().Create(&CrawlPage{
		JobID: job.ID, URL: truncateText(rawURL, 1024), Title: truncateText(title, 512),
		Status: pageStatus, Error: errMsg, DocID: docID,
	})
}

func (cc *behavior) ImportWebDocument(c *gin.Context) {
	book, status := cc.core.FindBook(c)
	if book == nil {
		cc.core.Fail(c, status, "书籍不存在")
		return
	}
	u := cc.core.CurrentUser(c)
	if !cc.core.CanEditBookContent(u, book) {
		cc.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	var req webImportPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		cc.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.ParentID != nil {
		var count int64
		if err := cc.core.Gorm().Model(&models.Document{}).Where("id = ? AND book_id = ?", *req.ParentID, book.ID).Count(&count).Error; err != nil {
			cc.core.Fail(c, http.StatusInternalServerError, "校验父章节失败")
			return
		}
		if count == 0 {
			cc.core.Fail(c, http.StatusBadRequest, "父章节不存在")
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	article, page, usedMode, err := cc.collectWebArticle(ctx, req)
	if err != nil {
		cc.recordPageCrawl(book.ID, u.ID, "chapter", req.URL, "", 0, false, publicWebImportError(err))
		cc.failWebImport(c, err)
		return
	}
	if customTitle := strings.TrimSpace(req.Title); customTitle != "" {
		article.Title = truncateText(customTitle, 255)
	}
	content := withSourceNote(article.Markdown, page.FinalURL.String(), req.IncludeSource)
	doc, err := cc.createImportedWebDocument(book, u, article.Title, content, req.ParentID, req.SortOrder)
	if err != nil {
		cc.recordPageCrawl(book.ID, u.ID, "chapter", page.FinalURL.String(), article.Title, 0, false, err.Error())
		cc.core.Fail(c, http.StatusInternalServerError, "创建网页章节失败: "+err.Error())
		return
	}
	cc.recordPageCrawl(book.ID, u.ID, "chapter", page.FinalURL.String(), doc.Title, doc.ID, true, "")
	cc.core.OK(c, gin.H{
		"document": doc, "source_url": page.FinalURL.String(), "render_mode": usedMode,
		"message": fmt.Sprintf("已采集为草稿章节《%s》", doc.Title),
	})
}

// CollectWebContent POST /import/web-content 抓取网页正文并返回 Markdown（不建文档），供编辑器「采集内容」插入。
func (cc *behavior) CollectWebContent(c *gin.Context) {
	var req webImportPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		cc.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	// 带 book_id 且当前用户可编辑该书时，把这次采集记录到该书的采集历史（kind=page）。
	var crawlBookID uint
	if req.BookID != nil {
		var book models.Book
		if cc.core.Gorm().First(&book, *req.BookID).Error == nil && cc.core.CanEditBookContent(cc.core.CurrentUser(c), &book) {
			crawlBookID = book.ID
		}
	}
	article, page, usedMode, err := cc.collectWebArticle(ctx, req)
	if err != nil {
		cc.recordPageCrawl(crawlBookID, cc.core.CurrentUser(c).ID, "page", req.URL, "", 0, false, publicWebImportError(err))
		cc.failWebImport(c, err)
		return
	}
	cc.recordPageCrawl(crawlBookID, cc.core.CurrentUser(c).ID, "page", page.FinalURL.String(), article.Title, 0, true, "")
	cc.core.OK(c, gin.H{
		"title":       article.Title,
		"markdown":    withSourceNote(article.Markdown, page.FinalURL.String(), req.IncludeSource),
		"source_url":  page.FinalURL.String(),
		"render_mode": usedMode,
	})
}

// BrowserRenderAvailable GET /import/browser-available 无头浏览器插件是否已安装（决定「浏览器渲染」采集是否可用）
func (cc *behavior) BrowserRenderAvailable(c *gin.Context) {
	cc.core.OK(c, gin.H{"available": cc.core.InstalledChromePath() != ""})
}

func (cc *behavior) collectWebArticle(ctx context.Context, req webImportPayload) (webArticle, webPage, string, error) {
	mode := strings.ToLower(strings.TrimSpace(req.RenderMode))
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "static" && mode != "browser" {
		return webArticle{}, webPage{}, "", errors.New("render_mode 必须为 auto、static 或 browser")
	}
	target, err := validateImportURL(req.URL)
	if err != nil {
		return webArticle{}, webPage{}, "", err
	}
	fetcher := webFetcher
	if fetcher == nil {
		fetcher = fetchStaticWebPage
	}
	// 浏览器渲染依赖无头浏览器插件（chrome-headless-shell）；未安装时 browser 模式不可用。
	// 测试可注入 webRenderer 绕过插件依赖。
	renderer := webRenderer
	browserAvailable := renderer != nil
	if renderer == nil {
		chromePath := cc.core.InstalledChromePath()
		browserAvailable = chromePath != ""
		renderer = func(ctx context.Context, target *url.URL) (webPage, error) {
			return renderDynamicWebPage(ctx, target, chromePath)
		}
	}

	usedMode := mode
	var page webPage
	if mode == "browser" {
		if !browserAvailable {
			return webArticle{}, webPage{}, "", errBrowserPluginNotInstalled
		}
		page, err = renderer(ctx, target)
	} else {
		page, err = fetcher(ctx, target)
		if mode == "auto" && err != nil {
			if !browserAvailable {
				return webArticle{}, webPage{}, "", err
			}
			staticErr := err
			page, err = renderer(ctx, target)
			if err == nil {
				usedMode = "browser"
			} else {
				err = fmt.Errorf("静态抓取失败（%v），浏览器渲染也失败: %w", staticErr, err)
			}
		} else if mode == "auto" {
			article, parseErr := extractWebArticle(page)
			switch {
			case parseErr == nil && !shouldRenderSPA(page.HTML, article.Markdown):
				usedMode = "static"
			case browserAvailable:
				if rendered, renderErr := renderer(ctx, target); renderErr == nil {
					page = rendered
					usedMode = "browser"
				} else if parseErr != nil || utf8.RuneCountInString(strings.TrimSpace(article.Markdown)) < 100 {
					err = fmt.Errorf("网页需要 JavaScript 渲染，但浏览器渲染失败: %w", renderErr)
				} else {
					usedMode = "static"
				}
			default:
				// 需要 JS 渲染但插件未安装：正文过少则提示安装插件，否则退回静态尽力而为
				if parseErr != nil || utf8.RuneCountInString(strings.TrimSpace(article.Markdown)) < 100 {
					err = errBrowserPluginNotInstalled
				} else {
					usedMode = "static"
				}
			}
		}
	}
	if err != nil {
		return webArticle{}, webPage{}, "", err
	}
	article, err := extractWebArticle(page)
	if err != nil {
		return webArticle{}, webPage{}, "", fmt.Errorf("网页正文解析失败: %w", err)
	}
	// 去掉文档站常见的「永久链接」锚点（如标题后的 [🔗](… "Permanent link")），避免误跳外链。
	article.Markdown = mdclean.StripPermalinkAnchors(article.Markdown)
	return article, page, usedMode, nil
}

func (cc *behavior) failWebImport(c *gin.Context, err error) {
	log.Printf("web content import failed: %v", err)
	status, message := webImportFailure(err)
	cc.core.Fail(c, status, message)
}

// webImportFailure 把采集错误映射为 HTTP 状态码与对外文案（隐藏浏览器内部诊断）。
func webImportFailure(err error) (int, string) {
	rawMessage := err.Error()
	status := http.StatusUnprocessableEntity
	if strings.Contains(rawMessage, "render_mode") || strings.Contains(rawMessage, "网页地址") || strings.Contains(rawMessage, "不允许访问") {
		status = http.StatusBadRequest
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		status = http.StatusGatewayTimeout
	}
	return status, "网页获取失败: " + publicWebImportError(err)
}

func publicWebImportError(err error) string {
	message := err.Error()
	if errors.Is(err, errBrowserPluginNotInstalled) {
		return errBrowserPluginNotInstalled.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "处理超时，请稍后重试或改用静态抓取"
	}
	if strings.Contains(message, "Chromium") || strings.Contains(message, "debug url") || strings.Contains(message, "crashpad") {
		return "服务器浏览器启动失败，请联系管理员在后台「插件」中安装无头浏览器插件，或尝试静态抓取"
	}
	for _, safeMessage := range []string{
		"render_mode", "网页地址", "不允许访问", "无法解析网页地址", "网页返回状态码",
		"目标地址返回的不是 HTML", "网页内容超过", "网页正文", "重定向次数过多",
	} {
		if strings.Contains(message, safeMessage) {
			return truncateText(strings.Join(strings.Fields(message), " "), 300)
		}
	}
	return "无法获取或解析网页正文，请检查地址后重试"
}

func (cc *behavior) createImportedWebDocument(book *models.Book, u *models.User, title, content string, parentID *uint, sortOrder *int) (models.Document, error) {
	title = truncateText(strings.TrimSpace(title), 255)
	if title == "" {
		title = "采集的网页"
	}
	allowComments := true
	// 与新建章节一致：子章节可跟随父章节，第一级章节用书籍「章节默认状态」
	status := cc.core.InitialChapterStatus(book, parentID)
	doc := models.Document{
		BookID: book.ID, UserID: u.ID, Title: title, Content: strings.TrimSpace(content),
		ParentID: parentID, Status: status, AllowComments: &allowComments,
	}
	if sortOrder != nil {
		doc.SortOrder = *sortOrder
	}
	// 与手动新建一致：冲突时递归用祖先 slug 作前缀（b-c），最终随机兜底，不再用 xxx-2 计数后缀。
	doc.Slug = cc.core.UniqueChildSlug(book.ID, parentID, cc.core.Slugify(title), 0)
	err := cc.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&doc).Error; err != nil {
			return err
		}
		revision := cc.core.NewDocumentRevision(&doc, u.ID, "create")
		return tx.Create(&revision).Error
	})
	if err == nil {
		doc.PublishHeld = cc.core.GuardDocumentPublish(book, &doc, u.ID) // 事务外审查（直接发布时）
	}
	return doc, err
}

func validateImportURL(raw string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target.Hostname() == "" {
		return nil, errors.New("请输入有效的网页地址")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, errors.New("网页地址仅支持 http 或 https")
	}
	if target.User != nil {
		return nil, errors.New("网页地址不能包含用户名或密码")
	}
	dnsContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := validatePublicHost(dnsContext, target.Hostname()); err != nil {
		return nil, err
	}
	return target, nil
}

func validatePublicHost(ctx context.Context, host string) error {
	return safehttp.ValidatePublicHost(ctx, host)
}

func newSafeWebClient(ctx context.Context) *http.Client {
	return safehttp.NewClient(ctx, webResourceMaxBytes)
}

func fetchStaticWebPage(ctx context.Context, target *url.URL) (webPage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return webPage{}, err
	}
	request.Header.Set("User-Agent", "KnowForge-Importer/1.0 (+https://knowforge.devlive.org)")
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := newSafeWebClient(ctx).Do(request)
	if err != nil {
		return webPage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return webPage{}, fmt.Errorf("网页返回状态码 %d", response.StatusCode)
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaType != "" && mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return webPage{}, errors.New("目标地址返回的不是 HTML 网页")
	}
	decoded, err := charset.NewReader(response.Body, response.Header.Get("Content-Type"))
	if err != nil {
		return webPage{}, errors.New("无法识别网页字符编码")
	}
	data, err := io.ReadAll(io.LimitReader(decoded, webImportMaxHTMLBytes+1))
	if err != nil {
		return webPage{}, err
	}
	if len(data) > webImportMaxHTMLBytes {
		return webPage{}, errors.New("网页内容超过 12MB")
	}
	return webPage{HTML: string(data), FinalURL: response.Request.URL}, nil
}

// errBrowserPluginNotInstalled 浏览器渲染依赖无头浏览器插件（chrome-headless-shell），未安装时返回。
var errBrowserPluginNotInstalled = errors.New("网页浏览器渲染依赖无头浏览器插件，请联系管理员在后台「插件」中安装，或改用静态抓取")

// renderDynamicWebPage 用插件安装的 chrome-headless-shell 渲染网页；browserPath 来自已安装的无头浏览器插件。
func renderDynamicWebPage(ctx context.Context, target *url.URL, browserPath string) (webPage, error) {
	if browserPath == "" {
		return webPage{}, errBrowserPluginNotInstalled
	}
	profileRoot := filepath.Join(config.DataDir(), "browser-profiles")
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		return webPage{}, fmt.Errorf("无法准备 Chromium 配置目录: %w", err)
	}
	controlURL, profileDir, err := launchImportBrowser(ctx, browserPath, profileRoot)
	if err != nil {
		return webPage{}, fmt.Errorf("无法启动 Chromium: %w", err)
	}
	defer os.RemoveAll(profileDir)
	browser := rod.New().Context(ctx).ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return webPage{}, err
	}
	defer browser.Close()

	client := newSafeWebClient(ctx)
	hijacker := browser.HijackRequests()
	if err := hijacker.Add("*", "", func(request *rod.Hijack) {
		method := request.Request.Method()
		if method != http.MethodGet && method != http.MethodHead && method != http.MethodPost {
			request.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		resourceType := request.Request.Type()
		if resourceType == proto.NetworkResourceTypeImage || resourceType == proto.NetworkResourceTypeMedia || resourceType == proto.NetworkResourceTypeFont {
			request.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		resourceURL := request.Request.URL()
		if resourceURL == nil || (resourceURL.Scheme != "http" && resourceURL.Scheme != "https") {
			request.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		if err := validatePublicHost(ctx, resourceURL.Hostname()); err != nil {
			request.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		if err := request.LoadResponse(client, true); err != nil {
			request.Response.Fail(proto.NetworkErrorReasonFailed)
		}
	}); err != nil {
		return webPage{}, err
	}
	go hijacker.Run()
	defer hijacker.Stop()

	page, err := browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return webPage{}, err
	}
	defer page.Close()
	waitIdle := page.WaitRequestIdle(750*time.Millisecond, nil, nil, nil)
	if err := page.Navigate(target.String()); err != nil {
		return webPage{}, err
	}
	if err := page.WaitLoad(); err != nil {
		return webPage{}, err
	}
	waitIdle()
	markup, err := page.HTML()
	if err != nil {
		return webPage{}, err
	}
	if len(markup) > webImportMaxHTMLBytes {
		return webPage{}, errors.New("渲染后的网页内容超过 12MB")
	}
	info, err := page.Info()
	if err != nil {
		return webPage{}, err
	}
	finalURL, err := url.Parse(info.URL)
	if err != nil {
		return webPage{}, err
	}
	if err := validatePublicHost(ctx, finalURL.Hostname()); err != nil {
		return webPage{}, err
	}
	return webPage{HTML: markup, FinalURL: finalURL}, nil
}

func launchImportBrowser(ctx context.Context, browserPath, profileRoot string) (string, string, error) {
	profileDir, err := os.MkdirTemp(profileRoot, "session-")
	if err != nil {
		return "", "", fmt.Errorf("无法准备 Chromium 配置目录: %w", err)
	}
	controlURL, err := launcher.New().
		Context(ctx).
		Bin(browserPath).
		UserDataDir(profileDir).
		Headless(true).
		NoSandbox(true).
		Leakless(false).
		Set("disable-crash-reporter").
		Set("disable-breakpad").
		Set("disable-features", "Crashpad").
		Set("no-first-run").
		Set("no-default-browser-check").
		Launch()
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return "", "", err
	}
	return controlURL, profileDir, nil
}

func shouldRenderSPA(markup, markdown string) bool {
	textLength := utf8.RuneCountInString(strings.TrimSpace(stripHTMLText(markup)))
	contentLength := utf8.RuneCountInString(strings.TrimSpace(markdown))
	lower := strings.ToLower(markup)
	rootShell := strings.Contains(lower, `id="root"`) || strings.Contains(lower, `id="app"`) ||
		strings.Contains(lower, "__next_data__") || strings.Contains(lower, "data-reactroot")
	return contentLength < 300 || (rootShell && textLength < 1500)
}

func extractWebArticle(page webPage) (webArticle, error) {
	if page.FinalURL == nil {
		return webArticle{}, errors.New("网页地址缺失")
	}
	doc, err := html.Parse(strings.NewReader(page.HTML))
	if err != nil {
		return webArticle{}, err
	}
	title := firstMetaContent(doc, "property", "og:title")
	if title == "" {
		title = nodeText(firstElement(doc, "title"))
	}
	if title == "" {
		title = nodeText(firstElement(doc, "h1"))
	}
	description := firstMetaContent(doc, "name", "description")
	if description == "" {
		description = firstMetaContent(doc, "property", "og:description")
	}
	contentNode := largestContentNode(doc)
	if contentNode == nil {
		return webArticle{}, errors.New("未识别到网页正文")
	}
	pruneIgnoredNodes(contentNode)
	var markup bytes.Buffer
	if err := html.Render(&markup, contentNode); err != nil {
		return webArticle{}, err
	}
	markdownConverter := converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
		table.NewTablePlugin(table.WithHeaderPromotion(true)),
	))
	markdown, err := markdownConverter.ConvertString(markup.String(), converter.WithDomain(page.FinalURL.String()))
	if err != nil {
		return webArticle{}, err
	}
	markdown = trimPageFeedback(strings.TrimSpace(markdown)) // 页尾的「Was this page helpful?」反馈组件
	if utf8.RuneCountInString(markdown) < 20 {
		return webArticle{}, errors.New("网页正文内容过少")
	}
	title = truncateText(strings.TrimSpace(title), 255)
	if title == "" {
		title = page.FinalURL.Hostname()
	}
	description = truncateText(strings.TrimSpace(description), 1000)
	if description == "" {
		description = "从网页导入：" + page.FinalURL.String()
	}
	return webArticle{Title: title, Description: description, Markdown: markdown}, nil
}

func largestContentNode(doc *html.Node) *html.Node {
	var semanticBest *html.Node
	semanticLength := 0
	var body *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			tag := strings.ToLower(node.Data)
			if shouldIgnoreWebNode(node) {
				return
			}
			if tag == "body" {
				body = node
			}
			if isWebContentCandidate(node) {
				length := utf8.RuneCountInString(nodeText(node))
				if length > semanticLength {
					semanticBest, semanticLength = node, length
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if semanticBest != nil && semanticLength >= 20 {
		return semanticBest
	}
	return body
}

func pruneIgnoredNodes(root *html.Node) {
	for child := root.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == html.ElementNode && shouldIgnoreWebNode(child) {
			root.RemoveChild(child)
			child = next
			continue
		}
		if child.Type == html.ElementNode && strings.EqualFold(child.Data, "pre") {
			// 代码块内部不按 class 关键词剪枝（Prism 的 <span class="token comment"> 并非评论区），只规整为纯文本。
			flattenPreText(child)
			child = next
			continue
		}
		pruneIgnoredNodes(child)
		child = next
	}
}

// preLineTags 语法高亮器常用来包裹「一行」的块级元素（Prism 的 div.token-line 等）。
var preLineTags = map[string]bool{"div": true, "p": true, "li": true, "tr": true}

// flattenPreText 把 <pre> 内的高亮标记还原为纯文本：<br> 与逐行块元素合计只产生一个换行，
// 避免 Prism（<div class="token-line">…<br></div>）等结构转换后每行之间出现大面积空行；
// 保留 <pre>/<code> 自身属性以便转换器识别语言。
func flattenPreText(pre *html.Node) {
	var b strings.Builder
	endsWithNewline := true
	write := func(s string) {
		if s == "" {
			return
		}
		b.WriteString(s)
		endsWithNewline = strings.HasSuffix(s, "\n")
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			write(n.Data)
		case html.ElementNode:
			tag := strings.ToLower(n.Data)
			if tag == "br" {
				write("\n")
				return
			}
			// 复制按钮、行号等非代码内容
			if tag == "button" || tag == "script" || tag == "style" || strings.EqualFold(attribute(n, "aria-hidden"), "true") {
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			if preLineTags[tag] && !endsWithNewline {
				write("\n")
			}
		}
	}
	var code *html.Node
	var findCode func(*html.Node)
	findCode = func(n *html.Node) {
		for c := n.FirstChild; c != nil && code == nil; c = c.NextSibling {
			if c.Type == html.ElementNode && strings.EqualFold(c.Data, "code") {
				code = c
				return
			}
			findCode(c)
		}
	}
	findCode(pre)
	for c := pre.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	text := &html.Node{Type: html.TextNode, Data: strings.TrimRight(b.String(), "\n")}
	for pre.FirstChild != nil {
		pre.RemoveChild(pre.FirstChild)
	}
	if code != nil {
		if code.Parent != nil {
			code.Parent.RemoveChild(code)
		}
		for code.FirstChild != nil {
			code.RemoveChild(code.FirstChild)
		}
		code.AppendChild(text)
		pre.AppendChild(code)
		return
	}
	pre.AppendChild(text)
}

var ignoredWebRegionTokens = map[string]bool{
	"ad": true, "ads": true, "advert": true, "advertisement": true,
	"aside": true, "banner": true, "breadcrumb": true, "breadcrumbs": true,
	"comment": true, "comments": true, "cookie": true, "dialog": true,
	"footer": true, "header": true, "menu": true, "modal": true,
	"nav": true, "navigation": true, "popup": true, "recommend": true,
	"recommendation": true, "related": true, "share": true, "sharing": true,
	"sidebar": true, "social": true, "subscribe": true, "toolbar": true,
	// 页内目录（「On this page」）：toc / table-of-contents / tableOfContents_xxx 等
	"toc": true, "tableofcontents": true,
	// 文档站生成器的页面装饰：版本标记与旧版本提示、编辑链接、最后更新时间、上一页/下一页
	"theme-doc-version-badge": true, "theme-doc-version-banner": true, "theme-edit-this-page": true,
	"theme-last-updated": true, "theme-doc-footer": true, // Docusaurus
	"vpdocaside": true, "vpdocfooter": true, "edit-link": true, "prev-next": true, // VitePress
	"md-source-file": true, "md-content__button": true, // MkDocs Material
}

var webContentRegionTokens = map[string]bool{
	"article-body": true, "article-content": true, "content-body": true,
	"entry-content": true, "post-body": true, "post-content": true,
}

func shouldIgnoreWebNode(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(node.Data)
	if tag == "script" || tag == "style" || tag == "noscript" || tag == "svg" || tag == "nav" || tag == "footer" || tag == "header" || tag == "form" || tag == "aside" || tag == "dialog" || tag == "template" {
		return true
	}
	if strings.EqualFold(attribute(node, "aria-hidden"), "true") || hasAttribute(node, "hidden") {
		return true
	}
	role := strings.ToLower(attribute(node, "role"))
	if role == "banner" || role == "complementary" || role == "contentinfo" || role == "dialog" || role == "navigation" {
		return true
	}
	// 结构性容器（html/body/main/article）不按 class/id 关键词忽略：
	// 如 Docusaurus 的 <body class="navigation-with-keyboard"> 会分出 navigation，误把整页当导航丢弃。
	if tag == "html" || tag == "body" || tag == "main" || tag == "article" {
		return false
	}
	for _, token := range webRegionTokens(node) {
		if ignoredWebRegionTokens[token] {
			return true
		}
	}
	return false
}

func isWebContentCandidate(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(node.Data)
	if tag == "article" || tag == "main" || strings.EqualFold(attribute(node, "role"), "main") || strings.EqualFold(attribute(node, "itemprop"), "articleBody") {
		return true
	}
	for _, token := range webRegionTokens(node) {
		if webContentRegionTokens[token] {
			return true
		}
	}
	return false
}

func webRegionTokens(node *html.Node) []string {
	raw := strings.ToLower(attribute(node, "id") + " " + attribute(node, "class"))
	compound := strings.FieldsFunc(raw, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-'
	})
	tokens := append([]string(nil), compound...)
	for _, token := range compound {
		tokens = append(tokens, strings.FieldsFunc(token, func(r rune) bool { return r == '-' })...)
	}
	return tokens
}

func firstElement(root *html.Node, tag string) *html.Node {
	if root == nil {
		return nil
	}
	if root.Type == html.ElementNode && strings.EqualFold(root.Data, tag) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := firstElement(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func firstMetaContent(root *html.Node, key, value string) string {
	if root == nil {
		return ""
	}
	if root.Type == html.ElementNode && strings.EqualFold(root.Data, "meta") && strings.EqualFold(attribute(root, key), value) {
		return attribute(root, "content")
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := firstMetaContent(child, key, value); found != "" {
			return found
		}
	}
	return ""
}

func attribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func hasAttribute(node *html.Node, key string) bool {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}
	return false
}

func nodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == html.TextNode {
		return node.Data
	}
	if node.Type == html.ElementNode && shouldIgnoreWebNode(node) {
		return ""
	}
	var result strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text := strings.TrimSpace(nodeText(child))
		if text != "" {
			if result.Len() > 0 {
				result.WriteByte(' ')
			}
			result.WriteString(text)
		}
	}
	return result.String()
}

func stripHTMLText(markup string) string {
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return ""
	}
	return nodeText(doc)
}

func truncateText(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

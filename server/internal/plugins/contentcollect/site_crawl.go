package contentcollect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"
)

const siteCrawlJobType = "collect.site"

// crawlNode 整站采集推断出的一个目录节点。
type crawlNode struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Depth     int    `json:"depth"`
	ParentURL string `json:"parent_url"`
}

type siteCrawlJobPayload struct {
	JobID uint `json:"job_id"`
}

// —— 目录树推断（站点导航/侧边栏优先） ——

// extractNavTree 从页面 HTML 中按「导航/侧边栏优先」推断目录树：
// 选出链接最多的导航容器，按其 ul/ol 嵌套层级得到 depth 与父子关系；同域、去重、限量。
func extractNavTree(root *html.Node, base *url.URL, limit int) []crawlNode {
	nav := pickNavContainer(root)
	if nav == nil {
		nav = root
	}
	out := []crawlNode{}
	seen := map[string]bool{}
	lastAtDepth := map[int]string{}

	var walk func(n *html.Node, listDepth int)
	walk = func(n *html.Node, listDepth int) {
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if limit > 0 && len(out) >= limit {
				return
			}
			if child.Type != html.ElementNode {
				continue
			}
			nextListDepth := listDepth
			if strings.EqualFold(child.Data, "ul") || strings.EqualFold(child.Data, "ol") {
				nextListDepth = listDepth + 1
			}
			if strings.EqualFold(child.Data, "a") {
				if u := normalizeCrawlURL(attribute(child, "href"), base); u != "" && !seen[u] {
					title := strings.TrimSpace(nodeText(child))
					if title == "" {
						title = lastURLSegment(u)
					}
					depth := listDepth - 1
					if depth < 0 {
						depth = 0
					}
					parent := ""
					if depth > 0 {
						parent = lastAtDepth[depth-1]
					}
					seen[u] = true
					out = append(out, crawlNode{URL: u, Title: truncateText(title, 500), Depth: depth, ParentURL: parent})
					lastAtDepth[depth] = u
					for dd := depth + 1; dd <= 20; dd++ {
						delete(lastAtDepth, dd)
					}
				}
			}
			walk(child, nextListDepth)
		}
	}
	walk(nav, 0)
	// 归一化 depth：容器可能有若干层包裹 ul，导致最小 depth>0；整体减去最小值，使顶层从 0 开始，
	// 便于目录树按层级正确缩进展示（父子关系由 parent_url 保证，不受此影响）。
	if len(out) > 0 {
		minDepth := out[0].Depth
		for _, n := range out {
			if n.Depth < minDepth {
				minDepth = n.Depth
			}
		}
		if minDepth > 0 {
			for i := range out {
				out[i].Depth -= minDepth
			}
		}
	}
	return out
}

// pickNavContainer 在页面中挑选「后代 <a> 链接最多」的导航容器：
// 优先 nav/aside/[role=navigation] 或 class/id 含 sidebar|toc|menu|nav|contents 的元素；都没有则返回 nil。
func pickNavContainer(root *html.Node) *html.Node {
	var best *html.Node
	bestScore := 0
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && isNavCandidate(n) {
			if score := countAnchors(n); score > bestScore {
				best, bestScore = n, score
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if bestScore < 2 {
		return nil
	}
	return best
}

func isNavCandidate(n *html.Node) bool {
	if strings.EqualFold(n.Data, "nav") || strings.EqualFold(n.Data, "aside") {
		return true
	}
	if strings.EqualFold(attribute(n, "role"), "navigation") {
		return true
	}
	hint := strings.ToLower(attribute(n, "class") + " " + attribute(n, "id"))
	for _, kw := range []string{"sidebar", "toc", "table-of-contents", "menu", "nav", "contents"} {
		if strings.Contains(hint, kw) {
			return true
		}
	}
	return false
}

func countAnchors(n *html.Node) int {
	count := 0
	var walk func(*html.Node)
	walk = func(m *html.Node) {
		if m.Type == html.ElementNode && strings.EqualFold(m.Data, "a") && attribute(m, "href") != "" {
			count++
		}
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return count
}

// normalizeCrawlURL 解析并规范化爬取 URL：仅同域、http(s)、去 fragment；返回绝对地址，不合格返回空。
// mdLinkURLRe 匹配 Markdown 链接的 URL 部分：](url) 或 ](url "title") 或 ](<url>)。
var mdLinkURLRe = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)

// rewriteInternalLinks 把正文里指向 urlToSlug（归一化绝对URL→章节slug）中页面的 http(s) 外链，
// 改写为站内阅读链接 /book/reader/{book}/{slug}，并保留原 #anchor 作页内定位；未命中的外链保持不变。
func rewriteInternalLinks(markdown string, urlToSlug map[string]string, bookSlug string) string {
	if len(urlToSlug) == 0 {
		return markdown
	}
	return mdLinkURLRe.ReplaceAllStringFunc(markdown, func(m string) string {
		sub := mdLinkURLRe.FindStringSubmatch(m)
		raw := sub[1]
		low := strings.ToLower(raw)
		if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
			return m
		}
		u, err := url.Parse(raw)
		if err != nil {
			return m
		}
		frag := u.Fragment
		u.Fragment = ""
		key := u.String()
		slug, ok := urlToSlug[key]
		if !ok {
			slug, ok = urlToSlug[strings.TrimRight(key, "/")]
		}
		if !ok {
			return m
		}
		internal := "/book/reader/" + bookSlug + "/" + slug
		if frag != "" {
			internal += "#" + frag
		}
		return "](" + internal + ")"
	})
}

// rewriteCrawledInternalLinks 采集结束后，对本次采集到的章节做一遍内链改写（urlToDoc: 归一化URL→docID）。
func (cc *behavior) rewriteCrawledInternalLinks(book *models.Book, urlToDoc map[string]uint) {
	if len(urlToDoc) == 0 {
		return
	}
	ids := make([]uint, 0, len(urlToDoc))
	for _, id := range urlToDoc {
		ids = append(ids, id)
	}
	var docs []models.Document
	cc.core.Gorm().Where("id IN ?", ids).Find(&docs)
	slugByID := make(map[uint]string, len(docs))
	for i := range docs {
		slugByID[docs[i].ID] = docs[i].Slug
	}
	urlToSlug := make(map[string]string, len(urlToDoc))
	for u, id := range urlToDoc {
		if s := slugByID[id]; s != "" {
			urlToSlug[u] = s
		}
	}
	for i := range docs {
		d := &docs[i]
		if nc := rewriteInternalLinks(d.Content, urlToSlug, book.Slug); nc != d.Content {
			cc.core.Gorm().Model(&models.Document{}).Where("id = ?", d.ID).Update("content", nc)
		}
	}
}

func normalizeCrawlURL(href string, base *url.URL) string {
	href = strings.TrimSpace(href)
	low := strings.ToLower(href)
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(low, "javascript:") || strings.HasPrefix(low, "mailto:") || strings.HasPrefix(low, "tel:") {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(ref)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}
	if !strings.EqualFold(abs.Hostname(), base.Hostname()) {
		return ""
	}
	abs.Fragment = ""
	return abs.String()
}

func lastURLSegment(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	p := strings.Trim(u.Path, "/")
	if p == "" {
		return u.Hostname()
	}
	parts := strings.Split(p, "/")
	seg := strings.TrimSuffix(parts[len(parts)-1], ".html")
	seg = strings.ReplaceAll(seg, "-", " ")
	if seg == "" {
		return u.Hostname()
	}
	return seg
}

// —— 采集向导：目录预览 + 内容区抽样 ——

// SiteCrawlPreview POST /collect/site/preview
// 抓取根页面，推断目录树；并抽取「首个正文页」内容供用户确认采集内容区是否正确。
func (cc *behavior) SiteCrawlPreview(c *gin.Context) {
	var req struct {
		URL        string `json:"url"`
		RenderMode string `json:"render_mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		cc.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()

	_, page, _, err := cc.collectWebArticle(ctx, webImportPayload{URL: req.URL, RenderMode: req.RenderMode})
	if err != nil {
		cc.failWebImport(c, err)
		return
	}
	limit := cc.siteCrawlPageLimit(cc.core.CurrentUser(c))
	doc, perr := html.Parse(strings.NewReader(page.HTML))
	if perr != nil {
		cc.core.Fail(c, http.StatusUnprocessableEntity, "网页解析失败")
		return
	}
	tree := extractNavTree(doc, page.FinalURL, limit)
	// 把根地址本身作为顶层节点并入（若导航里没有）
	rootURL := page.FinalURL.String()
	hasRoot := false
	for _, n := range tree {
		if n.URL == rootURL {
			hasRoot = true
			break
		}
	}
	if !hasRoot {
		title := strings.TrimSpace(nodeText(findFirstElementByTag(doc, "h1")))
		if title == "" {
			title = page.FinalURL.Hostname()
		}
		tree = append([]crawlNode{{URL: rootURL, Title: truncateText(title, 500), Depth: 0}}, tree...)
	}

	// 内容区抽样：优先抽取第一个「非根」的目录页，回退根页面。
	sampleURL := rootURL
	for _, n := range tree {
		if n.URL != rootURL {
			sampleURL = n.URL
			break
		}
	}
	sampleArticle, _, _, sErr := cc.collectWebArticle(ctx, webImportPayload{URL: sampleURL, RenderMode: req.RenderMode})
	sample := gin.H{"url": sampleURL, "ok": sErr == nil}
	if sErr == nil {
		sample["title"] = sampleArticle.Title
		sample["markdown"] = sampleArticle.Markdown
	} else {
		sample["error"] = sErr.Error()
	}

	if len(tree) > limit {
		tree = tree[:limit]
	}
	cc.core.OK(c, gin.H{"root_url": rootURL, "tree": tree, "sample": sample, "limit": limit})
}

// findFirstElementByTag 便捷封装：从解析后的 HTML 字符串里取第一个指定标签元素。
func findFirstElementByTag(root *html.Node, tag string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// StartSiteCrawl POST /collect/site
// 用户确认目录/内容区后，创建草稿书 + 采集任务 + 页面清单，投递后台采集。
func (cc *behavior) StartSiteCrawl(c *gin.Context) {
	u := cc.core.CurrentUser(c)
	if u == nil {
		cc.core.Fail(c, http.StatusUnauthorized, "请先登录")
		return
	}
	var req struct {
		RootURL         string      `json:"root_url"`
		Title           string      `json:"title"`
		RenderMode      string      `json:"render_mode"`
		ContentSelector string      `json:"content_selector"`
		BookID          uint        `json:"book_id"` // 可选：采集到已有书籍（追加章节）；省略则新建草稿书
		Pages           []crawlNode `json:"pages"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		cc.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if strings.TrimSpace(req.RootURL) == "" || len(req.Pages) == 0 {
		cc.core.Fail(c, http.StatusBadRequest, "请提供采集地址与页面")
		return
	}
	limit := cc.siteCrawlPageLimit(cc.core.CurrentUser(c))
	if len(req.Pages) > limit {
		req.Pages = req.Pages[:limit]
	}

	// 采集到已有书籍（追加）或新建草稿书。
	var book models.Book
	if req.BookID != 0 {
		if err := cc.core.Gorm().First(&book, req.BookID).Error; err != nil {
			cc.core.Fail(c, http.StatusNotFound, "目标书籍不存在")
			return
		}
		if !cc.core.CanEditBookContent(u, &book) {
			cc.core.Fail(c, http.StatusForbidden, "无权写入目标书籍")
			return
		}
	} else {
		if err := cc.core.EnsureBookQuota(u); err != nil {
			cc.core.Fail(c, http.StatusForbidden, err.Error())
			return
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			if pu, err := url.Parse(req.RootURL); err == nil {
				title = pu.Hostname()
			} else {
				title = "采集书籍"
			}
		}
		book = models.Book{Title: truncateText(title, 255), UserID: u.ID, Status: "draft", IsPublic: false, Slug: cc.core.RandomSlug("book")}
		for i := 0; i < 50; i++ {
			var count int64
			cc.core.Gorm().Unscoped().Model(&models.Book{}).Where("slug = ?", book.Slug).Count(&count)
			if count == 0 {
				break
			}
			book.Slug = cc.core.RandomSlug("book")
		}
		if err := cc.core.Gorm().Create(&book).Error; err != nil {
			cc.core.Fail(c, http.StatusInternalServerError, "创建书籍失败")
			return
		}
	}

	mode := strings.ToLower(strings.TrimSpace(req.RenderMode))
	if mode == "" {
		mode = "auto"
	}
	job := CrawlJob{
		UserID: u.ID, BookID: book.ID, Kind: "site", RootURL: req.RootURL,
		ContentSelector: truncateText(strings.TrimSpace(req.ContentSelector), 255),
		RenderMode:      mode, Status: "pending", PageLimit: limit, Total: len(req.Pages),
	}
	if err := cc.core.Gorm().Create(&job).Error; err != nil {
		cc.core.Fail(c, http.StatusInternalServerError, "创建采集任务失败")
		return
	}
	pages := make([]CrawlPage, 0, len(req.Pages))
	for i, n := range req.Pages {
		pages = append(pages, CrawlPage{
			JobID: job.ID, URL: n.URL, ParentURL: n.ParentURL,
			Title: truncateText(strings.TrimSpace(n.Title), 500), Depth: n.Depth, SortOrder: i, Status: "pending",
		})
	}
	if err := cc.core.Gorm().CreateInBatches(&pages, 100).Error; err != nil {
		cc.core.Fail(c, http.StatusInternalServerError, "创建页面清单失败")
		return
	}

	if queue := cc.core.JobQueue(); queue != nil {
		if _, err := queue.Enqueue(context.Background(), siteCrawlJobType, siteCrawlJobPayload{JobID: job.ID}, 3); err != nil {
			cc.core.Fail(c, http.StatusInternalServerError, "任务入队失败")
			return
		}
	}
	cc.publishCrawlTask(job.ID)
	cc.core.OK(c, gin.H{"job": job, "book": book})
}

// —— 采集执行（后台任务） ——

func (cc *behavior) runSiteCrawlJob(ctx context.Context, raw json.RawMessage) error {
	var p siteCrawlJobPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("解析采集任务失败: %w", err)
	}
	var job CrawlJob
	if err := cc.core.Gorm().First(&job, p.JobID).Error; err != nil {
		return nil // 任务已删除，视为完成
	}
	if job.Status == "succeeded" || job.Status == "partial" || job.Status == "failed" {
		return nil // 已完成，幂等
	}
	var book models.Book
	if err := cc.core.Gorm().First(&book, job.BookID).Error; err != nil {
		cc.finishCrawlJob(&job, "failed", "目标书籍不存在")
		return nil
	}
	now := time.Now()
	cc.core.Gorm().Model(&job).Updates(map[string]any{"status": "running", "started_at": &now})
	cc.publishCrawlTask(job.ID)

	var pages []CrawlPage
	cc.core.Gorm().Where("job_id = ?", job.ID).Order("sort_order ASC").Find(&pages)

	// 顶层排序基准：支持采集到「已有书籍」时，顶层章节追加到现有目录末尾（不覆盖已有顺序）。
	var topBase int64
	cc.core.Gorm().Model(&models.Document{}).Where("book_id = ? AND parent_id IS NULL", book.ID).Count(&topBase)
	topSort := int(topBase)

	urlToDoc := map[string]uint{}
	success, failed := 0, 0
	for i := range pages {
		page := &pages[i]
		if page.Status == "success" && page.DocID != 0 {
			urlToDoc[page.URL] = page.DocID
			success++
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err() // 取消：保留 pending，下次重试
		default:
		}
		article, _, _, err := cc.collectWebArticle(ctx, webImportPayload{URL: page.URL, RenderMode: job.RenderMode})
		if err != nil {
			failed++
			cc.core.Gorm().Model(page).Updates(map[string]any{"status": "failed", "error": truncateText(err.Error(), 1000)})
			cc.crawlProgress(&job, success, failed)
			continue
		}
		var parentID *uint
		if page.ParentURL != "" {
			if pid, ok := urlToDoc[page.ParentURL]; ok {
				parentID = &pid
			}
		}
		title := strings.TrimSpace(page.Title)
		if title == "" {
			title = strings.TrimSpace(article.Title)
		}
		if title == "" {
			title = lastURLSegment(page.URL)
		}
		// 顶层章节追加到目标书目录末尾；子章节保留其相对顺序
		sortOrder := page.SortOrder
		if parentID == nil {
			sortOrder = topSort
			topSort++
		}
		doc, derr := cc.crawlCreateDocument(&book, job.UserID, title, article.Markdown, page.URL, parentID, sortOrder)
		if derr != nil {
			failed++
			cc.core.Gorm().Model(page).Updates(map[string]any{"status": "failed", "error": truncateText(derr.Error(), 1000)})
			cc.crawlProgress(&job, success, failed)
			continue
		}
		success++
		urlToDoc[page.URL] = doc.ID
		cc.core.Gorm().Model(page).Updates(map[string]any{"status": "success", "error": "", "doc_id": doc.ID, "title": truncateText(title, 500)})
		cc.crawlProgress(&job, success, failed)
	}

	// 采集完成后改写内链：正文里指向本次采集页面的外链改为站内阅读链接。
	cc.rewriteCrawledInternalLinks(&book, urlToDoc)

	status := "succeeded"
	if failed > 0 && success > 0 {
		status = "partial"
	} else if failed > 0 && success == 0 {
		status = "failed"
	}
	cc.core.Gorm().Model(&job).Updates(map[string]any{"success": success, "failed": failed})
	cc.finishCrawlJob(&job, status, "")
	cc.core.NotifyI18n(job.UserID, "collect.finished", "notify.collect.finished", map[string]string{"book": book.Title}, map[string]any{
		"job_id": job.ID, "book_id": book.ID, "book_slug": book.Slug, "success": success, "failed": failed, "status": status,
	})
	return nil
}

func (cc *behavior) finishCrawlJob(job *CrawlJob, status, lastErr string) {
	now := time.Now()
	cc.core.Gorm().Model(job).Updates(map[string]any{"status": status, "finished_at": &now, "last_error": truncateText(lastErr, 1000)})
	cc.publishCrawlTask(job.ID)
}

// crawlProgress 每处理完一页记录进度并推送（「我的任务」实时显示已采集页数）。
func (cc *behavior) crawlProgress(job *CrawlJob, success, failed int) {
	cc.core.Gorm().Model(job).Updates(map[string]any{"success": success, "failed": failed})
	cc.publishCrawlTask(job.ID)
}

// crawlSlugPattern 与 slugify 不同：保留大小写（不转小写），仅把非字母数字压成中划线。
var crawlSlugPattern = regexp.MustCompile(`[^A-Za-z0-9]+`)

// crawlSlugFromURL 由源页面 URL 的末段生成章节 slug，保留原始大小写（用户明确要求不要转小写）。
func crawlSlugFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := strings.Trim(u.Path, "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	seg := strings.TrimSuffix(parts[len(parts)-1], ".html")
	seg = strings.Trim(crawlSlugPattern.ReplaceAllString(seg, "-"), "-")
	if len(seg) > 200 {
		seg = seg[:200]
	}
	return seg
}

// crawlCreateDocument 在书内创建一章（唯一 slug），采集内容作为草稿章节。
// slug 优先取源 URL 末段（保留大小写），回退为标题的保留大小写 slug。
func (cc *behavior) crawlCreateDocument(book *models.Book, userID uint, title, content, sourceURL string, parentID *uint, sortOrder int) (*models.Document, error) {
	doc := models.Document{BookID: book.ID, UserID: userID, Title: truncateText(strings.TrimSpace(title), 255), Content: content, Status: cc.core.InitialChapterStatus(book, parentID), SortOrder: sortOrder, ParentID: parentID}
	doc.Icon = cc.core.ExtractDocIcon(content)
	base := crawlSlugFromURL(sourceURL)
	if base == "" {
		base = strings.Trim(crawlSlugPattern.ReplaceAllString(doc.Title, "-"), "-")
	}
	// 冲突时递归用祖先 slug 作前缀（b-c），最终随机兜底，不再用 xxx-2 计数后缀。
	doc.Slug = cc.core.UniqueChildSlug(book.ID, parentID, base, 0)
	if err := cc.core.Gorm().Create(&doc).Error; err != nil {
		return nil, err
	}
	cc.core.GuardDocumentPublish(book, &doc, userID) // 按书籍默认状态直接发布时交发布守卫（如内容审核）审查
	return &doc, nil
}

// —— 采集历史 / 详情 / 重试 ——

// canManageCrawlJob 任务归属校验：本人或管理员。
func (cc *behavior) loadOwnedCrawlJob(c *gin.Context) (*CrawlJob, bool) {
	u := cc.core.CurrentUser(c)
	if u == nil {
		cc.core.Fail(c, http.StatusUnauthorized, "请先登录")
		return nil, false
	}
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var job CrawlJob
	if err := cc.core.Gorm().First(&job, uint(id)).Error; err != nil {
		cc.core.Fail(c, http.StatusNotFound, "采集任务不存在")
		return nil, false
	}
	if job.UserID != u.ID {
		cc.core.Fail(c, http.StatusForbidden, "无权访问该采集任务")
		return nil, false
	}
	return &job, true
}

// ListBookCrawlJobs GET /books/:id/collect/jobs?kind=site|page|chapter 采集历史（本书）。
func (cc *behavior) ListBookCrawlJobs(c *gin.Context) {
	book, status := cc.core.FindBook(c)
	if book == nil {
		cc.core.Fail(c, status, "书籍不存在")
		return
	}
	if !cc.core.CanEditBookContent(cc.core.CurrentUser(c), book) {
		cc.core.Fail(c, http.StatusForbidden, "无权查看采集历史")
		return
	}
	q := cc.core.Gorm().Where("book_id = ?", book.ID)
	if kind := strings.TrimSpace(c.Query("kind")); kind != "" {
		q = q.Where("kind = ?", kind)
	}
	jobs := []CrawlJob{}
	q.Order("created_at DESC").Limit(200).Find(&jobs)
	cc.core.OK(c, gin.H{"items": jobs})
}

// GetCrawlJob GET /collect/jobs/:id 任务详情 + 页面清单（按 sort_order，供前端按目录结构展示）。
func (cc *behavior) GetCrawlJob(c *gin.Context) {
	job, ok2 := cc.loadOwnedCrawlJob(c)
	if !ok2 {
		return
	}
	pages := []CrawlPage{}
	cc.core.Gorm().Where("job_id = ?", job.ID).Order("sort_order ASC").Find(&pages)
	cc.core.OK(c, gin.H{"job": job, "pages": pages})
}

// RetryCrawlJob POST /collect/jobs/:id/retry 重试该任务所有失败页。
func (cc *behavior) RetryCrawlJob(c *gin.Context) {
	job, ok2 := cc.loadOwnedCrawlJob(c)
	if !ok2 {
		return
	}
	res := cc.core.Gorm().Model(&CrawlPage{}).Where("job_id = ? AND status = ?", job.ID, "failed").Update("status", "pending")
	if res.RowsAffected == 0 {
		cc.core.Fail(c, http.StatusBadRequest, "没有需要重试的失败页面")
		return
	}
	cc.core.Gorm().Model(job).Updates(map[string]any{"status": "pending", "finished_at": nil, "last_error": ""})
	if queue := cc.core.JobQueue(); queue != nil {
		_, _ = queue.Enqueue(context.Background(), siteCrawlJobType, siteCrawlJobPayload{JobID: job.ID}, 3)
	}
	cc.publishCrawlTask(job.ID)
	cc.core.OK(c, gin.H{"retried": res.RowsAffected})
}

// RetryCrawlPage POST /collect/pages/:id/retry 重试单个失败页。
func (cc *behavior) RetryCrawlPage(c *gin.Context) {
	u := cc.core.CurrentUser(c)
	if u == nil {
		cc.core.Fail(c, http.StatusUnauthorized, "请先登录")
		return
	}
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var page CrawlPage
	if err := cc.core.Gorm().First(&page, uint(id)).Error; err != nil {
		cc.core.Fail(c, http.StatusNotFound, "采集页面不存在")
		return
	}
	var job CrawlJob
	if err := cc.core.Gorm().First(&job, page.JobID).Error; err != nil {
		cc.core.Fail(c, http.StatusNotFound, "采集任务不存在")
		return
	}
	if job.UserID != u.ID {
		cc.core.Fail(c, http.StatusForbidden, "无权操作")
		return
	}
	cc.core.Gorm().Model(&page).Updates(map[string]any{"status": "pending", "error": ""})
	cc.core.Gorm().Model(&job).Updates(map[string]any{"status": "pending", "finished_at": nil})
	if queue := cc.core.JobQueue(); queue != nil {
		_, _ = queue.Enqueue(context.Background(), siteCrawlJobType, siteCrawlJobPayload{JobID: job.ID}, 3)
	}
	cc.publishCrawlTask(job.ID)
	cc.core.OK(c, gin.H{"retried": 1})
}

// attachCrawlingFlags 书籍装饰：为列表/详情书籍填充「采集中」标记——存在 pending/running 的采集任务即为采集中。
// 插件禁用或表不存在时静默跳过（不影响列表）。
func (cc *behavior) attachCrawlingFlags(books []*models.Book) {
	db := cc.core.Gorm()
	if len(books) == 0 || !cc.core.PluginEnabled(plugins.KeyContentCollect) || !db.Migrator().HasTable(&CrawlJob{}) {
		return
	}
	ids := make([]uint, 0, len(books))
	for _, b := range books {
		ids = append(ids, b.ID)
	}
	var activeBookIDs []uint
	db.Model(&CrawlJob{}).
		Where("book_id IN ? AND status IN ?", ids, []string{"pending", "running"}).
		Distinct().Pluck("book_id", &activeBookIDs)
	active := map[uint]bool{}
	for _, id := range activeBookIDs {
		active[id] = true
	}
	for _, b := range books {
		if active[b.ID] {
			b.Crawling = true
		}
	}
}

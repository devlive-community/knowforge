package templates

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(pluginKey)
	use := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermUse), h}
	}
	manage := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage), h}
	}
	api.GET("/templates", use(b.List)...)
	api.GET("/templates/:id", use(b.Detail)...)
	api.POST("/templates", use(b.Create)...)
	api.POST("/templates/from-book", use(b.CreateFromBook)...)
	api.PUT("/templates/:id", use(b.Update)...)
	api.DELETE("/templates/:id", use(b.Delete)...)
	api.POST("/templates/:id/render", use(b.Render)...)
	api.POST("/templates/:id/apply", use(b.Apply)...)
	// 站点模板（所有作者可用）由管理员维护
	api.GET("/admin/templates", manage(b.AdminList)...)
	api.POST("/admin/templates", manage(b.Create)...)
	api.POST("/admin/templates/from-book", manage(b.CreateFromBook)...)
	api.PUT("/admin/templates/:id", manage(b.Update)...)
	api.DELETE("/admin/templates/:id", manage(b.Delete)...)
}

// isAdminRoute 管理员接口维护的是站点模板，普通接口维护的是本人的个人模板。
func isAdminRoute(c *gin.Context) bool { return strings.Contains(c.FullPath(), "/admin/templates") }

// —— 视图 ——

// summary 列表中的模板：不含正文，章节模板给出开头预览，书籍模板给出第一级章节标题。
type summary struct {
	Template
	Preview string   `json:"preview"`
	Outline []string `json:"outline,omitempty"`
}

// detail 模板详情：含正文与目录。
type detail struct {
	Template
	Chapters []Node `json:"chapters,omitempty"`
}

func toSummary(t Template) summary {
	s := summary{Template: t}
	s.Content = ""
	if t.Kind == KindBook {
		for _, n := range decodeTree(t.Chapters) {
			if len(s.Outline) == 8 {
				break
			}
			s.Outline = append(s.Outline, n.Title)
		}
		s.Outline = append([]string{}, s.Outline...)
	} else {
		s.Preview = preview(t.Content, 160)
	}
	return s
}

func toDetail(t Template) detail {
	d := detail{Template: t}
	if t.Kind == KindBook {
		d.Chapters = decodeTree(t.Chapters)
	}
	return d
}

// preview 正文开头的纯文字预览（去掉 Markdown 标记符号与多余空白）。
func preview(content string, n int) string {
	var parts []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>-*|`:"))
		if line != "" && !strings.HasPrefix(line, "---") {
			parts = append(parts, line)
		}
	}
	text := strings.Join(parts, " ")
	if utf8.RuneCountInString(text) > n {
		text = string([]rune(text)[:n]) + "…"
	}
	return text
}

func validKind(k string) bool { return k == KindChapter || k == KindBook }

func listQuery(db *gorm.DB, kind string) *gorm.DB {
	if validKind(kind) {
		db = db.Where("kind = ?", kind)
	}
	return db
}

func summaries(list []Template) []summary {
	out := make([]summary, 0, len(list))
	for _, t := range list {
		out = append(out, toSummary(t))
	}
	return out
}

// —— 列表与详情 ——

// List GET /templates?kind=chapter|book 可用的模板：站点模板与我的个人模板，以及个人模板的数量与上限。
func (b *behavior) List(c *gin.Context) {
	u := b.core.CurrentUser(c)
	kind := c.Query("kind")
	db := b.core.Gorm()
	var official, mine []Template
	listQuery(db.Model(&Template{}), kind).Where("official = ?", true).Order("sort_order ASC, id ASC").Find(&official)
	listQuery(db.Model(&Template{}), kind).Where("official = ? AND user_id = ?", false, u.ID).Order("updated_at DESC, id DESC").Find(&mine)
	var used int64
	db.Model(&Template{}).Where("official = ? AND user_id = ?", false, u.ID).Count(&used)
	b.core.OK(c, gin.H{
		"official": summaries(official), "mine": summaries(mine),
		"used": used, "limit": plugincore.EntitlementValue(b.core, u, entMax),
	})
}

// AdminList GET /admin/templates?kind= 站点模板。
func (b *behavior) AdminList(c *gin.Context) {
	var list []Template
	listQuery(b.core.Gorm().Model(&Template{}), c.Query("kind")).Where("official = ?", true).Order("sort_order ASC, id ASC").Find(&list)
	b.core.OK(c, gin.H{"items": summaries(list)})
}

// usable 查看者可使用的模板：站点模板或本人的个人模板。
func (b *behavior) usable(c *gin.Context) (*Template, bool) {
	u := b.core.CurrentUser(c)
	var t Template
	if b.core.Gorm().First(&t, c.Param("id")).Error != nil || (!t.Official && t.UserID != u.ID) {
		b.core.Fail(c, http.StatusNotFound, "模板不存在")
		return nil, false
	}
	return &t, true
}

// editable 可修改的模板：管理员接口只改站点模板，普通接口只改本人的个人模板。
func (b *behavior) editable(c *gin.Context) (*Template, bool) {
	u := b.core.CurrentUser(c)
	var t Template
	err := b.core.Gorm().First(&t, c.Param("id")).Error
	if err == nil && isAdminRoute(c) && t.Official {
		return &t, true
	}
	if err == nil && !isAdminRoute(c) && !t.Official && t.UserID == u.ID {
		return &t, true
	}
	b.core.Fail(c, http.StatusNotFound, "模板不存在")
	return nil, false
}

// Detail GET /templates/:id 模板详情（含正文与目录）。
func (b *behavior) Detail(c *gin.Context) {
	if t, ok := b.usable(c); ok {
		b.core.OK(c, toDetail(*t))
	}
}

// —— 创建与修改 ——

type templateRequest struct {
	Kind        string  `json:"kind"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Content     *string `json:"content"`
	Chapters    *[]Node `json:"chapters"`
	SortOrder   *int    `json:"sort_order"`
}

// applyRequest 把请求中出现的字段写入模板并校验。
func (b *behavior) applyRequest(c *gin.Context, t *Template, req templateRequest) bool {
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" || utf8.RuneCountInString(title) > maxTitle {
			b.core.Fail(c, http.StatusBadRequest, "请填写模板名称（不超过 100 个字）")
			return false
		}
		t.Title = title
	}
	if req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		if utf8.RuneCountInString(desc) > maxDesc {
			b.core.Fail(c, http.StatusBadRequest, "模板说明不超过 500 个字")
			return false
		}
		t.Description = desc
	}
	if t.Kind == KindChapter && req.Content != nil {
		if len(*req.Content) > maxContentBytes {
			b.core.Fail(c, http.StatusBadRequest, "模板正文过长")
			return false
		}
		t.Content = *req.Content
	}
	if t.Kind == KindBook && req.Chapters != nil {
		nodes, count, err := normalizeTree(*req.Chapters)
		if err != nil {
			b.core.Fail(c, http.StatusBadRequest, err.Error())
			return false
		}
		t.Chapters, t.ChapterCount = encodeTree(nodes), count
	}
	if req.SortOrder != nil && t.Official {
		t.SortOrder = *req.SortOrder
	}
	return true
}

// checkQuota 个人模板数量是否已达权益上限（站点模板不受限）。
func (b *behavior) checkQuota(c *gin.Context, u *models.User) bool {
	if isAdminRoute(c) {
		return true
	}
	if limit := plugincore.EntitlementValue(b.core, u, entMax); limit != plugincore.Unlimited {
		var n int64
		b.core.Gorm().Model(&Template{}).Where("official = ? AND user_id = ?", false, u.ID).Count(&n)
		if n >= limit {
			b.core.Fail(c, http.StatusForbidden, "个人模板数量已达上限，可删除不用的模板，或升级等级、开通会员获得更多")
			return false
		}
	}
	return true
}

func (b *behavior) newTemplate(c *gin.Context, u *models.User, kind string) Template {
	t := Template{Kind: kind, UserID: u.ID}
	if isAdminRoute(c) {
		t.UserID, t.Official = 0, true
	}
	return t
}

func (b *behavior) save(c *gin.Context, t *Template, action string) {
	if err := b.core.Gorm().Save(t).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	if t.Official {
		b.core.RecordAudit(c, action, "template", strconv.FormatUint(uint64(t.ID), 10), t.Title, map[string]any{"kind": t.Kind})
	}
	b.core.OK(c, toDetail(*t))
}

// Create POST /templates（个人模板）或 /admin/templates（站点模板）{kind, title, description?, content? | chapters?}
func (b *behavior) Create(c *gin.Context) {
	var req templateRequest
	if c.ShouldBindJSON(&req) != nil || !validKind(req.Kind) || req.Title == nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u := b.core.CurrentUser(c)
	if !b.checkQuota(c, u) {
		return
	}
	t := b.newTemplate(c, u, req.Kind)
	if req.Kind == KindBook && req.Chapters == nil {
		b.core.Fail(c, http.StatusBadRequest, "书籍模板至少需要一个章节")
		return
	}
	if !b.applyRequest(c, &t, req) {
		return
	}
	b.save(c, &t, "template.create")
}

// CreateFromBook POST /templates/from-book {book_id, title, description?, with_content} 把一本书（自己可编辑的）保存为书籍模板。
func (b *behavior) CreateFromBook(c *gin.Context) {
	var req struct {
		BookID      uint    `json:"book_id"`
		Title       *string `json:"title"`
		Description *string `json:"description"`
		WithContent bool    `json:"with_content"`
	}
	if c.ShouldBindJSON(&req) != nil || req.BookID == 0 {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u := b.core.CurrentUser(c)
	var book models.Book
	if b.core.Gorm().First(&book, req.BookID).Error != nil || !b.core.CanEditBookContent(u, &book) {
		b.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	if !b.checkQuota(c, u) {
		return
	}
	var docs []models.Document
	b.core.Gorm().Where("book_id = ?", book.ID).Order("sort_order ASC, id ASC").Find(&docs)
	nodes := treeFromDocuments(docs, req.WithContent)
	if req.Title == nil {
		req.Title = &book.Title
	}
	t := b.newTemplate(c, u, KindBook)
	if !b.applyRequest(c, &t, templateRequest{Title: req.Title, Description: req.Description, Chapters: &nodes}) {
		return
	}
	b.save(c, &t, "template.create")
}

// Update PUT /templates/:id 或 /admin/templates/:id 修改名称、说明、正文或目录。
func (b *behavior) Update(c *gin.Context) {
	t, ok := b.editable(c)
	if !ok {
		return
	}
	var req templateRequest
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if !b.applyRequest(c, t, req) {
		return
	}
	b.save(c, t, "template.update")
}

// Delete DELETE /templates/:id 或 /admin/templates/:id
func (b *behavior) Delete(c *gin.Context) {
	t, ok := b.editable(c)
	if !ok {
		return
	}
	if err := b.core.Gorm().Delete(t).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "删除失败")
		return
	}
	if t.Official {
		b.core.RecordAudit(c, "template.delete", "template", strconv.FormatUint(uint64(t.ID), 10), t.Title, map[string]any{"kind": t.Kind})
	}
	b.core.OK(c, gin.H{"id": t.ID})
}

// —— 使用 ——

// vars 组装模板变量：tz 为浏览器时区（IANA 名称），日期按使用者所在时区计算。
func (b *behavior) vars(u *models.User, book *models.Book, chapter, tz string) Vars {
	now := time.Now()
	if loc, err := time.LoadLocation(strings.TrimSpace(tz)); err == nil && tz != "" {
		now = now.In(loc)
	}
	v := Vars{Chapter: strings.TrimSpace(chapter), Author: u.PublicName(), Now: now}
	if book != nil {
		v.Book = book.Title
	}
	return v
}

func (b *behavior) countUse(t *Template) {
	b.core.Gorm().Model(&Template{}).Where("id = ?", t.ID).UpdateColumn("use_count", gorm.Expr("use_count + 1"))
}

// editableBook 当前用户可编辑正文的书籍。
func (b *behavior) editableBook(c *gin.Context, u *models.User, id uint) (*models.Book, bool) {
	var book models.Book
	if id == 0 || b.core.Gorm().First(&book, id).Error != nil || !b.core.CanEditBookContent(u, &book) {
		b.core.Fail(c, http.StatusNotFound, "书籍不存在")
		return nil, false
	}
	return &book, true
}

// Render POST /templates/:id/render {book_id?, chapter?, tz?} 取得章节模板替换变量后的正文（写作台插入到光标处）。
func (b *behavior) Render(c *gin.Context) {
	t, ok := b.usable(c)
	if !ok {
		return
	}
	if t.Kind != KindChapter {
		b.core.Fail(c, http.StatusBadRequest, "只有章节模板可以插入正文")
		return
	}
	var req struct {
		BookID  uint   `json:"book_id"`
		Chapter string `json:"chapter"`
		TZ      string `json:"tz"`
	}
	_ = c.ShouldBindJSON(&req)
	u := b.core.CurrentUser(c)
	var book *models.Book
	if req.BookID > 0 {
		if book, ok = b.editableBook(c, u, req.BookID); !ok {
			return
		}
	}
	b.countUse(t)
	b.core.OK(c, gin.H{"content": render(t.Content, b.vars(u, book, req.Chapter, req.TZ))})
}

// Apply POST /templates/:id/apply {book_id, tz?} 按书籍模板在书中生成章节（追加到目录末尾，均为草稿），返回 {created, first_slug}。
func (b *behavior) Apply(c *gin.Context) {
	t, ok := b.usable(c)
	if !ok {
		return
	}
	if t.Kind != KindBook {
		b.core.Fail(c, http.StatusBadRequest, "只有书籍模板可以生成章节")
		return
	}
	var req struct {
		BookID uint   `json:"book_id"`
		TZ     string `json:"tz"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u := b.core.CurrentUser(c)
	book, ok := b.editableBook(c, u, req.BookID)
	if !ok {
		return
	}
	nodes := decodeTree(t.Chapters)
	var maxOrder struct{ N *int }
	b.core.Gorm().Model(&models.Document{}).Select("MAX(sort_order) AS n").Where("book_id = ? AND parent_id IS NULL", book.ID).Scan(&maxOrder)
	start := 0
	if maxOrder.N != nil {
		start = *maxOrder.N + 1
	}
	vars := b.vars(u, book, "", req.TZ)
	created, firstSlug := 0, ""
	var create func(list []Node, parentID *uint, base int) error
	create = func(list []Node, parentID *uint, base int) error {
		for i, n := range list {
			v := vars
			v.Chapter = render(n.Title, vars)
			doc, err := b.createChapter(book, u.ID, v.Chapter, render(n.Content, v), parentID, base+i)
			if err != nil {
				return err
			}
			created++
			if firstSlug == "" {
				firstSlug = doc.Slug
			}
			if err := create(n.Children, &doc.ID, 0); err != nil {
				return err
			}
		}
		return nil
	}
	if err := create(nodes, nil, start); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "生成章节失败："+err.Error())
		return
	}
	b.countUse(t)
	b.core.OK(c, gin.H{"created": created, "first_slug": firstSlug})
}

// createChapter 在书中创建一个草稿章节（含首个历史版本），并通知其他插件章节内容已变化（如双向链接建立索引）。
func (b *behavior) createChapter(book *models.Book, userID uint, title, content string, parentID *uint, order int) (*models.Document, error) {
	allowComments := true
	doc := models.Document{
		BookID: book.ID, UserID: userID, Title: title, Content: content, ParentID: parentID,
		SortOrder: order, Status: "draft", AllowComments: &allowComments,
	}
	doc.Icon = b.core.ExtractDocIcon(content)
	doc.Slug = b.core.UniqueChildSlug(book.ID, parentID, b.core.Slugify(title), 0)
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&doc).Error; err != nil {
			return err
		}
		revision := b.core.NewDocumentRevision(&doc, userID, "create")
		return tx.Create(&revision).Error
	})
	if err != nil {
		return nil, err
	}
	plugincore.FireChapterContentChanged(b.core, book, &doc)
	return &doc, nil
}

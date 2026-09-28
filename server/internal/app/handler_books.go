package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var bookStatuses = map[string]bool{
	"draft": true, "in_progress": true, "published": true, "completed": true, "archived": true,
}

// publiclyReadableBookStatuses 是可对外提供阅读的书籍状态。
// 草稿尚未发布，归档已从公开区域下线；进行中、已发布、已完成均可公开访问。
var publiclyReadableBookStatuses = []string{"in_progress", "published", "completed"}

func isPubliclyReadableBookStatus(status string) bool {
	return status == "in_progress" || status == "published" || status == "completed"
}

var allowedOrderCols = map[string]bool{"created_at": true, "updated_at": true, "title": true, "view_count": true}

// bookSortOrders 列表排序白名单：前端 sort 值 → ORDER BY 子句（限定 books 表列，兼容联表查询）
var bookSortOrders = map[string]string{
	"updated": "books.updated_at DESC",
	"created": "books.created_at DESC",
	"views":   "books.view_count DESC",
	"title":   "books.title ASC",
}

// bookOrder 返回排序子句，未知或为空时按更新时间倒序
func bookOrder(sort string) string {
	if clause, ok := bookSortOrders[sort]; ok {
		return clause
	}
	return "books.updated_at DESC"
}

// decorateBookList 让插件回填列表书籍的非持久化字段（标签插件的标签、内容采集插件的「采集中」标记等）。
func (a *App) decorateBookList(books []models.Book) {
	ptrs := make([]*models.Book, len(books))
	for i := range books {
		ptrs[i] = &books[i]
	}
	plugincore.DecorateBooks(a, ptrs)
}

// attachChapterCounts 一次分组查询回填各书籍的章节（文档）数量，避免 N+1
func (a *App) attachChapterCounts(books []models.Book) {
	if len(books) == 0 {
		return
	}
	ids := make([]uint, len(books))
	for i := range books {
		ids[i] = books[i].ID
	}
	type countRow struct {
		BookID uint
		Cnt    int
	}
	var rows []countRow
	a.DB.Model(&models.Document{}).
		Select("book_id, COUNT(*) as cnt").
		Where("book_id IN ?", ids).
		Group("book_id").Scan(&rows)
	counts := make(map[uint]int, len(rows))
	for _, r := range rows {
		counts[r.BookID] = r.Cnt
	}
	for i := range books {
		books[i].ChapterCount = counts[books[i].ID]
	}
}

// canManageBook 判断用户能否管理书籍（设置与删除：owner/admin）
func (a *App) canManageBook(u *models.User, b *models.Book) bool {
	return IsAdmin(u) || (u != nil && u.ID == b.UserID)
}

// collaboratorRole 查询用户在书籍上的协作者角色；非协作者返回 ("", false)
func (a *App) collaboratorRole(u *models.User, bookID uint) (string, bool) {
	if u == nil {
		return "", false
	}
	var c models.BookCollaborator
	if err := a.DB.Where("book_id = ? AND user_id = ? AND status = ?", bookID, u.ID, "accepted").First(&c).Error; err != nil {
		return "", false
	}
	return c.Role, true
}

// canEditBookContent 内容归属校验：owner/admin 或 editor 协作者。
// 仅覆盖章节内容与目录管理，书籍设置与删除仍限 canManageBook。
func (a *App) canEditBookContent(u *models.User, b *models.Book) bool {
	if a.canManageBook(u, b) {
		return true
	}
	role, ok := a.collaboratorRole(u, b.ID)
	return ok && role == "editor"
}

// canReadBook 判断书籍是否对当前用户可见
func (a *App) canReadBook(u *models.User, b *models.Book) bool {
	// 公开且可读：默认所有人可读；开启「仅登录可读」时未登录游客不可读
	if b.IsPublic && isPubliclyReadableBookStatus(b.Status) && (!b.LoginRequired || u != nil) {
		return true
	}
	if a.canManageBook(u, b) {
		return true
	}
	// 协作者（editor/viewer）可访问私有协作书籍
	_, ok := a.collaboratorRole(u, b.ID)
	return ok
}

func preloadBookUser(db *gorm.DB) *gorm.DB {
	// 标签不再走 GORM Preload（Book.Tags 为 gorm:"-"）；Find 之后由书籍装饰钩子（标签插件）回填。
	return db.
		Preload("User", func(tx *gorm.DB) *gorm.DB {
			// 公开书籍响应只能携带公开资料，禁止通过嵌套 User 泄露邮箱、登录时间和账户状态。
			return tx.Select("id", "username", "nickname", "name_display", "avatar", "bio", "github_url", "role", "created_at")
		})
}

type bookAccess struct {
	CanRead          bool   `json:"can_read"`
	CanManage        bool   `json:"can_manage"`
	CanEditContent   bool   `json:"can_edit_content"`
	CanExport        bool   `json:"can_export"`
	CollaboratorRole string `json:"collaborator_role,omitempty"`
}

// GetBookAccess GET /books/slug/:slug/access 返回由服务端计算的对象级能力，供受保护页面守卫使用。
func (a *App) GetBookAccess(c *gin.Context) {
	var book models.Book
	if err := a.DB.Where("slug = ?", c.Param("slug")).First(&book).Error; err != nil {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canReadBook(u, &book) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	role, _ := a.collaboratorRole(u, book.ID)
	canManage := a.canManageBook(u, &book)
	canEdit := a.canEditBookContent(u, &book)
	ok(c, bookAccess{
		CanRead:          true,
		CanManage:        canManage,
		CanEditContent:   canEdit,
		CanExport:        canEdit,
		CollaboratorRole: role,
	})
}

// ListBooks GET /books
func (a *App) ListBooks(c *gin.Context) {
	page, pageSize := paginate(c)
	u := currentUser(c)
	mine := c.Query("mine") == "true"
	scope := c.Query("scope")

	query := a.DB.Model(&models.Book{})
	if mine || scope == "owned" || scope == "collaborating" {
		if u == nil {
			fail(c, http.StatusUnauthorized, "请先登录")
			return
		}
		if scope == "collaborating" {
			query = query.Joins("JOIN book_collaborators bc ON bc.book_id = books.id").
				Where("bc.user_id = ? AND bc.status = ?", u.ID, "accepted")
		} else {
			query = query.Where("books.user_id = ?", u.ID)
		}
		if s := c.Query("status"); s != "" && bookStatuses[s] {
			query = query.Where("books.status = ?", s)
		}
	} else {
		query = query.Where("is_public = ? AND status IN ?", true, publiclyReadableBookStatuses)
		// 未登录游客看不到「仅登录可读」书籍
		if u == nil {
			query = query.Where("books.login_required = ?", false)
		} else if c.Query("visibility") == "login" {
			// 登录用户可筛选「仅登录可读」书籍
			query = query.Where("books.login_required = ?", true)
		}
	}
	if title := c.Query("title"); title != "" {
		query = query.Where("title LIKE ?", "%"+title+"%")
	}
	if username := c.Query("username"); username != "" {
		query = query.Joins("JOIN users u ON u.id = books.user_id").Where("u.username LIKE ?", "%"+username+"%")
	}
	// 插件提供的筛选条件（如标签插件的 ?tag=slug）
	query = plugincore.ApplyBookFilters(a, c.Request.URL.Query(), query, "books.id")
	// 版本聚合：同一版本组只保留一本（服务端聚合，分页准确）
	var versionCounts map[string]int
	if c.Query("group_versions") == "true" && a.pluginEnabled(pluginBookVersions) {
		query, versionCounts = a.groupBookVersions(query)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	books := []models.Book{}
	if err := preloadBookUser(query).
		Order(bookOrder(c.Query("sort"))).
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&books).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	a.attachChapterCounts(books)
	a.decorateBookList(books)
	a.attachVersionInfo(books, versionCounts, u)
	if scope == "collaborating" {
		for i := range books {
			books[i].CollaboratorRole, _ = a.collaboratorRole(u, books[i].ID)
		}
	}
	ok(c, PageResult{Items: books, Total: total, Page: page, PageSize: pageSize})
}

type bookPayload struct {
	Title                   *string `json:"title"`
	Description             *string `json:"description"`
	CoverImage              *string `json:"cover_image"`
	Slug                    *string `json:"slug"`
	Status                  *string `json:"status"`
	IsPublic                *bool   `json:"is_public"`
	LoginRequired           *bool   `json:"login_required"`
	OrderCol                *string `json:"order_col"`
	OrderDir                *string `json:"order_dir"`
	ChapterPrefix           *string `json:"chapter_prefix"`
	DefaultChapterStatus    *string `json:"default_chapter_status"`
	ChildStatusFollowParent *bool   `json:"child_status_follow_parent"`
	Language                *string `json:"language"`
	TransGroup              *string `json:"trans_group"`
	Version                 *string `json:"version"`
	VersionGroup            *string `json:"version_group"`
	VersionIsLatest         *bool   `json:"version_is_latest"`
	WatermarkEnabled        *bool   `json:"watermark_enabled"`
	WatermarkText           *string `json:"watermark_text"`
	ExportEnabled           *bool   `json:"export_enabled"`
	GuestExportEnabled      *bool   `json:"guest_export_enabled"`
	ExportStyleShared       *bool   `json:"export_style_shared"`
	ExportFormats           *string `json:"export_formats"`
	// ExtraInfo 「更多信息」附加属性（整体替换）
	ExtraInfo *[]models.BookInfoItem `json:"extra_info"`
}

const maxWatermarkLength = 80

func normalizeWatermark(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	return trimmed, utf8.RuneCountInString(trimmed) <= maxWatermarkLength
}

// MyBookCounts GET /books/status-counts 当前用户各状态书籍数量
func (a *App) MyBookCounts(c *gin.Context) {
	u := currentUser(c)
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	query := a.DB.Model(&models.Book{})
	if c.Query("scope") == "collaborating" {
		query = query.Joins("JOIN book_collaborators bc ON bc.book_id = books.id").
			Where("bc.user_id = ? AND bc.status = ?", u.ID, "accepted")
	} else {
		query = query.Where("books.user_id = ?", u.ID)
	}
	query.Select("books.status, COUNT(*) as count").Group("books.status").Scan(&rows)

	counts := gin.H{"": 0, "draft": 0, "in_progress": 0, "published": 0, "completed": 0, "archived": 0}
	total := int64(0)
	for _, r := range rows {
		counts[r.Status] = r.Count
		total += r.Count
	}
	counts[""] = total
	ok(c, counts)
}

// bindBookPayload 解析书籍请求体：核心字段绑定到 req；插件登记的扩展字段（plugincore.RegisterBookField，如 tags）
// 按名收集并在保存前校验（出现且非 null 才收集），任一校验失败按参数错误处理。
func bindBookPayload(c *gin.Context, req *bookPayload) (map[string]json.RawMessage, bool) {
	raw, err := c.GetRawData()
	if err != nil || json.Unmarshal(raw, req) != nil {
		return nil, false
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return nil, false
	}
	ext := map[string]json.RawMessage{}
	for _, f := range plugincore.BookFields() {
		v, present := all[f.Name]
		if !present || string(v) == "null" {
			continue
		}
		if f.Validate != nil && f.Validate(v) != nil {
			return nil, false
		}
		ext[f.Name] = v
	}
	return ext, true
}

// saveBookFields 书籍保存成功后交由插件保存其扩展字段（如标签关联）。
func (a *App) saveBookFields(book *models.Book, ext map[string]json.RawMessage) error {
	for _, f := range plugincore.BookFields() {
		if v, ok := ext[f.Name]; ok && f.Save != nil {
			if err := f.Save(a, book, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateBook POST /books
func (a *App) CreateBook(c *gin.Context) {
	var req bookPayload
	extFields, bound := bindBookPayload(c, &req)
	if !bound {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Title == nil || *req.Title == "" {
		fail(c, http.StatusBadRequest, "请填写书籍标题")
		return
	}
	if a.failBookQuotaFor(c, currentUser(c), req.IsPublic == nil || !*req.IsPublic) {
		return
	}
	if req.Status != nil && !bookStatuses[*req.Status] {
		fail(c, http.StatusBadRequest, "无效的状态")
		return
	}
	u := currentUser(c)

	slug := ""
	if req.Slug != nil && *req.Slug != "" {
		if !validSlug(*req.Slug) {
			fail(c, http.StatusBadRequest, "slug 仅支持小写字母、数字和中划线")
			return
		}
		slug = *req.Slug
	} else {
		slug = slugify(*req.Title)
	}

	book := models.Book{
		Title:         *req.Title,
		UserID:        u.ID,
		Status:        "draft",
		OrderCol:      "created_at",
		OrderDir:      "desc",
		IsPublic:      req.IsPublic != nil && *req.IsPublic,
		LoginRequired: req.LoginRequired != nil && *req.LoginRequired,
	}
	if req.Description != nil {
		book.Description = *req.Description
	}
	if req.CoverImage != nil {
		book.CoverImage = *req.CoverImage
	}
	if req.Status != nil {
		book.Status = *req.Status
	}
	if req.OrderCol != nil && allowedOrderCols[*req.OrderCol] {
		book.OrderCol = *req.OrderCol
	}
	if req.OrderDir != nil && (*req.OrderDir == "asc" || *req.OrderDir == "desc") {
		book.OrderDir = *req.OrderDir
	}
	if req.ChapterPrefix != nil {
		book.ChapterPrefix = *req.ChapterPrefix
	}
	if req.DefaultChapterStatus != nil && docStatuses[*req.DefaultChapterStatus] {
		book.DefaultChapterStatus = *req.DefaultChapterStatus
	}
	if req.ChildStatusFollowParent != nil {
		book.ChildStatusFollowParent = *req.ChildStatusFollowParent
	}
	if req.Language != nil {
		book.Language = truncateText(strings.TrimSpace(*req.Language), 32)
	}
	if req.TransGroup != nil {
		book.TransGroup = truncateText(strings.TrimSpace(*req.TransGroup), 64)
	}
	if req.Version != nil {
		book.Version = truncateText(strings.TrimSpace(*req.Version), 32)
	}
	if req.VersionGroup != nil {
		book.VersionGroup = truncateText(strings.TrimSpace(*req.VersionGroup), 64)
	}
	if req.VersionIsLatest != nil {
		book.VersionIsLatest = *req.VersionIsLatest
	}
	if req.WatermarkText != nil {
		watermarkText, valid := normalizeWatermark(*req.WatermarkText)
		if !valid {
			fail(c, http.StatusBadRequest, "水印内容不能超过 80 个字符")
			return
		}
		book.WatermarkText = watermarkText
	}
	if req.WatermarkEnabled != nil {
		book.WatermarkEnabled = *req.WatermarkEnabled
	}
	if book.WatermarkEnabled && book.WatermarkText == "" {
		fail(c, http.StatusBadRequest, "开启水印后请填写水印内容")
		return
	}
	if req.ExportEnabled != nil {
		book.ExportEnabled = *req.ExportEnabled
	}
	if req.GuestExportEnabled != nil {
		book.GuestExportEnabled = *req.GuestExportEnabled
	}
	if req.ExportStyleShared != nil {
		book.ExportStyleShared = *req.ExportStyleShared
	}
	if req.ExportFormats != nil {
		book.ExportFormats = normalizeExportFormats(*req.ExportFormats)
	}

	for i := 0; i < 50; i++ {
		candidate := slug
		if candidate == "" {
			candidate = randomSlug("book")
		} else if i > 0 {
			candidate = slug + "-" + strconv.Itoa(i+1)
		}
		var count int64
		a.DB.Unscoped().Model(&models.Book{}).Where("slug = ?", candidate).Count(&count)
		if count == 0 {
			book.Slug = candidate
			break
		}
		if slug == "" {
			slug = "" // 随机 slug 冲突时下一轮重新生成
		}
	}
	if book.Slug == "" {
		fail(c, http.StatusConflict, "slug 生成失败，请手动指定")
		return
	}

	// 创建即对外可见时先存为私有，发布守卫审查通过后再公开
	wantVisible := bookVisible(&book)
	if wantVisible {
		book.IsPublic = false
	}
	if err := a.DB.Create(&book).Error; err != nil {
		fail(c, http.StatusInternalServerError, "创建失败: "+err.Error())
		return
	}
	if err := a.saveBookFields(&book, extFields); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if wantVisible {
		if v := plugincore.CheckPublish(a, bookPublishTarget(&book, u.ID)); v.Hold {
			book.PublishHeld = v.Message
		} else {
			book.IsPublic = true
			a.DB.Model(&models.Book{}).Where("id = ?", book.ID).Update("is_public", true)
		}
	}
	a.emitActivity(u.ID, "book.created", "book", strconv.FormatUint(uint64(book.ID), 10), fmt.Sprintf("book.created:%d", book.ID))
	ok(c, book)
}

// GetBook GET /books/:id
func (a *App) GetBook(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canReadBook(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权访问该书籍")
		return
	}
	plugincore.DecorateBooks(a, []*models.Book{book})
	ok(c, book)
}

// GetBookBySlug GET /books/slug/:slug
func (a *App) GetBookBySlug(c *gin.Context) {
	var book models.Book
	if err := preloadBookUser(a.DB).Where("slug = ?", c.Param("slug")).First(&book).Error; err != nil {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	if !a.canReadBook(currentUser(c), &book) {
		fail(c, http.StatusForbidden, "无权访问该书籍")
		return
	}
	plugincore.DecorateBooks(a, []*models.Book{&book})
	ok(c, book)
}

// UpdateBook PUT /books/:id
func (a *App) UpdateBook(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canManageBook(u, book) {
		fail(c, http.StatusForbidden, "无权操作该书籍")
		return
	}
	oldStatus, oldPublic := book.Status, book.IsPublic
	oldTitle, oldDescription := book.Title, book.Description

	var req bookPayload
	extFields, bound := bindBookPayload(c, &req)
	if !bound {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Status != nil && !bookStatuses[*req.Status] {
		fail(c, http.StatusBadRequest, "无效的状态")
		return
	}
	if req.Title != nil && *req.Title != "" {
		book.Title = *req.Title
	}
	if req.Description != nil {
		book.Description = *req.Description
	}
	if req.CoverImage != nil {
		book.CoverImage = *req.CoverImage
	}
	if req.Status != nil {
		book.Status = *req.Status
	}
	if req.IsPublic != nil {
		// 公开改为私有：按书籍所有者的私有书籍数量上限校验
		if oldPublic && !*req.IsPublic {
			var owner models.User
			if a.DB.First(&owner, book.UserID).Error == nil {
				if err := a.ensurePrivateBookQuota(&owner, book.ID); err != nil {
					fail(c, http.StatusForbidden, err.Error())
					return
				}
			}
		}
		book.IsPublic = *req.IsPublic
	}
	if req.LoginRequired != nil {
		book.LoginRequired = *req.LoginRequired
	}
	if req.OrderCol != nil && allowedOrderCols[*req.OrderCol] {
		book.OrderCol = *req.OrderCol
	}
	if req.OrderDir != nil && (*req.OrderDir == "asc" || *req.OrderDir == "desc") {
		book.OrderDir = *req.OrderDir
	}
	if req.ChapterPrefix != nil {
		book.ChapterPrefix = *req.ChapterPrefix
	}
	if req.DefaultChapterStatus != nil && docStatuses[*req.DefaultChapterStatus] {
		book.DefaultChapterStatus = *req.DefaultChapterStatus
	}
	if req.ChildStatusFollowParent != nil {
		book.ChildStatusFollowParent = *req.ChildStatusFollowParent
	}
	if req.Language != nil {
		book.Language = truncateText(strings.TrimSpace(*req.Language), 32)
	}
	if req.TransGroup != nil {
		book.TransGroup = truncateText(strings.TrimSpace(*req.TransGroup), 64)
	}
	if req.Version != nil {
		book.Version = truncateText(strings.TrimSpace(*req.Version), 32)
	}
	if req.VersionGroup != nil {
		book.VersionGroup = truncateText(strings.TrimSpace(*req.VersionGroup), 64)
	}
	if req.VersionIsLatest != nil {
		book.VersionIsLatest = *req.VersionIsLatest
	}
	if req.WatermarkText != nil {
		watermarkText, valid := normalizeWatermark(*req.WatermarkText)
		if !valid {
			fail(c, http.StatusBadRequest, "水印内容不能超过 80 个字符")
			return
		}
		book.WatermarkText = watermarkText
	}
	if req.WatermarkEnabled != nil {
		book.WatermarkEnabled = *req.WatermarkEnabled
	}
	if book.WatermarkEnabled && book.WatermarkText == "" {
		fail(c, http.StatusBadRequest, "开启水印后请填写水印内容")
		return
	}
	if req.ExportEnabled != nil {
		book.ExportEnabled = *req.ExportEnabled
	}
	if req.GuestExportEnabled != nil {
		book.GuestExportEnabled = *req.GuestExportEnabled
	}
	if req.ExportStyleShared != nil {
		book.ExportStyleShared = *req.ExportStyleShared
	}
	if req.ExportFormats != nil {
		book.ExportFormats = normalizeExportFormats(*req.ExportFormats)
	}
	if req.ExtraInfo != nil {
		info, err := normalizeBookInfo(*req.ExtraInfo)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		book.ExtraInfo = info
	}
	if req.Slug != nil && *req.Slug != book.Slug {
		// 访问路径只允许在「可编辑」时修改一次（目前仅复制出的书籍具备该资格）
		if !book.SlugEditable {
			fail(c, http.StatusForbidden, "该书籍的访问路径不可修改")
			return
		}
		if !validSlug(*req.Slug) {
			fail(c, http.StatusBadRequest, "slug 仅支持小写字母、数字和中划线")
			return
		}
		var count int64
		a.DB.Unscoped().Model(&models.Book{}).Where("slug = ? AND id != ?", *req.Slug, book.ID).Count(&count)
		if count > 0 {
			fail(c, http.StatusConflict, "slug 已被占用")
			return
		}
		book.Slug = *req.Slug
		book.SlugEditable = false // 用掉这次修改机会
	}

	// 发布守卫（如内容审核）：书籍即将对外可见、或可见时标题/简介有改动时审查；拦截则保持私有
	wasVisible := oldPublic && isPubliclyReadableBookStatus(oldStatus)
	if bookVisible(book) && (!wasVisible || book.Title != oldTitle || book.Description != oldDescription) {
		if v := plugincore.CheckPublish(a, bookPublishTarget(book, u.ID)); v.Hold {
			book.PublishHeld = v.Message
			book.IsPublic = false
		}
	}
	if err := a.DB.Save(book).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	if err := a.saveBookFields(book, extFields); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if IsAdmin(u) && (oldStatus != book.Status || oldPublic != book.IsPublic) {
		a.recordAudit(c, "book.moderated", "book", auditID(book.ID), book.Title, map[string]any{
			"status":    map[string]any{"from": oldStatus, "to": book.Status},
			"is_public": map[string]any{"from": oldPublic, "to": book.IsPublic},
			"owner_id":  book.UserID,
		})
	}
	a.emitActivity(book.UserID, "book.updated", "book", strconv.FormatUint(uint64(book.ID), 10), fmt.Sprintf("book.updated:%d:%d", book.ID, book.UpdatedAt.UnixNano()))
	ok(c, book)
}

// DeleteBook DELETE /books/:id 将书籍及当前可见章节移入回收站。
func (a *App) DeleteBook(c *gin.Context) {
	if !a.requireStepUp(c, tfOpDelete) {
		return
	}
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canManageBook(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权操作该书籍")
		return
	}
	now := currentTime()
	group := randomSlug("trash")
	u := currentUser(c)
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Document{}).Where("book_id = ?", book.ID).Updates(map[string]any{
			"deleted_at": now, "deleted_by": u.ID, "trash_group": group,
		}).Error; err != nil {
			return err
		}
		return tx.Model(book).Updates(map[string]any{
			"deleted_at": now, "deleted_by": u.ID, "trash_group": group,
		}).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "已移入回收站", "expires_at": now.Add(trashRetention)})
}

// IncrementBookView POST /books/:id/view
func (a *App) IncrementBookView(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canReadBook(currentUser(c), book) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	source := classifyAnalyticsSource(analyticsReferrer(c), c.Request.Host)
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Book{}).
			Where("id = ?", book.ID).
			UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error; err != nil {
			return err
		}
		return recordAnalyticsView(tx, book.ID, 0, source)
	}); err != nil {
		fail(c, http.StatusInternalServerError, "更新浏览量失败")
		return
	}
	var viewCount int
	if err := a.DB.Model(&models.Book{}).
		Where("id = ?", book.ID).
		Pluck("view_count", &viewCount).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询浏览量失败")
		return
	}
	bucket := currentTime().Unix() / 300
	a.emitActivity(book.UserID, "book.viewed", "book", strconv.FormatUint(uint64(book.ID), 10), fmt.Sprintf("book.viewed:%d:%d", book.UserID, bucket))
	ok(c, gin.H{"view_count": viewCount})
}

// findBook 按路径参数 :id 查找书籍
func (a *App) findBook(c *gin.Context) (*models.Book, int) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return nil, http.StatusBadRequest
	}
	var book models.Book
	if err := preloadBookUser(a.DB).First(&book, id).Error; err != nil {
		return nil, http.StatusNotFound
	}
	return &book, http.StatusOK
}

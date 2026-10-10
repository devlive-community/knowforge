package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var docStatuses = map[string]bool{"draft": true, "published": true, "archived": true}

// docIconAllowed 仅保留 FontAwesome 类名允许的字符（字母数字、空格、连字符、下划线）
var docIconAllowed = regexp.MustCompile(`[^a-zA-Z0-9 _-]+`)

// extractDocIcon 从章节开头的元数据（<!-- icon: xxx --> 或 front-matter 的 icon，见 doc_meta.go）提取图标，
// 归一化为安全的图标名（供目录树替换默认图标）。返回小写、去除非法字符、长度不超过 64 的值；未配置或非法时返回空串。
func extractDocIcon(content string) string {
	meta, _ := splitDocMeta(content)
	icon := strings.ToLower(strings.TrimSpace(meta["icon"]))
	icon = strings.TrimSpace(docIconAllowed.ReplaceAllString(icon, ""))
	icon = strings.Join(strings.Fields(icon), " ") // 折叠多余空白
	return truncateText(icon, 64)
}

// ListDocumentTree GET /books/:id/documents 返回文档树（不含正文）
func (a *App) ListDocumentTree(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canReadBook(u, book) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}

	var docs []models.Document
	query := a.DB.Where("book_id = ?", book.ID).
		Select("id", "book_id", "parent_id", "title", "slug", "user_id", "sort_order", "status", "icon", "external_url", "external_new_tab", "created_at", "updated_at")
	if !a.canSuggestBookContent(u, book) {
		query = query.Where("status = ?", "published")
	}
	if err := query.Order("sort_order ASC, created_at ASC").Find(&docs).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	ok(c, buildDocTree(docs, nil))
}

// buildDocTree 将平铺文档列表组装为树
func buildDocTree(docs []models.Document, parent *uint) []*models.Document {
	result := []*models.Document{}
	for i := range docs {
		doc := &docs[i]
		if (doc.ParentID == nil && parent == nil) || (doc.ParentID != nil && parent != nil && *doc.ParentID == *parent) {
			doc.Children = buildDocTree(docs, &doc.ID)
			result = append(result, doc)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].SortOrder != result[j].SortOrder {
			return result[i].SortOrder < result[j].SortOrder
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

type documentPayload struct {
	Title          *string         `json:"title"`
	Slug           *string         `json:"slug"`
	Content        *string         `json:"content"`
	ExternalURL    *string         `json:"external_url"`     // 非空=外链章节（跳转外部地址，不渲染正文）
	ExternalNewTab *bool           `json:"external_new_tab"` // 外链打开方式：true=新窗口（默认）| false=当前窗口
	ParentID       json.RawMessage `json:"parent_id"`
	SortOrder      *int            `json:"sort_order"`
	Status         *string         `json:"status"`
	CascadeStatus  *bool           `json:"cascade_status"` // 改状态时是否同步应用到所有子章节
	AllowComments  *bool           `json:"allow_comments"`
	CreateRevision *bool           `json:"create_revision"`
	RevisionReason *string         `json:"revision_reason"`
	// BaseHash 写作台打开章节时的 content_hash；传入时若期间他人已保存过标题或正文，返回 409（DOC_CONFLICT）与最新内容
	BaseHash *string `json:"base_hash"`
}

// validExternalURL 校验外链章节地址：仅允许 http/https 绝对地址。
func validExternalURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// initialChapterStatus 未显式指定状态时新章节的初始状态（与 CreateDocument 规则一致，供采集/导入复用）：
//   - 有父章节且书籍开启「子章节状态跟随父章节」→ 沿用父章节状态；
//   - 第一级章节 → 书籍配置的「章节默认状态」；
//   - 其余 → draft。
func (a *App) initialChapterStatus(book *models.Book, parentID *uint) string {
	if parentID != nil {
		if book.ChildStatusFollowParent {
			var parent models.Document
			if a.DB.Select("status").Where("id = ? AND book_id = ?", *parentID, book.ID).First(&parent).Error == nil && docStatuses[parent.Status] {
				return parent.Status
			}
		}
		return "draft"
	}
	if docStatuses[book.DefaultChapterStatus] {
		return book.DefaultChapterStatus
	}
	return "draft"
}

// uniqueChildSlug 为书内文档生成唯一 slug：
//   - 先用 base；书内不冲突就直接用；
//   - 冲突时逐级用祖先章节的 slug 作前缀（如 A/c 与 B/c → 后者变成 b-c，再冲突则 a-b-c…）；
//   - 一直到根仍冲突，退回随机后缀（c-<随机 hex>）。
//
// excludeID 用于「更新自身」时排除本条；base 为空按随机生成。
func (a *App) uniqueChildSlug(bookID uint, parentID *uint, base string, excludeID uint) string {
	base = strings.Trim(base, "-")
	taken := func(s string) bool {
		if s == "" {
			return true
		}
		var c int64
		q := a.DB.Unscoped().Model(&models.Document{}).Where("book_id = ? AND slug = ?", bookID, s)
		if excludeID != 0 {
			q = q.Where("id <> ?", excludeID)
		}
		q.Count(&c)
		return c > 0
	}
	if base == "" {
		return randomSlug("doc")
	}
	if !taken(base) {
		return base
	}
	// 逐级向上，用祖先 slug 作前缀
	prefix := ""
	for depth, pid := 0, parentID; depth < 20 && pid != nil; depth++ {
		var parent models.Document
		if err := a.DB.Select("id", "parent_id", "slug").First(&parent, *pid).Error; err != nil {
			break
		}
		prefix = parent.Slug + "-" + prefix
		if cand := strings.Trim(prefix, "-") + "-" + base; !taken(cand) {
			return cand
		}
		pid = parent.ParentID
	}
	// 祖先前缀仍冲突：随机后缀兜底
	for i := 0; i < 50; i++ {
		if cand := randomSlug(base); !taken(cand) {
			return cand
		}
	}
	return randomSlug(base)
}

// parseParentID 解析 parent_id 三态：缺省(present=false)不改动；显式 null 表示置为顶级；数字表示挂到该父级
func parseParentID(raw json.RawMessage) (present bool, id *uint, err error) {
	if len(raw) == 0 {
		return false, nil, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return true, nil, nil
	}
	var v uint
	if err := json.Unmarshal(raw, &v); err != nil {
		return true, nil, err
	}
	return true, &v, nil
}

// CreateDocument POST /books/:id/documents
func (a *App) CreateDocument(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canEditBookContent(u, book) {
		fail(c, http.StatusForbidden, "无权操作该书籍")
		return
	}

	var req documentPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Title == nil || *req.Title == "" {
		fail(c, http.StatusBadRequest, "请填写文档标题")
		return
	}
	statusStr := "draft"
	if req.Status != nil && docStatuses[*req.Status] {
		statusStr = *req.Status
	}
	_, createParentID, perr := parseParentID(req.ParentID)
	if perr != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if createParentID != nil {
		var parent models.Document
		if err := a.DB.Where("id = ? AND book_id = ?", *createParentID, book.ID).First(&parent).Error; err != nil {
			fail(c, http.StatusBadRequest, "父文档不存在")
			return
		}
		// 书籍开启「子章节状态跟随父章节」且未显式指定状态时，默认沿用父章节状态
		if book.ChildStatusFollowParent && req.Status == nil && docStatuses[parent.Status] {
			statusStr = parent.Status
		}
	} else if req.Status == nil && book.DefaultChapterStatus != "" && docStatuses[book.DefaultChapterStatus] {
		// 第一级章节（无父级）未显式指定状态时，采用书籍配置的「章节默认状态」。
		statusStr = book.DefaultChapterStatus
	}

	// 请求直接发布时先以草稿写入，发布守卫审查通过后再发布（内容不会在审查前短暂可见）
	wantPublish := statusStr == "published"
	if wantPublish {
		statusStr = "draft"
	}
	doc := models.Document{
		BookID:  book.ID,
		Title:   *req.Title,
		UserID:  u.ID,
		Status:  statusStr,
		Content: "",
	}
	defaultOn := true
	doc.AllowComments = &defaultOn
	if req.AllowComments != nil {
		doc.AllowComments = req.AllowComments
	}
	if createParentID != nil {
		doc.ParentID = createParentID
	}
	if req.Content != nil {
		doc.Content = *req.Content
	}
	if req.ExternalURL != nil {
		ext := strings.TrimSpace(*req.ExternalURL)
		if ext != "" && !validExternalURL(ext) {
			fail(c, http.StatusBadRequest, "外链地址需以 http:// 或 https:// 开头")
			return
		}
		doc.ExternalURL = ext
	}
	if req.ExternalNewTab != nil {
		doc.ExternalNewTab = req.ExternalNewTab
	}
	doc.Icon = extractDocIcon(doc.Content)
	if req.SortOrder != nil {
		doc.SortOrder = *req.SortOrder
	}
	if req.AllowComments != nil {
		doc.AllowComments = req.AllowComments
	}
	// 外链章节没有正文页面，强制关闭评论（公开后也不展示评论区）。
	if doc.ExternalURL != "" {
		off := false
		doc.AllowComments = &off
	}

	base := ""
	if req.Slug != nil && *req.Slug != "" {
		if !validSlug(*req.Slug) {
			fail(c, http.StatusBadRequest, "slug 仅支持小写字母、数字和中划线")
			return
		}
		base = *req.Slug
	} else {
		base = slugify(*req.Title)
	}
	// 冲突时递归用祖先章节 slug 作前缀（A/c 与 B/c → b-c），最终随机兜底。
	doc.Slug = a.uniqueChildSlug(book.ID, doc.ParentID, base, 0)

	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&doc).Error; err != nil {
			return err
		}
		revision := newDocumentRevision(&doc, u.ID, "create")
		return tx.Create(&revision).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, "创建失败: "+err.Error())
		return
	}
	if wantPublish {
		doc.PublishHeld = a.tryPublishDocument(book, &doc, u.ID)
		// 创建即发布同样是章节首次发布（被审核拦截的在审核通过时再触发）
		if doc.PublishHeld == "" {
			plugincore.FireChapterPublished(a, book, &doc)
		}
	}
	a.notifyDocumentIndexableChange(nil, book, nil, &doc)
	a.emitActivity(u.ID, "document.created", "document", strconv.FormatUint(uint64(doc.ID), 10), fmt.Sprintf("document.created:%d", doc.ID))
	a.publishTreeChanged(c, book.ID)
	a.recordActivity(book.ID, doc.ID, u.ID, "doc.created", map[string]any{"title": doc.Title})
	ok(c, withContentHash(&doc))
}

// findDocument 查找文档及其所属书籍
func (a *App) findDocument(c *gin.Context) (*models.Document, *models.Book, int) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return nil, nil, http.StatusBadRequest
	}
	var doc models.Document
	if err := a.DB.First(&doc, id).Error; err != nil {
		return nil, nil, http.StatusNotFound
	}
	var book models.Book
	if err := a.DB.First(&book, doc.BookID).Error; err != nil {
		return nil, nil, http.StatusNotFound
	}
	return &doc, &book, http.StatusOK
}

// canReadDocument 判断文档是否对当前用户可见
func (a *App) canReadDocument(u *models.User, doc *models.Document, book *models.Book) bool {
	if a.canSuggestBookContent(u, book) {
		return true
	}
	// viewer 协作者：私有书籍中可见已发布章节
	if role, ok := a.collaboratorRole(u, book.ID); ok && role == "viewer" {
		return doc.Status == "published"
	}
	// 公开已发布章节：默认所有人可读；书籍开启「仅登录可读」时未登录游客不可读
	return book.IsPublic && isPubliclyReadableBookStatus(book.Status) && doc.Status == "published" && (!book.LoginRequired || u != nil)
}

// GetDocument GET /documents/:id
func (a *App) GetDocument(c *gin.Context) {
	doc, book, status := a.findDocument(c)
	if doc == nil {
		fail(c, status, "文档不存在")
		return
	}
	if !a.canReadDocument(currentUser(c), doc, book) {
		fail(c, http.StatusForbidden, "无权访问该文档")
		return
	}
	a.applyContentGate(currentUser(c), book, doc)
	ok(c, withContentHash(doc))
}

// IncrementDocumentView POST /documents/:id/view 章节浏览 +1，并同步累加所属书籍的总浏览数
func (a *App) IncrementDocumentView(c *gin.Context) {
	doc, book, status := a.findDocument(c)
	if doc == nil {
		fail(c, status, "文档不存在")
		return
	}
	if !a.canReadDocument(currentUser(c), doc, book) {
		fail(c, http.StatusNotFound, "文档不存在")
		return
	}
	source := classifyAnalyticsSource(analyticsReferrer(c), c.Request.Host)
	var viewCount int
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Document{}).
			Where("id = ?", doc.ID).
			UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Book{}).
			Where("id = ?", book.ID).
			UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error; err != nil {
			return err
		}
		if err := recordAnalyticsView(tx, book.ID, doc.ID, source); err != nil {
			return err
		}
		return tx.Model(&models.Document{}).
			Where("id = ?", doc.ID).
			Pluck("view_count", &viewCount).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, "更新浏览量失败")
		return
	}
	bucket := currentTime().Unix() / 300
	a.emitActivity(book.UserID, "book.viewed", "book", strconv.FormatUint(uint64(book.ID), 10), fmt.Sprintf("book.viewed:%d:%d", book.UserID, bucket))
	ok(c, gin.H{"view_count": viewCount})
}

// GetDocumentBySlug GET /books/:id/documents/slug/:slug
func (a *App) GetDocumentBySlug(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	var doc models.Document
	if err := a.DB.Where("book_id = ? AND slug = ?", book.ID, c.Param("slug")).First(&doc).Error; err != nil {
		fail(c, http.StatusNotFound, "文档不存在")
		return
	}
	if !a.canReadDocument(currentUser(c), &doc, book) {
		fail(c, http.StatusForbidden, "无权访问该文档")
		return
	}
	a.notifyDocumentVisited(book, &doc)
	a.applyContentGate(currentUser(c), book, &doc)
	ok(c, withContentHash(&doc))
}

// UpdateDocument PUT /documents/:id
func (a *App) UpdateDocument(c *gin.Context) {
	doc, book, status := a.findDocument(c)
	if doc == nil {
		fail(c, status, "文档不存在")
		return
	}
	if !a.canEditBookContent(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权操作该文档")
		return
	}
	oldBook := *book
	oldDoc := *doc
	oldStatus := doc.Status // 用于判定「首次发布」以通知关注者
	oldTitle, oldContent := doc.Title, doc.Content
	oldParent, oldSort, oldSlug := parentKey(doc.ParentID), doc.SortOrder, doc.Slug // 目录是否变化（通知其他写作台刷新）

	var req documentPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Title != nil && *req.Title != "" {
		doc.Title = *req.Title
	}
	if req.Content != nil {
		doc.Content = *req.Content
		doc.Icon = extractDocIcon(doc.Content)
	}
	if req.ExternalURL != nil {
		ext := strings.TrimSpace(*req.ExternalURL)
		if ext != "" && !validExternalURL(ext) {
			fail(c, http.StatusBadRequest, "外链地址需以 http:// 或 https:// 开头")
			return
		}
		doc.ExternalURL = ext
	}
	if req.ExternalNewTab != nil {
		doc.ExternalNewTab = req.ExternalNewTab
	}
	if req.SortOrder != nil {
		doc.SortOrder = *req.SortOrder
	}
	if req.AllowComments != nil {
		doc.AllowComments = req.AllowComments
	}
	// 外链章节没有正文页面，强制关闭评论（公开后也不展示评论区）。
	if doc.ExternalURL != "" {
		off := false
		doc.AllowComments = &off
	}
	cascadeStatus := ""
	publishedChapter := false
	if req.Status != nil && docStatuses[*req.Status] {
		doc.Status = *req.Status
		if *req.Status == "published" {
			publishedChapter = true
		}
		if req.CascadeStatus != nil && *req.CascadeStatus {
			cascadeStatus = *req.Status
		}
	}
	if req.Slug != nil && *req.Slug != doc.Slug {
		if !validSlug(*req.Slug) {
			fail(c, http.StatusBadRequest, "slug 仅支持小写字母、数字和中划线")
			return
		}
		var count int64
		a.DB.Unscoped().Model(&models.Document{}).Where("book_id = ? AND slug = ? AND id != ?", book.ID, *req.Slug, doc.ID).Count(&count)
		if count > 0 {
			fail(c, http.StatusConflict, "slug 已被占用")
			return
		}
		doc.Slug = *req.Slug
	}
	present, newParentID, perr := parseParentID(req.ParentID)
	if perr != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if present {
		if newParentID == nil {
			doc.ParentID = nil // 移动为顶级章节
		} else if doc.ParentID == nil || *newParentID != *doc.ParentID {
			if *newParentID == doc.ID {
				fail(c, http.StatusBadRequest, "父文档不能是自身")
				return
			}
			var parent models.Document
			if err := a.DB.Where("id = ? AND book_id = ?", *newParentID, book.ID).First(&parent).Error; err != nil {
				fail(c, http.StatusBadRequest, "父文档不存在")
				return
			}
			// 检查是否会把文档移动到自己的后代下
			if isDescendant(a.DB, doc.ID, *newParentID) {
				fail(c, http.StatusBadRequest, "不能将文档移动到自己的子文档下")
				return
			}
			doc.ParentID = newParentID
		}
	}

	// 发布守卫（如内容审核）：即将发布、或已发布章节的标题/正文有改动时审查；拦截则保持未发布（事务外调用）
	if doc.Status == "published" && (oldStatus != "published" || doc.Title != oldTitle || doc.Content != oldContent) {
		if v := plugincore.CheckPublish(a, documentPublishTarget(book, doc, currentUser(c).ID)); v.Hold {
			doc.PublishHeld = v.Message
			doc.Status = oldStatus
			if oldStatus == "published" {
				doc.Status = "draft"
			}
			publishedChapter = false
			if cascadeStatus == "published" {
				cascadeStatus = ""
			}
		}
	}
	var cascaded []uint // 级联发布的后代章节，事务后逐个审查
	var cascadeBefore []models.Document
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		// 冲突保护：在事务中锁定并重读当前内容（SQLite 的写事务本身已串行），与写作台打开时的摘要比对
		if req.BaseHash != nil && *req.BaseHash != "" && (req.Title != nil || req.Content != nil) {
			var fresh models.Document
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "title", "content").First(&fresh, doc.ID).Error; err != nil {
				return err
			}
			if documentHash(fresh.Title, fresh.Content) != *req.BaseHash {
				return errDocConflict
			}
		}
		if err := tx.Save(doc).Error; err != nil {
			return err
		}
		// 级联：把该章节整棵子树的状态一并更新
		if cascadeStatus != "" {
			descendants := subtreeDocIDs(tx, doc.ID)
			if len(descendants) > 0 {
				if err := tx.Where("id IN ?", descendants).Find(&cascadeBefore).Error; err != nil {
					return err
				}
			}
			if cascadeStatus == "published" {
				tx.Model(&models.Document{}).Where("id IN ? AND status <> ?", descendants, "published").Pluck("id", &cascaded)
			}
			if len(descendants) > 0 {
				if err := tx.Model(&models.Document{}).Where("id IN ?", descendants).Update("status", cascadeStatus).Error; err != nil {
					return err
				}
			}
		}
		// 发布章节时联动提升书籍状态：草稿书一旦有章节被发布，说明写作已推进，
		// 将书籍从 draft 提升为 in_progress，避免“章节已发布但书籍仍是草稿”导致公开区不可见。
		// 只升不降：in_progress/published/completed/archived 均保持不变，尊重作者在书籍设置中的选择。
		if publishedChapter && book.Status == "draft" {
			if err := tx.Model(&models.Book{}).Where("id = ?", book.ID).Update("status", "in_progress").Error; err != nil {
				return err
			}
			book.Status = "in_progress"
		}
		if req.CreateRevision != nil && *req.CreateRevision {
			reason := "save"
			if req.RevisionReason != nil && (*req.RevisionReason == "save" || *req.RevisionReason == "publish") {
				reason = *req.RevisionReason
			}
			revision := newDocumentRevision(doc, currentUser(c).ID, reason)
			return tx.Create(&revision).Error
		}
		return nil
	}); err != nil {
		if errors.Is(err, errDocConflict) {
			a.respondDocConflict(c, doc.ID)
			return
		}
		fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	a.emitActivity(doc.UserID, "document.updated", "document", strconv.FormatUint(uint64(doc.ID), 10), fmt.Sprintf("document.updated:%d:%d", doc.ID, doc.UpdatedAt.UnixNano()))
	for _, id := range cascaded {
		var child models.Document
		if a.DB.First(&child, id).Error == nil {
			a.GuardDocumentPublish(book, &child, currentUser(c).ID)
		}
	}
	// 章节首次发布（草稿→已发布）：由插件订阅（书籍关注通知关注者、成长等级给作者发经验）
	if publishedChapter && oldStatus != "published" {
		plugincore.FireChapterPublished(a, book, doc)
	}
	bookVisibilityChanged := indexableBook(&oldBook) != indexableBook(book)
	a.notifyDocumentIndexableChange(&oldBook, book, &oldDoc, doc)
	if !bookVisibilityChanged {
		for _, before := range cascadeBefore {
			var after models.Document
			if a.DB.First(&after, before.ID).Error == nil {
				a.notifyDocumentIndexableChange(&oldBook, book, &before, &after)
			}
		}
	}
	// 标题或正文有修改：由插件订阅（如章节导读在内容变化后更新），并通知同书的其他写作台
	if doc.Title != oldTitle || doc.Content != oldContent {
		plugincore.FireChapterContentChanged(a, book, doc)
		a.publishDocSaved(c, book.ID, doc)
		added, removed := lineChanges(oldContent, doc.Content)
		detail := map[string]any{"title": doc.Title, "added": added, "removed": removed}
		if doc.Title != oldTitle {
			detail["old_title"] = oldTitle
		}
		a.recordActivity(book.ID, doc.ID, currentUser(c).ID, "doc.saved", detail)
	}
	if doc.Title != oldTitle || parentKey(doc.ParentID) != oldParent || doc.SortOrder != oldSort || doc.Slug != oldSlug || doc.Status != oldStatus {
		a.publishTreeChanged(c, book.ID)
	}
	ok(c, withContentHash(doc))
}

// subtreeDocIDs 收集 rootID 的全部后代章节 id（不含 root 本身），用于状态级联。
func subtreeDocIDs(db *gorm.DB, rootID uint) []uint {
	var all []uint
	frontier := []uint{rootID}
	for len(frontier) > 0 {
		var children []uint
		db.Model(&models.Document{}).Where("parent_id IN ?", frontier).Pluck("id", &children)
		if len(children) == 0 {
			break
		}
		all = append(all, children...)
		frontier = children
	}
	return all
}

// isDescendant 判断 candidateId 是否位于 rootId 的子树中
func isDescendant(db *gorm.DB, rootID, candidateID uint) bool {
	frontier := []uint{rootID}
	for len(frontier) > 0 {
		var next []uint
		for _, pid := range frontier {
			var children []uint
			db.Model(&models.Document{}).Where("parent_id = ?", pid).Pluck("id", &children)
			for _, cid := range children {
				if cid == candidateID {
					return true
				}
				next = append(next, cid)
			}
		}
		frontier = next
	}
	return false
}

// DeleteDocument DELETE /documents/:id 将章节子树作为同一批次移入回收站。
func (a *App) DeleteDocument(c *gin.Context) {
	if !a.requireStepUp(c, tfOpDelete) {
		return
	}
	doc, book, status := a.findDocument(c)
	if doc == nil {
		fail(c, status, "文档不存在")
		return
	}
	if !a.canEditBookContent(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权操作该文档")
		return
	}

	count, expires, err := a.trashDocumentSubtree(currentUser(c), doc)
	if err != nil {
		fail(c, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	a.publishTreeChanged(c, doc.BookID)
	a.recordActivity(doc.BookID, doc.ID, currentUser(c).ID, "doc.deleted", map[string]any{"title": doc.Title, "count": count})
	ok(c, gin.H{"message": "已移入回收站", "count": count, "expires_at": expires})
}

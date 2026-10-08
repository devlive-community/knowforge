package app

import (
	"net/http"
	"strings"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
)

type copyBookRequest struct {
	Title  string `json:"title"`
	Slug   string `json:"slug"`    // 可选：显式设置副本访问路径；设置后不可再改，留空则自动生成且允许改一次
	Mode   string `json:"mode"`    // full（整本）| custom（自选章节）| metadata（仅元数据，不含章节）
	DocIDs []uint `json:"doc_ids"` // custom 模式：有序、要复制的原章节 id（可拖拽重排 / 移除）
}

// CopyBook POST /books/:id/copy 复制书籍元数据 + 选定章节到当前用户名下的新草稿书。
// full：按原结构与顺序复制全部章节；custom：仅复制 doc_ids 指定的章节，顺序即为其在数组中的顺序。
func (a *App) CopyBook(c *gin.Context) {
	src, status := a.findBook(c)
	if src == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canReadBook(u, src) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	var req copyBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if a.failBookQuota(c, u) {
		return
	}

	// 源章节（未删除），按原顺序
	var srcDocs []models.Document
	a.DB.Where("book_id = ?", src.ID).Order("sort_order ASC, id ASC").Find(&srcDocs)
	byID := make(map[uint]*models.Document, len(srcDocs))
	for i := range srcDocs {
		byID[srcDocs[i].ID] = &srcDocs[i]
	}

	// 选定要复制的章节（有序）与选中集合（用于父子重映射）
	chosen := make([]*models.Document, 0, len(srcDocs))
	selected := map[uint]bool{}
	if req.Mode == "custom" {
		for _, id := range req.DocIDs {
			if d, ok := byID[id]; ok && !selected[id] {
				chosen = append(chosen, d)
				selected[id] = true
			}
		}
	} else if req.Mode == "metadata" {
		// 仅复制元数据，不含任何章节：chosen 保持为空
	} else {
		for i := range srcDocs {
			chosen = append(chosen, &srcDocs[i])
			selected[srcDocs[i].ID] = true
		}
	}

	// 元数据（副本始终为私有草稿；其余配置尽量完整复制，含语言/版本/翻译分组、导出与水印设置）
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = src.Title + " 副本"
	}
	title = truncateText(title, 255)
	// 访问路径：显式设置则校验唯一并锁定（SlugEditable=false）；留空则自动生成且允许改一次
	slug := strings.TrimSpace(req.Slug)
	slugEditable := true
	if slug != "" {
		if !validSlug(slug) {
			fail(c, http.StatusBadRequest, "访问路径仅支持小写字母、数字与中划线")
			return
		}
		var taken int64
		a.DB.Model(&models.Book{}).Where("slug = ?", slug).Count(&taken)
		if taken > 0 {
			fail(c, http.StatusConflict, "访问路径已被占用")
			return
		}
		slugEditable = false
	} else {
		slug = a.uniqueBookSlug(slugify(title))
		if slug == "" {
			slug = a.uniqueBookSlug("book")
		}
	}
	newBook, err := a.copyBookShell(u, src, title, slug, slugEditable)
	if err != nil {
		fail(c, http.StatusInternalServerError, "复制失败: "+err.Error())
		return
	}

	// 章节：第一遍建档（记录 原 id -> 新 id），第二遍重建父子关系（父章节须同在选中集合内）
	idMap := make(map[uint]uint, len(chosen))
	firstDocSlug := ""
	for idx, d := range chosen {
		base := d.Slug
		if base == "" {
			base = slugify(d.Title)
		}
		nd := models.Document{
			BookID: newBook.ID, UserID: u.ID, Title: d.Title,
			Slug: a.uniqueDocSlug(newBook.ID, base), Content: d.Content,
			Status: d.Status, SortOrder: idx, AllowComments: d.AllowComments,
			Icon: d.Icon, ExternalURL: d.ExternalURL, ExternalNewTab: d.ExternalNewTab,
		}
		if err := a.DB.Create(&nd).Error; err != nil {
			fail(c, http.StatusInternalServerError, "复制章节失败: "+err.Error())
			return
		}
		if idx == 0 {
			firstDocSlug = nd.Slug // 首个章节，供前端复制后直接进入写作台该章节
		}
		idMap[d.ID] = nd.ID
	}
	for _, d := range chosen {
		if d.ParentID != nil && selected[*d.ParentID] {
			a.DB.Model(&models.Document{}).Where("id = ?", idMap[d.ID]).Update("parent_id", idMap[*d.ParentID])
		}
	}

	ok(c, gin.H{"book": newBook, "copied_documents": len(chosen), "first_doc_slug": firstDocSlug})
}

// copyBookShell 以 src 为模板为 u 新建一本私有草稿书（不含章节）：尽量完整复制配置（含语言/版本/翻译分组、导出与水印设置、
// 每本书的导出样式），并触发插件的复制钩子（如标签）。
func (a *App) copyBookShell(u *models.User, src *models.Book, title, slug string, slugEditable bool) (models.Book, error) {
	newBook := models.Book{
		Title: title, UserID: u.ID, Status: "draft", IsPublic: false, Slug: slug,
		SlugEditable: slugEditable,
		Description:  src.Description, CoverImage: src.CoverImage,
		LoginRequired: src.LoginRequired,
		OrderCol:      src.OrderCol, OrderDir: src.OrderDir, ChapterPrefix: src.ChapterPrefix,
		ChildStatusFollowParent: src.ChildStatusFollowParent,
		Language:                src.Language, TransGroup: src.TransGroup,
		Version: src.Version, VersionGroup: src.VersionGroup,
		WatermarkEnabled: src.WatermarkEnabled, WatermarkText: src.WatermarkText, ExtraInfo: append(models.BookInfo{}, src.ExtraInfo...),
		ExportEnabled: src.ExportEnabled, GuestExportEnabled: src.GuestExportEnabled,
		ExportStyleShared: src.ExportStyleShared, ExportFormats: src.ExportFormats,
	}
	if err := a.DB.Create(&newBook).Error; err != nil {
		return newBook, err
	}
	// 插件复制自身关联数据（如标签插件复制标签）
	plugincore.FireBookCopied(a, src, &newBook)
	// 复制每本书的导出样式设置（PageSize/字号/页边距/页脚等）
	var srcExport models.BookExportSetting
	if a.DB.Where("book_id = ?", src.ID).First(&srcExport).Error == nil {
		copyExport := srcExport
		copyExport.ID = 0
		copyExport.BookID = newBook.ID
		copyExport.CreatedAt = time.Time{}
		copyExport.UpdatedAt = time.Time{}
		_ = a.DB.Create(&copyExport).Error
	}
	return newBook, nil
}

type copyDocumentsRequest struct {
	TargetBookID uint   `json:"target_book_id"`
	DocIDs       []uint `json:"doc_ids"`
	Move         bool   `json:"move"` // true=移动（复制后从源书删除选中章节，需源书写权限）
}

// CopyDocuments POST /books/:id/documents/copy 将选定章节（含各自子章节树）复制到目标书籍，保持目录结构。
// 选中父章节即连同其子树一并复制；顶层（父不在复制集合内）追加到目标书籍目录末尾。
func (a *App) CopyDocuments(c *gin.Context) {
	src, status := a.findBook(c)
	if src == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canReadBook(u, src) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	var req copyDocumentsRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.DocIDs) == 0 || req.TargetBookID == 0 {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	// 移动需要源书写权限（复制只需可读）
	if req.Move && !a.canEditBookContent(u, src) {
		fail(c, http.StatusForbidden, "无权从源书籍移动章节")
		return
	}
	var target models.Book
	if err := a.DB.First(&target, req.TargetBookID).Error; err != nil {
		fail(c, http.StatusNotFound, "目标书籍不存在")
		return
	}
	if !a.canEditBookContent(u, &target) {
		fail(c, http.StatusForbidden, "无权写入目标书籍")
		return
	}

	// 源书全部章节，建父->有序子节点映射
	var srcDocs []models.Document
	a.DB.Where("book_id = ?", src.ID).Order("sort_order ASC, id ASC").Find(&srcDocs)
	byID := make(map[uint]*models.Document, len(srcDocs))
	children := make(map[uint][]*models.Document)
	for i := range srcDocs {
		d := &srcDocs[i]
		byID[d.ID] = d
		pid := uint(0)
		if d.ParentID != nil {
			pid = *d.ParentID
		}
		children[pid] = append(children[pid], d)
	}

	// 复制集合 = 选中节点 + 各自子树（仅计算成员，顺序稍后按源文档顺序确定，不受选择顺序影响）
	copySet := map[uint]bool{}
	var mark func(d *models.Document)
	mark = func(d *models.Document) {
		if copySet[d.ID] {
			return
		}
		copySet[d.ID] = true
		for _, ch := range children[d.ID] {
			mark(ch)
		}
	}
	for _, id := range req.DocIDs {
		if d, ok := byID[id]; ok {
			mark(d)
		}
	}
	if len(copySet) == 0 {
		fail(c, http.StatusBadRequest, "没有可复制的章节")
		return
	}

	// 目标书顶层现有数量，作为追加起始 sort_order
	var topCount int64
	a.DB.Model(&models.Document{}).Where("book_id = ? AND parent_id IS NULL", target.ID).Count(&topCount)
	nextTop := int(topCount)

	// 按「源文档顺序」(sort_order,id) 遍历建档，保证复制后顺序与原书一致；完整复制内容与各项配置
	// （含图标、外链、评论开关、状态），做到与原章节一致。第二遍重建父子。
	idMap := make(map[uint]uint, len(copySet))
	createdDocs := make([]models.Document, 0, len(copySet))
	copied := 0
	for i := range srcDocs {
		d := &srcDocs[i]
		if !copySet[d.ID] {
			continue
		}
		base := d.Slug
		if base == "" {
			base = slugify(d.Title)
		}
		nd := models.Document{
			BookID: target.ID, UserID: u.ID, Title: d.Title,
			Slug: a.uniqueDocSlug(target.ID, base), Content: d.Content,
			Status: d.Status, AllowComments: d.AllowComments,
			Icon: d.Icon, ExternalURL: d.ExternalURL, ExternalNewTab: d.ExternalNewTab,
		}
		if d.ParentID == nil || !copySet[*d.ParentID] {
			nd.SortOrder = nextTop
			nextTop++
		} else {
			nd.SortOrder = d.SortOrder
		}
		if err := a.DB.Create(&nd).Error; err != nil {
			fail(c, http.StatusInternalServerError, "复制章节失败: "+err.Error())
			return
		}
		idMap[d.ID] = nd.ID
		createdDocs = append(createdDocs, nd)
		copied++
	}
	for i := range srcDocs {
		d := &srcDocs[i]
		if !copySet[d.ID] {
			continue
		}
		if d.ParentID != nil && copySet[*d.ParentID] {
			a.DB.Model(&models.Document{}).Where("id = ?", idMap[d.ID]).Update("parent_id", idMap[*d.ParentID])
		}
	}
	for i := range createdDocs {
		a.notifyDocumentIndexableChange(nil, &target, nil, &createdDocs[i])
	}

	// 移动：复制成功后从源书删除选中的章节（含子树）
	moved := false
	if req.Move && copied > 0 {
		ids := make([]uint, 0, len(copySet))
		movedURLs := make([]string, 0, len(copySet))
		for _, d := range srcDocs {
			if !copySet[d.ID] {
				continue
			}
			ids = append(ids, d.ID)
			if address := a.indexableDocumentURL(src, &d); address != "" {
				movedURLs = append(movedURLs, address)
			}
		}
		if err := a.DB.Where("book_id = ? AND id IN ?", src.ID, ids).Delete(&models.Document{}).Error; err != nil {
			fail(c, http.StatusInternalServerError, "移动时删除源章节失败: "+err.Error())
			return
		}
		a.emitIndexableURLs(movedURLs...)
		moved = true
	}

	action := "document.copied"
	label := "复制章节到书籍"
	if moved {
		action = "document.moved"
		label = "移动章节到书籍"
	}
	a.recordAudit(c, action, "book", strings.TrimSpace(target.Slug), label, map[string]any{
		"source_book_id": src.ID, "target_book_id": target.ID, "copied": copied, "moved": moved,
	})
	ok(c, gin.H{"copied_documents": copied, "moved": moved, "target_book_id": target.ID, "target_slug": target.Slug})
}

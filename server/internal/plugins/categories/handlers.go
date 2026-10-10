package categories

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(pluginKey)
	// 公开：分类树（含公开书籍数）与单个分类（路径、子分类）
	api.GET("/categories", core.OptionalAuth(), feat, b.PublicTree)
	api.GET("/categories/:slug", core.OptionalAuth(), feat, b.PublicCategory)
	// 管理员
	admin := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage), h}
	}
	api.GET("/admin/categories", admin(b.AdminTree)...)
	api.POST("/admin/categories", admin(b.AdminCreate)...)
	api.PUT("/admin/categories/:id", admin(b.AdminUpdate)...)
	api.DELETE("/admin/categories/:id", admin(b.AdminDelete)...)
	api.GET("/admin/category-books", admin(b.AdminBooks)...)
	api.POST("/admin/category-books/assign", admin(b.AdminAssign)...)
}

// —— 视图 ——

type node struct {
	Category
	BookCount int64   `json:"book_count"` // 含子分类
	Children  []*node `json:"children"`
}

// counts 每个分类直接归入的书籍数（publicOnly 时只计公开可读的书）。
func (b *behavior) counts(publicOnly bool) map[uint]int64 {
	type row struct {
		CategoryID uint
		N          int64
	}
	var rows []row
	q := b.core.Gorm().Table("book_category_assignments bca").Select("bca.category_id, COUNT(*) AS n").
		Joins("JOIN books ON books.id = bca.book_id AND books.deleted_at IS NULL")
	if publicOnly {
		q = q.Where("books.is_public = ? AND books.status IN ?", true, b.core.PubliclyReadableBookStatuses())
	}
	q.Group("bca.category_id").Scan(&rows)
	out := map[uint]int64{}
	for _, r := range rows {
		out[r.CategoryID] = r.N
	}
	return out
}

// build 组装分类树，书籍数累加到上级。
func (t tree) build(counts map[uint]int64) []*node {
	var walk func(parent uint, depth int) ([]*node, int64)
	walk = func(parent uint, depth int) ([]*node, int64) {
		out := []*node{}
		var total int64
		if depth > maxDepth+1 {
			return out, 0
		}
		for _, id := range t.children[parent] {
			n := &node{Category: t.byID[id]}
			var sub int64
			n.Children, sub = walk(id, depth+1)
			n.BookCount = counts[id] + sub
			total += n.BookCount
			out = append(out, n)
		}
		return out, total
	}
	nodes, _ := walk(0, 1)
	return nodes
}

// PublicTree GET /categories 分类树（书籍数只计公开可读的书，含子分类）。
func (b *behavior) PublicTree(c *gin.Context) {
	b.core.OK(c, gin.H{"items": b.tree().build(b.counts(true))})
}

// PublicCategory GET /categories/:slug 分类详情：自身、路径（顶级 → 自身）与子分类。
func (b *behavior) PublicCategory(c *gin.Context) {
	t := b.tree()
	cat, found := t.bySlug[c.Param("slug")]
	if !found {
		b.core.Fail(c, http.StatusNotFound, "分类不存在")
		return
	}
	counts := b.counts(true)
	var children []*node
	for _, n := range t.build(counts) {
		if found := findNode(n, cat.ID); found != nil {
			children = found.Children
			break
		}
	}
	if children == nil {
		children = []*node{}
	}
	b.core.OK(c, gin.H{"category": cat, "path": t.path(cat.ID), "children": children})
}

func findNode(n *node, id uint) *node {
	if n.ID == id {
		return n
	}
	for _, child := range n.Children {
		if found := findNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

// AdminTree GET /admin/categories 分类树（书籍数含私有书）与未分类书籍数。
func (b *behavior) AdminTree(c *gin.Context) {
	var total, assigned int64
	db := b.core.Gorm()
	db.Model(&models.Book{}).Count(&total)
	db.Table("book_category_assignments bca").Joins("JOIN books ON books.id = bca.book_id AND books.deleted_at IS NULL").Count(&assigned)
	b.core.OK(c, gin.H{"items": b.tree().build(b.counts(false)), "uncategorized": total - assigned, "max_depth": maxDepth})
}

// —— 增删改 ——

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type categoryPayload struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	IconType    string `json:"icon_type"`
	IconValue   string `json:"icon_value"`
	ParentID    uint   `json:"parent_id"`
	SortOrder   int    `json:"sort_order"`
}

// validate 校验并规范化请求；id 为正在编辑的分类（新建为 0）。
func (b *behavior) validate(req *categoryPayload, t tree, id uint) (int, string) {
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Name == "" || utf8.RuneCountInString(req.Name) > maxName {
		return http.StatusBadRequest, fmt.Sprintf("分类名称为 1 到 %d 个字", maxName)
	}
	if utf8.RuneCountInString(req.Description) > maxDesc {
		return http.StatusBadRequest, fmt.Sprintf("简介不超过 %d 字", maxDesc)
	}
	if req.IconType != "" && req.IconType != "fa" && req.IconType != "image" && req.IconType != "svg" {
		return http.StatusBadRequest, "图标类型无效"
	}
	if req.IconType == "" {
		req.IconValue = ""
	}
	if req.Slug == "" {
		req.Slug = b.core.Slugify(req.Name)
		if req.Slug == "" || len(req.Slug) > maxSlug || !slugPattern.MatchString(req.Slug) {
			req.Slug = b.core.RandomSlug("category")
		}
		// 自动生成的 slug 冲突时追加序号
		base := req.Slug
		for i := 2; ; i++ {
			if other, taken := t.bySlug[req.Slug]; !taken || other.ID == id {
				break
			}
			req.Slug = base + "-" + strconv.Itoa(i)
		}
	} else if len(req.Slug) > maxSlug || !slugPattern.MatchString(req.Slug) {
		return http.StatusBadRequest, "地址标识只能包含小写字母、数字和连字符"
	} else if other, taken := t.bySlug[req.Slug]; taken && other.ID != id {
		return http.StatusConflict, "地址标识已被其他分类使用"
	}
	if req.ParentID != 0 {
		if _, found := t.byID[req.ParentID]; !found {
			return http.StatusBadRequest, "上级分类不存在"
		}
		for _, p := range t.path(req.ParentID) {
			if id != 0 && p.ID == id {
				return http.StatusBadRequest, "不能把分类移到它自己或它的子分类下面"
			}
		}
	}
	height := 1
	if id != 0 {
		height = t.height(id)
	}
	if len(t.path(req.ParentID))+height > maxDepth {
		return http.StatusBadRequest, fmt.Sprintf("分类最多 %d 层", maxDepth)
	}
	return 0, ""
}

// AdminCreate POST /admin/categories
func (b *behavior) AdminCreate(c *gin.Context) {
	var req categoryPayload
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	t := b.tree()
	if len(t.byID) >= maxCategories {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("分类最多 %d 个", maxCategories))
		return
	}
	if status, msg := b.validate(&req, t, 0); status != 0 {
		b.core.Fail(c, status, msg)
		return
	}
	cat := Category{Name: req.Name, Slug: req.Slug, Description: req.Description, IconType: req.IconType, IconValue: req.IconValue, ParentID: req.ParentID, SortOrder: req.SortOrder}
	if err := b.core.Gorm().Create(&cat).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.RecordAudit(c, "categories.created", "book_category", strconv.FormatUint(uint64(cat.ID), 10), cat.Name, nil)
	b.core.NotifyIndexablePaths(categoryPath(cat.Slug))
	b.core.OK(c, cat)
}

// AdminUpdate PUT /admin/categories/:id
func (b *behavior) AdminUpdate(c *gin.Context) {
	t := b.tree()
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	cat, found := t.byID[uint(id)]
	if !found {
		b.core.Fail(c, http.StatusNotFound, "分类不存在")
		return
	}
	var req categoryPayload
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if status, msg := b.validate(&req, t, cat.ID); status != 0 {
		b.core.Fail(c, status, msg)
		return
	}
	oldSlug := cat.Slug
	if err := b.core.Gorm().Model(&Category{}).Where("id = ?", cat.ID).Updates(map[string]any{
		"name": req.Name, "slug": req.Slug, "description": req.Description, "icon_type": req.IconType, "icon_value": req.IconValue,
		"parent_id": req.ParentID, "sort_order": req.SortOrder,
	}).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.Gorm().First(&cat, cat.ID)
	b.core.RecordAudit(c, "categories.updated", "book_category", strconv.FormatUint(uint64(cat.ID), 10), cat.Name, nil)
	paths := []string{categoryPath(cat.Slug)}
	if oldSlug != cat.Slug {
		paths = append(paths, categoryPath(oldSlug))
	}
	b.core.NotifyIndexablePaths(paths...)
	b.core.OK(c, cat)
}

// AdminDelete DELETE /admin/categories/:id 只能删除没有子分类、没有书籍的分类。
func (b *behavior) AdminDelete(c *gin.Context) {
	t := b.tree()
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	cat, found := t.byID[uint(id)]
	if !found {
		b.core.Fail(c, http.StatusNotFound, "分类不存在")
		return
	}
	if len(t.children[cat.ID]) > 0 {
		b.core.Fail(c, http.StatusConflict, "请先删除或移走它的子分类")
		return
	}
	var n int64
	b.core.Gorm().Model(&Assignment{}).Where("category_id = ?", cat.ID).Count(&n)
	if n > 0 {
		b.core.Fail(c, http.StatusConflict, fmt.Sprintf("该分类下还有 %d 本书，请先把它们归入其他分类", n))
		return
	}
	if err := b.core.Gorm().Delete(&Category{}, cat.ID).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "删除失败")
		return
	}
	b.core.RecordAudit(c, "categories.deleted", "book_category", strconv.FormatUint(uint64(cat.ID), 10), cat.Name, nil)
	b.core.NotifyIndexablePaths(categoryPath(cat.Slug))
	b.core.OK(c, gin.H{"deleted": true})
}

// —— 批量归类 ——

// AdminBooks GET /admin/category-books?q=&category=<id|none>&page= 按书名 / 作者用户名与当前分类筛选书籍（含私有书）。
// category 为分类 ID 时含其子分类，为 none 时只列未分类的书。
func (b *behavior) AdminBooks(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm()
	q := db.Model(&models.Book{})
	if kw := strings.TrimSpace(c.Query("q")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("books.title LIKE ? OR books.user_id IN (SELECT id FROM users WHERE username LIKE ? OR nickname LIKE ?)", like, like, like)
	}
	switch cat := c.Query("category"); {
	case cat == "none":
		q = q.Where("NOT EXISTS (SELECT 1 FROM book_category_assignments bca WHERE bca.book_id = books.id)")
	case cat != "":
		id, _ := strconv.ParseUint(cat, 10, 64)
		q = q.Where("EXISTS (SELECT 1 FROM book_category_assignments bca WHERE bca.book_id = books.id AND bca.category_id IN ?)", b.tree().subtree(uint(id)))
	}
	var total int64
	q.Count(&total)
	var books []models.Book
	b.core.PreloadBookUserOn(q).Order("books.updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&books)
	b.core.DecorateBookList(books)
	b.core.OK(c, plugincore.PageResult{Items: books, Total: total, Page: page, PageSize: pageSize})
}

// AdminAssign POST /admin/category-books/assign {book_ids, category_id} 批量归类（category_id 为 0 时改为未分类）。
func (b *behavior) AdminAssign(c *gin.Context) {
	var req struct {
		BookIDs    []uint `json:"book_ids"`
		CategoryID uint   `json:"category_id"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.BookIDs) == 0 || len(req.BookIDs) > maxBatch {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("请选择 1 到 %d 本书", maxBatch))
		return
	}
	var ids []uint
	b.core.Gorm().Model(&models.Book{}).Where("id IN ?", req.BookIDs).Pluck("id", &ids)
	if err := b.assign(ids, req.CategoryID); err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	label := "未分类"
	if cat, found := b.tree().byID[req.CategoryID]; found {
		label = cat.Name
		b.core.NotifyIndexablePaths(categoryPath(cat.Slug))
	}
	b.core.RecordAudit(c, "categories.assigned", "book_category", strconv.FormatUint(uint64(req.CategoryID), 10), label, map[string]any{"books": len(ids)})
	b.core.OK(c, gin.H{"updated": len(ids)})
}

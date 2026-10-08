package tags

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// behavior 「标签」插件：端点 handler（本文件）与书籍-标签集成（integration.go，经 plugincore 扩展点接入核心书籍流程）。
type behavior struct{ core plugincore.Core }

func init() { plugincore.RegisterBehavior(&behavior{}) }

func (b *behavior) Key() string { return plugins.KeyTags }

// RegisterRoutes 注册标签相关路由（与原 router.go 中的分组/中间件链一致）。
func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(plugins.KeyTags)
	// 公开
	api.GET("/tags", core.OptionalAuth(), feat, b.ListTags)
	api.GET("/tags/:slug/books", core.OptionalAuth(), feat, b.BooksByTag)
	// 登录用户
	api.POST("/tags", core.RequireAuth(), feat, core.RequirePermissionMiddleware(authz.TagCreate), b.CreateTag)
	api.DELETE("/tags/:id", core.RequireAuth(), feat, core.RequirePermissionMiddleware(authz.TagDelete), b.DeleteTag)
	// 管理员
	api.GET("/admin/tags", core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(authz.TagManage), b.AdminListTags)
	api.POST("/admin/tags", core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(authz.TagManage), b.AdminCreateTag)
	api.PUT("/admin/tags/:id", core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(authz.TagManage), b.AdminUpdateTag)
	api.DELETE("/admin/tags/:id", core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(authz.TagDelete), b.DeleteTag)
}

func validTagIconType(t string) bool { return t == "" || t == "fa" || t == "image" || t == "svg" }

// findOrCreateTag 按名称查找或创建标签（slug 冲突时追加后缀）。
func (b *behavior) findOrCreateTag(name string) (*models.Tag, error) {
	core := b.core
	var tag models.Tag
	if err := core.Gorm().Where("name = ?", name).First(&tag).Error; err == nil {
		return &tag, nil
	}
	slug := core.Slugify(name)
	if slug == "" {
		slug = core.RandomSlug("tag")
	}
	tag = models.Tag{Name: name, Slug: slug}
	for i := 0; i < 50; i++ {
		candidate := tag.Slug
		if i > 0 {
			candidate = slug + "-" + strconv.Itoa(i+1)
		}
		var count int64
		core.Gorm().Model(&models.Tag{}).Where("slug = ?", candidate).Count(&count)
		if count == 0 {
			tag.Slug = candidate
			break
		}
	}
	if err := core.Gorm().Create(&tag).Error; err != nil {
		var existing models.Tag
		if err := core.Gorm().Where("name = ?", name).First(&existing).Error; err == nil {
			return &existing, nil
		}
		return nil, err
	}
	return &tag, nil
}

// ListTags GET /tags 标签列表（含公开书籍使用计数）
func (b *behavior) ListTags(c *gin.Context) {
	core := b.core
	q := strings.TrimSpace(c.Query("q"))
	paged := c.Query("page") != "" || c.Query("page_size") != ""
	page, pageSize := 1, 0
	var total int64
	if paged {
		page, pageSize = core.Paginate(c)
		countQuery := core.Gorm().Model(&models.Tag{}).
			Joins("JOIN book_tags ON book_tags.tag_id = tags.id").
			Joins("JOIN books ON books.id = book_tags.book_id").
			Where("books.is_public = ? AND books.status IN ?", true, core.PubliclyReadableBookStatuses())
		if core.CurrentUser(c) == nil {
			countQuery = countQuery.Where("books.login_required = ?", false)
		}
		if q != "" {
			countQuery = countQuery.Where("tags.name LIKE ?", "%"+q+"%")
		}
		if err := countQuery.Distinct("tags.id").Count(&total).Error; err != nil {
			core.Fail(c, http.StatusInternalServerError, "查询失败")
			return
		}
		if total > 0 {
			lastPage := int((total + int64(pageSize) - 1) / int64(pageSize))
			if page > lastPage {
				page = lastPage
			}
		}
	}
	limit := core.AtoiDefault(c.Query("limit"), 50)
	if limit < 1 || limit > 200 {
		limit = 50
	}

	query := core.Gorm().Model(&models.Tag{}).
		Select("tags.id, tags.name, tags.slug, tags.icon_type, tags.icon_value, COUNT(book_tags.book_id) AS book_count").
		Joins("JOIN book_tags ON book_tags.tag_id = tags.id").
		Joins("JOIN books ON books.id = book_tags.book_id").
		Where("books.is_public = ? AND books.status IN ?", true, core.PubliclyReadableBookStatuses()).
		Group("tags.id")
	if core.CurrentUser(c) == nil {
		query = query.Where("books.login_required = ?", false)
	}
	if q != "" {
		query = query.Where("tags.name LIKE ?", "%"+q+"%")
	}
	tagsList := []models.Tag{}
	if paged {
		query = query.Limit(pageSize).Offset((page - 1) * pageSize)
	} else {
		query = query.Limit(limit)
	}
	if err := query.Order("book_count DESC").Find(&tagsList).Error; err != nil {
		core.Fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	if paged {
		core.OK(c, plugincore.PageResult{Items: tagsList, Total: total, Page: page, PageSize: pageSize})
		return
	}
	core.OK(c, tagsList)
}

// CreateTag POST /tags 创建标签
func (b *behavior) CreateTag(c *gin.Context) {
	core := b.core
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		core.Fail(c, http.StatusBadRequest, "请填写标签名称")
		return
	}
	name := strings.TrimSpace(req.Name)
	if len([]rune(name)) > 50 {
		core.Fail(c, http.StatusBadRequest, "标签名称过长（最多 50 字）")
		return
	}
	tag, err := b.findOrCreateTag(name)
	if err != nil {
		core.Fail(c, http.StatusInternalServerError, "创建标签失败: "+err.Error())
		return
	}
	core.OK(c, tag)
}

// DeleteTag DELETE /tags/:id 删除标签并解绑全部书籍（仅管理员）
func (b *behavior) DeleteTag(c *gin.Context) {
	core := b.core
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var tag models.Tag
	if err := core.Gorm().First(&tag, id).Error; err != nil {
		core.Fail(c, http.StatusNotFound, "标签不存在")
		return
	}
	if err := core.Gorm().Where("tag_id = ?", tag.ID).Delete(&models.BookTag{}).Error; err != nil {
		core.Fail(c, http.StatusInternalServerError, "解绑书籍失败: "+err.Error())
		return
	}
	if err := core.Gorm().Delete(&tag).Error; err != nil {
		core.Fail(c, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	core.OK(c, gin.H{"message": "已删除"})
}

// AdminListTags GET /admin/tags 后台标签管理：列出全部标签（含总使用计数），支持搜索与分页
func (b *behavior) AdminListTags(c *gin.Context) {
	core := b.core
	page, pageSize := core.Paginate(c)
	q := strings.TrimSpace(c.Query("q"))

	countQuery := core.Gorm().Model(&models.Tag{})
	if q != "" {
		countQuery = countQuery.Where("name LIKE ?", "%"+q+"%")
	}
	var total int64
	countQuery.Count(&total)

	query := core.Gorm().Model(&models.Tag{}).
		Select("tags.id, tags.name, tags.slug, tags.icon_type, tags.icon_value, tags.created_at, COUNT(book_tags.book_id) AS book_count").
		Joins("LEFT JOIN book_tags ON book_tags.tag_id = tags.id").
		Group("tags.id")
	if q != "" {
		query = query.Where("tags.name LIKE ?", "%"+q+"%")
	}
	tagsList := []models.Tag{}
	if err := query.Order("book_count DESC, tags.id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).Find(&tagsList).Error; err != nil {
		core.Fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	core.OK(c, plugincore.PageResult{Items: tagsList, Total: total, Page: page, PageSize: pageSize})
}

// AdminCreateTag POST /admin/tags 后台创建标签（可带图标）
func (b *behavior) AdminCreateTag(c *gin.Context) {
	core := b.core
	var req struct {
		Name      string `json:"name"`
		IconType  string `json:"icon_type"`
		IconValue string `json:"icon_value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		core.Fail(c, http.StatusBadRequest, "请填写标签名称")
		return
	}
	if !validTagIconType(req.IconType) {
		core.Fail(c, http.StatusBadRequest, "图标类型无效")
		return
	}
	name := strings.TrimSpace(req.Name)
	if len([]rune(name)) > 50 {
		core.Fail(c, http.StatusBadRequest, "标签名称过长（最多 50 字）")
		return
	}
	tag, err := b.findOrCreateTag(name)
	if err != nil {
		core.Fail(c, http.StatusInternalServerError, "创建标签失败: "+err.Error())
		return
	}
	if err := core.Gorm().Model(tag).Updates(map[string]any{"icon_type": req.IconType, "icon_value": strings.TrimSpace(req.IconValue)}).Error; err != nil {
		core.Fail(c, http.StatusInternalServerError, "保存图标失败: "+err.Error())
		return
	}
	core.OK(c, tag)
}

// AdminUpdateTag PUT /admin/tags/:id 后台更新标签名称与图标（slug 保持不变，避免破坏既有链接）
func (b *behavior) AdminUpdateTag(c *gin.Context) {
	core := b.core
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var req struct {
		Name      string `json:"name"`
		IconType  string `json:"icon_type"`
		IconValue string `json:"icon_value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		core.Fail(c, http.StatusBadRequest, "请填写标签名称")
		return
	}
	if !validTagIconType(req.IconType) {
		core.Fail(c, http.StatusBadRequest, "图标类型无效")
		return
	}
	name := strings.TrimSpace(req.Name)
	if len([]rune(name)) > 50 {
		core.Fail(c, http.StatusBadRequest, "标签名称过长（最多 50 字）")
		return
	}
	var tag models.Tag
	if err := core.Gorm().First(&tag, id).Error; err != nil {
		core.Fail(c, http.StatusNotFound, "标签不存在")
		return
	}
	if name != tag.Name {
		var count int64
		core.Gorm().Model(&models.Tag{}).Where("name = ? AND id <> ?", name, tag.ID).Count(&count)
		if count > 0 {
			core.Fail(c, http.StatusConflict, "同名标签已存在")
			return
		}
	}
	if err := core.Gorm().Model(&tag).Updates(map[string]any{"name": name, "icon_type": req.IconType, "icon_value": strings.TrimSpace(req.IconValue)}).Error; err != nil {
		core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	core.OK(c, tag)
}

// BooksByTag GET /tags/:slug/books 按标签查询公开书籍
func (b *behavior) BooksByTag(c *gin.Context) {
	core := b.core
	page, pageSize := core.Paginate(c)
	slug := c.Param("slug")

	var tag models.Tag
	if err := core.Gorm().Where("slug = ?", slug).First(&tag).Error; err != nil {
		core.Fail(c, http.StatusNotFound, "标签不存在")
		return
	}

	base := core.Gorm().Model(&models.Book{}).
		Joins("JOIN book_tags ON book_tags.book_id = books.id").
		Joins("JOIN tags ON tags.id = book_tags.tag_id AND tags.slug = ?", slug).
		Where("books.is_public = ? AND books.status IN ?", true, core.PubliclyReadableBookStatuses())
	if core.CurrentUser(c) == nil {
		base = base.Where("books.login_required = ?", false)
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		core.Fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	var books []models.Book
	if err := core.PreloadBookUserOn(base).
		Order("books.created_at DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&books).Error; err != nil {
		core.Fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	core.AttachChapterCounts(books)
	core.DecorateBookList(books)
	core.OK(c, plugincore.PageResult{Items: books, Total: total, Page: page, PageSize: pageSize})
}

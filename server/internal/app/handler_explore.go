package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type footerLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

type footerLinkGroup struct {
	Title string       `json:"title"`
	Links []footerLink `json:"links"`
}

// normalizeFooterLinks 校验并归一化管理员配置的页脚链接分组（JSON）。
// 空串或空数组表示清空（前端回退到默认页脚）。上限：8 组、每组 12 条链接。
func normalizeFooterLinks(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return "", nil
	}
	var groups []footerLinkGroup
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return "", errors.New("页脚链接格式错误")
	}
	if len(groups) > 8 {
		return "", errors.New("页脚分组不能超过 8 组")
	}
	out := make([]footerLinkGroup, 0, len(groups))
	for _, g := range groups {
		title := truncateText(strings.TrimSpace(g.Title), 40)
		if len(g.Links) > 12 {
			return "", errors.New("每个分组的链接不能超过 12 条")
		}
		links := make([]footerLink, 0, len(g.Links))
		for _, l := range g.Links {
			label := truncateText(strings.TrimSpace(l.Label), 60)
			href := truncateText(strings.TrimSpace(l.Href), 500)
			if label == "" || href == "" {
				continue // 跳过不完整的链接
			}
			links = append(links, footerLink{Label: label, Href: href})
		}
		if len(links) == 0 {
			continue // 跳过无有效链接的分组
		}
		out = append(out, footerLinkGroup{Title: title, Links: links})
	}
	if len(out) == 0 {
		return "", nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", errors.New("页脚链接序列化失败")
	}
	return string(b), nil
}

func (a *App) publicReadableBooks() *gorm.DB {
	return a.DB.Where("is_public = ? AND status IN ?", true, publiclyReadableBookStatuses)
}

// publicDiscoverableBooks 在公开可读基础上，对未登录游客隐藏「仅登录可读」书籍
func (a *App) publicDiscoverableBooks(u *models.User) *gorm.DB {
	q := a.publicReadableBooks()
	if u == nil {
		q = q.Where("login_required = ?", false)
	}
	return q
}

// ExploreHot GET /explore/hot 浏览量最高的公开书籍（首页精选按屏宽自适应展示，取 8 本留出宽屏余量）
func (a *App) ExploreHot(c *gin.Context) {
	books := []models.Book{}
	if err := preloadBookUser(a.publicDiscoverableBooks(currentUser(c))).
		Order("view_count DESC").Limit(8).Find(&books).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	a.attachChapterCounts(books)
	a.decorateBookList(books)
	ok(c, books)
}

// ExploreLatest GET /explore/latest 最新发布的 6 本公开书籍
func (a *App) ExploreLatest(c *gin.Context) {
	books := []models.Book{}
	if err := preloadBookUser(a.publicDiscoverableBooks(currentUser(c))).
		Order("created_at DESC").Limit(6).Find(&books).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	a.attachChapterCounts(books)
	a.decorateBookList(books)
	ok(c, books)
}

// ExploreActiveAuthors GET /explore/active-authors 活跃作者：按公开书籍数排序，附作品数与总阅读量
func (a *App) ExploreActiveAuthors(c *gin.Context) {
	type authorRow struct {
		ID          uint   `json:"id"`
		Username    string `json:"username"`
		Nickname    string `json:"nickname"`
		NameDisplay string `json:"-"`
		DisplayName string `json:"display_name" gorm:"-"`
		Avatar      string `json:"avatar"`
		BookCount   int64  `json:"book_count"`
		TotalViews  int64  `json:"total_views"`
	}
	rows := []authorRow{}
	a.DB.Table("books").
		Select("users.id as id, users.username as username, users.nickname as nickname, users.name_display as name_display, users.avatar as avatar, COUNT(books.id) as book_count, COALESCE(SUM(books.view_count), 0) as total_views").
		Joins("JOIN users ON users.id = books.user_id").
		Where("books.is_public = ? AND books.status IN ? AND books.deleted_at IS NULL", true, publiclyReadableBookStatuses).
		Group("users.id, users.username, users.nickname, users.name_display, users.avatar").
		Order("book_count DESC, total_views DESC").
		Limit(8).
		Find(&rows)
	for i := range rows {
		u := models.User{Username: rows[i].Username, Nickname: rows[i].Nickname, NameDisplay: rows[i].NameDisplay}
		rows[i].DisplayName = u.PublicName()
	}
	ok(c, gin.H{"items": rows})
}

// SiteStats GET /stats 站点统计
func (a *App) SiteStats(c *gin.Context) {
	var userCount, bookCount, docCount int64
	var views int64
	a.DB.Model(&models.User{}).Count(&userCount)
	publicBooks := a.DB.Model(&models.Book{}).Where("is_public = ? AND status IN ?", true, publiclyReadableBookStatuses)
	publicBooks.Count(&bookCount)
	a.DB.Model(&models.Document{}).
		Joins("JOIN books b ON b.id = documents.book_id").
		Where("b.is_public = ? AND b.status IN ? AND documents.status = ?", true, publiclyReadableBookStatuses, "published").
		Count(&docCount)
	publicBooks.Select("COALESCE(SUM(view_count), 0)").Scan(&views)
	stats := gin.H{
		"user_count":     userCount,
		"book_count":     bookCount,
		"document_count": docCount,
		"total_views":    views,
	}
	plugincore.CollectStats(a, true, stats) // 插件补充的统计（如标签插件的 tag_count，仅计公开书籍上的标签）
	ok(c, stats)
}

// GetSiteConfig GET /site 公开站点配置
func (a *App) GetSiteConfig(c *gin.Context) {
	var rows []models.SiteConfig
	a.DB.Where("config_key IN ?", []string{"site_name", "site_description", "site_logo", "site_favicon", "site_keywords", "site_footer_text", "site_footer_links", "site_beian", "help_doc_url", "terms_url", "privacy_url", "version", "installation_date", "comments_enabled", "announcement_enabled", "announcement_text", "announcement_tone", "book_versions_sort", cfgRegRequireActivation, "custom_head_html", "custom_footer_html"}).Find(&rows)
	cfg := gin.H{}
	for _, r := range rows {
		cfg[r.ConfigKey] = r.ConfigValue
	}
	// 仅暴露翻译是否可用，不泄露 API Key 等敏感配置
	cfg["translation_enabled"] = a.translationEnabled()
	// 「我的 AI 用量」入口：站点配置了 AI 服务或翻译服务时显示
	chat, embed := a.AIStatus()
	cfg["ai_usage_enabled"] = chat || embed || a.translationEnabled()
	// 暴露已启用的特性插件键，供前端联动显示/隐藏对应页面与入口
	featurePlugins := []string{}
	for _, info := range pluginRegistry {
		if info.Kind == pluginKindFeature && a.pluginEnabled(info.Key) {
			featurePlugins = append(featurePlugins, info.Key)
		}
	}
	cfg["feature_plugins"] = featurePlugins
	plugincore.CollectPublicSiteConfig(a, cfg) // 插件补充的公开配置（如成就开关、经验排行榜是否开放、采集入口）
	ok(c, cfg)
}

type siteConfigUpdate struct {
	SiteName            *string `json:"site_name"`
	SiteDescription     *string `json:"site_description"`
	SiteLogo            *string `json:"site_logo"`
	SiteFavicon         *string `json:"site_favicon"`
	SiteKeywords        *string `json:"site_keywords"`
	SiteFooterText      *string `json:"site_footer_text"`
	SiteFooterLinks     *string `json:"site_footer_links"` // JSON: [{title, links:[{label, href}]}]
	SiteBeian           *string `json:"site_beian"`
	HelpDocURL          *string `json:"help_doc_url"`
	TermsURL            *string `json:"terms_url"`
	PrivacyURL          *string `json:"privacy_url"`
	AnnouncementEnabled *bool   `json:"announcement_enabled"`
	AnnouncementText    *string `json:"announcement_text"`
	AnnouncementTone    *string `json:"announcement_tone"`  // info | warning
	BookVersionsSort    *string `json:"book_versions_sort"` // 多版本阅读页排序：desc(默认,最新在前) | asc
	CustomHeadHTML      *string `json:"custom_head_html"`   // 每个页面 <head> 中追加的 HTML（统计脚本、站点验证 meta 等）
	CustomFooterHTML    *string `json:"custom_footer_html"` // 每个页面 </body> 前追加的 HTML
}

// maxCustomHTML 自定义 Head / Footer HTML 的长度上限（字节）。
const maxCustomHTML = 64 << 10

// UpdateSiteConfig PUT /site 管理员更新站点配置
func (a *App) UpdateSiteConfig(c *gin.Context) {
	var req siteConfigUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	updates := map[string]string{}
	if req.SiteName != nil {
		updates["site_name"] = *req.SiteName
	}
	if req.SiteDescription != nil {
		updates["site_description"] = *req.SiteDescription
	}
	if req.SiteLogo != nil {
		updates["site_logo"] = strings.TrimSpace(*req.SiteLogo)
	}
	if req.SiteFavicon != nil {
		updates["site_favicon"] = strings.TrimSpace(*req.SiteFavicon)
	}
	if req.SiteKeywords != nil {
		updates["site_keywords"] = strings.TrimSpace(*req.SiteKeywords)
	}
	if req.SiteFooterText != nil {
		updates["site_footer_text"] = strings.TrimSpace(*req.SiteFooterText)
	}
	if req.SiteFooterLinks != nil {
		normalized, err := normalizeFooterLinks(*req.SiteFooterLinks)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		updates["site_footer_links"] = normalized
	}
	if req.SiteBeian != nil {
		updates["site_beian"] = strings.TrimSpace(*req.SiteBeian)
	}
	if req.HelpDocURL != nil {
		updates["help_doc_url"] = strings.TrimSpace(*req.HelpDocURL)
	}
	if req.TermsURL != nil {
		updates["terms_url"] = strings.TrimSpace(*req.TermsURL)
	}
	if req.PrivacyURL != nil {
		updates["privacy_url"] = strings.TrimSpace(*req.PrivacyURL)
	}
	if req.AnnouncementEnabled != nil {
		v := "false"
		if *req.AnnouncementEnabled {
			v = "true"
		}
		updates["announcement_enabled"] = v
	}
	if req.AnnouncementText != nil {
		updates["announcement_text"] = strings.TrimSpace(*req.AnnouncementText)
	}
	if req.AnnouncementTone != nil {
		tone := strings.TrimSpace(*req.AnnouncementTone)
		if tone != "warning" {
			tone = "info"
		}
		updates["announcement_tone"] = tone
	}
	if req.BookVersionsSort != nil {
		sort := "desc"
		if strings.TrimSpace(*req.BookVersionsSort) == "asc" {
			sort = "asc"
		}
		updates["book_versions_sort"] = sort
	}
	for field, p := range map[string]*string{"custom_head_html": req.CustomHeadHTML, "custom_footer_html": req.CustomFooterHTML} {
		if p == nil {
			continue
		}
		v := strings.TrimSpace(*p)
		if len(v) > maxCustomHTML {
			fail(c, http.StatusBadRequest, fmt.Sprintf("自定义 HTML 不能超过 %d KB", maxCustomHTML>>10))
			return
		}
		updates[field] = v
	}
	for key, value := range updates {
		var cfg models.SiteConfig
		if err := a.DB.Where("config_key = ?", key).First(&cfg).Error; err != nil {
			cfg = models.SiteConfig{ConfigKey: key}
		}
		cfg.ConfigValue = value
		if err := a.DB.Save(&cfg).Error; err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	fields := make([]string, 0, len(updates))
	for key := range updates {
		fields = append(fields, key)
	}
	a.recordAudit(c, "site.updated", "site", "public", "公开站点配置", map[string]any{"changed_fields": fields})
	ok(c, gin.H{"message": "已保存"})
}

// GetUserProfile GET /users/:username
func (a *App) GetUserProfile(c *gin.Context) {
	var u models.User
	if err := a.DB.Select("id", "username", "avatar", "bio", "github_url", "nickname", "name_display", "website", "location", "company", "role", "created_at").
		Where("username = ?", c.Param("username")).First(&u).Error; err != nil {
		fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	var bookCount int64
	a.DB.Model(&models.Book{}).Where("user_id = ? AND is_public = ? AND status IN ?", u.ID, true, publiclyReadableBookStatuses).Count(&bookCount)
	ok(c, gin.H{
		"id": u.ID, "username": u.Username, "avatar": u.Avatar, "bio": u.Bio,
		"github_url": u.GithubURL, "nickname": u.Nickname, "display_name": u.PublicName(), "website": u.Website,
		"location": u.Location, "company": u.Company, "role": u.Role, "created_at": u.CreatedAt,
		"public_book_count": bookCount,
	})
}

// GetUserBooks GET /users/:username/books 该用户的公开书籍
func (a *App) GetUserBooks(c *gin.Context) {
	page, pageSize := paginate(c)
	var u models.User
	if err := a.DB.Select("id").Where("username = ?", c.Param("username")).First(&u).Error; err != nil {
		fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	query := a.DB.Model(&models.Book{}).Where("user_id = ? AND is_public = ? AND status IN ?", u.ID, true, publiclyReadableBookStatuses)
	var versionCounts map[string]int
	if c.Query("group_versions") == "true" && a.pluginEnabled(pluginBookVersions) {
		query, versionCounts = a.groupBookVersions(query)
	}
	var total int64
	query.Count(&total)
	books := []models.Book{}
	if err := preloadBookUser(query).Order(bookOrder(c.Query("sort"))).
		Limit(pageSize).Offset((page - 1) * pageSize).Find(&books).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	a.attachChapterCounts(books)
	a.decorateBookList(books)
	a.attachVersionInfo(books, versionCounts, currentUser(c))
	ok(c, PageResult{Items: books, Total: total, Page: page, PageSize: pageSize})
}

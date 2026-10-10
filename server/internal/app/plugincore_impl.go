package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/config"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 本文件把 *App 适配为 plugincore.Core：插件子包通过该接口访问核心能力，避免与 app 包循环依赖。
// 这些方法都是对既有 app 内部函数/字段的薄封装，行为与原来完全一致。
var _ plugincore.Core = (*App)(nil)

func (a *App) Gorm() *gorm.DB                                  { return a.DB }
func (a *App) CurrentUser(c *gin.Context) *models.User         { return currentUser(c) }
func (a *App) OK(c *gin.Context, data any)                     { ok(c, data) }
func (a *App) Fail(c *gin.Context, status int, message string) { fail(c, status, message) }
func (a *App) AtoiDefault(s string, def int) int               { return atoiDefault(s, def) }
func (a *App) PluginEnabled(key string) bool                   { return a.pluginEnabled(key) }
func (a *App) IsAdmin(u *models.User) bool                     { return IsAdmin(u) }
func (a *App) CanReadBook(u *models.User, b *models.Book) bool { return a.canReadBook(u, b) }
func (a *App) FindBook(c *gin.Context) (*models.Book, int)     { return a.findBook(c) }
func (a *App) PreloadBookUser() *gorm.DB                       { return preloadBookUser(a.DB) }
func (a *App) PreloadBookUserOn(db *gorm.DB) *gorm.DB          { return preloadBookUser(db) }
func (a *App) AttachChapterCounts(books []models.Book)         { a.attachChapterCounts(books) }
func (a *App) DecorateBookList(books []models.Book)            { a.decorateBookList(books) }
func (a *App) PubliclyReadableBookStatuses() []string          { return publiclyReadableBookStatuses }
func (a *App) RateLimitReaction() gin.HandlerFunc              { return a.RateLimit(reactionRateLimit) }

// PrivateDataDir 插件私有目录：<数据目录>/plugin-data/<name>。
func (a *App) PrivateDataDir(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return "", fmt.Errorf("无效的目录名: %q", name)
	}
	dir := filepath.Join(config.DataDir(), "plugin-data", name)
	return dir, os.MkdirAll(dir, 0o755)
}

func (a *App) RequirePermissionMiddleware(perm authz.Permission) gin.HandlerFunc {
	return a.RequirePermission(perm)
}
func (a *App) RecordAudit(c *gin.Context, action, resourceType, resourceID, label string, summary map[string]any) {
	a.recordAudit(c, action, resourceType, resourceID, label, summary)
}
func (a *App) Paginate(c *gin.Context) (int, int) { return paginate(c) }
func (a *App) Slugify(s string) string            { return slugify(s) }
func (a *App) RandomSlug(prefix string) string    { return randomSlug(prefix) }

// —— 内容采集插件所需 ——
func (a *App) CanEditBookContent(u *models.User, b *models.Book) bool {
	return a.canEditBookContent(u, b)
}
func (a *App) GetSetting(key string) string       { return a.getSetting(key) }
func (a *App) NotifyIndexableURLs(urls ...string) { a.emitIndexableURLs(urls...) }

// NotifyIndexablePaths 站内路径 → 绝对地址后推送；未配置可索引的站点地址时只重建 Sitemap。
func (a *App) NotifyIndexablePaths(paths ...string) {
	base := a.indexableSiteBase()
	if base == "" {
		a.enqueueSitemapGenerateSoon()
		return
	}
	urls := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasPrefix(p, "/") {
			urls = append(urls, base+p)
		}
	}
	if len(urls) == 0 {
		a.enqueueSitemapGenerateSoon()
		return
	}
	a.emitIndexableURLs(urls...)
}
func (a *App) NotifyIndexableBookChange(before, after *models.Book) {
	a.notifyBookIndexableChange(before, after)
}
func (a *App) NotifyIndexableDocumentChange(oldBook, newBook *models.Book, before, after *models.Document) {
	a.notifyDocumentIndexableChange(oldBook, newBook, before, after)
}
func (a *App) NotifyIndexableVariantGroupsChanged(groups ...string) {
	a.emitIndexableURLs(a.indexableVariantGroupURLs(groups...)...)
}
func (a *App) InstalledChromePath() string { return a.installedChromePath() }
func (a *App) JobQueue() *jobqueue.Queue   { return a.jobQueue() }
func (a *App) UniqueChildSlug(bookID uint, parentID *uint, base string, excludeID uint) string {
	return a.uniqueChildSlug(bookID, parentID, base, excludeID)
}
func (a *App) CreateContentImportBook(u *models.User, title, description string, chapters []plugincore.ImportedChapter) (models.Book, error) {
	local := make([]importedChapter, len(chapters))
	for i, ch := range chapters {
		local[i] = importedChapter{Title: ch.Title, Content: ch.Content}
	}
	return a.createContentImportBook(u, title, description, local)
}
func (a *App) CreateDraftBookFrom(u *models.User, src *models.Book, title string) (models.Book, error) {
	if err := a.EnsureBookQuota(u); err != nil {
		return models.Book{}, err
	}
	title = truncateText(strings.TrimSpace(title), 255)
	if title == "" {
		title = src.Title
	}
	slug := a.uniqueBookSlug(slugify(title))
	if slug == "" {
		slug = a.uniqueBookSlug(randomSlug("book"))
	}
	return a.copyBookShell(u, src, title, slug, true)
}
func (a *App) InitialChapterStatus(book *models.Book, parentID *uint) string {
	return a.initialChapterStatus(book, parentID)
}
func (a *App) NewDocumentRevision(doc *models.Document, userID uint, reason string) models.DocumentRevision {
	return newDocumentRevision(doc, userID, reason)
}
func (a *App) ExtractDocIcon(content string) string { return extractDocIcon(content) }

// —— 成就插件所需 ——
func (a *App) SetSetting(key, value, description string) error {
	return a.setSetting(key, value, description)
}
func (a *App) LoadResourceTranslations(db *gorm.DB, kind string, id uint) (map[string]plugincore.ResourceTranslation, error) {
	return loadResourceTranslations(db, kind, id)
}
func (a *App) SaveResourceTranslations(tx *gorm.DB, kind string, id, actor uint, translations map[string]plugincore.ResourceTranslation) error {
	return saveResourceTranslations(tx, kind, id, actor, translations)
}
func (a *App) DefaultContentLocale() (string, error) {
	locales, err := a.siteLocales()
	if err != nil {
		return "", err
	}
	return defaultLocale(locales), nil
}
func (a *App) LocalizeResources(c *gin.Context, kind string, ids []uint) (map[uint]plugincore.LocalizedResource, string, error) {
	return a.localizeResources(c, kind, ids)
}
func (a *App) PublicBackgroundJob(job *models.BackgroundJob) any { return publicBackgroundJob(job) }

// —— PDF 导出插件所需 ——
func (a *App) CanExportBook(u *models.User, book *models.Book) bool { return a.canExportBook(u, book) }
func (a *App) ExportFormatAllowed(book *models.Book, format string) bool {
	return formatAllowed(book, format)
}
func (a *App) ResolveExportStyle(style string, book *models.Book, u *models.User) models.UserExportSetting {
	return a.resolveExportStyle(style, book, u)
}
func (a *App) ResolveExportFooter(book *models.Book, u *models.User) string {
	return a.resolveExportFooter(book, u)
}
func (a *App) RecordBookExport(u *models.User, book *models.Book, format string) {
	a.recordBookExport(u, book, format)
}
func (a *App) WebPort() int { return a.web.Port() }

package app

import (
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/plugincore"
)

// sitemapEntry 一条可被搜索引擎抓取的公开 URL（相对路径 + 最近更新时间）。
type sitemapEntry struct {
	Path    string    `json:"path"`
	LastMod time.Time `json:"lastmod"`
}

// SitemapURLs GET /sitemap 返回站点全部公开可读内容的相对路径清单（供前端 /sitemap.xml 渲染）。
// 仅包含公开且非登录限制的书籍详情页，以及这些书籍下已发布章节的阅读页。
func (a *App) SitemapURLs(c *gin.Context) {
	type bookRow struct {
		ID        uint
		Slug      string
		UpdatedAt time.Time
	}
	books := []bookRow{}
	a.DB.Table("books").
		Select("id, slug, updated_at").
		Where("is_public = ? AND login_required = ? AND status IN ? AND deleted_at IS NULL", true, false, publiclyReadableBookStatuses).
		Order("updated_at DESC").
		Find(&books)

	entries := make([]sitemapEntry, 0, len(books)*4)
	bookIDs := make([]uint, 0, len(books))
	slugByID := make(map[uint]string, len(books))
	for _, b := range books {
		entries = append(entries, sitemapEntry{Path: "/book/detail/" + b.Slug, LastMod: b.UpdatedAt})
		bookIDs = append(bookIDs, b.ID)
		slugByID[b.ID] = b.Slug
	}

	if len(bookIDs) > 0 {
		type docRow struct {
			BookID    uint
			Slug      string
			UpdatedAt time.Time
		}
		docs := []docRow{}
		a.DB.Table("documents").
			Select("book_id, slug, updated_at").
			Where("book_id IN ? AND status = ? AND deleted_at IS NULL", bookIDs, "published").
			Find(&docs)
		for _, d := range docs {
			if bookSlug, ok := slugByID[d.BookID]; ok {
				entries = append(entries, sitemapEntry{Path: "/book/reader/" + bookSlug + "/" + d.Slug, LastMod: d.UpdatedAt})
			}
		}
	}

	for _, p := range plugincore.SitemapPages(a) {
		entries = append(entries, sitemapEntry{Path: p.Path, LastMod: p.LastMod})
	}
	ok(c, gin.H{"entries": entries})
}

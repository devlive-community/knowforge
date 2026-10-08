package contentcollect

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
)

// CleanupBookInternalLinks POST /books/:id/cleanup/internal-links
// 用本书历史采集记录（CrawlPage）构建「原始URL→章节」映射，把全书正文里指向这些页面的外链改写为站内链接。
func (cc *behavior) CleanupBookInternalLinks(c *gin.Context) {
	book, status := cc.core.FindBook(c)
	if book == nil {
		cc.core.Fail(c, status, "书籍不存在")
		return
	}
	if !cc.core.CanEditBookContent(cc.core.CurrentUser(c), book) {
		cc.core.Fail(c, http.StatusForbidden, "无权操作该书籍")
		return
	}
	db := cc.core.Gorm()
	// 本书全部采集页面（成功且已生成章节）→ 归一化URL→docID
	var jobIDs []uint
	db.Model(&CrawlJob{}).Where("book_id = ?", book.ID).Pluck("id", &jobIDs)
	urlToSlug := map[string]string{}
	if len(jobIDs) > 0 {
		var pages []CrawlPage
		db.Where("job_id IN ? AND doc_id <> 0", jobIDs).Find(&pages)
		docIDs := make([]uint, 0, len(pages))
		for i := range pages {
			docIDs = append(docIDs, pages[i].DocID)
		}
		var docs []models.Document
		db.Where("id IN ?", docIDs).Find(&docs)
		slugByID := make(map[uint]string, len(docs))
		for i := range docs {
			slugByID[docs[i].ID] = docs[i].Slug
		}
		for i := range pages {
			if s := slugByID[pages[i].DocID]; s != "" && pages[i].URL != "" {
				urlToSlug[pages[i].URL] = s
			}
		}
	}
	changed := 0
	if len(urlToSlug) > 0 {
		var all []models.Document
		db.Where("book_id = ?", book.ID).Find(&all)
		for i := range all {
			d := &all[i]
			if nc := rewriteInternalLinks(d.Content, urlToSlug, book.Slug); nc != d.Content {
				oldDoc := *d
				if db.Model(&models.Document{}).Where("id = ?", d.ID).Update("content", nc).Error == nil {
					after := oldDoc
					after.Content = nc
					cc.core.NotifyIndexableDocumentChange(book, book, &oldDoc, &after)
					changed++
				}
			}
		}
	}
	if changed > 0 {
		cc.core.RecordAudit(c, "book.cleanup_internal_links", "book", strings.TrimSpace(book.Slug), book.Title, map[string]any{"changed": changed})
	}
	cc.core.OK(c, gin.H{"changed": changed})
}

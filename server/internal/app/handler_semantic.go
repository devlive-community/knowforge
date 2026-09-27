package app

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 语义搜索与相关推荐：由插件登记的语义检索能力（plugincore.RegisterSemanticProvider）提供候选，
// 核心按当前用户的书籍/章节可见性与内容门禁再次过滤后返回；没有可用能力时返回空列表（前端回退到其他方式）。

type semanticDocResult struct {
	searchDocResult
	Heading string  `json:"heading"`
	Anchor  string  `json:"anchor"`
	Score   float64 `json:"score"`
}

type relatedDoc struct {
	ID        uint   `json:"id"`
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	BookID    uint   `json:"book_id"`
	BookSlug  string `json:"book_slug"`
	BookTitle string `json:"book_title"`
}

// readablePublishedDoc 章节已发布、书籍对 u 可见且全文可读（经内容门禁）时返回章节与书籍。
func (a *App) readablePublishedDoc(u *models.User, docID uint, books map[uint]*models.Book, needFullText bool) (*models.Document, *models.Book) {
	var doc models.Document
	if a.DB.First(&doc, docID).Error != nil || doc.Status != "published" {
		return nil, nil
	}
	book, cached := books[doc.BookID]
	if !cached {
		var b models.Book
		if a.DB.First(&b, doc.BookID).Error == nil && a.canReadBook(u, &b) {
			book = &b
		}
		books[doc.BookID] = book
	}
	if book == nil {
		return nil, nil
	}
	if needFullText && !a.contentAccess(u, book, &doc).Allowed {
		return nil, nil
	}
	return &doc, book
}

// SemanticSearch GET /search/semantic?q=&book= 语义相关的章节小节（与关键词搜索互补，前端单独加载）：
// {available, items[]{id, book_id, book_slug, book_title, doc_slug, title, heading, anchor, excerpt, score}}。
func (a *App) SemanticSearch(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if utf8.RuneCountInString(q) > 100 {
		fail(c, http.StatusBadRequest, "搜索关键词最多 100 个字符")
		return
	}
	provider, available := plugincore.ActiveSemanticProvider(a)
	items := []semanticDocResult{}
	u := currentUser(c)
	if available && provider.Allowed != nil && !provider.Allowed(a, u) {
		available = false
	}
	if !available || q == "" {
		ok(c, gin.H{"available": available, "items": items})
		return
	}
	var bookID uint
	if slug := strings.TrimSpace(c.Query("book")); slug != "" {
		var book models.Book
		if a.DB.Where("slug = ?", slug).First(&book).Error != nil || !a.canReadBook(u, &book) {
			fail(c, http.StatusNotFound, "书籍不存在")
			return
		}
		bookID = book.ID
	}
	hits, err := provider.Search(c.Request.Context(), a, u, q, bookID, 10)
	if err != nil {
		ok(c, gin.H{"available": true, "items": items, "error": "语义搜索暂时不可用"})
		return
	}
	books := map[uint]*models.Book{}
	seen := map[uint]bool{}
	for _, h := range hits {
		if seen[h.DocID] {
			continue
		}
		doc, book := a.readablePublishedDoc(u, h.DocID, books, true)
		if doc == nil {
			continue
		}
		seen[h.DocID] = true
		items = append(items, semanticDocResult{
			searchDocResult: searchDocResult{ID: doc.ID, BookID: book.ID, BookSlug: book.Slug, BookTitle: book.Title, DocSlug: doc.Slug, Title: doc.Title,
				Excerpt: searchExcerpt(h.Snippet, q, 160), UpdatedAt: doc.UpdatedAt},
			Heading: h.Heading, Anchor: h.Anchor, Score: h.Score,
		})
	}
	ok(c, gin.H{"available": true, "items": items})
}

func relatedLimit(c *gin.Context, def int) int {
	n := atoiDefault(c.Query("limit"), def)
	if n < 1 || n > 12 {
		return def
	}
	return n
}

// RelatedBooks GET /books/:id/related?limit= 内容相近的其他书籍（语义），没有可用能力时为空。
func (a *App) RelatedBooks(c *gin.Context) {
	book, status := a.findBook(c)
	u := currentUser(c)
	if book == nil || !a.canReadBook(u, book) {
		if book != nil {
			status = http.StatusNotFound
		}
		fail(c, status, "书籍不存在")
		return
	}
	items := []models.Book{}
	provider, available := plugincore.ActiveSemanticProvider(a)
	if available && provider.RelatedBooks != nil {
		for _, id := range provider.RelatedBooks(c.Request.Context(), a, u, book, relatedLimit(c, 3)) {
			var b models.Book
			if id != book.ID && a.PreloadBookUser().First(&b, id).Error == nil && a.canReadBook(u, &b) && bookVisible(&b) {
				items = append(items, b)
			}
		}
	}
	a.attachChapterCounts(items)
	a.decorateBookList(items)
	ok(c, gin.H{"items": items})
}

// RelatedDocuments GET /documents/:id/related?limit= 内容相近的其他已发布章节（语义），没有可用能力时为空。
func (a *App) RelatedDocuments(c *gin.Context) {
	doc, book, status := a.findDocument(c)
	u := currentUser(c)
	if doc == nil || !a.canReadDocument(u, doc, book) {
		if doc != nil {
			status = http.StatusNotFound
		}
		fail(c, status, "章节不存在")
		return
	}
	items := []relatedDoc{}
	provider, available := plugincore.ActiveSemanticProvider(a)
	if available && provider.RelatedDocs != nil {
		books := map[uint]*models.Book{}
		for _, id := range provider.RelatedDocs(c.Request.Context(), a, u, doc, relatedLimit(c, 5)) {
			if id == doc.ID {
				continue
			}
			// 只展示标题，付费章节同样可以推荐（进入后按门禁显示试读）
			if d, b := a.readablePublishedDoc(u, id, books, false); d != nil {
				items = append(items, relatedDoc{ID: d.ID, Title: d.Title, Slug: d.Slug, BookID: b.ID, BookSlug: b.Slug, BookTitle: b.Title})
			}
		}
	}
	ok(c, gin.H{"items": items})
}

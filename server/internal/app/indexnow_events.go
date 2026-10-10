package app

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"time"

	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

func (a *App) indexableSiteBase() string {
	raw := strings.TrimSpace(a.getSetting("site_url"))
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return strings.TrimRight(raw, "/")
}

func indexableBook(book *models.Book) bool {
	return book != nil && book.DeletedAt.Valid == false && book.IsPublic && !book.LoginRequired && isPubliclyReadableBookStatus(book.Status) && book.Slug != ""
}

func escapedPathSegment(value string) string {
	return url.PathEscape(value)
}

func (a *App) indexableSiteURLs() []string {
	books := []models.Book{}
	if err := a.DB.Where("is_public = ? AND login_required = ? AND status IN ?", true, false, publiclyReadableBookStatuses).Order("id ASC").Find(&books).Error; err != nil {
		return nil
	}
	urls := []string{}
	for i := range books {
		urls = append(urls, a.indexableBookURLs(&books[i])...)
	}
	return urls
}

func (a *App) indexableVariantGroupURLs(groups ...string) []string {
	seenGroups := map[string]struct{}{}
	validGroups := make([]string, 0, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if _, exists := seenGroups[group]; exists {
			continue
		}
		seenGroups[group] = struct{}{}
		validGroups = append(validGroups, group)
	}
	if len(validGroups) == 0 {
		return nil
	}
	books := []models.Book{}
	if err := a.DB.Where("is_public = ? AND login_required = ? AND status IN ? AND (trans_group IN ? OR version_group IN ?)",
		true, false, publiclyReadableBookStatuses, validGroups, validGroups).Order("id ASC").Find(&books).Error; err != nil {
		return nil
	}
	urls := []string{}
	for i := range books {
		urls = append(urls, a.indexableBookURLs(&books[i])...)
	}
	return urls
}

func (a *App) indexableBookURLs(book *models.Book) []string {
	base := a.indexableSiteBase()
	if base == "" || !indexableBook(book) {
		return nil
	}
	bookSlug := escapedPathSegment(book.Slug)
	urls := []string{base + "/book/detail/" + bookSlug}
	docs := []models.Document{}
	if err := a.DB.Where("book_id = ? AND status = ? AND slug <> ?", book.ID, "published", "").
		Order("id ASC").Find(&docs).Error; err != nil {
		return urls
	}
	for _, doc := range docs {
		urls = append(urls, base+"/book/reader/"+bookSlug+"/"+escapedPathSegment(doc.Slug))
	}
	return urls
}

// notifyBookVisited / notifyDocumentVisited 公开可索引的书籍详情、章节被访问（SSR 取数即算一次访问，含搜索引擎抓取）。
func (a *App) notifyBookVisited(book *models.Book) {
	base := a.indexableSiteBase()
	if base == "" || !indexableBook(book) {
		return
	}
	plugincore.FireIndexableURLVisited(a, "book", base+"/book/detail/"+escapedPathSegment(book.Slug))
}

func (a *App) notifyDocumentVisited(book *models.Book, doc *models.Document) {
	plugincore.FireIndexableURLVisited(a, "chapter", a.indexableDocumentURL(book, doc))
}

func (a *App) indexableDocumentSubtreeURLs(book *models.Book, root *models.Document) []string {
	if !indexableBook(book) || root == nil {
		return nil
	}
	ids := append([]uint{root.ID}, subtreeDocIDs(a.DB, root.ID)...)
	docs := []models.Document{}
	if err := a.DB.Where("id IN ? AND status = ?", ids, "published").Find(&docs).Error; err != nil {
		return nil
	}
	urls := make([]string, 0, len(docs))
	for i := range docs {
		if target := a.indexableDocumentURL(book, &docs[i]); target != "" {
			urls = append(urls, target)
		}
	}
	return urls
}

func (a *App) indexableDocumentURL(book *models.Book, doc *models.Document) string {
	base := a.indexableSiteBase()
	if base == "" || !indexableBook(book) || doc == nil || doc.Status != "published" || doc.DeletedAt.Valid || doc.Slug == "" {
		return ""
	}
	return base + "/book/reader/" + escapedPathSegment(book.Slug) + "/" + escapedPathSegment(doc.Slug)
}

func (a *App) emitIndexableURLs(urls ...string) {
	if len(urls) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(urls))
	unique := make([]string, 0, len(urls))
	for _, raw := range urls {
		if raw == "" {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		unique = append(unique, raw)
	}
	if len(unique) == 0 {
		return
	}
	plugincore.FirePublicURLsChanged(a, unique)
	a.enqueueSitemapGenerateSoon()
}

func (a *App) notifyBookIndexableChange(before, after *models.Book) {
	was, now := indexableBook(before), indexableBook(after)
	if !was && !now {
		return
	}
	if !was && now {
		a.emitIndexableURLs(a.indexableBookURLs(after)...)
		return
	}
	if was && !now {
		a.emitIndexableURLs(a.indexableBookURLs(before)...)
		return
	}
	if before.Slug != after.Slug {
		urls := a.indexableBookURLs(before)
		urls = append(urls, a.indexableBookURLs(after)...)
		a.emitIndexableURLs(urls...)
		return
	}
	variantChanged := before.TransGroup != after.TransGroup || before.VersionGroup != after.VersionGroup ||
		before.VersionIsLatest != after.VersionIsLatest || before.Version != after.Version || before.Language != after.Language
	if variantChanged {
		a.emitIndexableURLs(a.indexableVariantGroupURLs(before.TransGroup, after.TransGroup, before.VersionGroup, after.VersionGroup)...)
	}
	readerMetadataChanged := before.Title != after.Title || before.Description != after.Description || before.CoverImage != after.CoverImage ||
		before.Status != after.Status || before.ChapterPrefix != after.ChapterPrefix || variantChanged || !reflect.DeepEqual(before.ExtraInfo, after.ExtraInfo)
	if readerMetadataChanged {
		a.emitIndexableURLs(a.indexableBookURLs(after)...)
	}
}

func (a *App) notifyDocumentIndexableChange(oldBook, newBook *models.Book, before, after *models.Document) {
	if oldBook != nil && newBook != nil && indexableBook(oldBook) != indexableBook(newBook) {
		if indexableBook(newBook) {
			a.emitIndexableURLs(a.indexableBookURLs(newBook)...)
		} else {
			a.emitIndexableURLs(a.indexableBookURLs(oldBook)...)
		}
		return
	}
	oldURL := a.indexableDocumentURL(oldBook, before)
	newURL := a.indexableDocumentURL(newBook, after)
	if oldURL == "" && newURL == "" {
		return
	}
	if oldURL == "" {
		a.emitIndexableURLs(newURL)
		return
	}
	if newURL == "" {
		a.emitIndexableURLs(oldURL)
		return
	}
	if oldURL != newURL {
		a.emitIndexableURLs(oldURL, newURL)
		return
	}
	if before.ParentID == nil && after.ParentID != nil || before.ParentID != nil && after.ParentID == nil ||
		before.ParentID != nil && after.ParentID != nil && *before.ParentID != *after.ParentID || before.SortOrder != after.SortOrder {
		a.emitIndexableURLs(a.indexableBookURLs(newBook)...)
		return
	}
	if before.Title != after.Title || before.Content != after.Content || before.ExternalURL != after.ExternalURL ||
		!reflect.DeepEqual(before.ExternalNewTab, after.ExternalNewTab) || !reflect.DeepEqual(before.AllowComments, after.AllowComments) {
		a.emitIndexableURLs(newURL)
	}
}

func (a *App) enqueueSitemapGenerateSoon() {
	now := currentTime().UTC().Format(time.RFC3339Nano)
	_ = a.setSetting("sitemap_request_revision", now, "最近一次公开页面变更对应的 Sitemap 刷新标记")
	queue := a.jobQueue()
	if queue == nil {
		return
	}
	_, created, err := queue.EnqueueIfDue(context.Background(), sitemapJobType, struct{}{}, 3, 30*time.Second)
	if err != nil || created {
		return
	}
	// A running job will compare revisions when it completes. If the only
	// reason for suppression was a recent successful build, queue a fresh one.
	var active int64
	a.DB.Model(&models.BackgroundJob{}).
		Where("type = ? AND status IN ?", sitemapJobType, []string{jobqueue.StatusPending, jobqueue.StatusRunning, jobqueue.StatusRetrying}).
		Count(&active)
	if active == 0 && a.getSetting("sitemap_request_revision") != a.getSetting("sitemap_built_revision") {
		_, _ = queue.Enqueue(context.Background(), sitemapJobType, struct{}{}, 3)
	}
}

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

func TestIndexableBookURLsMatchPublicSitemapScope(t *testing.T) {
	a, owner, db := newContentImportTestApp(t)
	if err := a.setSetting("site_url", "https://books.example/", "test site URL"); err != nil {
		t.Fatal(err)
	}
	book := models.Book{
		Title: "Public book", Slug: "public-book", UserID: owner.ID,
		Status: "in_progress", IsPublic: true, LoginRequired: false,
	}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	published := models.Document{BookID: book.ID, UserID: owner.ID, Title: "Public chapter", Slug: "chapter-one", Status: "published"}
	draft := models.Document{BookID: book.ID, UserID: owner.ID, Title: "Private draft", Slug: "draft", Status: "draft"}
	if err := db.Create(&published).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}

	got := a.indexableBookURLs(&book)
	want := []string{
		"https://books.example/book/detail/public-book",
		"https://books.example/book/reader/public-book/chapter-one",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("indexable URLs mismatch: got %v, want %v", got, want)
	}

	book.LoginRequired = true
	if got := a.indexableBookURLs(&book); len(got) != 0 {
		t.Fatalf("login-only book must not be indexable: %v", got)
	}
	book.LoginRequired = false
	book.Status = "archived"
	if got := a.indexableBookURLs(&book); len(got) != 0 {
		t.Fatalf("archived book must not be indexable: %v", got)
	}
	book.Status = "in_progress"
	book.IsPublic = false
	if got := a.indexableBookURLs(&book); len(got) != 0 {
		t.Fatalf("private book must not be indexable: %v", got)
	}
}

func TestIndexableVariantGroupURLsIncludeVisiblePeerBooks(t *testing.T) {
	a, owner, db := newContentImportTestApp(t)
	if err := a.setSetting("site_url", "https://books.example", "test site URL"); err != nil {
		t.Fatal(err)
	}
	books := []models.Book{
		{Title: "English", Slug: "english", UserID: owner.ID, Status: "in_progress", IsPublic: true, TransGroup: "series-a"},
		{Title: "French", Slug: "french", UserID: owner.ID, Status: "completed", IsPublic: true, TransGroup: "series-a"},
		{Title: "Private", Slug: "private", UserID: owner.ID, Status: "in_progress", IsPublic: false, TransGroup: "series-a"},
	}
	for i := range books {
		if err := db.Create(&books[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	doc := models.Document{BookID: books[0].ID, UserID: owner.ID, Title: "Chapter", Slug: "chapter", Status: "published"}
	if err := db.Create(&doc).Error; err != nil {
		t.Fatal(err)
	}
	got := a.indexableVariantGroupURLs("series-a", "series-a")
	want := []string{
		"https://books.example/book/detail/english",
		"https://books.example/book/reader/english/chapter",
		"https://books.example/book/detail/french",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected variant-group URLs: got %v, want %v", got, want)
	}
}

func TestIndexableDocumentURLRequiresPublicBookAndPublishedChapter(t *testing.T) {
	a, _, _ := newContentImportTestApp(t)
	if err := a.setSetting("site_url", "https://books.example", "test site URL"); err != nil {
		t.Fatal(err)
	}
	book := &models.Book{Slug: "public-book", Status: "completed", IsPublic: true}
	doc := &models.Document{Slug: "chapter-one", Status: "published"}
	if got := a.indexableDocumentURL(book, doc); got != "https://books.example/book/reader/public-book/chapter-one" {
		t.Fatalf("unexpected public document URL: %q", got)
	}
	doc.Status = "draft"
	if got := a.indexableDocumentURL(book, doc); got != "" {
		t.Fatalf("draft chapter must not be indexable: %q", got)
	}
	doc.Status = "published"
	book.LoginRequired = true
	if got := a.indexableDocumentURL(book, doc); got != "" {
		t.Fatalf("login-only book chapter must not be indexable: %q", got)
	}
	book.LoginRequired = false
	doc.DeletedAt.Valid = true
	if got := a.indexableDocumentURL(book, doc); got != "" {
		t.Fatalf("deleted chapter must not be indexable: %q", got)
	}
}

func TestPublicURLChangeRequestsSitemapRefresh(t *testing.T) {
	a, _, _ := newContentImportTestApp(t)
	a.emitIndexableURLs("https://books.example/book/detail/public-book")
	if got := a.getSetting("sitemap_request_revision"); got == "" {
		t.Fatal("public URL change did not request a sitemap refresh")
	}
}

func TestPublicReaderMetadataChangesRequestSitemapRefresh(t *testing.T) {
	a, owner, db := newContentImportTestApp(t)
	if err := a.setSetting("site_url", "https://books.example", "test site URL"); err != nil {
		t.Fatal(err)
	}
	book := models.Book{Title: "Book", Slug: "book", UserID: owner.ID, Status: "in_progress", IsPublic: true}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	doc := models.Document{BookID: book.ID, UserID: owner.ID, Title: "Chapter", Slug: "chapter", Status: "published"}
	if err := db.Create(&doc).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.setSetting("sitemap_request_revision", "before", "test revision"); err != nil {
		t.Fatal(err)
	}
	updatedBook := book
	updatedBook.ChapterPrefix = "Part "
	a.notifyBookIndexableChange(&book, &updatedBook)
	if got := a.getSetting("sitemap_request_revision"); got == "" || got == "before" {
		t.Fatal("book reader metadata changes must request Sitemap refresh")
	}
	if err := a.setSetting("sitemap_request_revision", "before-sort", "test revision"); err != nil {
		t.Fatal(err)
	}
	movedDoc := doc
	parentID := uint(42)
	movedDoc.ParentID = &parentID
	a.notifyDocumentIndexableChange(&book, &book, &doc, &movedDoc)
	if got := a.getSetting("sitemap_request_revision"); got == "" || got == "before-sort" {
		t.Fatal("published chapter movement must request Sitemap refresh")
	}
}

func TestSitemapWithoutSiteURLMarksCurrentRevisionHandled(t *testing.T) {
	a, _, _ := newContentImportTestApp(t)
	if err := a.setSetting("sitemap_request_revision", "revision-1", "test sitemap revision"); err != nil {
		t.Fatal(err)
	}
	if err := a.runSitemapGenerate(context.Background(), json.RawMessage("{}")); err != nil {
		t.Fatal(err)
	}
	if got := a.getSetting("sitemap_built_revision"); got != "revision-1" {
		t.Fatalf("sitemap revision was not marked handled without site URL: %q", got)
	}
}

// 公开可索引的书籍详情与已发布章节被读取时通知「访问」订阅方（如 IndexNow 访问时推送）；私有书籍不通知。
func TestIndexableVisitsAreAnnounced(t *testing.T) {
	a, owner, db := newContentImportTestApp(t)
	a.Config = &config.Config{Installed: true, Secret: "indexnow-visit-test"}
	if err := a.setSetting("site_url", "https://books.example", "test site URL"); err != nil {
		t.Fatal(err)
	}
	var visits []string
	plugincore.OnIndexableURLVisited(func(_ plugincore.Core, kind, url string) {
		if strings.HasPrefix(url, "https://books.example/") {
			visits = append(visits, kind+" "+url)
		}
	})
	public := models.Book{Title: "Visit", Slug: "visit-book", UserID: owner.ID, Status: "in_progress", IsPublic: true}
	private := models.Book{Title: "Hidden", Slug: "hidden-book", UserID: owner.ID, Status: "in_progress"}
	db.Create(&public)
	db.Create(&private)
	db.Create(&models.Document{BookID: public.ID, UserID: owner.ID, Title: "One", Slug: "one", Status: "published"})
	router := a.Router()
	get := func(path string) int {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code
	}
	if get("/api/v1/books/slug/visit-book") != http.StatusOK || get(fmt.Sprintf("/api/v1/books/%d/documents/slug/one", public.ID)) != http.StatusOK {
		t.Fatal("public book and chapter should be readable")
	}
	get("/api/v1/books/slug/hidden-book")
	want := []string{"book https://books.example/book/detail/visit-book", "chapter https://books.example/book/reader/visit-book/one"}
	if !reflect.DeepEqual(visits, want) {
		t.Fatalf("visits: %v", visits)
	}
}

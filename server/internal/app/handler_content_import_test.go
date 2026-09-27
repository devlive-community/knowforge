package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/database"
	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newContentImportTestApp(t *testing.T) (*App, *models.User, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := database.Open(testdb.Config(t))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取测试数据库连接失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := models.All(db); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	app := &App{DB: db}
	app.syncPluginState() // 为默认启用的插件（标签）建表并注册权限，模拟 New() 启动
	user := &models.User{Username: "import-owner", Email: "import-owner@test.local", IsActive: true}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	return app, user, db
}

func contentImportRouter(app *App, user *models.User) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user", user)
		c.Next()
	})
	router.POST("/import/pdf", app.ImportPDFBook)
	router.POST("/books/:id/import/pdf", app.ReimportPDFBook)
	router.GET("/tasks/:id", app.GetBackgroundJob)
	return router
}

func TestImportPDFBookRunsAsOwnedBackgroundTask(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("KNOWFORGE_DATA", dataDir)
	app, owner, db := newContentImportTestApp(t)
	app.Config = &config.Config{Secret: "pdf-background-task-secret"}
	if err := app.configureJobQueue(); err != nil {
		t.Fatal(err)
	}
	app.PDFExtractor = func(path string) (pdfExtractResult, error) {
		if !strings.Contains(filepath.ToSlash(path), "/import-jobs/pdf-") {
			t.Fatalf("PDF source was not persisted in private import directory: %s", path)
		}
		return pdfExtractResult{Markdown: "## 第一章 后台导入\n\n这是后台任务解析得到的 Markdown 正文内容。", Pages: 2}, nil
	}
	router := contentImportRouter(app, owner)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, pdfImportRequest(t, http.MethodPost, "/import/pdf", ""))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("PDF async import should return 202: %d %v", recorder.Code, decodeImportResponse(t, recorder))
	}
	payload := decodeImportResponse(t, recorder)
	jobData := payload["data"].(map[string]any)["task"].(map[string]any)
	jobID := uint(jobData["id"].(float64))
	var count int64
	if err := db.Model(&models.Book{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("request must return before parsing creates a book: count=%d err=%v", count, err)
	}
	if ran, runErr := app.Jobs.RunOnce(context.Background()); !ran || runErr != nil {
		t.Fatalf("background PDF import failed: ran=%v err=%v", ran, runErr)
	}
	request := httptest.NewRequest(http.MethodGet, "/tasks/"+strconv.FormatUint(uint64(jobID), 10), nil)
	taskResponse := httptest.NewRecorder()
	router.ServeHTTP(taskResponse, request)
	if taskResponse.Code != http.StatusOK {
		t.Fatalf("owner cannot read task result: %d %v", taskResponse.Code, decodeImportResponse(t, taskResponse))
	}
	encoded := taskResponse.Body.String()
	if !strings.Contains(encoded, "修订版") || strings.Contains(encoded, "source_path") || strings.Contains(encoded, "payload") {
		t.Fatalf("task result or payload exposure is incorrect: %s", encoded)
	}
	var job models.BackgroundJob
	if err := db.First(&job, jobID).Error; err != nil {
		t.Fatal(err)
	}
	if job.OwnerID != owner.ID || job.Status != jobqueue.StatusSucceeded {
		t.Fatalf("unexpected completed import task: %+v", job)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "import-jobs")); err != nil {
		t.Fatalf("import directory missing: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "import-jobs"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("successful task must remove source file: entries=%v err=%v", entries, err)
	}
}

func pdfImportRequest(t *testing.T, method, target, mode string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "修订版.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("%PDF-test-content"))
	if mode != "" {
		_ = writer.WriteField("mode", mode)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func decodeImportResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, recorder.Body.String())
	}
	return payload
}

func TestImportPDFBookCreatesPrivateDraftAndRevisions(t *testing.T) {
	app, owner, db := newContentImportTestApp(t)
	app.PDFExtractor = func(path string) (pdfExtractResult, error) {
		return pdfExtractResult{
			Markdown: "这是导入文件的前言，包含足够多的文字用于解析。\n\n第一段介绍。\n\n## 第一章 起步\n\n这是第一章的正文内容。\n\n## 第二章 深入\n\n这是第二章的正文内容。",
			Pages:    12,
		}, nil
	}
	router := contentImportRouter(app, owner)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "知识手册.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("%PDF-test-content"))
	_ = writer.WriteField("title", "导入测试书")
	_ = writer.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/import/pdf", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PDF 导入失败: %d %v", recorder.Code, decodeImportResponse(t, recorder))
	}

	var book models.Book
	if err := db.Where("user_id = ?", owner.ID).First(&book).Error; err != nil {
		t.Fatalf("未创建书籍: %v", err)
	}
	if book.Title != "导入测试书" || book.Status != "draft" || book.IsPublic {
		t.Fatalf("导入书籍必须是私有草稿: %+v", book)
	}
	var docs []models.Document
	if err := db.Where("book_id = ?", book.ID).Order("sort_order ASC").Find(&docs).Error; err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 || docs[1].Title != "第一章 起步" || docs[2].Title != "第二章 深入" {
		t.Fatalf("PDF 章节拆分错误: %+v", docs)
	}
	var revisionCount int64
	if err := db.Model(&models.DocumentRevision{}).Where("book_id = ? AND reason = ?", book.ID, "create").Count(&revisionCount).Error; err != nil {
		t.Fatal(err)
	}
	if revisionCount != int64(len(docs)) {
		t.Fatalf("每个导入章节都应有初始版本: got=%d want=%d", revisionCount, len(docs))
	}
}

func TestReimportPDFBookAppendsDraftChapters(t *testing.T) {
	app, owner, db := newContentImportTestApp(t)
	book := models.Book{Title: "现有书籍", Slug: "existing-book", UserID: owner.ID, Status: "published", IsPublic: true}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	allowComments := true
	existing := models.Document{
		BookID: book.ID, UserID: owner.ID, Title: "原章节", Slug: "existing", Content: "原内容",
		SortOrder: 7, Status: "published", AllowComments: &allowComments,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}
	app.PDFExtractor = func(string) (pdfExtractResult, error) {
		return pdfExtractResult{Markdown: "## 第一章 修订\n\n这是重新导入后的第一章内容。\n\n## 第二章 新增\n\n这是重新导入后的第二章内容。", Pages: 6}, nil
	}

	recorder := httptest.NewRecorder()
	contentImportRouter(app, owner).ServeHTTP(recorder,
		pdfImportRequest(t, http.MethodPost, "/books/"+strconv.FormatUint(uint64(book.ID), 10)+"/import/pdf", "append"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("追加导入失败: %d %v", recorder.Code, decodeImportResponse(t, recorder))
	}
	var docs []models.Document
	if err := db.Where("book_id = ?", book.ID).Order("sort_order ASC").Find(&docs).Error; err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 || docs[0].ID != existing.ID || docs[1].SortOrder != 8 || docs[2].SortOrder != 9 {
		t.Fatalf("追加导入不应覆盖旧章节且应接续排序: %+v", docs)
	}
	if docs[1].Status != "draft" || docs[2].Status != "draft" {
		t.Fatalf("追加章节必须保持草稿状态: %+v", docs)
	}
	if err := db.First(&book, book.ID).Error; err != nil {
		t.Fatal(err)
	}
	if book.Status != "published" || !book.IsPublic {
		t.Fatalf("追加导入不应改变书籍发布状态: %+v", book)
	}
}

func TestReimportPDFBookReplacesChaptersAndRelatedState(t *testing.T) {
	app, owner, db := newContentImportTestApp(t)
	book := models.Book{Title: "错误导入书籍", Slug: "broken-import", UserID: owner.ID, Status: "published", IsPublic: true}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	allowComments := true
	oldDoc := models.Document{BookID: book.ID, UserID: owner.ID, Title: "错误章节", Slug: "broken", Content: "错误内容", Status: "published", AllowComments: &allowComments}
	if err := db.Create(&oldDoc).Error; err != nil {
		t.Fatal(err)
	}
	oldRevision := newDocumentRevision(&oldDoc, owner.ID, "create")
	for _, record := range []any{
		&oldRevision,
		&models.Comment{DocumentID: oldDoc.ID, UserID: owner.ID, Content: "旧评论", Status: "published"},
		&models.ReadChapter{UserID: owner.ID, BookID: book.ID, DocID: oldDoc.ID},
		&models.ReadingProgress{UserID: owner.ID, BookID: book.ID, DocID: oldDoc.ID, DocSlug: oldDoc.Slug, DocTitle: oldDoc.Title},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	app.PDFExtractor = func(string) (pdfExtractResult, error) {
		return pdfExtractResult{Markdown: "## 第一章 正确内容\n\n这是覆盖后生成的正确 Markdown 正文。", Pages: 3}, nil
	}

	recorder := httptest.NewRecorder()
	contentImportRouter(app, owner).ServeHTTP(recorder,
		pdfImportRequest(t, http.MethodPost, "/books/"+strconv.FormatUint(uint64(book.ID), 10)+"/import/pdf", "replace"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("覆盖导入失败: %d %v", recorder.Code, decodeImportResponse(t, recorder))
	}
	var docs []models.Document
	if err := db.Where("book_id = ?", book.ID).Find(&docs).Error; err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID == oldDoc.ID || docs[0].Title != "第一章 正确内容" || docs[0].Status != "draft" {
		t.Fatalf("覆盖导入章节错误: %+v", docs)
	}
	checks := []struct {
		name  string
		model any
		query string
	}{
		{name: "旧评论", model: &models.Comment{}, query: "document_id = ?"},
		{name: "旧版本", model: &models.DocumentRevision{}, query: "document_id = ?"},
		{name: "旧阅读记录", model: &models.ReadChapter{}, query: "doc_id = ?"},
		{name: "旧阅读进度", model: &models.ReadingProgress{}, query: "doc_id = ?"},
	}
	for _, check := range checks {
		var count int64
		if err := db.Model(check.model).Where(check.query, oldDoc.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s 未随覆盖导入清理: %d", check.name, count)
		}
	}
	if err := db.First(&book, book.ID).Error; err != nil {
		t.Fatal(err)
	}
	if book.Status != "draft" || book.IsPublic {
		t.Fatalf("覆盖后书籍必须转为私有草稿: %+v", book)
	}
}

func TestReimportPDFBookHidesBookFromUnauthorizedUser(t *testing.T) {
	app, owner, db := newContentImportTestApp(t)
	book := models.Book{Title: "私有书籍", Slug: "private-reimport", UserID: owner.ID, Status: "draft"}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	viewer := &models.User{Username: "reimport-viewer", Email: "reimport-viewer@test.local", IsActive: true}
	if err := db.Create(viewer).Error; err != nil {
		t.Fatal(err)
	}
	extractorCalled := false
	app.PDFExtractor = func(string) (pdfExtractResult, error) {
		extractorCalled = true
		return pdfExtractResult{}, nil
	}
	recorder := httptest.NewRecorder()
	contentImportRouter(app, viewer).ServeHTTP(recorder,
		pdfImportRequest(t, http.MethodPost, "/books/"+strconv.FormatUint(uint64(book.ID), 10)+"/import/pdf", "replace"))
	if recorder.Code != http.StatusNotFound || extractorCalled {
		t.Fatalf("无权用户应得到 404 且不得触发解析: status=%d called=%v", recorder.Code, extractorCalled)
	}
}

func TestReimportPDFBookKeepsOldContentWhenParsingFails(t *testing.T) {
	app, owner, db := newContentImportTestApp(t)
	book := models.Book{Title: "待修复书籍", Slug: "failed-reimport", UserID: owner.ID, Status: "published", IsPublic: true}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	allowComments := true
	oldDoc := models.Document{BookID: book.ID, UserID: owner.ID, Title: "保留章节", Slug: "keep", Content: "必须保留的正文", Status: "published", AllowComments: &allowComments}
	if err := db.Create(&oldDoc).Error; err != nil {
		t.Fatal(err)
	}
	app.PDFExtractor = func(string) (pdfExtractResult, error) {
		return pdfExtractResult{}, errors.New("malformed PDF")
	}

	recorder := httptest.NewRecorder()
	contentImportRouter(app, owner).ServeHTTP(recorder,
		pdfImportRequest(t, http.MethodPost, "/books/"+strconv.FormatUint(uint64(book.ID), 10)+"/import/pdf", "replace"))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("解析失败状态码错误: %d %v", recorder.Code, decodeImportResponse(t, recorder))
	}
	var stored models.Document
	if err := db.First(&stored, oldDoc.ID).Error; err != nil || stored.Content != oldDoc.Content {
		t.Fatalf("解析失败不得改动旧章节: doc=%+v err=%v", stored, err)
	}
	if err := db.First(&book, book.ID).Error; err != nil {
		t.Fatal(err)
	}
	if book.Status != "published" || !book.IsPublic {
		t.Fatalf("解析失败不得改变书籍状态: %+v", book)
	}
}

func TestRenderPDFMarkdownPreservesDocumentStructure(t *testing.T) {
	lines := []pdfLayoutLine{
		{Text: "工程实践指南", FontSize: 24, Bold: true, Page: 1, X: 60, Y: 780, GapAfter: 30},
		{Text: "第一章 起步", FontSize: 18, Bold: true, Page: 1, X: 60, Y: 730, GapAfter: 24},
		{Text: "这是跨行的第一部分，", FontSize: 12, Page: 1, X: 60, Y: 690, GapAfter: 14},
		{Text: "应该合并为一个段落。", FontSize: 12, Page: 1, X: 60, Y: 676, GapAfter: 30},
		{Text: "• 准备环境", FontSize: 12, Page: 1, X: 60, Y: 630, GapAfter: 18},
		{Text: `fmt.Println("ok")`, Font: "Courier", FontSize: 11, Monospace: true, Page: 1, X: 60, Y: 600},
	}
	markdown := renderPDFMarkdown(lines)
	for _, expected := range []string{"# 工程实践指南", "## 第一章 起步", "这是跨行的第一部分，应该合并为一个段落。", "- 准备环境", "~~~\nfmt.Println"} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("PDF 结构未转换为 Markdown，缺少 %q:\n%s", expected, markdown)
		}
	}
}

func TestOrderPDFPageLinesReadsColumnsTopToBottom(t *testing.T) {
	lines := []pdfLayoutLine{
		{Text: "页面标题", X: 40, EndX: 560, Y: 780, Page: 1},
		{Text: "左一", X: 40, EndX: 220, Y: 720, Page: 1}, {Text: "右一", X: 340, EndX: 520, Y: 720, Page: 1},
		{Text: "左二", X: 40, EndX: 220, Y: 680, Page: 1}, {Text: "右二", X: 340, EndX: 520, Y: 680, Page: 1},
		{Text: "左三", X: 40, EndX: 220, Y: 640, Page: 1}, {Text: "右三", X: 340, EndX: 520, Y: 640, Page: 1},
	}
	ordered := orderPDFPageLines(lines)
	got := make([]string, 0, len(ordered))
	for _, line := range ordered {
		got = append(got, line.Text)
	}
	want := []string{"页面标题", "左一", "左二", "左三", "右一", "右二", "右三"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("双栏阅读顺序错误: got=%v want=%v", got, want)
	}
}

func TestSplitPDFChaptersUsesMarkdownHeadings(t *testing.T) {
	chapters := splitPDFChapters("# 使用手册\n\n手册前言内容。\n\n## 安装\n\n安装章节内容。\n\n## 配置\n\n配置章节内容。", "使用手册")
	if len(chapters) != 3 || chapters[0].Title != "使用手册" || chapters[1].Title != "安装" || chapters[2].Title != "配置" {
		t.Fatalf("Markdown 标题拆章错误: %+v", chapters)
	}
}

func TestSplitPDFChaptersChunksLongUnstructuredText(t *testing.T) {
	chapters := splitPDFChapters(strings.Repeat("内容片段 ", 5000), "长文档")
	if len(chapters) < 2 {
		t.Fatalf("超长无结构内容应被拆分: %d", len(chapters))
	}
	for _, chapter := range chapters {
		if len([]rune(chapter.Content)) > 12000 {
			t.Fatalf("单章超过导入上限: %d", len([]rune(chapter.Content)))
		}
	}
}

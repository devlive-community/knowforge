package app

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	contentImportMaxBytes = 64 << 20
	maxImportedChapters   = 200
)

type importedChapter struct {
	Title   string
	Content string
}

type pdfExtractResult struct {
	Markdown string
	Pages    int
}

type uploadedPDF struct {
	Filename string
	Result   pdfExtractResult
}

type storedPDF struct {
	Filename string
	Path     string
}

type pdfImportResult struct {
	Book        *models.Book `json:"book,omitempty"`
	BookID      uint         `json:"book_id"`
	Mode        string       `json:"mode,omitempty"`
	ImportedDoc int          `json:"imported_doc"`
	RemovedDoc  int64        `json:"removed_doc,omitempty"`
	Pages       int          `json:"pages"`
	Source      string       `json:"source"`
	Message     string       `json:"message"`
}

var pdfChapterHeading = regexp.MustCompile(`(?i)^(?:第[零一二三四五六七八九十百千万两0-9]{1,12}[章节篇部卷]|chapter\s+[0-9ivxlcdm]+)(?:[\s　:：.、-]+.+)?$`)

// ImportPDFBook POST /import/pdf 上传 PDF 并建立草稿书籍。
func (a *App) ImportPDFBook(c *gin.Context) {
	u := currentUser(c)
	if a.failBookQuota(c, u) {
		return
	}
	if queue := a.jobQueue(); queue != nil {
		stored, status, err := a.storeUploadedPDF(c)
		if err != nil {
			fail(c, status, err.Error())
			return
		}
		job, err := queue.EnqueueOwned(c.Request.Context(), u.ID, pdfImportJobType, pdfImportJob{
			UserID: u.ID, SourcePath: stored.Path, Filename: stored.Filename,
			Title: truncateText(strings.TrimSpace(c.PostForm("title")), 255),
		}, 3)
		if err != nil {
			_ = os.Remove(stored.Path)
			fail(c, http.StatusInternalServerError, "创建 PDF 导入任务失败")
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"task": publicBackgroundJob(job), "message": "PDF 已上传，正在后台解析并构建书籍"}})
		return
	}
	upload, status, err := a.extractUploadedPDF(c)
	if err != nil {
		fail(c, status, err.Error())
		return
	}

	name := upload.Filename
	bookTitle := strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name)))
	if custom := strings.TrimSpace(c.PostForm("title")); custom != "" {
		bookTitle = custom
	}
	bookTitle = truncateText(bookTitle, 255)
	chapters := splitPDFChapters(upload.Result.Markdown, bookTitle)
	book, err := a.createContentImportBook(u, bookTitle,
		fmt.Sprintf("从 PDF 导入，共 %d 页。", upload.Result.Pages), chapters)
	if err != nil {
		fail(c, http.StatusInternalServerError, "创建 PDF 书籍失败: "+err.Error())
		return
	}
	ok(c, gin.H{
		"book": book, "imported_doc": len(chapters), "source": "pdf",
		"message": fmt.Sprintf("导入完成：《%s》共 %d 个章节", book.Title, len(chapters)),
	})
}

// ReimportPDFBook POST /books/:id/import/pdf 将 PDF 章节追加到已有书籍，或原子覆盖全部旧章节。
func (a *App) ReimportPDFBook(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	// 重新导入会批量改写整本书，只允许书籍所有者或管理员操作。
	if !a.canManageBook(u, book) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(c.PostForm("mode")))
	if mode == "" {
		mode = "append"
	}
	if mode != "append" && mode != "replace" {
		fail(c, http.StatusBadRequest, "导入方式必须为 append 或 replace")
		return
	}
	if queue := a.jobQueue(); queue != nil {
		stored, uploadStatus, err := a.storeUploadedPDF(c)
		if err != nil {
			fail(c, uploadStatus, err.Error())
			return
		}
		job, err := queue.EnqueueOwned(c.Request.Context(), u.ID, pdfImportJobType, pdfImportJob{
			UserID: u.ID, BookID: book.ID, Mode: mode, SourcePath: stored.Path, Filename: stored.Filename,
		}, 3)
		if err != nil {
			_ = os.Remove(stored.Path)
			fail(c, http.StatusInternalServerError, "创建 PDF 重新导入任务失败")
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"task": publicBackgroundJob(job), "message": "PDF 已上传，正在后台重新解析章节"}})
		return
	}

	upload, uploadStatus, err := a.extractUploadedPDF(c)
	if err != nil {
		fail(c, uploadStatus, err.Error())
		return
	}
	result, err := a.applyPDFReimport(book, u, upload, mode)
	if err != nil {
		fail(c, http.StatusInternalServerError, "重新导入 PDF 失败: "+err.Error())
		return
	}
	ok(c, result)
}

func (a *App) applyPDFReimport(book *models.Book, u *models.User, upload uploadedPDF, mode string) (pdfImportResult, error) {
	var removedURLs []string
	if mode == "replace" {
		removedURLs = a.indexableBookURLs(book)
	}
	chapters := splitPDFChapters(upload.Result.Markdown, book.Title)
	if len(chapters) == 0 || len(chapters) > maxImportedChapters {
		return pdfImportResult{}, errors.New("没有可导入的章节或章节数量过多")
	}
	removed := int64(0)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		startOrder := 0
		usedSlugs := map[string]bool{}
		if mode == "replace" {
			var ids []uint
			if err := tx.Model(&models.Document{}).Where("book_id = ?", book.ID).Pluck("id", &ids).Error; err != nil {
				return err
			}
			removed = int64(len(ids))
			if len(ids) > 0 {
				if err := tx.Where("document_id IN ?", ids).Delete(&models.Comment{}).Error; err != nil {
					return err
				}
				if err := tx.Where("document_id IN ?", ids).Delete(&models.DocumentRevision{}).Error; err != nil {
					return err
				}
				if err := tx.Where("doc_id IN ?", ids).Delete(&models.ReadChapter{}).Error; err != nil {
					return err
				}
				if err := tx.Where("document_id IN ?", ids).Delete(&models.ReadingAnnotation{}).Error; err != nil {
					return err
				}
				if err := tx.Unscoped().Where("id IN ?", ids).Delete(&models.Document{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("book_id = ?", book.ID).Delete(&models.ReadingProgress{}).Error; err != nil {
				return err
			}
			// 新内容必须重新审阅后发布，避免覆盖后直接暴露未校对内容。
			if err := tx.Model(book).Updates(map[string]any{"status": "draft", "is_public": false}).Error; err != nil {
				return err
			}
		} else {
			var slugs []string
			if err := tx.Unscoped().Model(&models.Document{}).Where("book_id = ?", book.ID).Pluck("slug", &slugs).Error; err != nil {
				return err
			}
			for _, slug := range slugs {
				usedSlugs[slug] = true
			}
			if err := tx.Model(&models.Document{}).Where("book_id = ?", book.ID).
				Select("COALESCE(MAX(sort_order), -1)").Scan(&startOrder).Error; err != nil {
				return err
			}
			startOrder++
		}
		return createImportedChapters(tx, book.ID, book.UserID, u.ID, chapters, startOrder, usedSlugs)
	})
	if err != nil {
		return pdfImportResult{}, err
	}
	if mode == "replace" {
		a.emitIndexableURLs(removedURLs...)
	}

	message := fmt.Sprintf("已追加 %d 个草稿章节", len(chapters))
	if mode == "replace" {
		message = fmt.Sprintf("已覆盖 %d 个旧章节并导入 %d 个草稿章节，书籍已转为私有草稿", removed, len(chapters))
	}
	return pdfImportResult{BookID: book.ID, Mode: mode, RemovedDoc: removed, ImportedDoc: len(chapters), Pages: upload.Result.Pages, Source: "pdf", Message: message}, nil
}

func (a *App) extractUploadedPDF(c *gin.Context) (uploadedPDF, int, error) {
	stored, status, err := a.storeUploadedPDF(c)
	if err != nil {
		return uploadedPDF{}, status, err
	}
	defer os.Remove(stored.Path)
	result, err := a.extractStoredPDF(stored)
	if err != nil {
		return uploadedPDF{}, http.StatusUnprocessableEntity, err
	}
	return result, http.StatusOK, nil
}

func (a *App) storeUploadedPDF(c *gin.Context) (storedPDF, int, error) {
	header, err := c.FormFile("file")
	if err != nil {
		return storedPDF{}, http.StatusBadRequest, errors.New("请选择要导入的 PDF 文件")
	}
	if header.Size <= 0 || header.Size > contentImportMaxBytes {
		return storedPDF{}, http.StatusBadRequest, errors.New("PDF 文件必须小于 64MB")
	}
	name := strings.TrimSpace(header.Filename)
	if ext := strings.ToLower(filepath.Ext(name)); ext != ".pdf" {
		return storedPDF{}, http.StatusBadRequest, errors.New("仅支持 PDF 文件")
	}

	source, err := header.Open()
	if err != nil {
		return storedPDF{}, http.StatusBadRequest, errors.New("读取 PDF 文件失败")
	}
	defer source.Close()
	magic := make([]byte, 5)
	if _, err := io.ReadFull(source, magic); err != nil || string(magic) != "%PDF-" {
		return storedPDF{}, http.StatusBadRequest, errors.New("文件内容不是有效的 PDF")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return storedPDF{}, http.StatusBadRequest, errors.New("读取 PDF 文件失败")
	}

	dir := filepath.Join(config.DataDir(), "import-jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return storedPDF{}, http.StatusInternalServerError, errors.New("准备 PDF 导入目录失败")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return storedPDF{}, http.StatusInternalServerError, errors.New("保护 PDF 导入目录失败")
	}
	temp, err := os.CreateTemp(dir, "pdf-*.pdf")
	if err != nil {
		return storedPDF{}, http.StatusInternalServerError, errors.New("准备 PDF 解析失败")
	}
	tempPath := temp.Name()
	_ = temp.Chmod(0o600)
	if _, err := io.Copy(temp, io.LimitReader(source, contentImportMaxBytes+1)); err != nil {
		temp.Close()
		_ = os.Remove(tempPath)
		return storedPDF{}, http.StatusBadRequest, errors.New("读取 PDF 文件失败")
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return storedPDF{}, http.StatusInternalServerError, errors.New("准备 PDF 解析失败")
	}
	return storedPDF{Filename: name, Path: tempPath}, http.StatusOK, nil
}

func (a *App) extractStoredPDF(stored storedPDF) (uploadedPDF, error) {
	extractor := a.PDFExtractor
	if extractor == nil {
		extractor = extractPDFText
	}
	extracted, err := extractor(stored.Path)
	if err != nil {
		return uploadedPDF{}, errors.New("PDF 解析失败: " + err.Error())
	}
	if utf8.RuneCountInString(strings.TrimSpace(extracted.Markdown)) < 20 {
		return uploadedPDF{}, errors.New("PDF 中未识别到可导入文字；扫描版 PDF 需要先完成 OCR")
	}
	return uploadedPDF{Filename: stored.Filename, Result: extracted}, nil
}

func splitPDFChapters(text, bookTitle string) []importedChapter {
	text = normalizeExtractedText(text)
	lines := strings.Split(text, "\n")
	chapters := make([]importedChapter, 0)
	currentTitle := ""
	currentLines := make([]string, 0)
	flush := func() {
		body := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if body == "" {
			return
		}
		title := strings.TrimSpace(currentTitle)
		if title == "" {
			title = "前言"
		}
		chapters = append(chapters, importedChapter{Title: truncateText(title, 255), Content: body})
		currentLines = currentLines[:0]
	}
	foundHeading := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		headingText := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		headingLevel := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		markdownChapter := headingLevel > 0 && headingLevel <= 2 && strings.HasPrefix(trimmed[headingLevel:], " ")
		if headingText != "" && utf8.RuneCountInString(headingText) <= 100 && (pdfChapterHeading.MatchString(headingText) || markdownChapter) {
			flush()
			currentTitle = headingText
			foundHeading = true
			continue
		}
		currentLines = append(currentLines, line)
	}
	flush()
	if foundHeading && len(chapters) > 0 && len(chapters) <= maxImportedChapters {
		return chapters
	}
	return chunkImportedText(text, bookTitle)
}

func chunkImportedText(text, fallbackTitle string) []importedChapter {
	const maxRunes = 12000
	paragraphs := strings.Split(text, "\n\n")
	chunks := make([]string, 0)
	var current strings.Builder
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		paragraphRunes := []rune(paragraph)
		for len(paragraphRunes) > maxRunes {
			if current.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			cut := maxRunes
			for i := maxRunes; i > maxRunes*3/4; i-- {
				if paragraphRunes[i-1] == '\n' || unicode.IsSpace(paragraphRunes[i-1]) {
					cut = i
					break
				}
			}
			chunks = append(chunks, strings.TrimSpace(string(paragraphRunes[:cut])))
			paragraphRunes = paragraphRunes[cut:]
		}
		paragraph = strings.TrimSpace(string(paragraphRunes))
		if paragraph == "" {
			continue
		}
		if current.Len() > 0 && utf8.RuneCountInString(current.String())+utf8.RuneCountInString(paragraph) > maxRunes {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(paragraph)
	}
	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}
	if len(chunks) == 0 {
		return nil
	}
	chapters := make([]importedChapter, 0, len(chunks))
	for i, chunk := range chunks {
		title := fallbackTitle
		if len(chunks) > 1 {
			title = fmt.Sprintf("第 %d 部分", i+1)
		}
		chapters = append(chapters, importedChapter{Title: truncateText(title, 255), Content: chunk})
	}
	return chapters
}

func normalizeExtractedText(text string) string {
	text = strings.ReplaceAll(text, "\x00", "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRightFunc(lines[i], unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (a *App) createContentImportBook(u *models.User, title, description string, chapters []importedChapter) (models.Book, error) {
	title = truncateText(strings.TrimSpace(title), 255)
	if title == "" {
		title = "导入的书籍"
	}
	if len(chapters) == 0 || len(chapters) > maxImportedChapters {
		return models.Book{}, errors.New("没有可导入的章节或章节数量过多")
	}
	// 后台任务执行时再次校验书籍数量上限（排队期间可能已创建了其他书）
	if err := a.EnsureBookQuota(u); err != nil {
		return models.Book{}, err
	}
	book := models.Book{
		Title: title, Description: truncateText(strings.TrimSpace(description), 1000),
		Slug: a.uniqueBookSlug(slugify(title)), UserID: u.ID, Status: "draft",
		IsPublic: false, OrderCol: "created_at", OrderDir: "asc",
	}
	if book.Slug == "" {
		book.Slug = a.uniqueBookSlug(randomSlug("book"))
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&book).Error; err != nil {
			return err
		}
		return createImportedChapters(tx, book.ID, u.ID, u.ID, chapters, 0, map[string]bool{})
	})
	return book, err
}

func createImportedChapters(
	tx *gorm.DB,
	bookID, documentUserID, revisionUserID uint,
	chapters []importedChapter,
	startOrder int,
	usedSlugs map[string]bool,
) error {
	allowComments := true
	for index, chapter := range chapters {
		title := truncateText(strings.TrimSpace(chapter.Title), 255)
		if title == "" {
			title = fmt.Sprintf("第 %d 部分", index+1)
		}
		baseSlug := slugify(title)
		if baseSlug == "" {
			baseSlug = randomSlug("doc")
		}
		docSlug := baseSlug
		for suffix := 2; usedSlugs[docSlug]; suffix++ {
			docSlug = fmt.Sprintf("%s-%d", baseSlug, suffix)
		}
		usedSlugs[docSlug] = true
		doc := models.Document{
			BookID: bookID, UserID: documentUserID, Title: title,
			Slug: docSlug, Content: strings.TrimSpace(chapter.Content),
			SortOrder: startOrder + index, Status: "draft", AllowComments: &allowComments,
		}
		if err := tx.Create(&doc).Error; err != nil {
			return err
		}
		revision := newDocumentRevision(&doc, revisionUserID, "create")
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
	}
	return nil
}

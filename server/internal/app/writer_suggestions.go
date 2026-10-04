package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/models"
)

// —— 修改建议：「建议者」协作者在写作台的修改不直接保存，而是作为建议提交；能编辑的人在写作台逐处审阅，
// 采纳的部分合并到当前版本（由写作台合并后按冲突保护保存），再记录采纳 / 部分采纳 / 拒绝并通知建议者。——

const maxSuggestionBytes = 2 << 20

func init() {
	i18ntext.Register("notify.writerSuggestion.new", map[string]string{
		"zh-CN": "「{user}」对《{book}》的「{chapter}」提交了修改建议",
		"en":    `{user} suggested changes to "{chapter}" in "{book}"`,
	})
	for status, texts := range map[string][2]string{
		"accepted": {"「{user}」采纳了你对「{chapter}」的修改建议", `{user} accepted your suggestion for "{chapter}"`},
		"partial":  {"「{user}」部分采纳了你对「{chapter}」的修改建议", `{user} accepted part of your suggestion for "{chapter}"`},
		"rejected": {"「{user}」没有采纳你对「{chapter}」的修改建议", `{user} declined your suggestion for "{chapter}"`},
	} {
		i18ntext.Register("notify.writerSuggestion."+status, map[string]string{"zh-CN": texts[0], "en": texts[1]})
	}
}

type writerSuggestionView struct {
	models.WriterSuggestion
	User collabUser `json:"user"`
}

func (a *App) suggestionViews(list []models.WriterSuggestion) []writerSuggestionView {
	ids := []uint{}
	for _, s := range list {
		ids = append(ids, s.UserID)
	}
	users := map[uint]collabUser{}
	if len(ids) > 0 {
		var rows []models.User
		a.DB.Where("id IN ?", ids).Find(&rows)
		for i := range rows {
			users[rows[i].ID] = toCollabUser(&rows[i])
		}
	}
	out := make([]writerSuggestionView, 0, len(list))
	for _, s := range list {
		u, found := users[s.UserID]
		if !found {
			u = collabUser{ID: s.UserID, Username: "deleted", DisplayName: "-"}
		}
		out = append(out, writerSuggestionView{WriterSuggestion: s, User: u})
	}
	return out
}

func (a *App) publishSuggestions(c *gin.Context, bookID, docID uint) {
	writerHub.Publish(bookID, "suggestions", gin.H{"doc_id": docID, "origin": c.GetHeader(collabConnHeader)})
}

// bookEditors 书籍作者与「编辑」协作者（审阅建议的人）。
func (a *App) bookEditors(book *models.Book) []uint {
	ids := []uint{book.UserID}
	var collab []uint
	a.DB.Model(&models.BookCollaborator{}).Where("book_id = ? AND status = ? AND role = ?", book.ID, "accepted", "editor").Pluck("user_id", &collab)
	return append(ids, collab...)
}

// CreateWriterSuggestion POST /documents/:id/suggestions {base_title, base_content, title, content, note}
func (a *App) CreateWriterSuggestion(c *gin.Context) {
	doc, book, allowed := a.commentDocument(c)
	if !allowed {
		return
	}
	var req struct {
		BaseTitle   string `json:"base_title"`
		BaseContent string `json:"base_content"`
		Title       string `json:"title"`
		Content     string `json:"content"`
		Note        string `json:"note"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = req.BaseTitle
	}
	if req.Title == req.BaseTitle && req.Content == req.BaseContent {
		fail(c, http.StatusBadRequest, "没有修改，无需提交建议")
		return
	}
	if len(req.Content) > maxSuggestionBytes || len(req.BaseContent) > maxSuggestionBytes || utf8.RuneCountInString(req.Title) > 255 {
		fail(c, http.StatusBadRequest, "内容过长")
		return
	}
	u := currentUser(c)
	s := models.WriterSuggestion{
		BookID: book.ID, DocumentID: doc.ID, UserID: u.ID, BaseTitle: clipRunes(req.BaseTitle, 255), BaseContent: req.BaseContent,
		Title: req.Title, Content: req.Content, Note: clipRunes(strings.TrimSpace(req.Note), 500), Status: "pending",
	}
	if err := a.DB.Create(&s).Error; err != nil {
		fail(c, http.StatusInternalServerError, "提交失败")
		return
	}
	for _, uid := range a.bookEditors(book) {
		if uid == u.ID {
			continue
		}
		a.NotifyI18n(uid, "collaboration", "notify.writerSuggestion.new",
			map[string]string{"user": u.PublicName(), "book": book.Title, "chapter": doc.Title},
			map[string]any{"link": "/book/writer/" + book.Slug + "/" + doc.Slug + "?comments=suggestions", "book_id": book.ID, "document_id": doc.ID, "suggestion_id": s.ID})
	}
	a.publishSuggestions(c, book.ID, doc.ID)
	a.recordActivity(book.ID, doc.ID, u.ID, "suggestion.created", map[string]any{"title": doc.Title, "note": s.Note})
	ok(c, a.suggestionViews([]models.WriterSuggestion{s})[0])
}

// ListWriterSuggestions GET /documents/:id/suggestions?status=pending|decided 章节的修改建议（新的在前）。
func (a *App) ListWriterSuggestions(c *gin.Context) {
	doc, _, allowed := a.commentDocument(c)
	if !allowed {
		return
	}
	q := a.DB.Where("document_id = ?", doc.ID)
	if c.Query("status") == "decided" {
		q = q.Where("status <> ?", "pending").Limit(50)
	} else {
		q = q.Where("status = ?", "pending")
	}
	var list []models.WriterSuggestion
	q.Order("created_at DESC, id DESC").Find(&list)
	var pending int64
	a.DB.Model(&models.WriterSuggestion{}).Where("document_id = ? AND status = ?", doc.ID, "pending").Count(&pending)
	ok(c, gin.H{"items": a.suggestionViews(list), "pending": pending})
}

// WriterSuggestionCounts GET /books/:id/suggestions/counts 各章节待处理的修改建议数。
func (a *App) WriterSuggestionCounts(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canSuggestBookContent(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权查看")
		return
	}
	var rows []struct {
		DocumentID uint
		N          int64
	}
	a.DB.Model(&models.WriterSuggestion{}).Select("document_id, COUNT(*) AS n").
		Where("book_id = ? AND status = ?", book.ID, "pending").Group("document_id").Scan(&rows)
	counts := map[string]int64{}
	for _, r := range rows {
		counts[strconv.FormatUint(uint64(r.DocumentID), 10)] = r.N
	}
	ok(c, gin.H{"counts": counts})
}

func (a *App) findSuggestion(c *gin.Context) (*models.WriterSuggestion, *models.Book, bool) {
	var s models.WriterSuggestion
	if a.DB.First(&s, c.Param("id")).Error != nil {
		fail(c, http.StatusNotFound, "修改建议不存在")
		return nil, nil, false
	}
	var book models.Book
	if a.DB.First(&book, s.BookID).Error != nil || !a.canSuggestBookContent(currentUser(c), &book) {
		fail(c, http.StatusNotFound, "修改建议不存在")
		return nil, nil, false
	}
	return &s, &book, true
}

// DecideWriterSuggestion POST /suggestions/:id/decide {status: accepted|partial|rejected}
// 由能编辑的人在写作台合并并保存采纳的修改后调用，记录结果并通知建议者。
func (a *App) DecideWriterSuggestion(c *gin.Context) {
	s, book, allowed := a.findSuggestion(c)
	if !allowed {
		return
	}
	u := currentUser(c)
	if !a.canEditBookContent(u, book) {
		fail(c, http.StatusForbidden, "只有能编辑这本书的人可以处理修改建议")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Status != "accepted" && req.Status != "partial" && req.Status != "rejected") {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if s.Status != "pending" {
		fail(c, http.StatusConflict, "这条修改建议已经处理过了")
		return
	}
	now := time.Now()
	if err := a.DB.Model(s).Updates(map[string]any{"status": req.Status, "decided_by": u.ID, "decided_at": now}).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	s.Status, s.DecidedBy, s.DecidedAt = req.Status, u.ID, &now
	var doc models.Document
	a.DB.Select("id", "title", "slug").First(&doc, s.DocumentID)
	var suggester models.User
	a.DB.First(&suggester, s.UserID)
	a.recordActivity(book.ID, s.DocumentID, u.ID, "suggestion.decided", map[string]any{"title": doc.Title, "status": req.Status, "suggester": suggester.PublicName()})
	if s.UserID != u.ID {
		a.NotifyI18n(s.UserID, "collaboration", "notify.writerSuggestion."+req.Status,
			map[string]string{"user": u.PublicName(), "book": book.Title, "chapter": doc.Title},
			map[string]any{"link": "/book/writer/" + book.Slug + "/" + doc.Slug + "?comments=suggestions", "book_id": book.ID, "document_id": doc.ID, "suggestion_id": s.ID})
	}
	a.publishSuggestions(c, book.ID, s.DocumentID)
	ok(c, a.suggestionViews([]models.WriterSuggestion{*s})[0])
}

// DeleteWriterSuggestion DELETE /suggestions/:id 建议者撤回自己尚未处理的建议。
func (a *App) DeleteWriterSuggestion(c *gin.Context) {
	s, book, allowed := a.findSuggestion(c)
	if !allowed {
		return
	}
	if s.UserID != currentUser(c).ID || s.Status != "pending" {
		fail(c, http.StatusForbidden, "只能撤回自己尚未处理的建议")
		return
	}
	if err := a.DB.Delete(s).Error; err != nil {
		fail(c, http.StatusInternalServerError, "撤回失败")
		return
	}
	a.publishSuggestions(c, book.ID, s.DocumentID)
	ok(c, gin.H{"id": s.ID})
}

// WriterMembers GET /books/:id/writer-members 书籍作者与已接受的协作者（批注中 @ 提及的候选人）。
func (a *App) WriterMembers(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canSuggestBookContent(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权查看")
		return
	}
	type member struct {
		collabUser
		Role string `json:"role"`
	}
	out := []member{}
	var owner models.User
	if a.DB.First(&owner, book.UserID).Error == nil {
		out = append(out, member{collabUser: toCollabUser(&owner), Role: "owner"})
	}
	var collabs []models.BookCollaborator
	a.DB.Preload("User").Where("book_id = ? AND status = ?", book.ID, "accepted").Order("id ASC").Find(&collabs)
	for _, cb := range collabs {
		if cb.User != nil {
			out = append(out, member{collabUser: toCollabUser(cb.User), Role: cb.Role})
		}
	}
	ok(c, gin.H{"items": out})
}

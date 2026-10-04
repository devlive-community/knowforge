package app

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/models"
)

// —— 写作批注：协作者在写作台选中正文添加批注、回复讨论、标记解决。只有能编辑该书的人可见与参与；
// 新批注与回复通知书籍作者与讨论中的其他人（站内通知「协作」类），并推送给同书的其他写作台刷新批注列表。——

const (
	maxCommentRunes = 2000
	maxQuoteRunes   = 1000
	maxContextRunes = 64
)

func init() {
	i18ntext.Register("notify.writerComment.new", map[string]string{
		"zh-CN": "「{user}」在《{book}》的「{chapter}」中添加了批注",
		"en":    `{user} commented on "{chapter}" in "{book}"`,
	})
	i18ntext.Register("notify.writerComment.mention", map[string]string{
		"zh-CN": "「{user}」在《{book}》「{chapter}」的批注中提到了你",
		"en":    `{user} mentioned you in a comment on "{chapter}" in "{book}"`,
	})
	i18ntext.Register("notify.writerComment.reply", map[string]string{
		"zh-CN": "「{user}」回复了《{book}》「{chapter}」中的批注",
		"en":    `{user} replied to a comment on "{chapter}" in "{book}"`,
	})
}

type writerCommentView struct {
	models.WriterComment
	User    collabUser          `json:"user"`
	Replies []writerCommentView `json:"replies,omitempty"`
}

// editableDocument 当前用户可编辑（因此可查看与批注）的章节。
func (a *App) commentDocument(c *gin.Context) (*models.Document, *models.Book, bool) {
	doc, book, status := a.findDocument(c)
	if doc == nil {
		fail(c, status, "文档不存在")
		return nil, nil, false
	}
	if !a.canSuggestBookContent(currentUser(c), book) {
		fail(c, http.StatusForbidden, "只有书籍作者与协作者可以查看批注")
		return nil, nil, false
	}
	return doc, book, true
}

func (a *App) commentViews(list []models.WriterComment) []writerCommentView {
	ids := map[uint]bool{}
	for _, cm := range list {
		ids[cm.UserID] = true
	}
	userIDs := make([]uint, 0, len(ids))
	for id := range ids {
		userIDs = append(userIDs, id)
	}
	users := map[uint]collabUser{}
	if len(userIDs) > 0 {
		var rows []models.User
		a.DB.Where("id IN ?", userIDs).Find(&rows)
		for i := range rows {
			users[rows[i].ID] = toCollabUser(&rows[i])
		}
	}
	out := make([]writerCommentView, 0, len(list))
	for _, cm := range list {
		u, found := users[cm.UserID]
		if !found {
			u = collabUser{ID: cm.UserID, Username: "deleted", DisplayName: "-"}
		}
		out = append(out, writerCommentView{WriterComment: cm, User: u})
	}
	return out
}

// ListWriterComments GET /documents/:id/writer-comments?status=open|resolved|all 章节的批注（按讨论串组织，新的在后）与未解决 / 已解决数量。
func (a *App) ListWriterComments(c *gin.Context) {
	doc, _, allowed := a.commentDocument(c)
	if !allowed {
		return
	}
	var roots []models.WriterComment
	q := a.DB.Where("document_id = ? AND parent_id IS NULL", doc.ID)
	switch c.Query("status") {
	case "resolved":
		q = q.Where("resolved = ?", true)
	case "all":
	default:
		q = q.Where("resolved = ?", false)
	}
	q.Order("created_at ASC, id ASC").Find(&roots)
	threads := a.commentViews(roots)
	if len(roots) > 0 {
		rootIDs := make([]uint, 0, len(roots))
		for _, r := range roots {
			rootIDs = append(rootIDs, r.ID)
		}
		var replies []models.WriterComment
		a.DB.Where("parent_id IN ?", rootIDs).Order("created_at ASC, id ASC").Find(&replies)
		byParent := map[uint][]writerCommentView{}
		for _, r := range a.commentViews(replies) {
			byParent[*r.ParentID] = append(byParent[*r.ParentID], r)
		}
		for i := range threads {
			threads[i].Replies = byParent[threads[i].ID]
		}
	}
	var open, resolved int64
	a.DB.Model(&models.WriterComment{}).Where("document_id = ? AND parent_id IS NULL AND resolved = ?", doc.ID, false).Count(&open)
	a.DB.Model(&models.WriterComment{}).Where("document_id = ? AND parent_id IS NULL AND resolved = ?", doc.ID, true).Count(&resolved)
	ok(c, gin.H{"items": threads, "open": open, "resolved": resolved})
}

// WriterCommentCounts GET /books/:id/writer-comments/counts 各章节未解决的批注数（写作台目录标记）。
func (a *App) WriterCommentCounts(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canSuggestBookContent(currentUser(c), book) {
		fail(c, http.StatusForbidden, "只有书籍作者与协作者可以查看批注")
		return
	}
	var rows []struct {
		DocumentID uint
		N          int64
	}
	a.DB.Model(&models.WriterComment{}).Select("document_id, COUNT(*) AS n").
		Where("book_id = ? AND parent_id IS NULL AND resolved = ?", book.ID, false).Group("document_id").Scan(&rows)
	counts := map[string]int64{}
	for _, r := range rows {
		counts[strconv.FormatUint(uint64(r.DocumentID), 10)] = r.N
	}
	ok(c, gin.H{"counts": counts})
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// CreateWriterComment POST /documents/:id/writer-comments {content, quote?, prefix?, suffix?, quote_offset?, parent_id?}
func (a *App) CreateWriterComment(c *gin.Context) {
	doc, book, allowed := a.commentDocument(c)
	if !allowed {
		return
	}
	var req struct {
		Content     string `json:"content"`
		Quote       string `json:"quote"`
		Prefix      string `json:"prefix"`
		Suffix      string `json:"suffix"`
		QuoteOffset int    `json:"quote_offset"`
		ParentID    *uint  `json:"parent_id"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" || utf8.RuneCountInString(content) > maxCommentRunes {
		fail(c, http.StatusBadRequest, "批注内容不能为空，且不超过 2000 字")
		return
	}
	u := currentUser(c)
	cm := models.WriterComment{BookID: book.ID, DocumentID: doc.ID, UserID: u.ID, Content: content}
	var root *models.WriterComment
	if req.ParentID != nil {
		var parent models.WriterComment
		if a.DB.Where("id = ? AND document_id = ? AND parent_id IS NULL", *req.ParentID, doc.ID).First(&parent).Error != nil {
			fail(c, http.StatusBadRequest, "要回复的批注不存在")
			return
		}
		cm.ParentID = &parent.ID
		root = &parent
	} else {
		if utf8.RuneCountInString(req.Quote) > maxQuoteRunes {
			fail(c, http.StatusBadRequest, "批注的原文过长，请选择较短的一段")
			return
		}
		cm.Quote = req.Quote
		cm.Prefix = clipRunes(req.Prefix, maxContextRunes)
		cm.Suffix = clipRunes(req.Suffix, maxContextRunes)
		cm.QuoteOffset = max(0, req.QuoteOffset)
	}
	if err := a.DB.Create(&cm).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存批注失败")
		return
	}
	a.notifyWriterComment(c, book, doc, &cm, root)
	writerHub.Publish(book.ID, "comments", gin.H{"doc_id": doc.ID, "origin": c.GetHeader(collabConnHeader)})
	ok(c, a.commentViews([]models.WriterComment{cm})[0])
}

// mentionPattern 批注中的 @用户名（与注册用户名规则一致）。
var mentionPattern = regexp.MustCompile(`@([A-Za-z0-9_-]{3,50})`)

// notifyWriterComment 通知书籍作者与讨论串中的其他人（不含自己）；被 @ 提及的人（须能参与写作）收到「提到了你」的通知。
func (a *App) notifyWriterComment(c *gin.Context, book *models.Book, doc *models.Document, cm *models.WriterComment, root *models.WriterComment) {
	u := currentUser(c)
	mentioned := map[uint]bool{}
	for _, m := range mentionPattern.FindAllStringSubmatch(cm.Content, 20) {
		var target models.User
		if a.DB.Where("username = ?", m[1]).First(&target).Error == nil && target.ID != u.ID && a.canSuggestBookContent(&target, book) {
			mentioned[target.ID] = true
		}
	}
	params := map[string]string{"user": u.PublicName(), "book": book.Title, "chapter": doc.Title}
	payload := map[string]any{"link": "/book/writer/" + book.Slug + "/" + doc.Slug + "?comments=open", "book_id": book.ID, "document_id": doc.ID, "comment_id": cm.ID}
	for uid := range mentioned {
		a.NotifyI18n(uid, "collaboration", "notify.writerComment.mention", params, payload)
	}
	recipients := map[uint]bool{book.UserID: true}
	key := "notify.writerComment.new"
	if root != nil {
		key = "notify.writerComment.reply"
		recipients[root.UserID] = true
		var ids []uint
		a.DB.Model(&models.WriterComment{}).Where("parent_id = ?", root.ID).Distinct().Pluck("user_id", &ids)
		for _, id := range ids {
			recipients[id] = true
		}
	}
	delete(recipients, u.ID)
	for uid := range recipients {
		if mentioned[uid] {
			continue // 已收到「提到了你」
		}
		// 只通知仍可编辑该书的人（如协作者已被移除则不再通知）
		var target models.User
		if a.DB.First(&target, uid).Error != nil || !a.canSuggestBookContent(&target, book) {
			continue
		}
		a.NotifyI18n(uid, "collaboration", key, params, payload)
	}
}

func (a *App) findWriterComment(c *gin.Context) (*models.WriterComment, *models.Book, bool) {
	var cm models.WriterComment
	if a.DB.First(&cm, c.Param("id")).Error != nil {
		fail(c, http.StatusNotFound, "批注不存在")
		return nil, nil, false
	}
	var book models.Book
	if a.DB.First(&book, cm.BookID).Error != nil || !a.canSuggestBookContent(currentUser(c), &book) {
		fail(c, http.StatusNotFound, "批注不存在")
		return nil, nil, false
	}
	return &cm, &book, true
}

func (a *App) publishComments(c *gin.Context, cm *models.WriterComment) {
	writerHub.Publish(cm.BookID, "comments", gin.H{"doc_id": cm.DocumentID, "origin": c.GetHeader(collabConnHeader)})
}

// UpdateWriterComment PUT /writer-comments/:id {content} 修改自己的批注。
func (a *App) UpdateWriterComment(c *gin.Context) {
	cm, _, allowed := a.findWriterComment(c)
	if !allowed {
		return
	}
	if cm.UserID != currentUser(c).ID {
		fail(c, http.StatusForbidden, "只能修改自己的批注")
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	content := ""
	if c.ShouldBindJSON(&req) == nil {
		content = strings.TrimSpace(req.Content)
	}
	if content == "" || utf8.RuneCountInString(content) > maxCommentRunes {
		fail(c, http.StatusBadRequest, "批注内容不能为空，且不超过 2000 字")
		return
	}
	cm.Content = content
	if err := a.DB.Model(cm).Update("content", content).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	a.publishComments(c, cm)
	ok(c, a.commentViews([]models.WriterComment{*cm})[0])
}

// DeleteWriterComment DELETE /writer-comments/:id 删除自己的批注（书籍所有者与管理员可删除任何批注）；删除顶级批注时连同回复一起删除。
func (a *App) DeleteWriterComment(c *gin.Context) {
	cm, book, allowed := a.findWriterComment(c)
	if !allowed {
		return
	}
	u := currentUser(c)
	if cm.UserID != u.ID && !a.canManageBook(u, book) {
		fail(c, http.StatusForbidden, "只能删除自己的批注")
		return
	}
	if cm.ParentID == nil {
		a.DB.Where("parent_id = ?", cm.ID).Delete(&models.WriterComment{})
	}
	if err := a.DB.Delete(cm).Error; err != nil {
		fail(c, http.StatusInternalServerError, "删除失败")
		return
	}
	a.publishComments(c, cm)
	ok(c, gin.H{"id": cm.ID})
}

// ResolveWriterComment POST /writer-comments/:id/resolve {resolved} 标记讨论已解决或重新打开（任何协作者均可）。
func (a *App) ResolveWriterComment(c *gin.Context) {
	cm, _, allowed := a.findWriterComment(c)
	if !allowed {
		return
	}
	if cm.ParentID != nil {
		fail(c, http.StatusBadRequest, "只能对顶级批注标记解决")
		return
	}
	var req struct {
		Resolved bool `json:"resolved"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	updates := map[string]any{"resolved": req.Resolved, "resolved_by": uint(0), "resolved_at": nil}
	if req.Resolved {
		now := time.Now()
		updates["resolved_by"], updates["resolved_at"] = currentUser(c).ID, now
	}
	if err := a.DB.Model(cm).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	a.DB.First(cm, cm.ID)
	a.publishComments(c, cm)
	ok(c, a.commentViews([]models.WriterComment{*cm})[0])
}

package app

import (
	"fmt"
	"net/http"
	"strconv"

	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type commentPayload struct {
	Content  *string `json:"content"`
	ParentID *uint   `json:"parent_id"`
}

func (a *App) canManageComment(c *gin.Context, comment *models.Comment, book *models.Book) bool {
	u := currentUser(c)
	if u == nil {
		return false
	}
	if u.ID == comment.UserID || u.Role == "admin" {
		return true
	}
	// 书籍作者可以管理自己书下的评论
	return book.UserID == u.ID && u.TokenAllowsBook(book.ID)
}

func publicCommentUser(u *models.User) gin.H {
	if u == nil {
		return gin.H{}
	}
	return gin.H{
		"id": u.ID, "username": u.Username, "nickname": u.Nickname, "display_name": u.PublicName(), "avatar": u.Avatar,
		"bio": u.Bio, "github_url": u.GithubURL, "role": u.Role,
	}
}

func publicCommentItem(comment models.Comment) gin.H {
	return gin.H{
		"id": comment.ID, "user_id": comment.UserID, "user": publicCommentUser(comment.User),
		"content": comment.Content, "created_at": comment.CreatedAt,
	}
}

// ListComments GET /documents/:docId/comments 章节评论（两级）
func (a *App) ListComments(c *gin.Context) {
	docID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var doc models.Document
	if err := a.DB.First(&doc, docID).Error; err != nil {
		fail(c, http.StatusNotFound, "章节不存在")
		return
	}
	var book models.Book
	if err := a.DB.First(&book, doc.BookID).Error; err != nil || !a.canReadDocument(currentUser(c), &doc, &book) {
		fail(c, http.StatusNotFound, "章节不存在")
		return
	}
	q := a.DB.Preload("User", func(tx *gorm.DB) *gorm.DB {
		return tx.Select("id", "username", "nickname", "name_display", "avatar", "bio", "github_url", "role")
	}).Where("document_id = ? AND status = ?", docID, "published").Order("created_at ASC")
	var comments []models.Comment
	if err := q.Find(&comments).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	// 组装两级
	byParent := map[uint][]models.Comment{}
	var roots []models.Comment
	for _, cm := range comments {
		if cm.ParentID == nil {
			roots = append(roots, cm)
		} else {
			byParent[*cm.ParentID] = append(byParent[*cm.ParentID], cm)
		}
	}
	result := make([]gin.H, 0, len(roots))
	for _, root := range roots {
		replies := make([]gin.H, 0, len(byParent[root.ID]))
		for _, reply := range byParent[root.ID] {
			replies = append(replies, publicCommentItem(reply))
		}
		item := publicCommentItem(root)
		item["replies"] = replies
		result = append(result, item)
	}
	ok(c, result)
}

// CreateComment POST /documents/:docId/comments
func (a *App) CreateComment(c *gin.Context) {
	if !a.commentsEnabled() {
		fail(c, http.StatusForbidden, "站点已关闭评论")
		return
	}
	u := currentUser(c)
	docID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var req struct {
		Content       string `json:"content"`
		ParentID      *uint  `json:"parent_id"`
		CaptchaID     string `json:"captcha_id"`
		CaptchaAnswer string `json:"captcha_answer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len([]rune(req.Content)) == 0 {
		fail(c, http.StatusBadRequest, "请填写评论内容")
		return
	}
	if len([]rune(req.Content)) > 2000 {
		fail(c, http.StatusBadRequest, "评论最多 2000 字")
		return
	}
	if err := a.checkCaptcha("comment", req.CaptchaID, req.CaptchaAnswer); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	var doc models.Document
	if err := a.DB.First(&doc, docID).Error; err != nil {
		fail(c, http.StatusNotFound, "章节不存在")
		return
	}
	var book models.Book
	if err := a.DB.First(&book, doc.BookID).Error; err != nil {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	if !a.canReadDocument(u, &doc, &book) {
		fail(c, http.StatusNotFound, "章节不存在")
		return
	}
	if doc.AllowComments != nil && !*doc.AllowComments {
		fail(c, http.StatusForbidden, "该章节已关闭评论")
		return
	}

	comment := models.Comment{
		DocumentID: doc.ID,
		UserID:     u.ID,
		Content:    req.Content,
		Status:     "published",
	}
	if req.ParentID != nil {
		var parent models.Comment
		if err := a.DB.Where("id = ? AND document_id = ?", *req.ParentID, doc.ID).First(&parent).Error; err != nil {
			fail(c, http.StatusBadRequest, "父评论不存在")
			return
		}
		comment.ParentID = req.ParentID
	}
	if err := a.DB.Create(&comment).Error; err != nil {
		fail(c, http.StatusInternalServerError, "发表失败: "+err.Error())
		return
	}
	a.DB.Preload("User").First(&comment, comment.ID)

	// M13 通知触发：书籍作者 + 被回复人（不通知操作者本人，作者与被回复人重复时只发一条）
	readerLink := fmt.Sprintf("/book/reader/%s/%s", book.Slug, doc.Slug)
	if book.UserID != u.ID {
		a.NotifyI18n(book.UserID, "comment", "notify.comment.chapter",
			map[string]string{"user": u.Username, "chapter": doc.Title},
			map[string]any{"link": readerLink})
	}
	if comment.ParentID != nil {
		var parent models.Comment
		if err := a.DB.First(&parent, *comment.ParentID).Error; err == nil &&
			parent.UserID != u.ID && parent.UserID != book.UserID {
			a.NotifyI18n(parent.UserID, "comment", "notify.comment.reply",
				map[string]string{"user": u.Username},
				map[string]any{"link": readerLink})
		}
	}
	a.emitActivity(u.ID, "comment.created", "comment", strconv.FormatUint(uint64(comment.ID), 10), fmt.Sprintf("comment.given:%d", comment.ID))
	if book.UserID != u.ID {
		a.emitActivity(book.UserID, "comment.received", "comment", strconv.FormatUint(uint64(comment.ID), 10), fmt.Sprintf("comment.received:%d", comment.ID))
	}
	ok(c, publicCommentItem(comment))
}

// UpdateComment PUT /comments/:id 编辑自己的评论
func (a *App) UpdateComment(c *gin.Context) {
	u := currentUser(c)
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var comment models.Comment
	if err := a.DB.First(&comment, id).Error; err != nil {
		fail(c, http.StatusNotFound, "评论不存在")
		return
	}
	if comment.UserID != u.ID {
		fail(c, http.StatusForbidden, "只能编辑自己的评论")
		return
	}
	var doc models.Document
	if err := a.DB.First(&doc, comment.DocumentID).Error; err != nil {
		fail(c, http.StatusNotFound, "章节不存在")
		return
	}
	var book models.Book
	if err := a.DB.First(&book, doc.BookID).Error; err != nil || !a.canReadDocument(u, &doc, &book) {
		fail(c, http.StatusNotFound, "章节不存在")
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len([]rune(req.Content)) == 0 {
		fail(c, http.StatusBadRequest, "请填写评论内容")
		return
	}
	if len([]rune(req.Content)) > 2000 {
		fail(c, http.StatusBadRequest, "评论最多 2000 字")
		return
	}
	comment.Content = req.Content
	a.DB.Save(&comment)
	ok(c, publicCommentItem(comment))
}

// DeleteComment DELETE /comments/:id 本人或书籍作者/管理员
func (a *App) DeleteComment(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var comment models.Comment
	if err := a.DB.First(&comment, id).Error; err != nil {
		fail(c, http.StatusNotFound, "评论不存在")
		return
	}
	var doc models.Document
	if err := a.DB.First(&doc, comment.DocumentID).Error; err != nil {
		fail(c, http.StatusNotFound, "章节不存在")
		return
	}
	var book models.Book
	if err := a.DB.First(&book, doc.BookID).Error; err != nil {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	if !a.canManageComment(c, &comment, &book) {
		fail(c, http.StatusForbidden, "无权删除该评论")
		return
	}
	a.DB.Delete(&comment)
	sourceID := strconv.FormatUint(uint64(comment.ID), 10)
	a.emitActivity(comment.UserID, "comment.deleted", "comment", sourceID, "comment.deleted:"+sourceID)
	ok(c, gin.H{"message": "已删除"})
}

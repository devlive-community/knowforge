package app

import (
	"fmt"
	"net/http"
	"strconv"

	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type reactionPayload struct {
	Type string `json:"type"` // like | favorite
}

// PutReaction POST /books/:id/reactions 点赞/收藏（同类型重复请求幂等）
func (a *App) PutReaction(c *gin.Context) {
	u := currentUser(c)
	bookID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var req reactionPayload
	if err := c.ShouldBindJSON(&req); err != nil || (req.Type != "like" && req.Type != "favorite") {
		fail(c, http.StatusBadRequest, "类型必须为 like 或 favorite")
		return
	}
	var book models.Book
	if err := a.DB.First(&book, bookID).Error; err != nil {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	if !a.canReadBook(u, &book) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}

	reaction := models.Reaction{UserID: u.ID, BookID: book.ID, Type: req.Type}
	tx := a.DB.Where(models.Reaction{UserID: u.ID, BookID: book.ID, Type: req.Type}).
		FirstOrCreate(&reaction)
	if tx.Error != nil {
		fail(c, http.StatusInternalServerError, "操作失败: "+tx.Error.Error())
		return
	}
	// M13 通知触发：仅新建的点赞/收藏才通知书籍作者
	if tx.RowsAffected > 0 && book.UserID != u.ID {
		key := "notify.reaction.like"
		if req.Type == "favorite" {
			key = "notify.reaction.favorite"
		}
		a.NotifyI18n(book.UserID, "reaction", key,
			map[string]string{"user": u.Username, "book": book.Title},
			map[string]any{"link": fmt.Sprintf("/book/detail/%s", book.Slug)})
	}
	if tx.RowsAffected > 0 {
		a.emitActivity(u.ID, "reaction.created", "reaction", strconv.FormatUint(uint64(reaction.ID), 10), fmt.Sprintf("reaction.given:%d", reaction.ID))
		if book.UserID != u.ID {
			a.emitActivity(book.UserID, "reaction.received", "reaction", strconv.FormatUint(uint64(reaction.ID), 10), fmt.Sprintf("reaction.received:%d", reaction.ID))
		}
	}
	ok(c, reaction)
}

// DeleteReaction DELETE /books/:id/reactions?type=
func (a *App) DeleteReaction(c *gin.Context) {
	u := currentUser(c)
	bookID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	rType := c.Query("type")
	if rType != "like" && rType != "favorite" {
		fail(c, http.StatusBadRequest, "类型必须为 like 或 favorite")
		return
	}
	var removed []models.Reaction
	a.DB.Where("user_id = ? AND book_id = ? AND type = ?", u.ID, bookID, rType).Find(&removed)
	a.DB.Where("user_id = ? AND book_id = ? AND type = ?", u.ID, bookID, rType).Delete(&models.Reaction{})
	for _, r := range removed {
		id := strconv.FormatUint(uint64(r.ID), 10)
		a.emitActivity(u.ID, "reaction.deleted", "reaction", id, "reaction.deleted:"+id)
	}
	ok(c, gin.H{"message": "已取消"})
}

// MyReactions GET /users/me/reactions?type= 当前用户的点赞/收藏列表（分页）
func (a *App) MyReactions(c *gin.Context) {
	u := currentUser(c)
	rType := c.DefaultQuery("type", "favorite")
	if rType != "like" && rType != "favorite" {
		fail(c, http.StatusBadRequest, "类型必须为 like 或 favorite")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize := 12
	if p := atoiDefault(c.Query("page_size"), 12); p > 0 && p <= 50 {
		pageSize = p
	}
	if page < 1 {
		page = 1
	}

	var reactions []models.Reaction
	q := a.DB.Model(&models.Reaction{}).
		Joins("JOIN books b ON b.id = reactions.book_id").
		Where("reactions.user_id = ? AND reactions.type = ?", u.ID, rType)
	if !IsAdmin(u) {
		q = q.Where(
			"(b.is_public = ? AND b.status IN ?) OR b.user_id = ? OR EXISTS (SELECT 1 FROM book_collaborators bc WHERE bc.book_id = b.id AND bc.user_id = ? AND bc.status = 'accepted')",
			true, publiclyReadableBookStatuses, u.ID, u.ID,
		)
	}
	// 先计数再取列：COUNT 不能带 reactions.* 选择列与排序（PostgreSQL 不支持 COUNT(reactions.*)）
	var total int64
	q.Session(&gorm.Session{}).Count(&total)
	if err := q.Select("reactions.*").Order("reactions.created_at DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&reactions).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}

	// 批量取书籍
	bookIDs := make([]uint, 0, len(reactions))
	for _, r := range reactions {
		bookIDs = append(bookIDs, r.BookID)
	}
	books := map[uint]models.Book{}
	if len(bookIDs) > 0 {
		var list []models.Book
		a.DB.Where("id IN ?", bookIDs).Find(&list)
		for _, b := range list {
			books[b.ID] = b
		}
	}

	items := make([]gin.H, 0, len(reactions))
	for _, r := range reactions {
		items = append(items, gin.H{"book": books[r.BookID], "reacted_at": r.CreatedAt})
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// BookReactions GET /books/:id/reactions/me 当前用户对该书的态度
func (a *App) MyBookReaction(c *gin.Context) {
	u := currentUser(c)
	bookID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var book models.Book
	if err := a.DB.First(&book, bookID).Error; err != nil || !a.canReadBook(u, &book) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	var reactions []models.Reaction
	a.DB.Where("user_id = ? AND book_id = ?", u.ID, bookID).Find(&reactions)
	types := make([]string, 0, len(reactions))
	for _, r := range reactions {
		types = append(types, r.Type)
	}
	// 计数
	var likeCount, favCount int64
	a.DB.Model(&models.Reaction{}).Where("book_id = ? AND type = ?", bookID, "like").Count(&likeCount)
	a.DB.Model(&models.Reaction{}).Where("book_id = ? AND type = ?", bookID, "favorite").Count(&favCount)
	ok(c, gin.H{
		"types":          types,
		"like_count":     likeCount,
		"favorite_count": favCount,
	})
}

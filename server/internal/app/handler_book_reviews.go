package app

import (
	"fmt"
	"net/http"
	"strconv"

	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 书籍评价：每位用户对每本书一条评分（1-5）+ 可选文字评论。详情页展示平均分、分布与评论列表。

func publicReviewItem(r models.BookReview) gin.H {
	return gin.H{
		"id": r.ID, "user_id": r.UserID, "user": publicCommentUser(r.User),
		"rating": r.Rating, "content": r.Content,
		"created_at": r.CreatedAt, "updated_at": r.UpdatedAt,
	}
}

// bookReviewSummary 统计一本书的评分概况（平均分、总数、1-5 分布）。
func (a *App) bookReviewSummary(bookID uint) gin.H {
	var rows []models.BookReview
	a.DB.Select("rating").Where("book_id = ? AND status = ?", bookID, "published").Find(&rows)
	dist := map[string]int{"1": 0, "2": 0, "3": 0, "4": 0, "5": 0}
	sum := 0
	for _, r := range rows {
		if r.Rating >= 1 && r.Rating <= 5 {
			dist[strconv.Itoa(r.Rating)]++
			sum += r.Rating
		}
	}
	avg := 0.0
	if len(rows) > 0 {
		avg = float64(sum) / float64(len(rows))
	}
	return gin.H{"average": avg, "count": len(rows), "distribution": dist}
}

// ListBookReviews GET /books/:id/reviews 分页返回书籍评论 + 评分概况 + 当前用户自己的评论
func (a *App) ListBookReviews(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canReadBook(u, book) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	page, pageSize := paginate(c)
	base := a.DB.Model(&models.BookReview{}).Where("book_id = ? AND status = ?", book.ID, "published")
	var total int64
	base.Count(&total)
	var reviews []models.BookReview
	a.DB.Preload("User", func(tx *gorm.DB) *gorm.DB {
		return tx.Select("id", "username", "nickname", "name_display", "avatar", "bio", "github_url", "role")
	}).Where("book_id = ? AND status = ?", book.ID, "published").
		Order("updated_at DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&reviews)

	items := make([]gin.H, 0, len(reviews))
	for _, r := range reviews {
		items = append(items, publicReviewItem(r))
	}
	var mine any
	if u != nil {
		var own models.BookReview
		if err := a.DB.Where("book_id = ? AND user_id = ?", book.ID, u.ID).First(&own).Error; err == nil {
			mine = publicReviewItem(own)
		}
	}
	ok(c, gin.H{
		"items": items, "total": total, "page": page, "page_size": pageSize,
		"summary": a.bookReviewSummary(book.ID), "mine": mine,
	})
}

// UpsertBookReview POST /books/:id/reviews 新增或更新当前用户对该书的评分与评论
func (a *App) UpsertBookReview(c *gin.Context) {
	if !a.commentsEnabled() {
		fail(c, http.StatusForbidden, "站点已关闭评论")
		return
	}
	u := currentUser(c)
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canReadBook(u, book) {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	if book.UserID == u.ID {
		fail(c, http.StatusForbidden, "不能评价自己的书籍")
		return
	}
	var req struct {
		Rating  int    `json:"rating"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Rating < 1 || req.Rating > 5 {
		fail(c, http.StatusBadRequest, "请给出 1-5 星评分")
		return
	}
	if len([]rune(req.Content)) > 2000 {
		fail(c, http.StatusBadRequest, "评论最多 2000 字")
		return
	}

	var review models.BookReview
	created := false
	if err := a.DB.Where("book_id = ? AND user_id = ?", book.ID, u.ID).First(&review).Error; err != nil {
		review = models.BookReview{BookID: book.ID, UserID: u.ID, Status: "published"}
		created = true
	}
	review.Rating = req.Rating
	review.Content = req.Content
	if err := a.DB.Save(&review).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}

	// 首次评价时通知书籍作者（更新已有评价不重复通知）
	if created && book.UserID != u.ID {
		a.NotifyI18n(book.UserID, "comment", "notify.book.reviewed",
			map[string]string{"user": u.Username, "book": book.Title},
			map[string]any{"link": fmt.Sprintf("/book/detail/%s", book.Slug)})
	}

	a.DB.Preload("User").First(&review, review.ID)
	ok(c, gin.H{"review": publicReviewItem(review), "summary": a.bookReviewSummary(book.ID)})
}

// DeleteBookReview DELETE /reviews/:id 删除评论（本人、书籍作者或管理员）
func (a *App) DeleteBookReview(c *gin.Context) {
	u := currentUser(c)
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var review models.BookReview
	if err := a.DB.First(&review, id).Error; err != nil {
		fail(c, http.StatusNotFound, "评论不存在")
		return
	}
	var book models.Book
	if err := a.DB.First(&book, review.BookID).Error; err != nil {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	if !(u.ID == review.UserID || u.Role == "admin" || book.UserID == u.ID) {
		fail(c, http.StatusForbidden, "无权删除该评论")
		return
	}
	a.DB.Delete(&review)
	ok(c, gin.H{"message": "已删除", "summary": a.bookReviewSummary(book.ID)})
}

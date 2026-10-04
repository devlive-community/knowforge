package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type revisionAuthor struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
}

type documentRevisionResponse struct {
	ID            uint            `json:"id"`
	DocumentID    uint            `json:"document_id"`
	BookID        uint            `json:"book_id"`
	Title         string          `json:"title"`
	Content       *string         `json:"content,omitempty"`
	ContentLength int             `json:"content_length"`
	Status        string          `json:"status"`
	AllowComments bool            `json:"allow_comments"`
	Reason        string          `json:"reason"`
	Author        *revisionAuthor `json:"author,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

func newDocumentRevision(doc *models.Document, userID uint, reason string) models.DocumentRevision {
	allowComments := true
	if doc.AllowComments != nil {
		allowComments = *doc.AllowComments
	}
	return models.DocumentRevision{
		DocumentID:    doc.ID,
		BookID:        doc.BookID,
		UserID:        userID,
		Title:         doc.Title,
		Content:       doc.Content,
		Status:        doc.Status,
		AllowComments: allowComments,
		Reason:        reason,
	}
}

func revisionResponse(revision models.DocumentRevision, author *models.User, includeContent bool) documentRevisionResponse {
	response := documentRevisionResponse{
		ID:            revision.ID,
		DocumentID:    revision.DocumentID,
		BookID:        revision.BookID,
		Title:         revision.Title,
		ContentLength: utf8.RuneCountInString(revision.Content),
		Status:        revision.Status,
		AllowComments: revision.AllowComments,
		Reason:        revision.Reason,
		CreatedAt:     revision.CreatedAt,
	}
	if includeContent {
		response.Content = &revision.Content
	}
	if author != nil {
		response.Author = &revisionAuthor{ID: author.ID, Username: author.Username, Avatar: author.Avatar}
	}
	return response
}

// editableRevisionDocument 版本历史属于写作域，只有可编辑章节内容的用户可访问。
// 未授权统一返回 404，避免枚举私有章节或协作关系。
func (a *App) editableRevisionDocument(c *gin.Context) (*models.Document, *models.Book) {
	doc, book, status := a.findDocument(c)
	if doc == nil {
		fail(c, status, "文档不存在")
		return nil, nil
	}
	if !a.canEditBookContent(currentUser(c), book) {
		fail(c, http.StatusNotFound, "文档不存在")
		return nil, nil
	}
	return doc, book
}

// ListDocumentRevisions GET /documents/:id/revisions
func (a *App) ListDocumentRevisions(c *gin.Context) {
	doc, book := a.editableRevisionDocument(c)
	if doc == nil {
		return
	}
	page, pageSize := paginate(c)
	if pageSize > 50 {
		pageSize = 50
	}

	query := a.DB.Model(&models.DocumentRevision{}).Where("document_id = ?", doc.ID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询版本历史失败")
		return
	}
	// 只列出保留范围内的最新版本（较早的仍保存，升级权益后可见）
	keep := a.revisionKeep(book)
	visible, hidden := total, int64(0)
	if keep != plugincore.Unlimited && total > keep {
		visible, hidden = keep, total-keep
	}
	offset := int64((page - 1) * pageSize)
	limit := int64(pageSize)
	if offset+limit > visible {
		limit = visible - offset
	}
	revisions := []models.DocumentRevision{}
	if limit > 0 {
		if err := query.Order("created_at DESC, id DESC").Limit(int(limit)).Offset(int(offset)).Find(&revisions).Error; err != nil {
			fail(c, http.StatusInternalServerError, "查询版本历史失败")
			return
		}
	}

	userIDs := make([]uint, 0, len(revisions))
	for _, revision := range revisions {
		userIDs = append(userIDs, revision.UserID)
	}
	authors := map[uint]models.User{}
	if len(userIDs) > 0 {
		var users []models.User
		if err := a.DB.Select("id", "username", "nickname", "name_display", "avatar").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
			fail(c, http.StatusInternalServerError, "查询版本作者失败")
			return
		}
		for _, user := range users {
			authors[user.ID] = user
		}
	}

	items := make([]documentRevisionResponse, 0, len(revisions))
	for _, revision := range revisions {
		author, found := authors[revision.UserID]
		if found {
			items = append(items, revisionResponse(revision, &author, false))
		} else {
			items = append(items, revisionResponse(revision, nil, false))
		}
	}
	ok(c, gin.H{"items": items, "total": visible, "page": page, "page_size": pageSize, "hidden": hidden, "keep": keep})
}

// revisionKeep 书籍所有者每章可查看的历史版本数（-1 不限）。
func (a *App) revisionKeep(book *models.Book) int64 {
	var owner models.User
	if book == nil || a.DB.First(&owner, book.UserID).Error != nil {
		return plugincore.Unlimited
	}
	return a.entitlement(&owner, entVersionsKeep)
}

// revisionHidden 版本是否超出保留范围（比它新的版本已达到保留数）。
func (a *App) revisionHidden(db *gorm.DB, book *models.Book, revision *models.DocumentRevision) (bool, int64) {
	keep := a.revisionKeep(book)
	if keep == plugincore.Unlimited {
		return false, keep
	}
	var newer int64
	db.Model(&models.DocumentRevision{}).Where("document_id = ? AND (created_at > ? OR (created_at = ? AND id > ?))",
		revision.DocumentID, revision.CreatedAt, revision.CreatedAt, revision.ID).Count(&newer)
	return newer >= keep, keep
}

func revisionHiddenMessage(keep int64) string {
	return fmt.Sprintf("该版本超出可查看的历史版本数（每章最近 %d 个），升级等级或开通会员可查看更早的版本", keep)
}

// GetDocumentRevision GET /documents/:id/revisions/:revisionId
func (a *App) GetDocumentRevision(c *gin.Context) {
	doc, book := a.editableRevisionDocument(c)
	if doc == nil {
		return
	}
	revisionID, err := strconv.ParseUint(c.Param("revisionId"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, "版本不存在")
		return
	}
	var revision models.DocumentRevision
	if err := a.DB.Where("id = ? AND document_id = ?", revisionID, doc.ID).First(&revision).Error; err != nil {
		fail(c, http.StatusNotFound, "版本不存在")
		return
	}
	if hidden, keep := a.revisionHidden(a.DB, book, &revision); hidden {
		fail(c, http.StatusForbidden, revisionHiddenMessage(keep))
		return
	}
	var author models.User
	var authorPtr *models.User
	if err := a.DB.Select("id", "username", "nickname", "name_display", "avatar").First(&author, revision.UserID).Error; err == nil {
		authorPtr = &author
	}
	ok(c, revisionResponse(revision, authorPtr, true))
}

// RestoreDocumentRevision POST /documents/:id/revisions/:revisionId/restore
func (a *App) RestoreDocumentRevision(c *gin.Context) {
	doc, book := a.editableRevisionDocument(c)
	if doc == nil {
		return
	}
	revisionID, err := strconv.ParseUint(c.Param("revisionId"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, "版本不存在")
		return
	}
	u := currentUser(c)

	var restored models.Document
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&restored, doc.ID).Error; err != nil {
			return err
		}
		var target models.DocumentRevision
		if err := tx.Where("id = ? AND document_id = ?", revisionID, restored.ID).First(&target).Error; err != nil {
			return err
		}
		if hidden, keep := a.revisionHidden(tx, book, &target); hidden {
			return errRevisionHidden{keep: keep}
		}

		before := newDocumentRevision(&restored, u.ID, "pre_restore")
		if err := tx.Create(&before).Error; err != nil {
			return err
		}
		restored.Title = target.Title
		restored.Content = target.Content
		restored.Status = target.Status
		allowComments := target.AllowComments
		restored.AllowComments = &allowComments
		if err := tx.Save(&restored).Error; err != nil {
			return err
		}
		after := newDocumentRevision(&restored, u.ID, "restore")
		return tx.Create(&after).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, "版本不存在")
			return
		}
		var hidden errRevisionHidden
		if errors.As(err, &hidden) {
			fail(c, http.StatusForbidden, hidden.Error())
			return
		}
		fail(c, http.StatusInternalServerError, "恢复版本失败")
		return
	}
	a.publishDocSaved(c, book.ID, &restored)
	a.publishTreeChanged(c, book.ID)
	a.recordActivity(book.ID, restored.ID, currentUser(c).ID, "doc.restored", map[string]any{"title": restored.Title})
	ok(c, withContentHash(&restored))
}

// errRevisionHidden 恢复的版本超出保留范围。
type errRevisionHidden struct{ keep int64 }

func (e errRevisionHidden) Error() string { return revisionHiddenMessage(e.keep) }

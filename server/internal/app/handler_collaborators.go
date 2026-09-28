package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// M14 协作与团队：
//   - 书籍所有者（book.user_id）或管理员可增删协作者；协作者可查看列表、可自行退出
//   - 只有 accepted editor 可编辑章节；accepted viewer 可访问私有书籍的已发布章节
//   - 新邀请为 pending，通过 Notify 发送，并由受邀用户接受或拒绝

var collaboratorRoles = map[string]bool{"editor": true, "viewer": true}
var collaboratorStatuses = map[string]bool{"pending": true, "accepted": true, "rejected": true}

// ListCollaborators GET /books/:id/collaborators
func (a *App) ListCollaborators(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canManageBook(u, book) {
		if _, ok := a.collaboratorRole(u, book.ID); !ok {
			fail(c, http.StatusForbidden, "无权查看协作者")
			return
		}
	}

	collaborators := []models.BookCollaborator{}
	query := a.DB.Preload("User", func(tx *gorm.DB) *gorm.DB {
		return tx.Select("id", "username", "nickname", "name_display", "avatar", "bio")
	}).Where("book_id = ?", book.ID)
	if !a.canManageBook(u, book) {
		query = query.Where("status = ?", "accepted")
	}
	query.Order("created_at ASC").Find(&collaborators)
	ok(c, gin.H{"collaborators": collaborators})
}

type collaboratorPayload struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// AddCollaborator POST /books/:id/collaborators 发送邀请；已接受的协作者仅更新角色。
func (a *App) AddCollaborator(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canManageBook(u, book) {
		fail(c, http.StatusForbidden, "仅书籍所有者可管理协作者")
		return
	}
	var req collaboratorPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	req.Username = strings.TrimSpace(req.Username)
	if !collaboratorRoles[req.Role] {
		fail(c, http.StatusBadRequest, "角色必须为 editor 或 viewer")
		return
	}
	var target models.User
	if err := a.DB.Where("username = ?", req.Username).First(&target).Error; err != nil {
		fail(c, http.StatusNotFound, "用户不存在")
		return
	}
	if target.ID == book.UserID {
		fail(c, http.StatusBadRequest, "书籍所有者无需添加为协作者")
		return
	}
	if !target.IsActive {
		fail(c, http.StatusForbidden, "该账户已被禁用")
		return
	}

	var collab models.BookCollaborator
	lookupErr := a.DB.Where("book_id = ? AND user_id = ?", book.ID, target.ID).First(&collab).Error
	// 新增协作者或重新邀请已拒绝者会占用名额：按书籍所有者的「协作者人数」权益校验（调整现有协作者角色不受限）
	if lookupErr != nil || collab.Status == "rejected" {
		var owner models.User
		if a.DB.First(&owner, book.UserID).Error == nil {
			limit := a.entitlement(&owner, entCollaboratorsMax)
			var count int64
			a.DB.Model(&models.BookCollaborator{}).Where("book_id = ? AND status IN ?", book.ID, []string{"pending", "accepted"}).Count(&count)
			if !plugincore.WithinLimit(limit, count) {
				fail(c, http.StatusForbidden, fmt.Sprintf("该书的协作者已达上限（%d 人），升级等级或开通会员可邀请更多协作者", limit))
				return
			}
		}
	}
	if lookupErr == nil {
		if collab.Status == "accepted" {
			// 已接受的协作者由所有者直接调整角色，不需要重新确认。
			if collab.Role == req.Role {
				ok(c, collab)
				return
			}
			collab.Role = req.Role
			if err := a.DB.Model(&collab).Updates(map[string]any{"role": req.Role, "invited_by": u.ID}).Error; err != nil {
				fail(c, http.StatusInternalServerError, "保存失败")
				return
			}
			ok(c, collab)
			return
		}

		// 待处理邀请可更新角色；被拒绝后再次邀请会重新进入待确认状态。
		shouldNotify := collab.Status != "pending" || collab.Role != req.Role
		collab.Role = req.Role
		collab.Status = "pending"
		collab.InvitedBy = u.ID
		collab.RespondedAt = nil
		if err := a.DB.Model(&collab).Updates(map[string]any{
			"role": req.Role, "status": "pending", "invited_by": u.ID, "responded_at": nil,
		}).Error; err != nil {
			fail(c, http.StatusInternalServerError, "保存失败")
			return
		}
		if shouldNotify {
			a.notifyCollaborationInvitation(u, &target, book, &collab)
		}
		ok(c, collab)
		return
	}
	if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		fail(c, http.StatusInternalServerError, "查询协作者失败")
		return
	}

	collab = models.BookCollaborator{
		BookID: book.ID, UserID: target.ID, Role: req.Role, Status: "pending", InvitedBy: u.ID,
	}
	if err := a.DB.Create(&collab).Error; err != nil {
		fail(c, http.StatusInternalServerError, "添加失败: "+err.Error())
		return
	}
	a.notifyCollaborationInvitation(u, &target, book, &collab)
	ok(c, collab)
}

func (a *App) notifyCollaborationInvitation(inviter, target *models.User, book *models.Book, collab *models.BookCollaborator) {
	a.NotifyI18n(target.ID, "collaboration", "notify.collab.invited",
		map[string]string{"user": inviter.Username, "book": book.Title},
		map[string]any{
			"link":          "/notifications",
			"book_slug":     book.Slug,
			"invitation_id": collab.ID,
			"role":          collab.Role,
		})
}

type collaborationInvitationItem struct {
	ID              uint      `json:"id"`
	BookID          uint      `json:"book_id"`
	BookTitle       string    `json:"book_title"`
	BookSlug        string    `json:"book_slug"`
	Role            string    `json:"role"`
	InviterUsername string    `json:"inviter_username"`
	CreatedAt       time.Time `json:"created_at"`
}

// ListCollaborationInvitations GET /collaboration/invitations 当前用户待确认的邀请。
func (a *App) ListCollaborationInvitations(c *gin.Context) {
	u := currentUser(c)
	items := []collaborationInvitationItem{}
	err := a.DB.Model(&models.BookCollaborator{}).
		Select("book_collaborators.id, book_collaborators.book_id, books.title AS book_title, books.slug AS book_slug, book_collaborators.role, users.username AS inviter_username, book_collaborators.updated_at AS created_at").
		Joins("JOIN books ON books.id = book_collaborators.book_id").
		Joins("LEFT JOIN users ON users.id = book_collaborators.invited_by").
		Where("book_collaborators.user_id = ? AND book_collaborators.status = ? AND books.deleted_at IS NULL", u.ID, "pending").
		Order("book_collaborators.created_at DESC").Scan(&items).Error
	if err != nil {
		fail(c, http.StatusInternalServerError, "获取协作邀请失败")
		return
	}
	ok(c, gin.H{"invitations": items})
}

func (a *App) respondCollaborationInvitation(c *gin.Context, nextStatus string) {
	u := currentUser(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || !collaboratorStatuses[nextStatus] || nextStatus == "pending" {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var collab models.BookCollaborator
	if err := a.DB.Where("id = ? AND user_id = ? AND status = ?", uint(id), u.ID, "pending").First(&collab).Error; err != nil {
		fail(c, http.StatusNotFound, "邀请不存在或已处理")
		return
	}
	var book models.Book
	if err := a.DB.First(&book, collab.BookID).Error; err != nil {
		fail(c, http.StatusNotFound, "书籍不存在")
		return
	}
	now := time.Now()
	result := a.DB.Model(&models.BookCollaborator{}).
		Where("id = ? AND user_id = ? AND status = ?", collab.ID, u.ID, "pending").
		Updates(map[string]any{"status": nextStatus, "responded_at": &now})
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "处理邀请失败")
		return
	}
	if result.RowsAffected == 0 {
		fail(c, http.StatusConflict, "邀请已被处理")
		return
	}
	if collab.InvitedBy != 0 && collab.InvitedBy != u.ID {
		key := "notify.collab.rejected"
		if nextStatus == "accepted" {
			key = "notify.collab.accepted"
		}
		a.NotifyI18n(collab.InvitedBy, "collaboration", key,
			map[string]string{"user": u.Username, "book": book.Title},
			map[string]any{"link": "/book/settings/" + book.Slug, "book_slug": book.Slug})
	}
	ok(c, gin.H{"id": collab.ID, "status": nextStatus, "book_slug": book.Slug})
}

func (a *App) AcceptCollaborationInvitation(c *gin.Context) {
	a.respondCollaborationInvitation(c, "accepted")
}

func (a *App) RejectCollaborationInvitation(c *gin.Context) {
	a.respondCollaborationInvitation(c, "rejected")
}

// RemoveCollaborator DELETE /books/:id/collaborators/:userId 移除协作者（协作者可自行退出）
func (a *App) RemoveCollaborator(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	targetID, err := strconv.ParseUint(c.Param("userId"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	// 所有者/管理员可移除任意协作者；协作者只能移除自己（退出协作）
	if !a.canManageBook(u, book) && u.ID != uint(targetID) {
		fail(c, http.StatusForbidden, "仅书籍所有者可移除协作者")
		return
	}
	result := a.DB.Where("book_id = ? AND user_id = ?", book.ID, targetID).Delete(&models.BookCollaborator{})
	if result.Error != nil {
		fail(c, http.StatusInternalServerError, "移除失败")
		return
	}
	if result.RowsAffected == 0 {
		fail(c, http.StatusNotFound, "该用户不是协作者")
		return
	}
	ok(c, gin.H{"message": "已移除"})
}

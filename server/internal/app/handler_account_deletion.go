package app

import (
	"net/http"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// deleteUserCompletely 彻底删除用户及其全部数据（书籍、章节、版本、书内互动/进度/评论，以及用户自身的个人数据）。
// 供自助注销的冷静期到期清理与「冷静期为 0」的即时删除复用。
func (a *App) deleteUserCompletely(uid uint) error {
	return a.DB.Transaction(func(tx *gorm.DB) error {
		var bookIDs []uint
		tx.Model(&models.Book{}).Where("user_id = ?", uid).Pluck("id", &bookIDs)
		var docIDs []uint
		if len(bookIDs) > 0 {
			tx.Unscoped().Model(&models.Document{}).Where("book_id IN ?", bookIDs).Pluck("id", &docIDs)
		}
		// 1. 书籍范围数据（含他人对这些书的互动/进度/标注/评论）
		if len(bookIDs) > 0 {
			// 插件登记的书籍归属表（如标签关联）；插件未启用时表可能不存在，先判存在
			for _, m := range plugincore.BookDataModels() {
				if !tx.Migrator().HasTable(m) {
					continue
				}
				if err := tx.Unscoped().Where("book_id IN ?", bookIDs).Delete(m).Error; err != nil {
					return err
				}
			}
			for _, m := range []any{
				&models.BookCollaborator{}, &models.Reaction{}, &models.ReadingProgress{}, &models.ReadChapter{},
				&models.ReadingAnnotation{}, &models.BookAnalyticsDaily{}, &models.BookExportSetting{},
				&models.WriterComment{}, &models.WriterSuggestion{}, &models.WriterChapterTask{}, &models.WriterActivity{}, &models.DocumentRevision{}, &models.Document{},
			} {
				if err := tx.Unscoped().Where("book_id IN ?", bookIDs).Delete(m).Error; err != nil {
					return err
				}
			}
		}
		if len(docIDs) > 0 {
			if err := tx.Unscoped().Where("document_id IN ?", docIDs).Delete(&models.Comment{}).Error; err != nil {
				return err
			}
		}
		if len(bookIDs) > 0 {
			if err := tx.Unscoped().Where("id IN ?", bookIDs).Delete(&models.Book{}).Error; err != nil {
				return err
			}
		}
		// 2. 用户自身数据（跨他人书籍的互动/进度、绑定、设置、2FA 等）
		for _, m := range append([]any{
			&models.UserAuthentication{}, &models.Notification{}, &models.BookCollaborator{},
			&models.PasswordResetToken{}, &models.LoginChallenge{}, &models.TwoFactorStepUp{},
			&models.UserNotificationPref{}, &models.TwoFactorBackupCode{}, &models.EmailVerificationToken{},
			&models.Comment{}, &models.Reaction{}, &models.ReadingProgress{}, &models.ReadChapter{},
			&models.ReadingAnnotation{}, &models.UserExportSetting{}, &models.UserReadingGoal{},
			&models.ReadingDailyTime{}, &models.UserThemeSetting{}, &models.WriterComment{}, &models.WriterSuggestion{}, &models.WriterActivity{},
		}, plugincore.UserDataModels()...) {
			// 插件独占表（如成就）在插件未启用时不存在，跳过其清理
			if !tx.Migrator().HasTable(m) {
				continue
			}
			if err := tx.Unscoped().Where("user_id = ?", uid).Delete(m).Error; err != nil {
				return err
			}
		}
		var uname string
		tx.Model(&models.User{}).Where("id = ?", uid).Pluck("username", &uname)
		if uname != "" {
			tx.Where("username = ?", uname).Delete(&models.LoginLockout{})
		}
		// 3. 用户本身
		return tx.Unscoped().Delete(&models.User{}, uid).Error
	})
}

// isLastActiveAdmin 该用户是否为最后一位启用中的管理员（防止注销后无人可管）。
func (a *App) isLastActiveAdmin(u *models.User) bool {
	if u.Role != "admin" {
		return false
	}
	var others int64
	a.DB.Model(&models.User{}).Where("role = ? AND is_active = ? AND id != ?", "admin", true, u.ID).Count(&others)
	return others == 0
}

func accountDeletionStatus(u *models.User, cooldown int) gin.H {
	h := gin.H{"requested": u.DeletionRequestedAt != nil, "cooldown_days": cooldown}
	if u.DeletionRequestedAt != nil {
		h["deletion_requested_at"] = u.DeletionRequestedAt
		h["scheduled_delete_at"] = u.DeletionRequestedAt.AddDate(0, 0, cooldown)
	}
	return h
}

// GetAccountDeletion GET /auth/account/deletion 当前用户的注销状态与冷静期。
func (a *App) GetAccountDeletion(c *gin.Context) {
	ok(c, accountDeletionStatus(currentUser(c), a.accountDeletionCooldownDays()))
}

// RequestAccountDeletion POST /auth/account/deletion 申请注销：校验密码（有密码时）+ 二次认证，进入冷静期；冷静期为 0 则立即删除。
func (a *App) RequestAccountDeletion(c *gin.Context) {
	if !a.requireStepUp(c, tfOpDelete) {
		return
	}
	u := currentUser(c)
	if a.isLastActiveAdmin(u) {
		fail(c, http.StatusBadRequest, "你是最后一位启用中的管理员，无法注销账号")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	_ = c.ShouldBindJSON(&req)
	if u.Password != "" && bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)) != nil {
		fail(c, http.StatusBadRequest, "密码错误")
		return
	}

	cooldown := a.accountDeletionCooldownDays()
	if cooldown == 0 {
		if err := a.deleteUserCompletely(u.ID); err != nil {
			fail(c, http.StatusInternalServerError, "注销失败: "+err.Error())
			return
		}
		ok(c, gin.H{"deleted": true})
		return
	}
	now := currentTime()
	if err := a.DB.Model(u).Update("deletion_requested_at", now).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	u.DeletionRequestedAt = &now
	ok(c, accountDeletionStatus(u, cooldown))
}

// CancelAccountDeletion DELETE /auth/account/deletion 撤销注销申请（冷静期内可随时取消）。
func (a *App) CancelAccountDeletion(c *gin.Context) {
	u := currentUser(c)
	if err := a.DB.Model(u).Update("deletion_requested_at", nil).Error; err != nil {
		fail(c, http.StatusInternalServerError, "取消失败: "+err.Error())
		return
	}
	u.DeletionRequestedAt = nil
	ok(c, accountDeletionStatus(u, a.accountDeletionCooldownDays()))
}

// purgeScheduledAccountDeletions 冷静期到期的注销账号自动删除（维护任务调用）。
func (a *App) purgeScheduledAccountDeletions(now time.Time) error {
	cutoff := now.AddDate(0, 0, -a.accountDeletionCooldownDays())
	var ids []uint
	a.DB.Model(&models.User{}).Where("deletion_requested_at IS NOT NULL AND deletion_requested_at <= ?", cutoff).Pluck("id", &ids)
	for _, id := range ids {
		if err := a.deleteUserCompletely(id); err != nil {
			return err
		}
	}
	return nil
}

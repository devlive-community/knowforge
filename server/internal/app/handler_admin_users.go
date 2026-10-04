package app

import (
	"net/http"
	"strconv"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// adminUserSorts 用户排序白名单：sort 值 → ORDER BY 子句。
// last_login_at 可空，三种数据库（sqlite/mysql/postgres）对 NULL 的默认排序方向不一致，
// 故用 (last_login_at IS NULL) 前置守卫，让从未登录者恒定落在列表末尾，与方向无关。
// 仅接受白名单值，绝不把用户输入直接拼进 SQL。
var adminUserSorts = map[string]string{
	"created_at_desc":    "created_at DESC",
	"created_at_asc":     "created_at ASC",
	"last_login_at_desc": "(last_login_at IS NULL), last_login_at DESC",
	"last_login_at_asc":  "(last_login_at IS NULL), last_login_at ASC",
}

// AdminListUsers GET /admin/users 管理员分页查询用户（支持关键字、角色/状态筛选与排序）
func (a *App) AdminListUsers(c *gin.Context) {
	page, pageSize := paginate(c)
	query := a.DB.Model(&models.User{})
	if q := c.Query("q"); q != "" {
		like := "%" + q + "%"
		query = query.Where("username LIKE ? OR email LIKE ?", like, like)
	}
	if role := c.Query("role"); role == "admin" || role == "user" {
		query = query.Where("role = ?", role)
	}
	switch c.Query("status") {
	case "active":
		query = query.Where("is_active = ?", true)
	case "inactive":
		query = query.Where("is_active = ?", false)
	}
	order := adminUserSorts["created_at_desc"]
	if clause, ok := adminUserSorts[c.Query("sort")]; ok {
		order = clause
	}

	var total int64
	query.Count(&total)
	users := []models.User{}
	if err := query.Order(order).
		Limit(pageSize).Offset((page - 1) * pageSize).Find(&users).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询失败")
		return
	}
	ok(c, PageResult{Items: users, Total: total, Page: page, PageSize: pageSize})
}

// findManagedUser 载入目标用户并禁止操作自身，返回 nil 时已写出错误响应
func (a *App) findManagedUser(c *gin.Context) *models.User {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, "用户 ID 无效")
		return nil
	}
	if me := currentUser(c); me != nil && uint64(me.ID) == id {
		fail(c, http.StatusBadRequest, "不能对当前登录的管理员自身执行该操作")
		return nil
	}
	var u models.User
	if err := a.DB.First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, "用户不存在")
		return nil
	}
	return &u
}

// activeAdminCount 统计仍处于启用状态的管理员数量，用于防止移除最后一位管理员
func (a *App) activeAdminCount() int64 {
	var n int64
	a.DB.Model(&models.User{}).Where("role = ? AND is_active = ?", "admin", true).Count(&n)
	return n
}

// AdminUpdateUserRole PUT /admin/users/:id/role 变更用户角色
func (a *App) AdminUpdateUserRole(c *gin.Context) {
	var req struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Role != "admin" && req.Role != "user") {
		fail(c, http.StatusBadRequest, "角色只能为 admin 或 user")
		return
	}
	u := a.findManagedUser(c)
	if u == nil {
		return
	}
	// 将唯一的启用管理员降级会导致无人可管理后台
	if u.Role == "admin" && req.Role != "admin" && u.IsActive && a.activeAdminCount() <= 1 {
		fail(c, http.StatusBadRequest, "至少需保留一位启用状态的管理员")
		return
	}
	oldRole := u.Role
	if err := a.DB.Model(u).Update("role", req.Role).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	u.Role = req.Role
	if oldRole != req.Role {
		a.recordAudit(c, "user.role_updated", "user", auditID(u.ID), u.Username, map[string]any{
			"role": map[string]any{"from": oldRole, "to": req.Role},
		})
	}
	ok(c, u)
}

// AdminUpdateUserStatus PUT /admin/users/:id/status 启用/停用用户
func (a *App) AdminUpdateUserStatus(c *gin.Context) {
	var req struct {
		IsActive *bool `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.IsActive == nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u := a.findManagedUser(c)
	if u == nil {
		return
	}
	if !*req.IsActive && u.Role == "admin" && u.IsActive && a.activeAdminCount() <= 1 {
		fail(c, http.StatusBadRequest, "至少需保留一位启用状态的管理员")
		return
	}
	oldStatus := u.IsActive
	if err := a.DB.Model(u).Update("is_active", *req.IsActive).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	u.IsActive = *req.IsActive
	if oldStatus != *req.IsActive {
		a.recordAudit(c, "user.status_updated", "user", auditID(u.ID), u.Username, map[string]any{
			"is_active": map[string]any{"from": oldStatus, "to": *req.IsActive},
		})
	}
	ok(c, u)
}

// AdminDeleteUser DELETE /admin/users/:id 删除用户（拥有书籍者需先转移或删除书籍）
func (a *App) AdminDeleteUser(c *gin.Context) {
	u := a.findManagedUser(c)
	if u == nil {
		return
	}
	var bookCount int64
	a.DB.Unscoped().Model(&models.Book{}).Where("user_id = ?", u.ID).Count(&bookCount)
	if bookCount > 0 {
		fail(c, http.StatusBadRequest, "该用户仍拥有书籍，请先删除其书籍或改为停用账户")
		return
	}
	// 先清理该用户的关联数据，再删用户：既避免外键约束（如 user_authentications）导致删除失败，
	// 也确保不残留孤立的个人数据。用户已无自有书籍，故不涉及书籍/章节本身。
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		related := []any{
			&models.UserAuthentication{}, &models.Notification{}, &models.BookCollaborator{},
			&models.PasswordResetToken{}, &models.LoginChallenge{}, &models.TwoFactorStepUp{},
			&models.UserNotificationPref{}, &models.TwoFactorBackupCode{}, &models.EmailVerificationToken{},
			&models.Comment{}, &models.Reaction{}, &models.ReadingProgress{}, &models.ReadChapter{},
			&models.ReadingAnnotation{}, &models.UserExportSetting{}, &models.UserReadingGoal{},
			&models.ReadingDailyTime{}, &models.UserThemeSetting{}, &models.WriterComment{}, &models.WriterSuggestion{},
		}
		related = append(related, plugincore.UserDataModels()...) // 插件登记的用户归属表（如成就）
		for _, m := range related {
			// 插件独占表（如成就）在插件未启用时不存在，跳过其清理
			if !tx.Migrator().HasTable(m) {
				continue
			}
			if err := tx.Unscoped().Where("user_id = ?", u.ID).Delete(m).Error; err != nil {
				return err
			}
		}
		// AI 用量记录保留（站点统计与成本核算需要），只解除与账号的关联
		if err := tx.Model(&models.AIUsageLog{}).Where("user_id = ?", u.ID).Update("user_id", 0).Error; err != nil {
			return err
		}
		return tx.Delete(u).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	a.recordAudit(c, "user.deleted", "user", auditID(u.ID), u.Username, changedFields("account_deleted"))
	ok(c, gin.H{"message": "已删除"})
}

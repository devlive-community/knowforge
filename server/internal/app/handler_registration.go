package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
)

// 注册设置（存 site_configs）：
//   - registration_mode：open | open_invite | invite | closed
//   - registration_require_email：注册是否必须绑定邮箱
//   - registration_require_email_activation：注册后是否必须激活邮箱（需先开启绑定邮箱）
const (
	cfgRegistrationMode     = "registration_mode"
	cfgRegRequireEmail      = "registration_require_email"
	cfgRegRequireActivation = "registration_require_email_activation"
	registrationMigratedKey = "registration_migrated"
)

var registrationModes = map[string]bool{"open": true, "open_invite": true, "invite": true, "closed": true}

const emailVerificationTTL = 24 * time.Hour

func (a *App) registrationMode() string {
	m := a.getSetting(cfgRegistrationMode)
	if !registrationModes[m] {
		return "open"
	}
	return m
}

func (a *App) regRequireEmail() bool { return a.getSetting(cfgRegRequireEmail) == "true" }

func (a *App) regRequireActivation() bool {
	return a.regRequireEmail() && a.getSetting(cfgRegRequireActivation) == "true"
}

// inviteCodeAlphabet 去除易混字符（0/O、1/I 等）
const inviteCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomCode(n int) string {
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(inviteCodeAlphabet))))
		if err != nil {
			return ""
		}
		b[i] = inviteCodeAlphabet[idx.Int64()]
	}
	return string(b)
}

// ensureInviteCode 保证用户有唯一邀请码（应用层去重），返回该码。
func (a *App) ensureInviteCode(u *models.User) string {
	if u.InviteCode != "" {
		return u.InviteCode
	}
	for i := 0; i < 8; i++ {
		code := randomCode(8)
		if code == "" {
			continue
		}
		var n int64
		a.DB.Model(&models.User{}).Where("invite_code = ?", code).Count(&n)
		if n == 0 {
			if err := a.DB.Model(u).Update("invite_code", code).Error; err == nil {
				u.InviteCode = code
				return code
			}
		}
	}
	return u.InviteCode
}

// inviterByCode 按邀请码找邀请人；仅匹配已启用的邀请码，返回 nil 表示无效码。
func (a *App) inviterByCode(code string) *models.User {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil
	}
	var owner models.User
	if err := a.DB.Where("invite_code = ? AND invite_code_enabled = ?", code, true).First(&owner).Error; err != nil {
		return nil
	}
	return &owner
}

// inviteCodePattern 自定义邀请码：字母数字 4-20 位
var inviteCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{4,20}$`)

// ---- 管理员：注册设置 ----

type registrationSettings struct {
	Mode              string `json:"mode"`
	RequireEmail      bool   `json:"require_email"`
	RequireActivation bool   `json:"require_activation"`
}

// PublicRegistrationInfo GET /auth/registration 注册/登录页据此决定是否显示邀请码、是否必填邮箱、是否关闭注册、密码策略。
func (a *App) PublicRegistrationInfo(c *gin.Context) {
	ok(c, gin.H{
		"mode":                   a.registrationMode(),
		"require_email":          a.regRequireEmail(),
		"password_min_length":    a.passwordMinLength(),
		"password_require_mixed": a.passwordRequireMixed(),
	})
}

// GetRegistrationSettings GET /registration（管理员）
func (a *App) GetRegistrationSettings(c *gin.Context) {
	ok(c, registrationSettings{
		Mode:              a.registrationMode(),
		RequireEmail:      a.getSetting(cfgRegRequireEmail) == "true",
		RequireActivation: a.getSetting(cfgRegRequireActivation) == "true",
	})
}

// UpdateRegistrationSettings PUT /admin/registration
func (a *App) UpdateRegistrationSettings(c *gin.Context) {
	var req registrationSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if !registrationModes[req.Mode] {
		fail(c, http.StatusBadRequest, "注册方式无效")
		return
	}
	boolStr := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	_ = a.setSetting(cfgRegistrationMode, req.Mode, "注册方式：open|open_invite|invite|closed")
	_ = a.setSetting(cfgRegRequireEmail, boolStr(req.RequireEmail), "注册是否必须绑定邮箱")
	_ = a.setSetting(cfgRegRequireActivation, boolStr(req.RequireActivation), "注册后是否必须激活邮箱")
	a.GetRegistrationSettings(c)
}

// ---- 用户：我的邀请码 ----

// MyInviteCode GET /auth/invite-code 返回当前用户的邀请码与启用状态。
func (a *App) MyInviteCode(c *gin.Context) {
	u := currentUser(c)
	ok(c, gin.H{"invite_code": u.InviteCode, "enabled": u.InviteCodeEnabled})
}

// EnableInviteCode POST /auth/invite-code 开启邀请码。
// 首次可选自定义 {code}（字母数字 4-20，全站唯一，只能设置一次）；不传则自动生成。
// 已有邀请码时忽略 code、原样启用（一经设置不再变化）。
func (a *App) EnableInviteCode(c *gin.Context) {
	u := currentUser(c)
	var req struct {
		Code string `json:"code"`
	}
	_ = c.ShouldBindJSON(&req)
	custom := strings.ToUpper(strings.TrimSpace(req.Code))

	if u.InviteCode == "" {
		if custom != "" {
			if !inviteCodePattern.MatchString(custom) {
				fail(c, http.StatusBadRequest, "邀请码需为 4-20 位字母或数字")
				return
			}
			var n int64
			a.DB.Model(&models.User{}).Where("invite_code = ?", custom).Count(&n)
			if n > 0 {
				fail(c, http.StatusConflict, "该邀请码已被占用")
				return
			}
			if err := a.DB.Model(u).Update("invite_code", custom).Error; err != nil {
				fail(c, http.StatusInternalServerError, "设置失败")
				return
			}
			u.InviteCode = custom
		} else {
			a.ensureInviteCode(u)
		}
	} else if custom != "" && custom != u.InviteCode {
		fail(c, http.StatusBadRequest, "邀请码只能设置一次，无法修改")
		return
	}

	a.DB.Model(u).Update("invite_code_enabled", true)
	u.InviteCodeEnabled = true
	ok(c, gin.H{"invite_code": u.InviteCode, "enabled": true})
}

// MyInvitedUsers GET /auth/invited 我邀请的用户列表（referral），关闭邀请码也可查看。
func (a *App) MyInvitedUsers(c *gin.Context) {
	u := currentUser(c)
	page, pageSize := paginate(c)
	query := a.DB.Model(&models.User{}).Where("invited_by = ?", u.ID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询邀请记录失败")
		return
	}
	if total > 0 {
		lastPage := int((total + int64(pageSize) - 1) / int64(pageSize))
		if page > lastPage {
			page = lastPage
		}
	}
	var users []models.User
	if err := query.Order("created_at DESC, id DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&users).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询邀请记录失败")
		return
	}
	items := make([]gin.H, 0, len(users))
	for _, x := range users {
		items = append(items, gin.H{"username": x.Username, "nickname": x.Nickname, "display_name": x.PublicName(), "avatar": x.Avatar, "created_at": x.CreatedAt})
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// DisableInviteCode DELETE /auth/invite-code 停用邀请码（保留 InviteCode，再开启仍是同一个）。
func (a *App) DisableInviteCode(c *gin.Context) {
	u := currentUser(c)
	if err := a.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("invite_code_enabled", false).Error; err != nil {
		fail(c, http.StatusInternalServerError, "关闭失败")
		return
	}
	ok(c, gin.H{"invite_code": u.InviteCode, "enabled": false})
}

// ---- 邮箱激活 ----

func (a *App) sendActivationEmail(c *gin.Context, u *models.User) {
	if u.Email == "" {
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	a.DB.Where("user_id = ? AND used_at IS NULL", u.ID).Delete(&models.EmailVerificationToken{})
	if err := a.DB.Create(&models.EmailVerificationToken{
		UserID:    u.ID,
		TokenHash: hex.EncodeToString(sum[:]),
		ExpiresAt: currentTime().Add(emailVerificationTTL),
	}).Error; err != nil {
		return
	}
	link := a.resetLinkBase(c) + "/verify-email?token=" + token
	if err := a.sendActionEmail(c, u, u.Email, "verify", link, int(emailVerificationTTL.Minutes())); err != nil {
		log.Printf("[mail] 创建邮箱激活邮件任务失败 to=%s: %v", u.Email, err)
	}
}

type verifyEmailRequest struct {
	Token string `json:"token"`
}

// VerifyEmail POST /auth/email/verify 匿名凭令牌激活邮箱。
func (a *App) VerifyEmail(c *gin.Context) {
	var req verifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Token) == "" {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(req.Token)))
	var t models.EmailVerificationToken
	if err := a.DB.Where("token_hash = ?", hex.EncodeToString(sum[:])).First(&t).Error; err != nil {
		fail(c, http.StatusBadRequest, "激活链接无效")
		return
	}
	if t.UsedAt != nil || currentTime().After(t.ExpiresAt) {
		fail(c, http.StatusBadRequest, "激活链接已失效，请重新发送")
		return
	}
	a.DB.Model(&t).Update("used_at", currentTime())
	a.DB.Model(&models.User{}).Where("id = ?", t.UserID).Update("email_verified", true)
	a.emitActivity(t.UserID, "account.email_verified", "user", strconv.FormatUint(uint64(t.UserID), 10), fmt.Sprintf("account.email_verified:%d", t.UserID))
	ok(c, gin.H{"verified": true})
}

// ResendActivation POST /auth/email/resend 登录用户重新发送激活邮件。
func (a *App) ResendActivation(c *gin.Context) {
	u := currentUser(c)
	if u.EmailVerified {
		ok(c, gin.H{"message": "邮箱已激活"})
		return
	}
	if u.Email == "" {
		fail(c, http.StatusBadRequest, "请先绑定邮箱")
		return
	}
	a.sendActivationEmail(c, u)
	ok(c, gin.H{"message": "激活邮件已发送，请查收"})
}

// RequireEmailVerified 写操作前置守卫：开启「注册后必须激活邮箱」且当前用户未激活时拒绝。
func (a *App) RequireEmailVerified() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.regRequireActivation() {
			u := currentUser(c)
			if u != nil && !u.EmailVerified {
				fail(c, http.StatusForbidden, "请先激活邮箱后再进行此操作")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

// migrateRegistrationDefaults 首次引入注册功能时：把已有用户视为已激活并补发邀请码（只跑一次）。
func (a *App) migrateRegistrationDefaults() {
	if a.DB == nil || a.getSetting(registrationMigratedKey) == "true" {
		return
	}
	a.DB.Model(&models.User{}).Where("email_verified = ?", false).Update("email_verified", true)
	_ = a.setSetting(registrationMigratedKey, "true", "注册功能数据迁移标记（已有用户视为已激活）")
}

// migrateInviteEnabled 引入邀请码启用状态后：已有邀请码的用户视为已启用（只跑一次）。
func (a *App) migrateInviteEnabled() {
	if a.DB == nil || a.getSetting("invite_enabled_migrated") == "true" {
		return
	}
	a.DB.Model(&models.User{}).Where("invite_code <> ?", "").Update("invite_code_enabled", true)
	_ = a.setSetting("invite_enabled_migrated", "true", "邀请码启用状态迁移标记")
}

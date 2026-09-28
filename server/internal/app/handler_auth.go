package app

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func (a *App) issueToken(c *gin.Context, u *models.User) {
	token, err := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
	if err != nil {
		fail(c, http.StatusInternalServerError, "签发令牌失败")
		return
	}
	// 同步下发 Cookie：SSR 页面据此在服务端渲染登录态，避免刷新闪烁
	c.SetCookie("knowforge_token", token, 7*24*3600, "/", "", false, false)
	ok(c, gin.H{"token": token, "user": u})
}

type registerRequest struct {
	Username      string `json:"username"`
	Email         string `json:"email"`
	Password      string `json:"password"`
	InviteCode    string `json:"invite_code"`
	CaptchaID     string `json:"captcha_id"`
	CaptchaAnswer string `json:"captcha_answer"`
}

// Register POST /auth/register
func (a *App) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := a.checkCaptcha("register", req.CaptchaID, req.CaptchaAnswer); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	// 注册方式门禁：关闭注册直接拒绝；邀请制校验邀请码
	mode := a.registrationMode()
	if mode == "closed" {
		fail(c, http.StatusForbidden, "站点当前已关闭注册")
		return
	}
	inviteRequired := mode == "invite" || mode == "open_invite"
	code := strings.TrimSpace(req.InviteCode)
	if inviteRequired && code == "" {
		fail(c, http.StatusBadRequest, "请输入邀请码")
		return
	}
	var inviter *models.User
	if code != "" {
		if inviter = a.inviterByCode(code); inviter == nil {
			fail(c, http.StatusBadRequest, "邀请码无效")
			return
		}
	}
	// 必须绑定邮箱时校验
	if a.regRequireEmail() && strings.TrimSpace(req.Email) == "" {
		fail(c, http.StatusBadRequest, "请绑定邮箱后再注册")
		return
	}

	if !usernameRegex.MatchString(req.Username) {
		fail(c, http.StatusBadRequest, "用户名需为 3-50 位字母、数字、下划线或中划线")
		return
	}
	if req.Email != "" && !emailRegex.MatchString(req.Email) {
		fail(c, http.StatusBadRequest, "邮箱格式不正确")
		return
	}
	if err := a.validatePassword(req.Password); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	var count int64
	a.DB.Model(&models.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		fail(c, http.StatusConflict, "用户名已被占用")
		return
	}
	if req.Email != "" {
		a.DB.Model(&models.User{}).Where("email = ?", req.Email).Count(&count)
		if count > 0 {
			fail(c, http.StatusConflict, "邮箱已被占用")
			return
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, http.StatusInternalServerError, "密码加密失败")
		return
	}
	u := models.User{
		Username: req.Username,
		Email:    req.Email,
		Password: string(hash),
		Role:     "user",
		IsActive: true,
		// 需要激活邮箱时，注册后未激活（只读）；否则视为已激活
		EmailVerified: !a.regRequireActivation(),
		// 注册时的界面语言记为偏好语言（之后的通知邮件等与注册时语言一致）
		PreferredLocale: a.signupLocale(c, a.DB),
	}
	if inviter != nil {
		u.InvitedBy = inviter.ID
	}
	if err := a.DB.Create(&u).Error; err != nil {
		fail(c, http.StatusInternalServerError, "注册失败: "+err.Error())
		return
	}
	// 邀请码为用户自主开启（opt-in），注册时不自动生成
	if a.regRequireActivation() {
		a.sendActivationEmail(c, &u)
	}
	a.emitActivity(u.ID, "account.registered", "user", strconv.FormatUint(uint64(u.ID), 10), fmt.Sprintf("account.registered:%d", u.ID))
	if inviter != nil {
		a.emitActivity(inviter.ID, "account.invited_user", "user", strconv.FormatUint(uint64(u.ID), 10), fmt.Sprintf("account.invited_user:%d", u.ID))
	}
	a.issueToken(c, &u)
}

type loginRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	CaptchaID     string `json:"captcha_id"`
	CaptchaAnswer string `json:"captcha_answer"`
	TwoFactorCode string `json:"two_factor_code"`
	LoginToken    string `json:"login_token"` // 两步登录第二步：凭挑战 token 提交动态码，不再校验验证码/密码
}

// Login POST /auth/login
func (a *App) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}

	// 第二步：凭 login_token + 动态码完成登录（第一步已校验验证码与密码，此处不再重复，避免验证码被消费后报错）。
	if strings.TrimSpace(req.LoginToken) != "" {
		lc, valid := a.findLoginChallenge(req.LoginToken)
		if !valid {
			fail(c, http.StatusUnauthorized, "登录会话已过期，请重新登录")
			return
		}
		var u models.User
		if err := a.DB.First(&u, lc.UserID).Error; err != nil {
			fail(c, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		if !u.IsActive {
			fail(c, http.StatusForbidden, "账户已被禁用")
			return
		}
		if !a.validateUserTOTP(&u, req.TwoFactorCode) {
			fail(c, http.StatusUnauthorized, "二次认证码错误") // 挑战未删除，可重试
			return
		}
		a.DB.Delete(&models.LoginChallenge{}, "id = ?", lc.ID)
		a.DB.Model(&u).Update("last_login_at", currentTime())
		a.issueToken(c, &u)
		return
	}

	// 第一步：用户名 + 密码 + 验证码
	if req.Username == "" || req.Password == "" {
		fail(c, http.StatusBadRequest, "请输入用户名和密码")
		return
	}
	if err := a.checkCaptcha("login", req.CaptchaID, req.CaptchaAnswer); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}

	var u models.User
	if err := a.DB.Where("username = ? OR email = ?", req.Username, req.Username).First(&u).Error; err != nil {
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	// 登录失败锁定：账户锁定期内直接拒绝
	if rem := a.loginLockRemaining(u.Username); rem > 0 {
		fail(c, http.StatusTooManyRequests, fmt.Sprintf("登录失败次数过多，账户已锁定，请 %d 分钟后再试", int(rem.Minutes())+1))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)) != nil {
		a.recordLoginFailure(u.Username)
		fail(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	a.clearLoginFailures(u.Username)
	if !u.IsActive {
		fail(c, http.StatusForbidden, "账户已被禁用")
		return
	}

	// 登录二次认证：开启且勾选「登录」时，验证码与密码通过后下发挑战，前端弹层输入动态码走第二步。
	if u.TwoFactorEnabled && tfOpEnabled(u.TwoFactorOps, tfOpLogin) {
		ok(c, gin.H{"two_factor_required": true, "login_token": a.createLoginChallenge(u.ID)})
		return
	}

	a.DB.Model(&u).Update("last_login_at", currentTime())
	a.issueToken(c, &u)
}

// Me GET /auth/me
func (a *App) Me(c *gin.Context) {
	u := *currentUser(c)
	u.Entitlements = a.entitlementValues(&u)
	ok(c, u)
}

type profileUpdate struct {
	Email     *string `json:"email"`
	Avatar    *string `json:"avatar"`
	Bio       *string `json:"bio"`
	GithubURL *string `json:"github_url"`
	Nickname  *string `json:"nickname"`
	// NameDisplay 名字显示方式：nickname（优先昵称）| username（始终用户名）
	NameDisplay *string `json:"name_display"`
	Website     *string `json:"website"`
	Location    *string `json:"location"`
	Company     *string `json:"company"`
}

// UpdateProfile PUT /auth/profile
func (a *App) UpdateProfile(c *gin.Context) {
	var req profileUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u := currentUser(c)

	if req.Email != nil {
		if *req.Email != "" && !emailRegex.MatchString(*req.Email) {
			fail(c, http.StatusBadRequest, "邮箱格式不正确")
			return
		}
		if *req.Email != u.Email {
			if !a.requireStepUp(c, tfOpCredentials) {
				return
			}
			var count int64
			a.DB.Model(&models.User{}).Where("email = ? AND id != ?", *req.Email, u.ID).Count(&count)
			if count > 0 {
				fail(c, http.StatusConflict, "邮箱已被占用")
				return
			}
		}
		u.Email = *req.Email
	}
	if req.Avatar != nil {
		u.Avatar = *req.Avatar
	}
	if req.Bio != nil {
		u.Bio = *req.Bio
	}
	if req.GithubURL != nil {
		u.GithubURL = *req.GithubURL
	}
	if req.Nickname != nil {
		u.Nickname = truncateText(strings.TrimSpace(*req.Nickname), 50)
	}
	if req.Website != nil {
		u.Website = truncateText(strings.TrimSpace(*req.Website), 255)
	}
	if req.Location != nil {
		u.Location = truncateText(strings.TrimSpace(*req.Location), 100)
	}
	if req.NameDisplay != nil {
		switch *req.NameDisplay {
		case models.NameDisplayNickname, models.NameDisplayUsername:
			u.NameDisplay = *req.NameDisplay
		default:
			fail(c, http.StatusBadRequest, "名字显示方式需为 nickname 或 username")
			return
		}
	}
	if req.Company != nil {
		u.Company = truncateText(strings.TrimSpace(*req.Company), 100)
	}
	if err := a.DB.Save(u).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	u.DisplayName = u.PublicName()
	ok(c, u)
}

type passwordUpdate struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// ChangePassword PUT /auth/password
func (a *App) ChangePassword(c *gin.Context) {
	if !a.requireStepUp(c, tfOpCredentials) {
		return
	}
	var req passwordUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := a.validatePassword(req.NewPassword); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	u := currentUser(c)
	// OAuth 注册的用户没有本地密码，首次设置免验原密码
	if u.Password != "" && bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.OldPassword)) != nil {
		fail(c, http.StatusBadRequest, "原密码不正确")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		fail(c, http.StatusInternalServerError, "密码加密失败")
		return
	}
	if err := a.DB.Model(u).Update("password", string(hash)).Error; err != nil {
		fail(c, http.StatusInternalServerError, "修改失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "密码已更新"})
}

package app

import (
	"net/http"
	"strings"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/authz"
	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
)

// CORS 允许跨域，便于桌面端与开发模式下的前端访问
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-KnowForge-Locale, X-Collab-Conn")
		c.Header("Access-Control-Expose-Headers", "Retry-After, X-RateLimit-Limit, X-RateLimit-Remaining")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// currentUser 从上下文取出可选登录用户（配合 RequireAuth / OptionalAuth 使用）
func currentUser(c *gin.Context) *models.User {
	if v, ok := c.Get("user"); ok {
		if u, ok := v.(*models.User); ok {
			return u
		}
	}
	return nil
}

// RequireAuth 强制登录中间件
func (a *App) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := a.resolveUser(c)
		if abortTokenDenied(c) {
			return
		}
		if u == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "请先登录"})
			return
		}
		if _, restricted := tokenLimits(c); restricted && !chainDeclaresPermission(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "code": "TOKEN_FORBIDDEN", "message": "该接口没有登记所需权限，只能使用「全部权限」的访问令牌调用"})
			return
		}
		c.Set("user", u)
		c.Next()
	}
}

// RequireAuthStream 供 SSE（EventSource 无法带请求头）使用的登录校验：请求头/Cookie 之外接受 ?ticket= 短时事件流凭证
// （POST /stream-tickets 签发），不接受 URL 中的登录令牌。只应挂在只读的事件流接口上。
func (a *App) RequireAuthStream() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := a.streamUser(c)
		if u == nil {
			failStream(c)
			return
		}
		c.Set("user", u)
		c.Next()
	}
}

// OptionalAuth 尝试解析登录态但不强制
func (a *App) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := a.resolveUser(c)
		if abortTokenDenied(c) { // 显式带了访问令牌但不允许本次请求：拒绝而不是按游客处理
			return
		}
		if _, restricted := tokenLimits(c); restricted && !chainDeclaresPermission(c) {
			u = nil // 限定权限的令牌调用未声明权限的公开接口：按游客处理
		}
		if u != nil {
			c.Set("user", u)
		}
		c.Next()
	}
}

// abortTokenDenied 访问令牌有效但不允许本次请求时返回 403。
func abortTokenDenied(c *gin.Context) bool {
	if reason := c.GetString("access_token_denied"); reason != "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "code": "TOKEN_FORBIDDEN", "message": reason})
		return true
	}
	return false
}

func (a *App) resolveUser(c *gin.Context) *models.User {
	token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	if token == "" {
		// SSR 场景：浏览器同源请求自动携带 Cookie。改名后优先读新 Cookie，
		// 回退旧 Cookie infosphere_token，避免升级后既有登录会话被强制登出。
		if ck, err := c.Cookie("knowforge_token"); err == nil {
			token = strings.TrimSpace(ck)
		} else if ck, err := c.Cookie("infosphere_token"); err == nil {
			token = strings.TrimSpace(ck)
		}
	}
	if token == "" || a.Config.Secret == "" {
		return nil
	}
	if strings.HasPrefix(token, accessTokenPrefix) { // 个人访问令牌（只接受请求头，Cookie 中只会是登录令牌）
		if c.GetHeader("Authorization") == "" {
			return nil
		}
		u, denied := a.resolveAccessToken(c, token)
		if denied != "" {
			c.Set("access_token_denied", denied)
		}
		return u
	}
	claims, err := auth.ParseToken(a.Config.Secret, token)
	if err != nil {
		return nil
	}
	var u models.User
	if err := a.DB.First(&u, claims.UserID).Error; err != nil {
		return nil
	}
	if !u.IsActive {
		return nil
	}
	return &u
}

// IsAdmin 判断用户是否管理员
func IsAdmin(u *models.User) bool {
	return u != nil && u.Role == "admin"
}

// RequireAdmin 强制管理员权限中间件
func (a *App) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := currentUser(c)
		if !IsAdmin(u) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

// RequirePermission 校验当前用户是否拥有指定权限（resource:action）
func (a *App) RequirePermission(perm authz.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := currentUser(c)
		if u == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "请先登录"})
			return
		}
		if !authz.Has(u.Role, perm) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"code":    "PERMISSION_DENIED",
				"message": "权限不足，需要 " + string(perm),
			})
			return
		}
		if lim, restricted := tokenLimits(c); restricted && !lim.allows(perm) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "code": "TOKEN_FORBIDDEN", "message": lim.denyMessage(perm)})
			return
		}
		c.Next()
	}
}

// CurrentPermissions 返回当前用户的权限列表
func (a *App) CurrentPermissions(c *gin.Context) {
	u := currentUser(c)
	perms := authz.ForRole(u.Role)
	if lim, restricted := tokenLimits(c); restricted { // 受限令牌：只返回令牌可使用的权限
		kept := []authz.Permission{}
		for _, p := range perms {
			if lim.allows(p) {
				kept = append(kept, p)
			}
		}
		perms = kept
	}
	ok(c, perms)
}

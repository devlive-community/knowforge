package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 个人访问令牌：用户在账号设置中生成，供脚本、CI 等以 Authorization: Bearer kf_pat_… 调用 API。
//   - 权限范围：all（全部权限，可调用普通用户能调用的全部接口）或 custom（只允许勾选的权限，resource:action）；
//     custom 令牌只能调用声明了所需权限的接口（路由上的 RequirePermission / DeclarePermission）：
//     未声明权限的登录接口一律拒绝，未声明权限的公开接口按游客处理，避免新接口在未登记权限时被越权调用；
//     旧令牌的 read（只能 GET/HEAD）与 write（等同 all）继续有效；
//   - 令牌从不具备管理员等角色权限（按普通用户鉴权），也不能访问 /auth 下的账号安全接口（密码、二次认证、注销、令牌管理等）；
//   - 只保存摘要，明文只在创建时返回一次；可设有效期、随时吊销，记录最近使用时间与 IP；
//   - 每人的令牌数为权益 api.tokens_max（基础 10 个），可由会员与等级提升，设为 0 即不开放 API 访问。

const (
	accessTokenPrefix  = "kf_pat_"
	entAPITokensMax    = "api.tokens_max"
	cfgAPITokensMax    = "entitlement_api_tokens_max"
	defaultTokensMax   = 10
	tokenScopeAll      = "all"
	tokenScopeCustom   = "custom"
	tokenScopeRead     = "read"  // 旧令牌：只读
	tokenScopeWrite    = "write" // 旧令牌：等同 all
	ctxTokenScopes     = "access_token_scopes"
	tokenTouchInterval = time.Minute // 最近使用时间的最小更新间隔
	tokenAlphabet      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

func init() {
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entAPITokensMax, Kind: plugincore.EntitlementLimit, Unit: "keys", Min: 0, Max: 1000, AllowUnlimited: true, Order: 32,
		Base: func(core plugincore.Core) int64 { return settingLimit(core, cfgAPITokensMax, defaultTokensMax) },
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgAPITokensMax, strconv.FormatInt(v, 10), "权益：个人访问令牌数量（基础）")
		},
	})
}

func hashAccessToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newAccessToken() (string, error) {
	var b strings.Builder
	b.WriteString(accessTokenPrefix)
	max := big.NewInt(int64(len(tokenAlphabet)))
	for i := 0; i < 40; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(tokenAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// tokenAccessDenied 令牌能否访问本次请求（返回拒绝原因，空表示允许）。
func tokenAccessDenied(c *gin.Context, t *models.PersonalAccessToken) string {
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/api/v1/auth/") {
		if tokenIdentityRequest(c) {
			return ""
		}
		return "访问令牌不能用于账号与安全设置，请在网页中登录后操作"
	}
	if t.Scope == tokenScopeRead && c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return "只读访问令牌不能修改数据"
	}
	return ""
}

// tokenIdentityRequest 查询令牌身份的接口（当前用户与其权限），任何令牌都可调用。
func tokenIdentityRequest(c *gin.Context) bool {
	path := c.Request.URL.Path
	return c.Request.Method == http.MethodGet && (path == "/api/v1/auth/me" || path == "/api/v1/auth/permissions")
}

// tokenScopes 本次请求所用限定权限令牌的权限集合；未使用令牌或令牌为全部权限时 restricted 为 false。
func tokenScopes(c *gin.Context) (scopes map[authz.Permission]bool, restricted bool) {
	v, ok := c.Get(ctxTokenScopes)
	if !ok {
		return nil, false
	}
	scopes, restricted = v.(map[authz.Permission]bool)
	return scopes, restricted
}

// chainDeclaresPermission 本路由的处理链是否声明了所需权限（RequirePermission / DeclarePermission）；身份查询接口视为已声明。
func chainDeclaresPermission(c *gin.Context) bool {
	if tokenIdentityRequest(c) {
		return true
	}
	for _, name := range c.HandlerNames() {
		if strings.Contains(name, ".RequirePermission.func") || strings.Contains(name, ".DeclarePermission.func") {
			return true
		}
	}
	return false
}

// DeclarePermission 为公开接口声明语义权限（对登录会话与全部权限令牌不生效）：
// 限定权限的令牌缺少该权限时按游客处理本次请求（只能看到公开内容）。
func (a *App) DeclarePermission(perm authz.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if scopes, restricted := tokenScopes(c); restricted && !scopes[perm] {
			c.Set("user", (*models.User)(nil))
		}
		c.Next()
	}
}

// tokenPermissionCatalog 令牌可选的权限：普通用户当前拥有的权限（含已启用插件），不含账号安全类（auth:*）。
// 核心权限保持登记顺序，插件权限按名称排序。
func tokenPermissionCatalog() []authz.Permission {
	out := []authz.Permission{}
	seen := map[authz.Permission]bool{}
	var dynamic []authz.Permission
	static := map[authz.Permission]bool{}
	for _, p := range authz.All {
		static[p] = true
	}
	for _, p := range authz.ForRole("user") {
		if strings.HasPrefix(string(p), "auth:") || seen[p] {
			continue
		}
		seen[p] = true
		if static[p] {
			out = append(out, p)
		} else {
			dynamic = append(dynamic, p)
		}
	}
	sort.Slice(dynamic, func(i, j int) bool { return dynamic[i] < dynamic[j] })
	return append(out, dynamic...)
}

// TokenPermissions GET /auth/tokens/permissions 创建令牌时可选的权限（按资源分组）。
func (a *App) TokenPermissions(c *gin.Context) {
	type group struct {
		Resource    string   `json:"resource"`
		Permissions []string `json:"permissions"`
	}
	groups := []*group{}
	index := map[string]*group{}
	for _, p := range tokenPermissionCatalog() {
		res := strings.SplitN(string(p), ":", 2)[0]
		g, ok := index[res]
		if !ok {
			g = &group{Resource: res}
			index[res] = g
			groups = append(groups, g)
		}
		g.Permissions = append(g.Permissions, string(p))
	}
	ok(c, gin.H{"groups": groups})
}

// resolveAccessToken 校验个人访问令牌：有效时返回按普通用户鉴权的用户；令牌有效但不允许本次请求时 denied 非空。
func (a *App) resolveAccessToken(c *gin.Context, raw string) (u *models.User, denied string) {
	var t models.PersonalAccessToken
	now := time.Now()
	if a.DB.Where("token_hash = ?", hashAccessToken(raw)).First(&t).Error != nil || t.RevokedAt != nil || (t.ExpiresAt != nil && now.After(*t.ExpiresAt)) {
		return nil, ""
	}
	var user models.User
	if a.DB.First(&user, t.UserID).Error != nil || !user.IsActive {
		return nil, ""
	}
	if reason := tokenAccessDenied(c, &t); reason != "" {
		return nil, reason
	}
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > tokenTouchInterval {
		a.DB.Model(&models.PersonalAccessToken{}).Where("id = ?", t.ID).Updates(map[string]any{"last_used_at": now, "last_used_ip": c.ClientIP()})
	}
	user.Role = "user" // 令牌不具备管理员等角色权限
	c.Set("access_token_id", t.ID)
	if t.Scope == tokenScopeCustom {
		scopes := make(map[authz.Permission]bool, len(t.Permissions))
		for _, p := range t.Permissions {
			scopes[authz.Permission(p)] = true
		}
		c.Set(ctxTokenScopes, scopes)
	}
	return &user, ""
}

type accessTokenView struct {
	models.PersonalAccessToken
	Expired bool `json:"expired"`
}

func toTokenView(t models.PersonalAccessToken) accessTokenView {
	return accessTokenView{PersonalAccessToken: t, Expired: t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt)}
}

func (a *App) activeTokenCount(userID uint) int64 {
	var n int64
	a.DB.Model(&models.PersonalAccessToken{}).Where("user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", userID, time.Now()).Count(&n)
	return n
}

// MyAccessTokens GET /auth/tokens 我的访问令牌（含已吊销、已过期的，新→旧）与数量上限。
func (a *App) MyAccessTokens(c *gin.Context) {
	u := currentUser(c)
	var rows []models.PersonalAccessToken
	a.DB.Where("user_id = ?", u.ID).Order("id DESC").Limit(100).Find(&rows)
	items := make([]accessTokenView, 0, len(rows))
	for _, t := range rows {
		items = append(items, toTokenView(t))
	}
	ok(c, gin.H{"items": items, "active": a.activeTokenCount(u.ID), "limit": a.entitlement(u, entAPITokensMax)})
}

// CreateAccessToken POST /auth/tokens {name, scope: all|custom, permissions[]（custom 时必填）, expires_days: 0（永不过期）|7|30|90|365}
// （仍接受旧的 read / write，write 按 all 保存）
// 返回 {token（明文，只返回这一次）, item}。
func (a *App) CreateAccessToken(c *gin.Context) {
	var req struct {
		Name        string   `json:"name"`
		Scope       string   `json:"scope"`
		Permissions []string `json:"permissions"`
		ExpiresDays int      `json:"expires_days"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 100 {
		fail(c, http.StatusBadRequest, "请填写令牌名称（不超过 100 字）")
		return
	}
	var perms []string
	switch req.Scope {
	case tokenScopeAll, tokenScopeRead:
	case tokenScopeWrite:
		req.Scope = tokenScopeAll
	case tokenScopeCustom:
		allowed := map[string]bool{}
		for _, p := range tokenPermissionCatalog() {
			allowed[string(p)] = true
		}
		seen := map[string]bool{}
		for _, p := range req.Permissions {
			if !allowed[p] {
				fail(c, http.StatusBadRequest, "不支持的权限："+p)
				return
			}
			if !seen[p] {
				seen[p] = true
				perms = append(perms, p)
			}
		}
		if len(perms) == 0 {
			fail(c, http.StatusBadRequest, "请至少选择一项权限")
			return
		}
		sort.Strings(perms)
	default:
		fail(c, http.StatusBadRequest, "权限范围需为全部权限或自定义")
		return
	}
	switch req.ExpiresDays {
	case 0, 7, 30, 90, 365:
	default:
		fail(c, http.StatusBadRequest, "有效期需为 7、30、90、365 天或永不过期")
		return
	}
	u := currentUser(c)
	if limit := a.entitlement(u, entAPITokensMax); limit != plugincore.Unlimited && a.activeTokenCount(u.ID) >= limit {
		if limit == 0 {
			fail(c, http.StatusForbidden, "当前等级或会员暂不支持访问令牌")
		} else {
			fail(c, http.StatusForbidden, "访问令牌数量已达上限（"+strconv.FormatInt(limit, 10)+" 个），请先吊销不用的令牌，或升级等级、开通会员获得更多")
		}
		return
	}
	token, err := newAccessToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, "生成令牌失败")
		return
	}
	t := models.PersonalAccessToken{UserID: u.ID, Name: req.Name, Prefix: token[:len(accessTokenPrefix)+4], TokenHash: hashAccessToken(token), Scope: req.Scope, Permissions: perms}
	if req.ExpiresDays > 0 {
		at := time.Now().AddDate(0, 0, req.ExpiresDays)
		t.ExpiresAt = &at
	}
	if err := a.DB.Create(&t).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	ok(c, gin.H{"token": token, "item": toTokenView(t)})
}

// RevokeAccessToken DELETE /auth/tokens/:id 吊销令牌（立即失效，记录保留）。
func (a *App) RevokeAccessToken(c *gin.Context) {
	u := currentUser(c)
	now := time.Now()
	res := a.DB.Model(&models.PersonalAccessToken{}).Where("id = ? AND user_id = ? AND revoked_at IS NULL", c.Param("id"), u.ID).Update("revoked_at", now)
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, "令牌不存在或已吊销")
		return
	}
	ok(c, gin.H{"revoked": true})
}

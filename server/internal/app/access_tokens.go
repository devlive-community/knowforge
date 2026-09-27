package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 个人访问令牌：用户在账号设置中生成，供脚本、CI 等以 Authorization: Bearer kf_pat_… 调用 API。
//   - 只读令牌只能发起 GET/HEAD 请求；读写令牌可以调用普通接口；
//   - 令牌从不具备管理员等角色权限（按普通用户鉴权），也不能访问 /auth 下的账号安全接口（密码、二次认证、注销、令牌管理等）；
//   - 只保存摘要，明文只在创建时返回一次；可设有效期、随时吊销，记录最近使用时间与 IP；
//   - 每人的令牌数为权益 api.tokens_max（基础 10 个），可由会员与等级提升，设为 0 即不开放 API 访问。

const (
	accessTokenPrefix  = "kf_pat_"
	entAPITokensMax    = "api.tokens_max"
	cfgAPITokensMax    = "entitlement_api_tokens_max"
	defaultTokensMax   = 10
	tokenScopeRead     = "read"
	tokenScopeWrite    = "write"
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
		if c.Request.Method == http.MethodGet && (path == "/api/v1/auth/me" || path == "/api/v1/auth/permissions") {
			return ""
		}
		return "访问令牌不能用于账号与安全设置，请在网页中登录后操作"
	}
	if t.Scope != tokenScopeWrite && c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return "只读访问令牌不能修改数据"
	}
	return ""
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

// CreateAccessToken POST /auth/tokens {name, scope: read|write, expires_days: 0（永不过期）|7|30|90|365}
// 返回 {token（明文，只返回这一次）, item}。
func (a *App) CreateAccessToken(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Scope       string `json:"scope"`
		ExpiresDays int    `json:"expires_days"`
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
	if req.Scope != tokenScopeRead && req.Scope != tokenScopeWrite {
		fail(c, http.StatusBadRequest, "权限范围需为只读或读写")
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
	t := models.PersonalAccessToken{UserID: u.ID, Name: req.Name, Prefix: token[:len(accessTokenPrefix)+4], TokenHash: hashAccessToken(token), Scope: req.Scope}
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

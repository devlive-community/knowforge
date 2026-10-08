package indexnow

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/plugincore"
)

func (b *behavior) Key() string { return pluginKey }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	api.GET("/indexnow/key", core.RequireFeaturePlugin(pluginKey), b.publicKey)
	admin := []gin.HandlerFunc{core.RequireAuth(), core.RequirePermission(authz.SiteUpdate)}
	api.GET("/admin/indexnow", append(admin, b.adminStatus)...)
	api.POST("/admin/indexnow/key", append(admin, b.generateKey)...)
	api.POST("/admin/indexnow/retry", append(admin, b.retry)...)
}

func validateSetup(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("IndexNow 需要先配置根域名形式的 HTTPS 站点地址")
	}
	return nil
}

func (b *behavior) config() (Config, bool) {
	if !b.core.Gorm().Migrator().HasTable(&Config{}) {
		return Config{}, false
	}
	var cfg Config
	if err := b.core.Gorm().First(&cfg, 1).Error; err != nil || cfg.Key == "" {
		return Config{}, false
	}
	return cfg, true
}

func (b *behavior) publicKey(c *gin.Context) {
	cfg, ok := b.config()
	if !ok {
		b.core.Fail(c, http.StatusNotFound, "IndexNow 密钥未配置")
		return
	}
	b.core.OK(c, gin.H{"key": cfg.Key})
}

func (b *behavior) adminStatus(c *gin.Context) {
	base, siteErr := canonicalSiteURL(b.core.GetSetting("site_url"))
	cfg, configured := b.config()
	var pending, failed int64
	var lastSubmitted *time.Time
	if b.core.Gorm().Migrator().HasTable(&URL{}) {
		db := b.core.Gorm().Model(&URL{})
		db.Where("version > submitted_version AND attempts < ?", maxAttempts).Count(&pending)
		b.core.Gorm().Model(&URL{}).Where("version > submitted_version AND attempts >= ?", maxAttempts).Count(&failed)
		var last URL
		if b.core.Gorm().Where("last_submitted_at IS NOT NULL").Order("last_submitted_at DESC").First(&last).Error == nil {
			lastSubmitted = last.LastSubmittedAt
		}
	}
	keyFileURL := ""
	if siteErr == nil && configured {
		keyFile := *base
		keyFile.Path = "/" + cfg.Key + ".txt"
		keyFileURL = keyFile.String()
	}
	b.core.OK(c, gin.H{
		"enabled": b.core.PluginEnabled(pluginKey), "site_url_valid": siteErr == nil, "key_configured": configured,
		"key_file_url": keyFileURL, "pending": pending, "failed": failed, "last_submitted_at": lastSubmitted,
	})
}

func newKey() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (b *behavior) generateKey(c *gin.Context) {
	if !b.core.PluginEnabled(pluginKey) {
		b.core.Fail(c, http.StatusConflict, "请先在插件管理中启用 IndexNow")
		return
	}
	if err := validateSetup(b.core.GetSetting("site_url")); err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if !b.core.Gorm().Migrator().HasTable(&Config{}) {
		b.core.Fail(c, http.StatusServiceUnavailable, "IndexNow 数据表尚未准备就绪")
		return
	}
	var req struct {
		Rotate bool `json:"rotate"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			b.core.Fail(c, http.StatusBadRequest, "参数错误")
			return
		}
	}
	db := b.core.Gorm()
	var cfg Config
	if err := db.First(&cfg, 1).Error; err == nil && cfg.Key != "" && !req.Rotate {
		b.core.Fail(c, http.StatusConflict, "密钥已生成；轮换密钥需显式确认")
		return
	}
	key, err := newKey()
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "生成 IndexNow 密钥失败")
		return
	}
	now := time.Now().UTC()
	cfg = Config{ID: 1, Key: key, UpdatedAt: now}
	if err := db.Save(&cfg).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存 IndexNow 密钥失败")
		return
	}
	base, _ := canonicalSiteURL(b.core.GetSetting("site_url"))
	keyFile := *base
	keyFile.Path = "/" + key + ".txt"
	b.core.RecordAudit(c, "indexnow.key_rotated", "config", "indexnow", "IndexNow 密钥", map[string]any{"rotated": req.Rotate})
	b.core.OK(c, gin.H{"key_configured": true, "key_file_url": keyFile.String()})
}

func (b *behavior) retry(c *gin.Context) {
	if !b.core.PluginEnabled(pluginKey) {
		b.core.Fail(c, http.StatusConflict, "IndexNow 插件尚未启用")
		return
	}
	if _, ok := b.config(); !ok {
		b.core.Fail(c, http.StatusConflict, "请先生成 IndexNow 密钥")
		return
	}
	count, err := b.retryFailed(c.Request.Context())
	if err != nil {
		b.core.Fail(c, http.StatusServiceUnavailable, "重试任务排队失败")
		return
	}
	b.core.RecordAudit(c, "indexnow.retry_requested", "config", "indexnow", "IndexNow 推送", map[string]any{"failed_urls": count})
	b.core.OK(c, gin.H{"queued": count > 0, "urls": count})
}

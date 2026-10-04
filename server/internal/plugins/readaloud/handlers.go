package readaloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(pluginKey)
	use := core.RequirePermissionMiddleware(PermUse)
	api.GET("/read-aloud/status", core.RequireAuth(), feat, use, b.Status)
	api.POST("/read-aloud/docs/:id/speech", core.RequireAuth(), feat, use, b.Speech)
	admin := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage), h}
	}
	api.GET("/admin/read-aloud/settings", admin(b.AdminGetSettings)...)
	api.PUT("/admin/read-aloud/settings", admin(b.AdminUpdateSettings)...)
	api.DELETE("/admin/read-aloud/cache", admin(b.AdminClearCache)...)
}

func period(now time.Time) string { return now.Format("2006-01") }

// usage 用户本月已朗读字数与额度（-1 不限）；ok=false 表示权益不可用（如未配置语音合成）。
func (b *behavior) usage(u *models.User) (used, limit int64, ok bool) {
	r, has := plugincore.ResolveEntitlements(b.core, u)[entMonthlyChars]
	if !has || r.Source == "unavailable" {
		return 0, 0, false
	}
	var total struct{ N int64 }
	b.core.Gorm().Model(&Play{}).Select("COALESCE(SUM(chars), 0) AS n").
		Where("user_id = ? AND period = ?", u.ID, period(time.Now())).Scan(&total)
	return total.N, r.Value, true
}

// Status GET /read-aloud/status 朗读是否可用、可选音色与本月用量。
func (b *behavior) Status(c *gin.Context) {
	u := b.core.CurrentUser(c)
	model, voices := b.core.AISpeechInfo()
	used, limit, ok := b.usage(u)
	if model == "" || !ok {
		b.core.OK(c, gin.H{"available": false})
		return
	}
	b.core.OK(c, gin.H{"available": true, "voices": voices, "default_voice": voices[0], "used": used, "limit": limit})
}

var (
	mdLinkTarget = regexp.MustCompile(`\]\([^)\n]*\)`)                      // [文字](地址 "标题") 的地址部分
	mdRefDef     = regexp.MustCompile(`(?m)^\s{0,3}\[[^\]\n]+\]:\s*\S+.*$`) // 引用式链接定义
	mdHTMLTag    = regexp.MustCompile(`<[^<>\n]*>`)                         // HTML 标签（含 <https://…> 自动链接）
	mdAttrs      = regexp.MustCompile(`\{[^{}\n]*\}`)                       // {类名} 等属性块
	mdIconCode   = regexp.MustCompile(`:[a-z][a-z0-9_+-]*:`)                // :icon-name: 图标 / 表情短码
	mdInlineExt  = regexp.MustCompile(`![a-z]+\[`)                          // !btn[…] / !tip[…] 等行内扩展
)

// visibleSource 去掉 Markdown 中排版后不显示的部分（链接地址、HTML 标签、属性块、图标短码），
// 使排版后的连续文字在源文中也连续。
func visibleSource(md string) string {
	s := mdRefDef.ReplaceAllString(md, "")
	s = mdLinkTarget.ReplaceAllString(s, "]")
	s = mdHTMLTag.ReplaceAllString(s, " ")
	s = mdAttrs.ReplaceAllString(s, "")
	s = mdInlineExt.ReplaceAllString(s, "[")
	return mdIconCode.ReplaceAllString(s, "")
}

// normalize 只保留字母与数字（小写），用于判断朗读文字是否出自章节：排版只会去掉 Markdown 标记与空白，不会增加文字。
func normalize(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}

// readableDoc 读者有权收听的章节：可阅读的书中已发布的章节（作者与协作者可听草稿），付费章节需已解锁。
func (b *behavior) readableDoc(c *gin.Context, u *models.User) (*models.Document, bool) {
	db := b.core.Gorm()
	var doc models.Document
	var book models.Book
	if db.First(&doc, c.Param("id")).Error != nil || db.First(&book, doc.BookID).Error != nil || !b.core.CanReadBook(u, &book) || doc.ExternalURL != "" {
		b.core.Fail(c, http.StatusNotFound, "章节不存在")
		return nil, false
	}
	if b.core.CanEditBookContent(u, &book) {
		return &doc, true
	}
	if doc.Status != "published" {
		b.core.Fail(c, http.StatusNotFound, "章节不存在")
		return nil, false
	}
	if !plugincore.CheckContentAccess(b.core, u, &book, &doc).Allowed {
		b.core.Fail(c, http.StatusForbidden, "解锁本章后才能收听全文")
		return nil, false
	}
	return &doc, true
}

// Speech POST /read-aloud/docs/:id/speech {text, voice} 合成一段朗读音频（audio/mpeg）。
// 文字须出自该章节标题或正文；本月首次收听的段落计入朗读字数，响应头 X-Quota-Used / X-Quota-Limit 为计入后的用量。
func (b *behavior) Speech(c *gin.Context) {
	var req struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	text := strings.Join(strings.Fields(req.Text), " ")
	norm := normalize(text)
	if norm == "" {
		b.core.Fail(c, http.StatusBadRequest, "朗读内容为空")
		return
	}
	chars := len([]rune(text))
	if chars > maxSegmentRunes {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("单段朗读最多 %d 字", maxSegmentRunes))
		return
	}
	model, voices := b.core.AISpeechInfo()
	if model == "" {
		b.core.Fail(c, http.StatusServiceUnavailable, "站点尚未配置语音合成")
		return
	}
	voice := voices[0]
	for _, v := range voices {
		if v == req.Voice {
			voice = v
		}
	}
	u := b.core.CurrentUser(c)
	doc, found := b.readableDoc(c, u)
	if !found {
		return
	}
	if !strings.Contains(normalize(doc.Title+"\n"+visibleSource(doc.Content)), norm) {
		b.core.Fail(c, http.StatusBadRequest, "朗读内容不属于本章")
		return
	}

	// 计量：本月首次收听的段落按字数计入；超出额度时拒绝
	sum := sha256.Sum256([]byte(norm))
	textHash := hex.EncodeToString(sum[:])
	now := time.Now()
	used, limit, ok := b.usage(u)
	if !ok {
		b.core.Fail(c, http.StatusServiceUnavailable, "站点尚未开放朗读")
		return
	}
	var played int64
	b.core.Gorm().Model(&Play{}).Where("user_id = ? AND period = ? AND text_hash = ?", u.ID, period(now), textHash).Count(&played)
	if played == 0 && limit != plugincore.Unlimited && used+int64(chars) > limit {
		b.core.Fail(c, http.StatusTooManyRequests, fmt.Sprintf("本月朗读字数已用完（%d 字），下月恢复，或提升等级/开通会员获得更多字数", limit))
		return
	}

	ctx := ai.WithCaller(c.Request.Context(), ai.Caller{UserID: u.ID, Feature: featureSpeech, RefType: "document", RefID: doc.ID})
	audio, err := b.synthesize(ctx, cacheKey(model, voice, text), text, voice)
	if err != nil {
		b.core.Fail(c, http.StatusBadGateway, err.Error())
		return
	}
	if played == 0 {
		row := Play{UserID: u.ID, Period: period(now), TextHash: textHash, DocID: doc.ID, Chars: chars}
		if res := b.core.Gorm().Clauses(clause.OnConflict{DoNothing: true}).Create(&row); res.Error == nil && res.RowsAffected > 0 {
			used += int64(chars)
		}
	}
	c.Header("Cache-Control", "private, max-age=86400")
	c.Header("X-Quota-Used", strconv.FormatInt(used, 10))
	c.Header("X-Quota-Limit", strconv.FormatInt(limit, 10))
	c.Data(http.StatusOK, "audio/mpeg", audio)
}

// synthesize 先查缓存；未命中时合成并写入缓存。同一段音频并发请求时只合成一次，其余等待后读缓存。
func (b *behavior) synthesize(ctx context.Context, key, text, voice string) ([]byte, error) {
	for {
		if data, hit := b.cacheGet(key); hit {
			return data, nil
		}
		inflight.Lock()
		if wg, busy := inflight.m[key]; busy {
			inflight.Unlock()
			wg.Wait()
			if data, hit := b.cacheGet(key); hit {
				return data, nil
			}
			continue
		}
		wg := &sync.WaitGroup{}
		wg.Add(1)
		inflight.m[key] = wg
		inflight.Unlock()
		res, err := b.core.AISpeech(ctx, ai.SpeechRequest{Text: text, Voice: voice})
		if err == nil {
			b.cachePut(key, res.Audio)
		}
		inflight.Lock()
		delete(inflight.m, key)
		inflight.Unlock()
		wg.Done()
		if err != nil {
			return nil, err
		}
		return res.Audio, nil
	}
}

// —— 管理员 ——

func (b *behavior) settingsPayload() gin.H {
	files, total, _ := b.cacheFiles()
	model, voices := b.core.AISpeechInfo()
	return gin.H{"cache_max_mb": b.cacheMaxBytes() >> 20, "cache_files": len(files), "cache_bytes": total, "speech_model": model, "voices": voices}
}

// AdminGetSettings GET /admin/read-aloud/settings 缓存上限、当前缓存与语音合成配置概况。
func (b *behavior) AdminGetSettings(c *gin.Context) { b.core.OK(c, b.settingsPayload()) }

// AdminUpdateSettings PUT /admin/read-aloud/settings {cache_max_mb}
func (b *behavior) AdminUpdateSettings(c *gin.Context) {
	var req struct {
		CacheMaxMB *int64 `json:"cache_max_mb"`
	}
	if c.ShouldBindJSON(&req) != nil || req.CacheMaxMB == nil || *req.CacheMaxMB < 0 || *req.CacheMaxMB > maxCacheMaxMB {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("缓存上限需为 0 到 %d 之间的整数（MB）", maxCacheMaxMB))
		return
	}
	if err := b.core.SetSetting(cfgCacheMaxMB, strconv.FormatInt(*req.CacheMaxMB, 10), "AI 朗读：音频缓存上限（MB，0 为不缓存）"); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.RecordAudit(c, "readaloud.settings_updated", "readaloud", "settings", "AI 朗读", map[string]any{"cache_max_mb": *req.CacheMaxMB})
	if _, err := b.evict(*req.CacheMaxMB << 20); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "清理缓存失败")
		return
	}
	b.core.OK(c, b.settingsPayload())
}

// AdminClearCache DELETE /admin/read-aloud/cache 清空音频缓存。
func (b *behavior) AdminClearCache(c *gin.Context) {
	removed, err := b.evict(0)
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "清空缓存失败")
		return
	}
	b.core.RecordAudit(c, "readaloud.cache_cleared", "readaloud", "cache", "AI 朗读", map[string]any{"files": removed})
	b.core.OK(c, b.settingsPayload())
}

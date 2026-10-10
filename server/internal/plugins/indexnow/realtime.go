package indexnow

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// —— 实时推送：除变更队列外的三种推送方式 ——
//   - 访问时推送：公开书籍详情 / 章节被访问（SSR 取数，含搜索引擎抓取）时立即推送该页，同一地址在间隔内只推一次；
//   - 触发地址：带密钥的公开地址，访问即把积压的待推送 URL 立即推出（可接外部定时任务）；
//   - 手动推送：管理员在后台输入本站页面地址立即推送。
// 每次推送都写入推送记录，管理页可查看来源与 IndexNow 的返回结果。

const (
	sourceQueue   = "queue"
	sourceVisit   = "visit"
	sourceTrigger = "trigger"
	sourceManual  = "manual"

	defaultVisitInterval = 24
	maxVisitInterval     = 720
	maxManualURLs        = 100
	maxTriggerBatches    = 10
	triggerCooldown      = 10 * time.Second
	keepLogs             = 200
	visitCacheMax        = 5000
)

// visitSeen 进程内的访问推送节流（同一地址在间隔内只推一次；数据库记录兜底跨实例去重）。
var visitSeen = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

var triggerGate = struct {
	sync.Mutex
	last time.Time
}{}

func (b *behavior) logPush(source string, urls []string, err error) {
	if len(urls) == 0 || !b.core.Gorm().Migrator().HasTable(&PushLog{}) {
		return
	}
	row := PushLog{Source: source, URLCount: len(urls), SampleURL: truncate(urls[0], 500), Status: "ok", CreatedAt: time.Now().UTC()}
	if err != nil {
		row.Status, row.Message = "error", truncate(safeError(err.Error()), 500)
	}
	if e := b.core.Gorm().Create(&row).Error; e != nil {
		log.Printf("[indexnow] write push log failed: %v", e)
	}
}

// purgeLogs 只保留最近 keepLogs 条推送记录。
func (b *behavior) purgeLogs() {
	if !b.core.Gorm().Migrator().HasTable(&PushLog{}) {
		return
	}
	var ids []uint
	b.core.Gorm().Model(&PushLog{}).Order("id DESC").Offset(keepLogs).Limit(1).Pluck("id", &ids)
	if len(ids) == 1 {
		b.core.Gorm().Where("id <= ?", ids[0]).Delete(&PushLog{})
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func visitIntervalHours(cfg Config) int {
	if cfg.VisitIntervalHours < 1 || cfg.VisitIntervalHours > maxVisitInterval {
		return defaultVisitInterval
	}
	return cfg.VisitIntervalHours
}

func visitInterval(cfg Config) time.Duration {
	return time.Duration(visitIntervalHours(cfg)) * time.Hour
}

// onVisit 可索引页面被访问：按设置在后台立即推送该页（不阻塞页面请求）。
func (b *behavior) onVisit(kind, raw string) {
	cfg, ok := b.config()
	if !ok || !cfg.VisitPush || (kind == "book" && !cfg.VisitBooks) || (kind == "chapter" && !cfg.VisitChapters) || (kind != "book" && kind != "chapter") {
		return
	}
	interval := visitInterval(cfg)
	now := time.Now()
	visitSeen.Lock()
	if last, seen := visitSeen.at[raw]; seen && now.Sub(last) < interval {
		visitSeen.Unlock()
		return
	}
	if len(visitSeen.at) >= visitCacheMax {
		visitSeen.at = map[string]time.Time{}
	}
	visitSeen.at[raw] = now
	visitSeen.Unlock()
	go func() {
		if _, err := b.pushNow(context.Background(), sourceVisit, []string{raw}, interval); err != nil {
			log.Printf("[indexnow] visit push failed: %v", err)
		}
	}()
}

func addressHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// pushNow 立即推送一批本站 URL（不经队列）；skipWithin > 0 时跳过该时间内已推送且没有新变化的地址。
// 成功后把这些地址记为已推送（队列中待推送且未被认领的同一地址随之完成）。返回实际推送的条数。
func (b *behavior) pushNow(ctx context.Context, source string, urls []string, skipWithin time.Duration) (int, error) {
	cfg, configured := b.config()
	if !configured || !b.core.PluginEnabled(pluginKey) {
		return 0, errors.New("请先生成 IndexNow 密钥")
	}
	base, err := canonicalSiteURL(b.core.GetSetting("site_url"))
	if err != nil {
		return 0, err
	}
	db := b.core.Gorm()
	now := time.Now().UTC()
	send := make([]string, 0, len(urls))
	for _, raw := range urls {
		if skipWithin > 0 {
			var row URL
			if db.Where("address_hash = ?", addressHash(raw)).First(&row).Error == nil &&
				row.SubmittedVersion >= row.Version && row.LastSubmittedAt != nil && now.Sub(*row.LastSubmittedAt) < skipWithin {
				continue
			}
		}
		send = append(send, raw)
	}
	if len(send) == 0 {
		return 0, nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout+3*time.Second)
	defer cancel()
	if err := post(reqCtx, endpointURL, base, cfg.Key, send); err != nil {
		b.logPush(source, send, err)
		return 0, err
	}
	for _, raw := range send {
		row := URL{Address: raw, AddressHash: addressHash(raw), Version: 1, SubmittedVersion: 1, LastSubmittedAt: &now, UpdatedAt: now}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			log.Printf("[indexnow] record pushed URL failed: %v", err)
			continue
		}
		db.Model(&URL{}).Where("address_hash = ? AND claim_token = ''", row.AddressHash).
			Updates(map[string]any{"submitted_version": gorm.Expr("version"), "attempts": 0, "last_error": "", "last_submitted_at": now, "updated_at": now})
	}
	b.logPush(source, send, nil)
	return len(send), nil
}

// manualURL 规范化后台输入的地址：可为本站绝对地址或以 / 开头的站内路径；只允许本站 HTTPS 页面。
func manualURL(raw string, base *url.URL) (string, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		raw = base.Scheme + "://" + base.Host + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || !strings.EqualFold(u.Host, base.Host) || len(raw) > maxStoredURLLen {
		return "", false
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), true
}

// —— 管理端 ——

type settingsPayload struct {
	VisitPush          bool `json:"visit_push"`
	VisitBooks         bool `json:"visit_books"`
	VisitChapters      bool `json:"visit_chapters"`
	VisitIntervalHours int  `json:"visit_interval_hours"`
}

// updateSettings PUT /admin/indexnow/settings 访问时推送的设置。
func (b *behavior) updateSettings(c *gin.Context) {
	if _, ok := b.config(); !ok {
		b.core.Fail(c, http.StatusConflict, "请先生成 IndexNow 密钥")
		return
	}
	var req settingsPayload
	if c.ShouldBindJSON(&req) != nil || req.VisitIntervalHours < 1 || req.VisitIntervalHours > maxVisitInterval {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("推送间隔需为 1 到 %d 小时", maxVisitInterval))
		return
	}
	if err := b.core.Gorm().Model(&Config{}).Where("id = ?", 1).Updates(map[string]any{
		"visit_push": req.VisitPush, "visit_books": req.VisitBooks, "visit_chapters": req.VisitChapters,
		"visit_interval_hours": req.VisitIntervalHours, "updated_at": time.Now().UTC(),
	}).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.RecordAudit(c, "indexnow.settings_updated", "config", "indexnow", "IndexNow 实时推送", map[string]any{
		"visit_push": req.VisitPush, "visit_books": req.VisitBooks, "visit_chapters": req.VisitChapters, "visit_interval_hours": req.VisitIntervalHours,
	})
	b.adminStatus(c)
}

// updateTrigger POST /admin/indexnow/trigger {enabled} 开启（生成新的密钥，旧地址失效）或关闭触发地址。
func (b *behavior) updateTrigger(c *gin.Context) {
	if _, ok := b.config(); !ok {
		b.core.Fail(c, http.StatusConflict, "请先生成 IndexNow 密钥")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	token := ""
	if req.Enabled {
		var err error
		if token, err = newKey(); err != nil {
			b.core.Fail(c, http.StatusInternalServerError, "生成触发密钥失败")
			return
		}
	}
	if err := b.core.Gorm().Model(&Config{}).Where("id = ?", 1).Updates(map[string]any{"trigger_token": token, "updated_at": time.Now().UTC()}).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.RecordAudit(c, "indexnow.trigger_updated", "config", "indexnow", "IndexNow 触发地址", map[string]any{"enabled": req.Enabled})
	b.adminStatus(c)
}

// manualSubmit POST /admin/indexnow/submit {urls} 立即推送本站页面（最多 maxManualURLs 条）。
func (b *behavior) manualSubmit(c *gin.Context) {
	var req struct {
		URLs []string `json:"urls"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.URLs) == 0 {
		b.core.Fail(c, http.StatusBadRequest, "请填写要推送的页面地址")
		return
	}
	base, err := canonicalSiteURL(b.core.GetSetting("site_url"))
	if err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	valid, invalid, seen := []string{}, []string{}, map[string]bool{}
	for _, raw := range req.URLs {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		u, ok := manualURL(raw, base)
		if !ok {
			invalid = append(invalid, raw)
			continue
		}
		if !seen[u] {
			seen[u] = true
			valid = append(valid, u)
		}
	}
	if len(valid) > maxManualURLs {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("一次最多推送 %d 个地址", maxManualURLs))
		return
	}
	if len(valid) == 0 {
		b.core.Fail(c, http.StatusBadRequest, "没有可推送的本站 HTTPS 地址")
		return
	}
	sent, err := b.pushNow(c.Request.Context(), sourceManual, valid, 0)
	if err != nil {
		b.core.Fail(c, http.StatusBadGateway, err.Error())
		return
	}
	b.core.RecordAudit(c, "indexnow.manual_submitted", "config", "indexnow", "IndexNow 手动推送", map[string]any{"urls": sent})
	b.core.OK(c, gin.H{"submitted": sent, "invalid": invalid})
}

// —— 触发地址（公开，凭密钥） ——

// trigger GET/POST /indexnow/trigger?token= 立即推送积压的待推送 URL（每次最多 maxTriggerBatches 批），
// 全站每 triggerCooldown 最多执行一次。
func (b *behavior) trigger(c *gin.Context) {
	cfg, ok := b.config()
	token := c.Query("token")
	if !ok || cfg.TriggerToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(cfg.TriggerToken)) != 1 {
		b.core.Fail(c, http.StatusNotFound, "触发地址无效")
		return
	}
	triggerGate.Lock()
	if time.Since(triggerGate.last) < triggerCooldown {
		triggerGate.Unlock()
		c.Header("Retry-After", "10")
		b.core.Fail(c, http.StatusTooManyRequests, "触发过于频繁，请稍后再试")
		return
	}
	triggerGate.last = time.Now()
	triggerGate.Unlock()
	total := 0
	var lastErr error
	for i := 0; i < maxTriggerBatches; i++ {
		n, err := b.flushBatch(c.Request.Context(), sourceTrigger)
		total += n
		if err != nil {
			lastErr = err
			break
		}
		if n == 0 {
			break
		}
	}
	var remaining int64
	b.core.Gorm().Model(&URL{}).Where("version > submitted_version AND attempts < ?", maxAttempts).Count(&remaining)
	if lastErr != nil {
		b.core.Fail(c, http.StatusBadGateway, lastErr.Error())
		return
	}
	b.core.OK(c, gin.H{"submitted": total, "remaining": remaining})
}

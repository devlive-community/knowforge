package indexnow

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	requestTimeout  = 12 * time.Second
	queueCooldown   = 15 * time.Second
	maxStoredURLLen = 2048
)

var endpointURL = "https://api.indexnow.org/indexnow"

type indexNowRequest struct {
	Host        string   `json:"host"`
	Key         string   `json:"key"`
	KeyLocation string   `json:"keyLocation"`
	URLList     []string `json:"urlList"`
}

type claimedURL struct {
	row   URL
	token string
}

func newClaimToken() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

func canonicalSiteURL(raw string) (*url.URL, error) {
	if err := validateSetup(raw); err != nil {
		return nil, err
	}
	u, _ := url.Parse(strings.TrimSpace(raw))
	u.Path = ""
	return u, nil
}

func validSubmittedURL(raw string, base *url.URL) bool {
	if len(raw) > maxStoredURLLen {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return false
	}
	if !strings.EqualFold(u.Host, base.Host) || u.Path == "" || strings.Contains(u.Path, "\\") {
		return false
	}
	// 分类页（书籍分类插件）：/explore?category=<slug>，只允许这一个参数
	if u.Path == "/explore" {
		q, err := url.ParseQuery(u.RawQuery)
		return err == nil && len(q) == 1 && len(q["category"]) == 1 && q.Get("category") != ""
	}
	if u.RawQuery != "" {
		return false
	}
	parts := strings.Split(u.EscapedPath(), "/")
	if len(parts) == 4 && parts[0] == "" && parts[1] == "book" && parts[2] == "detail" {
		return parts[3] != ""
	}
	if len(parts) == 5 && parts[0] == "" && parts[1] == "book" && parts[2] == "reader" {
		return parts[3] != "" && parts[4] != ""
	}
	return false
}

func (b *behavior) recordAndEnqueue(urls []string) {
	_, ok := b.config()
	if !ok || !b.core.PluginEnabled(pluginKey) {
		return
	}
	base, err := canonicalSiteURL(b.core.GetSetting("site_url"))
	if err != nil {
		return
	}
	seen := make(map[string]struct{}, len(urls))
	db := b.core.Gorm()
	now := time.Now().UTC()
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		if !validSubmittedURL(raw, base) {
			continue
		}
		if _, exists := seen[raw]; exists {
			continue
		}
		seen[raw] = struct{}{}
		sum := sha256.Sum256([]byte(raw))
		row := URL{Address: raw, AddressHash: hex.EncodeToString(sum[:]), Version: 1, UpdatedAt: now}
		err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "address_hash"}},
			DoUpdates: clause.Assignments(map[string]any{
				"version":    gorm.Expr("version + 1"),
				"attempts":   0,
				"last_error": "",
				"updated_at": now,
			}),
		}).Create(&row).Error
		if err != nil {
			// 保存失败不能阻塞已完成的内容保存；记录诊断，后续内容变更和巡检仍可补投。
			log.Printf("[indexnow] record URL change failed: %v", err)
			continue
		}
	}
	b.enqueueIfPending(context.Background())
}

func (b *behavior) enqueueIfPending(ctx context.Context) {
	if !b.core.PluginEnabled(pluginKey) || !b.core.Gorm().Migrator().HasTable(&URL{}) {
		return
	}
	var pending int64
	if err := b.core.Gorm().Model(&URL{}).Where("version > submitted_version AND attempts < ?", maxAttempts).Count(&pending).Error; err != nil || pending == 0 {
		return
	}
	q := b.core.JobQueue()
	if q == nil {
		return
	}
	_, _, _ = q.EnqueueIfDue(ctx, jobSubmit, submitPayload{}, maxAttempts, queueCooldown)
}

func (b *behavior) purgeSubmitted(ctx context.Context, now time.Time) {
	if !b.core.Gorm().Migrator().HasTable(&URL{}) {
		return
	}
	cutoff := now.Add(-submissionKeep)
	result := b.core.Gorm().WithContext(ctx).Where(
		"submitted_version >= version AND last_submitted_at IS NOT NULL AND last_submitted_at < ?", cutoff,
	).Delete(&URL{})
	if result.Error != nil {
		log.Printf("[indexnow] purge old submissions failed: %v", result.Error)
	}
}

func (b *behavior) retryFailed(ctx context.Context) (int64, error) {
	if !b.core.Gorm().Migrator().HasTable(&URL{}) {
		return 0, nil
	}
	result := b.core.Gorm().WithContext(ctx).Model(&URL{}).
		Where("version > submitted_version AND attempts >= ?", maxAttempts).
		Updates(map[string]any{"attempts": 0, "last_error": "", "updated_at": time.Now().UTC()})
	if result.Error != nil || result.RowsAffected == 0 {
		return result.RowsAffected, result.Error
	}
	q := b.core.JobQueue()
	if q == nil {
		return result.RowsAffected, fmt.Errorf("后台任务队列尚未就绪")
	}
	_, err := q.Enqueue(ctx, jobSubmit, submitPayload{}, maxAttempts)
	return result.RowsAffected, err
}

func (b *behavior) submitPending(ctx context.Context, _ json.RawMessage) error {
	if _, err := b.flushBatch(ctx, sourceQueue); err != nil {
		return err
	}
	var pending int64
	if b.core.Gorm().Model(&URL{}).Where("version > submitted_version AND attempts < ?", maxAttempts).Count(&pending).Error == nil && pending > 0 {
		if q := b.core.JobQueue(); q != nil {
			_, _ = q.Enqueue(ctx, jobSubmit, submitPayload{}, maxAttempts)
		}
	}
	return nil
}

// flushBatch 认领一批待推送的 URL（最多 maxBatchURLs 条）并提交，返回提交成功的条数；没有待推送的返回 0。
func (b *behavior) flushBatch(ctx context.Context, source string) (int, error) {
	if !b.core.PluginEnabled(pluginKey) || !b.core.Gorm().Migrator().HasTable(&URL{}) {
		return 0, nil
	}
	cfg, configured := b.config()
	if !configured {
		return 0, nil
	}
	base, err := canonicalSiteURL(b.core.GetSetting("site_url"))
	if err != nil {
		return 0, err
	}
	claimed, err := b.claim(ctx, time.Now().UTC())
	if err != nil || len(claimed) == 0 {
		return 0, err
	}
	validClaims := claimed[:0]
	for _, item := range claimed {
		if validSubmittedURL(item.row.Address, base) {
			validClaims = append(validClaims, item)
			continue
		}
		b.skipInvalidClaim(item, time.Now().UTC())
	}
	claimed = validClaims
	if len(claimed) == 0 {
		return 0, nil
	}
	urls := make([]string, 0, len(claimed))
	for _, item := range claimed {
		urls = append(urls, item.row.Address)
	}
	if err := post(ctx, endpointURL, base, cfg.Key, urls); err != nil {
		b.unlock(claimed, err.Error())
		b.logPush(source, urls, err)
		return 0, err
	}
	b.markSubmitted(claimed, time.Now().UTC())
	b.logPush(source, urls, nil)
	return len(urls), nil
}

// markSubmitted 推送成功：记录已提交的版本（推送期间到达的新版本仍保持待推送）。
func (b *behavior) markSubmitted(claimed []claimedURL, now time.Time) {
	for _, item := range claimed {
		if err := b.core.Gorm().Model(&URL{}).
			Where("id = ? AND claim_token = ?", item.row.ID, item.token).
			Updates(map[string]any{"submitted_version": item.row.Version, "attempts": 0, "locked_at": nil, "claim_token": "", "last_error": "", "last_submitted_at": now, "updated_at": now}).Error; err != nil {
			log.Printf("[indexnow] mark URL submitted failed: %v", err)
		}
	}
}

func (b *behavior) claim(ctx context.Context, now time.Time) ([]claimedURL, error) {
	db := b.core.Gorm().WithContext(ctx)
	rows := []URL{}
	if err := db.Where("version > submitted_version AND attempts < ? AND (locked_at IS NULL OR locked_at < ?)", maxAttempts, now.Add(-staleClaimAfter)).
		Order("updated_at ASC, id ASC").Limit(maxBatchURLs).Find(&rows).Error; err != nil {
		return nil, err
	}
	claimed := make([]claimedURL, 0, len(rows))
	for _, row := range rows {
		token := newClaimToken()
		result := db.Model(&URL{}).
			Where("id = ? AND version = ? AND attempts < ? AND (locked_at IS NULL OR locked_at < ?)", row.ID, row.Version, maxAttempts, now.Add(-staleClaimAfter)).
			Updates(map[string]any{"locked_at": now, "claim_token": token})
		if result.Error != nil {
			b.unlock(claimed, result.Error.Error())
			return nil, result.Error
		}
		if result.RowsAffected == 1 {
			claimed = append(claimed, claimedURL{row: row, token: token})
		}
	}
	return claimed, nil
}

func (b *behavior) skipInvalidClaim(item claimedURL, now time.Time) {
	db := b.core.Gorm().Model(&URL{})
	result := db.Where("id = ? AND version = ? AND claim_token = ?", item.row.ID, item.row.Version, item.token).
		Updates(map[string]any{"submitted_version": item.row.Version, "locked_at": nil, "claim_token": "", "attempts": 0, "last_error": "", "updated_at": now})
	if result.Error != nil {
		log.Printf("[indexnow] discard URL from previous site host failed: %v", result.Error)
		return
	}
	if result.RowsAffected == 0 {
		// A new version may have arrived after claim. Release its lock but leave it pending
		// so the next run can decide against the then-current configured host.
		if err := b.core.Gorm().Model(&URL{}).Where("id = ? AND claim_token = ?", item.row.ID, item.token).Updates(map[string]any{"locked_at": nil, "claim_token": ""}).Error; err != nil {
			log.Printf("[indexnow] release changed URL claim failed: %v", err)
		}
	}
}

func (b *behavior) unlock(rows []claimedURL, message string) {
	now := time.Now().UTC()
	message = safeError(message)
	for _, item := range rows {
		b.core.Gorm().Model(&URL{}).
			Where("id = ? AND claim_token = ?", item.row.ID, item.token).
			Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "locked_at": nil, "claim_token": "", "last_error": message, "updated_at": now})
	}
}

func post(ctx context.Context, endpoint string, base *url.URL, key string, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	keyURL := *base
	keyURL.Path = "/" + key + ".txt"
	payload := indexNowRequest{Host: base.Host, Key: key, KeyLocation: keyURL.String(), URLList: urls}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码 IndexNow 请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建 IndexNow 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "KnowForge-IndexNow/1")
	client := &http.Client{Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连接 IndexNow 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		return nil
	}
	return fmt.Errorf("IndexNow 返回 HTTP %d", resp.StatusCode)
}

func safeError(message string) string {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\x00", ""))
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}

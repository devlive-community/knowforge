package app

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// AI 用量：每次经站点 AI 服务的调用都记录调用方、功能、模型、tokens、耗时与估算费用（按调用时的单价）。
// 每月 AI 用量（tokens）是一项权益：基础值默认不限，成长等级与会员方案可设额度；超出后本月内调用被拒绝（ai.ErrQuotaExceeded）。

const (
	entAIMonthlyTokens    = "ai.monthly_tokens"
	cfgAIMonthlyTokens    = "ai_monthly_tokens"
	maxAIMonthlyTokens    = 1_000_000_000
	defaultAIUsageDays    = 30
	defaultAIUsageTopUser = 10
)

func init() {
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entAIMonthlyTokens, Kind: plugincore.EntitlementLimit, Unit: "tokens", Min: 0, Max: maxAIMonthlyTokens, AllowUnlimited: true, Order: 40,
		Available: func(core plugincore.Core) bool { chat, embed := core.AIStatus(); return chat || embed },
		Base: func(core plugincore.Core) int64 {
			return settingLimit(core, cfgAIMonthlyTokens, plugincore.Unlimited)
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgAIMonthlyTokens, strconv.FormatInt(v, 10), "权益：每月 AI 用量 tokens（基础）")
		},
	})
}

// aiPricing 每百万 tokens 的单价（货币单位），未配置为 0（不估算费用）。Input/Output/Embed 为默认单价，
// Models 为按模型单独设置的单价（见 ai_model_prices.go），优先于默认单价。
type aiPricing struct {
	Currency                        string
	Input, Output, Embed, Translate float64 // Translate 为每百万字符
	Models                          []aiModelPrice
}

func (a *App) aiPricing() aiPricing {
	num := func(key string) float64 {
		v, err := strconv.ParseFloat(strings.TrimSpace(a.getSetting(key)), 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0
		}
		return v
	}
	cur := strings.ToUpper(strings.TrimSpace(a.getSetting("ai_price_currency")))
	if cur == "" {
		cur = "USD"
	}
	return aiPricing{Currency: cur, Input: num("ai_price_input"), Output: num("ai_price_output"), Embed: num("ai_price_embed"),
		Translate: num("ai_price_translate"), Models: a.aiModelPrices()}
}

// costMicros 估算费用（货币单位的百万分之一）：tokens × 每百万单价 / 1e6 × 1e6。模型单独设置了单价时按模型单价计。
func (p aiPricing) costMicros(kind, model string, inputTokens, outputTokens, chars int64) int64 {
	if kind == "translate" {
		return int64(math.Round(float64(chars) * p.Translate))
	}
	input, output, embed := p.Input, p.Output, p.Embed
	if mp, ok := matchModelPrice(p.Models, model); ok {
		input, output, embed = mp.Input, mp.Output, mp.Input
	}
	if kind == "embed" {
		return int64(math.Round(float64(inputTokens) * embed))
	}
	return int64(math.Round(float64(inputTokens)*input + float64(outputTokens)*output))
}

// meteredLimit 按月计量权益的生效上限；不限或权益不可用（如对应服务未配置）时不做限制。
func (a *App) meteredLimit(u *models.User, key string) (int64, bool) {
	r, has := plugincore.ResolveEntitlements(a, u)[key]
	if !has || r.Source == "unavailable" || r.Value == plugincore.Unlimited {
		return 0, false
	}
	return r.Value, true
}

func monthStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}

// aiMonthUsed 用户本月已用 tokens（仅成功的调用）。
func (a *App) aiMonthUsed(userID uint) int64 {
	var total struct{ N int64 }
	a.DB.Model(&models.AIUsageLog{}).Select("COALESCE(SUM(input_tokens + output_tokens), 0) AS n").
		Where("user_id = ? AND status = ? AND created_at >= ?", userID, "ok", monthStart(time.Now())).Scan(&total)
	return total.N
}

// checkAIQuota 调用前按调用方的每月额度判定（系统调用与管理员不受限）。
func (a *App) checkAIQuota(caller ai.Caller) error {
	if caller.UserID == 0 {
		return nil
	}
	var u models.User
	if a.DB.First(&u, caller.UserID).Error != nil {
		return nil
	}
	limit, enforced := a.meteredLimit(&u, entAIMonthlyTokens)
	if !enforced {
		return nil
	}
	if a.aiMonthUsed(u.ID) >= limit {
		return ai.ErrQuotaExceeded
	}
	return nil
}

// recordAIUsage 写入一条用量记录（失败的调用也记录，便于排查；tokens 为 0）。
func (a *App) recordAIUsage(caller ai.Caller, kind, provider, model string, usage ai.Usage, chars int64, elapsed time.Duration, callErr error) {
	pricing := a.aiPricing()
	row := models.AIUsageLog{
		UserID: caller.UserID, Feature: caller.Feature, RefType: caller.RefType, RefID: caller.RefID, TraceID: caller.TraceID,
		Kind: kind, Provider: provider, Model: truncateRunes(model, 100), DurationMs: elapsed.Milliseconds(),
		Currency: pricing.Currency, Status: "ok",
	}
	if row.Feature == "" {
		row.Feature = "other"
	}
	if row.TraceID == "" {
		row.TraceID = ai.NewTraceID()
	}
	if callErr != nil {
		row.Status, row.Error = "error", truncateRunes(callErr.Error(), 300)
	} else {
		row.InputTokens, row.OutputTokens, row.Estimated, row.Characters = usage.InputTokens, usage.OutputTokens, usage.Estimated, chars
		row.CostMicros = pricing.costMicros(kind, model, usage.InputTokens, usage.OutputTokens, chars)
	}
	if a.DB.Create(&row).Error == nil {
		a.checkAIAlerts(&row)
	}
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// —— 保留期 ——

const (
	cfgAIUsageRetentionDays = "ai_usage_retention_days"
	minAIUsageRetentionDays = 90 // 统计页最长查看 90 天
)

// purgeExpiredAIUsage 按保留天数清理 AI 用量记录与预警（0 为永久保留）；分批删除，避免长事务。
func (a *App) purgeExpiredAIUsage(ctx context.Context, now time.Time) error {
	days, err := strconv.Atoi(strings.TrimSpace(a.getSetting(cfgAIUsageRetentionDays)))
	if err != nil || days <= 0 {
		return nil
	}
	if days < minAIUsageRetentionDays {
		days = minAIUsageRetentionDays
	}
	cutoff := now.AddDate(0, 0, -days)
	db := a.DB.WithContext(ctx)
	for {
		var ids []uint
		if err := db.Model(&models.AIUsageLog{}).Where("created_at < ?", cutoff).Limit(1000).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		if err := db.Where("id IN ?", ids).Delete(&models.AIUsageLog{}).Error; err != nil {
			return err
		}
	}
	return db.Where("created_at < ?", cutoff).Delete(&models.AIAlert{}).Error
}

// —— 用户 ——

// MyAIUsage GET /users/me/ai-usage 本月 AI 用量（tokens）、额度（-1 不限）、按功能分布，以及本月翻译字数与额度。
func (a *App) MyAIUsage(c *gin.Context) {
	u := currentUser(c)
	var rows []struct {
		Feature string
		Calls   int64
		Tokens  int64
	}
	a.DB.Model(&models.AIUsageLog{}).Select("feature, COUNT(*) AS calls, COALESCE(SUM(input_tokens + output_tokens), 0) AS tokens").
		Where("user_id = ? AND status = ? AND created_at >= ?", u.ID, "ok", monthStart(time.Now())).Group("feature").Order("tokens DESC").Scan(&rows)
	var used, calls int64
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		used += r.Tokens
		calls += r.Calls
		items = append(items, gin.H{"feature": r.Feature, "calls": r.Calls, "tokens": r.Tokens})
	}
	// 本月每日用量（tokens 与翻译字数），按服务器本地日期
	now := time.Now()
	start := monthStart(now)
	daily := map[string][2]int64{}
	var dayRows []models.AIUsageLog
	a.DB.Select("input_tokens, output_tokens, characters, created_at").
		Where("user_id = ? AND status = ? AND created_at >= ?", u.ID, "ok", start).Find(&dayRows)
	for _, r := range dayRows {
		k := r.CreatedAt.In(now.Location()).Format("2006-01-02")
		v := daily[k]
		daily[k] = [2]int64{v[0] + r.InputTokens + r.OutputTokens, v[1] + r.Characters}
	}
	series := []gin.H{}
	for d := start; !d.After(now); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		series = append(series, gin.H{"date": k, "tokens": daily[k][0], "characters": daily[k][1]})
	}
	ok(c, gin.H{"month_start": monthStart(time.Now()), "used_tokens": used, "calls": calls, "daily": series,
		"limit": a.entitlement(u, entAIMonthlyTokens), "by_feature": items,
		"translate_chars": a.translateMonthUsed(u.ID), "translate_limit": a.entitlement(u, entTranslateMonthlyChars)})
}

// userCallView 用户可见的单次调用（不含费用与服务端原始错误信息，避免泄露站点配置）。
type userCallView struct {
	ID           uint      `json:"id"`
	Feature      string    `json:"feature"`
	Kind         string    `json:"kind"`
	Model        string    `json:"model"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	Characters   int64     `json:"characters"`
	Estimated    bool      `json:"estimated"`
	DurationMs   int64     `json:"duration_ms"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// userTraceView 一条调用链（同一次操作的全部调用，按时间顺序）及其合计。
type userTraceView struct {
	TraceID      string         `json:"trace_id"`
	Feature      string         `json:"feature"`
	RefType      string         `json:"ref_type"`
	RefID        uint           `json:"ref_id"`
	StartedAt    time.Time      `json:"started_at"`
	EndedAt      time.Time      `json:"ended_at"`
	Calls        int            `json:"calls"`
	Errors       int            `json:"errors"`
	InputTokens  int64          `json:"input_tokens"`
	OutputTokens int64          `json:"output_tokens"`
	Characters   int64          `json:"characters"`
	DurationMs   int64          `json:"duration_ms"` // 各次调用耗时之和
	Items        []userCallView `json:"items"`
	// Ref 关联对象（如翻译任务、书籍问答），无法解析或不可见时为 null
	Ref *plugincore.AIUsageRef `json:"ref"`
}

// MyAIUsageLogs GET /users/me/ai-usage/logs?page=&page_size=&feature=&trace_id= 我的 AI 调用记录，按调用链分组（新→旧）。
func (a *App) MyAIUsageLogs(c *gin.Context) {
	u := currentUser(c)
	page, pageSize := paginate(c)
	base := func() *gorm.DB {
		q := a.DB.Model(&models.AIUsageLog{}).Where("user_id = ?", u.ID)
		if f := strings.TrimSpace(c.Query("feature")); f != "" {
			q = q.Where("feature = ?", f)
		}
		if tr := strings.TrimSpace(c.Query("trace_id")); tr != "" {
			q = q.Where("trace_id = ?", tr)
		}
		return q
	}
	var total int64
	base().Distinct("trace_id").Count(&total)
	var groups []struct {
		TraceID string
		LastID  uint
	}
	base().Select("trace_id, MAX(id) AS last_id").Group("trace_id").Order("last_id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Scan(&groups)
	ids := make([]string, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.TraceID)
	}
	byTrace := map[string]*userTraceView{}
	if len(ids) > 0 {
		var rows []models.AIUsageLog
		a.DB.Where("user_id = ? AND trace_id IN ?", u.ID, ids).Order("id ASC").Find(&rows)
		for _, r := range rows {
			t := byTrace[r.TraceID]
			if t == nil {
				t = &userTraceView{TraceID: r.TraceID, Feature: r.Feature, RefType: r.RefType, RefID: r.RefID, StartedAt: r.CreatedAt, Items: []userCallView{}}
				byTrace[r.TraceID] = t
			}
			t.EndedAt = r.CreatedAt
			t.Calls++
			if r.Status != "ok" {
				t.Errors++
			}
			t.InputTokens += r.InputTokens
			t.OutputTokens += r.OutputTokens
			t.Characters += r.Characters
			t.DurationMs += r.DurationMs
			t.Items = append(t.Items, userCallView{ID: r.ID, Feature: r.Feature, Kind: r.Kind, Model: r.Model, InputTokens: r.InputTokens,
				OutputTokens: r.OutputTokens, Characters: r.Characters, Estimated: r.Estimated, DurationMs: r.DurationMs, Status: r.Status, CreatedAt: r.CreatedAt})
		}
	}
	items := make([]*userTraceView, 0, len(groups))
	refs := aiUsageRefCache{}
	for _, g := range groups {
		if t := byTrace[g.TraceID]; t != nil {
			t.Ref = refs.resolve(a, u, t.Feature, t.RefType, t.RefID, t.TraceID)
			items = append(items, t)
		}
	}
	ok(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// aiUsageRefCache 同一页内相同关联只解析一次。
type aiUsageRefCache map[string]*plugincore.AIUsageRef

func (m aiUsageRefCache) resolve(a *App, viewer *models.User, feature, refType string, refID uint, traceID string) *plugincore.AIUsageRef {
	key := feature + "|" + refType + "|" + strconv.FormatUint(uint64(refID), 10)
	if strings.HasPrefix(feature, "translate.") {
		key += "|" + traceID // 翻译任务按调用链区分
	}
	if ref, done := m[key]; done {
		return ref
	}
	ref := plugincore.ResolveAIUsageRef(a, viewer, plugincore.AIUsageRefInput{Feature: feature, RefType: refType, RefID: refID, TraceID: traceID})
	m[key] = ref
	return ref
}

// —— 管理端 ——

type usageAgg struct {
	Calls        int64 `json:"calls"`
	Errors       int64 `json:"errors"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Characters   int64 `json:"characters"`
	CostMicros   int64 `json:"cost_micros"`
}

func (g *usageAgg) add(r *models.AIUsageLog) {
	g.Calls++
	if r.Status != "ok" {
		g.Errors++
		return
	}
	g.InputTokens += r.InputTokens
	g.OutputTokens += r.OutputTokens
	g.Characters += r.Characters
	g.CostMicros += r.CostMicros
}

type keyedAgg struct {
	Key string `json:"key"`
	usageAgg
}

func sortedAggs(m map[string]*usageAgg, limit int) []keyedAgg {
	out := make([]keyedAgg, 0, len(m))
	for k, v := range m {
		out = append(out, keyedAgg{Key: k, usageAgg: *v})
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].InputTokens+out[i].OutputTokens+out[i].Characters, out[j].InputTokens+out[j].OutputTokens+out[j].Characters
		if ti != tj {
			return ti > tj
		}
		return out[i].Key < out[j].Key
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// AdminAIUsage GET /admin/ai/usage?days=7|30|90 期间汇总、每日趋势、按功能/模型分布与用量最高的用户。
func (a *App) AdminAIUsage(c *gin.Context) {
	days := atoiDefault(c.Query("days"), defaultAIUsageDays)
	if days != 7 && days != 30 && days != 90 {
		days = defaultAIUsageDays
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := today.AddDate(0, 0, -(days - 1))

	total := usageAgg{}
	daily := map[string]*usageAgg{}
	byFeature, byModel, byUser := map[string]*usageAgg{}, map[string]*usageAgg{}, map[string]*usageAgg{}
	bucket := func(m map[string]*usageAgg, k string) *usageAgg {
		if m[k] == nil {
			m[k] = &usageAgg{}
		}
		return m[k]
	}
	// 逐行聚合（与数据库方言无关，按服务器本地日期分桶）
	rows, err := a.DB.Model(&models.AIUsageLog{}).
		Select("user_id, feature, model, input_tokens, output_tokens, characters, cost_micros, status, created_at").
		Where("created_at >= ?", from).Rows()
	if err != nil {
		fail(c, http.StatusInternalServerError, "统计失败")
		return
	}
	for rows.Next() {
		var r models.AIUsageLog
		if a.DB.ScanRows(rows, &r) != nil {
			continue
		}
		total.add(&r)
		bucket(daily, r.CreatedAt.In(now.Location()).Format("2006-01-02")).add(&r)
		bucket(byFeature, r.Feature).add(&r)
		if r.Model != "" {
			bucket(byModel, r.Model).add(&r)
		}
		bucket(byUser, strconv.FormatUint(uint64(r.UserID), 10)).add(&r)
	}
	_ = rows.Close()

	series := make([]gin.H, 0, days)
	for d := from; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		agg := usageAgg{}
		if daily[key] != nil {
			agg = *daily[key]
		}
		series = append(series, gin.H{"date": key, "calls": agg.Calls, "tokens": agg.InputTokens + agg.OutputTokens, "characters": agg.Characters, "cost_micros": agg.CostMicros})
	}

	top := sortedAggs(byUser, defaultAIUsageTopUser)
	ids := make([]uint, 0, len(top))
	for _, t := range top {
		if id, _ := strconv.ParseUint(t.Key, 10, 64); id > 0 {
			ids = append(ids, uint(id))
		}
	}
	names := map[uint]string{}
	if len(ids) > 0 {
		var users []models.User
		a.DB.Select("id, username").Where("id IN ?", ids).Find(&users)
		for _, u := range users {
			names[u.ID] = u.Username
		}
	}
	topUsers := make([]gin.H, 0, len(top))
	for _, t := range top {
		id, _ := strconv.ParseUint(t.Key, 10, 64)
		topUsers = append(topUsers, gin.H{"user_id": id, "username": names[uint(id)], "usage": t.usageAgg})
	}

	var features []string
	a.DB.Model(&models.AIUsageLog{}).Distinct("feature").Order("feature").Pluck("feature", &features)
	ok(c, gin.H{
		"days": days, "currency": a.aiPricing().Currency, "total": total, "daily": series,
		"by_feature": sortedAggs(byFeature, 0), "by_model": sortedAggs(byModel, 0), "top_users": topUsers, "features": features,
	})
}

// AdminAIUsageLogs GET /admin/ai/usage/logs?page=&page_size=&feature=&status=&user=&trace_id= 调用明细（user 为用户名）。
func (a *App) AdminAIUsageLogs(c *gin.Context) {
	page, pageSize := paginate(c)
	q := a.DB.Model(&models.AIUsageLog{})
	if f := strings.TrimSpace(c.Query("feature")); f != "" {
		q = q.Where("feature = ?", f)
	}
	if s := c.Query("status"); s == "ok" || s == "error" {
		q = q.Where("status = ?", s)
	}
	if tr := strings.TrimSpace(c.Query("trace_id")); tr != "" {
		q = q.Where("trace_id = ?", tr)
	}
	if name := strings.TrimSpace(c.Query("user")); name != "" {
		var u models.User
		if a.DB.Select("id").Where("username = ?", name).First(&u).Error != nil {
			ok(c, plugincore.PageResult{Items: []any{}, Total: 0, Page: page, PageSize: pageSize})
			return
		}
		q = q.Where("user_id = ?", u.ID)
	}
	var total int64
	q.Count(&total)
	var rows []models.AIUsageLog
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	ids := []uint{}
	for _, r := range rows {
		if r.UserID > 0 {
			ids = append(ids, r.UserID)
		}
	}
	names := map[uint]string{}
	if len(ids) > 0 {
		var users []models.User
		a.DB.Select("id, username").Where("id IN ?", ids).Find(&users)
		for _, u := range users {
			names[u.ID] = u.Username
		}
	}
	items := make([]gin.H, 0, len(rows))
	refs := aiUsageRefCache{}
	viewer := currentUser(c)
	for _, r := range rows {
		items = append(items, gin.H{"log": r, "username": names[r.UserID], "ref": refs.resolve(a, viewer, r.Feature, r.RefType, r.RefID, r.TraceID)})
	}
	ok(c, plugincore.PageResult{Items: items, Total: total, Page: page, PageSize: pageSize})
}

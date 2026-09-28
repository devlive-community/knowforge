package app

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
)

// 按模型单价：不同模型的价格不同（如 gpt-4o 与 gpt-4o-mini），管理员为模型单独设置每百万 tokens 的输入/输出单价；
// 模型名不区分大小写，可用 * 通配（如 claude-3-5-sonnet-*），精确匹配优先，其次取最具体的通配；
// 没有单独设置的模型使用「默认单价」（对话输入/输出、向量嵌入）。费用在调用时按当时的单价估算，
// 修改单价后可按新单价重算最近若干天的记录。

const (
	cfgAIModelPrices     = "ai_model_prices"
	maxAIModelPrices     = 200
	maxRecalculateDays   = 90
	recalculateBatchSize = 1000
)

// aiModelPrice 一个模型（或通配模式）的单价（每百万 tokens）；向量模型只看 Input。
type aiModelPrice struct {
	Model  string  `json:"model"`
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

func (a *App) aiModelPrices() []aiModelPrice {
	var list []aiModelPrice
	_ = json.Unmarshal([]byte(a.getSetting(cfgAIModelPrices)), &list)
	return list
}

// match 模型对应的单价：精确匹配优先，否则取非通配字符最多的通配模式。
func matchModelPrice(list []aiModelPrice, model string) (aiModelPrice, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return aiModelPrice{}, false
	}
	best, bestScore := aiModelPrice{}, -1
	for _, p := range list {
		pattern := strings.ToLower(p.Model)
		if pattern == m {
			return p, true
		}
		if !strings.Contains(pattern, "*") {
			continue
		}
		if ok, _ := path.Match(pattern, m); ok {
			if score := len(strings.ReplaceAll(pattern, "*", "")); score > bestScore {
				best, bestScore = p, score
			}
		}
	}
	return best, bestScore >= 0
}

type modelPriceView struct {
	Items []aiModelPrice `json:"items"`
	// Seen 最近 90 天调用过的模型：调用次数与当前按哪条单价计（matched 为空表示使用默认单价）
	Seen     []seenModel `json:"seen"`
	Currency string      `json:"currency"`
}

type seenModel struct {
	Model   string `json:"model"`
	Kind    string `json:"kind"`
	Calls   int64  `json:"calls"`
	Matched string `json:"matched"`
}

// AdminGetAIModelPrices GET /admin/ai/model-prices 按模型单价与最近调用过的模型。
func (a *App) AdminGetAIModelPrices(c *gin.Context) {
	list := a.aiModelPrices()
	if list == nil {
		list = []aiModelPrice{}
	}
	var rows []struct {
		Model string
		Kind  string
		Calls int64
	}
	a.DB.Model(&models.AIUsageLog{}).Select("model, kind, COUNT(*) AS calls").
		Where("created_at >= ? AND model <> '' AND kind <> ?", time.Now().AddDate(0, 0, -maxRecalculateDays), "translate").
		Group("model, kind").Order("calls DESC").Limit(100).Scan(&rows)
	seen := make([]seenModel, 0, len(rows))
	for _, r := range rows {
		s := seenModel{Model: r.Model, Kind: r.Kind, Calls: r.Calls}
		if p, ok := matchModelPrice(list, r.Model); ok {
			s.Matched = p.Model
		}
		seen = append(seen, s)
	}
	ok(c, modelPriceView{Items: list, Seen: seen, Currency: a.aiPricing().Currency})
}

// AdminUpdateAIModelPrices PUT /admin/ai/model-prices {items:[{model, input, output}]} 整体替换按模型单价。
func (a *App) AdminUpdateAIModelPrices(c *gin.Context) {
	var req struct {
		Items []aiModelPrice `json:"items"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if len(req.Items) > maxAIModelPrices {
		fail(c, http.StatusBadRequest, "最多设置 200 个模型的单价")
		return
	}
	seen := map[string]bool{}
	items := make([]aiModelPrice, 0, len(req.Items))
	valid := func(f float64) bool { return f >= 0 && f <= 100000 && !math.IsNaN(f) && !math.IsInf(f, 0) }
	for _, p := range req.Items {
		p.Model = strings.TrimSpace(p.Model)
		key := strings.ToLower(p.Model)
		switch {
		case p.Model == "" || len([]rune(p.Model)) > 100:
			fail(c, http.StatusBadRequest, "请填写模型名称（不超过 100 字）")
			return
		case seen[key]:
			fail(c, http.StatusBadRequest, "模型重复："+p.Model)
			return
		case !valid(p.Input) || !valid(p.Output):
			fail(c, http.StatusBadRequest, "单价需为 0 到 100000 之间的数字（每百万 tokens）")
			return
		}
		if _, err := path.Match(key, ""); err != nil {
			fail(c, http.StatusBadRequest, "模型名称格式无效："+p.Model)
			return
		}
		seen[key] = true
		items = append(items, p)
	}
	sort.SliceStable(items, func(i, j int) bool { return strings.ToLower(items[i].Model) < strings.ToLower(items[j].Model) })
	raw, _ := json.Marshal(items)
	if err := a.setSetting(cfgAIModelPrices, string(raw), "AI 服务：按模型单价（JSON）"); err != nil {
		fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	a.recordAudit(c, "ai.model_prices_updated", "config", "ai", "按模型单价", map[string]any{"count": len(items)})
	a.AdminGetAIModelPrices(c)
}

// AdminRecalculateAICost POST /admin/ai/model-prices/recalculate {days: 1-90} 按当前单价重算最近若干天成功调用的估算费用。
func (a *App) AdminRecalculateAICost(c *gin.Context) {
	var req struct {
		Days int `json:"days"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Days < 1 || req.Days > maxRecalculateDays {
		fail(c, http.StatusBadRequest, "天数需为 1 到 90")
		return
	}
	updated, err := a.recalculateAICost(c.Request.Context(), time.Now().AddDate(0, 0, -req.Days))
	if err != nil {
		fail(c, http.StatusInternalServerError, "重算失败: "+err.Error())
		return
	}
	a.recordAudit(c, "ai.cost_recalculated", "config", "ai", "重算 AI 费用", map[string]any{"days": req.Days, "updated": updated})
	ok(c, gin.H{"updated": updated})
}

// recalculateAICost 分批按当前单价重算 since 之后成功调用的费用与货币，返回费用有变化的记录数。
func (a *App) recalculateAICost(ctx context.Context, since time.Time) (int, error) {
	pricing := a.aiPricing()
	updated := 0
	for lastID := uint(0); ; {
		var rows []models.AIUsageLog
		if err := a.DB.WithContext(ctx).Where("id > ? AND created_at >= ? AND status = ?", lastID, since, "ok").
			Order("id ASC").Limit(recalculateBatchSize).Find(&rows).Error; err != nil {
			return updated, err
		}
		for _, r := range rows {
			cost := pricing.costMicros(r.Kind, r.Model, r.InputTokens, r.OutputTokens, r.Characters)
			if cost != r.CostMicros || r.Currency != pricing.Currency {
				if err := a.DB.WithContext(ctx).Model(&models.AIUsageLog{}).Where("id = ?", r.ID).
					Updates(map[string]any{"cost_micros": cost, "currency": pricing.Currency}).Error; err != nil {
					return updated, err
				}
				updated++
			}
			lastID = r.ID
		}
		if len(rows) < recalculateBatchSize {
			return updated, nil
		}
	}
}

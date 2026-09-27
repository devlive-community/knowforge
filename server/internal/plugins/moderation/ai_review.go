package moderation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// AI 辅助审核：在敏感词审查之外，可选让模型复核（后台任务，记为系统调用 moderation.ai）。
//   - 待审核（命中敏感词）的内容：模型判断是否真的违规，理由与置信度供审核员参考；「安全时自动通过」模式下，
//     模型判定安全且置信度达到阈值即自动发布（审核记录注明为 AI 复核），减少敏感词误判带来的人工审核；
//   - 可选复查「自动通过」的内容：词典漏掉的疑似违规由模型标记并通知管理员，不自动撤回，由审核员决定。
// 模型只看提交审查时的内容快照；作者看不到模型的判定。审核队列经 SSE 实时更新。

const (
	aiReviewJob = "moderation.ai_review"

	cfgAIMode       = "moderation_ai_mode"       // off | advise | auto_approve
	cfgAIConfidence = "moderation_ai_confidence" // 自动通过所需的最低置信度（0.5–0.99，默认 0.9）
	cfgAIScreen     = "moderation_ai_screen"     // 复查自动通过的内容（默认关）
	cfgAIPolicy     = "moderation_ai_policy"     // 站点补充的审核规则

	aiOff         = "off"
	aiAdvise      = "advise"
	aiAutoApprove = "auto_approve"

	aiQueued    = "queued"
	aiReviewing = "reviewing"
	aiDone      = "done"
	aiFailed    = "failed"

	verdictSafe      = "safe"
	verdictViolation = "violation"
	verdictUncertain = "uncertain"

	maxAIContentRunes = 20000
	maxPolicyRunes    = 2000
)

var casesHub = eventhub.New("moderation.cases", 256) // 审核队列页订阅（全部管理员共用一个频道）

type aiSettings struct {
	Mode          string  `json:"mode"`
	MinConfidence float64 `json:"min_confidence"`
	Screen        bool    `json:"screen_passed"`
	Policy        string  `json:"policy"`
}

func (b *behavior) aiSettings() aiSettings {
	s := aiSettings{Mode: aiOff, MinConfidence: 0.9, Screen: b.core.GetSetting(cfgAIScreen) == "true", Policy: b.core.GetSetting(cfgAIPolicy)}
	if m := b.core.GetSetting(cfgAIMode); m == aiAdvise || m == aiAutoApprove {
		s.Mode = m
	}
	if v, err := strconv.ParseFloat(b.core.GetSetting(cfgAIConfidence), 64); err == nil && v >= 0.5 && v <= 0.99 {
		s.MinConfidence = v
	}
	return s
}

// aiActive AI 复核是否可用（已开启且配置了对话模型）。
func (b *behavior) aiActive() bool {
	chat, _ := b.core.AIStatus()
	return chat && b.aiSettings().Mode != aiOff
}

// queueAIReview 审查记录更新后按设置排队 AI 复核：待审核的都复核，自动通过的在开启复查时复核。
func (b *behavior) queueAIReview(caseID uint, status string) {
	if !b.aiActive() || (status == StatusAutoPassed && !b.aiSettings().Screen) {
		return
	}
	q := b.core.JobQueue()
	if q == nil {
		return
	}
	b.core.Gorm().Model(&Case{}).Where("id = ?", caseID).Updates(map[string]any{"ai_status": aiQueued, "ai_error": ""})
	b.publishCase(caseID)
	_, _ = q.Enqueue(context.Background(), aiReviewJob, map[string]any{"case_id": caseID}, 2)
}

func (b *behavior) publishCase(id uint) {
	var row Case
	if b.core.Gorm().First(&row, id).Error == nil {
		casesHub.Publish(0, "case", b.caseItems([]Case{row})[0])
	}
}

const aiSystemPrompt = "你是内容安全审核员，判断用户提交的内容是否违反站点规则：违法犯罪、色情低俗、暴力恐怖、仇恨与歧视、" +
	"侵犯他人隐私（如公开他人身份证号、住址、电话）、诈骗与垃圾广告、自我伤害。敏感词命中可能是误判（正常语境中出现的词，" +
	"如技术文档、历史叙述、引用与批评），请结合上下文判断。只输出 JSON：" +
	"{\"verdict\": \"safe|violation|uncertain\", \"confidence\": 0 到 1 的小数, \"categories\": [\"违规类别\"], \"reason\": \"一句话理由（不超过 100 字）\"}。"

// aiFields 用于复核的内容：提交审查时的快照；没有快照的旧记录读取章节/书籍的当前内容。
func (b *behavior) aiFields(row *Case) map[string]string {
	if len(row.Snapshot) > 0 {
		return row.Snapshot
	}
	db := b.core.Gorm()
	switch row.Kind {
	case plugincore.PublishDocument:
		var d models.Document
		if db.First(&d, row.TargetID).Error == nil {
			return map[string]string{"title": d.Title, "content": d.Content}
		}
	case plugincore.PublishBook:
		var bk models.Book
		if db.First(&bk, row.TargetID).Error == nil {
			return map[string]string{"title": bk.Title, "description": bk.Description}
		}
	}
	return nil
}

type aiVerdict struct {
	Verdict    string   `json:"verdict"`
	Confidence float64  `json:"confidence"`
	Categories []string `json:"categories"`
	Reason     string   `json:"reason"`
}

func parseVerdict(out string) (aiVerdict, bool) {
	var v aiVerdict
	start, end := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if start < 0 || end <= start || json.Unmarshal([]byte(out[start:end+1]), &v) != nil {
		return v, false
	}
	switch v.Verdict {
	case verdictSafe, verdictViolation, verdictUncertain:
	default:
		return v, false
	}
	if v.Confidence < 0 {
		v.Confidence = 0
	}
	if v.Confidence > 1 {
		v.Confidence = 1
	}
	if v.Categories == nil {
		v.Categories = []string{}
	}
	return v, true
}

// runAIReview 复核一条审核记录并按模式处理。
func (b *behavior) runAIReview(ctx context.Context, raw json.RawMessage) error {
	var job struct {
		CaseID uint `json:"case_id"`
	}
	if json.Unmarshal(raw, &job) != nil || !b.core.PluginEnabled(plugins.KeyModeration) {
		return nil
	}
	db := b.core.Gorm()
	var row Case
	if db.First(&row, job.CaseID).Error != nil {
		return nil
	}
	fail := func(msg string) {
		db.Model(&Case{}).Where("id = ?", row.ID).Updates(map[string]any{"ai_status": aiFailed, "ai_error": msg})
		b.publishCase(row.ID)
	}
	if row.Status != StatusPending && row.Status != StatusAutoPassed {
		db.Model(&Case{}).Where("id = ?", row.ID).Update("ai_status", "")
		b.publishCase(row.ID)
		return nil // 已人工处理
	}
	fields := b.aiFields(&row)
	if len(fields) == 0 {
		fail("无法获取内容")
		return nil
	}
	db.Model(&Case{}).Where("id = ?", row.ID).Updates(map[string]any{"ai_status": aiReviewing, "ai_error": ""})
	b.publishCase(row.ID)

	var sb strings.Builder
	settings := b.aiSettings()
	if p := strings.TrimSpace(settings.Policy); p != "" {
		sb.WriteString("站点补充规则：" + p + "\n\n")
	}
	if len(row.Hits) > 0 {
		sb.WriteString("敏感词命中（可能误判，请结合上下文判断）：\n")
		for i, h := range row.Hits {
			if i >= 20 {
				break
			}
			sb.WriteString(fmt.Sprintf("- 「%s」：%s\n", h.Word, h.Context))
		}
		sb.WriteString("\n")
	}
	for _, k := range []string{"title", "description", "content", "body"} {
		if v := strings.TrimSpace(fields[k]); v != "" {
			if r := []rune(v); len(r) > maxAIContentRunes {
				v = string(r[:maxAIContentRunes]) + "\n（后文省略）"
			}
			sb.WriteString("<" + k + ">\n" + v + "\n</" + k + ">\n")
		}
	}
	resp, err := b.core.AIChat(ai.WithCaller(ctx, ai.Caller{Feature: "moderation.ai", RefType: "moderation_case", RefID: row.ID}),
		ai.ChatRequest{System: aiSystemPrompt, Messages: []ai.Message{{Role: "user", Content: sb.String()}}, Temperature: 0})
	if err != nil {
		fail("AI 服务暂时不可用")
		return nil
	}
	v, ok := parseVerdict(resp.Content)
	if !ok {
		fail("AI 返回的结果无法解析")
		return nil
	}
	cats, _ := json.Marshal(v.Categories)
	now := time.Now()
	reason := v.Reason
	if r := []rune(reason); len(r) > 200 {
		reason = string(r[:200])
	}
	db.Model(&Case{}).Where("id = ?", row.ID).Updates(map[string]any{
		"ai_status": aiDone, "ai_verdict": v.Verdict, "ai_confidence": v.Confidence, "ai_categories": string(cats), "ai_reason": reason,
		"ai_model": resp.Model, "ai_input_tokens": resp.Usage.InputTokens, "ai_output_tokens": resp.Usage.OutputTokens, "ai_reviewed_at": &now,
	})
	db.First(&row, row.ID)
	switch {
	case row.Status == StatusPending && settings.Mode == aiAutoApprove && v.Verdict == verdictSafe && v.Confidence >= settings.MinConfidence:
		// 判定安全且置信度足够：自动通过并发布（复审人为系统）
		if err := b.review(&row, 0, true, "AI 复核通过："+reason); err != nil {
			fail("自动通过失败：" + err.Error())
			return nil
		}
	case row.Status == StatusAutoPassed && v.Verdict != verdictSafe:
		b.notifyAdminsFlagged(row.Title)
	}
	b.publishCase(row.ID)
	return nil
}

// notifyAdminsFlagged 自动通过的内容被 AI 标记为疑似违规时通知管理员。
func (b *behavior) notifyAdminsFlagged(title string) {
	var ids []uint
	b.core.Gorm().Model(&models.User{}).Where("role = ? AND is_active = ?", "admin", true).Pluck("id", &ids)
	for _, id := range ids {
		b.core.NotifyI18n(id, notificationType, "notify.moderation.aiFlagged", map[string]string{"title": title}, map[string]any{"link": "/admin/moderation?tab=ai"})
	}
}

// —— 接口 ——

// AdminGetAISettings GET /admin/moderation/ai-settings
func (b *behavior) AdminGetAISettings(c *gin.Context) {
	chat, _ := b.core.AIStatus()
	b.core.OK(c, gin.H{"settings": b.aiSettings(), "ai_available": chat})
}

// AdminUpdateAISettings PUT /admin/moderation/ai-settings {mode, min_confidence, screen_passed, policy}（可只传部分字段）
func (b *behavior) AdminUpdateAISettings(c *gin.Context) {
	var req struct {
		Mode          *string  `json:"mode"`
		MinConfidence *float64 `json:"min_confidence"`
		Screen        *bool    `json:"screen_passed"`
		Policy        *string  `json:"policy"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if req.Mode != nil && *req.Mode != aiOff && *req.Mode != aiAdvise && *req.Mode != aiAutoApprove {
		b.core.Fail(c, http.StatusBadRequest, "不支持的模式")
		return
	}
	if req.MinConfidence != nil && (*req.MinConfidence < 0.5 || *req.MinConfidence > 0.99) {
		b.core.Fail(c, http.StatusBadRequest, "置信度需在 0.5 到 0.99 之间")
		return
	}
	if req.Policy != nil && utf8.RuneCountInString(*req.Policy) > maxPolicyRunes {
		b.core.Fail(c, http.StatusBadRequest, "补充规则不能超过 2000 字")
		return
	}
	changed := []string{}
	set := func(field, key, value, desc string) {
		_ = b.core.SetSetting(key, value, desc)
		changed = append(changed, field)
	}
	old := b.aiSettings()
	if req.Mode != nil && *req.Mode != old.Mode {
		set("ai_mode", cfgAIMode, *req.Mode, "内容审核：AI 复核模式（off / advise / auto_approve）")
	}
	if req.MinConfidence != nil && *req.MinConfidence != old.MinConfidence {
		set("ai_min_confidence", cfgAIConfidence, strconv.FormatFloat(*req.MinConfidence, 'f', 2, 64), "内容审核：AI 自动通过所需置信度")
	}
	if req.Screen != nil && *req.Screen != old.Screen {
		set("ai_screen_passed", cfgAIScreen, strconv.FormatBool(*req.Screen), "内容审核：AI 复查自动通过的内容")
	}
	if req.Policy != nil && strings.TrimSpace(*req.Policy) != old.Policy {
		set("ai_policy", cfgAIPolicy, strings.TrimSpace(*req.Policy), "内容审核：AI 复核的站点补充规则")
	}
	if len(changed) > 0 {
		b.core.RecordAudit(c, "moderation.settings_updated", "moderation", "settings", "内容审核设置", changedFields(changed...))
	}
	b.AdminGetAISettings(c)
}

// AdminAIReview POST /admin/moderation/cases/:id/ai-review 重新让 AI 复核一条未结记录。
func (b *behavior) AdminAIReview(c *gin.Context) {
	row, found := b.findCase(c)
	if !found {
		return
	}
	if !b.aiActive() {
		b.core.Fail(c, http.StatusBadRequest, "AI 复核未开启或未配置对话模型")
		return
	}
	if row.Status != StatusPending && row.Status != StatusAutoPassed {
		b.core.Fail(c, http.StatusConflict, "该记录已处理")
		return
	}
	if row.AIStatus == aiQueued || row.AIStatus == aiReviewing {
		b.core.Fail(c, http.StatusConflict, "AI 正在复核")
		return
	}
	q := b.core.JobQueue()
	if q == nil {
		b.core.Fail(c, http.StatusServiceUnavailable, "任务队列未就绪")
		return
	}
	b.core.Gorm().Model(&Case{}).Where("id = ?", row.ID).Updates(map[string]any{"ai_status": aiQueued, "ai_error": ""})
	b.publishCase(row.ID)
	if _, err := q.Enqueue(c.Request.Context(), aiReviewJob, map[string]any{"case_id": row.ID}, 2); err != nil {
		b.core.Fail(c, http.StatusServiceUnavailable, "排队失败")
		return
	}
	b.core.Gorm().First(row, row.ID)
	b.core.OK(c, b.caseItems([]Case{*row})[0])
}

// AdminCasesStream GET /admin/moderation/stream（?ticket=）审核队列的实时变化：case（一条记录的最新状态，含 AI 复核结果）。
func (b *behavior) AdminCasesStream(c *gin.Context) {
	eventhub.StartSSE(c)
	ch := casesHub.Subscribe(0)
	defer casesHub.Unsubscribe(0, ch)
	eventhub.Write(c.Writer, "ready", []byte("{}"))
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			eventhub.Write(c.Writer, ev.Name, ev.Data)
		case <-heartbeat.C:
			eventhub.Ping(c.Writer)
		}
	}
}

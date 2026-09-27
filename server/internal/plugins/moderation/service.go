package moderation

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

type behavior struct{ core plugincore.Core }

// 设置（站点配置键值）。
const (
	cfgScopeDocuments = "moderation_scope_documents" // 审查章节发布（默认开）
	cfgScopeBooks     = "moderation_scope_books"     // 审查书籍公开（默认开）
	cfgScopeUGC       = "moderation_scope_ugc"       // 审查其他插件登记的用户内容（如问答的提问、回答，默认开）
	cfgSkipNoise      = "moderation_skip_noise"      // 忽略词语间的空白与符号（默认开）
	cfgNotifyPass     = "moderation_notify_pass"     // 自动通过时通知作者（默认关）
	cfgAdminExempt    = "moderation_admin_exempt"    // 管理员发布免审（默认开）
	cfgDictVersion    = "moderation_dict_version"    // 词典版本（词条变更时刷新，多进程据此重建自动机）
	maxHits           = 200
)

type settings struct {
	ScopeDocuments bool `json:"scope_documents"`
	ScopeBooks     bool `json:"scope_books"`
	ScopeUGC       bool `json:"scope_ugc"`
	SkipNoise      bool `json:"skip_noise"`
	NotifyPass     bool `json:"notify_pass"`
	AdminExempt    bool `json:"admin_exempt"`
}

func (b *behavior) settings() settings {
	on := func(key string, def bool) bool {
		switch b.core.GetSetting(key) {
		case "true":
			return true
		case "false":
			return false
		}
		return def
	}
	return settings{
		ScopeDocuments: on(cfgScopeDocuments, true), ScopeBooks: on(cfgScopeBooks, true), ScopeUGC: on(cfgScopeUGC, true), SkipNoise: on(cfgSkipNoise, true),
		NotifyPass: on(cfgNotifyPass, false), AdminExempt: on(cfgAdminExempt, true),
	}
}

// —— 词典自动机缓存 ——

var dict struct {
	sync.Mutex
	key     string
	ac      *automaton
	entries []Word
}

func (b *behavior) bumpDictVersion() {
	_ = b.core.SetSetting(cfgDictVersion, strconv.FormatInt(time.Now().UnixNano(), 10), "内容审核：词典版本")
}

// matcher 当前启用词条的自动机（词典版本或规范化方式变化时重建）。
func (b *behavior) matcher(skipNoise bool) (*automaton, []Word) {
	key := b.core.GetSetting(cfgDictVersion) + "|" + strconv.FormatBool(skipNoise)
	dict.Lock()
	defer dict.Unlock()
	if dict.ac != nil && dict.key == key {
		return dict.ac, dict.entries
	}
	var words []Word
	b.core.Gorm().Where("enabled = ?", true).Order("id").Find(&words)
	normWords := make([][]rune, len(words))
	for i, w := range words {
		normWords[i] = normalize([]rune(w.Word), skipNoise).runes
	}
	dict.ac, dict.entries, dict.key = buildAutomaton(normWords), words, key
	return dict.ac, dict.entries
}

// scan 审查各字段，返回命中（按字段、位置排序）。
func (b *behavior) scan(fields map[string]string, skipNoise bool) Hits {
	ac, entries := b.matcher(skipNoise)
	if len(entries) == 0 {
		return nil
	}
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	var hits Hits
	for _, field := range names {
		orig := []rune(fields[field])
		norm := normalize(orig, skipNoise)
		for _, m := range ac.search(norm.runes, maxHits-len(hits)) {
			line, column, start, end := locate(orig, norm, m)
			w := entries[m.word]
			hits = append(hits, Hit{Field: field, Word: w.Word, Category: w.Category, Line: line, Column: column,
				Text: string(orig[start : end+1]), Context: contextOf(orig, start, end)})
		}
		if len(hits) >= maxHits {
			break
		}
	}
	return hits
}

// —— 发布守卫 ——

func guard(core plugincore.Core, t plugincore.PublishTarget) plugincore.PublishVerdict {
	if !core.PluginEnabled(plugins.KeyModeration) {
		return plugincore.PublishVerdict{}
	}
	b := &behavior{core: core}
	s := b.settings()
	_, ugc := plugincore.UserContentFor(t.Kind)
	if (t.Kind == plugincore.PublishDocument && !s.ScopeDocuments) || (t.Kind == plugincore.PublishBook && !s.ScopeBooks) || (ugc && !s.ScopeUGC) {
		return plugincore.PublishVerdict{}
	}
	if s.AdminExempt && t.ActorID != 0 {
		var actor models.User
		if core.Gorm().First(&actor, t.ActorID).Error == nil && core.IsAdmin(&actor) {
			return plugincore.PublishVerdict{}
		}
	}
	hits := b.scan(t.Fields, s.SkipNoise)
	if len(hits) == 0 {
		b.queueAIReview(b.recordCase(t, StatusAutoPassed, nil), StatusAutoPassed)
		if s.NotifyPass {
			core.NotifyI18n(t.UserID, notificationType, "notify.moderation.passed", map[string]string{"title": t.Title}, map[string]any{"link": "/user/moderation"})
		}
		return plugincore.PublishVerdict{}
	}
	b.queueAIReview(b.recordCase(t, StatusPending, hits), StatusPending)
	core.NotifyI18n(t.UserID, notificationType, "notify.moderation.held", map[string]string{"title": t.Title}, map[string]any{"link": "/user/moderation"})
	b.notifyAdmins(t.Title)
	return plugincore.PublishVerdict{Hold: true, Message: fmt.Sprintf("内容中有 %d 处需要人工审核，已提交审核，通过后将自动发布", len(hits))}
}

// recordCase 更新对象的未结记录（自动通过/待审核），没有则新建；保存内容快照并清空上一次的 AI 复核结果。返回记录 ID。
func (b *behavior) recordCase(t plugincore.PublishTarget, status string, hits Hits) uint {
	if hits == nil {
		hits = Hits{}
	}
	db := b.core.Gorm()
	var existing Case
	found := db.Where("kind = ? AND target_id = ? AND status IN ?", t.Kind, t.ID, []string{StatusAutoPassed, StatusPending}).
		Order("id DESC").First(&existing).Error == nil
	if found {
		db.Model(&Case{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"status": status, "hits": hits, "requested": StringMap(t.Requested), "title": t.Title, "user_id": t.UserID, "book_id": t.BookID, "updated_at": time.Now(),
			"snapshot": StringMap(t.Fields), "ai_status": "", "ai_verdict": "", "ai_confidence": 0, "ai_categories": StringList{}, "ai_reason": "", "ai_error": "", "ai_reviewed_at": nil,
		})
		return existing.ID
	}
	row := Case{Kind: t.Kind, TargetID: t.ID, BookID: t.BookID, UserID: t.UserID, Title: t.Title, Status: status, Hits: hits, Requested: t.Requested, Snapshot: t.Fields}
	db.Create(&row)
	return row.ID
}

// notifyAdmins 通知全部启用中的管理员有内容待审核。
func (b *behavior) notifyAdmins(title string) {
	var ids []uint
	b.core.Gorm().Model(&models.User{}).Where("role = ? AND is_active = ?", "admin", true).Pluck("id", &ids)
	for _, id := range ids {
		b.core.NotifyI18n(id, notificationType, "notify.moderation.pending", map[string]string{"title": title}, map[string]any{"link": "/admin/moderation"})
	}
}

// review 管理员复审：approve 为真时待审核内容发布（自动通过的仅确认）；否则驳回（自动通过的撤回发布），并通知作者。
func (b *behavior) review(c *Case, reviewerID uint, approve bool, note string) error {
	switch {
	case approve && c.Status == StatusPending:
		if err := b.core.ApplyModeration(c.Kind, c.TargetID, true, c.Requested); err != nil {
			return err
		}
		b.core.NotifyI18n(c.UserID, notificationType, "notify.moderation.approved", map[string]string{"title": c.Title}, map[string]any{"link": "/user/moderation"})
	case !approve && c.Status == StatusAutoPassed:
		if err := b.core.ApplyModeration(c.Kind, c.TargetID, false, nil); err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
	}
	if !approve {
		b.core.NotifyI18n(c.UserID, notificationType, "notify.moderation.rejected", map[string]string{"title": c.Title, "note": note}, map[string]any{"link": "/user/moderation"})
	}
	status := StatusApproved
	if !approve {
		status = StatusRejected
	}
	now := time.Now()
	return b.core.Gorm().Model(&Case{}).Where("id = ?", c.ID).Updates(map[string]any{
		"status": status, "reviewer_id": reviewerID, "review_note": note, "reviewed_at": &now,
	}).Error
}

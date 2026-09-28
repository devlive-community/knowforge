package growth

import (
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 经验服务层：订阅核心事件（业务活动目录 xpCatalog + 章节发布钩子）按经验规则发经验，
// 为其他插件（如成就解锁奖励）提供幂等记账，并在等级变化时写历史、发通知、发出 growth.level_changed 活动。
// 插件禁用时全部为安全空操作。

func init() {
	i18ntext.Register("notify.growth.levelUp", map[string]string{"zh-CN": "成长升级：{name}", "en": "Level up: {name}"})
	plugincore.ProvideExperienceRecorder(func(core plugincore.Core, userID uint, ruleKey, sourceType, sourceID, dedupeKey string, xp int, reason string) {
		(&behavior{core: core}).recordExperience(userID, ruleKey, sourceType, sourceID, dedupeKey, xp, reason)
	})
	plugincore.ProvideExperienceGranter(func(core plugincore.Core, userID uint, ruleKey, sourceType, sourceID string, xp int, reason string) {
		(&behavior{core: core}).grantOnce(userID, ruleKey, sourceType, sourceID, xp, reason)
	})
	plugincore.ProvideExperienceRevoker(func(core plugincore.Core, userID uint, ruleKey, sourceID, reason string) {
		b := &behavior{core: core}
		var granted []models.ExperienceEvent
		core.Gorm().Where("user_id = ? AND rule_key = ? AND source_id = ? AND final_xp > 0", userID, ruleKey, sourceID).Find(&granted)
		b.revokeEvents(granted, reason)
	})
	plugincore.OnActivity(func(core plugincore.Core, ev plugincore.ActivityEvent) {
		b := &behavior{core: core}
		b.awardForActivity(ev)
		b.revokeForActivity(ev)
	})
	plugincore.OnChapterPublished(func(core plugincore.Core, _ *models.Book, doc *models.Document) {
		id := strconv.FormatUint(uint64(doc.ID), 10)
		(&behavior{core: core}).awardExperience(doc.UserID, "creation.chapter_published", "document", id, "creation.chapter_published:"+id)
	})
	// 首次启用：种子默认等级与经验规则
	plugincore.OnPluginEnabled(plugins.KeyGrowth, func(core plugincore.Core) error {
		b := &behavior{core: core}
		b.seedDefaultLevels()
		b.ensureExperienceRules()
		// 经验服务就绪：其他插件据此对账（如补发/收回停用期间的成就奖励）
		plugincore.FireExperienceReady(core)
		return nil
	})
	// 删除用户时一并清理其成长数据
	plugincore.RegisterUserDataModels(&models.UserGrowthProfile{}, &models.ExperienceEvent{}, &models.UserLevelHistory{}, &models.UserCheckin{})
}

// seedDefaultLevels 首次启用时种子一套默认等级（已存在则跳过）。
func (b *behavior) seedDefaultLevels() {
	db := b.core.Gorm()
	var count int64
	db.Model(&models.LevelDefinition{}).Count(&count)
	if count > 0 {
		return
	}
	thresholds := []int{0, 100, 250, 500, 1000, 2000, 4000, 8000, 16000, 32000}
	colors := []string{"#94a3b8", "#22c55e", "#0ea5e9", "#6366f1", "#a855f7", "#ec4899", "#f59e0b", "#ef4444", "#14b8a6", "#eab308"}
	for i, min := range thresholds {
		lvl := i + 1
		db.Create(&models.LevelDefinition{
			Level: lvl, Key: fmt.Sprintf("lv%d", lvl), Name: fmt.Sprintf("Lv.%d", lvl),
			IconType: "fa", IconValue: "fa-star", Color: colors[i%len(colors)],
			MinXP: min, SortOrder: lvl, Status: "active",
		})
	}
}

// xpTrigger 经验触发器：核心业务活动 → 经验规则。目录由代码拥有（事件语义与去重方式固定），
// 管理员只能调整各规则的经验值/每日上限/启停。Activity 为空表示由专门钩子触发（如章节发布）。
type xpTrigger struct {
	RuleKey   string
	Activity  string
	Label     string
	BaseXP    int
	DailyCap  int
	Enabled   bool // 首次创建规则时的默认启停：原有 3 条默认启用，其余默认停用，避免升级后行为突变
	SortOrder int
	// Dedupe 去重键；为 nil 时用「规则键:来源 ID」（来源 ID 唯一标识一次行为，如评论/点赞/书籍 ID）
	Dedupe func(b *behavior, ev plugincore.ActivityEvent) string
}

var xpCatalog = []xpTrigger{
	{RuleKey: "reading.chapter", Activity: "chapter.read", Label: "阅读章节（首次）", BaseXP: 5, DailyCap: 50, Enabled: true, SortOrder: 1,
		Dedupe: func(b *behavior, ev plugincore.ActivityEvent) string {
			// 以已读记录 id 去重（与迁移前的去重键一致）
			var read models.ReadChapter
			if b.core.Gorm().Select("id").Where("user_id = ? AND doc_id = ?", ev.UserID, ev.SourceID).First(&read).Error != nil {
				return ""
			}
			return fmt.Sprintf("reading.chapter:%d", read.ID)
		}},
	{RuleKey: "creation.chapter_published", Label: "发布章节", BaseXP: 10, DailyCap: 100, Enabled: true, SortOrder: 2},
	{RuleKey: "community.comment", Activity: "comment.created", Label: "发表评论", BaseXP: 3, DailyCap: 30, Enabled: true, SortOrder: 3},
	{RuleKey: "reading.time", Activity: "reading.time", Label: "阅读时长（每 5 分钟）", BaseXP: 1, DailyCap: 12, SortOrder: 4,
		Dedupe: func(_ *behavior, ev plugincore.ActivityEvent) string { return ev.DedupeKey }}, // 形如 reading.time:<用户>:<5 分钟桶>
	{RuleKey: "reading.annotation", Activity: "annotation.created", Label: "添加笔记标注", BaseXP: 2, DailyCap: 20, SortOrder: 5},
	{RuleKey: "creation.book_created", Activity: "book.created", Label: "创建书籍", BaseXP: 5, DailyCap: 10, SortOrder: 6},
	{RuleKey: "creation.chapter_created", Activity: "document.created", Label: "新建章节", BaseXP: 2, DailyCap: 20, SortOrder: 7},
	{RuleKey: "community.comment_received", Activity: "comment.received", Label: "作品收到评论", BaseXP: 2, DailyCap: 20, SortOrder: 8},
	{RuleKey: "community.reaction", Activity: "reaction.created", Label: "点赞/收藏", BaseXP: 1, DailyCap: 10, SortOrder: 9},
	{RuleKey: "community.reaction_received", Activity: "reaction.received", Label: "作品获得点赞/收藏", BaseXP: 2, DailyCap: 20, SortOrder: 10},
	{RuleKey: "account.registered", Activity: "account.registered", Label: "注册账号", BaseXP: 20, SortOrder: 11},
	{RuleKey: "account.email_verified", Activity: "account.email_verified", Label: "验证邮箱", BaseXP: 10, SortOrder: 12},
	{RuleKey: "account.two_factor_enabled", Activity: "account.two_factor_enabled", Label: "开启二次认证", BaseXP: 20, SortOrder: 13},
	{RuleKey: "account.oauth_bound", Activity: "account.oauth_bound", Label: "绑定第三方账号", BaseXP: 5, DailyCap: 15, SortOrder: 14},
	{RuleKey: "account.invited_user", Activity: "account.invited_user", Label: "邀请用户注册", BaseXP: 20, DailyCap: 100, SortOrder: 15},
	// 签到为新功能，规则默认启用（不存在「升级后行为突变」的顾虑）
	{RuleKey: "checkin.daily", Activity: "checkin.created", Label: "每日签到", BaseXP: 5, Enabled: true, SortOrder: 16},
	{RuleKey: "checkin.streak_bonus", Activity: "checkin.streak_milestone", Label: "连续签到奖励", BaseXP: 20, Enabled: true, SortOrder: 17},
	// 会员插件在订单付款开通/续期后发出 membership.purchased（来源 ID 为订单号），退款撤销时收回
	{RuleKey: "membership.purchased", Activity: "membership.purchased", Label: "购买会员", BaseXP: 50, Enabled: true, SortOrder: 18},
}

// ensureExperienceRules 为目录中缺失的触发器补建规则（已存在的规则不改动，保留管理员的配置）。
// 首次启用与管理端查看规则时调用，使升级后的新触发器出现在管理端（默认停用）。
func (b *behavior) ensureExperienceRules() {
	db := b.core.Gorm()
	var existing []string
	db.Model(&models.ExperienceRule{}).Pluck("rule_key", &existing)
	have := make(map[string]bool, len(existing))
	for _, k := range existing {
		have[k] = true
	}
	for _, t := range xpCatalog {
		if have[t.RuleKey] {
			continue
		}
		rule := models.ExperienceRule{RuleKey: t.RuleKey, Label: t.Label, BaseXP: t.BaseXP, DailyCap: t.DailyCap, Enabled: t.Enabled, SortOrder: t.SortOrder}
		if db.Create(&rule).Error == nil && !t.Enabled {
			// enabled 列有 default:true，零值 false 在插入时会被默认值覆盖，需显式写回
			db.Model(&models.ExperienceRule{}).Where("id = ?", rule.ID).Update("enabled", false)
		}
	}
}

// xpRevocations 内容被删除（评论、点赞/收藏、标注）时收回的规则：删除活动 → 对应的发放规则。
// 以「规则键:来源 ID」定位原经验流水（与发放时的默认去重键一致），给各自的获得者记一条等额负经验，
// 流水保留可追溯；防止「发了删、删了再发」刷经验。书籍/章节不在此列（作者整理内容不应被扣经验）。
var xpRevocations = map[string][]string{
	"comment.deleted":     {"community.comment", "community.comment_received"},
	"reaction.deleted":    {"community.reaction", "community.reaction_received"},
	"annotation.deleted":  {"reading.annotation"},
	"membership.refunded": {"membership.purchased"},
}

// revokedReason 收回经验的流水说明（前端按 growth.reason.<值> 显示多语言文案）。
const revokedReason = "revoked"

// revokeForActivity 按删除活动收回对应经验（幂等：每条原流水只收回一次）。
func (b *behavior) revokeForActivity(ev plugincore.ActivityEvent) {
	rules, ok := xpRevocations[ev.Type]
	if !ok || ev.SourceID == "" || !b.core.PluginEnabled(plugins.KeyGrowth) {
		return
	}
	keys := make([]string, 0, len(rules))
	for _, rule := range rules {
		keys = append(keys, rule+":"+ev.SourceID)
	}
	var granted []models.ExperienceEvent
	b.core.Gorm().Where("dedupe_key IN ? AND final_xp > 0", keys).Find(&granted)
	b.revokeEvents(granted, revokedReason)
}

// grantOnce 为来源（用户 + 规则 + 来源 ID）发放唯一一份经验：净经验 > 0 时跳过；
// 否则以「规则:用户:来源[:代次]」为去重键发放（代次 = 该来源已有的正流水条数），
// 首份与历史键「achievement.unlocked:<用户>:<成就>」一致；并发调用落到同一去重键，只成功一次。
func (b *behavior) grantOnce(userID uint, ruleKey, sourceType, sourceID string, xp int, reason string) {
	if xp <= 0 || userID == 0 || sourceID == "" || !b.core.PluginEnabled(plugins.KeyGrowth) {
		return
	}
	db := b.core.Gorm()
	scope := db.Model(&models.ExperienceEvent{}).Where("user_id = ? AND rule_key = ? AND source_id = ?", userID, ruleKey, sourceID)
	var net int64
	scope.Session(&gorm.Session{}).Select("COALESCE(SUM(final_xp),0)").Scan(&net)
	if net > 0 {
		return
	}
	var generation int64
	scope.Session(&gorm.Session{}).Where("final_xp > 0").Count(&generation)
	dedupe := fmt.Sprintf("%s:%d:%s", ruleKey, userID, sourceID)
	if generation > 0 {
		dedupe = fmt.Sprintf("%s:%d", dedupe, generation)
	}
	b.recordExperience(userID, ruleKey, sourceType, sourceID, dedupe, xp, reason)
}

// revokeEvents 为每条正经验流水记一条等额负流水（去重键 revoke:<原键>，每条只收回一次）。
func (b *behavior) revokeEvents(granted []models.ExperienceEvent, reason string) {
	for _, g := range granted {
		b.recordEvent(g.UserID, g.RuleKey, g.SourceType, g.SourceID, "revoke:"+g.DedupeKey, -g.BaseXP, -g.FinalXP, reason)
	}
}

// awardForActivity 按目录把业务活动映射为经验（规则停用/插件禁用时为空操作）。
func (b *behavior) awardForActivity(ev plugincore.ActivityEvent) {
	for i := range xpCatalog {
		t := &xpCatalog[i]
		if t.Activity == "" || t.Activity != ev.Type {
			continue
		}
		dedupe := t.RuleKey + ":" + ev.SourceID
		if t.Dedupe != nil {
			dedupe = t.Dedupe(b, ev)
		}
		if dedupe == "" || ev.SourceID == "" {
			continue
		}
		b.awardExperience(ev.UserID, t.RuleKey, ev.SourceType, ev.SourceID, dedupe)
	}
}

// awardExperience 按「经验规则」给固定事件发经验：读取 base_xp，按用户的经验加成权益（growth.xp_bonus，百分比）
// 放大，并执行每人每日上限（上限按规则基础经验累计，加成不会让人更早触顶）。
func (b *behavior) awardExperience(userID uint, ruleKey, sourceType, sourceID, dedupeKey string) {
	if !b.core.PluginEnabled(plugins.KeyGrowth) || userID == 0 {
		return
	}
	db := b.core.Gorm()
	var r models.ExperienceRule
	if db.Where("rule_key = ?", ruleKey).First(&r).Error != nil || !r.Enabled || r.BaseXP == 0 {
		return
	}
	if r.DailyCap > 0 {
		var todaySum int64
		db.Model(&models.ExperienceEvent{}).
			Where("user_id = ? AND rule_key = ? AND created_at >= ?", userID, ruleKey, dayStart(time.Now())).
			Select("COALESCE(SUM(base_xp),0)").Scan(&todaySum)
		if todaySum >= int64(r.DailyCap) {
			return // 已达每日上限，不再入账
		}
	}
	final := r.BaseXP
	if bonus := b.xpBonus(userID); bonus > 0 && final > 0 {
		final = r.BaseXP * (100 + int(bonus)) / 100
	}
	b.recordEvent(userID, ruleKey, sourceType, sourceID, dedupeKey, r.BaseXP, final, "")
}

// recordExperience 幂等记账一条经验流水并重算资料。dedupeKey 唯一，重复/并发只落一条。
func (b *behavior) recordExperience(userID uint, ruleKey, sourceType, sourceID, dedupeKey string, xp int, reason string) {
	b.recordEvent(userID, ruleKey, sourceType, sourceID, dedupeKey, xp, xp, reason)
}

// recordEvent 同 recordExperience，基础经验与实得经验（含加成）分别记录。
func (b *behavior) recordEvent(userID uint, ruleKey, sourceType, sourceID, dedupeKey string, baseXP, finalXP int, reason string) {
	if !b.core.PluginEnabled(plugins.KeyGrowth) || userID == 0 || finalXP == 0 || dedupeKey == "" {
		return
	}
	ev := models.ExperienceEvent{
		UserID: userID, RuleKey: ruleKey, SourceType: sourceType, SourceID: sourceID,
		DedupeKey: dedupeKey, BaseXP: baseXP, FinalXP: finalXP, Reason: reason,
	}
	res := b.core.Gorm().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedupe_key"}}, DoNothing: true}).Create(&ev)
	if res.Error != nil || res.RowsAffected == 0 {
		return // 幂等：重复事件不重复结算
	}
	b.recalcGrowthProfile(userID)
}

// resolveLevel 按经验总量解析当前等级编号（取 min_xp<=xp 的最高 active 等级；无则 1）。
func (b *behavior) resolveLevel(xp int64) int {
	var lvl models.LevelDefinition
	if err := b.core.Gorm().Where("status = ? AND min_xp <= ?", "active", xp).Order("min_xp DESC, level DESC").First(&lvl).Error; err != nil {
		return 1
	}
	return lvl.Level
}

// recalcGrowthProfile 从经验流水重算用户成长资料（权威汇总），并在等级变化时写历史 + 升级通知。
func (b *behavior) recalcGrowthProfile(userID uint) {
	db := b.core.Gorm()
	var total int64
	db.Model(&models.ExperienceEvent{}).Where("user_id = ?", userID).Select("COALESCE(SUM(final_xp),0)").Scan(&total)
	newLevel := b.resolveLevel(total)

	var p models.UserGrowthProfile
	if err := db.Where("user_id = ?", userID).First(&p).Error; err != nil {
		p = models.UserGrowthProfile{UserID: userID, Public: true, CurrentLevel: 1, HighestLevel: 1}
	}
	oldLevel := p.CurrentLevel
	if oldLevel < 1 {
		oldLevel = 1
	}
	p.LifetimeXP = total
	p.CurrentLevel = newLevel
	if newLevel > p.HighestLevel {
		p.HighestLevel = newLevel
	}
	// 只更新经验/等级列：public 等用户设置不能被重算覆盖（public 有 default:true，整行 upsert 会把「隐藏」改回公开）
	db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"lifetime_xp", "current_level", "highest_level", "updated_at"}),
	}).Create(&p)

	if newLevel != oldLevel {
		db.Create(&models.UserLevelHistory{UserID: userID, FromLevel: oldLevel, ToLevel: newLevel, Reason: "xp"})
		if newLevel > oldLevel {
			name := fmt.Sprintf("Lv.%d", newLevel)
			var def models.LevelDefinition
			if db.Where("level = ?", newLevel).First(&def).Error == nil && def.Name != "" {
				name = def.Name
			}
			b.core.NotifyI18n(userID, "growth", "notify.growth.levelUp",
				map[string]string{"name": name, "level": strconv.Itoa(newLevel)}, map[string]any{"link": "/user/growth"})
		}
		// 成长→成就 双向联动：等级变化发出活动，触发 growth.* 指标成就重新评估（每级去重）
		plugincore.FireActivity(b.core, plugincore.ActivityEvent{
			UserID: userID, Type: "growth.level_changed", SourceType: "user",
			SourceID: strconv.FormatUint(uint64(userID), 10), DedupeKey: fmt.Sprintf("growth.level:%d:%d", userID, newLevel),
		})
	}
}

// dayStart 本地时区当天零点（与核心书籍分析的日桶一致）。
func dayStart(value time.Time) time.Time {
	local := value.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}

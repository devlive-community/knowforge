package membership

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

type behavior struct{ core plugincore.Core }

var (
	errPlanNotFound = errors.New("会员方案不存在")
	errPlanArchived = errors.New("会员方案已归档，不能再开通")
	errBadDuration  = fmt.Errorf("开通时长需在 1 到 %d 天之间", maxDurationDays)
)

// activeMembership 用户当前有效的会员及其方案（无或已到期返回 nil）。
func activeMembership(db *gorm.DB, userID uint, now time.Time) (*UserMembership, *Plan) {
	var m UserMembership
	if db.Where("user_id = ? AND expires_at > ?", userID, now).First(&m).Error != nil {
		return nil, nil
	}
	var p Plan
	if db.First(&p, m.PlanID).Error != nil {
		return nil, nil
	}
	return &m, &p
}

// planGrants 启用中的方案为某项权益设置的取值（按方案排序）。
func planGrants(core plugincore.Core, key string) []plugincore.EntitlementGrant {
	if !core.PluginEnabled(plugins.KeyMembership) {
		return nil
	}
	var plans []Plan
	core.Gorm().Where("status = ?", "active").Order("sort_order ASC, id ASC").Find(&plans)
	out := []plugincore.EntitlementGrant{}
	for i, p := range plans {
		if v, ok := p.Entitlements[key]; ok {
			out = append(out, plugincore.EntitlementGrant{Source: sourceKey, Label: p.Name, Rank: i, Value: v})
		}
	}
	return out
}

// resolveEntitlements 权益来源：有效会员独占（返回非 nil 即不再参考成长等级，未配置的键回退基础值）。
func resolveEntitlements(core plugincore.Core, u *models.User) (map[string]int64, bool) {
	if !core.PluginEnabled(plugins.KeyMembership) {
		return nil, false
	}
	_, plan := activeMembership(core.Gorm(), u.ID, time.Now())
	if plan == nil {
		return nil, false
	}
	values := make(map[string]int64, len(plan.Entitlements))
	for k, v := range plan.Entitlements {
		values[k] = v
	}
	return values, true
}

// grant 一次开通/续期请求（管理员开通或其他插件履约）。
type grant struct {
	UserID     uint
	PlanID     uint
	Days       int
	Source     string
	SourceRef  string
	OperatorID uint
	Reason     string
	// AllowArchived 已付款订单履约时允许归档方案（下单时方案仍在售）
	AllowArchived bool
}

// applyGrant 开通/续期/更换方案（在事务内调用）：
//   - 有效期内同方案 → 到期时间顺延 Days 天；
//   - 有效期内不同方案 → 从当前时间起按新方案计算（原方案剩余时长不保留）；
//   - 无会员或已到期 → 从当前时间起开通。
func applyGrant(tx *gorm.DB, g grant, now time.Time) (UserMembership, Plan, string, error) {
	var plan Plan
	if err := tx.First(&plan, g.PlanID).Error; err != nil {
		return UserMembership{}, plan, "", errPlanNotFound
	}
	if plan.Status != "active" && !g.AllowArchived {
		return UserMembership{}, plan, "", errPlanArchived
	}
	if g.Days < 1 || g.Days > maxDurationDays {
		return UserMembership{}, plan, "", errBadDuration
	}
	var m UserMembership
	found := tx.Where("user_id = ?", g.UserID).First(&m).Error == nil
	var prev *time.Time
	if found {
		p := m.ExpiresAt
		prev = &p
	}
	action := ActionGrant
	switch {
	case found && m.ExpiresAt.After(now) && m.PlanID == plan.ID:
		action = ActionExtend
		m.ExpiresAt = addDays(m.ExpiresAt, g.Days)
	case found && m.ExpiresAt.After(now):
		action = ActionSwitch
		m.PlanID, m.StartedAt, m.ExpiresAt = plan.ID, now, addDays(now, g.Days)
	default:
		m.UserID, m.PlanID, m.StartedAt, m.ExpiresAt = g.UserID, plan.ID, now, addDays(now, g.Days)
	}
	m.Trial = g.Source == sourceTrial
	if err := saveMembership(tx, &m, found); err != nil {
		return m, plan, "", err
	}
	expires := m.ExpiresAt
	err := tx.Create(&Record{
		UserID: g.UserID, PlanID: plan.ID, PlanName: plan.Name, Action: action, Days: g.Days,
		PrevExpiresAt: prev, ExpiresAt: &expires, Source: g.Source, SourceRef: g.SourceRef, OperatorID: g.OperatorID, Reason: g.Reason,
	}).Error
	return m, plan, action, err
}

// saveMembership 写入会员（有效期变化后清空提醒标记，下一周期重新提醒）。
func saveMembership(tx *gorm.DB, m *UserMembership, exists bool) error {
	m.RemindedAt, m.ExpiredNoticeAt = nil, nil
	if !exists {
		return tx.Create(m).Error
	}
	return tx.Model(&UserMembership{}).Where("user_id = ?", m.UserID).Updates(map[string]any{
		"plan_id": m.PlanID, "started_at": m.StartedAt, "expires_at": m.ExpiresAt, "trial": m.Trial,
		"reminded_at": nil, "expired_notice_at": nil, "updated_at": time.Now(),
	}).Error
}

// notifyChange 开通/续期/调整后通知用户。
func (b *behavior) notifyChange(userID uint, plan Plan, action string, expires time.Time) {
	emitChanged(b.core, userID, plan, "active", expires, false)
	key := "notify.membership.updated"
	if action == ActionGrant || action == ActionSwitch {
		key = "notify.membership.granted"
	}
	b.core.NotifyI18n(userID, notificationType, key, map[string]string{"plan": plan.Name, "date": formatDate(expires)}, map[string]any{"link": notificationLink})
}

// addDays 按绝对时长（24 小时/天）计算，与时区、夏令时无关（数据库读回的时间只带固定偏移）。
func addDays(t time.Time, days int) time.Time { return t.Add(time.Duration(days) * 24 * time.Hour) }

func formatDate(t time.Time) string { return t.Local().Format("2006-01-02") }

// sweep 周期巡检（约每小时）：到期前 N 天提醒一次；到期后补发一次「已到期」通知。
func sweep(core plugincore.Core, _ *jobqueue.Queue) {
	if !core.PluginEnabled(plugins.KeyMembership) {
		return
	}
	b := &behavior{core: core}
	db := core.Gorm()
	now := time.Now()
	notify := func(q *gorm.DB, key, trialKey, column string) {
		var rows []UserMembership
		if q.Limit(500).Find(&rows).Error != nil {
			return
		}
		for _, m := range rows {
			var plan Plan
			if db.First(&plan, m.PlanID).Error != nil {
				continue
			}
			// 先占位再发通知：并发巡检时只有更新成功的一方发送
			res := db.Model(&UserMembership{}).Where("user_id = ? AND "+column+" IS NULL", m.UserID).Update(column, now)
			if res.Error != nil || res.RowsAffected != 1 {
				continue
			}
			k := key
			if m.Trial {
				k = trialKey
			}
			core.NotifyI18n(m.UserID, notificationType, k, map[string]string{"plan": plan.Name, "date": formatDate(m.ExpiresAt)}, map[string]any{"link": notificationLink})
			if column == "expired_notice_at" {
				emitChanged(core, m.UserID, plan, "expired", m.ExpiresAt, m.Trial) // 已到期：会员指标变化
			}
		}
	}
	b.sweepRenewals(now) // 续费计划先处理（已处理的周期不再发普通到期提醒）
	if days := b.reminderDays(); days > 0 {
		notify(db.Where("reminded_at IS NULL AND expires_at > ? AND expires_at <= ?", now, addDays(now, days)), "notify.membership.expiring", "notify.membership.trialExpiring", "reminded_at")
	}
	notify(db.Where("expired_notice_at IS NULL AND expires_at <= ? AND expires_at > ?", now, addDays(now, -expiredNoticeRange)), "notify.membership.expired", "notify.membership.trialExpired", "expired_notice_at")
}

// —— 设置 ——

func (b *behavior) currency() string {
	if v := strings.ToUpper(strings.TrimSpace(b.core.GetSetting(cfgCurrency))); validCurrency(v) {
		return v
	}
	return defaultCurrency
}

func (b *behavior) reminderDays() int {
	raw := b.core.GetSetting(cfgReminderDays)
	if raw == "" {
		return defaultReminder
	}
	if v, err := strconv.Atoi(raw); err == nil && v >= 0 && v <= maxReminderDays {
		return v
	}
	return defaultReminder
}

func validCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

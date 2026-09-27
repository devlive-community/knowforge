package membership

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/plugincore"
)

// 自动续费：用户为当前方案选一档时长开启续费计划。到期前：
//   - 有可用的周期扣款能力（支付插件在渠道开通签约后经 plugincore 提供）时自动扣款续期，失败每天重试，共 3 次，仍失败则提醒手动续费；
//   - 没有时发一条「一键续费」通知，链接直达该档价格的结算页。
// 每个会员周期（以到期时间区分）只处理一次；方案下架、价格删除或已更换方案时自动关闭续费计划并通知。

const (
	renewChargeLead  = 24 * time.Hour // 自动扣款提前量
	renewNoticeDays  = 3              // 未设置到期提醒天数时，一键续费通知的提前天数
	renewMaxAttempts = 3
	renewRetryDelay  = 24 * time.Hour
)

// Renewal 用户的续费计划（每人一条）。
type Renewal struct {
	UserID  uint `gorm:"primaryKey" json:"-"`
	PriceID uint `json:"price_id"`
	Enabled bool `json:"enabled"`
	// CycleExpiresAt 已处理完毕的会员到期时间（本周期已续期或已通知，不再处理）
	CycleExpiresAt *time.Time `json:"-"`
	Attempts       int        `json:"attempts"`
	NextAttemptAt  *time.Time `json:"-"`
	LastError      string     `gorm:"size:500" json:"last_error"`
	LastOrderNo    string     `gorm:"size:40" json:"last_order_no"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (Renewal) TableName() string { return "membership_renewals" }

// renewalView 续费计划与当前生效方式。
type renewalView struct {
	Renewal
	Mode        string     `json:"mode"`                    // auto（自动扣款）| notify（到期前提醒一键续费）
	NextRenewAt *time.Time `json:"next_renew_at,omitempty"` // 预计处理时间
}

func (b *behavior) renewalNoticeDays() int {
	if d := b.reminderDays(); d > 0 {
		return d
	}
	return renewNoticeDays
}

// renewalMode 当前是否可自动扣款。
func (b *behavior) renewalMode(userID uint) string {
	if _, ok := plugincore.RecurringChargerFor(b.core, userID, b.currency()); ok {
		return "auto"
	}
	return "notify"
}

// MyRenewal GET /users/me/membership/renewal 我的续费计划。
func (b *behavior) MyRenewal(c *gin.Context) {
	u := b.core.CurrentUser(c)
	var r Renewal
	b.core.Gorm().Where("user_id = ?", u.ID).First(&r)
	v := renewalView{Renewal: r, Mode: b.renewalMode(u.ID)}
	var m UserMembership
	if r.Enabled && b.core.Gorm().Where("user_id = ?", u.ID).First(&m).Error == nil && m.ExpiresAt.After(time.Now()) {
		at := addDays(m.ExpiresAt, -b.renewalNoticeDays())
		if v.Mode == "auto" {
			at = m.ExpiresAt.Add(-renewChargeLead)
		}
		v.NextRenewAt = &at
	}
	b.core.OK(c, v)
}

// UpdateRenewal PUT /users/me/membership/renewal {enabled, price_id?} 开启（选当前方案的一档时长）或关闭续费计划。
func (b *behavior) UpdateRenewal(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
		PriceID uint `json:"price_id"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	u := b.core.CurrentUser(c)
	db := b.core.Gorm()
	r := Renewal{UserID: u.ID}
	db.Where("user_id = ?", u.ID).First(&r)
	if req.Enabled {
		m, plan := activeMembership(db, u.ID, time.Now())
		if m == nil {
			b.core.Fail(c, http.StatusBadRequest, "开通会员后才能设置续费")
			return
		}
		if m.Trial {
			b.core.Fail(c, http.StatusBadRequest, "试用期间不能设置续费，请先购买会员")
			return
		}
		var price Price
		if db.First(&price, req.PriceID).Error != nil || price.PlanID != plan.ID || plan.Status != "active" {
			b.core.Fail(c, http.StatusBadRequest, "请选择当前方案的一档时长")
			return
		}
		r.PriceID = price.ID
		r.Attempts, r.NextAttemptAt, r.LastError = 0, nil, ""
	}
	r.Enabled = req.Enabled
	if err := db.Save(&r).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.MyRenewal(c)
}

// sweepRenewals 巡检：处理即将到期的续费计划（在到期提醒之前运行，处理过的周期不再发普通到期提醒）。
func (b *behavior) sweepRenewals(now time.Time) {
	db := b.core.Gorm()
	var rows []Renewal
	db.Where("enabled = ?", true).Limit(500).Find(&rows)
	for _, r := range rows {
		b.processRenewal(r, now)
	}
}

func (b *behavior) processRenewal(r Renewal, now time.Time) {
	db := b.core.Gorm()
	var m UserMembership
	if db.Where("user_id = ?", r.UserID).First(&m).Error != nil || !m.ExpiresAt.After(now) {
		return // 没有有效会员（已到期的按普通流程通知）
	}
	if r.CycleExpiresAt != nil && r.CycleExpiresAt.Equal(m.ExpiresAt) {
		return // 本周期已处理
	}
	var price Price
	var plan Plan
	if db.First(&price, r.PriceID).Error != nil || db.First(&plan, price.PlanID).Error != nil || plan.Status != "active" || price.PlanID != m.PlanID {
		db.Model(&Renewal{}).Where("user_id = ?", r.UserID).Updates(map[string]any{"enabled": false, "last_error": "续费方案已下架或已更换方案"})
		b.core.NotifyI18n(r.UserID, notificationType, "notify.membership.renewalDisabled", map[string]string{"plan": plan.Name}, map[string]any{"link": notificationLink})
		return
	}
	charger, auto := plugincore.RecurringChargerFor(b.core, r.UserID, b.currency())
	if auto {
		if m.ExpiresAt.Sub(now) > renewChargeLead || (r.NextAttemptAt != nil && now.Before(*r.NextAttemptAt)) {
			return
		}
		b.autoRenew(r, m, plan, price, charger, now)
		return
	}
	if m.ExpiresAt.After(addDays(now, b.renewalNoticeDays())) {
		return
	}
	b.renewalNotice(r, m, plan, price, "notify.membership.renewalDue", now)
}

// autoRenew 自动扣款续期；失败按间隔重试，用完次数后提醒手动续费。
func (b *behavior) autoRenew(r Renewal, m UserMembership, plan Plan, price Price, charger plugincore.RecurringCharger, now time.Time) {
	db := b.core.Gorm()
	// 先占位：并发巡检时只有一方发起扣款
	claim := db.Model(&Renewal{}).Where("user_id = ? AND attempts = ?", r.UserID, r.Attempts).
		Updates(map[string]any{"attempts": r.Attempts + 1, "next_attempt_at": now.Add(renewRetryDelay)})
	if claim.RowsAffected != 1 {
		return
	}
	orderNo, err := charger.Charge(context.Background(), b.core, r.UserID, productKind, strconv.FormatUint(uint64(price.ID), 10))
	if err == nil {
		db.Model(&Renewal{}).Where("user_id = ?", r.UserID).Updates(map[string]any{
			"cycle_expires_at": m.ExpiresAt, "attempts": 0, "next_attempt_at": nil, "last_error": "", "last_order_no": orderNo,
		})
		return // 履约与开通通知由订单流程完成
	}
	db.Model(&Renewal{}).Where("user_id = ?", r.UserID).Update("last_error", truncate(err.Error(), 500))
	if r.Attempts+1 >= renewMaxAttempts {
		b.renewalNotice(r, m, plan, price, "notify.membership.renewalFailed", now)
	}
}

// renewalNotice 本周期发一次续费通知（一键续费链接直达该档价格的结算页），并视为已发过到期提醒。
func (b *behavior) renewalNotice(r Renewal, m UserMembership, plan Plan, price Price, key string, now time.Time) {
	db := b.core.Gorm()
	res := db.Model(&Renewal{}).Where("user_id = ? AND (cycle_expires_at IS NULL OR cycle_expires_at <> ?)", r.UserID, m.ExpiresAt).
		Updates(map[string]any{"cycle_expires_at": m.ExpiresAt, "attempts": 0, "next_attempt_at": nil})
	if res.RowsAffected != 1 {
		return
	}
	db.Model(&UserMembership{}).Where("user_id = ? AND reminded_at IS NULL", r.UserID).Update("reminded_at", now)
	link := "/pay/checkout?kind=" + productKind + "&sku=" + strconv.FormatUint(uint64(price.ID), 10)
	b.core.NotifyI18n(r.UserID, notificationType, key, map[string]string{"plan": plan.Name, "date": formatDate(m.ExpiresAt)}, map[string]any{"link": link})
}

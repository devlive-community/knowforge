package membership

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 邀请奖励：沿用站点的邀请码（注册时记录 User.InvitedBy），在被邀请人首次购买会员后奖励邀请人与被邀请人会员天数；
// 可选在被邀请人验证邮箱后奖励邀请人（默认关闭，防刷）。每位邀请人每月获得奖励的人数有上限；奖励天数加在当前会员方案上，
// 没有会员时开通所设的奖励方案。触发奖励的订单退款并撤销时按比例扣回双方的奖励。

const (
	sourceReferral = "referral"

	referralSignup   = "signup"   // 被邀请人验证邮箱
	referralPurchase = "purchase" // 被邀请人首次购买会员

	cfgReferral       = "membership_referral"
	maxReferralDays   = 365
	maxReferralMonthy = 1000
)

// ReferralReward 一次邀请奖励（同一被邀请人每种触发只奖励一次）。InviterDays 为 0 表示邀请人当月已达上限未获奖励。
type ReferralReward struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	InviterID   uint      `gorm:"index" json:"inviter_id"`
	InviteeID   uint      `gorm:"uniqueIndex:idx_referral_reward" json:"invitee_id"`
	Kind        string    `gorm:"size:10;uniqueIndex:idx_referral_reward" json:"kind"`
	OrderNo     string    `gorm:"size:40;index" json:"order_no,omitempty"`
	InviterDays int       `json:"inviter_days"`
	InviteeDays int       `json:"invitee_days"`
	Capped      bool      `json:"capped"`
	CreatedAt   time.Time `json:"created_at"`
}

func (ReferralReward) TableName() string { return "membership_referral_rewards" }

// referralConfig 邀请奖励设置（整体存为一个 JSON 设置项）。
type referralConfig struct {
	Enabled      bool `json:"enabled"`
	PlanID       uint `json:"plan_id"`       // 没有会员时开通的方案
	InviterDays  int  `json:"inviter_days"`  // 被邀请人首次购买后邀请人获得的天数
	InviteeDays  int  `json:"invitee_days"`  // 被邀请人首次购买后额外获得的天数
	SignupDays   int  `json:"signup_days"`   // 被邀请人验证邮箱后邀请人获得的天数（0 为不奖励）
	MonthlyLimit int  `json:"monthly_limit"` // 每位邀请人每月最多获得奖励的次数（0 为不限）
}

func defaultReferralConfig() referralConfig {
	return referralConfig{InviterDays: 30, InviteeDays: 7, MonthlyLimit: 10}
}

func (b *behavior) referralConfig() referralConfig {
	cfg := defaultReferralConfig()
	if raw := b.core.GetSetting(cfgReferral); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	return cfg
}

// active 邀请奖励是否生效（开启且奖励方案有效）。
func (b *behavior) referralActive(cfg referralConfig) bool {
	if !cfg.Enabled || cfg.PlanID == 0 || !b.core.PluginEnabled(plugins.KeyMembership) {
		return false
	}
	var n int64
	b.core.Gorm().Model(&Plan{}).Where("id = ?", cfg.PlanID).Count(&n)
	return n > 0
}

func init() {
	plugincore.OnActivity(func(core plugincore.Core, ev plugincore.ActivityEvent) {
		if ev.Type == "account.registered" || ev.Type == "account.email_verified" {
			(&behavior{core: core}).rewardSignup(ev.UserID)
		}
	})
}

// grantBonus 在事务内奖励 days 天：有有效会员时加在当前方案上，否则开通 planID。
func grantBonus(tx *gorm.DB, userID, planID uint, days int, ref, reason string, now time.Time) (UserMembership, Plan, string, error) {
	if m, _ := activeMembership(tx, userID, now); m != nil {
		planID = m.PlanID
	}
	return applyGrant(tx, grant{UserID: userID, PlanID: planID, Days: days, Source: sourceReferral, SourceRef: ref, Reason: reason, AllowArchived: true}, now)
}

// capped 邀请人本月获得奖励的次数是否已达上限。
func referralCapped(tx *gorm.DB, cfg referralConfig, inviterID uint, now time.Time) bool {
	if cfg.MonthlyLimit <= 0 {
		return false
	}
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var n int64
	tx.Model(&ReferralReward{}).Where("inviter_id = ? AND inviter_days > 0 AND created_at >= ?", inviterID, start).Count(&n)
	return n >= int64(cfg.MonthlyLimit)
}

type bonusNotice struct {
	userID uint
	key    string
	plan   Plan
	days   int
	expire time.Time
}

func (n bonusNotice) send(core plugincore.Core) {
	emitChanged(core, n.userID)
	core.NotifyI18n(n.userID, notificationType, n.key, map[string]string{"plan": n.plan.Name, "days": strconv.Itoa(n.days), "date": formatDate(n.expire)},
		map[string]any{"link": notificationLink})
}

// reward 在事务内记录并发放一次邀请奖励（按被邀请人与触发类型幂等）。
func (b *behavior) reward(invitee *models.User, kind, orderNo string, inviterDays, inviteeDays int) {
	cfg := b.referralConfig()
	if invitee.InvitedBy == 0 || invitee.InvitedBy == invitee.ID || !b.referralActive(cfg) {
		return
	}
	var notices []bonusNotice
	now := time.Now()
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		rw := ReferralReward{InviterID: invitee.InvitedBy, InviteeID: invitee.ID, Kind: kind, OrderNo: orderNo, InviterDays: inviterDays, InviteeDays: inviteeDays}
		if referralCapped(tx, cfg, rw.InviterID, now) {
			rw.InviterDays, rw.Capped = 0, true
		}
		if res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rw); res.Error != nil || res.RowsAffected == 0 {
			return res.Error // 已奖励过
		}
		ref := "referral:" + strconv.FormatUint(uint64(rw.ID), 10)
		if rw.InviterDays > 0 {
			m, plan, _, err := grantBonus(tx, rw.InviterID, cfg.PlanID, rw.InviterDays, ref, fmt.Sprintf("邀请 %s 的奖励", invitee.Username), now)
			if err != nil {
				return err
			}
			notices = append(notices, bonusNotice{rw.InviterID, "notify.membership.referralReward", plan, rw.InviterDays, m.ExpiresAt})
		}
		if rw.InviteeDays > 0 {
			m, plan, _, err := grantBonus(tx, invitee.ID, cfg.PlanID, rw.InviteeDays, ref, "受邀开通会员的额外奖励", now)
			if err != nil {
				return err
			}
			notices = append(notices, bonusNotice{invitee.ID, "notify.membership.referralBonus", plan, rw.InviteeDays, m.ExpiresAt})
		}
		return nil
	})
	if err != nil {
		return
	}
	for _, n := range notices {
		n.send(b.core)
	}
}

// rewardSignup 被邀请人验证邮箱后奖励邀请人（未设置天数时不奖励）。
func (b *behavior) rewardSignup(userID uint) {
	cfg := b.referralConfig()
	if cfg.SignupDays <= 0 {
		return
	}
	var u models.User
	if b.core.Gorm().First(&u, userID).Error != nil || !u.EmailVerified {
		return
	}
	b.reward(&u, referralSignup, "", cfg.SignupDays, 0)
}

// rewardPurchase 被邀请人首次购买会员（订单履约后调用）。
func (b *behavior) rewardPurchase(userID uint, orderNo string) {
	cfg := b.referralConfig()
	var u models.User
	if b.core.Gorm().First(&u, userID).Error != nil || u.InvitedBy == 0 {
		return
	}
	var orders int64
	b.core.Gorm().Model(&Record{}).Where("user_id = ? AND source = ?", userID, orderSource).Count(&orders)
	if orders > 1 { // 只奖励首次购买
		return
	}
	b.reward(&u, referralPurchase, orderNo, cfg.InviterDays, cfg.InviteeDays)
}

// clawbackReferral 触发奖励的订单退款并撤销：在事务内按比例扣回邀请人的奖励，返回被邀请人应一并扣回的奖励天数。
func clawbackReferral(tx *gorm.DB, ev plugincore.RefundEvent, now time.Time) (int, []deduction, error) {
	var rw ReferralReward
	if tx.Where("order_no = ? AND kind = ?", ev.OrderNo, referralPurchase).First(&rw).Error != nil {
		return 0, nil, nil
	}
	var out []deduction
	if days := proportionalDays(rw.InviterDays, ev); days > 0 {
		d, err := deductDays(tx, rw.InviterID, days, ev.RefundNo, "被邀请人的订单 %s 退款，扣回邀请奖励 %d 天", ev.OrderNo, now)
		if err != nil {
			return 0, nil, err
		}
		out = append(out, d)
	}
	return proportionalDays(rw.InviteeDays, ev), out, nil
}

// —— 接口 ——

// MyReferral GET /users/me/membership/referral 邀请奖励规则与我获得的奖励。
func (b *behavior) MyReferral(c *gin.Context) {
	u := b.core.CurrentUser(c)
	cfg := b.referralConfig()
	active := b.referralActive(cfg)
	out := gin.H{"enabled": active}
	if active {
		var plan Plan
		b.core.Gorm().First(&plan, cfg.PlanID)
		out["rules"] = gin.H{"inviter_days": cfg.InviterDays, "invitee_days": cfg.InviteeDays, "signup_days": cfg.SignupDays, "monthly_limit": cfg.MonthlyLimit, "plan_name": plan.Name}
	}
	var rows []ReferralReward
	b.core.Gorm().Where("inviter_id = ?", u.ID).Order("id DESC").Limit(50).Find(&rows)
	ids := make([]uint, 0, len(rows))
	total := 0
	for _, r := range rows {
		ids = append(ids, r.InviteeID)
	}
	users := b.userBriefs(ids)
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		total += r.InviterDays
		items = append(items, gin.H{"kind": r.Kind, "days": r.InviterDays, "capped": r.Capped, "created_at": r.CreatedAt, "invitee": users[r.InviteeID]})
	}
	out["rewards"], out["total_days"] = items, total
	b.core.OK(c, out)
}

// AdminGetReferral GET /admin/membership/referral 设置与最近的奖励。
func (b *behavior) AdminGetReferral(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&ReferralReward{})
	var total int64
	q.Count(&total)
	var rows []ReferralReward
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	ids := make([]uint, 0, len(rows)*2)
	for _, r := range rows {
		ids = append(ids, r.InviterID, r.InviteeID)
	}
	users := b.userBriefs(ids)
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, gin.H{"reward": r, "inviter": users[r.InviterID], "invitee": users[r.InviteeID]})
	}
	b.core.OK(c, gin.H{"settings": b.referralConfig(), "items": items, "total": total, "page": page, "page_size": pageSize})
}

// AdminUpdateReferral PUT /admin/membership/referral 保存设置。
func (b *behavior) AdminUpdateReferral(c *gin.Context) {
	var req referralConfig
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := validateReferral(b.core.Gorm(), req); err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	raw, _ := json.Marshal(req)
	if err := b.core.SetSetting(cfgReferral, string(raw), "会员：邀请奖励设置"); err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	b.core.RecordAudit(c, "membership.referral_updated", "membership", "referral", "邀请奖励", changedFields("referral"))
	b.AdminGetReferral(c)
}

func validateReferral(db *gorm.DB, cfg referralConfig) error {
	for _, d := range []int{cfg.InviterDays, cfg.InviteeDays, cfg.SignupDays} {
		if d < 0 || d > maxReferralDays {
			return fmt.Errorf("奖励天数需在 0 到 %d 之间", maxReferralDays)
		}
	}
	if cfg.MonthlyLimit < 0 || cfg.MonthlyLimit > maxReferralMonthy {
		return errors.New("每月奖励上限无效")
	}
	if cfg.Enabled {
		var plan Plan
		if cfg.PlanID == 0 || db.First(&plan, cfg.PlanID).Error != nil {
			return errors.New("请选择奖励的会员方案")
		}
	}
	return nil
}

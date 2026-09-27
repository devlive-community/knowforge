package membership

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"knowforge/server/internal/models"
)

// 免费试用：方案可设置试用天数，从未开通过会员的用户可免费试用一次（默认需先验证邮箱）。试用经 applyGrant 开通（流水来源 trial），
// 会员标记为试用中；试用期内购买同一方案会在剩余试用期后顺延，购买、兑换或管理员调整后不再是试用。

const sourceTrial = "trial"

// TrialUse 用户已领取的试用（每人一条，防止并发重复领取）。
type TrialUse struct {
	UserID    uint      `gorm:"primaryKey" json:"user_id"`
	PlanID    uint      `json:"plan_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (TrialUse) TableName() string { return "membership_trial_uses" }

var (
	errTrialUsed       = errors.New("每位用户只能试用一次，且仅限从未开通过会员的用户")
	errTrialUnverified = errors.New("请先验证邮箱再领取试用")
	errTrialNone       = errors.New("该方案不提供试用")
)

func (b *behavior) trialNeedsVerifiedEmail() bool {
	return b.core.GetSetting(cfgTrialVerified) != "false"
}

// trialBlocker 用户不能试用的原因（nil 表示可以）。
func (b *behavior) trialBlocker(u *models.User) error {
	db := b.core.Gorm()
	var n int64
	db.Model(&TrialUse{}).Where("user_id = ?", u.ID).Count(&n)
	if n == 0 {
		db.Model(&Record{}).Where("user_id = ?", u.ID).Count(&n)
	}
	if n > 0 {
		return errTrialUsed
	}
	if b.trialNeedsVerifiedEmail() && !u.EmailVerified {
		return errTrialUnverified
	}
	return nil
}

// StartTrial POST /membership/plans/:id/trial 领取方案的免费试用。
func (b *behavior) StartTrial(c *gin.Context) {
	u := b.core.CurrentUser(c)
	var plan Plan
	if b.core.Gorm().First(&plan, c.Param("id")).Error != nil || plan.Status != "active" {
		b.core.Fail(c, http.StatusNotFound, errPlanNotFound.Error())
		return
	}
	if plan.TrialDays <= 0 {
		b.core.Fail(c, http.StatusBadRequest, errTrialNone.Error())
		return
	}
	if err := b.trialBlocker(u); err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var m UserMembership
	err := b.core.Gorm().Transaction(func(tx *gorm.DB) error {
		if res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&TrialUse{UserID: u.ID, PlanID: plan.ID}); res.Error != nil || res.RowsAffected == 0 {
			return errTrialUsed
		}
		var err error
		m, _, _, err = applyGrant(tx, grant{UserID: u.ID, PlanID: plan.ID, Days: plan.TrialDays, Source: sourceTrial, Reason: "免费试用"}, time.Now())
		return err
	})
	if err != nil {
		b.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	b.core.NotifyI18n(u.ID, notificationType, "notify.membership.trialStarted", map[string]string{"plan": plan.Name, "date": formatDate(m.ExpiresAt)}, map[string]any{"link": notificationLink})
	b.core.OK(c, gin.H{"plan": gin.H{"id": plan.ID, "name": plan.Name}, "expires_at": m.ExpiresAt, "trial": true})
}

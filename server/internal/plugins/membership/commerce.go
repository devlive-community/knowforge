package membership

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 在线购买：把方案的每档价格登记为可售商品（kind=membership，SKU=价格 ID），由支付插件经 plugincore 下单与回调履约；
// 本插件不感知具体支付方式。

const (
	productKind  = "membership"
	orderSource  = "order"
	refundSource = "refund"
)

func init() {
	plugincore.RegisterProductProvider(plugincore.ProductProvider{Kind: productKind, Resolve: resolveProduct, Fulfill: fulfillOrder, Refund: refundOrder})
}

// resolveProduct 按价格 ID 解析可购买的会员商品（方案须启用中）。
func resolveProduct(core plugincore.Core, _ *models.User, sku string) (plugincore.Product, error) {
	if !core.PluginEnabled(plugins.KeyMembership) {
		return plugincore.Product{}, errors.New("会员功能未启用")
	}
	id, err := strconv.ParseUint(sku, 10, 64)
	if err != nil {
		return plugincore.Product{}, errors.New("商品不存在")
	}
	var price Price
	var plan Plan
	db := core.Gorm()
	if db.First(&price, id).Error != nil || db.First(&plan, price.PlanID).Error != nil {
		return plugincore.Product{}, errors.New("商品不存在")
	}
	if plan.Status != "active" {
		return plugincore.Product{}, errPlanArchived
	}
	b := &behavior{core: core}
	return plugincore.Product{
		Kind: productKind, SKU: sku, Title: plan.Name, Description: plan.Description, DurationDays: price.DurationDays,
		AmountCents: price.PriceCents, Currency: b.currency(), ReturnLink: notificationLink,
		Payload: map[string]any{"plan_id": plan.ID, "price_id": price.ID, "days": price.DurationDays},
	}, nil
}

// fulfillOrder 订单支付成功后开通/续期（按订单号幂等：已有该订单的会员流水则跳过）。
func fulfillOrder(core plugincore.Core, userID uint, orderNo string, payload map[string]any) error {
	planID, days := payloadInt(payload["plan_id"]), payloadInt(payload["days"])
	if planID <= 0 || days <= 0 {
		return fmt.Errorf("订单 %s 的会员快照无效", orderNo)
	}
	var (
		m      UserMembership
		plan   Plan
		action string
		done   bool
	)
	err := core.Gorm().Transaction(func(tx *gorm.DB) error {
		var n int64
		tx.Model(&Record{}).Where("source = ? AND source_ref = ?", orderSource, orderNo).Count(&n)
		if n > 0 {
			done = true
			return nil
		}
		var err error
		m, plan, action, err = applyGrant(tx, grant{UserID: userID, PlanID: uint(planID), Days: int(days), Source: orderSource, SourceRef: orderNo,
			Reason: orderNo, AllowArchived: true}, time.Now())
		return err
	})
	if err != nil || done {
		return err
	}
	(&behavior{core: core}).notifyChange(userID, plan, action, m.ExpiresAt)
	return nil
}

// payloadInt 读取快照中的整数（JSON 往返后为 float64）。
func payloadInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case uint:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

// refundOrder 订单退款：选择撤销时扣回该订单开通的天数（部分退款按比例），扣完则会员结束。
// 按退款单号幂等（已有该退款的会员流水则跳过）；会员没有需要冲回的财务。
func refundOrder(core plugincore.Core, ev plugincore.RefundEvent) error {
	if !ev.Revoke || ev.TotalCents <= 0 {
		return nil
	}
	var d deduction
	err := core.Gorm().Transaction(func(tx *gorm.DB) error {
		var n int64
		tx.Model(&Record{}).Where("source = ? AND source_ref = ?", refundSource, ev.RefundNo).Count(&n)
		var granted Record
		if n > 0 || tx.Where("source = ? AND source_ref = ?", orderSource, ev.OrderNo).First(&granted).Error != nil {
			return nil // 已处理，或该订单从未开通（无需扣回）
		}
		var err error
		d, err = deductDays(tx, ev.UserID, proportionalDays(granted.Days, ev), ev.RefundNo, "订单 %s 退款，扣回 %d 天", ev.OrderNo, time.Now())
		return err
	})
	if err != nil {
		return err
	}
	d.notify(core)
	return nil
}

// proportionalDays 按退款金额占订单金额的比例折算天数。
func proportionalDays(days int, ev plugincore.RefundEvent) int {
	return int(math.Round(float64(days) * float64(ev.AmountCents) / float64(ev.TotalCents)))
}

// deduction 一次扣回会员天数的结果（用于事务提交后通知）。
type deduction struct {
	userID  uint
	plan    Plan
	expires time.Time
	ended   bool
	done    bool // 确实扣回了
}

// deductDays 在事务内从用户的会员中扣回 days 天（流水来源 refund，关联退款单号），扣完则会员结束；
// 用户没有会员或 days ≤ 0 时不处理。reason 为格式串，参数为 ref 与天数。
func deductDays(tx *gorm.DB, userID uint, days int, refundNo, reason, ref string, now time.Time) (deduction, error) {
	d := deduction{userID: userID}
	var m UserMembership
	if days <= 0 || tx.Where("user_id = ?", userID).First(&m).Error != nil {
		return d, nil
	}
	tx.First(&d.plan, m.PlanID)
	prev := m.ExpiresAt
	d.expires = addDays(m.ExpiresAt, -days)
	action := ActionAdjust
	if !d.expires.After(now) {
		d.ended, action, d.expires = true, ActionRevoke, now
		if err := tx.Where("user_id = ?", userID).Delete(&UserMembership{}).Error; err != nil {
			return d, err
		}
	} else if err := tx.Model(&UserMembership{}).Where("user_id = ?", userID).
		Updates(map[string]any{"expires_at": d.expires, "reminded_at": nil, "expired_notice_at": nil, "updated_at": now}).Error; err != nil {
		return d, err
	}
	d.done = true
	return d, tx.Create(&Record{UserID: userID, PlanID: m.PlanID, PlanName: d.plan.Name, Action: action, Days: days,
		PrevExpiresAt: &prev, ExpiresAt: &d.expires, Source: refundSource, SourceRef: refundNo,
		Reason: truncate(fmt.Sprintf(reason, ref, days), 255)}).Error
}

// notify 通知用户会员被扣回（结束或有效期调整）。
func (d deduction) notify(core plugincore.Core) {
	if !d.done {
		return
	}
	if d.ended {
		core.NotifyI18n(d.userID, notificationType, "notify.membership.revoked", map[string]string{"plan": d.plan.Name}, map[string]any{"link": notificationLink})
	} else {
		(&behavior{core: core}).notifyChange(d.userID, d.plan, ActionAdjust, d.expires)
	}
}

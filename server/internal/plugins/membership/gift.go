package membership

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 礼品卡：用户在线购买方案的某档价格（商品 kind=membership_gift，SKU=价格 ID），支付成功后生成一个属于购买者的一次性兑换码，
// 可复制送给他人（或自己）兑换；兑换后通知购买者。退款选择撤销时：未兑换的礼品卡按比例缩短天数、全额则作废；
// 已兑换的按比例从兑换者的会员中扣回。

const (
	giftProductKind = "membership_gift"
	kindGift        = "gift"
)

// GiftRefund 礼品卡的一次退款（按退款单号幂等）。
type GiftRefund struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	RefundNo  string    `gorm:"size:64;uniqueIndex" json:"refund_no"`
	BatchID   uint      `gorm:"index" json:"batch_id"`
	Days      int       `json:"days"`
	CreatedAt time.Time `json:"created_at"`
}

func (GiftRefund) TableName() string { return "membership_gift_refunds" }

func init() {
	plugincore.RegisterProductProvider(plugincore.ProductProvider{Kind: giftProductKind, Resolve: resolveGift, Fulfill: fulfillGift, Refund: refundGift})
}

// resolveGift 与购买会员相同的价格，商品为礼品卡。
func resolveGift(core plugincore.Core, u *models.User, sku string) (plugincore.Product, error) {
	p, err := resolveProduct(core, u, sku)
	if err != nil {
		return p, err
	}
	p.Kind = giftProductKind
	p.Title += "（礼品卡）"
	p.Description = "付款后获得一个兑换码，可送给他人开通会员"
	return p, nil
}

// fulfillGift 支付成功后为购买者生成礼品卡（按订单号幂等）。
func fulfillGift(core plugincore.Core, userID uint, orderNo string, payload map[string]any) error {
	planID, days := payloadInt(payload["plan_id"]), payloadInt(payload["days"])
	if planID <= 0 || days <= 0 {
		return errors.New("订单 " + orderNo + " 的礼品卡快照无效")
	}
	var plan Plan
	created := false
	err := core.Gorm().Transaction(func(tx *gorm.DB) error {
		var n int64
		tx.Model(&RedeemBatch{}).Where("order_no = ?", orderNo).Count(&n)
		if n > 0 {
			return nil
		}
		tx.First(&plan, planID)
		batch := RedeemBatch{Name: "礼品卡 " + orderNo, PlanID: uint(planID), Days: int(days), Kind: kindGift, MaxUses: 1,
			Status: "active", CreatedBy: userID, OrderNo: orderNo}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		created = true
		_, err := createCodes(tx, batch, 1, userID)
		return err
	})
	if err != nil || !created {
		return err
	}
	core.NotifyI18n(userID, notificationType, "notify.membership.giftReady", map[string]string{"plan": plan.Name}, map[string]any{"link": notificationLink})
	return nil
}

// refundGift 礼品卡订单退款：选择撤销时，未兑换的按比例缩短天数（扣完则作废），已兑换的从兑换者的会员中扣回。
func refundGift(core plugincore.Core, ev plugincore.RefundEvent) error {
	if !ev.Revoke || ev.TotalCents <= 0 {
		return nil
	}
	var d deduction
	err := core.Gorm().Transaction(func(tx *gorm.DB) error {
		var batch RedeemBatch
		if tx.Where("order_no = ? AND kind = ?", ev.OrderNo, kindGift).First(&batch).Error != nil {
			return nil // 尚未生成礼品卡
		}
		// 与兑换互斥：锁住兑换码后再判断是否已兑换
		var rc RedeemCode
		if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("batch_id = ?", batch.ID).First(&rc).Error != nil {
			return nil
		}
		// 按原始天数折算（之前的部分退款已缩短过 batch.Days）
		var refunded struct{ N int }
		tx.Model(&GiftRefund{}).Select("COALESCE(SUM(days), 0) AS n").Where("batch_id = ?", batch.ID).Scan(&refunded)
		days := proportionalDays(batch.Days+refunded.N, ev)
		if res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&GiftRefund{RefundNo: ev.RefundNo, BatchID: batch.ID, Days: days}); res.Error != nil || res.RowsAffected == 0 {
			return res.Error // 已处理
		}
		left := max(batch.Days-days, 0)
		if err := tx.Model(&RedeemBatch{}).Where("id = ?", batch.ID).Update("days", left).Error; err != nil {
			return err
		}
		var use RedeemUse
		if tx.Where("code_id = ?", rc.ID).First(&use).Error == nil {
			var err error
			d, err = deductDays(tx, use.UserID, days, ev.RefundNo, "礼品卡订单 %s 退款，扣回 %d 天", ev.OrderNo, time.Now())
			return err
		}
		if left == 0 { // 未兑换且已退完：作废
			if err := tx.Model(&RedeemCode{}).Where("id = ?", rc.ID).Update("disabled", true).Error; err != nil {
				return err
			}
			return tx.Model(&RedeemBatch{}).Where("id = ?", batch.ID).Update("status", "disabled").Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.notify(core)
	return nil
}

// giftView 我购买的一张礼品卡。
type giftView struct {
	ID           uint       `json:"id"`
	Code         string     `json:"code"`
	PlanName     string     `json:"plan_name"`
	Days         int        `json:"days"`
	OrderNo      string     `json:"order_no"`
	Status       string     `json:"status"` // unused | redeemed | void
	RedeemedAt   *time.Time `json:"redeemed_at,omitempty"`
	RedeemedByMe bool       `json:"redeemed_by_me"`
	CreatedAt    time.Time  `json:"created_at"`
}

// MyGifts GET /users/me/membership/gifts?page=&page_size= 我购买的礼品卡（新→旧）。
func (b *behavior) MyGifts(c *gin.Context) {
	u := b.core.CurrentUser(c)
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm()
	q := db.Model(&RedeemCode{}).Joins("JOIN membership_redeem_batches ON membership_redeem_batches.id = membership_redeem_codes.batch_id").
		Where("membership_redeem_codes.owner_id = ? AND membership_redeem_batches.kind = ?", u.ID, kindGift)
	var total int64
	q.Count(&total)
	var codes []RedeemCode
	q.Order("membership_redeem_codes.id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&codes)
	plans := b.planBriefs()
	items := make([]giftView, 0, len(codes))
	for _, rc := range codes {
		var batch RedeemBatch
		db.First(&batch, rc.BatchID)
		v := giftView{ID: rc.ID, Code: formatCode(rc.Code), Days: batch.Days, OrderNo: batch.OrderNo, Status: "unused", CreatedAt: rc.CreatedAt}
		if p, ok := plans[batch.PlanID]; ok {
			v.PlanName, _ = p["name"].(string)
		}
		var use RedeemUse
		switch {
		case db.Where("code_id = ?", rc.ID).First(&use).Error == nil:
			v.Status, v.RedeemedAt, v.RedeemedByMe = "redeemed", &use.CreatedAt, use.UserID == u.ID
		case rc.Disabled || batch.Status != "active":
			v.Status, v.Code = "void", ""
		}
		items = append(items, v)
	}
	b.core.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

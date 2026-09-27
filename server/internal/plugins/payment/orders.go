package payment

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"knowforge/server/internal/jobqueue"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

const (
	onlineOrderTTL = 2 * time.Hour   // 在线支付订单有效期（Stripe 要求 ≥30 分钟）
	syncInterval   = 5 * time.Second // 轮询时向渠道主动查询的最小间隔
)

type behavior struct{ core plugincore.Core }

// newOrderNo 订单号：北京时间 年月日时分秒 + 8 位随机数字（22 位，满足各渠道 out_trade_no 的长度/字符要求）。
func newOrderNo() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(100_000_000))
	return fmt.Sprintf("%s%08d", time.Now().In(chinaTime).Format("20060102150405"), n.Int64())
}

// createOrder 解析商品、快照下单并向渠道发起支付。
func (b *behavior) createOrder(ctx context.Context, u *models.User, kind, sku, channelKey, coupon string, mobile bool, baseURL string) (*Order, action, error) {
	provider, found := plugincore.ProductProviderFor(kind)
	if !found {
		return nil, action{}, errors.New("商品不存在")
	}
	product, err := provider.Resolve(b.core, u, sku)
	if err != nil {
		return nil, action{}, err
	}
	if product.AmountCents <= 0 {
		return nil, action{}, errors.New("商品价格无效")
	}
	cfg := loadConfig(b.core)
	ch, found := channelFor(channelKey)
	if !found || !ch.Available(cfg, product.Currency) {
		return nil, action{}, errors.New("该支付方式不可用")
	}
	db := b.core.Gorm()
	var pending int64
	db.Model(&Order{}).Where("user_id = ? AND status = ? AND expires_at > ?", u.ID, StatusPending, time.Now()).Count(&pending)
	if pending >= maxPendingOrders {
		return nil, action{}, errors.New("待支付订单过多，请先完成或取消已有订单")
	}
	payload, _ := json.Marshal(product.Payload)
	ttl := onlineOrderTTL
	if channelKey == chOffline {
		ttl = time.Duration(cfg.OfflineExpireHours) * time.Hour
	}
	o := &Order{
		OrderNo: newOrderNo(), UserID: u.ID, Kind: product.Kind, SKU: product.SKU, Title: product.Title, DurationDays: product.DurationDays,
		AmountCents: product.AmountCents, OriginalCents: product.AmountCents, Currency: strings.ToUpper(product.Currency), ReturnLink: product.ReturnLink,
		Payload: string(payload), Channel: channelKey, Status: StatusPending, ExpiresAt: time.Now().Add(ttl),
	}
	if coupon = strings.TrimSpace(coupon); coupon != "" {
		dp, found := plugincore.DiscountProviderFor(b.core, product.Kind)
		if !found {
			return nil, action{}, errors.New("该商品不能使用优惠码")
		}
		d, err := dp.Reserve(b.core, u, product, coupon, o.OrderNo)
		if err != nil {
			return nil, action{}, err
		}
		o.DiscountKey = dp.Key
		if d.AmountOffCents <= 0 || d.AmountOffCents >= product.AmountCents {
			b.releaseDiscount(o)
			return nil, action{}, errors.New("优惠金额无效")
		}
		o.CouponCode, o.DiscountCents, o.AmountCents = d.Code, d.AmountOffCents, product.AmountCents-d.AmountOffCents
	}
	if err := db.Create(o).Error; err != nil {
		b.releaseDiscount(o)
		return nil, action{}, err
	}
	ref, act, err := ch.Create(ctx, createInput{Order: o, Cfg: cfg, BaseURL: baseURL, Mobile: mobile})
	if err != nil {
		db.Model(o).Updates(map[string]any{"status": StatusCancelled, "fulfill_error": truncateRunes("发起支付失败: "+err.Error(), 500)})
		b.releaseDiscount(o)
		return nil, action{}, err
	}
	if ref != "" {
		o.ChannelRef = ref
		db.Model(o).Update("channel_ref", ref)
	}
	return o, act, nil
}

// markPaid 渠道确认支付成功（或管理员确认线下到账）：校验金额/货币/渠道后原子地置为已支付，并履约。
// 返回的 changed 表示本次调用完成了状态变更（重复通知返回 false）。
func (b *behavior) markPaid(res *paidResult, channelKey string, confirmedBy uint) (bool, error) {
	db := b.core.Gorm()
	var o Order
	if db.Where("order_no = ?", res.OrderNo).First(&o).Error != nil {
		return false, fmt.Errorf("订单不存在：%s", res.OrderNo)
	}
	if o.Channel != channelKey {
		return false, fmt.Errorf("订单 %s 的支付方式不匹配（%s ≠ %s）", o.OrderNo, channelKey, o.Channel)
	}
	if res.AmountCents != o.AmountCents || !strings.EqualFold(res.Currency, o.Currency) {
		return false, fmt.Errorf("订单 %s 金额不匹配（%d %s ≠ %d %s）", o.OrderNo, res.AmountCents, res.Currency, o.AmountCents, o.Currency)
	}
	now := time.Now()
	// 已支付或已退款的订单不再变更（避免渠道重发的支付通知把已退款订单重新置为已支付并再次履约）
	result := db.Model(&Order{}).Where("id = ? AND status NOT IN ?", o.ID, []string{StatusPaid, StatusRefunded}).Updates(map[string]any{
		"status": StatusPaid, "paid_at": now, "channel_trade_no": truncateRunes(res.TradeNo, 128), "confirmed_by": confirmedBy,
	})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 {
		return false, nil
	}
	o.Status, o.PaidAt = StatusPaid, &now
	if dp, found := plugincore.DiscountProviderByKey(o.DiscountKey); found && o.DiscountKey != "" {
		dp.Confirm(b.core, o.OrderNo)
	}
	b.fulfill(&o)
	link := o.ReturnLink
	if link == "" {
		link = "/pay/orders/" + o.OrderNo
	}
	b.core.NotifyI18n(o.UserID, notificationType, "notify.payment.paid", map[string]string{"title": o.Title}, map[string]any{"link": link, "order_no": o.OrderNo})
	return true, nil
}

// releaseDiscount 订单未支付即结束：归还占用的优惠码次数（提供者按订单号幂等）。
func (b *behavior) releaseDiscount(o *Order) {
	if o.DiscountKey == "" {
		return
	}
	if dp, found := plugincore.DiscountProviderByKey(o.DiscountKey); found {
		dp.Release(b.core, o.OrderNo)
	}
}

// fulfill 回调商品提供者履约（提供者按订单号幂等）；失败记录错误，由巡检或管理员重试。
func (b *behavior) fulfill(o *Order) {
	db := b.core.Gorm()
	provider, found := plugincore.ProductProviderFor(o.Kind)
	var err error
	if !found {
		err = fmt.Errorf("商品类型 %s 未登记（对应插件未加载）", o.Kind)
	} else {
		payload := map[string]any{}
		_ = json.Unmarshal([]byte(o.Payload), &payload)
		err = provider.Fulfill(b.core, o.UserID, o.OrderNo, payload)
	}
	if err != nil {
		db.Model(&Order{}).Where("id = ?", o.ID).Update("fulfill_error", truncateRunes(err.Error(), 500))
		log.Printf("[payment] 订单 %s 履约失败: %v", o.OrderNo, err)
		return
	}
	now := time.Now()
	o.FulfilledAt, o.FulfillError = &now, ""
	db.Model(&Order{}).Where("id = ?", o.ID).Updates(map[string]any{"fulfilled_at": now, "fulfill_error": ""})
}

// sync 待支付的在线订单：向渠道主动查询（限频），已支付则置为已支付（回调地址不可达时的兜底）。
func (b *behavior) sync(ctx context.Context, o *Order) {
	if o.Status != StatusPending && o.Status != StatusExpired {
		return
	}
	if o.Channel == chOffline || (o.SyncedAt != nil && time.Since(*o.SyncedAt) < syncInterval) {
		return
	}
	ch, found := channelFor(o.Channel)
	if !found {
		return
	}
	now := time.Now()
	b.core.Gorm().Model(&Order{}).Where("id = ?", o.ID).Update("synced_at", now)
	res, err := ch.Query(ctx, loadConfig(b.core), o)
	if err != nil {
		log.Printf("[payment] 查询订单 %s 失败: %v", o.OrderNo, err)
		return
	}
	if res == nil || res.OrderNo != o.OrderNo {
		return
	}
	if _, err := b.markPaid(res, o.Channel, 0); err != nil {
		log.Printf("[payment] 订单 %s 标记支付失败: %v", o.OrderNo, err)
		return
	}
	b.core.Gorm().First(o, o.ID)
}

// sweep 周期巡检：过期待支付订单；重试已支付但履约失败的订单；查询受理中的退款、重试退款后的商品回调。
func sweep(core plugincore.Core, _ *jobqueue.Queue) {
	if !core.Gorm().Migrator().HasTable(&Order{}) {
		return
	}
	b := &behavior{core: core}
	db := core.Gorm()
	var expiring []Order
	db.Where("status = ? AND expires_at < ?", StatusPending, time.Now()).Find(&expiring)
	for i := range expiring {
		if db.Model(&Order{}).Where("id = ? AND status = ?", expiring[i].ID, StatusPending).Update("status", StatusExpired).RowsAffected == 1 {
			b.releaseDiscount(&expiring[i])
		}
	}
	var failed []Order
	db.Where("status = ? AND fulfilled_at IS NULL", StatusPaid).Limit(50).Find(&failed)
	for i := range failed {
		b.fulfill(&failed[i])
	}
	b.sweepRefunds(context.Background())
}

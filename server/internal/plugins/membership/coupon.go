package membership

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 优惠券：管理员创建优惠码（折扣或满减），用户在线购买会员（及可选的礼品卡）时于结算页输入，经 plugincore 的优惠扩展点由支付插件调用。
// 可限定适用方案、仅限首次开通会员的用户、总次数与每人次数、生效与截止时间。下单时占用次数，支付后确认，订单取消/过期时归还。

const (
	discountKey        = "membership_coupon"
	couponPercent      = "percent" // 折扣：PercentOff% off
	couponAmount       = "amount"  // 满减：满 MinAmountCents 减 AmountOffCents
	couponUseReserved  = "reserved"
	couponUseUsed      = "used"
	couponUseReleased  = "released"
	maxCouponCodeRunes = 40
)

// Coupon 一个优惠码。
type Coupon struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	Code           string     `gorm:"size:40;uniqueIndex" json:"code"` // 规范化：大写、去掉空白与连字符
	Name           string     `gorm:"size:120" json:"name"`
	Type           string     `gorm:"size:10" json:"type"` // percent | amount
	PercentOff     int        `json:"percent_off"`         // 1–99
	AmountOffCents int64      `json:"amount_off_cents"`
	MinAmountCents int64      `json:"min_amount_cents"` // 最低消费（满减须大于优惠金额）
	Currency       string     `gorm:"size:3" json:"currency"`
	PlanIDs        []uint     `gorm:"type:text;serializer:json" json:"plan_ids"` // 空为全部方案
	IncludeGifts   bool       `json:"include_gifts"`                             // 购买礼品卡也可使用
	NewMembersOnly bool       `json:"new_members_only"`                          // 仅限从未开通过会员的用户
	MaxUses        int        `json:"max_uses"`                                  // 总次数（0 为不限）
	PerUserLimit   int        `json:"per_user_limit"`                            // 每人次数（0 为不限）
	StartsAt       *time.Time `json:"starts_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Status         string     `gorm:"size:10;index" json:"status"` // active | disabled
	CreatedBy      uint       `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (Coupon) TableName() string { return "membership_coupons" }

// CouponUse 一次使用（按订单号唯一）：下单时 reserved，支付后 used，订单未支付即结束时 released。
type CouponUse struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	CouponID      uint      `gorm:"index" json:"coupon_id"`
	UserID        uint      `gorm:"index" json:"user_id"`
	OrderNo       string    `gorm:"size:40;uniqueIndex" json:"order_no"`
	DiscountCents int64     `json:"discount_cents"`
	Status        string    `gorm:"size:10;index" json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (CouponUse) TableName() string { return "membership_coupon_uses" }

func init() {
	plugincore.RegisterDiscountProvider(plugincore.DiscountProvider{Key: discountKey, Applies: couponApplies, Quote: quoteCoupon,
		Reserve: reserveCoupon, Confirm: confirmCoupon, Release: releaseCoupon})
}

// couponApplies 会员与礼品卡商品在有启用中的优惠券时显示优惠码输入框。
func couponApplies(core plugincore.Core, kind string) bool {
	if (kind != productKind && kind != giftProductKind) || !core.PluginEnabled(plugins.KeyMembership) {
		return false
	}
	q := core.Gorm().Model(&Coupon{}).Where("status = ?", "active")
	if kind == giftProductKind {
		q = q.Where("include_gifts = ?", true)
	}
	var n int64
	q.Count(&n)
	return n > 0
}

var (
	errCouponInvalid  = errors.New("优惠码无效")
	errCouponNotStart = errors.New("优惠码尚未生效")
	errCouponExpired  = errors.New("优惠码已过期")
	errCouponUsedUp   = errors.New("优惠码已被领完")
	errCouponUserUsed = errors.New("你已使用过该优惠码")
	errCouponNewOnly  = errors.New("该优惠码仅限首次开通会员的用户使用")
	errCouponScope    = errors.New("该优惠码不适用于此商品")
)

// couponFor 校验优惠码对 u 购买 product 是否可用并计算优惠（tx 内调用；不含次数校验）。
func couponFor(tx *gorm.DB, u *models.User, product plugincore.Product, raw string, now time.Time, lock bool) (Coupon, int64, error) {
	var cp Coupon
	code := normalizeCode(raw)
	if code == "" || len([]rune(code)) > maxCouponCodeRunes {
		return cp, 0, errCouponInvalid
	}
	q := tx
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if q.Where("code = ?", code).First(&cp).Error != nil || cp.Status != "active" {
		return cp, 0, errCouponInvalid
	}
	switch {
	case cp.StartsAt != nil && now.Before(*cp.StartsAt):
		return cp, 0, errCouponNotStart
	case cp.ExpiresAt != nil && now.After(*cp.ExpiresAt):
		return cp, 0, errCouponExpired
	case product.Kind == giftProductKind && !cp.IncludeGifts, product.Kind != productKind && product.Kind != giftProductKind:
		return cp, 0, errCouponScope
	}
	if len(cp.PlanIDs) > 0 {
		planID := uint(payloadInt(product.Payload["plan_id"]))
		found := false
		for _, id := range cp.PlanIDs {
			found = found || id == planID
		}
		if !found {
			return cp, 0, errCouponScope
		}
	}
	if cp.NewMembersOnly && product.Kind == productKind {
		var n int64
		tx.Model(&Record{}).Where("user_id = ? AND source <> ?", u.ID, sourceTrial).Count(&n) // 试用不算开通过
		if n > 0 {
			return cp, 0, errCouponNewOnly
		}
	}
	if product.AmountCents < cp.MinAmountCents {
		return cp, 0, fmt.Errorf("该优惠码需满 %.2f %s 才能使用", float64(cp.MinAmountCents)/100, cp.Currency)
	}
	var off int64
	if cp.Type == couponPercent {
		off = int64(math.Round(float64(product.AmountCents) * float64(cp.PercentOff) / 100))
	} else {
		if !strings.EqualFold(cp.Currency, product.Currency) {
			return cp, 0, errCouponScope
		}
		off = cp.AmountOffCents
	}
	if off <= 0 {
		return cp, 0, errCouponScope
	}
	if off >= product.AmountCents {
		off = product.AmountCents - 1 // 优惠后至少支付最小货币单位
	}
	return cp, off, nil
}

// checkCouponLimits 总次数与每人次数（按已占用与已使用计）。
func checkCouponLimits(tx *gorm.DB, cp Coupon, userID uint) error {
	active := []string{couponUseReserved, couponUseUsed}
	if cp.MaxUses > 0 {
		var n int64
		tx.Model(&CouponUse{}).Where("coupon_id = ? AND status IN ?", cp.ID, active).Count(&n)
		if n >= int64(cp.MaxUses) {
			return errCouponUsedUp
		}
	}
	if cp.PerUserLimit > 0 {
		var n int64
		tx.Model(&CouponUse{}).Where("coupon_id = ? AND user_id = ? AND status IN ?", cp.ID, userID, active).Count(&n)
		if n >= int64(cp.PerUserLimit) {
			return errCouponUserUsed
		}
	}
	return nil
}

func couponDiscount(cp Coupon, off int64) plugincore.Discount {
	return plugincore.Discount{Code: cp.Code, Label: cp.Name, AmountOffCents: off}
}

func quoteCoupon(core plugincore.Core, u *models.User, product plugincore.Product, code string) (plugincore.Discount, error) {
	db := core.Gorm()
	cp, off, err := couponFor(db, u, product, code, time.Now(), false)
	if err == nil {
		err = checkCouponLimits(db, cp, u.ID)
	}
	if err != nil {
		return plugincore.Discount{}, err
	}
	return couponDiscount(cp, off), nil
}

// reserveCoupon 在事务内锁住优惠码、校验次数并占用一次（按订单号幂等）。
func reserveCoupon(core plugincore.Core, u *models.User, product plugincore.Product, code, orderNo string) (plugincore.Discount, error) {
	var d plugincore.Discount
	err := core.Gorm().Transaction(func(tx *gorm.DB) error {
		var existing CouponUse
		if tx.Where("order_no = ?", orderNo).First(&existing).Error == nil {
			var cp Coupon
			tx.First(&cp, existing.CouponID)
			d = couponDiscount(cp, existing.DiscountCents)
			return nil
		}
		cp, off, err := couponFor(tx, u, product, code, time.Now(), true)
		if err != nil {
			return err
		}
		if err := checkCouponLimits(tx, cp, u.ID); err != nil {
			return err
		}
		d = couponDiscount(cp, off)
		return tx.Create(&CouponUse{CouponID: cp.ID, UserID: u.ID, OrderNo: orderNo, DiscountCents: off, Status: couponUseReserved}).Error
	})
	return d, err
}

func confirmCoupon(core plugincore.Core, orderNo string) {
	core.Gorm().Model(&CouponUse{}).Where("order_no = ?", orderNo).Update("status", couponUseUsed)
}

func releaseCoupon(core plugincore.Core, orderNo string) {
	core.Gorm().Model(&CouponUse{}).Where("order_no = ? AND status = ?", orderNo, couponUseReserved).Update("status", couponUseReleased)
}

// —— 管理员 ——

type couponView struct {
	Coupon
	Used     int64 `json:"used"`
	Reserved int64 `json:"reserved"`
}

func (b *behavior) couponViews(rows []Coupon) []couponView {
	db := b.core.Gorm()
	out := make([]couponView, 0, len(rows))
	for _, r := range rows {
		v := couponView{Coupon: r}
		db.Model(&CouponUse{}).Where("coupon_id = ? AND status = ?", r.ID, couponUseUsed).Count(&v.Used)
		db.Model(&CouponUse{}).Where("coupon_id = ? AND status = ?", r.ID, couponUseReserved).Count(&v.Reserved)
		out = append(out, v)
	}
	return out
}

// AdminListCoupons GET /admin/membership/coupons?page=&page_size=
func (b *behavior) AdminListCoupons(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&Coupon{})
	var total int64
	q.Count(&total)
	var rows []Coupon
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	b.core.OK(c, gin.H{"items": b.couponViews(rows), "total": total, "page": page, "page_size": pageSize, "currency": b.currency()})
}

// AdminCreateCoupon POST /admin/membership/coupons 新建优惠码。
func (b *behavior) AdminCreateCoupon(c *gin.Context) {
	var req struct {
		Code           string     `json:"code"`
		Name           string     `json:"name"`
		Type           string     `json:"type"`
		PercentOff     int        `json:"percent_off"`
		AmountOffCents int64      `json:"amount_off_cents"`
		MinAmountCents int64      `json:"min_amount_cents"`
		PlanIDs        []uint     `json:"plan_ids"`
		IncludeGifts   bool       `json:"include_gifts"`
		NewMembersOnly bool       `json:"new_members_only"`
		MaxUses        int        `json:"max_uses"`
		PerUserLimit   *int       `json:"per_user_limit"`
		StartsAt       *time.Time `json:"starts_at"`
		ExpiresAt      *time.Time `json:"expires_at"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	fail := func(msg string) { b.core.Fail(c, http.StatusBadRequest, msg) }
	cp := Coupon{Name: truncate(strings.TrimSpace(req.Name), 120), Type: req.Type, MinAmountCents: req.MinAmountCents, Currency: b.currency(),
		IncludeGifts: req.IncludeGifts, NewMembersOnly: req.NewMembersOnly, MaxUses: req.MaxUses, PerUserLimit: 1,
		StartsAt: req.StartsAt, ExpiresAt: req.ExpiresAt, Status: "active", CreatedBy: b.core.CurrentUser(c).ID}
	if req.PerUserLimit != nil {
		cp.PerUserLimit = *req.PerUserLimit
	}
	switch {
	case cp.Name == "":
		fail("请填写优惠券名称")
		return
	case req.MinAmountCents < 0 || req.MinAmountCents > maxPriceCents:
		fail("最低消费金额无效")
		return
	case req.MaxUses < 0 || req.MaxUses > maxCodeUses || cp.PerUserLimit < 0 || cp.PerUserLimit > 1000:
		fail("使用次数无效")
		return
	case req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()):
		fail("截止时间需晚于现在")
		return
	case req.StartsAt != nil && req.ExpiresAt != nil && !req.ExpiresAt.After(*req.StartsAt):
		fail("截止时间需晚于生效时间")
		return
	}
	switch req.Type {
	case couponPercent:
		if req.PercentOff < 1 || req.PercentOff > 99 {
			fail("折扣需在 1% 到 99% 之间")
			return
		}
		cp.PercentOff = req.PercentOff
	case couponAmount:
		if req.AmountOffCents <= 0 || req.AmountOffCents > maxPriceCents {
			fail("优惠金额无效")
			return
		}
		if req.MinAmountCents <= req.AmountOffCents {
			fail("满减的最低消费需大于优惠金额")
			return
		}
		cp.AmountOffCents = req.AmountOffCents
	default:
		fail("参数错误")
		return
	}
	for _, id := range req.PlanIDs {
		var plan Plan
		if b.core.Gorm().First(&plan, id).Error != nil {
			fail(errPlanNotFound.Error())
			return
		}
		cp.PlanIDs = append(cp.PlanIDs, id)
	}
	code := normalizeCode(req.Code)
	if code == "" {
		random, err := randomCode()
		if err != nil {
			b.core.Fail(c, http.StatusInternalServerError, err.Error())
			return
		}
		code = random[:10]
	} else if len(code) < 4 || len(code) > maxCouponCodeRunes || strings.IndexFunc(code, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') }) >= 0 {
		fail("优惠码需为 4 到 40 位字母、数字或下划线")
		return
	}
	cp.Code = code
	var exists int64
	b.core.Gorm().Model(&Coupon{}).Where("code = ?", code).Count(&exists)
	if exists > 0 {
		fail("该优惠码已存在")
		return
	}
	if err := b.core.Gorm().Create(&cp).Error; err != nil {
		fail(err.Error())
		return
	}
	b.core.RecordAudit(c, "membership.coupon_created", "membership", auditID(cp.ID), cp.Name,
		map[string]any{"changed_fields": []string{"coupon"}, "code": cp.Code, "type": cp.Type})
	b.core.OK(c, b.couponViews([]Coupon{cp})[0])
}

// AdminUpdateCoupon PUT /admin/membership/coupons/:id {status?, max_uses?} 停用/启用、调整总次数。
func (b *behavior) AdminUpdateCoupon(c *gin.Context) {
	var req struct {
		Status  *string `json:"status"`
		MaxUses *int    `json:"max_uses"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Status != nil && *req.Status != "active" && *req.Status != "disabled") ||
		(req.MaxUses != nil && (*req.MaxUses < 0 || *req.MaxUses > maxCodeUses)) {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var cp Coupon
	if b.core.Gorm().First(&cp, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "优惠券不存在")
		return
	}
	updates, fields := map[string]any{}, []string{}
	if req.Status != nil {
		updates["status"], cp.Status = *req.Status, *req.Status
		fields = append(fields, "status")
	}
	if req.MaxUses != nil {
		updates["max_uses"], cp.MaxUses = *req.MaxUses, *req.MaxUses
		fields = append(fields, "max_uses")
	}
	if len(updates) > 0 {
		b.core.Gorm().Model(&Coupon{}).Where("id = ?", cp.ID).Updates(updates)
		b.core.RecordAudit(c, "membership.coupon_updated", "membership", auditID(cp.ID), cp.Name, map[string]any{"changed_fields": fields})
	}
	b.core.OK(c, b.couponViews([]Coupon{cp})[0])
}

// AdminCouponUses GET /admin/membership/coupons/:id/uses?page=&page_size= 使用记录（含占用中与已归还）。
func (b *behavior) AdminCouponUses(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&CouponUse{}).Where("coupon_id = ?", c.Param("id"))
	var total int64
	q.Count(&total)
	var uses []CouponUse
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&uses)
	ids := make([]uint, 0, len(uses))
	for _, u := range uses {
		ids = append(ids, u.UserID)
	}
	users := b.userBriefs(ids)
	items := make([]gin.H, 0, len(uses))
	for _, u := range uses {
		items = append(items, gin.H{"use": u, "user": users[u.UserID]})
	}
	b.core.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

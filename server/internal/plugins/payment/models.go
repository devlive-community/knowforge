package payment

import "time"

// 订单状态。
const (
	StatusPending   = "pending"   // 待支付（线下转账：待管理员确认）
	StatusPaid      = "paid"      // 已支付（履约结果见 FulfilledAt / FulfillError）
	StatusCancelled = "cancelled" // 用户或管理员取消
	StatusExpired   = "expired"   // 超时未支付
	StatusRefunded  = "refunded"  // 已全额退款（部分退款的订单仍为 paid，见 RefundedCents）
)

// Order 支付订单：下单时快照商品（标题、金额、履约数据），支付成功后回调商品提供者履约。
// 已取消/已过期的订单若随后收到渠道的支付成功回调，仍按已支付处理（用户确实付了款）。
type Order struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	OrderNo      string `gorm:"size:40;uniqueIndex;not null" json:"order_no"`
	UserID       uint   `gorm:"index;not null" json:"user_id"`
	Kind         string `gorm:"size:40;not null" json:"kind"`
	SKU          string `gorm:"size:64" json:"sku"`
	Title        string `gorm:"size:200" json:"title"`
	DurationDays int    `json:"duration_days"`
	AmountCents  int64  `gorm:"not null" json:"amount_cents"` // 实付金额（优惠后）
	Currency     string `gorm:"size:3;not null" json:"currency"`
	// 优惠：OriginalCents 商品原价，DiscountCents 优惠金额，CouponCode 使用的优惠码，DiscountKey 优惠提供者（支付/取消时回调）
	OriginalCents int64  `gorm:"not null;default:0" json:"original_cents"`
	DiscountCents int64  `gorm:"not null;default:0" json:"discount_cents"`
	CouponCode    string `gorm:"size:64" json:"coupon_code"`
	DiscountKey   string `gorm:"size:40" json:"-"`
	ReturnLink    string `gorm:"size:255" json:"return_link"`
	Payload       string `gorm:"type:text" json:"-"` // 履约快照（JSON）
	Channel       string `gorm:"size:20;index" json:"channel"`
	Status        string `gorm:"size:20;index;not null" json:"status"`
	// ChannelRef 渠道侧的会话/二维码信息（Stripe Checkout Session ID、微信 code_url），ChannelTradeNo 渠道交易号
	ChannelRef     string     `gorm:"size:512" json:"-"`
	ChannelTradeNo string     `gorm:"size:128" json:"channel_trade_no"`
	PayerNote      string     `gorm:"size:500" json:"payer_note"` // 线下转账：用户填写的付款说明
	ProofAt        *time.Time `json:"proof_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	PaidAt         *time.Time `json:"paid_at"`
	ConfirmedBy    uint       `json:"confirmed_by"` // 线下转账：确认收款的管理员
	FulfilledAt    *time.Time `json:"fulfilled_at"`
	FulfillError   string     `gorm:"size:500" json:"fulfill_error"`
	// RefundCommittedCents 已占用的退款额度（申请中/处理中/已成功的退款之和，用于原子地防止超额退款）；RefundedCents 已成功退款
	RefundCommittedCents int64      `gorm:"not null;default:0" json:"-"`
	RefundedCents        int64      `gorm:"not null;default:0" json:"refunded_cents"`
	SyncedAt             *time.Time `json:"-"` // 最近一次向渠道主动查询的时间（轮询限频）
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func (Order) TableName() string { return "payment_orders" }

// 退款状态。
const (
	RefundRequested  = "requested"  // 用户申请，待管理员审核
	RefundRejected   = "rejected"   // 管理员驳回
	RefundPending    = "pending"    // 已批准，正在向渠道发起
	RefundProcessing = "processing" // 渠道受理中（异步到账）
	RefundSucceeded  = "succeeded"  // 已退款
	RefundFailed     = "failed"     // 渠道退款失败
)

// Refund 退款单：用户申请后管理员审核，或管理员直接发起；支持部分退款。
// 退款成功后回调商品提供者冲回财务，并在 Revoke 时撤销已发放的商品（按退款单号幂等，失败由巡检重试）。
type Refund struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	RefundNo        string     `gorm:"size:40;uniqueIndex;not null" json:"refund_no"` // 渠道侧的退款请求号（幂等）
	OrderID         uint       `gorm:"index;not null" json:"order_id"`
	OrderNo         string     `gorm:"size:40;index;not null" json:"order_no"`
	UserID          uint       `gorm:"index;not null" json:"user_id"`
	AmountCents     int64      `gorm:"not null" json:"amount_cents"`
	Currency        string     `gorm:"size:3;not null" json:"currency"`
	Channel         string     `gorm:"size:20" json:"channel"`
	Reason          string     `gorm:"size:500" json:"reason"`     // 用户申请理由或管理员填写的原因
	AdminNote       string     `gorm:"size:500" json:"admin_note"` // 审核意见 / 驳回理由
	Revoke          bool       `json:"revoke"`
	Status          string     `gorm:"size:20;index;not null" json:"status"`
	ChannelRefundID string     `gorm:"size:128" json:"channel_refund_id"`
	Error           string     `gorm:"size:500" json:"error"`
	RequestedBy     uint       `json:"requested_by"` // 发起人（用户或管理员）
	ReviewedBy      uint       `json:"reviewed_by"`
	SucceededAt     *time.Time `json:"succeeded_at"`
	SettledAt       *time.Time `json:"settled_at"` // 已回调商品提供者（冲回财务/撤销商品）
	SettleError     string     `gorm:"size:500" json:"settle_error"`
	SyncedAt        *time.Time `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (Refund) TableName() string { return "payment_refunds" }

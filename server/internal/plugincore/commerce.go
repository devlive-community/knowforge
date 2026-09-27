package plugincore

import (
	"sort"

	"knowforge/server/internal/models"
)

// —— 商品与履约：支付类插件售卖由其他插件登记的「商品」，支付成功后回调商品提供者履约。——
//
// 支付插件与商品提供者（如会员）互不依赖，只经此中性扩展点协作：
//   - 下单时支付插件调用 Resolve 取得商品（价格、标题）并把 Payload 快照进订单；
//   - 支付成功后支付插件以订单号调用 Fulfill（须按订单号幂等），提供者据快照发放（如开通会员）；
//   - 退款成功后支付插件调用 Refund（须按退款单号幂等），提供者冲回相关财务（如作者收益），并在 Revoke 时撤销已发放的商品。

// CheckoutEnabledKey 公开站点配置（GET /site）中「可在线购买」的中性开关：由提供结算能力的插件（如支付）
// 经 RegisterPublicSiteConfig 下发；商品所属插件的前端只看此键，不感知具体由哪个插件提供结算。
const CheckoutEnabledKey = "checkout_enabled"

// Product 一件可售商品（由商品提供者按 SKU 解析）。
type Product struct {
	Kind        string `json:"kind"`
	SKU         string `json:"sku"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// DurationDays 限时商品的时长（如会员 30 天），0 表示不适用；前端据此展示「1 个月」等
	DurationDays int    `json:"duration_days,omitempty"`
	AmountCents  int64  `json:"amount_cents"` // 金额（最小货币单位，如分）
	Currency     string `json:"currency"`     // ISO 4217
	// ReturnLink 支付完成后引导用户前往的站内链接（如 /user/membership）
	ReturnLink string `json:"return_link,omitempty"`
	// Payload 下单时的履约快照（如方案、天数），支付成功后原样交给 Fulfill，避免之后的改价/改方案影响已下单订单
	Payload map[string]any `json:"-"`
}

// RefundEvent 一笔退款成功（部分或全额）。
type RefundEvent struct {
	UserID      uint
	OrderNo     string
	RefundNo    string // 退款单号（幂等键）
	AmountCents int64  // 本次退款金额
	TotalCents  int64  // 订单金额
	Currency    string
	// Revoke 是否撤销已发放的商品（如扣回会员时长、收回已解锁内容）；由管理员在退款时决定
	Revoke  bool
	Payload map[string]any // 下单时的履约快照
}

// ProductProvider 商品提供者。
type ProductProvider struct {
	Kind string
	// Resolve 返回用户当前可购买的商品；不可购买时返回可展示的错误。
	Resolve func(core Core, u *models.User, sku string) (Product, error)
	// Fulfill 订单支付成功后履约；须按 orderNo 幂等（重复调用不重复发放）。
	Fulfill func(core Core, userID uint, orderNo string, payload map[string]any) error
	// Refund 退款成功后回调（可选）；须按 RefundNo 幂等。未提供时退款只退钱、不撤销商品。
	Refund func(core Core, ev RefundEvent) error
}

var productProviders = map[string]ProductProvider{}

// RegisterProductProvider 登记商品提供者（Kind 唯一）。
func RegisterProductProvider(p ProductProvider) { productProviders[p.Kind] = p }

// ProductProviderFor 按商品类型查提供者。
func ProductProviderFor(kind string) (ProductProvider, bool) {
	p, ok := productProviders[kind]
	return p, ok
}

// ProductKinds 已登记的商品类型（排序）。
func ProductKinds() []string {
	out := make([]string, 0, len(productProviders))
	for k := range productProviders {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// —— 优惠：结算时由优惠提供者（如会员插件的优惠券）为商品计算折扣。——
//
// 支付插件只经此扩展点询问：结算页输入优惠码时调用 Quote 试算；下单时调用 Reserve 占用一次使用次数（按订单号），
// 订单支付成功后调用 Confirm 确认使用，订单取消/过期/发起支付失败时调用 Release 归还（均须按订单号幂等）。
// 优惠后金额须大于 0（提供者保证折扣小于商品金额）。

// Discount 一次优惠的结果。
type Discount struct {
	Code           string `json:"code"`             // 规范化后的优惠码
	Label          string `json:"label"`            // 展示名称（如「新用户 8 折」）
	AmountOffCents int64  `json:"amount_off_cents"` // 优惠金额（小于商品金额）
}

// DiscountProvider 优惠提供者。
type DiscountProvider struct {
	Key string
	// Applies 该类商品是否可能使用优惠（结算页据此决定是否显示优惠码输入框）。
	Applies func(core Core, kind string) bool
	// Quote 试算：返回 u 对 product 使用 code 的优惠；不可用时返回可展示的错误。
	Quote func(core Core, u *models.User, product Product, code string) (Discount, error)
	// Reserve 下单时占用一次使用次数（按 orderNo 幂等），返回的优惠以此为准。
	Reserve func(core Core, u *models.User, product Product, code, orderNo string) (Discount, error)
	// Confirm 订单支付成功：确认使用（此前已归还的也重新计为已使用，因为用户确实按优惠价付了款）。
	Confirm func(core Core, orderNo string)
	// Release 订单未支付即结束：归还占用的次数。
	Release func(core Core, orderNo string)
}

var discountProviders []DiscountProvider

// RegisterDiscountProvider 登记优惠提供者。
func RegisterDiscountProvider(p DiscountProvider) { discountProviders = append(discountProviders, p) }

// DiscountProviderFor 可用于该类商品的优惠提供者（第一个适用的）。
func DiscountProviderFor(core Core, kind string) (DiscountProvider, bool) {
	for _, p := range discountProviders {
		if p.Applies != nil && p.Applies(core, kind) {
			return p, true
		}
	}
	return DiscountProvider{}, false
}

// DiscountProviderByKey 按键查优惠提供者（订单记录了使用的提供者，支付/取消时据此回调）。
func DiscountProviderByKey(key string) (DiscountProvider, bool) {
	for _, p := range discountProviders {
		if p.Key == key {
			return p, true
		}
	}
	return DiscountProvider{}, false
}
